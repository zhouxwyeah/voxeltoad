# ADR-0039: LLM trace payload capture — message & raw layer

- Status: Accepted
- Date: 2026-07-10
- Builds on ADR-0021 (request_logs ledger — the body-level audit it explicitly
  defers), ADR-0016 (fail-open async recording), ADR-0017/0019 (RBAC + read-only
  query patterns), ADR-0027 (PII redaction plugin), and `design/observability.md`.

## Context

The gateway's local observability story is currently two layers deep:

```
Session  (request_logs.session_id aggregation)
  └─ Request  (request_logs row: metadata only — token counts, timings, error)
```

Operators can see *which* requests happened in a session and *how much* they
cost, but cannot see *what was said*. When an agent loop goes wrong, an upstream
returns a malformed body, or a tool call misfires, there is no local way to
reproduce the exact prompt/completion pair — the only bodies ever held
(`UnifiedResponse.Raw` / `Chunk.Raw`) are `json:"-"` passthrough buffers,
discarded with the request.

Reference systems like LangSmith / Phoenix / `trace.databend.cloud` model LLM
tracing as four layers:

```
Session → Request → Messages → Raw
```

The bottom two layers — the normalized message array and the raw upstream
request/response bodies — are exactly what the gateway refuses to store today.
ADR-0021 §2 and `design/observability.md:67` deliberately keep `request_logs`
free of prompt/completion bodies, and point body-level audit to *"an explicit
audit plugin and retention policy, not in this core ledger."* That plugin/policy
does not exist yet. This ADR defines it.

## Decision

### 1. A separate `trace_payloads` table — never extend `request_logs`

Introduce `trace_payloads` as a second, independent data-plane ledger dedicated
to the message and raw layers. It is **not** `request_logs` and never shares its
rows. The two ledgers are joined 1:1 on `request_id` at read time.

Reasons mirroring why ADR-0021 separated `request_logs` from `usage_records` and
`audit_logs`: different sensitivity, different volume, different retention, and
different access policy.

- `request_logs` stays a compact metadata ledger (ADR-0021 unchanged).
- `trace_payloads` carries large, sensitive bodies behind its own controls.

Rejected: adding `messages`/`request_body`/`response_body` JSONB columns to
`request_logs`. That would violate ADR-0021 §2's explicit prohibition, bloat the
metadata ledger's partitions, and couple two retention policies. ADR-0021
specifically reserves this for a separate ledger — this is it.

### 2. Capture full plaintext, by explicit config opt-in

When `trace.capture_payload.enabled` is true, the data plane captures, for every
request it can, the four-layer payload:

- `messages` — the normalized message array the gateway routed on (`adapter.
  Message[]`), giving the message layer a provider-agnostic shape.
- `request_raw` — the original client request body (the same `rawBody` already
  read once by the handler).
- `response_raw` — the upstream response body: for non-streaming, `resp.Raw`
  (already preserved byte-for-byte, ADR-0032); for streaming, the reassembled
  full SSE transcript.
- `error_raw` — the upstream error body surfaced on failure (currently only sent
  to the client and lost).

> **Update (2026-07-11):** the capture on/off switch, body cap, and retention
> window moved from the bootstrap `trace.capture_payload` YAML into a
> hot-reloadable `gateway_settings` document (managed via the admin UI Settings
> page / `PUT /api/v1/gateway-settings`, stored in the `gateway_settings` table,
> and applied per-request via `Dynamic.Settings`). The recorder is now always
> built and started; whether capture happens is gated per-request by
> `gateway_settings.trace.capture_payload_enabled`, so flipping it takes effect
> within one config poll (~5s) with no gateway restart. The bootstrap YAML now
> only carries the non-hot-reloadable recorder `buffer` size and a retention
> fallback default. References to `trace.capture_payload.enabled` below refer to
> the original (now-superseded) bootstrap gate.

Plaintext capture is the team's stated choice (full-fidelity debugging). The
knob defaults to **off** so the gateway keeps its privacy-preserving default
until a deployment explicitly opts in, and can be turned off instantly for
emergency/gray situations. PII redaction (ADR-0027) can be layered on the
captured text later without schema change.

### 3. Fail-open async write, off the request path

`trace_payloads` is written by a new `AsyncTracePayloadRecorder` that mirrors
`AsyncRequestLogRecorder` (ADR-0016): a bounded channel + worker goroutine;
`Record` never blocks; buffer-full / sink-error rows are dropped and counted.
A dropped trace row is acceptable — trace payloads are debugging state, never
the money path, and never worth adding latency to a user request.

Rejected: synchronous INSERT. Body payloads are far larger than `request_logs`
rows; a PG slowdown must never become user-visible data-plane latency.

### 4. Storage: PostgreSQL JSONB + RANGE partition + partition-DROP retention

Use the existing PostgreSQL, not a new columnar/object store:

- `messages` / `request_raw` are `JSONB` — flexible across heterogeneous
  provider schemas (OpenAI / Claude / domestic vendors differ widely), and PG's
  TOAST compression handles the size for short retention.
- `response_raw` is `TEXT` — it is the verbatim upstream response body, which
  for streaming requests is the reassembled SSE transcript and therefore not
  JSON. This mirrors `error_raw`, which is already `TEXT` for the same reason.
- `PARTITION BY RANGE (created_at)` with a default partition, mirroring
  `request_logs`/`usage_records` (ADR-0021 §5) so monthly partitions and
  partition-DROP TTL can be added without a retrofit.
- Short retention (default 7 days, configurable) is enforced by dropping old
  monthly partitions — O(1), no bloat, unlike `DELETE`.

Rationale for staying on PG: this project's principle is "PostgreSQL is the only
stateful dependency." Reference systems use a columnar store (Databend) because
they retain huge cold datasets for heavy analytics. Short retention + a gateway
(non-analytics) workload removes that pressure. The one documented escalation
path — externalizing the largest field (`response_raw`) to object storage — is
preserved by keeping it a separate column, ready to split off if volume demands.

### 5. Read access is more restrictive than `request_logs`

`request_logs` reads are not audited and serve both roles. Because `trace_
payloads` carries prompt/completion **plaintext**, its read API:

- is RBAC-gated and tenant-scoped identically to `/request-logs` (operator →
  tenant resolution, structural isolation — a tenant-admin cannot read another
  tenant's payloads);
- **is audited** — each detail read (`GET .../requests/:request_id`) writes an
  `audit_logs` row, since reading plaintext is a sensitive operator action,
  unlike reading metadata.

### 6. Schema

```sql
CREATE TABLE trace_payloads (
    id              BIGINT GENERATED BY DEFAULT AS IDENTITY,
    request_id      VARCHAR NOT NULL DEFAULT '',
    session_id      VARCHAR NOT NULL DEFAULT '',
    trace_id        VARCHAR NOT NULL DEFAULT '',
    tenant          VARCHAR NOT NULL DEFAULT '',
    group_name      VARCHAR NOT NULL DEFAULT '',
    api_key_id      VARCHAR NOT NULL DEFAULT '',
    provider        VARCHAR NOT NULL DEFAULT '',
    model_requested VARCHAR NOT NULL DEFAULT '',
    stream          BOOLEAN NOT NULL DEFAULT false,
    status_code     INTEGER NOT NULL DEFAULT 0,
    stop_reason     VARCHAR NOT NULL DEFAULT '',
    n_messages      INTEGER NOT NULL DEFAULT 0,
    n_tool_use      INTEGER NOT NULL DEFAULT 0,
    messages        JSONB   NOT NULL DEFAULT '[]',
    request_raw     JSONB   NOT NULL DEFAULT '{}',
    response_raw    TEXT    NOT NULL DEFAULT '',
    error_raw       TEXT    NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
```

Summary columns (`status_code`, `stop_reason`, `n_messages`, `n_tool_use`) let
the "request list" view render a row without decoding the large JSON — they
mirror the dimensions a reference system surfaces per event.

## Consequences

- `request_logs` is untouched; ADR-0021's prohibition is honored.
- A new `trace_payloads` ledger, written fail-open async, read with RBAC +
  tenant scope + read audit, retained short via partition DROP.
- `AsyncTracePayloadRecorder` is assembled in `internal/app` and wired at
  `cmd/gateway`; capture is gated by `trace.capture_payload.enabled` (off by
  default) and short-circuited at the capture sites when disabled.
- Delivered: monthly partitions (migration `00020`) + a daily partition-DROP
  retention job in `cmd/admin` (the §4 design); the `max_body_kb` cap on
  captured bodies; full SSE wire-frame transcripts for streaming `response_raw`;
  the raw upstream error body in `error_raw`.
- Remaining limitations: no PII redaction on captured text (pluggable later via
  ADR-0027); `error_raw` is empty for transport/build/parse errors that carry no
  upstream body.
- New metric: `trace_payloads_dropped_total` joins the existing Prometheus set.

## Related

- ADR-0021: request_logs ledger (the core metadata ledger this ADR extends
  downward into bodies — without modifying).
- ADR-0016: fail-open async recording (the recorder pattern this reuses).
- ADR-0017/0019: RBAC + read-only query APIs (the read-access pattern).
- ADR-0027: PII redaction (layered on captured text in a future step).
- ADR-0032: OpenAI passthrough fidelity (`UnifiedResponse.Raw` / `Chunk.Raw`,
  the byte-for-byte source for the raw layer).

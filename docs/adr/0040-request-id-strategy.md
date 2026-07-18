# ADR-0040: Request ID strategy — client cooperation, non-uniqueness, upstream-id capture and isolation

- Status: Accepted
- Date: 2026-07-15
- Builds on ADR-0021 (request_logs ledger), ADR-0039 (trace_payloads and the
  application-layer 1:1 join on request_id), ADR-0011 (failover semantics that
  produce multiple upstream attempts per gateway request), and
  `design/observability.md` (field-level rules for llm.request_id /
  llm.upstream_request_id / llm.session_id).

## Context

The gateway's request-correlation story spans three distinct IDs that have
accreted across migrations without a single decision record:

- `request_id` — the entry correlation id. Source of truth at
  `request_logs.request_id` + `trace_payloads.request_id` + the OTel span
  attribute `llm.request_id`. Historically client-supplied (00010), later
  clarified to fall back to chi generation.
- `upstream_request_id` — the provider-assigned id returned in the upstream
  response (OpenAI `x-request-id` header, Anthropic `request-id` header/body,
  Gemini `x-goog-request-id`). Added recently (00024) to close the
  support/reconciliation gap.
- `session_id` — the client-supplied session key (`X-Voxeltoad-Session`). Context
  only in this ADR; its semantics belong to routing/affinity (ADR-0018).

The design tensions that shaped the current code — and that this ADR makes
explicit — are:

1. **Client cooperation vs gateway authority** for entry id generation. W3C
   Trace Context / OpenTelemetry / Nginx / Envoy / Kong all favor client
   cooperation; some commercial LLM gateways (Portkey, Helicone) override with
   a gateway-minted id. The gateway chose the former, but the rationale is
   only in code comments.
2. **Uniqueness tolerance vs enforcement**. Clients may legitimately reuse the
   same `X-Request-Id` across requests in a session (or a buggy client may
   hardcode it). The gateway does not enforce uniqueness, which has
   downstream consequences on read paths that were only documented in a single
   `GetByRowID` code comment.
3. **Upstream-id capture vs ledger simplicity**. A gateway request that
   fails over across N candidates produces N upstream HTTP calls, each with
   its own provider-assigned id. Capturing all N would break the "1 gateway
   request = 1 request_logs row" invariant (ADR-0021 §5); capturing only one
   loses the failed attempts' ids.
4. **Echo policy vs security**. Some gateways echo the upstream id back to the
   client for convenience; this leaks internal infrastructure topology.

Each of these was resolved one piece at a time inside
`internal/proxy/requestid_middleware.go`, `internal/proxy/forward.go`, and
`internal/store/tracepayload_query.go`. This ADR consolidates them into a
single decision record so future contributors do not relitigate them.

## Decision

### 1. Client-cooperative entry-id generation

The entry `request_id` follows a client-cooperative resolution chain, in
priority order:

```
X-Request-Id → X-Trace-Id → traceparent → chi middleware auto-generated
```

`normalizeRequestID` (`internal/proxy/requestid.go:22-31`) rejects only the
"nil/zero UUID" family (empty, whitespace, or all-zeros with optional
dashes/spaces) — values that some agent SDKs allocate but never fill. Rejected
values are cleared so chi mints a fresh `host/random-000001` id, restoring the
same handling as if the client had sent no header at all. Everything else
(non-zero) is accepted as-is; the gateway does **not** mandate a specific id
format.

Rejected: a "gateway-authoritative" model where the gateway always mints a
`gateway_request_id` and the client-supplied id is demoted to a separate
`client_request_id` column. Three reasons:

- Client cooperation is the W3C Trace Context / OpenTelemetry / Nginx / Envoy
  / Kong standard. A client being able to thread its own trace id end-to-end
  is a feature, not a bug.
- Demotion would change the semantics of the id clients receive in the
  `X-Request-Id` response header, silently breaking every external integration
  that reads it for correlation.
- The cost (a second column + an indirection on every lookup) has no
  corresponding benefit in this gateway's workload.

### 2. `request_id` is NOT unique — by design

`request_logs.request_id` carries **no UNIQUE constraint and no dedicated
index**. Clients may legitimately send the same `X-Request-Id` for every
request in a session (some agent SDKs do), and a buggy or malicious client
may hardcode it. The gateway does not attempt to prevent this: it does not
reject duplicates, does not append a suffix, does not mint a new id when a
duplicate is detected.

This is a deliberate consequence of Decision 1. Enforcing uniqueness would
require either rejecting legitimate client reuse (breaking the
client-cooperative contract) or silently overriding the client's id (making
the echoed `X-Request-Id` response header a lie).

Rejected:

- **Add UNIQUE constraint** — would reject legitimate client reuse; a buggy
  client hardcoding an id should not cause gateway 500s.
- **Add a dedicated index on `request_id`** — point queries return multiple
  rows whenever the client reuses the id, defeating the point of the index;
  the write cost on every request is not justified by occasional lookups.
  Existing time-scoped list queries use `(session_id, created_at)` or
  `(tenant, created_at)` indexes.

The read-side consequence is captured by Decision 3.

### 3. Consumer-side hard constraint: `GetByRowID` is the preferred single-row lookup

Because `request_id` may be duplicated (Decision 2), any code that fetches a
**single** trace-payload row by request id must use the auto-increment primary
key (`id`), not `request_id`:

- **Preferred**: `TracePayloadQueryRepo.GetByRowID(rowID int64)` —
  `internal/store/tracepayload_query.go:120-145`. Addresses each row uniquely
  via the table's identity primary key.
- **Only when non-duplication is guaranteed**:
  `TracePayloadQueryRepo.GetByRequestID(requestID string)` —
  `internal/store/tracepayload_query.go:104-118`. Uses `LIMIT 1` and therefore
  returns the same row whenever `request_id` is duplicated.

The frontend session detail page already routes through the primary key:
`web/src/app/.../trace/sessions/[session_id]/[req]/fetch-detail.ts:24` calls
`GET /api/v1/trace/rows/{id}` (the `GetByRowID` endpoint), not
`GET /api/v1/trace/requests/{request_id}`.

Rejected: force all lookups through the primary key and drop
`GetByRequestID`. The `request_id`-based lookup is still needed for
support/reconciliation scenarios ("the client sent us this `X-Request-Id`,
which gateway request was it?"), which work correctly when combined with a
time window or session filter.

### 4. Upstream-id isolation — the upstream `req_xxx` is never echoed to the client

`echoCorrelationHeaders` (`internal/proxy/router.go:61-71`) sets exactly three
response headers on every exit path (success, error, streaming):

- `X-Request-Id` — the gateway's entry correlation id (Decision 1).
- `X-Voxeltoad-Session` — the client-supplied session key.
- `X-Trace-Id` — the W3C trace id (when present).

The upstream provider's `req_xxx` (OpenAI internal id, Anthropic request id,
etc.) is **never** forwarded to the external client. It is captured into
`request_logs.upstream_request_id` (Decision 5) for support/reconciliation
use, but stays internal.

Rejected: echo the upstream id back to the client "for convenience." Three
reasons:

- **Security** — upstream internal ids can leak infrastructure topology (e.g.
  OpenAI's `req_` prefix and encoding reveal provider-side sharding). Exposing
  them to external callers is an unnecessary information disclosure.
- **Industry convention** — Nginx, HAProxy, AWS ALB all echo their own
  correlation id and do not forward backend ids to the client.
- **No functionality lost** — the upstream id is already persisted to
  `request_logs.upstream_request_id`. When a client reports an issue, the
  operator can look it up and use it in a provider support ticket.

### 5. Capture only the final/successful attempt's `upstream_request_id`

`request_logs.upstream_request_id` stores the upstream id from the
**final successful** attempt only. When failover (ADR-0011) or retry across
candidates produces N upstream HTTP calls, the failed attempts' upstream ids
are not captured — they are lost as the Forwarder loop exits.

This is a deliberate scope boundary. The alternative — capturing every
attempt's id including failures — is valuable for post-incident forensics, but
it cannot fit the existing ledger model without structural change.

Rejected:

- **Add `attempt_index` + per-attempt upstream id columns to `request_logs`**
  — violates the "1 gateway request = 1 row" invariant that ADR-0021 §5
  establishes and that every read API (list, session-aggregate, CSV export)
  relies on.
- **Build the per-attempt table (`upstream_attempts`) now** — scope is large
  (new table, new recorder plumbing through the dispatcher loop, new read
  API) and the marginal value of failed-attempt ids does not justify an
  independent architectural change in this round.

The per-attempt capture (including failed retries and failover hops) is
explicitly listed as a phase-2 enhancement in
`docs/ops/failover-troubleshooting.md §8` ("逐跳明细缺失"). The 1:1 capture
shipped here covers the primary support/reconciliation case: successful
requests are the ones that incur cost and need provider-side reconciliation.

## Consequences

- The entry id resolution is governed by
  `internal/proxy/requestid.go:22-31` (rejection) and
  `internal/proxy/requestid_middleware.go:50-77` (override + regenerate). No
  new code is required by this ADR — it records decisions already in force.
- `request_logs.request_id` and `trace_payloads.request_id` remain non-unique
  by design. Any consumer that needs a unique row address uses the
  auto-increment primary key (`id`).
- The `trace_id` field exists on both ledgers but the gateway does not emit a
  synthetic `llm.trace_id` OTel span attribute — trace_id is a W3C standard
  carried by the OTel trace context itself, so a separate attribute would be
  redundant. See `docs/glossary.md` §Request tracing for the precise
  definition.
- Upstream-id capture (1:1, final/successful attempt) is live: migration
  `00024_upstream_request_id.sql` + Forwarder header extraction +
  `extractUpstreamID` per-provider dispatch (`internal/proxy/forward.go`).

**Phase-1 limitations / deferred:**

- **Per-attempt upstream-id capture** (including failed retries and failover
  hops) is a phase-2 enhancement tracked in
  `docs/ops/failover-troubleshooting.md §8`. It requires a new
  `upstream_attempts`-style table and plumbing through the dispatcher loop,
  and is not justified by the current support/reconciliation workload alone.

**Closed:**

- `upstream_request_id` 1:1 capture (final/successful attempt) — shipped via
  migration `00024` and the Forwarder header-extraction path. Previously the
  gateway had no provider-side correlation id at all.

## Related

- ADR-0021 — `request_logs` data-plane audit ledger (the primary host for
  `request_id` and `upstream_request_id`; this ADR's Decision 2 and Decision 5
  respect its "1 row per request" invariant).
- ADR-0039 — `trace_payloads` 4-layer trace capture (joins `request_logs` 1:1
  on `request_id` at read time; Decision 3 constrains how that join is
  addressed).
- ADR-0011 — routing strategy and failover semantics (the source of N-upstream-
  call-per-gateway-request that motivates Decision 5's scope boundary).
- `design/observability.md` §request_id 与 session_id 与 upstream_request_id —
  field-level rules and consumer-side hard constraints (the authoritative
  current-rules document).
- `internal/proxy/requestid.go`, `internal/proxy/requestid_middleware.go` —
  entry-id validation and regeneration.
- `internal/proxy/forward.go` — `extractUpstreamID` per-provider header
  dispatch.
- `internal/store/tracepayload_query.go:120-145` — `GetByRowID` (preferred
  single-row lookup per Decision 3).

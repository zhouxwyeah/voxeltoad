# ADR-0050: Client request ID split — gateway always generates, client original preserved separately

- Status: Accepted
- Date: 2026-07-22
- Revises: [ADR-0021](0021-request-logs-data-plane-audit-ledger.md) §5 (request_id semantics)
- Builds on: [ADR-0021](0021-request-logs-data-plane-audit-ledger.md) (request_logs audit ledger), migration [00024_upstream_request_id.sql](../../internal/store/migrations/00024_upstream_request_id.sql) (upstream_request_id)

## Context

`design/database.md` historically described `request_id` as "gateway 分配（或上行透传）" — the gateway accepts a client-supplied `X-Request-Id` header when present (only rejecting the nil-UUID all-zeros family), and otherwise generates its own via chi's `middleware.RequestID`.

Several agent clients — notably **Claude Code**, **Codex**, and some OpenAI SDK configurations — **reuse the same `X-Request-Id` header value across every request in a session**. Under the accept-on-forward policy, every request in such a session lands in `request_logs` with the same `request_id`. This breaks the core assumption that `request_id` is a per-request correlation key:

- The desktop UI's request-logs table shows visibly identical `request_id` values across rows, making it impossible to tell requests apart at a glance.
- Any `LIMIT 1` lookup by `request_id` returns an arbitrary (usually the first inserted) row rather than the intended one — the gateway already carries row-id fallback paths (`GetTraceByRowID`, `/api/v1/trace/rows/:id`) precisely for this case.
- React keys derived from `request_id` collide, causing render warnings and row-misalignment bugs in session timeline components.
- Audit and support workflows that assume one `request_id` ↔ one request break silently.

The design acknowledged this risk in scattered comments (`internal/store/tracepayload_query.go:122`, `internal/admin/trace_handlers.go:89`, migration `00024` header), but mitigated it only with row-id lookup alternatives — leaving the duplicate `request_id` values themselves in the table.

## Decision

### `request_id` becomes gateway-generated only

The gateway **always** generates a fresh, unique `request_id` for every request. Client-supplied `X-Request-Id` / `X-Trace-Id` values are **no longer adopted** as the primary correlation id. The nil-UUID rejection logic is subsumed by the simpler "always clear and regenerate" rule — the detection stays around only for the labeled warning metric.

This revises ADR-0021 §5: `request_id` is now strictly **gateway-assigned**, period. The "(or upstream-forwarded)" clause is removed from `design/database.md`'s field semantics.

### New `client_request_id` field preserves the client original

A new `client_request_id` column on `request_logs` (and `trace_payloads`, for paired reads) stores the raw client-supplied `X-Request-Id` header value verbatim. Empty when the client did not send the header. Together with the existing `upstream_request_id`, every request now carries a three-id tuple:

| Field | Source | Purpose |
|---|---|---|
| `request_id` | Gateway (chi middleware) | Per-request uniqueness; primary key for in-gateway correlation, OTel span, access log |
| `client_request_id` | Client `X-Request-Id` header | Cross-system join with the caller's trace; empty when the client sent nothing |
| `upstream_request_id` | Provider response (`x-request-id`, `request-id`, …) | Support/reconciliation against the vendor's records |

The gateway adds the response header `X-Client-Request-Id: <original>` (echoed only if the client sent one) so clients can correlate their id with the gateway's.

### Scope of the split: `X-Request-Id` only; `X-Trace-Id` is not preserved

The `client_request_id` backup captures **only** the `X-Request-Id` header value. `X-Trace-Id` is **not** backed up anywhere — and it is also not adopted as `request_id`, not parsed as the W3C `trace_id`, and not used for any correlation:

| Header | Pre-ADR-0050 role | Post-ADR-0050 role |
|---|---|---|
| `X-Request-Id` | Adopted as `request_id` (if non-nil-uuid) | Backed up to `client_request_id`; echoed as `X-Client-Request-Id`; not adopted as `request_id` |
| `X-Trace-Id` | Adopted as `request_id` fallback (if no `X-Request-Id`) | **Ignored entirely** — not backed up, not adopted, not parsed |
| `traceparent` (W3C) | Trace segment parsed as `trace_id`; never a request id | Unchanged — trace segment still parsed as `trace_id` |

This is an intentional narrowing. Rationale:

- **No known client relies on `X-Trace-Id` for cross-system correlation.** Every agent we ship against (Claude Code, Codex, CodeBuddy, OpenAI SDK) sends `X-Request-Id`, not `X-Trace-Id`. The header was in the priority chain as a hedge for "some client, someday"; preserving its value would be dead weight.
- **Two backup fields would be a footgun.** If `X-Trace-Id` were also backed up (to `client_request_id` or a new `client_trace_id`), consumers would need to know which one to consult. A single canonical "client original" field is simpler; a second one is a future enhancement if a real consumer shows up.
- **`traceparent` covers the W3C trace-correlation path already.** A client that wants trace-correlation at the W3C layer uses `traceparent`, which we still honor (parsed into `trace_id`, not a request id). The legacy `X-Trace-Id` header was duplicative of that path.
- **Response-header asymmetry is unchanged and pre-existing.** We echo `X-Trace-Id` in responses (populated from the parsed `traceparent`) but never read it on the request side. ADR-0040 already fixed the response direction; this ADR does not change it.

If a future agent ships with `X-Trace-Id` as its only correlation header, the fix is a follow-up migration adding a second backup field (`client_trace_id`) — not silently treating it as `client_request_id`, which would conflate two different wire semantics.

### Why not regenerate only for known-bad clients?

A denylist keyed on `User-Agent` / detected `agent_type` was considered. Rejected because:

- The middleware runs **before** agent detection, so the decision would need to move later or duplicate detection logic.
- The behavior boundary (which clients are "bad") drifts as new agents ship; a per-client carve-out is more fragile than a uniform rule.
- The semantic value of `request_id` differs by caller path — that is exactly the kind of split this ADR exists to eliminate.

### Why not keep the client value as `request_id` and add a separate `gateway_request_id`?

That keeps `request_id` stable for existing joins but at the cost of preserving the bug: queries and UIs that filter or key on `request_id` continue to hit duplicates, and every new consumer has to learn to use `gateway_request_id` instead. Reassigning the canonical meaning to the gateway-generated id is the clean break — existing callers already use row-id lookups for the duplicated cases.

## Consequences

### Schema

- New migration `00027_client_request_id.sql` adds `client_request_id TEXT NOT NULL DEFAULT ''` to `request_logs` plus `idx_request_logs_client_request_id` for reverse lookups.
- `trace_payloads` gains the column symmetrically for paired-trace reads.
- Desktop SQLite schema mirrors via GORM AutoMigrate (`RequestLogRow`, `TracePayloadRow`).
- **No backfill**: pre-migration rows keep `client_request_id = ''`. Historical `request_id` values are unchanged, so existing audit/search joins against old data still work.

### Wire

- `X-Request-Id` response header continues to carry the **gateway-generated** id (unchanged from today's behavior when the client sent nothing).
- New `X-Client-Request-Id` response header carries the client's original value when present.

### Code surfaces touched

- `internal/proxy/requestid_middleware.go` — always clear the header; back up the raw value to ctx.
- `internal/proxy/router.go` — `requestAndSessionIDs` returns the tuple; `echoCorrelationHeaders` adds `X-Client-Request-Id`.
- `internal/proxy/telemetry.go` + `internal/observability/requestlog.go` — `ClientRequestID` plumbed end-to-end.
- Stores: `internal/desktopstore/*`, `internal/store/*` schema + sinks + queries.
- Admin API: filter + CSV export + OpenAPI schema.
- Frontend: desktop `RequestLogs.tsx` shows the gateway id (now unique); admin web fixes the React-key bug in `request-logs/sessions/[session_id]/client.tsx` (key on `r.id` instead of `r.request_id`).

### Relationship to existing row-id fallbacks

The row-id lookup paths (`GetTraceByRowID`, `/api/v1/trace/rows/:id`) remain valid and are still the preferred lookup for trace detail — they are immune to any future `request_id` collision. This ADR does not deprecate them; it removes the upstream cause of the collisions they were working around.

## Alternatives considered

- **Status quo + UI hint**: leave the data alone, change the UI to key on row id everywhere and badge duplicate request_ids. Rejected — the user explicitly asked for the gateway to always generate unique ids and to record both the client and upstream values, which this ADR delivers.
- **Per-client regeneration denylist**: rejected per the argument above.
- **Rename `request_id` → `gateway_request_id`, keep client value as `request_id`**: rejected per the argument above.

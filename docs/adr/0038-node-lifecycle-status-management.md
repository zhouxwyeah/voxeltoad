# ADR-0038: Node lifecycle & status management — gap analysis and recommendation

- Status: Diagnostic (no impl)
- Date: 2026-07-10

## Context

The control plane (`admin`) and the data panel (`/overview` business dashboard + `/data-plane-nodes`
node list) need a complete view of each data-plane instance's lifecycle: which nodes are **active**,
which are in **warning / abnormal** state, which are under **maintenance**, and which have been taken
**offline** (manually or automatically).

This ADR is a diagnosis of the current state of node lifecycle/status management between the data panel
and admin node information. It documents what exists today, what is missing against the four status
dimensions the team cares about, and a recommendation for how to close the gap. **No code is changed
by this ADR**; the recommendation is intended as input for a future implementation ADR.

### How the current system works (verified)

- The `data_plane_nodes` table (`internal/store/migrations/00006_data_plane_nodes.sql`) holds one row
  per proxy replica, keyed by `instance_id`. Its `status` column is a bare `TEXT` with default
  `'online'` and **no enum, no CHECK constraint, no state machine**.
- The Go model mirrors this: `DataPlaneNode.Status string` (`internal/store/data_plane.go:19`).
  The only status values used anywhere are the three string literals `'online'`, `'draining'`,
  `'offline'`.
- The data plane drives its own lifecycle (`cmd/gateway/main.go`):
  - `Register()` on startup → `online` (idempotent upsert by `instance_id`, `data_plane.go:36-50`).
  - `Heartbeat()` every 15s → updates `last_heartbeat_at` while `status='online'`
    (`data_plane.go:53-58`).
  - `Drain()` on `SIGTERM` → `draining` (`data_plane.go:63-68`).
  - `UpdateBreakerStates()` every 15s → writes per-instance circuit-breaker state into a
    `breaker_states JSONB` column (`data_plane.go:116-125`; `cmd/gateway/main.go:245-262`).
- The admin plane is **read-only** on nodes:
  - `GET /api/v1/data-plane-nodes` lists all rows (`internal/admin/data_plane_nodes.go`, mounted in
    `internal/admin/server.go:96`).
  - `GET /api/v1/overview` aggregates `Nodes.Total` / `Nodes.Online` and a breaker-state count
    (`internal/admin/overview.go:54-84`).
  - A background goroutine every 60s calls `CleanupStale` with a 45s threshold, marking stale
    `online` nodes `offline` (`cmd/admin/main.go:140-161`; `data_plane.go:81-89`).
- The data panel is server-rendered (`web/src/app/[locale]/(dashboard)/data-plane-nodes/*` and
  `.../overview/*`) as Next.js RSC with `export const dynamic = "force-dynamic"`. There is **no
  WebSocket and no client-side polling**; freshness depends on the 15s heartbeat + re-render on
  navigation/refresh. The node table color-codes the three known statuses
  (`online`→emerald, `offline`→red, `draining`→amber) and shows `breaker_states` only as an
  aggregated overview count — never as a per-node status.

## Decision

No implementation decision is made in this ADR. Below is the **gap analysis** (the diagnosis) and a
**recommended direction** for a follow-up implementation ADR.

### Gap analysis vs. the four status dimensions

| Dimension | Present today? | Evidence / gap |
|---|---|---|
| **active (活跃)** | No | Only `online` exists. There is no distinction between "just registered", "serving normally", and "recovered". `active` has no semantics. |
| **warning / abnormal (告警 / 异常)** | No | The only abnormality signal is `breaker_states` JSONB (per-provider open/half-open/closed), surfaced **only** as an aggregated count on `/overview`. It is never mapped to a per-node `warning`/`abnormal` status and is not visualized per node. |
| **maintenance (维护中)** | No | No way for an operator to pause/isolate a node's traffic while keeping its record. No `maintenance` state exists. |
| **offline — manual takedown (手动下线)** | No | `MarkOffline` (`data_plane.go:72-77`) has **no production caller**. There is no admin endpoint or UI action to manually offline/resume a node. `offline` is set only by the passive `CleanupStale` job. |

### Additional defects found

- **`draining` can get stuck.** `CleanupStale` only acts on `status='online'` rows
  (`data_plane.go:84`). A node that crashes *while draining* is never moved to `offline` and stays
  `draining` indefinitely.
- **No transition validation / state machine.** Every transition is an ad-hoc `UPDATE` with no check
  of the current status (e.g. `Drain` does not verify it started from `online`). Illegal transitions
  are possible.
- **No active health check.** Admin only runs passive stale-detection; it never probes nodes. A node
  that is `online` but serving errors is indistinguishable from a healthy one.
- **Read-only admin surface.** There is no node mutation API at all; all writes come from the data
  plane itself or the cleanup job. Operators cannot intervene in the lifecycle.

### Recommended direction (for a future implementation ADR)

1. **Introduce a status enum + legal-transition state machine.** Define constants in
   `internal/store/data_plane.go` and add a `CHECK (status IN (...))` constraint via a new migration.
   Suggested state set:

   ```
   registering → active → warning / abnormal → maintenance → draining → offline
   ```

   with explicit transition functions and guards rejecting illegal transitions (e.g. `draining`
   should only be reached from `active`/`warning`/`maintenance`).

2. **Give `active` real meaning.** Differentiate `registering` (post-Register, pre-first successful
   health/serve) from `active` (serving normally), and a `recovered` sub-state if desired.

3. **Land warning/abnormal from real signals.** Aggregate `breaker_states` (and optionally error-rate
   / heartbeat jitter) into a per-node `warning` / `abnormal` status, and visualize it on both the
   `/data-plane-nodes` table and `/overview`.

4. **Add maintenance state.** Allow an operator to set `maintenance` (pause traffic, keep record,
   exclude from `OnlineCount` rate-limit division) and resume.

5. **Add manual-offline management API.** New super-admin endpoints, e.g.
   `POST /api/v1/data-plane-nodes/:id/actions` with `{set_maintenance, force_offline, resume}`, plus
   action buttons on the `/data-plane-nodes` table. This gives `MarkOffline` a real caller and a
   manual resume path.

6. **Fix the `draining` stuck-state.** Add a TTL: any `draining` node whose `last_heartbeat_at` is
   older than a threshold is auto-moved to `offline` by the cleanup job (extend `CleanupStale` to also
   cover `draining`).

7. **(Optional) Active health probe.** Have admin periodically probe each `online`/`active` node so
   that "online but broken" is detectable and can be promoted to `warning`/`abnormal`.

## Consequences

### Of the current (diagnosed) state

- Positive: zero new infrastructure (PG-only), fail-open data plane, automatic stale cleanup.
- Negative: the data panel cannot show whether a node is truly healthy, warning, abnormal, in
  maintenance, or manually taken down. Operators have no lifecycle control. A crashed-during-drain
  node leaks a stuck `draining` row.

### Of the recommended direction (if adopted later)

- Positive: a complete, auditable node lifecycle; operators can intervene; warning/abnormal become
  first-class and visible; `draining` no longer leaks.
- Negative / cost: new migration + state-machine code; new admin mutation endpoints (authz, audit);
  optional active probe adds load and a new failure mode. Should be scoped in a follow-up
  implementation ADR, not here.

## Related

- ADR-0024: data-plane node registration & heartbeat (current design, Accepted)
- ADR-0037: multi-instance deployment topology
- ADR-0036 / B1': rate-limit instance-count division (depends on `OnlineCount`)
- ADR-0034: Redis-backed shared state (notes current PG-only node state)
- Plan A3: business overview dashboard
- Code: `internal/store/data_plane.go`, `cmd/gateway/main.go`, `cmd/admin/main.go`,
  `internal/admin/data_plane_nodes.go`, `internal/admin/overview.go`,
  `web/src/app/[locale]/(dashboard)/data-plane-nodes/*`

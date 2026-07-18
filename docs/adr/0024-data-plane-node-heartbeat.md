# ADR-0024: Data-plane instance registration and heartbeat

- Status: Accepted
- Date: 2026-07-04

## Context

Multiple data-plane (proxy) replicas run independently behind a load balancer
(Ingress). The control plane (admin UI) needs visibility into which instances
are online, their versions, and their configuration freshness — but **not** for
service discovery (routing/LB are the deploy layer's responsibility, not the
gateway's).

The architecture constraint from the plan is single-PG dependency, no etcd.

## Decision

### table: data_plane_nodes

Each proxy replica self-registers on startup via a lightweight `INSERT … ON
CONFLICT DO UPDATE` row keyed by `instance_id` (hostname-pid). Heartbeats
update `last_heartbeat_at` every 15s. On graceful shutdown (`SIGTERM`), status
transitions to `offline`.

### Fail-open semantics

If the table does not exist yet (migration not run on admin) or PG is
temporarily unavailable, registration and heartbeat failures are **logged as
warnings** — the data plane continues serving requests. The admin UI simply
shows an empty node list until the channel recovers.

### Zombie cleanup

A background goroutine in the admin process periodically (every 60s) marks
online nodes whose `last_heartbeat_at` is stale (>45s) as `offline`. This
handles crashed instances without a Drain call.

### Status lifecycle

```
online ── Drain() ──→ offline    (graceful)
online ── CleanupStale() ──→ offline (crash detected)
```

### Integration points

- `cmd/gateway/main.go`: registration + heartbeat goroutine + drain on shutdown
- `cmd/admin/main.go`: `CleanupStale` background goroutine
- `GET /api/v1/data-plane-nodes`: admin UI list endpoint
- `GET /api/v1/overview`: aggregates online/total counts for dashboard

## Consequences

### Positive

- Zero new infrastructure — PG is the only dependency.
- Fail-open means the data plane never fails to start due to missing migration.
- Stale cleanup is automatic, no manual intervention for crashed instances.
- Instance IDs (`hostname-pid`) are unique per-process, safe across restarts.

### Negative

- `CleanupStale` runs only on the admin plane; if no admin is running, crashed
  nodes remain `online` until admin restarts.
- Heartbeat-to-threshold timing is hardcoded (15s/45s). Coordinated restarts
  (rolling deploy with heartbeat window < drain window) may briefly show all
  instances offline.
- `OnlineCount()` for B1' rate limit division is called once at startup; it
  does not react to dynamic scale events.

## Related

- ADR-0013: quota data-plane access (PG direct-connection pattern)
- Plan A3: business overview dashboard
- Plan B1': rate limit instance-count division

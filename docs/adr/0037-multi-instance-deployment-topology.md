# ADR-0037: Multi-instance (cluster) deployment topology

- Status: Proposed
- Date: 2026-07-09
- Builds on ADR-0013 (PG is the sole strong-consistency dependency), ADR-0007
  (data↔management-plane trust via shared secret), ADR-0024 (node heartbeat),
  ADR-0034 (Redis-backed shared state), ADR-0035 (connection pool tuning),
  ADR-0036 (dynamic rate-limit division).

## Context

The project ships a Helm chart (`deploy/helm/`) with `replicaCount: 1` and a
comment noting that per-instance state makes multi-replica deployment
imprecise. As adoption grows, operators need a clear, documented topology for
running multiple data-plane replicas behind a load balancer. The pieces are
already in place or proposed:

- Configuration distribution: HTTP snapshot polling (ADR-0007), eventually
  consistent, no coordination service required.
- Strong-consistency state (quota): direct PG (ADR-0013).
- Node visibility: heartbeat registration (ADR-0024).
- Shared hot-path state: Redis opt-in (ADR-0034) or in-memory with dynamic
  division (ADR-0036).
- Connection safety: pool tuning (ADR-0035).

What was missing is a single document that ties these together into a concrete,
operable deployment topology — the relationships, the failure modes, and the
sizing guidance.

## Decision

Document the **reference topology** for a multi-instance deployment:

```
                    ┌─────────────┐
   Client traffic → │  Ingress/LB │  (L7, round-robin or least-conn; TLS term)
                    └──────┬──────┘
               ┌───────────┼───────────┐
               ▼           ▼           ▼
        ┌─────────┐ ┌─────────┐ ┌─────────┐   data-plane replicas (gateway)
        │ gateway │ │ gateway │ │ gateway │   stateless; config via poll
        └────┬────┘ └────┬────┘ └────┬────┘
             │           │           │
             ▼           ▼           ▼
        ┌─────────────────────────────────┐
        │           PostgreSQL             │  ← sole strong-consistency store
        │  (quotas, usage_records, config, │     (quota TryDebit/Settle, async
        │   request_logs, heartbeat)       │      writes are fail-open)
        └─────────────────────────────────┘
             │           │           │
             ▼           ▼           ▼
        ┌─────────────────────────────────┐
        │            Redis (opt-in)        │  ← shared limiter/cache/breaker
        │  (only when cluster.redis_addr   │     (ADR-0034); absent = in-mem
        │   is set)                        │      + dynamic division ADR-0036)
        └─────────────────────────────────┘
```

**Topology rules:**

1. **Load balancer** — any L7 LB (Ingress, ALB, Envoy). Round-robin suffices
   for the dynamic-division path (ADR-0036); least-connections is preferred
   when available. Health-check against the data plane's readiness endpoint.
   **No session stickiness is required** — session affinity is a routing
   concern (ADR-0018), not a load-balancing concern.

2. **Replicas** — the data plane is horizontally scalable; add/remove freely.
   Each replica self-registers via heartbeat (ADR-0024) and polls the admin
   plane for config (ADR-0007). Graceful shutdown drains in-flight requests
   before the heartbeat expires.

3. **PostgreSQL** — the single source of truth. Shared by all replicas. The
   connection budget must accommodate `replicas × max_open` (ADR-0035):
   `PG max_connections ≥ replicas × max_open + admin_plane + operational(10)`.
   Quota operations are synchronous; usage/request-log writes are async and
   fail-open (ADR-0016).

4. **Redis (opt-in)** — added only when exact rate-limit/cache/breaker
   behavior across replicas is required (ADR-0034). Without it, the
   dynamic-division path (ADR-0036) bounds rate limits approximately; caching
   and breaking remain per-instance.

5. **Admin plane** — runs as a **separate** deployment (it need not be
   replicated for correctness; it is the config write surface). Data-plane
   replicas poll it over the internal token (ADR-0007).

**Failure modes:**

| Failure | Effect | Mitigation |
|---|---|---|
| PG unreachable | Quota fail-closed → 503 (no uncontrolled spend). Async writes dropped (fail-open). | PG HA (replica + failover); the data plane stays correct, just rejects. |
| Redis unreachable (if used) | Rate limit/cache degrade to fail-open; breaker falls back per-instance. | Not a correctness risk — only precision. Redis is optional by design. |
| Admin plane unreachable | Replicas keep serving on last-known config (eventual consistency). No new config until restored. | Admin plane is not on the request hot path. |
| Replica crash | LB stops sending traffic after health-check fails. In-flight requests fail. Heartbeat expires (45s), removed from online count. | Graceful shutdown drains before exit; HPA replaces the replica. |

## Consequences

- Multi-instance deployment is now a documented, operable topology rather than
  an implicit "it should work."
- The dependency story is explicit: **PG is required; Redis and the admin
  plane are optional/separate.** No etcd, no ZooKeeper, no coordination
  service — the system's coordination is PG + HTTP polling.
- Sizing is formulaic: `PG max_connections` and `replicaCount` must be tuned
  together via `max_open` (ADR-0035).
- The Helm chart's `replicaCount` can move past 1 with confidence once the
  operator has chosen the Redis (exact) or no-Redis (approximate) path and
  tuned the connection pool.
- This ADR is descriptive (topology + guidance), not prescriptive of code —
  the enabling decisions are ADR-0034/0035/0036. It exists so operators have a
  single reference instead of reconstructing the picture from scattered ADRs.

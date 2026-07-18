# ADR-0026: Rate limit instance-count division (no-Redis interim)

- Status: Accepted
- Date: 2026-07-04
- Builds on: ADR-0008 (rate limiting interface), ADR-0024 (data-plane heartbeat)

## Context

ADR-0008 rate limits use per-instance in-memory sliding windows. With N data-plane
replicas behind a load balancer, each enforces the full configured RPM/TPM
independently, multiplying the effective aggregate limit by N.

The user decision is **暂不引入 Redis** — defer the precise cross-instance
limiter (Redis backend) to a future scaling milestone. An interim solution is
needed so the aggregate limit approximates the configured value.

## Decision

At startup, after self-registration (ADR-0024), the proxy queries
`DataPlaneRepo.OnlineCount()` and **ceil-divides** all 6 rate limit quotas by
the number of online instances:

```
divide(v) = (v + n - 1) / n     // ceil(a/b)

TenantRPM = divide(cfg.TenantRPM)
TenantTPM = divide(cfg.TenantTPM)
GroupRPM  = divide(cfg.GroupRPM)
GroupTPM  = divide(cfg.GroupTPM)
KeyRPM    = divide(cfg.KeyRPM)
KeyTPM    = divide(cfg.KeyTPM)
```

The division is **one-time at startup** — it does not dynamically react to
scale events (instance joins or leaves). With Ingress round-robin, per-instance
load is approximately uniform, making the approximation bounded.

### Why not dynamic

Dynamic rebalancing would require a notification channel (push from admin or
periodic re-query) — adding complexity without resolving the fundamental
per-instance in-memory limitation. The dynamic path is deferred to the
Redis-backed B1 (ADR-0008 clause).

## Consequences

### Positive

- Zero new infrastructure (uses existing `data_plane_nodes.OnlineCount()`).
- Aggressive over-limit scenarios (single-instance dev → multi-instance prod
  without reconfiguration) are avoided.
- Configuration values remain total-cluster limits in intent — the division
  is operational, not semantic.

### Negative

- `OnlineCount()` is queried once at startup. If the instance count changes
  (scale up/down), limits are not rebalanced until restart.
- During a rolling deploy, the count briefly fluctuates — some instances may
  get larger quotas while others are in limbo.
- The `Limiter` interface (ADR-0008) already supports Redis swap; this is an
  interim approximation, not a permanent solution.

## Related

- ADR-0008: rate limiting interface (Limiter, sliding window, RPM/TPM)
- ADR-0013: quota consistency (PG, not in-memory — unaffected by this decision)
- ADR-0024: data-plane heartbeat (source of OnlineCount)

# ADR-0034: Redis-backed shared state for rate limiting, caching, and circuit breaking

- Status: Proposed
- Date: 2026-07-09
- Builds on ADR-0008 (rate limiting: in-memory sliding window P0), ADR-0026
  (instance-count division as the no-Redis interim), and the circuit breaker
  (`internal/proxy/breaker.go`) + response cache (`internal/plugin/cache/`).

## Context

The data plane is designed to be horizontally scalable behind a stateless load
balancer, with PostgreSQL as the **only** required stateful dependency
(ADR-0013: quota is money, so it goes through PG directly). Three hot-path
components, however, keep **per-instance in-memory** state that becomes incorrect
when more than one replica serves traffic:

| Component | Interface | In-memory impl | Multi-instance defect |
|---|---|---|---|
| Rate limiter | `ratelimit.Limiter` | `MemoryLimiter` (sliding window) | N replicas each enforce `limit` independently → effective limit is `limit × N` |
| Response cache | `cache.Cache` | `MemoryCache` (map + TTL) | Cache not shared → lower hit rate, redundant upstream calls |
| Circuit breaker | (internal) | `circuitBreaker` per provider | Each replica judges health independently → a failing provider gets retried by replicas that haven't tripped yet |

ADR-0026 is the acknowledged **interim** fix for rate limiting: at startup each
replica divides its configured limits by the online instance count (ceil), so
the aggregate is approximately bounded. It is a static, one-shot division that
does not track autoscaling and assumes round-robin evenness. The code comments
in all three components explicitly mark this as a "known gap" and point to Redis
as the cluster-correct upgrade behind the **same interface**.

## Decision

Introduce an **opt-in Redis backend** for all three components, selected by
configuration, behind their existing interfaces — so no call-site changes are
needed:

1. **Rate limiter** — `ratelimit.RedisLimiter` implements `Limiter` using a
   Redis sorted-set sliding window (ZREMRANGEBYSCORE + ZADD + ZCARD in a Lua
   script for atomicity). Provides exact global RPM/TPM across all replicas.
   Replaces ADR-0026's startup division entirely when enabled.

2. **Response cache** — `cache.RedisCache` implements `Cache` using Redis
   `SET key value EX ttl` / `GET`. Shared hit pool across replicas.

3. **Circuit breaker** — a `RedisCircuitBreaker` stores per-provider failure
   counts and open/until state in Redis (INCR + EXPIRE), so all replicas see a
   unified health view and stop hitting a provider as soon as the first replica
   trips it.

**Selection** is driven by a new Bootstrap config block:
```yaml
cluster:
  redis_addr: ""        # empty = in-memory (current default, single-instance)
  redis_password_ref: ""# secret ref (env://, plain://) — reuse SecretResolver
```
When `redis_addr` is empty, the existing in-memory implementations are wired
(single-instance deployments are unaffected). When set, the Redis-backed
implementations are constructed and injected. This keeps the P0 single-binary
path dependency-free while making the multi-instance path a config flip.

**Failure mode.** Redis is treated as **fail-open** for rate limiting and
caching (a Redis outage degrades to "no limit / no cache" rather than rejecting
requests — matching the async recorder philosophy in ADR-0016). The circuit
breaker fails **closed per-instance** (falls back to local state) so a Redis
outage does not cause a thundering herd against a known-bad provider. Quota is
**never** moved to Redis — it stays in PG (ADR-0013).

## Consequences

- The three components gain cluster-correct behavior without interface churn:
  callers still see `Limiter` / `Cache` / breaker abstractions.
- ADR-0026's startup division becomes **dead code when Redis is enabled** (and
  remains the fallback for deployments that opt out of Redis). See ADR-0036 for
  the dynamic re-division path that replaces the static one.
- New dependency: `github.com/redis/go-redis/v9` (or equivalent) added to
  go.mod. Single-instance deployments keep zero external deps beyond PG.
- Operational surface grows: Redis must be monitored, sized (eviction policy,
  memory), and made highly-available. The config flip makes this opt-in, so it
  is a conscious scaling decision, not a silent default.
- Latency: each rate-limit/cache/breaker check adds a Redis RTT (~0.3–1ms in a
  co-located deployment). This is acceptable on the request hot path given the
  multi-second upstream latencies; the cache *saves* far more than it costs.

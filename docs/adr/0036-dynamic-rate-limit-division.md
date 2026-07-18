# ADR-0036: Dynamic rate-limit division (replaces static startup division)

- Status: Proposed
- Date: 2026-07-09
- Supersedes the interim approach in ADR-0026 (static startup-time instance
  division). Builds on ADR-0024 (data-plane node heartbeat → live online count)
  and ADR-0008 (rate limiting: RPM/TPM sliding window).

## Context

ADR-0026 solved multi-instance rate limiting **without Redis**: at startup each
replica queries `OnlineCount()` and ceil-divides its configured limits so the
aggregate approximates the configured value. Two limitations are acknowledged in
that ADR:

1. **Static.** The division is computed once at startup. If the fleet
   autoscales (HPA adds/removes replicas) or a replica crashes, the per-replica
   limits do **not** re-adjust. A scale-up from 2→4 replicas leaves each
   enforcing a ½-share (aggregate = 2× configured); a scale-down from 4→2
   leaves each enforcing a ¼-share (aggregate = ½ configured).
2. **Assumes evenness.** Round-robin LB is assumed; weighted or
   least-connections LB skews the approximation.

The heartbeat table (`data_plane_nodes`, ADR-0024) already tracks live online
instances with a 15s heartbeat and 45s zombie cleanup — a continuously-updated
source of truth for the current fleet size.

## Decision

Replace the one-shot startup division with a **periodic re-division** driven by
the heartbeat count:

1. Each replica already heartbeats every 15s (ADR-0024). Piggyback on that
   cadence: after each heartbeat, re-query `OnlineCount()` and re-apply the
   ceil-division to the in-memory limiter's configured RPM/TPM.
2. The division uses the **same ceil formula** as ADR-0026
   (`per_replica = ceil(configured / online)`) for consistency.
3. The limiter's effective limits are updated in-place (the `MemoryLimiter`
   already holds its rates as mutable fields); in-flight requests against the
   old window finish, new requests use the adjusted window.

**Redis alternative.** When ADR-0034 (Redis-backed limiter) is enabled, this
re-division is **not needed** — a global Redis limiter enforces the configured
limit exactly across all replicas, regardless of fleet size. This ADR is the
**no-Redis upgrade path** for deployments that cannot or will not add Redis; it
makes the static approach dynamic using data we already have.

**Bounds.** Re-division runs at most every 15s (the heartbeat interval), so a
scale event converges within one heartbeat cycle — acceptable for a system
whose limits are already approximate under no-Redis. The online count is
fail-open: if `OnlineCount()` returns an error (PG briefly unreachable), the
replica keeps its current division rather than spiking to full or zero.

## Consequences

- Autoscaling and crash recovery no longer leave the rate budget wrong until a
  manual restart. The system self-corrects within ~15s.
- The code path introduced by ADR-0026 (the `divide := func(v int) int {...}`
  block in `cmd/gateway/main.go`) moves from startup-once into the heartbeat
  goroutine.
- When Redis is enabled (ADR-0034), this mechanism is bypassed — the two are
  mutually exclusive, selected by config.
- This remains an **approximation**: it still assumes round-robin evenness.
  Deployments needing exact limits adopt Redis (ADR-0034). The hierarchy is:
  Redis (exact) > dynamic division (good) > static division (ADR-0026, crude).

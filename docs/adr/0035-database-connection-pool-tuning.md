# ADR-0035: Database connection pool tuning

- Status: Proposed
- Date: 2026-07-09
- Builds on ADR-0013 (quota is the data plane's one synchronous stateful
  dependency — direct PG), ADR-0016 (data-service design), ADR-0024 (data-plane
  node heartbeat / multi-instance).

## Context

`internal/store.Open` opens the database with gorm's defaults and never calls
`SetMaxOpenConns` / `SetMaxIdleConns` / `SetConnMaxLifetime`:

```go
func Open(dsn string) (*DB, error) {
    gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
    ...
}
```

Go's `database/sql` defaults are `MaxOpenConns = 0` (unbounded) and
`MaxIdleConns = 2`. This is a latent production hazard:

- **Unbounded open connections** — under load each data-plane replica can open
  an arbitrary number of PG connections, exhausting the server's
  `max_connections` (default 100 in vanilla PG; often raised but still finite).
  With N replicas this compounds: `N × unbounded` connections.
- **Idle pool of 2** — bursty traffic repeatedly opens/closes connections
  (connection setup is expensive: TLS handshake, auth, session init), adding
  latency jitter on every spike.
- **No max lifetime** — long-lived connections never get recycled, so a
  PG-side restart, network blip, or stale-role issue can leave half-dead
  connections that fail only on next use.

The quota hot path (`QuotaRepo.TryDebit`) runs a synchronous transaction per
request — so the data plane holds a PG connection for the duration of each
request's pre-debit + settle. Connection exhaustion would directly cause 503
fail-closed rejections (quota unreachable), which is the worst outcome for a
gateway (correct but unavailable).

## Decision

Add a `db.pool` configuration block to Bootstrap and apply it at `Open`:

```yaml
db:
  dsn: "postgres://..."
  pool:
    max_open: 25        # cap concurrent connections per replica
    max_idle: 10        # warm idle pool to absorb bursts
    max_lifetime: 30m   # recycle connections to shed staleness
    max_idle_time: 5m   # close idle conns promptly to free server slots
```

`store.Open` reads these and calls the four `Set*` methods on the underlying
`*sql.DB`. Defaults (when the block is absent) are chosen for a **typical
single-replica dev/small deployment** and are conservative:

| Setting | Default | Rationale |
|---|---|---|
| max_open | 25 | PG's default `max_connections=100`; leaves headroom for admin plane + psql + replicas. Tunable per deployment. |
| max_idle | 10 | covers a modest burst without re-opening |
| max_lifetime | 30m | recycles before PG-side idle-in-transaction or connection-age issues bite |
| max_idle_time | 5m | returns idle connections to the server pool promptly |

**Multi-instance guidance.** With R replicas, total connections ≈
`R × max_open`. Operators must size `max_open` so `R × max_open < PG
max_connections − overhead`. ADR-0037 (cluster deployment topology) records this
formula. The cap also makes the failure mode **graceful**: when the pool is
exhausted, requests queue briefly then fail-closed 503 (quota unreachable)
rather than crashing PG.

## Consequences

- The data plane now has a bounded, predictable connection footprint per
  replica — no more unbounded growth under load.
- Burst latency improves: the idle pool absorbs spikes without re-handshaking.
- Connections recycle on a schedule, shedding half-dead state.
- This is a **config + defaults** change, not an architectural one — gorm and
  `database/sql` already support it; we simply stop ignoring the knobs.
- The defaults are intentionally not aggressive (25, not 100) so a misconfigured
  multi-replica deployment cannot trivially exhaust PG. Operators scaling up
  tune both `max_open` and PG's `max_connections` together.

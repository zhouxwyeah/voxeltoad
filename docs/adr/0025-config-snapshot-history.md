# ADR-0025: Config snapshot history — async save, diff, rollback

- Status: Accepted
- Date: 2026-07-04
- Builds on: ADR-0015 (config_generation counter, snapshot versioning)

## Context

Every config write (provider/model/route/plugin update or delete) bumps the
`config_generation` counter in the same transaction. The data plane polls
`/internal/config/snapshot` and hot-reloads. But there is no version history:
operators cannot browse past configs, compare versions, or roll back a mistaken
change.

The plan's B3/B4/B5 calls for config version history, diff, rollback, and
dry-run preview — all using only the existing PG dependency.

## Decision

### table: config_snapshots

After every config mutation, a **full `config.Dynamic` snapshot** is saved to the
`config_snapshots` table under the new generation version. Saving is
**asynchronous** and **fail-open**: a goroutine calls `ConfigRepo.Snapshot()`
and inserts into the table. If the save fails (e.g., transient PG error), it
is logged and discarded — mutation success is not gated on snapshot persistence.

`INSERT … ON CONFLICT (version) DO NOTHING` ensures idempotency.

### API surface

| Endpoint | Purpose |
|----------|---------|
| `GET /api/v1/config/history` | List versions (keyset DESC, cursor pagination) |
| `GET /api/v1/config/history/:version` | Full snapshot at a version |
| `GET /api/v1/config/history/diff?from=X&to=Y` | Structured diff (added/deleted resources) |
| `POST /api/v1/config/rollback` | Restore a historical snapshot (DELETE + re-INSERT all resources in transaction + bump generation) |
| `POST /api/v1/config/preview` | Validate + diff against current (dry-run, no persistence) |

### Async vs synchronous trade-off

The snapshot save is async to **keep the config mutation path latency
unchanged**. Accidental loss of a snapshot (PG down after write) means that
version is irretrievable from history but does not affect the running config.
This is acceptable because config writes are low-frequency and the window
between write and async save is negligible in practice.

## Consequences

### Positive

- Operators can browse config history, diff versions, and rollback mistakes —
  reducing production config change risk.
- Dry-run preview allows validation before publishing.
- Zero new infrastructure — PG is the only dependency.

### Negative

- Full snapshot per write means ~1-10 KB per version of JSONB. With hundreds
  of config writes per day this is negligible; a future TTL on snapshots
  (keep last N or last 30 days) can be added if storage becomes material.
- The async window between write and snapshot persistence means a rare
  failure can lose a version from history (but never from the running config).
- Rollback does not emit `audit_logs` entries (Phase 3 enhancement tracked
  outside this ADR).

## Related

- ADR-0015: config_generation counter
- ADR-0019: control-plane read API patterns (keyset cursor)
- Plan B3/B4/B5: config history, rollback, dry-run

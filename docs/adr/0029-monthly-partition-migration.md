# ADR-0029: Monthly partition migration — PostgreSQL DO block

- Status: Accepted
- Date: 2026-07-04
- Builds on: ADR-0015 (migration framework, goose embedded)

## Context

Both `usage_records` and `request_logs` are partitioned by `RANGE (created_at)`
with a single `DEFAULT` partition in Phase 1. As data grows, query performance
on time-range scans degrades because all rows land in the default partition.
Monthly partitions are needed to constrain scans to specific date ranges.

ADR-0015 embeds SQL migration files via `//go:embed` and uses `goose` as the
migration engine. Goose v3 supports Go-based migrations, but switching from
embedded SQL to compiled Go code requires changes to the embed/compile
pipeline.

## Decision

### SQL migration with PostgreSQL DO block

Use a standard SQL migration file (`00007_monthly_partitions.sql`) with a
PostgreSQL anonymous `DO $$ ... $$` block that dynamically creates 12 monthly
partitions (current month through 11 months ahead) for both `usage_records`
and `request_logs`.

### Dynamic partition creation

```sql
DO $$
DECLARE
    start_date date;
    end_date   date;
    suffix     text;
    rec        record;
BEGIN
    FOR i IN 0..11 LOOP
        start_date := date_trunc('month', current_date) + (i || ' months')::interval;
        end_date   := start_date + interval '1 month';
        suffix     := to_char(start_date, 'YYYY_MM');
        FOR rec IN
            SELECT unnest(ARRAY['request_logs', 'usage_records']) AS tbl
        LOOP
            EXECUTE format(
                'CREATE TABLE IF NOT EXISTS %I PARTITION OF %I
                 FOR VALUES FROM (%L) TO (%L)',
                rec.tbl || '_' || suffix, rec.tbl,
                start_date, end_date
            );
        END LOOP;
    END LOOP;
END;
$$;
```

### Why DO block over Go migration

- The existing migration pipeline (`//go:embed migrations/*.sql` → `goose.NewProvider`)
  works only with SQL files. Adding Go migrations requires switching to
  `goose.AddNamedMigration` and altering the embed/compile boundary.
- A PostgreSQL DO block achieves the same dynamic behavior without changing
  the migration infrastructure.
- `CREATE TABLE IF NOT EXISTS` ensures idempotency across repeated runs.

### Year boundary

The DO block creates 12 partitions from `current_date`. This covers a rolling
12-month window through the current month. A yearly maintenance task (or
re-running the same migration) is needed to extend coverage. This is acceptable
because:
- The DEFAULT partition catches rows outside the 12-month window.
- Query performance on historical data is a gradual concern, not a hard cutoff.

## Consequences

### Positive

- Zero migration framework changes — stays within the existing goose/SQL
  pipeline.
- Dynamic — no hardcoded year-month values in the migration file.
- Idempotent — safe to re-run.

### Negative

- Rollback (`DOWN`) drops all monthly partitions — rows in dropped partitions
  are lost. Acceptable for a migration rollback (usually followed by restore).
- Requires yearly re-run (or automated cron) to maintain the 12-month window.
  A future enhancement can make this a periodic job triggered by A4 leader
  election.
- `usage_records` monthly partitions follow the same 12-month window as
  `request_logs`, even though usage retention needs may differ.

## Related

- ADR-0014: management-plane schema (usage_records partition design)
- ADR-0015: migration & snapshot versioning (goose, embed)
- ADR-0021: request_logs schema (partition skeleton)
- Plan C4: monthly partition management

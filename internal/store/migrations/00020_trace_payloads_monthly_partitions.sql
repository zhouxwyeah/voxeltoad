-- +goose Up
-- +goose StatementBegin

-- Monthly RANGE partitions for trace_payloads, mirroring the partitioning
-- strategy used for request_logs/usage_records (ADR-0029). Covers the next 12
-- calendar months (current month through 11 months ahead). The DEFAULT
-- partition (created in 00019) retains rows outside this window.
--
-- trace_payloads has a SHORT retention (ADR-0039, default 7 days), enforced by
-- dropping old monthly partitions (Phase 4); the monthly granularity still
-- keeps the partition-DROP TTL cheap (one DROP per expired month, not a DELETE
-- scan) and constrains time-range scans to the relevant months.
--
-- Idempotent: CREATE TABLE IF NOT EXISTS skips partitions that already exist.
DO $$
DECLARE
    start_date date;
    end_date   date;
    suffix     text;
BEGIN
    FOR i IN 0..11 LOOP
        start_date := date_trunc('month', current_date) + (i || ' months')::interval;
        end_date   := start_date + interval '1 month';
        suffix     := to_char(start_date, 'YYYY_MM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
            'trace_payloads_' || suffix,
            'trace_payloads',
            start_date,
            end_date
        );
    END LOOP;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Detach and drop monthly partitions (rows in dropped partitions are lost).
DO $$
DECLARE
    rec record;
BEGIN
    FOR rec IN
        SELECT tablename
        FROM pg_tables
        WHERE schemaname = 'public'
          AND tablename LIKE 'trace_payloads_%'
          AND tablename <> 'trace_payloads_default'
    LOOP
        EXECUTE format('DROP TABLE IF EXISTS %I CASCADE', rec.tablename);
    END LOOP;
END;
$$;

-- +goose StatementEnd

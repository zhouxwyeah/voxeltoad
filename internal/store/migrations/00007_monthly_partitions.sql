-- +goose Up
-- +goose StatementBegin

-- Create monthly RANGE partitions for request_logs and usage_records covering
-- the next 12 calendar months (current month through 11 months ahead). New
-- rows from the DEFAULT partition are automatically routed to the correct
-- monthly partition; the DEFAULT partition retains rows outside the 12-month
-- sliding window and rows inserted before this migration ran.
--
-- The DO block is idempotent: CREATE TABLE IF NOT EXISTS skips partitions that
-- already exist.
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
                'CREATE TABLE IF NOT EXISTS %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
                rec.tbl || '_' || suffix,
                rec.tbl,
                start_date,
                end_date
            );
        END LOOP;
    END LOOP;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Detach and drop monthly partitions. Rows in dropped partitions are lost.
DO $$
DECLARE
    rec record;
BEGIN
    FOR rec IN
        SELECT tablename
        FROM pg_tables
        WHERE schemaname = 'public'
          AND (tablename LIKE 'request_logs_%' OR tablename LIKE 'usage_records_%')
          AND tablename NOT IN ('request_logs_default', 'usage_records_default')
    LOOP
        EXECUTE format('DROP TABLE IF EXISTS %I CASCADE', rec.tablename);
    END LOOP;
END;
$$;

-- +goose StatementEnd

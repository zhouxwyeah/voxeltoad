-- +goose Up
-- +goose StatementBegin

-- Config version history for rollback, diff, and dry-run previews (B3/B4/B5).
-- Each row holds a full config.Dynamic snapshot at a config_generation version,
-- saved asynchronously after every config mutation (fail-open).
CREATE TABLE IF NOT EXISTS config_snapshots (
    version   BIGINT NOT NULL PRIMARY KEY,
    payload   JSONB  NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS config_snapshots;
-- +goose StatementEnd

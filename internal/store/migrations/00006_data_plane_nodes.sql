-- +goose Up
-- +goose StatementBegin

-- Data-plane instance self-registration and heartbeat for cluster visibility
-- (A1). Each proxy replica writes a row on startup, updates last_heartbeat
-- periodically, and marks itself offline on graceful shutdown.
CREATE TABLE IF NOT EXISTS data_plane_nodes (
    id                BIGSERIAL PRIMARY KEY,
    instance_id       TEXT NOT NULL,
    hostname          TEXT NOT NULL DEFAULT '',
    addr              TEXT NOT NULL DEFAULT '',
    version           TEXT NOT NULL DEFAULT 'dev',
    commit            TEXT NOT NULL DEFAULT '',
    config_generation BIGINT NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'online',
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_data_plane_nodes_instance_id
    ON data_plane_nodes (instance_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS data_plane_nodes;
-- +goose StatementEnd

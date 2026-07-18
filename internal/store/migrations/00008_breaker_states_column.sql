-- +goose Up
-- +goose StatementBegin

-- Breaker state JSONB column for B2': per-instance circuit breaker state
-- reported via heartbeat, aggregated by the admin overview panel.
ALTER TABLE data_plane_nodes ADD COLUMN IF NOT EXISTS breaker_states JSONB;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE data_plane_nodes DROP COLUMN IF EXISTS breaker_states;
-- +goose StatementEnd

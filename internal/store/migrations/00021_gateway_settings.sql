-- +goose Up
-- +goose StatementBegin

-- Gateway-wide behavior settings (trace capture, future otel/rate_limit, etc.),
-- stored as a single JSONB row. This is the management-plane source of truth
-- for hot-reloadable gateway behavior parameters: the admin plane writes here,
-- the data plane reads them via the config snapshot (Dynamic.Settings), and
-- applies them per-request without restart.
--
-- Single-row design (CHECK id = 1) mirrors config_generation: there is exactly
-- one global settings document. JSONB gives forward-compatible schema evolution
-- (new params need no ALTER TABLE). Each mutation bumps config_generation in the
-- same transaction so the snapshot version tracks the change.
CREATE TABLE gateway_settings (
    id         SMALLINT PRIMARY KEY DEFAULT 1,
    spec       JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT gateway_settings_single_row CHECK (id = 1)
);

INSERT INTO gateway_settings (id, spec) VALUES (1, '{}');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gateway_settings;
-- +goose StatementEnd

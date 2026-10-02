-- +goose Up
-- +goose StatementBegin
ALTER TABLE usage_records ADD COLUMN application_id BIGINT;
ALTER TABLE usage_records ADD COLUMN environment VARCHAR NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN currency VARCHAR NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN application_id BIGINT;
ALTER TABLE request_logs ADD COLUMN environment VARCHAR NOT NULL DEFAULT '';
ALTER TABLE trace_payloads ADD COLUMN application_id BIGINT;
ALTER TABLE trace_payloads ADD COLUMN environment VARCHAR NOT NULL DEFAULT '';

-- Historical attribution is an immutable snapshot, not a foreign key.
CREATE INDEX idx_usage_application_environment ON usage_records (tenant, application_id, environment, created_at);
CREATE INDEX idx_requests_application_environment ON request_logs (tenant, application_id, environment, created_at);
CREATE INDEX idx_trace_application_environment ON trace_payloads (tenant, application_id, environment, created_at);

INSERT INTO role_permissions (role_id, permission)
SELECT r.id, p.permission FROM roles r CROSS JOIN
  (VALUES ('application.read'), ('application.write'), ('budget.read')) AS p(permission)
WHERE r.name = 'tenant-admin'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permissions WHERE permission IN ('application.read', 'application.write', 'budget.read')
  AND role_id IN (SELECT id FROM roles WHERE name = 'tenant-admin');
DROP INDEX IF EXISTS idx_trace_application_environment;
DROP INDEX IF EXISTS idx_requests_application_environment;
DROP INDEX IF EXISTS idx_usage_application_environment;
ALTER TABLE trace_payloads DROP COLUMN environment, DROP COLUMN application_id;
ALTER TABLE request_logs DROP COLUMN environment, DROP COLUMN application_id;
ALTER TABLE usage_records DROP COLUMN currency, DROP COLUMN environment, DROP COLUMN application_id;
-- +goose StatementEnd

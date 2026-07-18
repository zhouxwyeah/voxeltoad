-- +goose Up
-- +goose StatementBegin

-- Attribute each audit row to the AFFECTED tenant (ADR-0019), not the acting
-- operator's tenant. NULL means a global/platform-level action (config CRUD,
-- tenant creation) with no single owning tenant. This lets a tenant-admin read
-- the audit trail of operations against its tenant — including super-admin
-- actions on it — while super-admin sees everything. Nullable, no backfill:
-- pre-existing rows stay NULL (global), which is the correct default for the
-- global config mutations recorded so far.
ALTER TABLE audit_logs ADD COLUMN tenant VARCHAR;

-- Indexes backing the audit read endpoint: (created_at) for the global feed
-- (super-admin, keyset by created_at,id) and (tenant, created_at) for the
-- tenant-scoped feed (tenant-admin).
CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at);
CREATE INDEX idx_audit_logs_tenant_created ON audit_logs (tenant, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_audit_logs_tenant_created;
DROP INDEX IF EXISTS idx_audit_logs_created_at;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS tenant;
-- +goose StatementEnd

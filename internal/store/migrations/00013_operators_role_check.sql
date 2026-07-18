-- +goose Up
-- +goose StatementBegin

-- RBAC Phase-2: drop the hardcoded role CHECK on operators.role that only
-- allowed 'super-admin' and 'tenant-admin'. Custom roles (defined in the roles
-- table) are the new norm; scope-kind ⇔ tenant_id coherence is enforced in the
-- handler layer (operator_handlers.go), not in a DB constraint that can't
-- reference the roles table.

-- 1. Drop the inline column CHECK (Postgres auto-names it; find dynamically).
DO $$
DECLARE
    cname text;
BEGIN
    SELECT c.conname INTO cname
    FROM pg_constraint c
    JOIN pg_class t ON t.oid = c.conrelid
    WHERE t.relname = 'operators' AND c.contype = 'c'
      AND pg_get_constraintdef(c.oid) ILIKE '%super-admin%'
      AND c.conname != 'operator_role_tenant';
    IF cname IS NOT NULL THEN
        EXECUTE 'ALTER TABLE operators DROP CONSTRAINT ' || quote_ident(cname);
    END IF;
END $$;

-- 2. Drop the table-level operator_role_tenant CHECK. It was a guard against
--    wrong role/tenant combinations, but with custom roles it can't enumerate
--    names. The handler layer performs scope-kind validation instead.
ALTER TABLE operators DROP CONSTRAINT IF EXISTS operator_role_tenant;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Re-apply the original constraints (semi-hardcoded because custom roles may
-- still exist in the DB; the Down is NOT expected to run in production).
ALTER TABLE operators ADD CONSTRAINT operator_role_tenant CHECK (
    (role = 'super-admin' AND tenant_id IS NULL) OR
    (role = 'tenant-admin' AND tenant_id IS NOT NULL)
);
ALTER TABLE operators ADD CONSTRAINT operators_role_check
    CHECK (role IN ('super-admin', 'tenant-admin'));

-- +goose StatementEnd

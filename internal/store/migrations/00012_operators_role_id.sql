-- +goose Up
-- +goose StatementBegin

-- Phase 2 of the RBAC migration: add role_id FK to operators, backfill from the
-- existing role text column, and make it NOT NULL. The old role column is kept
-- for rollback safety and will be dropped in a later incremental migration.

-- Step 1: add the nullable FK column.
ALTER TABLE operators ADD COLUMN role_id BIGINT;

-- Step 2: backfill by resolving the existing role text to the seeded role row.
UPDATE operators SET role_id = (
    SELECT r.id FROM roles r WHERE r.name = operators.role
);

-- Safety valve: if any operator had an unknown role string that didn't map to
-- a row in roles, role_id will be NULL and the NOT NULL below will fail with a
-- clear error — which is correct: you must seed the role before migrating.
-- (Test coverage: operatorrepo_dbtest_test.go asserts no null role_id.)

ALTER TABLE operators ALTER COLUMN role_id SET NOT NULL;

-- Step 3: add the FK constraint.
ALTER TABLE operators ADD CONSTRAINT fk_operator_role
    FOREIGN KEY (role_id) REFERENCES roles(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE operators DROP CONSTRAINT IF EXISTS fk_operator_role;
ALTER TABLE operators ALTER COLUMN role_id DROP NOT NULL;
ALTER TABLE operators DROP COLUMN IF EXISTS role_id;

-- +goose StatementEnd

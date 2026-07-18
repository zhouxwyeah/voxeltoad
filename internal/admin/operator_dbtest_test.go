//go:build dbtest

package admin_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"voxeltoad/internal/store"
)

// super-admin creates a tenant-admin operator; the new account can then log in.
// This is the UI-critical path: without it, tenant-admin accounts can only be
// made by hand-inserting DB rows.
func TestOperators_CreateTenantAdminThenLogin(t *testing.T) {
	h, db, saTok := authedAdmin(t)

	// Need a tenant for the tenant-admin to bind to.
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", rr.Code, rr.Body.String())
	}
	var tenant struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &tenant)

	rr = doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "ta@acme", "password": "ta-pass-123456",
		"role": "tenant-admin", "tenant_id": tenant.ID,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create operator status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	// Response must not leak the password/hash.
	var created map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if _, ok := created["password"]; ok {
		t.Error("create response leaked password")
	}
	if _, ok := created["password_hash"]; ok {
		t.Error("create response leaked password_hash")
	}
	if created["email"] != "ta@acme" || created["role"] != "tenant-admin" {
		t.Errorf("created operator = %v, want ta@acme/tenant-admin", created)
	}

	// The new account can authenticate.
	tok := login(t, h, "ta@acme", "ta-pass-123456")
	if tok == "" {
		t.Fatal("newly created tenant-admin could not log in")
	}

	// The mutation was audited (resource_type=operator, affected tenant=acme).
	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action='create' AND resource_type='operator' AND tenant='acme'`,
	).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 1 {
		t.Errorf("operator-create audit rows (tenant=acme) = %d, want 1", count)
	}
}

// Creating a tenant-admin without a tenant_id is rejected at the write boundary
// (400, not a DB CHECK 500).
func TestOperators_TenantAdminRequiresTenant(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "ta@acme", "password": "ta-pass-123456", "role": "tenant-admin",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("tenant-admin without tenant_id status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// A super-admin with a tenant_id is rejected (400): super-admin is global.
func TestOperators_SuperAdminRejectsTenant(t *testing.T) {
	h, db, saTok := authedAdmin(t)
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "sa2@x", "password": "pw-12345678", "role": "super-admin", "tenant_id": tenantID,
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("super-admin with tenant_id status = %d, want 400", rr.Code)
	}
}

// Duplicate email is rejected (4xx, not 500).
func TestOperators_DuplicateEmail(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	body := map[string]any{"email": "dup@x", "password": "pw-12345678", "role": "super-admin"}
	if rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", body); rr.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", body)
	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("duplicate email status = %d, want 4xx", rr.Code)
	}
}

// A tenant-admin may not manage operators (super-admin only) → 403.
func TestOperators_RejectsTenantAdmin(t *testing.T) {
	h, db, saTok := authedAdmin(t)
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123456", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123456")
	_ = saTok

	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/operators", nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin list operators status = %d, want 403", rr.Code)
	}
	rr = doAuth(t, h, taTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "x@y", "password": "pw-12345678", "role": "super-admin",
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin create operator status = %d, want 403", rr.Code)
	}
}

// List returns the envelope and includes the seeded super-admin.
func TestOperators_List(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	rr := doAuth(t, h, saTok, http.MethodGet, "/api/v1/operators", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) < 1 {
		t.Errorf("operators list = %v, want at least the bootstrapped super-admin", rows)
	}
}

// Deleting an operator revokes its sessions: a token issued before deletion no
// longer authenticates.
func TestOperators_DeleteRevokesSessions(t *testing.T) {
	h, db, saTok := authedAdmin(t)
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	// Create a tenant-admin and log it in to get an active session.
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "ta@acme", "password": "ta-pass-123456", "role": "tenant-admin", "tenant_id": tenantID,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create operator: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	taTok := login(t, h, "ta@acme", "ta-pass-123456")

	// The tenant-admin's token works before deletion (hit a tenant-scoped read).
	if rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/usage", nil); rr.Code != http.StatusOK {
		t.Fatalf("pre-delete usage read status = %d, want 200", rr.Code)
	}

	// super-admin deletes the operator.
	if rr := doAuth(t, h, saTok, http.MethodDelete, "/api/v1/operators/"+strconv.FormatInt(created.ID, 10), nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete operator status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}

	// Its session is now invalid.
	if rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/usage", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("post-delete usage read status = %d, want 401 (session revoked)", rr.Code)
	}
}

// The last super-admin cannot be deleted (lockout protection) → 409.
func TestOperators_CannotDeleteLastSuperAdmin(t *testing.T) {
	h, db, saTok := authedAdmin(t)
	var id int64
	if err := db.Raw(`SELECT id FROM operators WHERE role='super-admin' LIMIT 1`).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	rr := doAuth(t, h, saTok, http.MethodDelete, "/api/v1/operators/"+strconv.FormatInt(id, 10), nil)
	if rr.Code != http.StatusConflict {
		t.Errorf("delete last super-admin status = %d, want 409", rr.Code)
	}
}

// Update email returns 200 and the operator is reachable by the new email.
func TestOperators_UpdateEmail(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	// Create an operator to update.
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "old@x", "password": "pw-12345678", "role": "super-admin",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	rr = doAuth(t, h, saTok, http.MethodPut, "/api/v1/operators/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"email": "new@x",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	// Verify response body: email updated, role unchanged, no password leak.
	var updated struct {
		ID       int64  `json:"id"`
		Email    string `json:"email"`
		Role     string `json:"role"`
		TenantID *int64 `json:"tenant_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal update response: %v", err)
	}
	if updated.Email != "new@x" {
		t.Errorf("response email = %q, want new@x", updated.Email)
	}
	if updated.Role != "super-admin" {
		t.Errorf("response role = %q, want super-admin", updated.Role)
	}
	var raw map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &raw)
	if _, leaked := raw["password"]; leaked {
		t.Error("update response leaked password")
	}
	if _, leaked := raw["password_hash"]; leaked {
		t.Error("update response leaked password_hash")
	}

	// Verify via list: new email present, old email gone.
	list := doAuth(t, h, saTok, http.MethodGet, "/api/v1/operators", nil)
	rows := decodeList(t, list)
	foundNew, foundOld := false, false
	for _, r := range rows {
		switch r["email"] {
		case "new@x":
			foundNew = true
		case "old@x":
			foundOld = true
		}
	}
	if !foundNew {
		t.Error("updated email not found in list")
	}
	if foundOld {
		t.Error("old email still present in list after update")
	}
}

// Update password returns 200; the operator can log in with the new password.
func TestOperators_UpdatePassword(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	// Create a tenant and a tenant-admin through the API.
	tr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme-update"})
	if tr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", tr.Code, tr.Body.String())
	}
	var tenant struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(tr.Body.Bytes(), &tenant)

	cr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "ta@acme-update", "password": "old-password",
		"role": "tenant-admin", "tenant_id": tenant.ID,
	})
	if cr.Code != http.StatusCreated {
		t.Fatalf("create operator: %d %s", cr.Code, cr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(cr.Body.Bytes(), &created)

	// Verify pre-update login works.
	if login(t, h, "ta@acme-update", "old-password") == "" {
		t.Fatal("pre-update login failed")
	}

	rr := doAuth(t, h, saTok, http.MethodPut, "/api/v1/operators/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"password": "new-password-strong",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	// Verify response body: email unchanged, no password leak.
	var updated struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal update response: %v", err)
	}
	if updated.Email != "ta@acme-update" {
		t.Errorf("response email = %q, want ta@acme-update", updated.Email)
	}
	if updated.Role != "tenant-admin" {
		t.Errorf("response role = %q, want tenant-admin", updated.Role)
	}
	var raw map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &raw)
	if _, leaked := raw["password"]; leaked {
		t.Error("update response leaked password")
	}

	// Old password should no longer authenticate (use do, not login, because
	// login calls t.Fatalf on failure and we want to continue verifying).
	oldRR := do(t, h, http.MethodPost, "/auth/login", map[string]any{
		"email": "ta@acme-update", "password": "old-password",
	})
	if oldRR.Code != http.StatusUnauthorized {
		t.Errorf("old password login status = %d, want 401; body=%s", oldRR.Code, oldRR.Body.String())
	}
	if login(t, h, "ta@acme-update", "new-password-strong") == "" {
		t.Error("new password does not authenticate after update")
	}
}

// Update tenant_id returns 200; verify via direct DB query.
func TestOperators_UpdateTenantID(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	// Create two tenants through the API.
	t1r := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "alpha"})
	if t1r.Code != http.StatusCreated {
		t.Fatalf("create tenant alpha: %d %s", t1r.Code, t1r.Body.String())
	}
	var t1 struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(t1r.Body.Bytes(), &t1)

	t2r := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "beta"})
	if t2r.Code != http.StatusCreated {
		t.Fatalf("create tenant beta: %d %s", t2r.Code, t2r.Body.String())
	}
	var t2 struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(t2r.Body.Bytes(), &t2)

	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "movable@acme", "password": "pw-12345678", "role": "tenant-admin", "tenant_id": t1.ID,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	rr = doAuth(t, h, saTok, http.MethodPut, "/api/v1/operators/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"tenant_id": t2.ID,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	// Verify response body: tenant_id updated, no password leak.
	var updated struct {
		TenantID *int64 `json:"tenant_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal update response: %v", err)
	}
	if updated.TenantID == nil || *updated.TenantID != t2.ID {
		t.Errorf("response tenant_id = %v, want %d", updated.TenantID, t2.ID)
	}
	var raw map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &raw)
	if _, leaked := raw["password"]; leaked {
		t.Error("update response leaked password")
	}

	// Verify via list: the operator's tenant_id updated to beta.
	listRR := doAuth(t, h, saTok, http.MethodGet, "/api/v1/operators", nil)
	rows := decodeList(t, listRR)
	for _, r := range rows {
		if r["email"] == "movable@acme" {
			tid, _ := r["tenant_id"].(float64)
			if int64(tid) != t2.ID {
				t.Errorf("list tenant_id = %v, want %d", r["tenant_id"], t2.ID)
			}
			return
		}
	}
	t.Error("updated operator not found in list")
}

// Empty body on PUT returns 400.
func TestOperators_UpdateNoFields(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	rr := doAuth(t, h, saTok, http.MethodPut, "/api/v1/operators/1", map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty body status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// Non-existent id returns 404.
func TestOperators_UpdateNotFound(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	rr := doAuth(t, h, saTok, http.MethodPut, "/api/v1/operators/99999", map[string]any{
		"email": "nobody@x",
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing id status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// Phase-2 RBAC: super-admin can create an operator with a custom (non-builtin)
// role. This was blocked before migration 00013 by the hardcoded role CHECK.
func TestOperators_CreateWithCustomRole(t *testing.T) {
	h, db, saTok := authedAdmin(t)

	// Seed a tenant and a custom role.
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('custom-role-acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	customRoleID := seedRole(t, db, "billing-viewer", "tenant", "usage.read")

	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "billing@custom", "password": "pass-12345678",
		"role_id":   customRoleID,
		"tenant_id": tenantID,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create operator with custom role status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// The operator can log in.
	tok := login(t, h, "billing@custom", "pass-12345678")
	if tok == "" {
		t.Fatal("custom-role operator could not log in")
	}

	// Verify the role resolution: operator should have billing-viewer role.
	var role string
	if err := db.Raw(
		`SELECT role FROM operators WHERE email = 'billing@custom'`,
	).Scan(&role).Error; err != nil {
		t.Fatal(err)
	}
	if role != "billing-viewer" {
		t.Errorf("operator role = %q, want billing-viewer", role)
	}
}

// seedRole creates a role with the given name, scope-kind, and permissions
// (idempotent via ON CONFLICT). Returns the role id.
func seedRole(t *testing.T, db *store.DB, name, scopeKind string, perms ...string) int64 {
	t.Helper()
	var id int64
	err := db.Raw(
		`INSERT INTO roles (name, scope_kind, is_builtin, description)
		 VALUES (?, ?, false, 'test custom role')
		 ON CONFLICT (name) DO UPDATE SET scope_kind = excluded.scope_kind
		 RETURNING id`,
		name, scopeKind,
	).Scan(&id).Error
	if err != nil {
		t.Fatalf("seed role %q: %v", name, err)
	}
	for _, perm := range perms {
		_ = db.Exec(
			`INSERT INTO role_permissions (role_id, permission)
			 VALUES (?, ?)
			 ON CONFLICT (role_id, permission) DO NOTHING`,
			id, perm,
		)
	}
	return id
}

// Duplicate email returns 4xx.
func TestOperators_UpdateDuplicateEmail(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	// Create two operators.
	if rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "dup-a@x", "password": "pw-12345678", "role": "super-admin",
	}); rr.Code != http.StatusCreated {
		t.Fatalf("create a: %d %s", rr.Code, rr.Body.String())
	}
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "dup-b@x", "password": "pw-12345678", "role": "super-admin",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create b: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	// Try to rename b to a's email.
	rr = doAuth(t, h, saTok, http.MethodPut, "/api/v1/operators/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"email": "dup-a@x",
	})
	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("duplicate email status = %d, want 4xx; body=%s", rr.Code, rr.Body.String())
	}
}

// A tenant-admin may not update operators (super-admin only) → 403.
func TestOperators_PUTRejectsTenantAdmin(t *testing.T) {
	h, db, _ := authedAdmin(t)
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme-put-403') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	seedTenantAdmin(t, db, "ta@acme-put-403", "ta-pass-123456", tenantID)
	taTok := login(t, h, "ta@acme-put-403", "ta-pass-123456")

	// Tenant-admin must be rejected when trying to update another operator's email.
	rr := doAuth(t, h, taTok, http.MethodPut, "/api/v1/operators/1", map[string]any{
		"email": "hijacked@x",
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin PUT operator status = %d, want 403", rr.Code)
	}
}

// Updating tenant_id to a non-existent tenant triggers a foreign-key violation
// (SQLSTATE 23503). The handler must catch it via isConstraintViolation and
// return 400 (client error), not 500.
func TestOperators_PUTFKViolation(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	// Create a real tenant.
	tr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "fk-tenant"})
	if tr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", tr.Code, tr.Body.String())
	}
	var tenant struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(tr.Body.Bytes(), &tenant)

	// Create a tenant-admin bound to the real tenant.
	cr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": "victim@fk", "password": "pw-12345678",
		"role": "tenant-admin", "tenant_id": tenant.ID,
	})
	if cr.Code != http.StatusCreated {
		t.Fatalf("create operator: %d %s", cr.Code, cr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(cr.Body.Bytes(), &created)

	// Try to re-point it to a non-existent tenant → 400, not 500.
	rr := doAuth(t, h, saTok, http.MethodPut, "/api/v1/operators/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"tenant_id": 99999,
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("FK violation PUT status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	// The operator must still be bound to the original tenant (defence).
	list := doAuth(t, h, saTok, http.MethodGet, "/api/v1/operators", nil)
	rows := decodeList(t, list)
	for _, r := range rows {
		if r["email"] == "victim@fk" {
			tid, _ := r["tenant_id"].(float64)
			if int64(tid) != tenant.ID {
				t.Errorf("operator tenant_id changed to %v despite FK violation", r["tenant_id"])
			}
			return
		}
	}
	t.Error("operator disappeared from list after FK-violation PUT")
}

// Role mutations are audited with resource_type "role" (Phase-2 RBAC).
func TestRoles_AuditCreateAndDelete(t *testing.T) {
	h, db, saTok := authedAdmin(t)

	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "audit-test-role", "scope_kind": "global",
		"description": "test role for audit verification",
		"permissions": []string{"usage.read"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create role status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	// Verify audit entry for create.
	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action='create' AND resource_type='role' AND resource_id = ?`,
		strconv.FormatInt(created.ID, 10),
	).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 1 {
		t.Errorf("role-create audit rows = %d, want 1", count)
	}

	// Verify after payload is captured.
	var afterExists bool
	if err := db.Raw(
		`SELECT after IS NOT NULL FROM audit_logs WHERE action='create' AND resource_type='role' AND resource_id = ?`,
		strconv.FormatInt(created.ID, 10),
	).Scan(&afterExists).Error; err != nil {
		t.Fatalf("check after: %v", err)
	}
	if !afterExists {
		t.Error("role-create audit after payload missing")
	}

	// Update the role (PATCH).
	rr = doAuth(t, h, saTok, http.MethodPatch, "/api/v1/roles/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"description": "updated audit test",
		"permissions": []string{"usage.read", "audit.read"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch role status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action='update' AND resource_type='role' AND resource_id = ?`,
		strconv.FormatInt(created.ID, 10),
	).Scan(&count).Error; err != nil {
		t.Fatalf("count audit update: %v", err)
	}
	if count != 1 {
		t.Errorf("role-update audit rows = %d, want 1", count)
	}

	// Delete the role.
	rr = doAuth(t, h, saTok, http.MethodDelete, "/api/v1/roles/"+strconv.FormatInt(created.ID, 10), nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete role status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action='delete' AND resource_type='role' AND resource_id = ?`,
		strconv.FormatInt(created.ID, 10),
	).Scan(&count).Error; err != nil {
		t.Fatalf("count audit delete: %v", err)
	}
	if count != 1 {
		t.Errorf("role-delete audit rows = %d, want 1", count)
	}
}

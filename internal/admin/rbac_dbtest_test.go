//go:build dbtest

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// seedSuperAdmin creates a super-admin operator with the given credentials.
func seedSuperAdmin(t *testing.T, db *store.DB, email, password string) {
	t.Helper()
	hash, err := operator.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := store.NewOperatorRepo(db).Create(context.Background(), email, hash, operator.RoleSuperAdmin, nil); err != nil {
		t.Fatalf("seed super-admin: %v", err)
	}
}

// seedTenantAdmin creates a tenant-admin operator bound to the given tenant.
func seedTenantAdmin(t *testing.T, db *store.DB, email, password string, tenantID int64) {
	t.Helper()
	hash, err := operator.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := store.NewOperatorRepo(db).Create(context.Background(), email, hash, operator.RoleTenantAdmin, &tenantID); err != nil {
		t.Fatalf("seed tenant-admin: %v", err)
	}
}

// login posts credentials and returns the session token.
func login(t *testing.T, h http.Handler, email, password string) string {
	t.Helper()
	rr := do(t, h, http.MethodPost, "/auth/login", map[string]any{"email": email, "password": password})
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("login response missing token: %s", rr.Body.String())
	}
	return out.Token
}

// doAuth is like do but sets the operator session bearer token.
func doAuth(t *testing.T, h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

// decodeList unmarshals a list response's uniform envelope {data, next_cursor}
// (ADR-0019) and returns the data rows. It fails the test if the body is not a
// well-formed envelope, so bare-array regressions are caught.
func decodeList(t *testing.T, rr *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var env struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode list envelope: %v; body=%s", err, rr.Body.String())
	}
	if env.NextCursor == nil {
		t.Fatalf("list response missing next_cursor field; body=%s", rr.Body.String())
	}
	return env.Data
}

// decodePage unpacks the offset-paginated envelope {data,total,page,page_size}
// used by the audit & request-logs page-jump endpoints. Returns the rows and
// the reported total.
func decodePage(t *testing.T, rr *httptest.ResponseRecorder) ([]map[string]any, int64) {
	t.Helper()
	var env struct {
		Data     []map[string]any `json:"data"`
		Total    int64            `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"page_size"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode page envelope: %v; body=%s", err, rr.Body.String())
	}
	if env.Data == nil {
		t.Fatalf("page response missing data field; body=%s", rr.Body.String())
	}
	return env.Data, env.Total
}

func TestLogin_WrongPasswordRejected(t *testing.T) {
	h, db := newAdmin(t)
	seedSuperAdmin(t, db, "root@x", "s3cret-pass")

	rr := do(t, h, http.MethodPost, "/auth/login", map[string]any{"email": "root@x", "password": "nope"})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rr.Code)
	}
	// Correct password succeeds.
	tok := login(t, h, "root@x", "s3cret-pass")
	if tok == "" {
		t.Error("expected a session token")
	}
}

func TestGlobalCRUD_RequiresAuth(t *testing.T) {
	h, db := newAdmin(t)
	seedSuperAdmin(t, db, "root@x", "pw-123456")

	// No token → 401.
	rr := do(t, h, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}

	// With a super-admin token → 201.
	tok := login(t, h, "root@x", "pw-123456")
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("authenticated create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestGlobalConfig_RejectsTenantAdmin(t *testing.T) {
	h, db := newAdmin(t)

	// Seed a tenant + tenant-admin, then log in.
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	hash, _ := operator.HashPassword("ta-pass-123")
	if _, err := store.NewOperatorRepo(db).Create(context.Background(), "ta@acme", hash, operator.RoleTenantAdmin, &tenantID); err != nil {
		t.Fatalf("seed tenant-admin: %v", err)
	}
	tok := login(t, h, "ta@acme", "ta-pass-123")

	// tenant-admin may not create global config → 403.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin global create status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// TestGlobalConfigReads_TenantAdminScope locks the read-side contract: models
// are the deliberate read-open carve-out (global shared config; the API-key
// form needs aliases → 200), while the other platform-level config/tenancy
// GETs are super-admin only (tenant-admin holds none of provider/route/
// plugin/operator/tenant read perms per migration 00011 → 403). Guards
// against regressions in either direction.
func TestGlobalConfigReads_TenantAdminScope(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme-read') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta-read@acme", "ta-pass-123", tenantID)
	tok := login(t, h, "ta-read@acme", "ta-pass-123")

	if rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/models", nil); rr.Code != http.StatusOK {
		t.Errorf("tenant-admin GET /models status = %d, want 200 (read-open carve-out); body=%s", rr.Code, rr.Body.String())
	}
	for _, p := range []string{"providers", "routes", "plugins", "operators", "tenants"} {
		if rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/"+p, nil); rr.Code != http.StatusForbidden {
			t.Errorf("tenant-admin GET /%s status = %d, want 403; body=%s", p, rr.Code, rr.Body.String())
		}
	}
}

// Every successful mutation writes an audit_logs row; reads do not.
func TestAudit_RecordsMutations(t *testing.T) {
	h, db := newAdmin(t)
	seedSuperAdmin(t, db, "root@x", "pw-123456")
	tok := login(t, h, "root@x", "pw-123456")

	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "audited", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", rr.Code, rr.Body.String())
	}
	// A GET (read) must not add an audit row.
	_ = doAuth(t, h, tok, http.MethodGet, "/api/v1/providers", nil)

	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'create' AND resource_type = 'provider' AND resource_id = 'audited'`,
	).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows = %d, want 1 (create provider audited; reads not audited)", count)
	}

	// The audit row references the operator.
	var operatorPresent bool
	if err := db.Raw(
		`SELECT operator_id IS NOT NULL FROM audit_logs WHERE resource_id = 'audited' LIMIT 1`,
	).Scan(&operatorPresent).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !operatorPresent {
		t.Error("audit row missing operator_id")
	}
}

func TestLogin_LockoutAfterFailures(t *testing.T) {
	h, db := newAdmin(t)
	seedSuperAdmin(t, db, "lock@x", "right-pass-1")

	// Hammer wrong passwords; after the threshold the endpoint locks (429),
	// even for the correct password (ADR-0017: failed-attempt lockout).
	var got429 bool
	for i := 0; i < 10; i++ {
		rr := do(t, h, http.MethodPost, "/auth/login", map[string]any{"email": "lock@x", "password": "wrong"})
		if rr.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("expected lockout (429) after repeated failed logins")
	}
}

// Tenant-scoped groups reject super-admin (requireTenantAdmin middleware).
func TestGroups_RejectsSuperAdmin(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/groups", nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("super-admin GET /groups status = %d, want 403", rr.Code)
	}
}

// /me is available to both roles (no role gate — only authn required).
func TestMe_BothRoles(t *testing.T) {
	h, db := newAdmin(t)

	seedSuperAdmin(t, db, "root@x", "root-pass-123")
	saTok := login(t, h, "root@x", "root-pass-123")

	// super-admin /me → 200.
	rr := doAuth(t, h, saTok, http.MethodGet, "/api/v1/me", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("super-admin /me status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// tenant-admin /me → 200.
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/me", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("tenant-admin /me status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

//go:build dbtest

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// --- API key handler dbtest ---
// API keys are tenant-scoped (tenant-admin creates/revokes). Tests use
// authedAdmin to bootstrap a tenant + tenant-admin, then issue keys via the
// tenant-admin token.

// Create + revoke full lifecycle.
func TestAPIKey_CreateAndRevoke(t *testing.T) {
	h, _, taTok := seededTenantAdmin(t)

	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "k1",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create key: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		KeyID     string `json:"key_id"`
		Plaintext string `json:"api_key"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.KeyID != "k1" {
		t.Errorf("key_id = %q, want k1", created.KeyID)
	}
	if created.Plaintext == "" {
		t.Error("create response missing plaintext api_key")
	}

	// List includes the key.
	list := doAuth(t, h, taTok, http.MethodGet, "/api/v1/api-keys", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	rows := decodeList(t, list)
	found := false
	for _, r := range rows {
		if r["key_id"] == "k1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("created key not found in list")
	}

	// Revoke.
	rr = doAuth(t, h, taTok, http.MethodDelete, "/api/v1/api-keys/k1", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body.String())
	}

	// List excludes the revoked key.
	list = doAuth(t, h, taTok, http.MethodGet, "/api/v1/api-keys", nil)
	rows = decodeList(t, list)
	for _, r := range rows {
		if r["key_id"] == "k1" {
			t.Error("revoked key still present in list")
		}
	}
}

// Create with allowed_models.
func TestAPIKey_CreateWithAllowedModels(t *testing.T) {
	h, db, taTok := seededTenantAdmin(t)

	// Seed allowed models before creating the key.
	if err := db.Exec(`INSERT INTO models (alias, spec) VALUES ('m1', '{}'), ('m2', '{}')`).Error; err != nil {
		t.Fatalf("seed models: %v", err)
	}

	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id":         "k2",
		"allowed_models": []string{"m1", "m2"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	list := doAuth(t, h, taTok, http.MethodGet, "/api/v1/api-keys", nil)
	rows := decodeList(t, list)
	for _, r := range rows {
		if r["key_id"] == "k2" {
			models, _ := r["allowed_models"].([]any)
			if len(models) != 2 {
				t.Errorf("allowed_models = %v, want 2 entries", r["allowed_models"])
			}
			return
		}
	}
	t.Error("key with allowed_models not found in list")
}

// Super-admin is rejected (tenant-scoped resource).
func TestAPIKey_RejectsSuperAdmin(t *testing.T) {
	h, _, saTok := authedAdmin(t)

	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "k3",
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("super-admin create status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// Duplicate key_id returns 4xx.
func TestAPIKey_DuplicateKeyId(t *testing.T) {
	h, _, taTok := seededTenantAdmin(t)

	if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "dup-key",
	}); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "dup-key",
	})
	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("duplicate key_id status = %d, want 4xx; body=%s", rr.Code, rr.Body.String())
	}
}

// Revoke of non-existent key returns 404.
func TestAPIKey_RevokeNonexistent(t *testing.T) {
	h, _, taTok := seededTenantAdmin(t)

	rr := doAuth(t, h, taTok, http.MethodDelete, "/api/v1/api-keys/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("revoke nonexistent status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// Tenant-admin can read models (GET /api/v1/models), but cannot write them
// (POST/PATCH/DELETE remain super-admin only). Models are global shared config;
// the read access is needed for the API-key allowed_models selector.
func TestAPIKey_ModelsRead_AsTenantAdmin(t *testing.T) {
	h, db, taTok := seededTenantAdmin(t)

	// Seed a model so there's something to list.
	if err := db.Exec(`INSERT INTO models (alias, spec) VALUES ('gpt-4', '{}')`).Error; err != nil {
		t.Fatalf("seed model: %v", err)
	}

	// GET models as tenant-admin → 200.
	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/models", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin GET /models status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) == 0 {
		t.Error("tenant-admin got empty model list; expected at least the seeded model")
	}
}

func TestAPIKey_ModelsWrite_AsTenantAdmin(t *testing.T) {
	h, _, taTok := seededTenantAdmin(t)

	// POST models as tenant-admin → 403.
	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "claude",
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin POST /models status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}

	// PATCH models as tenant-admin → 403.
	rr = doAuth(t, h, taTok, http.MethodPatch, "/api/v1/models/gpt-4", map[string]any{
		"upstreams": []any{},
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin PATCH /models status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}

	// DELETE models as tenant-admin → 403.
	rr = doAuth(t, h, taTok, http.MethodDelete, "/api/v1/models/gpt-4", nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin DELETE /models status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// seededTenantAdmin bootstraps a tenant + tenant-admin and returns the
// handler, db, and tenant-admin token for tenant-scoped resource tests.
func seededTenantAdmin(t *testing.T) (http.Handler, *store.DB, string) {
	t.Helper()
	ctx := context.Background()
	h, db, saTok := authedAdmin(t)

	// Create a tenant.
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{
		"name": "acme-api-key-test",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", rr.Code, rr.Body.String())
	}
	var tenantID int64
	if err := db.Raw(`SELECT id FROM tenants WHERE name = 'acme-api-key-test'`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}

	// Seed a tenant-admin and log in.
	hash, _ := operator.HashPassword("ta-pass-123")
	if _, err := store.NewOperatorRepo(db).Create(ctx, "ta@acme-api-key-test", hash, operator.RoleTenantAdmin, &tenantID); err != nil {
		t.Fatalf("seed tenant-admin: %v", err)
	}
	taTok := login(t, h, "ta@acme-api-key-test", "ta-pass-123")
	return h, db, taTok
}

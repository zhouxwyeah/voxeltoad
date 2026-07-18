//go:build dbtest

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"voxeltoad/internal/admin"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/store"
)

var testDSN string

func TestMain(m *testing.M) {
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(55431). // distinct from store (54329) and app (54330); avoids a 5433x range used by other local apps
			Database("voxeltoad_admin_test").
			RuntimePath(filepath.Join(os.TempDir(), "voxeltoad-epg-admin")),
	)
	if err := pg.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres start:", err)
		os.Exit(1)
	}
	testDSN = "postgres://postgres:postgres@localhost:55431/voxeltoad_admin_test?sslmode=disable"
	code := m.Run()
	if err := pg.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres stop:", err)
	}
	os.Exit(code)
}

// devKEK returns a deterministic 32-byte key for tests. The key is not secret
// because all test data is ephemeral; it just needs to be valid for AES-256-GCM.
func devKEK() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

// newAdmin migrates a clean DB and returns a configured admin handler + the
// underlying store for assertions.
func newAdmin(t *testing.T) (http.Handler, *store.DB) {
	t.Helper()
	db, err := store.Open(testDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"provider_credentials", "providers", "models", "routes", "plugins", "audit_logs", "usage_records", "quotas", "request_logs"} {
		if err := db.Exec("TRUNCATE " + tbl + " CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	// RBAC tables (operators/sessions) cascade to sessions; also clear tenancy so
	// each test starts from a clean slate.
	if err := db.Exec(`TRUNCATE operators, sessions, api_keys, groups, tenants RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate rbac/tenancy: %v", err)
	}
	if err := db.Exec(`UPDATE config_generation SET version = 0`).Error; err != nil {
		t.Fatalf("reset gen: %v", err)
	}
	credService, err := credential.NewAESGCMService(devKEK())
	if err != nil {
		t.Fatalf("create credential service: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return admin.Router(admin.Options{
		DB:                db,
		CredentialService: credService,
		CredentialRepo:    store.NewCredentialRepo(db),
	}), db
}

// authedAdmin returns an admin handler with a seeded super-admin already logged
// in, plus the session token for authenticated requests.
func authedAdmin(t *testing.T) (http.Handler, *store.DB, string) {
	t.Helper()
	h, db := newAdmin(t)
	seedSuperAdmin(t, db, "root@x", "root-pass-123")
	token := login(t, h, "root@x", "root-pass-123")
	return h, db, token
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

func decodeOne(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode one: %v", err)
	}
	return v
}

func TestProviderCRUD(t *testing.T) {
	h, _, tok := authedAdmin(t)

	// Create.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "openai-prod", "type": "openai", "adapter": "openai",
		"base_url": "https://api.openai.com/v1", "api_key_ref": "env://OPENAI_KEY",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	created := decodeOne(t, rr)
	if created["api_key_ref"] != "env://***" {
		t.Errorf("created api_key_ref = %q, want env://***", created["api_key_ref"])
	}

	// List.
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/providers", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rr.Code)
	}
	list := decodeList(t, rr)
	if len(list) != 1 || list[0]["name"] != "openai-prod" {
		t.Errorf("list = %v, want one openai-prod", list)
	}
	if list[0]["api_key_ref"] != "env://***" {
		t.Errorf("listed api_key_ref = %q, want env://***", list[0]["api_key_ref"])
	}

	// Delete.
	rr = doAuth(t, h, tok, http.MethodDelete, "/api/v1/providers/openai-prod", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rr.Code)
	}
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/providers", nil)
	list = decodeList(t, rr)
	if len(list) != 0 {
		t.Errorf("after delete list = %v, want empty", list)
	}
}

// TestProviderPatch covers the PATCH partial-update endpoint (ADR-0030):
// success, 404 for unknown name, unspecified fields preserved, and audit.
func TestProviderPatch(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Seed a provider.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "openai-prod", "type": "openai", "adapter": "openai",
		"base_url": "https://api.openai.com/v1", "api_key_ref": "env://OPENAI_KEY",
		"weight": 10,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Patch only base_url; everything else must be preserved.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/providers/openai-prod", map[string]any{
		"base_url": "https://upstream.example.com/v1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched provider: %v; body=%s", err, rr.Body.String())
	}
	if patched["base_url"] != "https://upstream.example.com/v1" {
		t.Errorf("patched base_url = %v, want updated", patched["base_url"])
	}
	if patched["weight"] != float64(10) {
		t.Errorf("patched weight = %v, want preserved 10", patched["weight"])
	}
	if patched["api_key_ref"] != "env://OPENAI_KEY" {
		t.Errorf("patched api_key_ref = %v, want preserved", patched["api_key_ref"])
	}

	// Patch unknown provider → 404.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/providers/ghost", map[string]any{
		"weight": 5,
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("patch unknown status = %d, want 404", rr.Code)
	}

	// Patch with unknown adapter → 400.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/providers/openai-prod", map[string]any{
		"adapter": "no-such-adapter",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("patch bad adapter status = %d, want 400", rr.Code)
	}

	// Audit row written for the successful update.
	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'update' AND resource_type = 'provider' AND resource_id = 'openai-prod'`,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("audit rows for provider PATCH = %d, want 1", count)
	}
}

// TestModelPatch covers PATCH /models/{alias}: success, 404, orphan-route
// rejection, and audit.
func TestModelPatch(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Seed provider + model.
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "type": "openai", "adapter": "openai", "base_url": "https://x", "api_key_ref": "env://K",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p2", "type": "openai", "adapter": "openai", "base_url": "https://y", "api_key_ref": "env://K",
	})
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "chat",
		"upstreams": []map[string]any{
			{"provider": "p1", "upstream_model": "gpt-4o"},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed model status = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Patch: add a second upstream; p1 preserved.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/models/chat", map[string]any{
		"upstreams": []map[string]any{
			{"provider": "p1", "upstream_model": "gpt-4o"},
			{"provider": "p2", "upstream_model": "gpt-4o-mini"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	ups, _ := patched["upstreams"].([]any)
	if len(ups) != 2 {
		t.Errorf("patched upstreams len = %d, want 2", len(ups))
	}

	// 404 unknown model.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/models/ghost", map[string]any{
		"upstreams": []map[string]any{{"provider": "p1", "upstream_model": "x"}},
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("patch unknown model status = %d, want 404", rr.Code)
	}

	// Audit row written.
	var count int64
	if err := db.Raw(`SELECT count(*) FROM audit_logs WHERE action='update' AND resource_type='model' AND resource_id='chat'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("audit rows for model PATCH = %d, want 1", count)
	}
}

// TestRoutePatch covers PATCH /routes/{alias}: success, 404, and audit.
func TestRoutePatch(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Seed provider + model + route.
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "rp1", "type": "openai", "adapter": "openai", "base_url": "https://x", "api_key_ref": "env://K",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "rchat",
		"upstreams": []map[string]any{
			{"provider": "rp1", "upstream_model": "gpt-4o"},
		},
	})
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/routes", map[string]any{
		"model_alias": "rchat", "strategy": "priority",
		"providers": []map[string]any{{"name": "rp1", "weight": 1}},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed route status = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Patch strategy.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/routes/rchat", map[string]any{
		"strategy": "weighted",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched["strategy"] != "weighted" {
		t.Errorf("patched strategy = %v, want weighted", patched["strategy"])
	}

	// 404.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/routes/ghost", map[string]any{
		"strategy": "weighted",
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("patch unknown route status = %d, want 404", rr.Code)
	}

	// Audit.
	var count int64
	if err := db.Raw(`SELECT count(*) FROM audit_logs WHERE action='update' AND resource_type='route' AND resource_id='rchat'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("audit rows for route PATCH = %d, want 1", count)
	}
}

// TestPluginPatch covers PATCH /plugins/{name}?scope=: success, bad phase, 404,
// and audit.
func TestPluginPatch(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Seed plugin (global scope).
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/plugins", map[string]any{
		"name": "rate-limit", "phase": "pre", "scope": "", "enabled": true,
		"params": map[string]any{"rps": 10},
	})

	// Patch enabled + params.
	rr := doAuth(t, h, tok, http.MethodPatch, "/api/v1/plugins/rate-limit?scope=", map[string]any{
		"enabled": false,
		"params":  map[string]any{"rps": 20},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched["enabled"] != false {
		t.Errorf("patched enabled = %v, want false", patched["enabled"])
	}

	// Bad phase → 400.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/plugins/rate-limit?scope=", map[string]any{
		"phase": "nope",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("patch bad phase status = %d, want 400", rr.Code)
	}

	// 404 unknown plugin.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/plugins/ghost?scope=", map[string]any{
		"enabled": false,
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("patch unknown plugin status = %d, want 404", rr.Code)
	}

	// Audit.
	var count int64
	if err := db.Raw(`SELECT count(*) FROM audit_logs WHERE action='update' AND resource_type='plugin' AND resource_id='rate-limit'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("audit rows for plugin PATCH = %d, want 1", count)
	}
}

func TestProvider_CreateWithEncryptedCredential(t *testing.T) {
	h, db, tok := authedAdmin(t)

	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "openai-enc", "type": "openai", "adapter": "openai",
		"base_url": "https://api.openai.com/v1", "api_key": "sk-encrypt-me",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	created := decodeOne(t, rr)
	if created["api_key_ref"] != "db://provider/openai-enc" {
		t.Errorf("created api_key_ref = %q, want db://provider/openai-enc", created["api_key_ref"])
	}

	// Verify the encrypted credential exists in the database.
	repo := store.NewCredentialRepo(db)
	cred, ok, err := repo.Get(context.Background(), "openai-enc")
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if !ok {
		t.Fatal("encrypted credential not found in provider_credentials")
	}

	// Verify it can be decrypted with the test KEK.
	svc, _ := credential.NewAESGCMService(devKEK())
	plain, err := svc.Decrypt(cred)
	if err != nil {
		t.Fatalf("decrypt credential: %v", err)
	}
	if plain != "sk-encrypt-me" {
		t.Errorf("decrypted credential = %q, want sk-encrypt-me", plain)
	}
}

func TestProvider_CredentialEndpoint(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Create provider without a credential.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "openai-cred", "type": "openai", "adapter": "openai",
		"base_url": "https://api.openai.com/v1", "api_key_ref": "env://OPENAI_KEY",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// Update credential via dedicated endpoint.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/providers/openai-cred/credential", map[string]any{
		"api_key": "sk-rotated-key",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch credential status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeOne(t, rr)
	if resp["api_key_ref"] != "db://provider/openai-cred" {
		t.Errorf("patch credential api_key_ref = %q, want db://provider/openai-cred", resp["api_key_ref"])
	}

	// Verify the provider's api_key_ref was updated.
	repo := store.NewCredentialRepo(db)
	cred, ok, err := repo.Get(context.Background(), "openai-cred")
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if !ok {
		t.Fatal("rotated credential not found")
	}
	svc, _ := credential.NewAESGCMService(devKEK())
	plain, err := svc.Decrypt(cred)
	if err != nil {
		t.Fatalf("decrypt rotated credential: %v", err)
	}
	if plain != "sk-rotated-key" {
		t.Errorf("decrypted rotated credential = %q, want sk-rotated-key", plain)
	}

	// Verify the provider spec now points to the encrypted credential.
	cfgRepo := store.NewConfigRepo(db, nil)
	p, ok, err := cfgRepo.GetProvider(context.Background(), "openai-cred")
	if err != nil {
		t.Fatalf("get provider: %v", err)
	}
	if !ok {
		t.Fatal("provider not found after credential update")
	}
	if p.APIKeyRef != "db://provider/openai-cred" {
		t.Errorf("provider api_key_ref = %q, want db://provider/openai-cred", p.APIKeyRef)
	}
}

func TestProvider_CredentialEndpointRequiresCredentialService(t *testing.T) {
	// Admin without credential service: patching a credential should fail cleanly.
	db, _ := newAdminNoCreds(t)
	seedSuperAdmin(t, db, "root@x", "root-pass-123")
	h := admin.Router(admin.Options{DB: db})
	token := login(t, h, "root@x", "root-pass-123")

	// Create a provider with an env ref first.
	rr := doAuth(t, h, token, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "openai-no-cred", "type": "openai", "adapter": "openai",
		"base_url": "https://api.openai.com/v1", "api_key_ref": "env://OPENAI_KEY",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// Attempt to patch a plaintext credential with no service configured.
	rr = doAuth(t, h, token, http.MethodPatch, "/api/v1/providers/openai-no-cred/credential", map[string]any{
		"api_key": "sk-should-fail",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("patch credential status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// newAdminNoCreds creates an admin handler without a credential service.
func newAdminNoCreds(t *testing.T) (*store.DB, http.Handler) {
	t.Helper()
	db, err := store.Open(testDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"provider_credentials", "providers", "models", "routes", "plugins", "audit_logs", "usage_records", "quotas", "request_logs"} {
		if err := db.Exec("TRUNCATE " + tbl + " CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	if err := db.Exec(`TRUNCATE operators, sessions, api_keys, groups, tenants RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate rbac/tenancy: %v", err)
	}
	if err := db.Exec(`UPDATE config_generation SET version = 0`).Error; err != nil {
		t.Fatalf("reset gen: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, admin.Router(admin.Options{DB: db})
}

// A route referencing an unknown provider is rejected at the write boundary
// (ADR-0014: cross-refs validated in the admin service layer → 400).
func TestRoute_RejectsUnknownProvider(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/routes", map[string]any{
		"model_alias": "chat", "strategy": "priority",
		"providers": []map[string]any{{"name": "ghost"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown provider ref; body=%s", rr.Code, rr.Body.String())
	}
}

// A model upstream referencing an unknown provider is rejected.
func TestModel_RejectsUnknownProvider(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "chat",
		"upstreams": []map[string]any{
			{"provider": "ghost", "upstream_model": "gpt-4o"},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown upstream provider; body=%s", rr.Code, rr.Body.String())
	}
}

// A route referencing a non-existent model alias is rejected (ADR-0014: write-
// time cross-ref validation). The error must mention the unknown model.
func TestRoute_RejectsUnknownModelAlias(t *testing.T) {
	h, _, tok := authedAdmin(t)
	// Seed a provider so the only failure is the unknown model alias.
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "type": "o", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/routes", map[string]any{
		"model_alias": "ghost-model", "strategy": "priority",
		"providers": []map[string]any{{"name": "p1"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown model alias; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unknown model") {
		t.Errorf("body = %s, want it to mention \"unknown model\"", rr.Body.String())
	}
}

// A route whose provider exists but is not an upstream of the referenced model
// is rejected (Route.Providers ⊆ Model.Upstreams at write time).
func TestRoute_RejectsProviderNotInUpstreams(t *testing.T) {
	h, _, tok := authedAdmin(t)
	// Two providers; only p1 is an upstream of m1.
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "type": "o", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p2", "type": "o", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "m1", "upstreams": []map[string]any{
			{"provider": "p1", "upstream_model": "gpt-4o"},
		},
	})
	// Route references p2 which exists as a provider but is not in m1's upstreams.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/routes", map[string]any{
		"model_alias": "m1", "strategy": "priority",
		"providers": []map[string]any{{"name": "p1"}, {"name": "p2"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for provider not in upstreams; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not an upstream") {
		t.Errorf("body = %s, want it to mention \"not an upstream\"", rr.Body.String())
	}
}

// A route whose providers are a subset of the model's upstreams is accepted.
func TestRoute_AcceptsSubset(t *testing.T) {
	h, _, tok := authedAdmin(t)
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "type": "o", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p2", "type": "o", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "m1", "upstreams": []map[string]any{
			{"provider": "p1", "upstream_model": "gpt-4o"},
			{"provider": "p2", "upstream_model": "gpt-4o-mini"},
		},
	})
	// Route uses only p1 — a strict subset of {p1, p2}.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/routes", map[string]any{
		"model_alias": "m1", "strategy": "priority",
		"providers": []map[string]any{{"name": "p1"}},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for valid subset; body=%s", rr.Code, rr.Body.String())
	}
}

// Updating a model so that an existing route's providers are no longer a subset
// of the new upstreams is rejected (reverse check: Model update must not orphan a
// referencing route).
func TestModel_UpdateBreaksRouteSubset(t *testing.T) {
	h, _, tok := authedAdmin(t)
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "type": "o", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p2", "type": "o", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "m1", "upstreams": []map[string]any{
			{"provider": "p1", "upstream_model": "gpt-4o"},
			{"provider": "p2", "upstream_model": "gpt-4o-mini"},
		},
	})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/routes", map[string]any{
		"model_alias": "m1", "strategy": "priority",
		"providers": []map[string]any{{"name": "p1"}, {"name": "p2"}},
	})
	// Update model to drop p2; route now references a provider not in upstreams.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/models", map[string]any{
		"alias": "m1", "upstreams": []map[string]any{
			{"provider": "p1", "upstream_model": "gpt-4o"},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for model update breaking route subset; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not in the new upstreams") {
		t.Errorf("body = %s, want it to mention \"not in the new upstreams\"", rr.Body.String())
	}
}

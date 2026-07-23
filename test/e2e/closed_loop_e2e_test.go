//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"voxeltoad/internal/admin"
	"voxeltoad/internal/app"
	"voxeltoad/internal/auth"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/operator"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/proxy"
	"voxeltoad/internal/store"
	"voxeltoad/test/testsupport"
)

// TestClosedLoop_ChatCompletion exercises the full integration wired in step 7
// and step 8: admin persists config and serves a versioned snapshot; the data
// plane polls it, assembles a dispatcher, authenticates against the PG KeyStore,
// pre-debits + settles quota, forwards to the (mock) upstream, and records usage.
//
//	client → gateway(router→auth→billing.Pre→dispatcher→forwarder) → mock upstream
//	         → billing.Post(settle+usage) → response
func TestClosedLoop_ChatCompletion(t *testing.T) {
	ctx := context.Background()

	// --- shared embedded PostgreSQL (started once in TestMain), reset per test ---
	truncateAll(t)
	dsn := sharedDSN
	db := sharedDB

	// --- mock upstream (OpenAI-compatible) ---
	const upstreamBody = `{"id":"chatcmpl-e2e","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello from upstream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`
	var upstreamHits int
	mock := testsupport.NewMockUpstream(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		if got := r.Header.Get("Authorization"); got != "Bearer sk-upstream" {
			t.Errorf("upstream Authorization = %q, want Bearer sk-upstream", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamBody))
	})
	defer mock.Close()

	// --- admin plane (config CRUD + snapshot), backed by the same DB ---
	adminSrv := httptest.NewServer(admin.Router(admin.Options{DB: db}))
	defer adminSrv.Close()

	// Bootstrap a super-admin and log in (global config CRUD requires operator
	// auth — ADR-0017). The real path uses `voxeltoad-admin bootstrap`; here we seed it.
	adminToken := bootstrapAndLogin(t, db, adminSrv.URL, "root@e2e", "root-pass-e2e")

	// Seed config through the admin REST API (the real management path).
	adminPost(t, adminSrv.URL, adminToken, "/api/v1/providers", config.Provider{
		Name: "mock-openai", Type: "openai",
		Endpoints: []config.ProviderEndpoint{{ID: "openai", Adapter: "openai", BaseURL: mock.URL()}},
		APIKeyRef: "plain://sk-upstream",
		Timeouts:  config.ProviderTimeouts{Connect: 2 * time.Second, FirstByte: 2 * time.Second, Overall: 5 * time.Second},
	})
	adminPost(t, adminSrv.URL, adminToken, "/api/v1/models", config.Model{
		Alias: "chat",
		Upstreams: []config.ModelUpstream{{
			Provider: "mock-openai", UpstreamModel: "gpt-4o",
			Pricing: config.Pricing{PromptPer1M: 1_000_000, CompletionPer1M: 2_000_000, Currency: "usd"},
		}},
	})
	adminPost(t, adminSrv.URL, adminToken, "/api/v1/routes", config.Route{
		ModelAlias: "chat", Strategy: "priority",
		Providers: []config.RouteProvider{{Name: "mock-openai"}},
	})

	// Seed tenancy + API key + quota directly (admin RBAC/key issuance is 7.6).
	const plaintextKey = "sk-client-e2e"
	seedKeyAndQuota(t, db, plaintextKey, "acme", "team-a", "key_e2e")

	// --- data plane, wired exactly like cmd/gateway ---
	cfgStore := config.NewStore()
	poller := config.NewPoller(adminSrv.URL, 200*time.Millisecond, cfgStore)
	pollCtx, cancelPoll := context.WithCancel(ctx)
	defer cancelPoll()
	if err := poller.Start(pollCtx); err != nil {
		t.Fatalf("poller initial fetch: %v", err)
	}

	stores, err := app.OpenStores(dsn, app.StoreOptions{UsageBuffer: 16})
	if err != nil {
		t.Fatalf("open stores: %v", err)
	}
	defer func() { _ = stores.Close() }()

	authn := auth.NewAuthenticator(stores.KeyStore, auth.Options{})
	billingPlugin := billing.NewPlugin(cfgStore.Current, stores.Quota, stores.UsageRecorder)
	chain := plugin.NewChain(billingPlugin)

	dispWatcher := app.NewDispatcherWatcher(cfgStore.Current, proxy.DispatcherConfig{})
	if err := dispWatcher.Build(); err != nil {
		t.Fatalf("dispatcher build: %v", err)
	}

	gateway := httptest.NewServer(proxy.Router(nil,
		proxy.WithAuth(authn),
		proxy.WithPlugins(chain),
		proxy.WithDispatcherProvider(dispWatcher.Current),
	))
	defer gateway.Close()

	// --- drive an authenticated chat request through the whole stack ---
	reqBody := `{"model":"chat","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Authorization", "Bearer "+plaintextKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	var chat struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &chat); err != nil {
		t.Fatalf("decode chat response: %v; body=%s", err, body)
	}
	if len(chat.Choices) != 1 || chat.Choices[0].Message.Content != "hello from upstream" {
		t.Errorf("response content = %+v, want upstream content", chat.Choices)
	}
	if chat.Usage.PromptTokens != 11 || chat.Usage.CompletionTokens != 7 {
		t.Errorf("usage = %+v, want 11/7 from upstream", chat.Usage)
	}
	if upstreamHits != 1 {
		t.Errorf("upstream hits = %d, want 1", upstreamHits)
	}

	// --- assert quota was debited (settled to exact cost) ---
	// cost = 11/1_000_000*1_000_000 + 7/1_000_000*2_000_000 = 11 + 14 = 25 micro-units (round-half-up).
	// tenant:acme started at 1_000_000 → 999_975.
	quotaRepo := store.NewQuotaRepo(db)
	bal, err := quotaRepo.Balance(ctx, "tenant:acme")
	if err != nil {
		t.Fatalf("read quota balance: %v", err)
	}
	if bal != 1_000_000-25 {
		t.Errorf("tenant:acme balance = %d, want %d (debited exact cost 25)", bal, 1_000_000-25)
	}

	// --- assert a usage record was written (async; poll briefly) ---
	waitFor(t, 2*time.Second, func() bool {
		var n int
		if err := db.Raw(`SELECT count(*) FROM usage_records WHERE tenant = 'acme'`).Scan(&n).Error; err != nil {
			t.Fatalf("count usage: %v", err)
		}
		return n == 1
	}, "usage_records row for tenant acme")

	var provider string
	var cost int64
	if err := db.Raw(`SELECT provider, cost FROM usage_records WHERE tenant = 'acme'`).Row().Scan(&provider, &cost); err != nil {
		t.Fatalf("read usage record: %v", err)
	}
	if provider != "mock-openai" || cost != 25 {
		t.Errorf("usage record provider/cost = %s/%d, want mock-openai/25", provider, cost)
	}
}

// TestClosedLoop_RejectsUnknownKey verifies auth rejects an unseeded key (401)
// through the same wiring, without needing the full config.
func TestClosedLoop_RejectsUnknownKey(t *testing.T) {
	truncateAll(t)
	dsn := sharedDSN

	stores, err := app.OpenStores(dsn, app.StoreOptions{})
	if err != nil {
		t.Fatalf("open stores: %v", err)
	}
	defer func() { _ = stores.Close() }()

	authn := auth.NewAuthenticator(stores.KeyStore, auth.Options{})
	gateway := httptest.NewServer(proxy.Router(nil, proxy.WithAuth(authn)))
	defer gateway.Close()

	req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"chat","messages":[]}`)))
	req.Header.Set("Authorization", "Bearer sk-nonexistent")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for unknown key", resp.StatusCode)
	}
}

// --- helpers ---

func adminPost(t *testing.T, baseURL, token, path string, payload any) {
	t.Helper()
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("admin POST %s status = %d, want 201; body=%s", path, resp.StatusCode, body)
	}
}

// bootstrapAndLogin seeds a super-admin operator (as `voxeltoad-admin bootstrap`
// would) and logs in, returning the session token.
func bootstrapAndLogin(t *testing.T, db *store.DB, baseURL, email, password string) string {
	t.Helper()
	hash, err := operator.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := store.NewOperatorRepo(db).Create(context.Background(), email, hash, operator.RoleSuperAdmin, nil); err != nil {
		t.Fatalf("seed super-admin: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := http.Post(baseURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
		t.Fatalf("login response missing token")
	}
	return out.Token
}

func seedKeyAndQuota(t *testing.T, db *store.DB, plaintextKey, tenant, group, keyID string) {
	t.Helper()
	sum := sha256.Sum256([]byte(plaintextKey))
	hash := hex.EncodeToString(sum[:])

	var tenantID, groupID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES (?) RETURNING id`, tenant).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := db.Raw(`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`, tenantID, group).Scan(&groupID).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, allowed_models)
		 VALUES (?, ?, ?, ?, '[]'::jsonb)`,
		keyID, hash, tenantID, groupID,
	).Error; err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	// Fund the tenant scope so quota pre-debit/settle succeeds.
	if err := store.NewQuotaRepo(db).SetBalance(context.Background(), "tenant:"+tenant, 1_000_000, "usd"); err != nil {
		t.Fatalf("seed quota: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

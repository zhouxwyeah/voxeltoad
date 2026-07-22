//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"voxeltoad/internal/admin"
	"voxeltoad/internal/app"
	"voxeltoad/internal/auth"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/plugin/ratelimit"
	"voxeltoad/internal/proxy"
	"voxeltoad/internal/store"
)

// Harness is a fully-wired data-plane environment for e2e tests: embedded
// PostgreSQL, the admin plane (config CRUD + snapshot) and the data-plane
// gateway (poller → auth → billing → dispatcher), assembled exactly as
// cmd/gateway wires them. Tests seed config/keys/quota through the real admin
// API + helpers, then drive requests at GatewayURL.
type Harness struct {
	t           *testing.T
	DB          *store.DB
	stores      *app.Stores
	cancelPoll  context.CancelFunc
	cfgStore    *config.Store
	dispWatcher *app.DispatcherWatcher

	AdminURL   string
	AdminToken string // super-admin session token
	GatewayURL string

	pollInterval time.Duration
}

// HarnessOption configures optional data-plane features when building the
// harness (applied before the gateway is assembled).
type HarnessOption func(*harnessConfig)

type harnessConfig struct {
	rateLimits *ratelimit.Limits
	dispCfg    proxy.DispatcherConfig
}

// WithRateLimits installs a rate-limit plugin (in front of billing) in the
// harness gateway, mirroring cmd/gateway's wiring.
func WithRateLimits(l ratelimit.Limits) HarnessOption {
	return func(c *harnessConfig) { c.rateLimits = &l }
}

// WithDispatcherConfig overrides the dispatcher's circuit-breaker config
// (failure threshold / cooldown), letting breaker tests use a low threshold and
// short cooldown instead of waiting on the production defaults (5 failures /
// 30s, see internal/proxy/breaker.go).
func WithDispatcherConfig(cfg proxy.DispatcherConfig) HarnessOption {
	return func(c *harnessConfig) { c.dispCfg = cfg }
}

// NewHarness brings up the full stack and returns a ready Harness. It registers
// t.Cleanup to tear everything down. A super-admin is bootstrapped and logged in
// (AdminToken).
func NewHarness(t *testing.T, opts ...HarnessOption) *Harness {
	t.Helper()

	var hc harnessConfig
	for _, o := range opts {
		o(&hc)
	}

	// Reset the shared database to a clean state; the embedded PostgreSQL is
	// started once per package in TestMain (see main_test.go) and reused across
	// tests, so each test wipes tables instead of paying a fresh-instance cost.
	truncateAll(t)
	dsn := sharedDSN
	db := sharedDB

	// Admin plane.
	adminSrv := httptest.NewServer(admin.Router(admin.Options{DB: db}))
	adminToken := bootstrapAndLogin(t, db, adminSrv.URL, "root@e2e", "root-pass-e2e")

	// Data plane, wired like cmd/gateway.
	cfgStore := config.NewStore()
	pollInterval := 100 * time.Millisecond
	poller := config.NewPoller(adminSrv.URL, pollInterval, cfgStore)
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	if err := poller.Start(pollCtx); err != nil {
		cancelPoll()
		adminSrv.Close()
		t.Fatalf("poller initial fetch: %v", err)
	}

	stores, err := app.OpenStores(dsn, app.StoreOptions{UsageBuffer: 64})
	if err != nil {
		cancelPoll()
		adminSrv.Close()
		t.Fatalf("open stores: %v", err)
	}

	authn := auth.NewAuthenticator(stores.KeyStore, auth.Options{})
	billingPlugin := billing.NewPlugin(cfgStore.Current, stores.Quota, stores.UsageRecorder)
	var plugins []plugin.Plugin
	if hc.rateLimits != nil {
		plugins = append(plugins, ratelimit.NewPlugin(ratelimit.NewMemoryLimiter(), *hc.rateLimits))
	}
	plugins = append(plugins, billingPlugin)
	chain := plugin.NewChain(plugins...)
	dispWatcher := app.NewDispatcherWatcher(cfgStore.Current, hc.dispCfg)
	_ = dispWatcher.Build() // best-effort; refreshed by the watcher as config arrives
	go dispWatcher.Watch(pollCtx, pollInterval)

	gatewaySrv := httptest.NewServer(proxy.Router(nil,
		proxy.WithAuth(authn),
		proxy.WithPlugins(chain),
		proxy.WithDispatcherProvider(dispWatcher.Current),
		proxy.WithAuditRecorder(stores.RequestLog),
	))

	h := &Harness{
		t: t, DB: db, stores: stores, cancelPoll: cancelPoll,
		cfgStore: cfgStore, dispWatcher: dispWatcher,
		AdminURL: adminSrv.URL, AdminToken: adminToken, GatewayURL: gatewaySrv.URL,
		pollInterval: pollInterval,
	}

	t.Cleanup(func() {
		gatewaySrv.Close()
		cancelPoll()
		_ = stores.Close()
		adminSrv.Close()
		// Note: db is the package-shared sharedDB — it is closed in TestMain,
		// not here. Only per-test resources (servers, poller, stores) are torn
		// down; the embedded PostgreSQL persists for the next test.
	})
	return h
}

// --- config seeding (through the real admin API) ---

// AddProvider registers a provider pointing at the given upstream base URL.
func (h *Harness) AddProvider(name, baseURL, apiKeyRef string) {
	h.t.Helper()
	h.AddProviderWithAdapter(name, baseURL, apiKeyRef, "openai")
}

// AddProviderWithAdapter is AddProvider with an explicit adapter name
// ("openai" / "claude"). Used by protocol-aware-routing tests (ADR-0047) that
// need a claude-adapter provider to exercise the Anthropic passthrough path.
func (h *Harness) AddProviderWithAdapter(name, baseURL, apiKeyRef, adapter string) {
	h.t.Helper()
	h.adminPost("/api/v1/providers", config.Provider{
		Name: name, Type: "openai", Adapter: adapter,
		BaseURL: baseURL, APIKeyRef: apiKeyRef,
		Timeouts: config.ProviderTimeouts{Connect: 2 * time.Second, FirstByte: 2 * time.Second, Overall: 5 * time.Second},
	})
}

// AddModel registers a model alias served by the given upstreams. Pricing is the
// same for all upstreams here (per-million-token micro-units).
func (h *Harness) AddModel(alias string, promptPer1M, completionPer1M int64, upstreams ...config.ModelUpstream) {
	h.t.Helper()
	for i := range upstreams {
		upstreams[i].Pricing = config.Pricing{PromptPer1M: promptPer1M, CompletionPer1M: completionPer1M, Currency: "usd"}
	}
	h.adminPost("/api/v1/models", config.Model{Alias: alias, Upstreams: upstreams})
}

// AddRoute registers a route for a model alias over ordered candidate providers.
func (h *Harness) AddRoute(alias, strategy string, providers ...config.RouteProvider) {
	h.t.Helper()
	h.adminPost("/api/v1/routes", config.Route{ModelAlias: alias, Strategy: strategy, Providers: providers})
}

// SeedKey creates a tenant/group/api-key directly (SQL) and returns the plaintext
// key. allowedModels restricts the key (nil/empty = all).
func (h *Harness) SeedKey(plaintext, tenant, group, keyID string, allowedModels []string) {
	h.t.Helper()
	seedKeyFull(h.t, h.DB, plaintext, tenant, group, keyID, allowedModels, nil)
}

// SeedKeyExpired creates a key that expired in the past.
func (h *Harness) SeedKeyExpired(plaintext, tenant, group, keyID string) {
	h.t.Helper()
	past := time.Now().Add(-time.Hour)
	seedKeyFull(h.t, h.DB, plaintext, tenant, group, keyID, nil, &past)
}

// DisableTenant flips a tenant's enabled flag to false directly via SQL,
// rejecting every API key under it at the next authentication lookup (see
// store.KeyRepo.LookupByHash) without touching the keys themselves.
func (h *Harness) DisableTenant(tenant string) {
	h.t.Helper()
	if err := h.DB.Exec(`UPDATE tenants SET enabled = false WHERE name = ?`, tenant).Error; err != nil {
		h.t.Fatalf("disable tenant %s: %v", tenant, err)
	}
}

// SetQuota funds a scope's balance in micro-units.
func (h *Harness) SetQuota(scope string, balance int64) {
	h.t.Helper()
	if err := store.NewQuotaRepo(h.DB).SetBalance(context.Background(), scope, balance, "usd"); err != nil {
		h.t.Fatalf("set quota %s: %v", scope, err)
	}
}

// Balance reads a quota scope's current balance.
func (h *Harness) Balance(scope string) int64 {
	h.t.Helper()
	bal, err := store.NewQuotaRepo(h.DB).Balance(context.Background(), scope)
	if err != nil {
		h.t.Fatalf("balance %s: %v", scope, err)
	}
	return bal
}

// SyncConfig blocks until the data plane has caught up to the admin plane's
// current config version and rebuilds the dispatcher synchronously, so a
// just-seeded provider/model/route is guaranteed live before the test drives a
// request. This removes poll-timing flakiness.
func (h *Harness) SyncConfig() {
	h.t.Helper()
	want := h.adminSnapshotVersion()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cur := h.cfgStore.Current(); cur != nil && cur.Version == want {
			h.dispWatcher.Refresh() // rebuild dispatcher from the caught-up config
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("data plane did not reach config version %q within timeout", want)
}

// adminSnapshotVersion reads the current snapshot ETag/version from the admin
// plane (the authoritative config generation).
func (h *Harness) adminSnapshotVersion() string {
	h.t.Helper()
	resp, err := http.Get(h.AdminURL + "/internal/config/snapshot")
	if err != nil {
		h.t.Fatalf("read admin snapshot: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var snap struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		h.t.Fatalf("decode snapshot: %v", err)
	}
	return snap.Version
}

// --- request helpers ---

// Chat sends a chat-completions request with the given bearer key. stream toggles
// SSE. The caller owns closing resp.Body.
func (h *Harness) Chat(key, model string, stream bool) *http.Response {
	return h.ChatWithHeaders(key, model, stream, nil)
}

// ChatWithHeaders is Chat with additional request headers (e.g. a session header
// for affinity routing). The caller owns closing resp.Body.
func (h *Harness) ChatWithHeaders(key, model string, stream bool, headers map[string]string) *http.Response {
	h.t.Helper()
	return h.ChatMessages(key, model, stream, []map[string]string{{"role": "user", "content": "hi"}}, headers)
}

// ChatMessages is Chat with an explicit message list (e.g. a multi-turn
// conversation history) and additional request headers. The caller owns
// closing resp.Body.
func (h *Harness) ChatMessages(key, model string, stream bool, messages []map[string]string, headers map[string]string) *http.Response {
	h.t.Helper()
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   stream,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.GatewayURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("chat request: %v", err)
	}
	return resp
}

func (h *Harness) adminPost(path string, payload any) {
	h.t.Helper()
	adminPost(h.t, h.AdminURL, h.AdminToken, path, payload)
}

// --- shared low-level helpers ---

// seedKeyFull inserts a tenant/group/api_key with optional allowed_models and
// expiry, computing the SHA-256 hash the auth layer expects.
func seedKeyFull(t *testing.T, db *store.DB, plaintext, tenant, group, keyID string, allowedModels []string, expiresAt *time.Time) {
	t.Helper()
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])

	var tenantID, groupID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES (?) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, tenant).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := db.Raw(`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`, tenantID, group).Scan(&groupID).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if allowedModels == nil {
		allowedModels = []string{}
	}
	models, _ := json.Marshal(allowedModels)
	if err := db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, expires_at, allowed_models)
		 VALUES (?, ?, ?, ?, ?, ?::jsonb)`,
		keyID, hash, tenantID, groupID, expiresAt, string(models),
	).Error; err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
}

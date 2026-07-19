//go:build adminstack

package main

// Demo data seeding for the adminstack dev environment. seedDemoData populates
// a realistic starter set (providers/models/routes, a tenant+group+api key, a
// tenant-admin operator, and a funded quota) so the Control Panel is not empty
// on a fresh start. It is idempotent: every store call uses ON CONFLICT
// upsert/DO-NOTHING, so re-running on a persisted database neither fails nor
// clobbers rows. Disabling via GATEWAY_SEED_DEMO=0 leaves only the super-admin.
//
// Mirrors the proven seeding patterns from cmd/devstack (seedKey) and the e2e
// harness — no new SQL, only the existing store-layer methods.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"voxeltoad/internal/config"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// seedDemoData populates the dev dataset. It returns a short human-readable
// summary of the seeded accounts (for the ready banner) and an error only when
// a store call genuinely fails.
func seedDemoData(ctx context.Context, db *store.DB, credService credential.Service) (string, error) {
	cfgRepo := store.NewConfigRepo(db, nil) // snapshot repo optional for dev seed
	credRepo := store.NewCredentialRepo(db)

	// --- 1. Providers + their encrypted upstream credentials ---
	// seedUpstreamKey returns the upstream key for a provider from its
	// GATEWAY_SEED_*_KEY env var. Empty means "no real key configured" — the
	// provider row is still upserted but no credential is written, so the
	// admin plane shows the provider while upstream calls fail with a clear
	// auth error until the operator configures a real key.
	type prov struct {
		name    string
		typ     string
		adapter string
		baseURL string
		envKey  string
	}
	providers := []prov{
		{name: "深度求索", typ: "deepseek", adapter: "openai", baseURL: "https://api.deepseek.com", envKey: "GATEWAY_SEED_DEEPSEEK_KEY"},
		{name: "TokenHub", typ: "tencent", adapter: "openai", baseURL: "https://tokenhub.tencentmaas.com/v1", envKey: "GATEWAY_SEED_TOKENHUB_KEY"},
		{name: "Kimi-code", typ: "Kimi", adapter: "openai", baseURL: "https://api.kimi.com/coding/v1", envKey: "GATEWAY_SEED_KIMI_KEY"},
		{name: "GLM", typ: "zhipu", adapter: "openai", baseURL: "https://open.bigmodel.cn/api/coding/paas/v4", envKey: "GATEWAY_SEED_GLM_KEY"},
	}
	providerTimeouts := config.ProviderTimeouts{
		Connect: 2 * time.Second, FirstByte: 5 * time.Second, Overall: 30 * time.Second,
	}
	for _, p := range providers {
		if err := cfgRepo.UpsertProvider(ctx, config.Provider{
			Name: p.name, Type: p.typ, Adapter: p.adapter,
			BaseURL: p.baseURL, APIKeyRef: config.DBProviderRef(p.name),
			Timeouts: providerTimeouts, Weight: 100,
		}); err != nil {
			return "", fmt.Errorf("seed provider %q: %w", p.name, err)
		}
		// Optional real upstream key from GATEWAY_SEED_*_KEY; skipped when
		// unset. The adminstack dev KEK encrypts it exactly as the real admin
		// plane would, so the resolve path works end-to-end when a key is
		// provided. Replace via the Control Panel to rotate.
		key := strings.TrimSpace(os.Getenv(p.envKey))
		if key == "" {
			continue // skip credential row; provider visible but uncallable
		}
		enc, err := credService.Encrypt(key)
		if err != nil {
			return "", fmt.Errorf("encrypt credential for %q: %w", p.name, err)
		}
		if err := credRepo.Upsert(ctx, p.name, enc); err != nil {
			return "", fmt.Errorf("seed credential for %q: %w", p.name, err)
		}
	}

	// --- 2. Models (alias → provider upstream + pricing) ---
	// Pricing rates are micro-units per MILLION tokens (ADR-0013); the literal
	// values are 1000× the prior per-1K values to keep the real-world $ price
	// unchanged (e.g. 2500 micro/1K → 2_500_000 micro/1M == $2.5 per 1M).
	mk := func(alias, provider, upstream string, prompt, completion int64) config.Model {
		return config.Model{
			Alias: alias,
			Upstreams: []config.ModelUpstream{{
				Provider: provider, UpstreamModel: upstream,
				Pricing: config.Pricing{PromptPer1M: prompt, CompletionPer1M: completion, Currency: "usd"},
			}},
		}
	}
	models := []config.Model{
		mk("deepseek-v4-flash", "深度求索", "deepseek-v4-flash", 2_500_000, 10_000_000),
		mk("deepseek-v4-pro", "深度求索", "deepseek-v4-pro", 150_000, 600_000),
		mk("deepseek-v4-flash", "TokenHub", "deepseek-v4-flash", 2_500_000, 10_000_000),
		mk("deepseek-v4-pro", "TokenHub", "deepseek-v4-pro", 150_000, 600_000),
		mk("hy3", "TokenHub", "hy3", 150_000, 600_000),
		mk("kimi-for-coding", "Kimi-code", "kimi-for-coding", 150_000, 600_000),
		mk("glm-5.2", "GLM", "glm-5.2", 150_000, 600_000),
		// default-chat demonstrates cross-provider failover: both upstreams under
		// one alias, the priority route lists openai first.
		{
			Alias: "deepseek-v4-flash",
			Upstreams: []config.ModelUpstream{
				{Provider: "深度求索", UpstreamModel: "deepseek-v4-flash", Pricing: config.Pricing{PromptPer1M: 2_500_000, CompletionPer1M: 10_000_000, Currency: "usd"}},
				// {Provider: "TokenHub", UpstreamModel: "deepseek-v4-flash", Pricing: config.Pricing{PromptPer1M: 3_000_000, CompletionPer1M: 15_000_000, Currency: "usd"}},
			},
		},
	}
	for _, m := range models {
		if err := cfgRepo.UpsertModel(ctx, m); err != nil {
			return "", fmt.Errorf("seed model %q: %w", m.Alias, err)
		}
	}

	// --- 3. Routes (strategy + ordered candidate providers) ---
	route := func(alias, strategy string, names ...string) config.Route {
		rps := make([]config.RouteProvider, len(names))
		for i, n := range names {
			rps[i] = config.RouteProvider{Name: n, Weight: 1}
		}
		return config.Route{ModelAlias: alias, Strategy: strategy, Providers: rps}
	}
	routes := []config.Route{
		route("deepseek-v4-flash", "priority", "深度求索"),
		route("deepseek-v4-pro", "round_robin", "深度求索"),
		// route("hy3", "session_affinity", "TokenHub"),
		route("glm-5.2", "session_affinity", "GLM"),
		route("kimi-for-coding", "session_affinity", "Kimi-code"),
	}
	for _, rt := range routes {
		if err := cfgRepo.UpsertRoute(ctx, rt); err != nil {
			return "", fmt.Errorf("seed route %q: %w", rt.ModelAlias, err)
		}
	}

	// --- 4. Tenant + group + a known-plaintext API key ---
	const (
		tenantName   = "demo-tenant"
		groupName    = "default"
		keyPlaintext = "sk-demo-tenant-key-0001"
		keyID        = "demo-key-001"
	)
	tenantID, err := store.CreateTenant(ctx, db, tenantName)
	if err != nil {
		return "", fmt.Errorf("seed tenant: %w", err)
	}
	tRepo := store.NewTenantRepo(db, tenantID)
	groupID, err := tRepo.CreateGroup(ctx, groupName)
	if err != nil {
		return "", fmt.Errorf("seed group: %w", err)
	}
	sum := sha256.Sum256([]byte(keyPlaintext))
	allowedModels := []string{"deepseek-v4-pro", "deepseek-v4-flash", "glm-5.2", "kimi-for-coding"}
	if err := tRepo.CreateAPIKey(ctx, store.APIKeySpec{
		KeyID: keyID, Hash: hex.EncodeToString(sum[:]),
		GroupID: &groupID, AllowedModels: allowedModels,
	}); err != nil {
		return "", fmt.Errorf("seed api key: %w", err)
	}

	// --- 5. Tenant-admin operator (login-able, scoped to the demo tenant) ---
	const (
		tenantAdminEmail    = "tenant-admin@demo"
		tenantAdminPassword = "demo-pass-123"
	)
	hash, err := operator.HashPassword(tenantAdminPassword)
	if err != nil {
		return "", fmt.Errorf("hash tenant-admin password: %w", err)
	}
	if _, err := store.NewOperatorRepo(db).Create(ctx, tenantAdminEmail, hash, operator.RoleTenantAdmin, &tenantID); err != nil {
		// On a persisted re-run the operator already exists (email UNIQUE); that is
		// the expected idempotent outcome, not a failure.
		if !isUniqueViolation(err) {
			return "", fmt.Errorf("seed tenant-admin: %w", err)
		}
	}

	// --- 6. Fund the demo tenant's quota (micro-units, matches devstack) ---
	if err := store.NewQuotaRepo(db).SetBalance(ctx, "tenant:"+tenantName, 1_000_000_000, "usd"); err != nil {
		return "", fmt.Errorf("seed quota: %w", err)
	}

	aliases := make([]string, 0, len(models))
	for _, m := range models {
		aliases = append(aliases, m.Alias)
	}
	summary := fmt.Sprintf(`
  --- demo data ---
  tenant-admin %s / %s
  tenant       %s  (group %q, quota funded)
  api key      %s  (key_id %s, models: %s)
  models       %s`,
		tenantAdminEmail, tenantAdminPassword,
		tenantName, groupName,
		keyPlaintext, keyID, strings.Join(allowedModels, ", "),
		strings.Join(aliases, ", "))
	return summary, nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (used to treat duplicate-insert on idempotent re-runs as success).
// Mirrors internal/admin's isConstraintViolation convention.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}

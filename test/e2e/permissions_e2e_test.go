//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"testing"

	"voxeltoad/internal/config"
)

// setupChatModel wires one working provider + model + route named "chat" and
// returns after the config is live. Callers seed their own keys/quota.
func (h *Harness) setupChatModel(t *testing.T) *int {
	t.Helper()
	hits := new(int)
	up := jsonUpstream("ok", 4, 4, hits)
	t.Cleanup(up.Close)
	h.AddProvider("p1", up.URL(), "plain://k")
	// DefaultMaxTokens drives the billing Pre-estimate when a request omits
	// max_tokens (realistic config; Claude requires it — ADR-0009).
	h.AddModel("chat", 1_000_000, 1_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o", DefaultMaxTokens: 256})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	return hits
}

// A valid key whose AllowedModels lists the requested alias is permitted.
func TestPerm_AllowedModelPermitted(t *testing.T) {
	h := NewHarness(t)
	h.setupChatModel(t)
	h.SeedKey("sk-allow", "acme", "team", "key_allow", []string{"chat"})
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-allow", "chat", false)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("status = %d, want 200 for allowed model; body=%s", resp.StatusCode, body)
	}
	_ = resp.Body.Close()
}

// A key restricted to other models is rejected 403 when it requests "chat".
func TestPerm_DisallowedModelForbidden(t *testing.T) {
	h := NewHarness(t)
	h.setupChatModel(t)
	h.SeedKey("sk-deny", "acme", "team", "key_deny", []string{"other-model"})
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-deny", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403 for disallowed model; body=%s", resp.StatusCode, body)
	}
}

// An unknown key is rejected 401.
func TestPerm_UnknownKeyUnauthorized(t *testing.T) {
	h := NewHarness(t)
	h.setupChatModel(t)
	h.SyncConfig()

	resp := h.Chat("sk-does-not-exist", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for unknown key", resp.StatusCode)
	}
}

// A key whose tenant has been disabled is rejected 401, even though the key
// itself was never revoked — disabling a tenant is reversible (unlike
// api_keys.revoked_at) and rejects every key under it at the auth boundary.
func TestPerm_DisabledTenantUnauthorized(t *testing.T) {
	h := NewHarness(t)
	h.setupChatModel(t)
	h.SeedKey("sk-disabled-tenant", "acme-disabled", "team", "key_disabled_tenant", nil)
	h.SetQuota("tenant:acme-disabled", 1_000_000)
	h.SyncConfig()
	h.DisableTenant("acme-disabled")

	resp := h.Chat("sk-disabled-tenant", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 401 for a key under a disabled tenant; body=%s", resp.StatusCode, body)
	}
}

// An expired key is rejected 401.
func TestPerm_ExpiredKeyUnauthorized(t *testing.T) {
	h := NewHarness(t)
	h.setupChatModel(t)
	h.SeedKeyExpired("sk-expired", "acme", "team", "key_expired")
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-expired", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for expired key", resp.StatusCode)
	}
}

// A key with an exhausted quota is rejected 402 (insufficient quota).
func TestPerm_OutOfQuotaPaymentRequired(t *testing.T) {
	h := NewHarness(t)
	h.setupChatModel(t)
	h.SeedKey("sk-broke", "broke-tenant", "team", "key_broke", nil)
	h.SetQuota("tenant:broke-tenant", 1) // 1 micro-unit: below any estimate
	h.SyncConfig()

	resp := h.Chat("sk-broke", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 402 for exhausted quota; body=%s", resp.StatusCode, body)
	}
}

// Boundary: a model with NO DefaultMaxTokens + a request with NO max_tokens must
// still enforce quota — the global max-tokens ceiling drives a non-zero
// reservation, so a broke tenant is rejected 402 instead of slipping through
// with a zero estimate (ADR-0013 quota-bypass fix).
func TestPerm_QuotaEnforcedWithoutMaxTokens(t *testing.T) {
	h := NewHarness(t)
	// Model whose upstream sets NO DefaultMaxTokens.
	up := jsonUpstream("ok", 4, 4, nil)
	t.Cleanup(up.Close)
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-broke2", "broke2", "team", "key_broke2", nil)
	h.SetQuota("tenant:broke2", 1) // below the ceiling-driven estimate
	h.SyncConfig()

	// Request omits max_tokens (harness Chat sends none).
	resp := h.Chat("sk-broke2", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 402 (quota enforced via global ceiling); body=%s", resp.StatusCode, body)
	}
}

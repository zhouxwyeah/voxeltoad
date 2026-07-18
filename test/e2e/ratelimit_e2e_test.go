//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"testing"
	"time"

	"voxeltoad/internal/plugin/ratelimit"
)

// With a per-tenant RPM limit, requests within the limit succeed and the one
// over the limit is rejected 429 (rate_limit_error) — ADR-0008. The ratelimit
// plugin runs in Pre, before billing, so an over-limit request is rejected
// without touching quota.
func TestRateLimit_TenantRPMRejectsOverLimit(t *testing.T) {
	h := NewHarness(t, WithRateLimits(ratelimit.Limits{TenantRPM: 3, Window: time.Minute}))
	h.setupChatModel(t)
	h.SeedKey("sk-rl", "rl-tenant", "team", "key_rl", nil)
	h.SetQuota("tenant:rl-tenant", 1_000_000_000)
	h.SyncConfig()

	// First 3 requests are within the limit → 200.
	for i := 0; i < 3; i++ {
		resp := h.Chat("sk-rl", "chat", false)
		code := resp.StatusCode
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (within RPM limit)", i+1, code)
		}
	}

	// The 4th request in the same window exceeds RPM → 429.
	resp := h.Chat("sk-rl", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("over-limit request status = %d, want 429; body=%s", resp.StatusCode, body)
	}
}

// A different tenant is unaffected by another tenant's limit (per-identity
// dimensions).
func TestRateLimit_IsolatedPerTenant(t *testing.T) {
	h := NewHarness(t, WithRateLimits(ratelimit.Limits{TenantRPM: 1, Window: time.Minute}))
	h.setupChatModel(t)
	h.SeedKey("sk-a", "tenant-a", "team", "key_a", nil)
	h.SeedKey("sk-b", "tenant-b", "team", "key_b", nil)
	h.SetQuota("tenant:tenant-a", 1_000_000_000)
	h.SetQuota("tenant:tenant-b", 1_000_000_000)
	h.SyncConfig()

	// tenant-a uses its single allowed request.
	respA := h.Chat("sk-a", "chat", false)
	_, _ = io.Copy(io.Discard, respA.Body)
	_ = respA.Body.Close()
	if respA.StatusCode != http.StatusOK {
		t.Fatalf("tenant-a first request = %d, want 200", respA.StatusCode)
	}
	// tenant-a is now over limit.
	respA2 := h.Chat("sk-a", "chat", false)
	_, _ = io.Copy(io.Discard, respA2.Body)
	_ = respA2.Body.Close()
	if respA2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("tenant-a second request = %d, want 429", respA2.StatusCode)
	}
	// tenant-b still has its own quota of requests → 200.
	respB := h.Chat("sk-b", "chat", false)
	defer func() { _ = respB.Body.Close() }()
	if respB.StatusCode != http.StatusOK {
		t.Errorf("tenant-b request = %d, want 200 (isolated from tenant-a)", respB.StatusCode)
	}
}

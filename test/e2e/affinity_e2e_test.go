//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"testing"

	"voxeltoad/internal/config"
)

// setupTwoProviderAffinityRoute wires two providers under a session_affinity
// route named "chat", returning their hit counters.
func (h *Harness) setupTwoProviderAffinityRoute(t *testing.T) (aHits, bHits *int) {
	t.Helper()
	aHits, bHits = new(int), new(int)
	a := jsonUpstream("from-a", 4, 4, aHits)
	t.Cleanup(a.Close)
	b := jsonUpstream("from-b", 4, 4, bHits)
	t.Cleanup(b.Close)

	h.AddProvider("pa", a.URL(), "plain://k")
	h.AddProvider("pb", b.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000,
		config.ModelUpstream{Provider: "pa", UpstreamModel: "gpt-4o", DefaultMaxTokens: 128},
		config.ModelUpstream{Provider: "pb", UpstreamModel: "gpt-4o", DefaultMaxTokens: 128},
	)
	h.AddRoute("chat", "session_affinity",
		config.RouteProvider{Name: "pa"}, config.RouteProvider{Name: "pb"})
	return aHits, bHits
}

// All requests carrying the same session header stick to one provider (so its
// prompt-cache prefix keeps hitting) — ADR-0018.
func TestAffinity_SameSessionSticksToOneProvider(t *testing.T) {
	h := NewHarness(t)
	aHits, bHits := h.setupTwoProviderAffinityRoute(t)
	h.SeedKey("sk-aff", "acme", "team", "key_aff", nil)
	h.SetQuota("tenant:acme", 100_000_000)
	h.SyncConfig()

	const session = "sess-sticky-1"
	for i := 0; i < 12; i++ {
		resp := h.ChatWithHeaders("sk-aff", "chat", false, map[string]string{"X-Voxeltoad-Session": session})
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// Exactly one provider served all 12; the other saw none.
	if !((*aHits == 12 && *bHits == 0) || (*bHits == 12 && *aHits == 0)) {
		t.Errorf("session did not stick to one provider: a=%d b=%d (want 12/0 or 0/12)", *aHits, *bHits)
	}
}

// Distinct sessions spread across both providers (affinity is per-session, not a
// single global pin). With enough distinct keys, HRW should exercise both.
func TestAffinity_DistinctSessionsSpread(t *testing.T) {
	h := NewHarness(t)
	aHits, bHits := h.setupTwoProviderAffinityRoute(t)
	h.SeedKey("sk-aff", "acme", "team", "key_aff", nil)
	h.SetQuota("tenant:acme", 100_000_000)
	h.SyncConfig()

	for i := 0; i < 40; i++ {
		// session ids must pass validateSessionID (≥8 word/hyphen chars) since
		// the gateway rejects malformed values to prevent affinity hijack.
		session := "sess-" + string(rune('A'+i%26)) + string(rune('0'+i/26)) + "-aff"
		resp := h.ChatWithHeaders("sk-aff", "chat", false, map[string]string{"X-Voxeltoad-Session": session})
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	if *aHits == 0 || *bHits == 0 {
		t.Errorf("distinct sessions did not spread across providers: a=%d b=%d", *aHits, *bHits)
	}
	if *aHits+*bHits != 40 {
		t.Errorf("total = %d, want 40", *aHits+*bHits)
	}
}

// A given session repeated across separate calls always maps to the same
// provider — determinism across requests (not just within one loop).
func TestAffinity_DeterministicAcrossRequests(t *testing.T) {
	h := NewHarness(t)
	aHits, bHits := h.setupTwoProviderAffinityRoute(t)
	h.SeedKey("sk-aff", "acme", "team", "key_aff", nil)
	h.SetQuota("tenant:acme", 100_000_000)
	h.SyncConfig()

	// First request for a session establishes which provider it maps to.
	resp := h.ChatWithHeaders("sk-aff", "chat", false, map[string]string{"X-Voxeltoad-Session": "det-session"})
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	firstA, firstB := *aHits, *bHits

	// Subsequent requests for the same session go to the same provider only.
	for i := 0; i < 5; i++ {
		resp := h.ChatWithHeaders("sk-aff", "chat", false, map[string]string{"X-Voxeltoad-Session": "det-session"})
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	// Whichever provider took the first request took all subsequent ones.
	if firstA == 1 && *bHits != 0 {
		t.Errorf("session flipped to pb: a=%d b=%d", *aHits, *bHits)
	}
	if firstB == 1 && *aHits != 0 {
		t.Errorf("session flipped to pa: a=%d b=%d", *aHits, *bHits)
	}
}

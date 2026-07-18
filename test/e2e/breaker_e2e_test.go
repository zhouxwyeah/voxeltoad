//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"

	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
)

// TestBreaker_TripsAndSkipsUnhealthyProvider drives repeated requests through
// the full stack (admin -> poller -> DispatcherWatcher -> Dispatcher) against a
// permanently-failing primary and a healthy backup, and asserts the circuit
// breaker actually takes effect end-to-end: once the primary's failure count
// reaches the configured threshold, subsequent requests skip it entirely
// (ADR-0011) rather than merely failing over to it on every request. This
// complements the internal/proxy unit tests (breaker_test.go,
// dispatcher_test.go), which exercise the breaker in isolation but never
// through the real HTTP/config-sync path.
func TestBreaker_TripsAndSkipsUnhealthyProvider(t *testing.T) {
	h := NewHarness(t, WithDispatcherConfig(proxy.DispatcherConfig{
		FailureThreshold: 2,
		Cooldown:         30 * time.Second,
	}))

	var badHits, goodHits int
	bad := failingUpstream(http.StatusInternalServerError, &badHits)
	defer bad.Close()
	good := jsonUpstream("from backup", 5, 5, &goodHits)
	defer good.Close()

	h.AddProvider("bad", bad.URL(), "plain://k")
	h.AddProvider("good", good.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000,
		config.ModelUpstream{Provider: "bad", UpstreamModel: "gpt-4o"},
		config.ModelUpstream{Provider: "good", UpstreamModel: "gpt-4o"},
	)
	h.AddRoute("chat", "priority",
		config.RouteProvider{Name: "bad"}, config.RouteProvider{Name: "good"})
	h.SeedKey("sk-brk", "acme", "team", "key_brk", nil)
	h.SetQuota("tenant:acme", 1_000_000_000)
	h.SyncConfig()

	// First FailureThreshold (2) requests: each fails over bad -> good and
	// records a failure against bad. The second failure trips bad's breaker.
	for i := 0; i < 2; i++ {
		resp := h.Chat("sk-brk", "chat", false)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("req %d: status = %d, want 200 (failed over to good)", i+1, resp.StatusCode)
		}
	}
	if badHits != 2 || goodHits != 2 {
		t.Fatalf("after warmup: hits bad=%d good=%d, want 2/2", badHits, goodHits)
	}

	// Third request: bad's breaker is now open, so the router should skip it
	// and go straight to good — bad is not hit again.
	resp := h.Chat("sk-brk", "chat", false)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("req 3: status = %d, want 200", resp.StatusCode)
	}
	if badHits != 2 {
		t.Errorf("bad hits after breaker should be open = %d, want still 2 (skipped)", badHits)
	}
	if goodHits != 3 {
		t.Errorf("good hits = %d, want 3", goodHits)
	}
}

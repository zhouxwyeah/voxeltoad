//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"voxeltoad/internal/config"
)

// TestDisconnect_Settlement_RefundsQuota drives a streaming chat to first chunk,
// then cancels the request context (simulating client disconnect), and asserts
// the quota pre-debit is reconciled/refunded rather than leaked.
//
// Regression guard for B1: settle used to run with the cancelled request ctx,
// silently failing the refund UPDATE and permanently leaking the reservation.
func TestDisconnect_Settlement_RefundsQuota(t *testing.T) {
	h := NewHarness(t)

	var hits int
	up := sseUpstream("partial stream", 5, 3, &hits)
	defer up.Close()

	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "openai", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-disc", "acme", "team-a", "key_disc", nil)
	const initial = 1_000_000_000
	h.SetQuota("tenant:acme", initial)
	h.SyncConfig()

	// Build a cancellable request so we can simulate mid-stream client disconnect.
	reqCtx, cancel := context.WithCancel(context.Background())
	body := strings.NewReader(`{"model":"chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, h.GatewayURL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer sk-disc")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Read until we get the first content chunk, then cancel (client disconnect).
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	gotChunk := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") && !strings.Contains(line, "[DONE]") {
			gotChunk = true
			break
		}
	}
	if !gotChunk {
		t.Fatal("expected at least one SSE chunk before disconnect")
	}

	// Simulate client disconnect: cancel the request context. The server's
	// stream loop will see the write error on the next flush; the deferred
	// runPost must still settle (refund the unused reservation).
	cancel()

	// Drain whatever's left so the server can observe the write error and run
	// its deferred cleanup. Then close the body.
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	// Give the async settlement a moment to land (Settle is synchronous in the
	// Post phase, but the request_logs recorder is async).
	time.Sleep(300 * time.Millisecond)

	// The reservation was for the completion estimate (max-tokens ceiling ×
	// completion rate). Actual usage was small (5 prompt + 3 completion).
	// After settle, the balance must be back near initial — NOT still missing
	// the full reservation.
	bal := h.Balance("tenant:acme")
	if bal <= 0 {
		t.Fatalf("balance = %d, expected refund to have landed", bal)
	}
	// The reservation was debited at Pre; after refunding (reserved - actual),
	// the net debit should be just the actual cost (~5*1_000_000/1_000_000 + 3*2_000_000/1_000_000
	// = 5 + 6 = 11 micro-units). Allow generous slack for the estimate vs
	// actual delta — the key assertion is "not the full reservation leaked".
	netDebit := initial - bal
	if netDebit <= 0 {
		t.Fatalf("net debit = %d, expected a positive (possibly tiny) charge", netDebit)
	}
	// If the bug is present, netDebit == full reservation (thousands of
	// micro-units). After the fix, netDebit is just the actual cost (~11).
	// Use a threshold well below any plausible estimate but well above actual.
	const estimateFloor = 100 // actual is ~11; any sane reservation is >>100
	if netDebit > estimateFloor {
		t.Errorf("net debit = %d, expected ~actual cost (≈11); reservation likely leaked (B1 regression)", netDebit)
	}

	// request_logs must also have a row (the audit recorder is independent of
	// the quota store and should always append).
	var n int64
	if err := h.DB.Raw(`SELECT count(*) FROM request_logs WHERE tenant = 'acme'`).Scan(&n).Error; err != nil {
		t.Fatalf("count request_logs: %v", err)
	}
	if n == 0 {
		t.Error("expected a request_logs row even after client disconnect")
	}
}

// TestNonStream_UpstreamError_RefundsQuota asserts the non-streaming failure
// path also reconciles the reservation when the upstream returns a 5xx. Same
// regression concern: Post must not use the (possibly cancelled) request ctx.
func TestNonStream_UpstreamError_RefundsQuota(t *testing.T) {
	h := NewHarness(t)

	// Upstream that returns 500 on every request (retryable → failover exhausted → 502).
	var hits int
	up := failingUpstream(http.StatusInternalServerError, &hits)
	defer up.Close()

	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "openai", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-err", "acme", "team-a", "key_err", nil)
	const initial = 1_000_000_000
	h.SetQuota("tenant:acme", initial)
	h.SyncConfig()

	resp := h.Chat("sk-err", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}

	time.Sleep(300 * time.Millisecond)
	bal := h.Balance("tenant:acme")
	// No usage → full refund → balance back to initial.
	if bal != initial {
		t.Errorf("balance = %d, want %d (full refund on upstream failure; B1 regression)", bal, initial)
	}
}

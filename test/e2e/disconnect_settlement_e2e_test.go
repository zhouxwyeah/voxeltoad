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
	"voxeltoad/test/testsupport"
)

// Cancellation must still run durable cleanup with a detached context. If the
// upstream was called but never supplied usage, cleanup retains an explicit
// unknown reservation rather than inventing a zero-cost refund.
func TestDisconnect_Settlement_RetainsUnknown(t *testing.T) {
	h := NewHarness(t)
	up := testsupport.NewMockUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"partial\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		// Do not race cancellation against a complete stream. This upstream
		// deliberately cannot return usage or a terminal chunk.
		<-r.Context().Done()
	})
	defer up.Close()
	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "openai", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-disc", "acme", "team-a", "key_disc", nil)
	const initial = 1_000_000_000
	h.SetQuota("tenant:acme", initial)
	h.SyncConfig()

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := strings.NewReader(`{"model":"chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, h.GatewayURL+"/v1/chat/completions", body)
	if err != nil {
		t.Fatal(err)
	}
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
	scanner := bufio.NewScanner(resp.Body)
	gotChunk := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") && strings.Contains(scanner.Text(), "partial") {
			gotChunk = true
			break
		}
	}
	if !gotChunk {
		t.Fatal("expected a content chunk before disconnect")
	}
	cancel()
	_ = resp.Body.Close()
	assertUnknownReservation(t, h, initial)
}

func TestNonStream_UpstreamError_RetainsUnknown(t *testing.T) {
	h := NewHarness(t)
	up := testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"truncated",`)
	})
	defer up.Close()
	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "openai", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-err", "acme", "team-a", "key_err", nil)
	const initial = 1_000_000_000
	h.SetQuota("tenant:acme", initial)
	h.SyncConfig()
	enterpriseResponse(t, h.Chat("sk-err", "chat", false), http.StatusBadGateway)
	assertUnknownReservation(t, h, initial)
}

func assertUnknownReservation(t *testing.T, h *Harness, initial int64) {
	t.Helper()
	waitFor(t, 3*time.Second, func() bool {
		var n int64
		if err := h.DB.Raw(`SELECT count(*) FROM billing_reservations WHERE tenant_id = (SELECT id FROM tenants WHERE name='acme') AND status = 'unknown' AND reason <> ''`).Scan(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n == 1
	}, "durable unknown reservation after upstream failure")
	if got := h.Balance("tenant:acme"); got != initial-8192 {
		t.Errorf("balance = %d, want %d (retain 4096 output-token estimate at 2 micros)", got, initial-8192)
	}
	enterpriseWaitRows(t, h, "request_logs", "acme", 1)
	var usages int64
	if err := h.DB.Table("usage_records").Where("tenant = ?", "acme").Count(&usages).Error; err != nil {
		t.Fatal(err)
	}
	if usages != 0 {
		t.Errorf("failure without usage fabricated %d usage records", usages)
	}
}

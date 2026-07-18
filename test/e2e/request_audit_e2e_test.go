//go:build e2e

package e2e

import (
	"testing"
	"time"

	"voxeltoad/internal/config"
)

// TestRequestAudit_SuccessWritesRow drives a full chat through the harness and
// asserts the data-plane request-audit ledger (request_logs) gains a complete
// row: identity, provider, model, tokens, and a non-negative duration. The
// recorder is async/fail-open, so we poll briefly.
func TestRequestAudit_SuccessWritesRow(t *testing.T) {
	h := NewHarness(t)

	var hits int
	up := jsonUpstream("hi from upstream", 11, 7, &hits)
	defer up.Close()

	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "openai", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-audit", "acme", "team-a", "key_audit", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-audit", "chat", false)
	_ = resp.Body.Close()

	waitForRequestLog(t, h, "acme", func(r requestLogRow) bool {
		return r.Provider == "openai" && r.TotalTokens == 18 && r.ErrorType == ""
	}, "success row with provider=openai, total_tokens=18")

	row := readLastRequestLog(t, h, "acme")
	if row.ModelRequested != "chat" {
		t.Errorf("model_requested = %q, want chat", row.ModelRequested)
	}
	if row.APIKeyID != "key_audit" {
		t.Errorf("api_key_id = %q, want key_audit", row.APIKeyID)
	}
	if row.PromptTokens != 11 || row.CompletionTokens != 7 {
		t.Errorf("tokens = %d/%d, want 11/7", row.PromptTokens, row.CompletionTokens)
	}
	if row.ModelResolved != "gpt-4o" {
		t.Errorf("model_resolved = %q, want gpt-4o (routing-layer resolved upstream name)", row.ModelResolved)
	}
	if row.Fallback {
		t.Error("fallback = true, want false (single candidate, first try)")
	}
}

// TestRequestAudit_FailoverWritesFallbackRow drives a request that fails over
// from a bad provider to a good one and asserts the audit row captures the
// resolved upstream model name of the provider that actually served the
// request and fallback=true (ADR-0021 / design/observability.md llm.fallback).
func TestRequestAudit_FailoverWritesFallbackRow(t *testing.T) {
	h := NewHarness(t)

	var badHits, goodHits int
	bad := failingUpstream(500, &badHits)
	defer bad.Close()
	good := jsonUpstream("from backup", 5, 5, &goodHits)
	defer good.Close()

	h.AddProvider("bad", bad.URL(), "plain://k")
	h.AddProvider("good", good.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000,
		config.ModelUpstream{Provider: "bad", UpstreamModel: "gpt-4o-bad-upstream"},
		config.ModelUpstream{Provider: "good", UpstreamModel: "gpt-4o-good-upstream"},
	)
	h.AddRoute("chat", "priority",
		config.RouteProvider{Name: "bad"}, config.RouteProvider{Name: "good"})
	h.SeedKey("sk-fo-audit", "fallback-co", "team", "key_fo_audit", nil)
	h.SetQuota("tenant:fallback-co", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-fo-audit", "chat", false)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 after failover", resp.StatusCode)
	}

	waitForRequestLog(t, h, "fallback-co", func(r requestLogRow) bool {
		return r.Provider == "good" && r.Fallback
	}, "fallback row with provider=good, fallback=true")

	row := readLastRequestLog(t, h, "fallback-co")
	if row.ModelResolved != "gpt-4o-good-upstream" {
		t.Errorf("model_resolved = %q, want gpt-4o-good-upstream", row.ModelResolved)
	}
	if badHits < 1 || goodHits != 1 {
		t.Errorf("hits bad=%d good=%d, want bad≥1 good=1", badHits, goodHits)
	}
}

// TestRequestAudit_RejectionWritesRow asserts a quota-rejected request (no
// upstream call) is still audited, with the error_type recorded.
func TestRequestAudit_RejectionWritesRow(t *testing.T) {
	h := NewHarness(t)

	var hits int
	up := jsonUpstream("should not be reached", 1, 1, &hits)
	defer up.Close()

	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "openai", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-audit2", "broke", "team-a", "key_audit2", nil)
	h.SetQuota("tenant:broke", 0) // no balance → pre-debit rejects with 402
	h.SyncConfig()

	resp := h.Chat("sk-audit2", "chat", false)
	_ = resp.Body.Close()
	if resp.StatusCode != 402 {
		t.Fatalf("status = %d, want 402 (out of quota)", resp.StatusCode)
	}

	waitForRequestLog(t, h, "broke", func(r requestLogRow) bool {
		return r.ErrorType == "insufficient_quota"
	}, "rejection row with error_type=insufficient_quota")

	if hits != 0 {
		t.Errorf("upstream hits = %d, want 0 (rejected before dispatch)", hits)
	}
}

// --- request_logs read helpers ---

type requestLogRow struct {
	APIKeyID         string `gorm:"column:api_key_id"`
	Provider         string
	ModelRequested   string `gorm:"column:model_requested"`
	ModelResolved    string `gorm:"column:model_resolved"`
	PromptTokens     int    `gorm:"column:prompt_tokens"`
	CompletionTokens int    `gorm:"column:completion_tokens"`
	TotalTokens      int    `gorm:"column:total_tokens"`
	ErrorType        string `gorm:"column:error_type"`
	Fallback         bool
}

func readLastRequestLog(t *testing.T, h *Harness, tenant string) requestLogRow {
	t.Helper()
	var row requestLogRow
	err := h.DB.Raw(
		`SELECT api_key_id, provider, model_requested, model_resolved,
		        prompt_tokens, completion_tokens, total_tokens, error_type, fallback
		 FROM request_logs WHERE tenant = ? ORDER BY created_at DESC, id DESC LIMIT 1`,
		tenant,
	).Scan(&row).Error
	if err != nil {
		t.Fatalf("read request_logs: %v", err)
	}
	return row
}

func waitForRequestLog(t *testing.T, h *Harness, tenant string, cond func(requestLogRow) bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		if err := h.DB.Raw(`SELECT count(*) FROM request_logs WHERE tenant = ?`, tenant).Scan(&n).Error; err == nil && n > 0 {
			if cond(readLastRequestLog(t, h, tenant)) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for request_logs: %s", what)
}

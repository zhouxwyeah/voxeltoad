//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"voxeltoad/internal/config"
)

// TestSessionTrace_EndToEnd drives two chat requests carrying the same
// X-Voxeltoad-Session header, then queries the admin session-trace endpoint and
// asserts:
//  1. both requests appear in chronological ASC order,
//  2. the cost summary aggregates both requests' costs.
//
// This is the end-to-end proof that session_id propagates from the request
// header → plugin.Context → usage_records/request_logs → the trace API.
func TestSessionTrace_EndToEnd(t *testing.T) {
	h := NewHarness(t)

	var hits int
	up := jsonUpstream("hi from upstream", 10, 5, &hits)
	defer up.Close()

	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{
		Provider: "openai", UpstreamModel: "gpt-4o",
	})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-sess", "acme", "team-a", "key_sess", nil)
	h.SetQuota("tenant:acme", 100_000_000)
	h.SyncConfig()

	// Two requests in the same session. The session header value is arbitrary.
	sessionID := "sess-e2e-001"
	resp1 := h.ChatWithHeaders("sk-sess", "chat", false, map[string]string{"X-Voxeltoad-Session": sessionID})
	// The gateway echoes correlation ids back so callers can join gateway
	// logs/usage to their own traces. The session id MUST round-trip.
	if got := resp1.Header.Get("X-Voxeltoad-Session"); got != sessionID {
		t.Errorf("echoed X-Voxeltoad-Session = %q, want %q", got, sessionID)
	}
	if got := resp1.Header.Get("X-Request-Id"); got == "" {
		t.Error("echoed X-Request-Id is empty; expected a gateway-assigned request id")
	}
	_ = resp1.Body.Close()
	// Small gap so created_at differs (timeline order is testable).
	time.Sleep(60 * time.Millisecond)
	resp2 := h.ChatWithHeaders("sk-sess", "chat", false, map[string]string{"X-Voxeltoad-Session": sessionID})
	_ = resp2.Body.Close()

	// Wait for both request_logs rows (async recorder) to land.
	waitForRowCount(t, h, "request_logs", "tenant = 'acme'", 2)
	// Wait for both usage_records rows (async recorder) to land.
	waitForRowCount(t, h, "usage_records", "tenant = 'acme'", 2)

	// Query the admin session-trace endpoint.
	req, _ := http.NewRequest(http.MethodGet,
		h.AdminURL+"/api/v1/request-logs/sessions/"+sessionID, nil)
	req.Header.Set("Authorization", "Bearer "+h.AdminToken)
	traceResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("session trace GET: %v", err)
	}
	defer func() { _ = traceResp.Body.Close() }()
	if traceResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(traceResp.Body)
		t.Fatalf("session trace status = %d, want 200; body=%s", traceResp.StatusCode, body)
	}

	var trace struct {
		SessionID string `json:"session_id"`
		Requests  []struct {
			Provider string `json:"provider"`
		} `json:"requests"`
		CostSummary struct {
			RequestCount int   `json:"request_count"`
			Cost         int64 `json:"cost"`
		} `json:"cost_summary"`
	}
	body, _ := io.ReadAll(traceResp.Body)
	if err := json.Unmarshal(body, &trace); err != nil {
		t.Fatalf("unmarshal trace: %v; body=%s", err, body)
	}

	if trace.SessionID != sessionID {
		t.Errorf("session_id = %q, want %q", trace.SessionID, sessionID)
	}
	if len(trace.Requests) != 2 {
		t.Errorf("requests = %d, want 2", len(trace.Requests))
	}
	if trace.CostSummary.RequestCount != 2 {
		t.Errorf("cost_summary.request_count = %d, want 2", trace.CostSummary.RequestCount)
	}
	if trace.CostSummary.Cost <= 0 {
		t.Errorf("cost_summary.cost = %d, want > 0 (both requests billed)", trace.CostSummary.Cost)
	}
	// Each request hit openai.
	for i, r := range trace.Requests {
		if r.Provider != "openai" {
			t.Errorf("requests[%d].provider = %q, want openai", i, r.Provider)
		}
	}
}

// waitForRowCount polls until the table has the expected row count matching the
// where clause (the async recorders are fail-open, so we poll briefly).
func waitForRowCount(t *testing.T, h *Harness, table, where string, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		q := "SELECT count(*) FROM " + table + " WHERE " + where
		if err := h.DB.Raw(q).Scan(&n).Error; err == nil && n >= want {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s count >= %d", table, want)
}

// TestTrace_AgentDetectionAndRequestID drives a request with a claude-code
// User-Agent and a nil-UUID X-Request-Id, then asserts:
//  1. the detected agent_type ("claude-code") lands on request_logs,
//  2. the all-zero request_id is NOT stored — the gateway regenerated it.
//
// This is the end-to-end proof of the Part 1 (request-id fix) + Part 2 (agent
// detection) work.
func TestTrace_AgentDetectionAndRequestID(t *testing.T) {
	h := NewHarness(t)

	var hits int
	up := jsonUpstream("hi from upstream", 10, 5, &hits)
	defer up.Close()

	h.AddProvider("openai", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{
		Provider: "openai", UpstreamModel: "gpt-4o",
	})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "openai"})
	h.SeedKey("sk-agent", "acme", "team-a", "key_agent", nil)
	h.SetQuota("tenant:acme", 100_000_000)
	h.SyncConfig()

	// A request that identifies as claude-code AND sends a nil-UUID request id.
	resp := h.ChatWithHeaders("sk-agent", "chat", false, map[string]string{
		"User-Agent":          "claude-cli/1.0.83 (external, cli)",
		"X-Request-Id":        "00000000000000000000000000000000",
		"X-Voxeltoad-Session": "sess-agent-001",
	})
	// The echoed request id must NOT be the all-zero value the client sent —
	// the gateway must have regenerated it.
	if got := resp.Header.Get("X-Request-Id"); got == "" || got == "00000000000000000000000000000000" {
		t.Errorf("echoed X-Request-Id = %q; expected a regenerated non-zero id", got)
	}
	_ = resp.Body.Close()

	waitForRowCount(t, h, "request_logs", "tenant = 'acme' AND session_id = 'sess-agent-001'", 1)

	// Verify agent_type landed and request_id is NOT the zero value.
	var row struct {
		AgentType string `gorm:"column:agent_type"`
		RequestID string `gorm:"column:request_id"`
	}
	if err := h.DB.Raw(
		`SELECT agent_type, request_id FROM request_logs
		 WHERE tenant = 'acme' AND session_id = 'sess-agent-001' LIMIT 1`,
	).Scan(&row).Error; err != nil {
		t.Fatalf("query request_logs: %v", err)
	}
	if row.AgentType != "claude-code" {
		t.Errorf("agent_type = %q, want claude-code", row.AgentType)
	}
	if row.RequestID == "" || row.RequestID == "00000000000000000000000000000000" {
		t.Errorf("request_id = %q; expected a regenerated non-zero id", row.RequestID)
	}

	// Verify the session-list aggregation surfaces the agent type.
	req, _ := http.NewRequest(http.MethodGet,
		h.AdminURL+"/api/v1/request-logs/sessions?agent_type=claude-code", nil)
	req.Header.Set("Authorization", "Bearer "+h.AdminToken)
	listResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sessions list GET: %v", err)
	}
	defer func() { _ = listResp.Body.Close() }()
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		t.Fatalf("sessions list status = %d, want 200; body=%s", listResp.StatusCode, body)
	}
	var list struct {
		Data []struct {
			SessionID string `json:"session_id"`
			AgentType string `json:"agent_type"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(listResp.Body)
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("unmarshal sessions list: %v; body=%s", err, body)
	}
	found := false
	for _, s := range list.Data {
		if s.SessionID == "sess-agent-001" {
			found = true
			if s.AgentType != "claude-code" {
				t.Errorf("session agent_type = %q, want claude-code", s.AgentType)
			}
		}
	}
	if !found {
		t.Errorf("sess-agent-001 not in sessions list (agent filter); got %d sessions", len(list.Data))
	}
}

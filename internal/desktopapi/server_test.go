package desktopapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"voxeltoad/internal/desktopstore"
)

func openTestDB(t *testing.T) *desktopstore.DB {
	t.Helper()
	db, err := desktopstore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seed(t *testing.T, db *desktopstore.DB) {
	t.Helper()
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	logs := []desktopstore.RequestLogRow{
		{ID: 1, Tenant: "default", Provider: "openai", ModelRequested: "default", ModelResolved: "gpt-4o-mini", PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30, TTFTms: 100, Durationms: 500, RequestID: "req-1", SessionID: "sess-a", AgentType: "claude-code", CreatedAt: base},
		{ID: 2, Tenant: "default", Provider: "openai", ModelRequested: "default", ModelResolved: "gpt-4o-mini", PromptTokens: 5, TotalTokens: 5, Durationms: 50, ErrorType: "upstream_error", RequestID: "req-2", SessionID: "sess-b", AgentType: "codebuddy", CreatedAt: base.Add(time.Minute)},
	}
	for _, l := range logs {
		if err := db.Create(&l).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}
	traces := []desktopstore.TracePayloadRow{
		{ID: 1, RequestID: "req-1", SessionID: "sess-a", Provider: "openai", ModelRequested: "default", AgentType: "claude-code", StatusCode: 200, NMessages: 1, Messages: `[{"role":"user","content":"hi"}]`, RequestRaw: `{"model":"default"}`, ResponseRaw: "data:{...}", CreatedAt: base},
		{ID: 2, RequestID: "req-2", SessionID: "sess-b", Provider: "openai", ModelRequested: "default", AgentType: "codebuddy", StatusCode: 502, NMessages: 1, Messages: `[{"role":"user","content":"bad"}]`, RequestRaw: `{"model":"default"}`, ErrorRaw: "upstream 401", CreatedAt: base.Add(time.Minute)},
	}
	for _, tr := range traces {
		if err := db.Create(&tr).Error; err != nil {
			t.Fatalf("seed trace: %v", err)
		}
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *desktopstore.DB) {
	db := openTestDB(t)
	seed(t, db)
	return httptest.NewServer(New(db, "", nil).Handler()), db
}

func get(t *testing.T, ts *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &m)
	}
	return resp.StatusCode, m
}

func TestHealth(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, m := get(t, ts, "/api/v1/health")
	if code != 200 || m["status"] != "ok" {
		t.Fatalf("health = %d %v", code, m)
	}
}

func TestRequestLogsEnvelope(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, m := get(t, ts, "/api/v1/request-logs")
	if code != 200 {
		t.Fatalf("request-logs code = %d", code)
	}
	if m["total"] != float64(2) {
		t.Errorf("total = %v, want 2", m["total"])
	}
	data, ok := m["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("data = %v, want 2 rows", m["data"])
	}
}

func TestRequestLogsFilter(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, m := get(t, ts, "/api/v1/request-logs?agent_type=codebuddy")
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	data := m["data"].([]any)
	if len(data) != 1 {
		t.Errorf("filtered data = %d rows, want 1", len(data))
	}
}

func TestSessions(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, m := get(t, ts, "/api/v1/sessions")
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	if m["total"] != float64(2) {
		t.Errorf("total = %v, want 2 sessions", m["total"])
	}
}

func TestOverview(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, m := get(t, ts, "/api/v1/overview")
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	if _, ok := m["agents"].([]any); !ok {
		t.Errorf("agents missing: %v", m)
	}
	if _, ok := m["totals"].(map[string]any); !ok {
		t.Errorf("totals missing: %v", m)
	}
}

func TestTraceBySession(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, m := get(t, ts, "/api/v1/trace/sessions/sess-a")
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	if m["session_id"] != "sess-a" {
		t.Errorf("session_id = %v", m["session_id"])
	}
	reqs := m["requests"].([]any)
	if len(reqs) != 1 {
		t.Errorf("requests = %d, want 1", len(reqs))
	}
}

func TestTraceByRowID(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// Found: full detail with messages + bodies.
	code, m := get(t, ts, "/api/v1/trace/rows/1")
	if code != 200 {
		t.Fatalf("found code = %d", code)
	}
	if m["session_id"] != "sess-a" || m["status_code"] != float64(200) {
		t.Errorf("detail = %v", m)
	}
	if _, ok := m["messages"]; !ok {
		t.Errorf("messages missing: %v", m)
	}

	// Bad id → 400.
	code, _ = get(t, ts, "/api/v1/trace/rows/abc")
	if code != 400 {
		t.Errorf("bad id code = %d, want 400", code)
	}

	// Missing id → 404.
	code, _ = get(t, ts, "/api/v1/trace/rows/999")
	if code != 404 {
		t.Errorf("missing id code = %d, want 404", code)
	}
}

func TestTraceByRequestID(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, m := get(t, ts, "/api/v1/trace/requests/req-2")
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	if m["session_id"] != "sess-b" || m["error_raw"] != "upstream 401" {
		t.Errorf("detail = %v", m)
	}
}

// TestTraceByRequestIDWithSlash guards the multi-segment wildcard
// ({request_id...}). The data plane emits request IDs that contain a slash
// (e.g. "NODE/abc-000001"); a single-segment wildcard returned 404 for them.
func TestTraceByRequestIDWithSlash(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	row := desktopstore.TracePayloadRow{
		ID:             1,
		RequestID:      "NODE/abc-000001",
		SessionID:      "sess-slash",
		Provider:       "openai",
		ModelRequested: "default",
		AgentType:      "claude-code",
		StatusCode:     200,
		NMessages:      1,
		Messages:       `[{"role":"user","content":"hi"}]`,
		RequestRaw:     `{"model":"default"}`,
		ResponseRaw:    "data:{...}",
		CreatedAt:      base,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	ts := httptest.NewServer(New(db, "", nil).Handler())
	defer ts.Close()

	// Raw slash in the path must match the "..." wildcard.
	code, m := get(t, ts, "/api/v1/trace/requests/NODE/abc-000001")
	if code != 200 {
		t.Fatalf("raw slash code = %d, want 200", code)
	}
	if m["request_id"] != "NODE/abc-000001" {
		t.Errorf("request_id = %v", m["request_id"])
	}

	// Browsers send encodeURIComponent(requestId) → %2F; the server must
	// decode it back and still resolve.
	code, _ = get(t, ts, "/api/v1/trace/requests/NODE%2Fabc-000001")
	if code != 200 {
		t.Errorf("encoded %%2F code = %d, want 200", code)
	}
}

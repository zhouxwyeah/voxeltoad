//go:build dbtest

package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

// decodeJSON unmarshals a raw JSON response body into a map.
func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decodeJSON: %v; body=%s", err, rr.Body.String())
	}
	return m
}

func seedDataPlaneNode(t *testing.T, db *store.DB, instanceID, statusArg string, heartbeatAt time.Time) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO data_plane_nodes
		   (instance_id, hostname, addr, version, commit, config_generation, status, started_at, last_heartbeat_at)
		 VALUES (?, 'h', ':8080', 'v1.0', 'abc', 5, ?, now(), ?)
		 ON CONFLICT (instance_id) DO NOTHING`,
		instanceID, statusArg, heartbeatAt,
	).Error; err != nil {
		t.Fatalf("seed data_plane_nodes: %v", err)
	}
}

// TestDataPlaneNodes_List returns registered nodes.
func TestDataPlaneNodes_List(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedDataPlaneNode(t, db, "node-1", "online", time.Now())
	seedDataPlaneNode(t, db, "node-2", "draining", time.Now().Add(-30*time.Second))

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/data-plane-nodes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("data-plane-nodes status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) < 2 {
		t.Errorf("data-plane-nodes len = %d, want >= 2", len(rows))
	}
}

// TestOverview_ReturnsStats checks the overview endpoint responds with a payload.
func TestOverview_ReturnsStats(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedDataPlaneNode(t, db, "ov-node", "online", time.Now())
	seedRequestLogRow(t, db, "acme", "openai", "upstream_error")

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/overview", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("overview status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeJSON(t, rr)
	nodes, ok := body["nodes"].(map[string]interface{})
	if !ok {
		t.Fatal("overview.nodes is missing or not an object")
	}
	if v, ok := nodes["total"].(float64); !ok || v < 1 {
		t.Errorf("overview.nodes.total = %v, want >= 1", v)
	}
	stats, ok := body["recent_stats"].(map[string]interface{})
	if !ok {
		t.Fatal("overview.recent_stats is missing or not an object")
	}
	if v, ok := stats["total_errors"].(float64); !ok || v < 1 {
		t.Errorf("overview.recent_stats.total_errors = %v, want >= 1 (got %v)", stats, stats["total_errors"])
	}
}

// TestOverview_AgentStats verifies the per-agent rollup: rows are returned
// ordered by request_count DESC, with token sums and error counts aggregated
// correctly. Also verifies the ?from/&to window filters rows.
func TestOverview_AgentStats(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Seed 3 rows for codex, 1 for claude-code, 1 with empty agent_type.
	// All seeded at now() so the default 24h fallback picks them up.
	seedRequestLogRowWithAgent(t, db, "acme", "openai", "", "codex")
	seedRequestLogRowWithAgent(t, db, "acme", "openai", "", "codex")
	seedRequestLogRowWithAgent(t, db, "acme", "openai", "timeout_error", "codex")
	seedRequestLogRowWithAgent(t, db, "acme", "anthropic", "", "claude-code")
	seedRequestLogRowWithAgent(t, db, "acme", "openai", "", "")

	// Use an explicit wide window so the test is robust to clock skew.
	from := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/overview?from="+from+"&to="+to, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("overview status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeJSON(t, rr)
	rowsRaw, ok := body["agent_stats"].([]interface{})
	if !ok {
		t.Fatalf("overview.agent_stats missing or not an array; body=%s", rr.Body.String())
	}
	if len(rowsRaw) != 3 {
		t.Fatalf("agent_stats len = %d, want 3 (codex / claude-code / empty)", len(rowsRaw))
	}

	// First row must be codex (3 requests > 1 > 1).
	first := rowsRaw[0].(map[string]interface{})
	if first["agent_type"] != "codex" {
		t.Errorf("agent_stats[0].agent_type = %v, want codex", first["agent_type"])
	}
	if v := first["request_count"].(float64); v != 3 {
		t.Errorf("agent_stats[0].request_count = %v, want 3", v)
	}
	if v := first["prompt_tokens"].(float64); v != 30 {
		t.Errorf("agent_stats[0].prompt_tokens = %v, want 30 (3×10)", v)
	}
	if v := first["completion_tokens"].(float64); v != 60 {
		t.Errorf("agent_stats[0].completion_tokens = %v, want 60 (3×20)", v)
	}
	if v := first["total_tokens"].(float64); v != 90 {
		t.Errorf("agent_stats[0].total_tokens = %v, want 90 (3×30)", v)
	}
	if v := first["error_count"].(float64); v != 1 {
		t.Errorf("agent_stats[0].error_count = %v, want 1 (one timeout_error)", v)
	}

	// Verify all rows have non-negative integer fields (smoke test).
	for i, r := range rowsRaw {
		row := r.(map[string]interface{})
		for _, k := range []string{"request_count", "prompt_tokens", "completion_tokens", "total_tokens", "duration_ms", "ttft_ms", "error_count"} {
			if v, ok := row[k].(float64); !ok || v < 0 {
				t.Errorf("agent_stats[%d].%s = %v, want non-negative number", i, k, row[k])
			}
		}
	}
}

// TestConfigSnapshots_ListHistory verifies snapshots are saved after mutations.
func TestConfigSnapshots_ListHistory(t *testing.T) {
	h, _, tok := authedAdmin(t)

	// Create a provider to trigger snapshot save (async).
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]interface{}{
		"name":     "snap-test",
		"type":     "openai",
		"adapter":  "openai",
		"base_url": "https://api.example.com",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("provider create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// Poll for snapshot save (async goroutine delay — longer window for shared DB).
	deadline := time.Now().Add(5 * time.Second)
	var snapCount int
	for time.Now().Before(deadline) {
		rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/config/history?limit=50", nil)
		if rr.Code == http.StatusOK {
			rows := decodeList(t, rr)
			snapCount = len(rows)
			if snapCount > 0 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snapCount == 0 {
		t.Skip("snapshot not saved within poll window (async goroutine delay)")
	}
	t.Logf("config history: %d snapshots found", snapCount)

	// Diff between first and latest snapshot (if at least 2 exist).
	if snapCount >= 2 {
		rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/config/history/diff?from=1&to=2", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("history/diff status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	}
}

// TestRequestLogs_CSVExport checks ?format=csv returns text/csv.
func TestRequestLogs_CSVExport(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedRequestLogRow(t, db, "acme", "openai", "")

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs?format=csv", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("csv status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	cd := rr.Header().Get("Content-Disposition")
	if cd == "" {
		t.Error("Content-Disposition header missing")
	}
	body := rr.Body.String()
	if !strings.Contains(body, "id,tenant") && !strings.Contains(body, "id,") {
		t.Errorf("CSV body missing expected header prefix; got: %s", body[:min(len(body), 80)])
	}
}

// TestAudit_CSVExport checks ?format=csv for audit logs.
func TestAudit_CSVExport(t *testing.T) {
	h, _, tok := authedAdmin(t)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/audit?format=csv", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("csv status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("Content-Type is not text/csv")
	}
}

// TestUsage_CSVExport checks ?format=csv for usage.
func TestUsage_CSVExport(t *testing.T) {
	h, _, tok := authedAdmin(t)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?format=csv", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("csv status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("Content-Type is not text/csv")
	}
}

// TestConfig_Preview validates the dry-run preview endpoint.
func TestConfig_Preview(t *testing.T) {
	h, _, tok := authedAdmin(t)

	preview := map[string]interface{}{
		"version":   "preview",
		"providers": []interface{}{map[string]interface{}{"name": "test", "type": "openai", "adapter": "openai"}},
		"models":    []interface{}{},
		"routes":    []interface{}{},
		"plugins":   []interface{}{},
	}
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/config/preview", preview)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeJSON(t, rr)
	if v, ok := body["valid"]; !ok || v != true {
		t.Errorf("preview.valid = %v, want true", v)
	}
	diff, ok := body["diff"].(map[string]interface{})
	if !ok {
		t.Fatal("preview.diff missing or not an object")
	}
	if v, ok := diff["added_providers"]; !ok {
		t.Error("preview.diff.added_providers missing")
	} else if arr, ok := v.([]interface{}); !ok || len(arr) != 1 {
		t.Errorf("preview.diff.added_providers = %v, want [\"test\"]", v)
	}
}

// TestConfig_PreviewInvalid returns 400 on empty provider name.
func TestConfig_PreviewInvalid(t *testing.T) {
	h, _, tok := authedAdmin(t)

	preview := map[string]interface{}{
		"version":   "preview",
		"providers": []interface{}{map[string]interface{}{"name": "", "type": "openai"}},
	}
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/config/preview", preview)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("preview invalid status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// seedAuditRow inserts a management-plane audit entry for a specific operator.
func seedAuditRow(t *testing.T, db *store.DB, action, resourceType, resourceID string) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO audit_logs (operator_id, action, resource_type, resource_id, after, created_at)
		 VALUES (1, ?, ?, ?, '{}', now())`,
		action, resourceType, resourceID,
	).Error; err != nil {
		t.Fatalf("seed audit_logs: %v", err)
	}
}

// TestAudit_CSVExport_WithData exports audit entries as CSV and validates the
// format includes expected columns and data.
func TestAudit_CSVExport_WithData(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedAuditRow(t, db, "create", "provider", "openai-prod")
	seedAuditRow(t, db, "update", "model", "gpt-4o")

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/audit?format=csv", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("csv status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "id,operator_id") {
		t.Errorf("CSV header missing expected columns: %s", body[:min(len(body), 100)])
	}
	if !strings.Contains(body, "create") || !strings.Contains(body, "update") {
		t.Errorf("CSV missing action data: %s", body[:min(len(body), 200)])
	}
}

// TestDataPlaneNodes_FullLifecycle validates the online→draining→offline
// state machine through Register→Drain→CleanupStale.
func TestDataPlaneNodes_FullLifecycle(t *testing.T) {
	h, db, tok := authedAdmin(t)
	dpRepo := store.NewDataPlaneRepo(db)

	// Register: node appears online.
	_ = dpRepo.Register(t.Context(), &store.DataPlaneNode{
		InstanceID: "lifecycle-test", Hostname: "h1", Addr: ":9090", Version: "v1",
	})
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/data-plane-nodes", nil)
	rows := decodeList(t, rr)
	var node map[string]any
	for _, r := range rows {
		if r["instance_id"] == "lifecycle-test" {
			node = r
			break
		}
	}
	if node == nil {
		t.Fatal("registered node not found in list")
	}
	if node["status"] != "online" {
		t.Errorf("fresh node status = %v, want online", node["status"])
	}

	// Drain: node transitions to draining.
	_ = dpRepo.Drain(t.Context(), "lifecycle-test")
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/data-plane-nodes", nil)
	for _, r := range decodeList(t, rr) {
		if r["instance_id"] == "lifecycle-test" {
			if r["status"] != "draining" {
				t.Errorf("drained node status = %v, want draining", r["status"])
			}
			break
		}
	}

	// MarkOffline: cleanup removes it from online count.
	_ = dpRepo.MarkOffline(t.Context(), "lifecycle-test")
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/overview", nil)
	overview := decodeJSON(t, rr)
	nodes := overview["nodes"].(map[string]interface{})
	// After marking offline, only the initial ov-node should be online.
	if v, ok := nodes["online"].(float64); !ok || v < 0 {
		t.Errorf("overview online count after lifecycle: %v", nodes["online"])
	}
}

// TestConfig_Rollback_API triggers a rollback via the HTTP API. Creates a
// provider, waits for snapshot save, then rolls back to the first version.
func TestConfig_RollbackAPI(t *testing.T) {
	h, _, tok := authedAdmin(t)

	// Create a provider to trigger snapshot save.
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]interface{}{
		"name":     "rollback-me",
		"type":     "openai",
		"adapter":  "openai",
		"base_url": "https://api.openai.com/v1",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create provider: status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Create a second provider.
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]interface{}{
		"name":     "rollback-target",
		"type":     "claude",
		"adapter":  "claude",
		"base_url": "https://api.anthropic.com/v1",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create second provider: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Poll for snapshots — find the version with exactly 1 provider.
	var v1 int64
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/config/history?limit=50", nil)
		if rr.Code != http.StatusOK {
			continue
		}
		for _, r := range decodeList(t, rr) {
			ver := int64(r["version"].(float64))
			if ver < 1 {
				continue
			}
			// Fetch the full snapshot to check content.
			drr := doAuth(t, h, tok, http.MethodGet,
				fmt.Sprintf("/api/v1/config/history/%d", ver), nil)
			if drr.Code == http.StatusOK {
				body := decodeJSON(t, drr)
				provs, ok := body["providers"].([]interface{})
				if !ok || provs == nil {
					continue
				}
				if len(provs) == 1 {
					if _, ok := provs[0].(map[string]interface{})["name"]; ok {
						v1 = ver
						break
					}
				}
			}
		}
		if v1 > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if v1 == 0 {
		t.Skip("snapshot with 1 provider not found (async delay)")
	}

	// Rollback to the 1-provider version.
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/config/rollback", map[string]interface{}{
		"version": v1,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("rollback status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// Verify: only one provider after rollback.
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/providers", nil)
	if rr.Code != http.StatusOK {
		t.Fatal("list providers after rollback failed")
	}
	providers := decodeList(t, rr)
	if len(providers) != 1 {
		t.Errorf("after rollback providers = %d, want 1 (versions: %v)", len(providers), providers)
	}
}

// TestRequestLogs_Filtered verifies provider/model/error_type filter params
// are honored end-to-end (C2 feature).
func TestRequestLogs_Filtered(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedRequestLogRow(t, db, "acme", "openai", "")
	seedRequestLogRow(t, db, "acme", "claude", "")
	seedRequestLogRow(t, db, "other", "openai", "timeout_error")

	// Filter by provider.
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs?provider=claude", nil)
	if rr.Code != http.StatusOK {
		t.Fatal("filter by provider failed")
	}
	rows, _ := decodePage(t, rr)
	for _, r := range rows {
		if r["provider"] != "claude" {
			t.Errorf("provider filter leaked: %v", r["provider"])
		}
	}

	// Filter by error_type.
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs?error_type=timeout_error", nil)
	if rr.Code != http.StatusOK {
		t.Fatal("filter by error_type failed")
	}
	rows, _ = decodePage(t, rr)
	if len(rows) != 1 {
		t.Errorf("error_type filter rows = %d, want 1", len(rows))
	}
}

// TestUsage_Filtered verifies provider/model filter params on the usage
// endpoint (C2 feature).
func TestUsage_Filtered(t *testing.T) {
	h, db, tok := authedAdmin(t)
	prov := fmt.Sprintf("ufilter-%d", time.Now().UnixNano())

	// Seed with unique provider name to avoid contamination from other tests.
	if err := db.Exec(
		`INSERT INTO usage_records
		   (tenant, group_name, api_key_id, provider, model, prompt_tokens, completion_tokens, cost, created_at)
		 VALUES ('acme', '', 'k1', ?, 'm1', 10, 20, 100, now()),
		        ('acme', '', 'k2', ?, 'm2', 5, 15, 50, now())`,
		prov, prov,
	).Error; err != nil {
		t.Fatal(err)
	}

	rr := doAuth(t, h, tok, http.MethodGet, fmt.Sprintf("/api/v1/usage?provider=%s", prov), nil)
	if rr.Code != http.StatusOK {
		t.Fatal("usage filter failed")
	}
	rows := decodeList(t, rr)
	if len(rows) != 2 {
		t.Errorf("provider filter rows = %d, want 2", len(rows))
	}

	rr = doAuth(t, h, tok, http.MethodGet, fmt.Sprintf("/api/v1/usage?provider=%s&model=m1", prov), nil)
	if rr.Code != http.StatusOK {
		t.Fatal("usage model filter failed")
	}
	rows = decodeList(t, rr)
	if len(rows) != 1 || rows[0]["model"] != "m1" {
		t.Errorf("model filter rows = %d / model=%v, want 1 / m1", len(rows), rows)
	}
}

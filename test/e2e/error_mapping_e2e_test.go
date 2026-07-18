//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"voxeltoad/internal/config"
)

// TestCompat_UpstreamErrorMapping asserts that whatever error status the
// upstream returns (4xx or 5xx), the gateway maps it to a client-facing 502
// with the OpenAI-compatible "upstream_error" envelope (mapForwardError,
// internal/proxy/router.go), and that the failure is still recorded in
// request_logs with a matching error_type — even though no failover target
// exists (single candidate). This complements TestCompat_ErrorEnvelope, which
// only covers the auth-rejection (401) path.
func TestCompat_UpstreamErrorMapping(t *testing.T) {
	cases := []struct {
		name          string
		upstreamState int
	}{
		{"upstream400", http.StatusBadRequest},
		{"upstream429", http.StatusTooManyRequests},
		{"upstream500", http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHarness(t)

			var hits int
			up := failingUpstream(tc.upstreamState, &hits)
			defer up.Close()

			h.AddProvider("p1", up.URL(), "plain://k")
			h.AddModel("chat", 1_000_000, 1_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
			h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
			h.SeedKey("sk-em-"+tc.name, "acme-"+tc.name, "team", "key_"+tc.name, nil)
			h.SetQuota("tenant:acme-"+tc.name, 1_000_000)
			h.SyncConfig()

			resp := h.Chat("sk-em-"+tc.name, "chat", false)
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502 (upstream %d mapped); body=%s", resp.StatusCode, tc.upstreamState, body)
			}
			var env struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("decode error envelope: %v; body=%s", err, body)
			}
			if env.Error.Type != "upstream_error" {
				t.Errorf("error.type = %q, want upstream_error", env.Error.Type)
			}

			waitForRequestLog(t, h, "acme-"+tc.name, func(r requestLogRow) bool {
				return r.ErrorType == "upstream_error"
			}, "row with error_type=upstream_error")

			if hits != 1 {
				t.Errorf("upstream hits = %d, want 1 (no failover target)", hits)
			}
		})
	}
}

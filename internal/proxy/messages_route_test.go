package proxy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
)

// TestRouter_MessagesRoute_Registered verifies that POST /v1/messages is a
// registered route — i.e. it does NOT return 404. Once the Anthropic ingress
// codec is wired (Step 2+), the route processes requests; before any body is
// supplied it returns 400 with an Anthropic-shaped error envelope. The crucial
// invariant is that Anthropic clients (Claude Code) see the route as present.
//
// The assertion is intentionally strict (status + envelope shape) rather than
// just "not 404", so a future change that accidentally breaks the Anthropic
// envelope (e.g. reverting to OpenAI shape on this route) is caught here.
func TestRouter_MessagesRoute_Registered(t *testing.T) {
	h := proxy.Router(nil)
	rr := httptest.NewRecorder()
	// Nil body: the handler rejects it as invalid_request_body, but the route
	// resolves (NOT 404) and the error envelope is Anthropic-shaped.
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusNotFound {
		t.Fatalf("POST /v1/messages returned 404 — route is not registered; Anthropic clients cannot use the gateway. body: %s", rr.Body.String())
	}
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /v1/messages with empty body: status = %d, want 400 (invalid_request_body); body: %s", rr.Code, rr.Body.String())
	}

	// Anthropic error envelope: {"type":"error","error":{"type":...,"message":...}}.
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	if env.Type != "error" {
		t.Errorf("envelope type = %q, want 'error' (Anthropic shape); body=%s", env.Type, rr.Body.String())
	}
	if env.Error.Type == "" {
		t.Errorf("error.type empty; body=%s", rr.Body.String())
	}
	if env.Error.Message == "" {
		t.Errorf("error.message empty; body=%s", rr.Body.String())
	}
}

// TestRouter_MessagesRoute_AnthropicDisabled verifies that when the
// AnthropicDisabled gateway setting is true, POST /v1/messages returns 404 in
// the Anthropic error envelope — the terminal "endpoint absent" semantic chosen
// in ADR-0048 (not 503, which would trigger client retry loops). The request
// never reaches the dispatcher/telemetry in this state.
func TestRouter_MessagesRoute_AnthropicDisabled(t *testing.T) {
	settings := func() *config.GatewaySettings {
		return &config.GatewaySettings{Ingress: config.IngressSettings{AnthropicDisabled: true}}
	}
	h := proxy.Router(nil, proxy.WithSettingsSource(settings))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (anthropic disabled); body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	if env.Type != "error" || env.Error.Type != "not_found_error" {
		t.Errorf("envelope = %+v, want type=error / error.type=not_found_error", env)
	}
}

//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"voxeltoad/internal/config"
)

// TestAnthropicIngress_XApiKeyAuth verifies that Anthropic clients using the
// x-api-key header convention (Claude Code's default) can authenticate. Both
// x-api-key and Authorization: Bearer must work on /v1/messages.
func TestAnthropicIngress_XApiKeyAuth(t *testing.T) {
	h := NewHarness(t)
	up := jsonUpstream("ok", 1, 1, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-xk", "acme", "team", "key_xk", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	// Valid x-api-key → 200.
	resp := h.AnthropicMessagesWithAuth("sk-xk", "x-api-key", "chat", false, map[string]any{
		"max_tokens": 16, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("valid x-api-key status = %d, want 200; body=%s", resp.StatusCode, b)
	}

	// Unknown x-api-key → 401 in Anthropic envelope shape.
	resp2 := h.AnthropicMessagesWithAuth("sk-unknown", "x-api-key", "chat", false, map[string]any{
		"max_tokens": 16, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown x-api-key status = %d, want 401", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	assertAnthropicErrorEnvelope(t, body, "authentication")
}

// TestAnthropicIngress_ErrorEnvelopeAnthropicShape verifies that all error
// responses on /v1/messages use the Anthropic error envelope shape
// {"type":"error","error":{"type":...,"message":...}}, NOT the OpenAI shape.
func TestAnthropicIngress_ErrorEnvelopeAnthropicShape(t *testing.T) {
	h := NewHarness(t)
	up := failingUpstream(http.StatusInternalServerError, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-err", "acme", "team", "key_err", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	// Upstream 500 → gateway 502 with Anthropic envelope.
	resp := h.AnthropicMessages("sk-err", "chat", false, map[string]any{
		"max_tokens": 16, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	assertAnthropicErrorEnvelope(t, body, "api_error")
}

// TestAnthropicIngress_ModelNotPermittedAnthropicShape verifies the model
// permission rejection returns the Anthropic envelope on /v1/messages (while
// keeping the OpenAI envelope on /v1/chat/completions for parity).
func TestAnthropicIngress_ModelNotPermittedAnthropicShape(t *testing.T) {
	h := NewHarness(t)
	up := jsonUpstream("ok", 1, 1, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	// Key restricted to model "other" — "chat" must be rejected.
	h.SeedKey("sk-restricted", "acme", "team", "key_r", []string{"other"})
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.AnthropicMessages("sk-restricted", "chat", false, map[string]any{
		"max_tokens": 16, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	assertAnthropicErrorEnvelope(t, body, "invalid_request_error")
}

// TestOpenAIIngress_ErrorEnvelopeUnchanged is the parity test: the OpenAI
// /v1/chat/completions route still uses the OpenAI error envelope after the
// Anthropic ingress was added (no accidental envelope cross-contamination).
func TestOpenAIIngress_ErrorEnvelopeUnchanged(t *testing.T) {
	h := NewHarness(t)
	up := failingUpstream(http.StatusInternalServerError, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-openai", "acme", "team", "key_o", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-openai", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode OpenAI envelope: %v; body=%s", err, body)
	}
	if env.Error.Type == "" {
		t.Errorf("OpenAI envelope missing error.type: %s", body)
	}
	// Must NOT be the Anthropic shape ({"type":"error", ...}).
	if strings.Contains(string(body), `"type":"error"`) {
		t.Errorf("OpenAI route returned Anthropic envelope by mistake: %s", body)
	}
}

// assertAnthropicErrorEnvelope asserts body is the Anthropic error envelope
// {"type":"error","error":{"type":...,"message":...}} and that error.type
// matches wantTypePrefix.
func assertAnthropicErrorEnvelope(t *testing.T, body []byte, wantTypePrefix string) {
	t.Helper()
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode Anthropic error envelope: %v; body=%s", err, body)
	}
	if env.Type != "error" {
		t.Errorf("envelope type = %q, want 'error'", env.Type)
	}
	if !strings.Contains(env.Error.Type, wantTypePrefix) {
		t.Errorf("error.type = %q, want prefix %q", env.Error.Type, wantTypePrefix)
	}
	if env.Error.Message == "" {
		t.Errorf("error.message empty")
	}
}

// AnthropicMessagesWithAuth sends an Anthropic /v1/messages request with a
// specific auth scheme. scheme is "x-api-key" or "Bearer". The caller owns
// closing resp.Body.
func (h *Harness) AnthropicMessagesWithAuth(key, scheme, model string, stream bool, extra map[string]any) *http.Response {
	h.t.Helper()
	body := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"stream":     stream,
	}
	for k, v := range extra {
		body[k] = v
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.GatewayURL+"/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if scheme == "x-api-key" {
		req.Header.Set("x-api-key", key)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("messages request: %v", err)
	}
	return resp
}

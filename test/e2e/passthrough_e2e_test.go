//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"voxeltoad/internal/config"
)

// TestPassthrough_AnthropicToClaude_NonStreaming was written for ADR-0047
// (two separate providers + cross-provider protocol partition). ADR-0049
// replaced this model with multi-endpoint providers. The multi-endpoint
// equivalent (one provider with openai + anthropic endpoints) is covered in
// slice-7 e2e tests. Skipping this ADR-0047 scenario.
func TestPassthrough_AnthropicToClaude_NonStreaming(t *testing.T) {
	t.Skip("ADR-0047 scenario superseded by ADR-0049 multi-endpoint provider; covered by slice-7 e2e")
	h := NewHarness(t)

	claudeHits := 0
	openaiHits := 0
	claudeUp := claudeJSONUpstream("passthrough works", 11, 6, &claudeHits)
	defer claudeUp.Close()
	openaiUp := jsonUpstream("translated fallback", 11, 6, &openaiHits)
	defer openaiUp.Close()

	// Both providers in one route; claude provider first in config (priority),
	// but protocol-aware routing must pick it for anthropic ingress regardless
	// of config order.
	h.AddProviderWithAdapter("claude-p", claudeUp.URL(), "plain://k", "claude")
	h.AddProvider("openai-p", openaiUp.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000,
		config.ModelUpstream{Provider: "openai-p", UpstreamModel: "gpt-4o"},
		config.ModelUpstream{Provider: "claude-p", UpstreamModel: "claude-opus-4-5"},
	)
	h.AddRoute("chat", "priority",
		config.RouteProvider{Name: "openai-p"},
		config.RouteProvider{Name: "claude-p"},
	)
	h.SeedKey("sk-pt", "acme", "team", "key_pt", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.AnthropicMessages("sk-pt", "chat", false, map[string]any{
		"max_tokens": 32, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	// The claude provider must have been hit (protocol-aware routing picked it).
	if claudeHits == 0 {
		t.Fatalf("claude provider not hit; openai hits = %d (anthropic ingress should prefer claude provider)", openaiHits)
	}

	// Response must be the upstream's raw bytes: id is the upstream's msg_x,
	// content text is the upstream's exact string. A translated response would
	// have a gateway-generated id and re-encoded content.
	var msg struct {
		ID      string `json:"id"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if msg.ID != "msg_x" {
		t.Errorf("response id = %q, want msg_x (upstream raw id — passthrough)", msg.ID)
	}
	if len(msg.Content) == 0 || msg.Content[0].Text != "passthrough works" {
		t.Errorf("content = %+v, want upstream's verbatim text", msg.Content)
	}
}

// TestPassthrough_AnthropicToClaude_Streaming verifies the streaming
// passthrough relays every upstream SSE frame verbatim, including
// content_block_start / content_block_stop (the critical-#1 regression: these
// were swallowed by the claude adapter's default branch, breaking tool_use and
// text-block boundaries on the client).
func TestPassthrough_AnthropicToClaude_Streaming(t *testing.T) {
	t.Skip("ADR-0047 scenario superseded by ADR-0049 multi-endpoint provider; covered by slice-7 e2e")
	h := NewHarness(t)

	claudeUp := claudeSSEUpstream([]claudeEvent{
		{Type: "message_start", Data: `{"type":"message_start","message":{"id":"msg_s","type":"message","role":"assistant","model":"claude-opus-4-5","content":[],"usage":{"input_tokens":7,"output_tokens":0}}}`},
		{Type: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{Type: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`},
		{Type: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`},
		{Type: "content_block_stop", Data: `{"type":"content_block_stop","index":0}`},
		{Type: "message_delta", Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`},
		{Type: "message_stop", Data: `{"type":"message_stop"}`},
	}, nil)
	defer claudeUp.Close()

	h.AddProviderWithAdapter("claude-p", claudeUp.URL(), "plain://k", "claude")
	h.AddModel("chat", 1_000_000, 2_000_000,
		config.ModelUpstream{Provider: "claude-p", UpstreamModel: "claude-opus-4-5"},
	)
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "claude-p"})
	h.SeedKey("sk-pts", "acme", "team", "key_pts", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.AnthropicMessages("sk-pts", "chat", true, map[string]any{
		"max_tokens": 32, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; body=%s", resp.StatusCode, b)
	}

	events := scanSSEEvents(t, resp.Body)
	var names []string
	for _, e := range events {
		names = append(names, e.Event)
	}

	// The passthrough must relay every upstream frame: message_start,
	// content_block_start, 2× content_block_delta, content_block_stop,
	// message_delta, message_stop. Before the fix, content_block_start and
	// content_block_stop were missing (swallowed by the claude adapter).
	want := []string{
		"message_start", "content_block_start", "content_block_delta",
		"content_block_delta", "content_block_stop", "message_delta", "message_stop",
	}
	if len(events) != len(want) {
		t.Fatalf("event count = %d (%v), want %d (%v)", len(events), names, len(want), want)
	}
	for i, w := range want {
		if events[i].Event != w {
			t.Errorf("events[%d] = %q, want %q", i, events[i].Event, w)
		}
	}

	// The relayed text must round-trip verbatim.
	var text strings.Builder
	for _, e := range events {
		if e.Event != "content_block_delta" {
			continue
		}
		var d struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(e.Data), &d)
		text.WriteString(d.Delta.Text)
	}
	if text.String() != "Hello" {
		t.Errorf("streamed text = %q, want Hello", text.String())
	}
}

// TestPassthrough_AnthropicFailover_Translates verifies graceful degradation
// (ADR-0047): when the claude provider fails, the anthropic-ingress request
// fails over to the openai provider and the response is properly TRANSLATED
// (not raw — RawProtocol gating prevents the openai adapter's Raw from being
// mistaken for Anthropic bytes). The client gets a valid Anthropic-shaped
// response either way.
func TestPassthrough_AnthropicFailover_Translates(t *testing.T) {
	t.Skip("ADR-0047 scenario superseded by ADR-0049 multi-endpoint provider; covered by slice-7 e2e")
	h := NewHarness(t)

	claudeUp := failingUpstream(http.StatusInternalServerError, nil)
	defer claudeUp.Close()
	openaiUp := jsonUpstream("translated via failover", 9, 4, nil)
	defer openaiUp.Close()

	h.AddProviderWithAdapter("claude-p", claudeUp.URL(), "plain://k", "claude")
	h.AddProvider("openai-p", openaiUp.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000,
		config.ModelUpstream{Provider: "claude-p", UpstreamModel: "claude-opus-4-5"},
		config.ModelUpstream{Provider: "openai-p", UpstreamModel: "gpt-4o"},
	)
	h.AddRoute("chat", "priority",
		config.RouteProvider{Name: "claude-p"},
		config.RouteProvider{Name: "openai-p"},
	)
	h.SeedKey("sk-ptf", "acme", "team", "key_ptf", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.AnthropicMessages("sk-ptf", "chat", false, map[string]any{
		"max_tokens": 32, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	// The response must be Anthropic-shaped (translated from the openai
	// upstream), NOT raw OpenAI JSON. If RawProtocol gating failed, the client
	// would receive OpenAI chat.completion JSON (no "type":"message").
	var msg struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if msg.Type != "message" {
		t.Errorf("response type = %q, want message (Anthropic shape, translated); RawProtocol gating may have leaked OpenAI JSON: %s", msg.Type, body)
	}
	if len(msg.Content) == 0 || msg.Content[0].Text != "translated via failover" {
		t.Errorf("content = %+v, want translated upstream text", msg.Content)
	}
}

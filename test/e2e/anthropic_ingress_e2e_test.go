//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"voxeltoad/internal/config"
)

// sseEvent is one SSE event parsed from a streaming response body.
type sseEvent struct{ Event, Data string }

// scanSSEEvents reads an SSE response body and returns the (event, data) pairs
// in order. Used by the Anthropic streaming tests to assert event sequences.
func scanSSEEvents(t *testing.T, r io.Reader) []sseEvent {
	t.Helper()
	var events []sseEvent
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var curEvent, curData strings.Builder
	flush := func() {
		if curData.Len() > 0 {
			events = append(events, sseEvent{Event: curEvent.String(), Data: curData.String()})
			curEvent.Reset()
			curData.Reset()
		}
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			curEvent.WriteString(strings.TrimPrefix(line, "event: "))
		} else if strings.HasPrefix(line, "data: ") {
			if curData.Len() > 0 {
				curData.WriteByte('\n')
			}
			curData.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	flush()
	return events
}

func sseEventNames(events []sseEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Event
	}
	return out
}

// TestAnthropicIngress_NonStreaming_Text verifies the end-to-end Anthropic
// ingress flow: Claude Code (Anthropic /v1/messages) → gateway → OpenAI-shape
// upstream. The response must be Anthropic-shaped (type=message, content[].text,
// stop_reason=end_turn, usage with input/output tokens).
func TestAnthropicIngress_NonStreaming_Text(t *testing.T) {
	h := NewHarness(t)
	up := jsonUpstream("hello world", 12, 8, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-a", "acme", "team", "key_a", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.AnthropicMessages("sk-a", "chat", false, map[string]any{
		"max_tokens": 64,
		"system":     "be brief",
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	var msg struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode Anthropic response: %v; body=%s", err, body)
	}
	if msg.Type != "message" {
		t.Errorf("type = %q, want message", msg.Type)
	}
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want assistant", msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != "text" || msg.Content[0].Text != "hello world" {
		t.Errorf("content = %+v, want one text block 'hello world'", msg.Content)
	}
	if msg.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", msg.StopReason)
	}
	if msg.Usage.InputTokens != 12 || msg.Usage.OutputTokens != 8 {
		t.Errorf("usage = %+v, want input=12 output=8", msg.Usage)
	}
}

// TestAnthropicIngress_Streaming_Tools drives the streaming tool-use flow:
// the OpenAI-shape upstream emits a chat.completion.chunk sequence with a
// delta.tool_calls entry (id+name+arguments fragments), and the gateway must
// translate it into Anthropic content_block_start (tool_use) +
// content_block_delta (input_json_delta) + content_block_stop + message_delta
// (stop_reason=tool_use) + message_stop.
func TestAnthropicIngress_Streaming_Tools(t *testing.T) {
	h := NewHarness(t)
	// OpenAI-shape streaming tool call: role + tool_calls[0] with name, then
	// arguments fragments, then finish_reason=tool_calls + usage, then [DONE].
	chunks := []string{
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_z","type":"function","function":{"name":"get_weather"}}]},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\""}}]},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Berlin\"}"}}]},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	up := sseToolCallUpstream(chunks, 6, 4, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-st", "acme", "team", "key_st", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.AnthropicMessages("sk-st", "chat", true, map[string]any{
		"max_tokens": 256,
		"messages":   []map[string]any{{"role": "user", "content": "weather?"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; body=%s", resp.StatusCode, b)
	}

	// Collect events.
	events := scanSSEEvents(t, resp.Body)

	want := []string{
		"message_start",
		"content_block_start", // tool_use
		"content_block_delta", // input_json_delta fragment 1
		"content_block_delta", // input_json_delta fragment 2
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d; events = %+v", len(events), len(want), sseEventNames(events))
	}
	for i, w := range want {
		if events[i].Event != w {
			t.Errorf("events[%d] = %q, want %q", i, events[i].Event, w)
		}
	}

	// content_block_start is tool_use with the right id/name.
	cbs := events[1]
	if !strings.Contains(cbs.Data, `"type":"tool_use"`) || !strings.Contains(cbs.Data, `"id":"call_z"`) || !strings.Contains(cbs.Data, `"name":"get_weather"`) {
		t.Errorf("content_block_start wrong: %s", cbs.Data)
	}
	// input_json_delta fragments concatenate to the full input.
	var fragments []string
	for _, ev := range events {
		if ev.Event != "content_block_delta" {
			continue
		}
		var d struct {
			Delta struct {
				Type        string `json:"type"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(ev.Data), &d)
		if d.Delta.Type == "input_json_delta" {
			fragments = append(fragments, d.Delta.PartialJSON)
		}
	}
	joined := strings.Join(fragments, "")
	if joined != `{"city":"Berlin"}` {
		t.Errorf("input partial_json concatenation = %q, want {\"city\":\"Berlin\"}", joined)
	}
	// message_delta stop_reason is tool_use.
	md := events[5]
	if !strings.Contains(md.Data, `"stop_reason":"tool_use"`) {
		t.Errorf("message_delta stop_reason not tool_use: %s", md.Data)
	}
}

// flow end-to-end: an Anthropic request carrying tools + an assistant tool_use
// turn + a user tool_result turn, against an OpenAI-shape upstream that
// responds with a fresh tool_call. The response must be Anthropic-shaped
// (content[].tool_use, stop_reason=tool_use). The captured upstream request
// must carry the OpenAI-shape tools/tool_calls/tool messages (verifying the
// decode direction).
func TestAnthropicIngress_NonStreaming_Tools(t *testing.T) {
	h := NewHarness(t)

	// Upstream replies with a tool_call.
	upBody := `{"id":"chatcmpl-x","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_42","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":15,"completion_tokens":5,"total_tokens":20}}`
	var captured []byte
	up := newMockUpstreamCapturing(upBody, &captured)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-t", "acme", "team", "key_t", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	reqBody := map[string]any{
		"max_tokens": 256,
		"system":     "you call tools",
		"tools": []map[string]any{{
			"name":        "get_weather",
			"description": "Get the weather",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []string{"city"},
			},
		}},
		"messages": []map[string]any{
			{"role": "user", "content": "what's the weather?"},
			{"role": "assistant", "content": []map[string]any{{
				"type": "tool_use", "id": "toolu_01", "name": "get_weather",
				"input": map[string]any{"city": "Tokyo"},
			}}},
			{"role": "user", "content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": "toolu_01",
				"content":     "Sunny, 25C",
			}}},
		},
	}

	resp := h.AnthropicMessages("sk-t", "chat", false, reqBody)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	var msg struct {
		Type    string `json:"type"`
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if msg.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", msg.StopReason)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != "tool_use" {
		t.Fatalf("content = %+v, want one tool_use block", msg.Content)
	}
	tb := msg.Content[0]
	if tb.ID != "call_42" || tb.Name != "get_weather" {
		t.Errorf("tool_use id/name = %q/%q", tb.ID, tb.Name)
	}
	if !strings.Contains(string(tb.Input), "Paris") {
		t.Errorf("input not Paris: %s", tb.Input)
	}

	// Verify the upstream received the OpenAI-shape request: tools with
	// function.parameters, assistant message with tool_calls, tool message.
	var upReq struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCallID string          `json:"tool_call_id,omitempty"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(captured, &upReq); err != nil {
		t.Fatalf("decode captured upstream: %v; raw=%s", err, captured)
	}
	if len(upReq.Tools) != 1 || upReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("upstream tools = %+v", upReq.Tools)
	}
	if len(upReq.Tools) > 0 {
		if !strings.Contains(string(upReq.Tools[0].Function.Parameters), `"type":"object"`) {
			t.Errorf("parameters not preserved verbatim: %s", upReq.Tools[0].Function.Parameters)
		}
	}
	// Messages: system, user, assistant(tool_calls), tool
	if len(upReq.Messages) != 4 {
		t.Fatalf("upstream messages len = %d, want 4", len(upReq.Messages))
	}
	if upReq.Messages[0].Role != "system" {
		t.Errorf("upstream msg[0] role = %q, want system", upReq.Messages[0].Role)
	}
	if upReq.Messages[2].Role != "assistant" || len(upReq.Messages[2].ToolCalls) != 1 {
		t.Errorf("upstream msg[2] (assistant tool_calls) wrong: %+v", upReq.Messages[2])
	}
	if upReq.Messages[3].Role != "tool" || upReq.Messages[3].ToolCallID != "toolu_01" {
		t.Errorf("upstream msg[3] (tool result) wrong: role=%q id=%q", upReq.Messages[3].Role, upReq.Messages[3].ToolCallID)
	}
}

// auth (OpenAI convention) works for the /v1/messages endpoint — clients
// using the official Anthropic SDK can set ANTHROPIC_AUTH_TOKEN to a Bearer and
// not have to use x-api-key. (x-api-key support lands in Step 6; the error
// envelope shape on rejection lands in Step 7.)
func TestAnthropicIngress_BearerAuthWorks(t *testing.T) {
	h := NewHarness(t)
	up := jsonUpstream("ok", 1, 1, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-bearer", "acme", "team", "key_bearer", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	// Valid bearer → 200 (Anthropic-shaped response).
	resp := h.AnthropicMessages("sk-bearer", "chat", false, map[string]any{
		"max_tokens": 16, "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("valid bearer status = %d, want 200; body=%s", resp.StatusCode, b)
	}
}

// TestAnthropicIngress_Streaming_Text verifies the streaming SSE path: the
// gateway translates the OpenAI-style upstream SSE (delta.content chunks, a
// trailing usage-only chunk, [DONE]) into an Anthropic-shaped event sequence
// (message_start → content_block_start → content_block_delta → content_block_stop
// → message_delta → message_stop). The streamed text must round-trip intact.
func TestAnthropicIngress_Streaming_Text(t *testing.T) {
	h := NewHarness(t)
	up := sseUpstream("chunked hi", 13, 9, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-s", "acme", "team", "key_s", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.AnthropicMessages("sk-s", "chat", true, map[string]any{
		"max_tokens": 32,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; body=%s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	events := scanSSEEvents(t, resp.Body)

	// Expected event sequence.
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if len(events) < len(want) {
		t.Fatalf("event count = %d, want at least %d", len(events), len(want))
	}
	for i, w := range want {
		if events[i].Event != w {
			t.Fatalf("events[%d].Event = %q, want %q (full: %+v)", i, events[i].Event, w, sseEventNames(events))
		}
	}

	// Verify the streamed text round-trips.
	var textParts []string
	for _, ev := range events {
		if ev.Event != "content_block_delta" {
			continue
		}
		var d struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &d); err != nil {
			t.Fatalf("decode content_block_delta: %v; data=%s", err, ev.Data)
		}
		textParts = append(textParts, d.Delta.Text)
	}
	if got := strings.Join(textParts, ""); got != "chunked hi" {
		t.Errorf("streamed text = %q, want 'chunked hi'", got)
	}

	// Verify message_delta carries output_tokens from the trailing usage.
	var lastMD struct {
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	}
	for _, ev := range events {
		if ev.Event == "message_delta" {
			_ = json.Unmarshal([]byte(ev.Data), &lastMD)
		}
	}
	if lastMD.Usage.OutputTokens != 9 {
		t.Errorf("message_delta output_tokens = %d, want 9", lastMD.Usage.OutputTokens)
	}
	if lastMD.Delta.StopReason != "end_turn" {
		t.Errorf("message_delta stop_reason = %q, want end_turn", lastMD.Delta.StopReason)
	}
}

// AnthropicMessages sends an Anthropic /v1/messages request. key is sent as a
// Bearer token (the OpenAI convention — works for /v1/messages too; x-api-key
// support lands in Step 6). The caller owns closing resp.Body.
func (h *Harness) AnthropicMessages(key, model string, stream bool, extra map[string]any) *http.Response {
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
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// anthropic-version header: some Anthropic SDKs add it, the gateway
	// doesn't require it, but including it keeps the test close to a real
	// Claude Code request.
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("messages request: %v", err)
	}
	return resp
}

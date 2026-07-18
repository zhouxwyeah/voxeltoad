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

// TestChat_MultiTurnConversationForwarded drives a chat request carrying a
// full conversation history (system + alternating user/assistant turns) and
// asserts the gateway forwards every message, in order, to the upstream
// provider unmodified — not just the final user turn.
func TestChat_MultiTurnConversationForwarded(t *testing.T) {
	h := NewHarness(t)

	var captured []byte
	up := capturingUpstream("ok", 4, 4, &captured)
	defer up.Close()

	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-mt", "acme", "team", "key_mt", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	history := []map[string]string{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "What is 2+2?"},
		{"role": "assistant", "content": "4."},
		{"role": "user", "content": "And 3+3?"},
	}

	resp := h.ChatMessages("sk-mt", "chat", false, history, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var upstreamReq struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(captured, &upstreamReq); err != nil {
		t.Fatalf("decode upstream request: %v; body=%s", err, captured)
	}
	if len(upstreamReq.Messages) != len(history) {
		t.Fatalf("upstream received %d messages, want %d; body=%s", len(upstreamReq.Messages), len(history), captured)
	}
	for i, want := range history {
		got := upstreamReq.Messages[i]
		if got.Role != want["role"] || got.Content != want["content"] {
			t.Errorf("message[%d] = {role:%q, content:%q}, want {role:%q, content:%q}",
				i, got.Role, got.Content, want["role"], want["content"])
		}
	}
}

// TestChat_ToolCallConversationForwarded verifies that a multi-turn
// conversation with tool calls (assistant tool_calls + tool responses with
// tool_call_id) is forwarded to the upstream provider with all tool fields
// intact.
func TestChat_ToolCallConversationForwarded(t *testing.T) {
	h := NewHarness(t)

	var captured []byte
	up := capturingUpstream("ok", 4, 4, &captured)
	defer up.Close()

	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-tool", "acme", "team", "key_tool", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	// Simulate a tool-calling conversation: user asks a question, model requests
	// a tool call, tool returns the result. ChatMessages only accepts
	// []map[string]string, so we send raw JSON to include nested tool_calls.
	requestBody := map[string]any{
		"model":  "chat",
		"stream": false,
		"messages": []map[string]any{
			{"role": "user", "content": "What is the weather in Beijing?"},
			{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{
					{
						"id":   "call_abc123",
						"type": "function",
						"function": map[string]any{
							"name":      "get_weather",
							"arguments": `{"city":"Beijing"}`,
						},
					},
				},
			},
			{
				"role":         "tool",
				"content":      `{"temperature":25,"condition":"sunny"}`,
				"tool_call_id": "call_abc123",
			},
		},
	}
	b, _ := json.Marshal(requestBody)
	req, _ := http.NewRequest(http.MethodPost, h.GatewayURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-tool")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Parse the upstream request to verify tool fields were preserved.
	var upstreamReq struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id,omitempty"`
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
	if err := json.Unmarshal(captured, &upstreamReq); err != nil {
		t.Fatalf("decode upstream request: %v; body=%s", err, captured)
	}
	if len(upstreamReq.Messages) != 3 {
		t.Fatalf("upstream received %d messages, want 3; body=%s", len(upstreamReq.Messages), captured)
	}

	// Message 0: user
	if upstreamReq.Messages[0].Role != "user" {
		t.Errorf("msg[0].role = %q, want user", upstreamReq.Messages[0].Role)
	}

	// Message 1: assistant with tool_calls
	assist := upstreamReq.Messages[1]
	if assist.Role != "assistant" {
		t.Errorf("msg[1].role = %q, want assistant", assist.Role)
	}
	if len(assist.ToolCalls) != 1 {
		t.Fatalf("msg[1].tool_calls len = %d, want 1", len(assist.ToolCalls))
	}
	if assist.ToolCalls[0].ID != "call_abc123" {
		t.Errorf("msg[1].tool_calls[0].id = %q, want call_abc123", assist.ToolCalls[0].ID)
	}
	if assist.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("msg[1].tool_calls[0].function.name = %q, want get_weather", assist.ToolCalls[0].Function.Name)
	}
	if assist.ToolCalls[0].Function.Arguments != `{"city":"Beijing"}` {
		t.Errorf("msg[1].tool_calls[0].function.arguments = %q, want {\"city\":\"Beijing\"}", assist.ToolCalls[0].Function.Arguments)
	}

	// Message 2: tool with tool_call_id
	toolMsg := upstreamReq.Messages[2]
	if toolMsg.Role != "tool" {
		t.Errorf("msg[2].role = %q, want tool", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_abc123" {
		t.Errorf("msg[2].tool_call_id = %q, want call_abc123", toolMsg.ToolCallID)
	}
}

// TestChat_StreamToolCallRoundTrip verifies end-to-end that a streaming
// chat completion with tools delivers tool_call deltas to the client and
// that argument fragments, when reassembled by Index, produce valid tool
// calls with correct finish_reason.
func TestChat_StreamToolCallRoundTrip(t *testing.T) {
	h := NewHarness(t)

	toolChunks := []string{
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_w","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"BJ\"}"}}]},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	up := sseToolCallUpstream(toolChunks, 10, 20, nil)
	defer up.Close()

	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-stream-tool", "acme", "team", "key_stream_tool", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	requestBody := map[string]any{
		"model":  "chat",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "What is the weather in Beijing?"},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "Get the weather for a city",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{"city": map[string]any{"type": "string"}},
						"required":   []string{"city"},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(requestBody)
	req, _ := http.NewRequest(http.MethodPost, h.GatewayURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-stream-tool")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	type assembled struct {
		id, name, arguments string
	}
	byIndex := map[int]*assembled{}
	var finish string
	var usageSeen bool

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("unmarshal chunk: %v\npayload: %s", err, payload)
		}
		if chunk.Usage != nil {
			usageSeen = true
		}
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].FinishReason != nil {
				finish = *chunk.Choices[0].FinishReason
			}
			for _, tc := range chunk.Choices[0].Delta.ToolCalls {
				a := byIndex[tc.Index]
				if a == nil {
					a = &assembled{}
					byIndex[tc.Index] = a
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				a.arguments += tc.Function.Arguments
			}
		}
	}
	if sc.Err() != nil {
		t.Fatalf("scanner: %v", sc.Err())
	}

	if finish != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", finish)
	}
	if !usageSeen {
		t.Error("expected trailing usage chunk")
	}
	if len(byIndex) != 1 {
		t.Fatalf("got %d tool calls, want 1: %+v", len(byIndex), byIndex)
	}
	tc := byIndex[0]
	if tc.id != "call_w" {
		t.Errorf("tool call id = %q, want call_w", tc.id)
	}
	if tc.name != "get_weather" {
		t.Errorf("tool call name = %q, want get_weather", tc.name)
	}
	if tc.arguments != `{"city":"BJ"}` {
		t.Errorf("reassembled arguments = %q, want {\"city\":\"BJ\"}", tc.arguments)
	}
}

// TestChat_StreamMultiTurnToolCall verifies end-to-end that a multi-turn
// conversation with streaming and tool calls forwards the full history
// correctly to the upstream across turns.
func TestChat_StreamMultiTurnToolCall(t *testing.T) {
	h := NewHarness(t)

	toolChunks := []string{
		`{"id":"s1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_time","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"s1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"tz\":\"CST\"}"}}]},"finish_reason":null}]}`,
		`{"id":"s1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	up := sseToolCallUpstream(toolChunks, 10, 5, nil)
	defer up.Close()

	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-mtt", "acme", "team", "key_mtt", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	// Turn 1: get streaming tool call response.
	requestBody := map[string]any{
		"model":  "chat",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "What time is it?"},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":       "get_time",
					"parameters": map[string]any{"type": "object", "properties": map[string]any{"tz": map[string]any{"type": "string"}}},
				},
			},
		},
	}
	b, _ := json.Marshal(requestBody)
	req, _ := http.NewRequest(http.MethodPost, h.GatewayURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-mtt")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	var turn1Finish string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		json.Unmarshal([]byte(payload), &chunk)
		if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
			turn1Finish = *chunk.Choices[0].FinishReason
		}
	}
	resp.Body.Close()
	if turn1Finish != "tool_calls" {
		t.Fatalf("turn 1 finish_reason = %q, want tool_calls", turn1Finish)
	}

	// Turn 2: non-streaming turn with full tool history.
	var captured []byte
	up.SetHandler(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = b
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"3PM CST"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}`))
	})

	requestBody2 := map[string]any{
		"model":  "chat",
		"stream": false,
		"messages": []map[string]any{
			{"role": "user", "content": "What time is it?"},
			{
				"role": "assistant", "content": nil,
				"tool_calls": []map[string]any{
					{"id": "call_abc", "type": "function", "function": map[string]any{"name": "get_time", "arguments": `{"tz":"CST"}`}},
				},
			},
			{"role": "tool", "content": "3PM CST", "tool_call_id": "call_abc"},
			{"role": "user", "content": "Thanks, what about tomorrow?"},
		},
	}
	b2, _ := json.Marshal(requestBody2)
	req2, _ := http.NewRequest(http.MethodPost, h.GatewayURL+"/v1/chat/completions", bytes.NewReader(b2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer sk-mtt")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("turn 2 status = %d, want 200", resp2.StatusCode)
	}

	var upstreamReq struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id,omitempty"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(captured, &upstreamReq); err != nil {
		t.Fatalf("decode turn 2 upstream: %v; body=%s", err, captured)
	}
	if len(upstreamReq.Messages) != 4 {
		t.Fatalf("upstream received %d messages, want 4; body=%s", len(upstreamReq.Messages), captured)
	}
	if upstreamReq.Messages[1].Role != "assistant" || len(upstreamReq.Messages[1].ToolCalls) != 1 || upstreamReq.Messages[1].ToolCalls[0].ID != "call_abc" {
		t.Errorf("msg[1] = %+v, want assistant with tool_calls[call_abc]", upstreamReq.Messages[1])
	}
	if upstreamReq.Messages[2].Role != "tool" || upstreamReq.Messages[2].ToolCallID != "call_abc" {
		t.Errorf("msg[2] = {role:%q tool_call_id:%q}, want tool with call_abc", upstreamReq.Messages[2].Role, upstreamReq.Messages[2].ToolCallID)
	}
}

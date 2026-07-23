package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
)

// ---- decode: tools, tool_use, tool_result -------------------------------

// TestDecodeRequest_Tools verifies Anthropic tools (input_schema) map to
// unified tools (function.parameters, carried verbatim as JSON).
func TestDecodeRequest_Tools(t *testing.T) {
	body := []byte(`{
		"model": "m", "max_tokens": 16,
		"tools": [
			{"name": "get_weather", "description": "Get weather",
			 "input_schema": {"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}
		],
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	req, err := new(codec).DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("tools len = %d", len(req.Tools))
	}
	t0 := req.Tools[0]
	if t0.Type != "function" {
		t.Errorf("tool type = %q, want function", t0.Type)
	}
	if t0.Function.Name != "get_weather" {
		t.Errorf("function name = %q", t0.Function.Name)
	}
	if t0.Function.Description != "Get weather" {
		t.Errorf("description = %q", t0.Function.Description)
	}
	// parameters preserved verbatim
	var params map[string]any
	if err := json.Unmarshal(t0.Function.Parameters, &params); err != nil {
		t.Fatalf("unmarshal parameters: %v; raw=%s", err, t0.Function.Parameters)
	}
	if params["type"] != "object" {
		t.Errorf("parameters.type = %v, want object", params["type"])
	}
}

// TestDecodeRequest_ToolUseBlock verifies that an assistant message with a
// tool_use content block lifts into Message.ToolCalls (OpenAI shape) with the
// input JSON serialized as the arguments string.
func TestDecodeRequest_ToolUseBlock(t *testing.T) {
	body := []byte(`{
		"model": "m", "max_tokens": 16,
		"messages": [
			{"role": "user", "content": "what's the weather?"},
			{"role": "assistant", "content": [
				{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"city":"Paris"}}
			]}
		]
	}`)
	req, err := new(codec).DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	asst := req.Messages[1]
	if asst.Role != adapter.RoleAssistant {
		t.Errorf("asst role = %q", asst.Role)
	}
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("toolcalls len = %d", len(asst.ToolCalls))
	}
	tc := asst.ToolCalls[0]
	if tc.ID != "toolu_01" || tc.Type != "function" {
		t.Errorf("toolcall id/type = %q/%q", tc.ID, tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("name = %q", tc.Function.Name)
	}
	// arguments is the JSON string of input
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments not JSON: %v; raw=%s", err, tc.Function.Arguments)
	}
	if args["city"] != "Paris" {
		t.Errorf("arguments.city = %v", args["city"])
	}
}

// TestDecodeRequest_ToolResultBlock verifies a user message with a tool_result
// content block maps to a separate role=tool unified message carrying the
// tool_use_id (ToolCallID) and the flattened result content.
func TestDecodeRequest_ToolResultBlock(t *testing.T) {
	body := []byte(`{
		"model": "m", "max_tokens": 16,
		"messages": [
			{"role": "user", "content": "weather?"},
			{"role": "assistant", "content": [
				{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"city":"Paris"}}
			]},
			{"role": "user", "content": [
				{"type":"tool_result","tool_use_id":"toolu_01","content":"Sunny, 18C"}
			]}
		]
	}`)
	req, err := new(codec).DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	// Expect 3 messages: user, assistant-with-toolcalls, tool
	if len(req.Messages) != 3 {
		t.Fatalf("messages len = %d, want 3 (user, assistant, tool): %+v", len(req.Messages), req.Messages)
	}
	tool := req.Messages[2]
	if tool.Role != adapter.RoleTool {
		t.Errorf("third message role = %q, want tool", tool.Role)
	}
	if tool.ToolCallID != "toolu_01" {
		t.Errorf("ToolCallID = %q", tool.ToolCallID)
	}
	if got := tool.Content.Text(); got != "Sunny, 18C" {
		t.Errorf("content = %q, want 'Sunny, 18C'", got)
	}
}

// TestDecodeRequest_ToolChoiceMapping verifies the Anthropic tool_choice →
// OpenAI tool_choice mapping for the three structured forms.
func TestDecodeRequest_ToolChoiceMapping(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want any
	}{
		{"auto", `{"type":"auto"}`, "auto"},
		{"any", `{"type":"any"}`, "required"},
		{"none", `{"type":"none"}`, "none"},
		{"tool", `{"type":"tool","name":"get_weather"}`, map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := []byte(`{"model":"m","max_tokens":16,"tool_choice":` + c.raw + `,"messages":[{"role":"user","content":"hi"}]}`)
			req, err := new(codec).DecodeRequest(body)
			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}
			gotJSON, _ := json.Marshal(req.ToolChoice)
			wantJSON, _ := json.Marshal(c.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("tool_choice %s: got %s, want %s", c.name, gotJSON, wantJSON)
			}
		})
	}
}

// ---- encode: tool_calls → tool_use --------------------------------------

// TestEncodeResponse_ToolCalls verifies a unified response with tool_calls
// maps to an Anthropic message whose content array contains tool_use blocks
// (with input as a nested JSON object) and stop_reason is tool_use.
func TestEncodeResponse_ToolCalls(t *testing.T) {
	resp := &adapter.UnifiedResponse{
		ID:    "chatcmpl-x",
		Model: "gpt-4o",
		Choices: []adapter.Choice{{
			Index: 0,
			Message: adapter.Message{
				Role: adapter.RoleAssistant,
				ToolCalls: []adapter.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: adapter.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"city":"Paris"}`,
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: &adapter.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	}
	b, err := new(codec).EncodeResponse(resp)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, b)
	}
	if got["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use", got["stop_reason"])
	}
	content, _ := got["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1 (no text block)", len(content))
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "tool_use" {
		t.Errorf("block type = %v, want tool_use", block["type"])
	}
	if block["id"] != "call_1" || block["name"] != "get_weather" {
		t.Errorf("block id/name = %v/%v", block["id"], block["name"])
	}
	input, _ := block["input"].(map[string]any)
	if input["city"] != "Paris" {
		t.Errorf("input.city = %v, want Paris", input["city"])
	}
}

// TestEncodeResponse_TextAndToolCalls verifies a response with BOTH text
// content and tool_calls produces a content array with a text block followed
// by a tool_use block (the OpenAI ordering preserved).
func TestEncodeResponse_TextAndToolCalls(t *testing.T) {
	resp := &adapter.UnifiedResponse{
		ID:    "chatcmpl-x",
		Model: "gpt-4o",
		Choices: []adapter.Choice{{
			Index: 0,
			Message: adapter.Message{
				Role:    adapter.RoleAssistant,
				Content: adapter.NewContentText("calling tool"),
				ToolCalls: []adapter.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: adapter.FunctionCall{
						Name:      "go",
						Arguments: `{}`,
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}
	b, _ := new(codec).EncodeResponse(resp)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	content, _ := got["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2 (text + tool_use)", len(content))
	}
	b0, _ := content[0].(map[string]any)
	b1, _ := content[1].(map[string]any)
	if b0["type"] != "text" || b0["text"] != "calling tool" {
		t.Errorf("block 0 = %+v, want text 'calling tool'", b0)
	}
	if b1["type"] != "tool_use" {
		t.Errorf("block 1 type = %v, want tool_use", b1["type"])
	}
}

// TestEncodeResponse_MalformedArgumentsFallsBackToObject ensures a tool call
// with arguments that don't parse as JSON still produces a valid tool_use
// block (input defaults to an empty object).
func TestEncodeResponse_MalformedArgumentsFallsBackToObject(t *testing.T) {
	resp := &adapter.UnifiedResponse{
		Choices: []adapter.Choice{{
			Message: adapter.Message{
				Role: adapter.RoleAssistant,
				ToolCalls: []adapter.ToolCall{{
					ID: "x", Type: "function",
					Function: adapter.FunctionCall{Name: "go", Arguments: "not json"},
				}},
			},
		}},
	}
	b, _ := new(codec).EncodeResponse(resp)
	if !strings.Contains(string(b), `"input":{}`) {
		t.Errorf("expected fallback input={}; body=%s", b)
	}
}

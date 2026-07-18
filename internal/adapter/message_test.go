package adapter_test

import (
	"encoding/json"
	"testing"

	"voxeltoad/internal/adapter"
)

// TestMessage_ToolCallID_RoundTrip verifies that tool_call_id survives a
// JSON marshal/unmarshal round-trip on a tool-role message.
func TestMessage_ToolCallID_RoundTrip(t *testing.T) {
	orig := adapter.Message{
		Role:       adapter.RoleTool,
		Content:    adapter.NewContentText("result from function"),
		ToolCallID: "call_abc123",
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored adapter.Message
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.ToolCallID != "call_abc123" {
		t.Errorf("ToolCallID = %q, want call_abc123", restored.ToolCallID)
	}
	if restored.Role != adapter.RoleTool {
		t.Errorf("Role = %q, want tool", restored.Role)
	}
	if restored.Content.Text() != "result from function" {
		t.Errorf("Content = %q, want result from function", restored.Content.Text())
	}

	// Verify the serialized JSON contains the field.
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["tool_call_id"] != "call_abc123" {
		t.Errorf("raw[tool_call_id] = %v, want call_abc123", raw["tool_call_id"])
	}
}

// TestMessage_ToolCalls_RoundTrip verifies that assistant messages with
// tool_calls survive a JSON round-trip.
func TestMessage_ToolCalls_RoundTrip(t *testing.T) {
	orig := adapter.Message{
		Role:    adapter.RoleAssistant,
		Content: adapter.Content{},
		ToolCalls: []adapter.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: adapter.FunctionCall{
					Name:      "get_weather",
					Arguments: `{"city":"Beijing"}`,
				},
			},
			{
				ID:   "call_2",
				Type: "function",
				Function: adapter.FunctionCall{
					Name:      "get_time",
					Arguments: `{"timezone":"Asia/Shanghai"}`,
				},
			},
		},
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored adapter.Message
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(restored.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(restored.ToolCalls))
	}
	if restored.ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCalls[0].ID = %q, want call_1", restored.ToolCalls[0].ID)
	}
	if restored.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("ToolCalls[0].Function.Name = %q, want get_weather", restored.ToolCalls[0].Function.Name)
	}
	if restored.ToolCalls[1].ID != "call_2" {
		t.Errorf("ToolCalls[1].ID = %q, want call_2", restored.ToolCalls[1].ID)
	}
}

// TestMessage_Name_RoundTrip verifies that the optional name field survives
// a JSON round-trip.
func TestMessage_Name_RoundTrip(t *testing.T) {
	orig := adapter.Message{
		Role:    adapter.RoleUser,
		Content: adapter.NewContentText("hello"),
		Name:    "alice",
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored adapter.Message
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.Name != "alice" {
		t.Errorf("Name = %q, want alice", restored.Name)
	}
}

// TestMessage_OmitEmpty_ExcludesOptionalFields verifies that messages
// without optional tool fields do NOT emit them in JSON (omitempty).
func TestMessage_OmitEmpty_ExcludesOptionalFields(t *testing.T) {
	orig := adapter.Message{
		Role:    adapter.RoleUser,
		Content: adapter.NewContentText("plain message"),
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// Optional fields must not appear in the output.
	for _, key := range []string{"tool_call_id", "tool_calls", "name"} {
		if _, ok := raw[key]; ok {
			t.Errorf("key %q should be omitted for a plain message, but is present", key)
		}
	}
	// Required fields must be present.
	if raw["role"] != "user" {
		t.Errorf("role = %v, want user", raw["role"])
	}
	if raw["content"] != "plain message" {
		t.Errorf("content = %v, want plain message", raw["content"])
	}
}

// TestMessage_AllFields_RoundTrip verifies a full message with every field
// survives JSON round-trip intact.
func TestMessage_AllFields_RoundTrip(t *testing.T) {
	orig := adapter.Message{
		Role:       adapter.RoleAssistant,
		Content:    adapter.NewContentText("Let me check the weather for you."),
		ToolCallID: "",
		Name:       "weather_bot",
		ToolCalls: []adapter.ToolCall{
			{
				ID:   "call_x",
				Type: "function",
				Function: adapter.FunctionCall{
					Name:      "get_weather",
					Arguments: `{"city":"Beijing"}`,
				},
			},
		},
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored adapter.Message
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.Role != adapter.RoleAssistant {
		t.Errorf("Role = %q", restored.Role)
	}
	if restored.Content.Text() != "Let me check the weather for you." {
		t.Errorf("Content = %q", restored.Content.Text())
	}
	if restored.Name != "weather_bot" {
		t.Errorf("Name = %q", restored.Name)
	}
	if len(restored.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls)=%d, want 1", len(restored.ToolCalls))
	}
	if restored.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("Function.Name = %q", restored.ToolCalls[0].Function.Name)
	}
	if restored.ToolCalls[0].Function.Arguments != `{"city":"Beijing"}` {
		t.Errorf("Function.Arguments = %q", restored.ToolCalls[0].Function.Arguments)
	}
}

// TestMessage_Unmarshal_RequiredFieldsOnly verifies that JSON with only
// role+content (no tool fields) unmarshals correctly into the extended struct.
func TestMessage_Unmarshal_RequiredFieldsOnly(t *testing.T) {
	data := `{"role":"user","content":"hi"}`
	var m adapter.Message
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != adapter.RoleUser || m.Content.Text() != "hi" {
		t.Errorf("got {role:%q, content:%q}, want {role:user, content:hi}", m.Role, m.Content.Text())
	}
	if m.ToolCallID != "" {
		t.Errorf("ToolCallID should be empty, got %q", m.ToolCallID)
	}
	if len(m.ToolCalls) != 0 {
		t.Errorf("ToolCalls should be nil, got %v", m.ToolCalls)
	}
	if m.Name != "" {
		t.Errorf("Name should be empty, got %q", m.Name)
	}
}

// --- Content type tests ---

// TestContent_StringRoundTrip verifies that a plain-text Content survives
// JSON marshal/unmarshal unchanged.
func TestContent_StringRoundTrip(t *testing.T) {
	msg := adapter.Message{Role: adapter.RoleUser, Content: adapter.NewContentText("hello")}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["content"] != "hello" {
		t.Errorf("raw[content] = %v, want hello", raw["content"])
	}

	var restored adapter.Message
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Content.Text() != "hello" {
		t.Errorf("Content.Text() = %q, want hello", restored.Content.Text())
	}
	if restored.Content.IsNull() {
		t.Error("IsNull() should be false for string content")
	}
	if restored.Content.IsMultipart() {
		t.Error("IsMultipart() should be false for string content")
	}
}

// TestContent_NullRoundTrip verifies that null content survives JSON
// round-trip unchanged — critical for assistant messages with tool_calls
// where content must be null, not empty string.
func TestContent_NullRoundTrip(t *testing.T) {
	// Unmarshal a message with content: null.
	data := `{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}`
	var msg adapter.Message
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !msg.Content.IsNull() {
		t.Error("expected Content.IsNull() after unmarshalling null")
	}
	if msg.Content.Text() != "" {
		t.Errorf("Content.Text() = %q, want empty for null", msg.Content.Text())
	}

	// Marshal back — content must be null, not "" and not missing.
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["content"] != nil {
		t.Errorf("raw[content] = %v, want null", raw["content"])
	}
	// "content" key must exist (not omitted), even when null.
	if _, ok := raw["content"]; !ok {
		t.Error("content key should exist in JSON even when null")
	}
}

// TestContent_MultipartRoundTrip verifies that a multipart content array
// ([{type:text,...},{type:image_url,...}]) survives JSON round-trip
// byte-for-byte.
func TestContent_MultipartRoundTrip(t *testing.T) {
	data := `{"role":"user","content":[{"type":"text","text":"describe this image"},{"type":"image_url","image_url":{"url":"https://example.com/photo.jpg"}}]}`
	var msg adapter.Message
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !msg.Content.IsMultipart() {
		t.Error("expected Content.IsMultipart() after unmarshalling array")
	}
	if msg.Content.Text() != "describe this image" {
		t.Errorf("Content.Text() = %q, want 'describe this image'", msg.Content.Text())
	}
	if msg.Content.IsNull() {
		t.Error("IsNull() should be false for array content")
	}

	// Marshal back — must preserve array structure.
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	content, ok := raw["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content array = %v, want 2 elements", raw["content"])
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Errorf("content[0].type want text, got %v", content[0])
	}
	if content[1].(map[string]any)["type"] != "image_url" {
		t.Errorf("content[1].type want image_url, got %v", content[1])
	}
}

// TestContent_SetText verifies that SetText replaces content with a string.
func TestContent_SetText(t *testing.T) {
	var c adapter.Content
	c.SetText("new text")
	if c.Text() != "new text" {
		t.Errorf("Text() = %q, want 'new text'", c.Text())
	}
	if c.IsNull() {
		t.Error("IsNull() should be false after SetText")
	}

	// Marshal as JSON string.
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"new text"` {
		t.Errorf("marshalled = %s, want \"new text\"", b)
	}
}

// TestContent_ZeroValue_EmitsNull verifies that the zero-value Content
// marshals as null, matching the OpenAI spec for assistant messages with
// tool_calls (content must be null, not missing and not "").
func TestContent_ZeroValue_EmitsNull(t *testing.T) {
	msg := adapter.Message{Role: adapter.RoleAssistant, Content: adapter.Content{}, ToolCalls: []adapter.ToolCall{
		{ID: "c1", Type: "function", Function: adapter.FunctionCall{Name: "f", Arguments: "{}"}},
	}}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["content"] != nil {
		t.Errorf(`raw[content] = %v, want null`, raw["content"])
	}
}

// --- UnifiedRequest Extra (passthrough) tests ---

// TestUnifiedRequest_UnmarshalJSON_CapturesUnknownFields verifies that
// client-supplied fields not explicitly modelled in UnifiedRequest are
// captured in Extra — countering the data loss bug where response_format,
// n, stop, seed, etc. were silently discarded.
func TestUnifiedRequest_UnmarshalJSON_CapturesUnknownFields(t *testing.T) {
	data := `{
		"model": "gpt-4o",
		"messages": [{"role":"user","content":"hi"}],
		"response_format": {"type": "json_object"},
		"n": 3,
		"stop": ["\n"],
		"seed": 42
	}`
	var req adapter.UnifiedRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if req.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", req.Model)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(req.Messages))
	}
	if req.Extra == nil {
		t.Fatal("Extra must not be nil after unmarshal; unknown fields should be captured")
	}
	// response_format captured.
	if _, ok := req.Extra["response_format"]; !ok {
		t.Error("Extra missing response_format")
	}
	// n captured.
	if _, ok := req.Extra["n"]; !ok {
		t.Error("Extra missing n")
	}
	// stop captured.
	if _, ok := req.Extra["stop"]; !ok {
		t.Error("Extra missing stop")
	}
	// seed captured.
	if _, ok := req.Extra["seed"]; !ok {
		t.Error("Extra missing seed")
	}
	// Known fields must NOT appear in Extra.
	for _, known := range []string{"model", "messages", "stream"} {
		if _, ok := req.Extra[known]; ok {
			t.Errorf("known field %q leaked into Extra", known)
		}
	}
}

// TestUnifiedRequest_MarshalJSON_MergesExtra verifies that Extra fields are
// merged back into the serialized JSON, and that known fields take priority
// over colliding Extra entries.
func TestUnifiedRequest_MarshalJSON_MergesExtra(t *testing.T) {
	// Build a request with both known and unknown fields.
	data := `{
		"model": "gpt-4o",
		"messages": [{"role":"user","content":"hi"}],
		"response_format": {"type": "json_object"},
		"n": 3
	}`
	var req adapter.UnifiedRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Known fields present.
	if raw["model"] != "gpt-4o" {
		t.Errorf("model = %v, want gpt-4o", raw["model"])
	}
	// Extra fields merged back.
	if _, ok := raw["response_format"]; !ok {
		t.Error("response_format not merged from Extra")
	}
	if v, _ := raw["n"].(float64); v != 3 {
		t.Errorf("n = %v, want 3", raw["n"])
	}
}

// TestUnifiedRequest_MarshalJSON_KnownFieldsPriority verifies that known
// fields are never overwritten by Extra entries — even if someone
// explicitly populates Extra with a colliding key.
func TestUnifiedRequest_MarshalJSON_KnownFieldsPriority(t *testing.T) {
	req := adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Messages: []adapter.Message{{Role: "user", Content: adapter.NewContentText("hi")}},
		Extra:    map[string]json.RawMessage{"model": json.RawMessage(`"hijacked"`)},
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if raw["model"] != "gpt-4o" {
		t.Errorf("model = %v, want gpt-4o (Extra must not overwrite known field)", raw["model"])
	}
}

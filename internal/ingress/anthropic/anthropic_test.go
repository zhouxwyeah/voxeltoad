package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
)

// ---- DecodeRequest ------------------------------------------------------

// TestDecodeRequest_PlainText decodes a minimal Anthropic Messages request:
// model, max_tokens, and a single user text message. Verifies system is absent,
// the user message maps to unified, and max_tokens is preserved.
func TestDecodeRequest_PlainText(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 256,
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	req, err := new(codec).DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 256 {
		t.Errorf("MaxTokens = %v", req.MaxTokens)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(req.Messages))
	}
	m := req.Messages[0]
	if m.Role != adapter.RoleUser {
		t.Errorf("role = %q, want user", m.Role)
	}
	if got := m.Content.Text(); got != "hi" {
		t.Errorf("content = %q, want hi", got)
	}
}

// TestDecodeRequest_StringSystem verifies that a top-level string system prompt
// is lifted into a leading unified system message.
func TestDecodeRequest_StringSystem(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"max_tokens": 64,
		"system": "You are helpful.",
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	req, err := new(codec).DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(req.Messages))
	}
	if req.Messages[0].Role != adapter.RoleSystem {
		t.Errorf("first message role = %q, want system", req.Messages[0].Role)
	}
	if got := req.Messages[0].Content.Text(); got != "You are helpful." {
		t.Errorf("system content = %q", got)
	}
	if req.Messages[1].Role != adapter.RoleUser {
		t.Errorf("second message role = %q, want user", req.Messages[1].Role)
	}
}

// TestDecodeRequest_ArraySystem verifies that an array-of-content-blocks system
// prompt is concatenated into a single leading unified system message.
func TestDecodeRequest_ArraySystem(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"max_tokens": 64,
		"system": [{"type":"text","text":"Part A."},{"type":"text","text":"Part B."}],
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	req, err := new(codec).DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != adapter.RoleSystem {
		t.Fatalf("expected system + user, got %+v", req.Messages)
	}
	if got, want := req.Messages[0].Content.Text(), "Part A.\n\nPart B."; got != want {
		t.Errorf("system concat = %q, want %q", got, want)
	}
}

// TestDecodeRequest_ArrayContentText verifies that Anthropic array-form content
// (array of {type:text,...} blocks) is preserved as multipart content (the
// unified Content is raw-backed and supports arrays).
func TestDecodeRequest_ArrayContentText(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"max_tokens": 64,
		"messages": [{"role": "user", "content": [{"type":"text","text":"hello"},{"type":"text","text":"world"}]}]
	}`)
	req, err := new(codec).DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	if !req.Messages[0].Content.IsMultipart() {
		t.Errorf("content not multipart: %s", req.Messages[0].Content.Text())
	}
	if got := req.Messages[0].Content.Text(); got != "helloworld" {
		t.Errorf("concat text = %q", got)
	}
}

// TestDecodeRequest_ImageUnsupported confirms image content blocks fail
// decode explicitly (rather than silently drop them) in the first version. This
// is the decision recorded in ADR-0045 §gap.
func TestDecodeRequest_ImageUnsupported(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"max_tokens": 64,
		"messages": [{"role": "user", "content": [{"type":"image","source":{"data":"x"}}]}]
	}`)
	_, err := new(codec).DecodeRequest(body)
	if err == nil {
		t.Fatalf("expected error on image content, got nil")
	}
	if !strings.Contains(err.Error(), "image") && !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error doesn't mention image/unsupported: %v", err)
	}
}

// ---- EncodeResponse -----------------------------------------------------

// TestEncodeResponse_PlainText verifies that a unified response with one text
// choice is encoded as an Anthropic Message: type=message, role=assistant,
// content[].type=text, stop_reason=end_turn, and usage with input/output tokens.
func TestEncodeResponse_PlainText(t *testing.T) {
	resp := &adapter.UnifiedResponse{
		ID:    "chatcmpl-abc",
		Model: "gpt-4o",
		Choices: []adapter.Choice{{
			Index:        0,
			Message:      adapter.Message{Role: adapter.RoleAssistant, Content: adapter.NewContentText("hello")},
			FinishReason: "stop",
		}},
		Usage: &adapter.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	b, err := new(codec).EncodeResponse(resp)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, b)
	}
	if got["type"] != "message" {
		t.Errorf("type = %v, want message", got["type"])
	}
	if got["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", got["role"])
	}
	content, _ := got["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1", len(content))
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "hello" {
		t.Errorf("content[0] = %+v, want {type:text text:hello}", block)
	}
	if got["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", got["stop_reason"])
	}
	usage, _ := got["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) {
		t.Errorf("usage = %+v, want input=10 output=5", usage)
	}
}

// TestEncodeResponse_FinishReasonMapping verifies the OpenAI finish_reason →
// Anthropic stop_reason mapping for the three cases the first version sees:
// stop → end_turn, length → max_tokens, tool_calls → tool_use.
func TestEncodeResponse_FinishReasonMapping(t *testing.T) {
	cases := []struct{ in, want string }{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"", ""},
	}
	for _, c := range cases {
		resp := &adapter.UnifiedResponse{
			Choices: []adapter.Choice{{FinishReason: c.in, Message: adapter.Message{Role: adapter.RoleAssistant}}},
		}
		b, err := new(codec).EncodeResponse(resp)
		if err != nil {
			t.Fatalf("EncodeResponse(%q): %v", c.in, err)
		}
		var got map[string]any
		_ = json.Unmarshal(b, &got)
		if got["stop_reason"] != c.want {
			t.Errorf("finish_reason=%q → stop_reason=%v, want %q", c.in, got["stop_reason"], c.want)
		}
	}
}

// ---- EncodeError --------------------------------------------------------

// TestEncodeError_Envelope verifies the Anthropic error envelope shape
// {"type":"error","error":{"type":...,"message":...}}.
func TestEncodeError_Envelope(t *testing.T) {
	b := new(codec).EncodeError(401, "authentication_error", "bad key")
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, b)
	}
	if got["type"] != "error" {
		t.Errorf("type = %v, want error", got["type"])
	}
	inner, _ := got["error"].(map[string]any)
	if inner["type"] != "authentication_error" {
		t.Errorf("error.type = %v, want authentication_error", inner["type"])
	}
	if inner["message"] != "bad key" {
		t.Errorf("error.message = %v", inner["message"])
	}
}

// TestEncodeError_TypeMapping verifies apperr-style codes map to Anthropic's
// error type vocabulary.
func TestEncodeError_TypeMapping(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"authentication_error", "authentication_error"},
		{"invalid_api_key", "authentication_error"},
		{"rate_limit_error", "rate_limit_error"},
		{"insufficient_quota", "invalid_request_error"},
		{"invalid_request_error", "invalid_request_error"},
		{"invalid_request_body", "invalid_request_error"},
		{"model_not_permitted", "invalid_request_error"},
		{"upstream_error", "api_error"},
		{"timeout_error", "api_error"},
		{"api_error", "api_error"},
		{"unknown_code", "api_error"}, // default fallback
	}
	for _, c := range cases {
		b := new(codec).EncodeError(400, c.in, "msg")
		var got map[string]any
		_ = json.Unmarshal(b, &got)
		inner, _ := got["error"].(map[string]any)
		if inner["type"] != c.want {
			t.Errorf("errType=%q → %v, want %q", c.in, inner["type"], c.want)
		}
	}
}

// ---- StreamTerminator / ContentType / Protocol --------------------------

func TestCodec_Protocol(t *testing.T) {
	if got := new(codec).Protocol(); got != "anthropic" {
		t.Errorf("Protocol() = %q, want anthropic", got)
	}
}

func TestCodec_StreamContentType(t *testing.T) {
	if got := new(codec).StreamContentType(); got != "text/event-stream" {
		t.Errorf("StreamContentType() = %q", got)
	}
}

// TestCodec_StreamTerminator verifies the terminator contains a message_stop
// event (the Anthropic stream terminator, not OpenAI's [DONE]).
func TestCodec_StreamTerminator(t *testing.T) {
	tm := new(codec).StreamTerminator()
	s := string(tm)
	if !strings.Contains(s, "event: message_stop") {
		t.Errorf("terminator missing 'event: message_stop': %q", s)
	}
}

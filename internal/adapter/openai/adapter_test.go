package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/adapter/openai"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return b
}

func newAdapter(t *testing.T) adapter.Adapter {
	t.Helper()
	a, err := openai.New(openai.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "sk-test",
	})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	return a
}

func TestName(t *testing.T) {
	if got := newAdapter(t).Name(); got != "openai" {
		t.Errorf("Name() = %q, want openai", got)
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	// The package's init() must register the "openai" adapter so providers with
	// adapter:"openai" (openai/tencent/zhipu/compatible) resolve. (ADR-0001)
	a, err := adapter.New("openai", openai.Options{BaseURL: "https://x", APIKey: "k"})
	if err != nil {
		t.Fatalf("registry New(openai): %v", err)
	}
	if a.Name() != "openai" {
		t.Errorf("registry adapter Name = %q, want openai", a.Name())
	}
}

func TestBuildRequest(t *testing.T) {
	a := newAdapter(t)
	temp := 0.7
	ur, err := a.BuildRequest(context.Background(), &adapter.UnifiedRequest{
		Model:       "gpt-4o", // already the upstream model (ADR-0002)
		Messages:    []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
		Temperature: &temp,
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if ur.Method != "POST" {
		t.Errorf("Method = %q, want POST", ur.Method)
	}
	if ur.URL != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("URL = %q, want .../chat/completions", ur.URL)
	}
	if got := ur.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", got)
	}
	if got := ur.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	body := string(ur.Body)
	if !strings.Contains(body, `"model":"gpt-4o"`) {
		t.Errorf("body missing model: %s", body)
	}
	if !strings.Contains(body, `"hi"`) {
		t.Errorf("body missing message content: %s", body)
	}
}

func TestBuildRequest_StreamSetsIncludeUsage(t *testing.T) {
	// Per the Chunk usage contract, streaming requests must ask OpenAI to emit
	// usage on the final chunk via stream_options.include_usage.
	a := newAdapter(t)
	ur, err := a.BuildRequest(context.Background(), &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	body := string(ur.Body)
	if !strings.Contains(body, `"stream":true`) {
		t.Errorf("body missing stream:true: %s", body)
	}
	if !strings.Contains(body, `"include_usage":true`) {
		t.Errorf("streaming body must set stream_options.include_usage: %s", body)
	}
}

func TestParseResponse(t *testing.T) {
	a := newAdapter(t)
	resp, err := a.ParseResponse(readTestdata(t, "chat_response.json"))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.ID != "chatcmpl-abc123" {
		t.Errorf("ID = %q", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content.Text() != "Hello there!" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q", resp.Choices[0].FinishReason)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 21 {
		t.Errorf("usage = %+v, want total 21", resp.Usage)
	}
}

func TestParseResponse_InvalidJSON(t *testing.T) {
	a := newAdapter(t)
	if _, err := a.ParseResponse([]byte("not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestExtractUsage(t *testing.T) {
	a := newAdapter(t)
	u, err := a.ExtractUsage(&adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 9, CompletionTokens: 12, TotalTokens: 21}})
	if err != nil {
		t.Fatalf("ExtractUsage: %v", err)
	}
	if u.TotalTokens != 21 {
		t.Errorf("total = %d, want 21", u.TotalTokens)
	}
	if _, err := a.ExtractUsage(&adapter.UnifiedResponse{}); err == nil {
		t.Error("expected error when usage absent")
	}
}

func TestParseStream(t *testing.T) {
	a := newAdapter(t)
	sr, err := a.ParseStream(strings.NewReader(string(readTestdata(t, "chat_stream.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var content strings.Builder
	var lastUsage *adapter.Usage
	var finish string
	n := 0
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		n++
		content.WriteString(c.DeltaContent)
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
		if c.Usage != nil {
			lastUsage = c.Usage
		}
	}

	if content.String() != "Hello" {
		t.Errorf("assembled content = %q, want Hello", content.String())
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q, want stop", finish)
	}
	if lastUsage == nil || lastUsage.TotalTokens != 11 {
		t.Errorf("trailing usage = %+v, want total 11", lastUsage)
	}
}

func TestParseStream_PartialFramesSpanningReads(t *testing.T) {
	// The SSE decode must survive byte-by-byte delivery (half/joined packets).
	a := newAdapter(t)
	raw := string(readTestdata(t, "chat_stream.txt"))
	sr, err := a.ParseStream(iotest.OneByteReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var content strings.Builder
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		content.WriteString(c.DeltaContent)
	}
	if content.String() != "Hello" {
		t.Errorf("assembled content = %q, want Hello", content.String())
	}
}

// --- Tool call tests ---

// TestBuildRequest_ToolMessage verifies that a request containing a tool
// message (role=tool, tool_call_id=...) serializes tool_call_id into the
// upstream request body.
func TestBuildRequest_ToolMessage(t *testing.T) {
	a := newAdapter(t)

	req := &adapter.UnifiedRequest{
		Model: "gpt-4o",
		Messages: []adapter.Message{
			{Role: adapter.RoleUser, Content: adapter.NewContentText("What is the weather?")},
			{Role: adapter.RoleAssistant, Content: adapter.Content{}, ToolCalls: []adapter.ToolCall{
				{ID: "call_abc", Type: "function", Function: adapter.FunctionCall{Name: "get_weather", Arguments: `{"city":"Beijing"}`}},
			}},
			{Role: adapter.RoleTool, Content: adapter.NewContentText(`{"temp":25}`), ToolCallID: "call_abc"},
		},
	}

	ur, err := a.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(ur.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	var msgs []map[string]any
	if err := json.Unmarshal(body["messages"], &msgs); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}

	// Assistant message must contain tool_calls.
	toolCalls, ok := msgs[1]["tool_calls"].([]any)
	if !ok {
		t.Fatalf("msg[1].tool_calls missing or not an array; body=%s", ur.Body)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("msg[1] tool_calls count = %d, want 1", len(toolCalls))
	}
	tc := toolCalls[0].(map[string]any)
	if tc["id"] != "call_abc" {
		t.Errorf("tool_call id = %v, want call_abc", tc["id"])
	}

	// Tool message must contain tool_call_id.
	if msgs[2]["tool_call_id"] != "call_abc" {
		t.Errorf("msg[2].tool_call_id = %v, want call_abc", msgs[2]["tool_call_id"])
	}
	if msgs[2]["role"] != "tool" {
		t.Errorf("msg[2].role = %v, want tool", msgs[2]["role"])
	}
}

// TestParseResponse_WithToolCalls verifies that a response containing
// tool_calls in the message is correctly parsed.
func TestParseResponse_WithToolCalls(t *testing.T) {
	a := newAdapter(t)

	body := `{
		"id": "chatcmpl-tool",
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_x",
					"type": "function",
					"function": {
						"name": "get_weather",
						"arguments": "{\"city\":\"Beijing\"}"
					}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
	}`

	resp, err := a.ParseResponse([]byte(body))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}

	if resp.ID != "chatcmpl-tool" {
		t.Errorf("ID = %q, want chatcmpl-tool", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("len(Choices) = %d, want 1", len(resp.Choices))
	}

	msg := resp.Choices[0].Message
	if msg.Role != adapter.RoleAssistant {
		t.Errorf("Role = %q, want assistant", msg.Role)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "call_x" {
		t.Errorf("ToolCalls[0].ID = %q, want call_x", msg.ToolCalls[0].ID)
	}
	if msg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("ToolCalls[0].Function.Name = %q, want get_weather", msg.ToolCalls[0].Function.Name)
	}
	if msg.ToolCalls[0].Function.Arguments != `{"city":"Beijing"}` {
		t.Errorf("ToolCalls[0].Function.Arguments = %q", msg.ToolCalls[0].Function.Arguments)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("Usage.TotalTokens = %d, want 30", resp.Usage.TotalTokens)
	}
}

// TestBuildRequest_ForwardsTools verifies that the request-level tools and
// tool_choice fields are forwarded into the upstream request body. Without this,
// an OpenAI-compatible upstream never sees tool definitions and the model falls
// back to emitting tool calls as raw text in content.
func TestBuildRequest_ForwardsTools(t *testing.T) {
	a := newAdapter(t)

	tools := []adapter.Tool{
		{
			Type: "function",
			Function: adapter.FunctionDef{
				Name:        "get_weather",
				Description: "Get the weather for a city",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
			},
		},
	}

	ur, err := a.BuildRequest(context.Background(), &adapter.UnifiedRequest{
		Model:      "gpt-4o",
		Messages:   []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("weather?")}},
		Tools:      tools,
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(ur.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	// tools array must be present and carry the function name + schema.
	toolsRaw, ok := body["tools"]
	if !ok {
		t.Fatalf("tools field missing from upstream body: %s", ur.Body)
	}
	var gotTools []adapter.Tool
	if err := json.Unmarshal(toolsRaw, &gotTools); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if len(gotTools) != 1 || gotTools[0].Function.Name != "get_weather" {
		t.Fatalf("tools not forwarded correctly: %+v", gotTools)
	}
	if len(gotTools[0].Function.Parameters) == 0 {
		t.Fatalf("parameters schema dropped: %+v", gotTools[0])
	}

	// tool_choice must round-trip as "auto".
	var gotChoice any
	if err := json.Unmarshal(body["tool_choice"], &gotChoice); err != nil {
		t.Fatalf("unmarshal tool_choice: %v", err)
	}
	if gotChoice != "auto" {
		t.Errorf("tool_choice = %v, want auto", gotChoice)
	}
}

// TestBuildRequest_ForwardsTools_ObjectChoice verifies the object form of
// tool_choice ({"type":"function","function":{"name":"..."}}) is forwarded
// correctly. This is the shape used to force a specific tool.
func TestBuildRequest_ForwardsTools_ObjectChoice(t *testing.T) {
	a := newAdapter(t)

	toolChoice := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "get_weather",
		},
	}

	ur, err := a.BuildRequest(context.Background(), &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("weather?")}},
		Tools: []adapter.Tool{{
			Type:     "function",
			Function: adapter.FunctionDef{Name: "get_weather"},
		}},
		ToolChoice: toolChoice,
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(ur.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	var gotChoice any
	if err := json.Unmarshal(body["tool_choice"], &gotChoice); err != nil {
		t.Fatalf("unmarshal tool_choice: %v", err)
	}
	m, ok := gotChoice.(map[string]any)
	if !ok {
		t.Fatalf("tool_choice should be an object, got %T: %v", gotChoice, gotChoice)
	}
	if m["type"] != "function" {
		t.Errorf("tool_choice.type = %v, want function", m["type"])
	}
	fn, ok := m["function"].(map[string]any)
	if !ok || fn["name"] != "get_weather" {
		t.Errorf("tool_choice.function.name = %v, want get_weather", fn["name"])
	}
}

// TestParseStream_ToolCalls verifies that streamed tool_call deltas are parsed
// and that fragments with the same Index can be reassembled (the OpenAI
// streaming tool-call convention: the first chunk carries id/type/name, later
// chunks carry only the next arguments fragment).
func TestParseStream_ToolCalls(t *testing.T) {
	a := newAdapter(t)
	sr, err := a.ParseStream(strings.NewReader(string(readTestdata(t, "chat_stream_tools.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	type assembledToolCall struct {
		id, name, arguments string
	}
	byIndex := map[int]*assembledToolCall{}
	var finish string
	var lastUsage *adapter.Usage

	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
		if c.Usage != nil {
			lastUsage = c.Usage
		}
		for _, tc := range c.DeltaToolCalls {
			ac := byIndex[tc.Index]
			if ac == nil {
				ac = &assembledToolCall{}
				byIndex[tc.Index] = ac
			}
			if tc.ID != "" {
				ac.id = tc.ID
			}
			if tc.Function.Name != "" {
				ac.name = tc.Function.Name
			}
			ac.arguments += tc.Function.Arguments
		}
	}

	if finish != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", finish)
	}
	if lastUsage == nil || lastUsage.TotalTokens != 70 {
		t.Errorf("trailing usage = %+v, want total 70", lastUsage)
	}
	if len(byIndex) != 1 {
		t.Fatalf("got %d tool calls, want 1: %+v", len(byIndex), byIndex)
	}
	tc := byIndex[0]
	if tc.id != "call_001" {
		t.Errorf("tool call id = %q, want call_001", tc.id)
	}
	if tc.name != "get_weather" {
		t.Errorf("tool call name = %q, want get_weather", tc.name)
	}
	wantArgs := `{"city":"Beijing"}`
	if tc.arguments != wantArgs {
		t.Errorf("reassembled arguments = %q, want %q", tc.arguments, wantArgs)
	}
}

// TestBuildRequest_PassthroughExtra verifies that Extra fields from the
// unified request are merged into the upstream body. Without this,
// client-supplied parameters like response_format, n, stop, seed, etc.
// are silently discarded.
func TestBuildRequest_PassthroughExtra(t *testing.T) {
	a := newAdapter(t)

	req := &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
		Extra: map[string]json.RawMessage{
			"response_format": json.RawMessage(`{"type":"json_object"}`),
			"n":               json.RawMessage(`3`),
			"seed":            json.RawMessage(`42`),
		},
	}

	ur, err := a.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(ur.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	// Extra fields must appear in upstream body.
	if _, ok := body["response_format"]; !ok {
		t.Error("response_format missing from upstream body")
	}
	if _, ok := body["n"]; !ok {
		t.Error("n missing from upstream body")
	}
	if _, ok := body["seed"]; !ok {
		t.Error("seed missing from upstream body")
	}

	// Known fields must NOT be overwritten by Extra.
	var model string
	json.Unmarshal(body["model"], &model)
	if model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o (Extra must not overwrite)", model)
	}
}

// TestBuildRequest_PassthroughExtra_EmptyExtra verifies that when Extra
// is nil/empty, the upstream body is unchanged.
func TestBuildRequest_PassthroughExtra_EmptyExtra(t *testing.T) {
	a := newAdapter(t)

	req := &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
	}

	ur, err := a.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(ur.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	// No extra fields should appear.
	if _, ok := body["response_format"]; ok {
		t.Error("response_format should not appear when Extra is empty")
	}
}

// TestParseResponse_PreservesRaw verifies that ParseResponse stores the
// original upstream body in Raw so it can be written back to the client
// without a re-encode round-trip. Extra fields (system_fingerprint,
// logprobs, etc.) that don't have struct members are preserved.
func TestParseResponse_PreservesRaw(t *testing.T) {
	a := newAdapter(t)
	raw := readTestdata(t, "chat_response_extra_fields.json")
	resp, err := a.ParseResponse(raw)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.Raw == nil {
		t.Fatal("Raw must not be nil after ParseResponse")
	}
	// The parsed content must be correct.
	if resp.Choices[0].Message.Content.Text() != "Hello!" {
		t.Errorf("content = %q, want Hello!", resp.Choices[0].Message.Content.Text())
	}
	// system_fingerprint must be present in Raw even though it has no struct member.
	var rawJSON map[string]any
	if err := json.Unmarshal(resp.Raw, &rawJSON); err != nil {
		t.Fatalf("unmarshal Raw: %v", err)
	}
	if fp, ok := rawJSON["system_fingerprint"]; !ok || fp != "fp_abc123" {
		t.Errorf("system_fingerprint in Raw = %v, want fp_abc123", rawJSON["system_fingerprint"])
	}
}

// TestParseResponse_PromptCache verifies that the nested
// usage.prompt_tokens_details.cached_tokens field is decoded and folded into
// Usage.CachedPromptTokens. OpenAI semantics: prompt_tokens already includes
// the cached portion (non-cached = prompt - cached).
func TestParseResponse_PromptCache(t *testing.T) {
	a := newAdapter(t)
	resp, err := a.ParseResponse(readTestdata(t, "chat_response_cached.json"))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", resp.Usage.PromptTokens)
	}
	if resp.Usage.CachedPromptTokens != 80 {
		t.Errorf("CachedPromptTokens = %d, want 80", resp.Usage.CachedPromptTokens)
	}
	// Raw must still carry the original body (cache parsing must not disturb it).
	var rawJSON map[string]any
	if err := json.Unmarshal(resp.Raw, &rawJSON); err != nil {
		t.Fatalf("unmarshal Raw: %v", err)
	}
	_, ok := rawJSON["usage"].(map[string]any)["prompt_tokens_details"]
	if !ok {
		t.Error("prompt_tokens_details missing from Raw (should be preserved)")
	}
}

// TestParseStream_PromptCache verifies streamed cached_tokens decoding: the
// trailing usage chunk's prompt_tokens_details.cached_tokens reaches the final
// Chunk.Usage.CachedPromptTokens.
func TestParseStream_PromptCache(t *testing.T) {
	a := newAdapter(t)
	sr, err := a.ParseStream(strings.NewReader(string(readTestdata(t, "chat_stream_cached.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var lastUsage *adapter.Usage
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if c.Usage != nil {
			lastUsage = c.Usage
		}
	}
	if lastUsage == nil {
		t.Fatal("no usage chunk observed")
	}
	if lastUsage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", lastUsage.PromptTokens)
	}
	if lastUsage.CachedPromptTokens != 80 {
		t.Errorf("CachedPromptTokens = %d, want 80", lastUsage.CachedPromptTokens)
	}
}

// TestParseStream_ParallelToolCalls verifies that parallel tool calls with
// interleaved argument fragments are correctly parsed and can be reassembled
// by Index. This is the streaming equivalent of multiple parallel tool_use
// blocks — critical for multi-tool scenarios.
func TestParseStream_ParallelToolCalls(t *testing.T) {
	a := newAdapter(t)
	sr, err := a.ParseStream(strings.NewReader(string(readTestdata(t, "chat_stream_parallel_tools.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	type assembled struct {
		id, name, arguments string
	}
	byIndex := map[int]*assembled{}
	var finished bool
	var hasRaw bool

	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if c.Raw != nil {
			hasRaw = true
		}
		if c.FinishReason == "tool_calls" {
			finished = true
		}
		for _, tc := range c.DeltaToolCalls {
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

	if !hasRaw {
		t.Error("expected Chunk.Raw to be populated for every chunk")
	}
	if !finished {
		t.Error("expected finish_reason=tool_calls")
	}
	if len(byIndex) != 2 {
		t.Fatalf("got %d tool calls, want 2: %+v", len(byIndex), byIndex)
	}

	tc0 := byIndex[0]
	if tc0.name != "get_weather" {
		t.Errorf("tool[0].name = %q, want get_weather", tc0.name)
	}
	if tc0.arguments != `{"city":"BJ"}` {
		t.Errorf("tool[0].args = %q, want {\"city\":\"BJ\"}", tc0.arguments)
	}

	tc1 := byIndex[1]
	if tc1.name != "get_time" {
		t.Errorf("tool[1].name = %q, want get_time", tc1.name)
	}
	if tc1.arguments != `{"tz":"CST"}` {
		t.Errorf("tool[1].args = %q, want {\"tz\":\"CST\"}", tc1.arguments)
	}
}

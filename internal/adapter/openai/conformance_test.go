package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"voxeltoad/internal/adapter"
	adapter_openai "voxeltoad/internal/adapter/openai"
)

// adapterBridge bridges to the internal adapter package.
type adapterBridge struct {
	a adapter.Adapter
}

func newBridge(t *testing.T) *adapterBridge {
	t.Helper()
	a, err := adapter_openai.New(adapter_openai.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "sk-test",
	})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	return &adapterBridge{a: a}
}

// TestConformance_BuildRequest_ParsesAsSDK verifies that the upstream body
// produced by BuildRequest can be unmarshalled into the official
// go-openai SDK's ChatCompletionRequest type. This acts as a drift
// detector: if OpenAI adds/renames fields, the SDK will eventually
// validate them while our hand-written wire struct may lag behind.
func TestConformance_BuildRequest_ParsesAsSDK(t *testing.T) {
	bridge := newBridge(t)

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

	// Request with tools, tool_choice, and Extra (passthrough).
	ur, err := bridge.a.BuildRequest(context.Background(), &adapter.UnifiedRequest{
		Model:      "gpt-4o",
		Messages:   []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("weather?")}},
		Tools:      tools,
		ToolChoice: "auto",
		Stream:     true,
		Extra: map[string]json.RawMessage{
			"response_format": json.RawMessage(`{"type":"json_object"}`),
			"seed":            json.RawMessage(`42`),
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	var sdkReq openai.ChatCompletionRequest
	if err := json.Unmarshal(ur.Body, &sdkReq); err != nil {
		t.Fatalf("upstream body failed to unmarshal into go-openai ChatCompletionRequest: %v\nBody: %s", err, ur.Body)
	}

	// Check key fields round-tripped correctly.
	if sdkReq.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", sdkReq.Model)
	}
	if len(sdkReq.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(sdkReq.Messages))
	}
	if sdkReq.Messages[0].Role != "user" {
		t.Errorf("Role = %q, want user", sdkReq.Messages[0].Role)
	}
	if sdkReq.Messages[0].Content != "weather?" {
		t.Errorf("Content = %q, want weather?", sdkReq.Messages[0].Content)
	}

	// Tools forwarded correctly.
	if len(sdkReq.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(sdkReq.Tools))
	}
	if sdkReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("Tool name = %q, want get_weather", sdkReq.Tools[0].Function.Name)
	}

	// Stream + stream_options.
	if !sdkReq.Stream {
		t.Error("Stream should be true")
	}

	// Passthrough Extra fields parsed by SDK.
	if sdkReq.ResponseFormat.Type != "json_object" {
		t.Errorf("ResponseFormat.Type = %q, want json_object", sdkReq.ResponseFormat.Type)
	}
	if sdkReq.Seed == nil || *sdkReq.Seed != 42 {
		t.Errorf("Seed = %v, want 42", sdkReq.Seed)
	}
}

// TestConformance_ParseResponse_ParsesAsSDK verifies that a non-streaming
// response (with tool_calls, extra fields) can be unmarshalled into the
// SDK's ChatCompletionResponse.
func TestConformance_ParseResponse_ParsesAsSDK(t *testing.T) {
	bridge := newBridge(t)

	body := `{
		"id": "chatcmpl-conform",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4o",
		"system_fingerprint": "fp_test123",
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
			"logprobs": null,
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
	}`

	// Parse through our adapter.
	resp, err := bridge.a.ParseResponse([]byte(body))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}

	// The parsed response should be semantically correct.
	if len(resp.Choices) != 1 {
		t.Fatalf("len(Choices) = %d, want 1", len(resp.Choices))
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.Choices[0].Message.ToolCalls))
	}

	// The Raw body must unmarshal into the SDK type.
	var sdkResp openai.ChatCompletionResponse
	if err := json.Unmarshal(resp.Raw, &sdkResp); err != nil {
		t.Fatalf("Raw body failed to unmarshal into go-openai ChatCompletionResponse: %v\nRaw: %s", err, resp.Raw)
	}
	if sdkResp.ID != "chatcmpl-conform" {
		t.Errorf("ID = %q, want chatcmpl-conform", sdkResp.ID)
	}
	if sdkResp.SystemFingerprint != "fp_test123" {
		t.Errorf("SystemFingerprint = %q, want fp_test123", sdkResp.SystemFingerprint)
	}
	if len(sdkResp.Choices[0].Message.ToolCalls) != 1 {
		t.Errorf("SDK tool_calls len = %d, want 1", len(sdkResp.Choices[0].Message.ToolCalls))
	}
}

// TestConformance_StreamChunk_ParsesAsSDK verifies that streamed chunks
// (our golden testdata) can be unmarshalled into the SDK's
// ChatCompletionStreamResponse.
func TestConformance_StreamChunk_ParsesAsSDK(t *testing.T) {
	bridge := newBridge(t)

	sr, err := bridge.a.ParseStream(strings.NewReader(string(readTestdata(t, "chat_stream_tools.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		// Each chunk's Raw must unmarshal as a valid SDK stream response.
		if c.Raw == nil {
			t.Error("expected Chunk.Raw to be populated")
			continue
		}
		var sdkChunk openai.ChatCompletionStreamResponse
		if err := json.Unmarshal(c.Raw, &sdkChunk); err != nil {
			t.Fatalf("stream chunk failed to unmarshal into go-openai ChatCompletionStreamResponse: %v\nRaw: %s", err, c.Raw)
		}
		// Basic field sanity.
		if sdkChunk.ID == "" {
			t.Error("SDK stream response ID is empty")
		}
	}
}

// TestConformance_ParallelTools_ParsesAsSDK verifies that parallel tool
// call stream chunks (interleaved arguments) remain structurally valid
// per the SDK.
func TestConformance_ParallelTools_ParsesAsSDK(t *testing.T) {
	bridge := newBridge(t)

	sr, err := bridge.a.ParseStream(strings.NewReader(string(readTestdata(t, "chat_stream_parallel_tools.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	toolCallChunks := 0
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if c.Raw == nil {
			continue
		}
		var sdkChunk openai.ChatCompletionStreamResponse
		if err := json.Unmarshal(c.Raw, &sdkChunk); err != nil {
			t.Fatalf("parallel tools chunk failed SDK unmarshal: %v\nRaw: %s", err, c.Raw)
		}
		if len(sdkChunk.Choices) > 0 && len(sdkChunk.Choices[0].Delta.ToolCalls) > 0 {
			toolCallChunks++
		}
	}
	if toolCallChunks == 0 {
		t.Error("expected at least one chunk with tool_calls delta in parallel tools stream")
	}
}

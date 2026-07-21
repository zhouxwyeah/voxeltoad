package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
)

// These tests verify the OpenAI chat.completion.chunk wire shape produced by
// toWireChunk. They were migrated from internal/proxy/stream_test.go when the
// wire types moved to the ingress codec (the proxy layer no longer owns an
// inbound wire shape).

// TestToWireChunk_EmptyChunkEnsuresChoicesArray ensures that a usage-only chunk
// (no delta, no finish reason) emits "choices":[] — not "choices":null. This
// matters because Go's json.Unmarshal silently accepts both for slices, so
// tests that round-trip through Unmarshal won't catch the difference. We
// validate the raw JSON string instead.
func TestToWireChunk_EmptyChunkEnsuresChoicesArray(t *testing.T) {
	c := adapter.Chunk{
		ID:    "chunk-1",
		Model: "test-model",
		Usage: &adapter.Usage{TotalTokens: 42},
	}
	raw := toJSON(t, toWireChunk(c))

	if strings.Contains(raw, `"choices":null`) {
		t.Fatalf("empty chunk produced null choices, want empty array: %s", raw)
	}
	if !strings.Contains(raw, `"choices":[]`) {
		t.Fatalf("empty chunk did not produce empty choices array: %s", raw)
	}
	if !strings.Contains(raw, `"total_tokens":42`) {
		t.Fatalf("usage not present in empty chunk: %s", raw)
	}
}

func TestToWireChunk_DeltaContent(t *testing.T) {
	c := adapter.Chunk{
		ID:           "chunk-2",
		Model:        "test-model",
		DeltaContent: "hello",
	}
	raw := toJSON(t, toWireChunk(c))

	if !strings.Contains(raw, `"content":"hello"`) {
		t.Fatalf("delta content missing: %s", raw)
	}
	if !strings.Contains(raw, `"index":0`) {
		t.Fatalf("choice index missing: %s", raw)
	}
}

func TestToWireChunk_FinishReason(t *testing.T) {
	c := adapter.Chunk{
		ID:           "chunk-3",
		Model:        "test-model",
		FinishReason: "stop",
	}
	raw := toJSON(t, toWireChunk(c))

	if !strings.Contains(raw, `"finish_reason":"stop"`) {
		t.Fatalf("finish_reason missing or wrong: %s", raw)
	}
}

func TestToWireChunk_DeltaRole(t *testing.T) {
	c := adapter.Chunk{
		ID:        "chunk-4",
		Model:     "test-model",
		DeltaRole: adapter.RoleAssistant,
	}
	raw := toJSON(t, toWireChunk(c))

	if !strings.Contains(raw, `"role":"assistant"`) {
		t.Fatalf("delta role missing or wrong: %s", raw)
	}
}

func TestToWireChunk_FullChunk(t *testing.T) {
	c := adapter.Chunk{
		ID:           "chunk-5",
		Model:        "test-model",
		DeltaRole:    adapter.RoleAssistant,
		DeltaContent: "world",
		FinishReason: "stop",
		Usage:        &adapter.Usage{TotalTokens: 99},
	}
	raw := toJSON(t, toWireChunk(c))

	if !strings.Contains(raw, `"role":"assistant"`) {
		t.Fatalf("delta role missing: %s", raw)
	}
	if !strings.Contains(raw, `"content":"world"`) {
		t.Fatalf("delta content missing: %s", raw)
	}
	if !strings.Contains(raw, `"finish_reason":"stop"`) {
		t.Fatalf("finish_reason missing: %s", raw)
	}
	if !strings.Contains(raw, `"total_tokens":99`) {
		t.Fatalf("usage missing: %s", raw)
	}
}

func TestToWireChunk_ObjectIsAlwaysChatCompletionChunk(t *testing.T) {
	c := adapter.Chunk{ID: "chunk-6", Model: "m"}
	raw := toJSON(t, toWireChunk(c))

	if !strings.Contains(raw, `"object":"chat.completion.chunk"`) {
		t.Fatalf("object field wrong: %s", raw)
	}
}

// TestToWireChunk_ToolCalls verifies that streamed tool-call deltas are emitted
// into the downstream delta.tool_calls array, including the Index that lets
// clients reassemble fragments. It also confirms a tool-call-only chunk still
// produces a choice entry (it must not be treated as a usage-only chunk).
func TestToWireChunk_ToolCalls(t *testing.T) {
	c := adapter.Chunk{
		ID:    "chunk-tc",
		Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{
			{Index: 0, ID: "call_1", Type: "function", Function: adapter.FunctionCallDelta{Name: "get_weather"}},
			{Index: 1, ID: "call_2", Type: "function", Function: adapter.FunctionCallDelta{Arguments: `{"a":1}`}},
		},
	}
	raw := toJSON(t, toWireChunk(c))

	if !strings.Contains(raw, `"choices":[]`) && !strings.Contains(raw, `"choices":[{`) {
		t.Fatalf("tool-call chunk produced no choice entry: %s", raw)
	}
	if !strings.Contains(raw, `"tool_calls":[`) {
		t.Fatalf("delta.tool_calls missing: %s", raw)
	}
	// Index must be present for both entries.
	if !strings.Contains(raw, `"index":0`) {
		t.Fatalf("index 0 missing: %s", raw)
	}
	if !strings.Contains(raw, `"index":1`) {
		t.Fatalf("index 1 missing: %s", raw)
	}
	if !strings.Contains(raw, `"name":"get_weather"`) {
		t.Fatalf("function name missing: %s", raw)
	}
	if !strings.Contains(raw, `"id":"call_1"`) {
		t.Fatalf("first tool call id missing: %s", raw)
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

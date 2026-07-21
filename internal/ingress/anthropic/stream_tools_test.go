package anthropic

import (
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
)

// TestStream_SingleToolCall verifies the streaming tool-use path: OpenAI emits
// a chunk carrying delta.tool_calls[0] with id+name, then subsequent chunks
// with fragments of function.arguments (Index-grouped). The Anthropic side
// must produce:
//
//	content_block_start (tool_use) at index N
//	input_json_delta (one per arguments fragment)
//	content_block_stop
//	message_delta with stop_reason=tool_use
//	message_stop (terminator)
func TestStream_SingleToolCall(t *testing.T) {
	enc := new(codec).NewStreamEncoder()

	// Chunk 1: role.
	b1, _ := enc.EncodeChunk(adapter.Chunk{ID: "c1", Model: "gpt-4o", DeltaRole: adapter.RoleAssistant})

	// Chunk 2: tool_call opens — Index 0, id+name+empty args.
	b2, _ := enc.EncodeChunk(adapter.Chunk{
		ID: "c1", Model: "gpt-4o",
		DeltaToolCalls: []adapter.ToolCallDelta{{
			Index: 0, ID: "call_1", Type: "function",
			Function: adapter.FunctionCallDelta{Name: "get_weather"},
		}},
	})

	// Chunk 3: arguments fragment.
	b3, _ := enc.EncodeChunk(adapter.Chunk{
		ID: "c1", Model: "gpt-4o",
		DeltaToolCalls: []adapter.ToolCallDelta{{
			Index:    0,
			Function: adapter.FunctionCallDelta{Arguments: `{"city"`},
		}},
	})

	// Chunk 4: arguments fragment.
	b4, _ := enc.EncodeChunk(adapter.Chunk{
		ID: "c1", Model: "gpt-4o",
		DeltaToolCalls: []adapter.ToolCallDelta{{
			Index:    0,
			Function: adapter.FunctionCallDelta{Arguments: `:"Paris"}`},
		}},
	})

	// Chunk 5: finish.
	b5, _ := enc.EncodeChunk(adapter.Chunk{
		ID: "c1", Model: "gpt-4o", FinishReason: "tool_calls",
		Usage: &adapter.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	})

	closer := enc.(interface{ Close() ([]byte, error) })
	bclose, _ := closer.Close()
	bterm := new(codec).StreamTerminator()

	all := concat(b1, b2, b3, b4, b5, bclose, bterm)
	events := parseSSE(t, all)

	want := []string{
		"message_start",
		"content_block_start", // tool_use block
		"content_block_delta", // input_json_delta fragment 1
		"content_block_delta", // input_json_delta fragment 2
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	if len(events) != len(want) {
		t.Fatalf("event count = %d (%+v), want %d (%+v)", len(events), eventNames(events), len(want), want)
	}
	for i, w := range want {
		if events[i].Event != w {
			t.Errorf("events[%d] = %q, want %q", i, events[i].Event, w)
		}
	}

	// content_block_start must be a tool_use with id+name and index (text block
	// was never opened because no DeltaContent).
	cbs := events[1]
	if !strings.Contains(cbs.Data, `"type":"tool_use"`) {
		t.Errorf("content_block_start not tool_use: %s", cbs.Data)
	}
	if !strings.Contains(cbs.Data, `"id":"call_1"`) || !strings.Contains(cbs.Data, `"name":"get_weather"`) {
		t.Errorf("content_block_start missing id/name: %s", cbs.Data)
	}

	// input_json_delta fragments.
	d1 := events[2]
	if !strings.Contains(d1.Data, `"type":"input_json_delta"`) {
		t.Errorf("delta1 not input_json_delta: %s", d1.Data)
	}
	if !strings.Contains(d1.Data, `"partial_json":"{\"city\""`) {
		t.Errorf("delta1 partial_json wrong: %s", d1.Data)
	}
	d2 := events[3]
	if !strings.Contains(d2.Data, `"partial_json":":\"Paris\"}"`) {
		t.Errorf("delta2 partial_json wrong: %s", d2.Data)
	}

	// message_delta stop_reason.
	md := events[5]
	if !strings.Contains(md.Data, `"stop_reason":"tool_use"`) {
		t.Errorf("message_delta stop_reason not tool_use: %s", md.Data)
	}
}

// TestStream_ParallelToolCalls verifies two tool calls streamed in parallel
// (interleaved Index 0 and Index 1 fragments) each get their own content_block
// lifecycle. OpenAI streams fragments by Index; the Anthropic encoder must
// maintain independent block state per Index.
func TestStream_ParallelToolCalls(t *testing.T) {
	enc := new(codec).NewStreamEncoder()
	bRole, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m", DeltaRole: adapter.RoleAssistant})

	// Open Index 0.
	bOpen0, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 0, ID: "call_a", Type: "function", Function: adapter.FunctionCallDelta{Name: "fnA"}}},
	})
	// Open Index 1.
	bOpen1, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 1, ID: "call_b", Type: "function", Function: adapter.FunctionCallDelta{Name: "fnB"}}},
	})
	// Interleaved fragments.
	bArgsA, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 0, Function: adapter.FunctionCallDelta{Arguments: `{"a"`}}},
	})
	bArgsB, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 1, Function: adapter.FunctionCallDelta{Arguments: `{"b"`}}},
	})
	bArgsA2, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 0, Function: adapter.FunctionCallDelta{Arguments: `:1}`}}},
	})
	bArgsB2, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 1, Function: adapter.FunctionCallDelta{Arguments: `:2}`}}},
	})

	// Finish — close both blocks.
	bFin, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m", FinishReason: "tool_calls", Usage: &adapter.Usage{TotalTokens: 1}})
	closer := enc.(interface{ Close() ([]byte, error) })
	bclose, _ := closer.Close()
	bterm := new(codec).StreamTerminator()

	all := concat(bRole, bOpen0, bOpen1, bArgsA, bArgsB, bArgsA2, bArgsB2, bFin, bclose, bterm)
	events := parseSSE(t, all)

	// Must see 2 content_block_start (one per tool_use) + 2 content_block_stop.
	var starts, stops int
	for _, ev := range events {
		switch ev.Event {
		case "content_block_start":
			starts++
		case "content_block_stop":
			stops++
		}
	}
	if starts != 2 {
		t.Errorf("content_block_start count = %d, want 2", starts)
	}
	if stops != 2 {
		t.Errorf("content_block_stop count = %d, want 2", stops)
	}
}

// TestStream_TextAndToolUseMixed verifies that a stream which opens a text
// block and then a tool_use block produces two content blocks, each opened and
// closed in order.
func TestStream_TextAndToolUseMixed(t *testing.T) {
	enc := new(codec).NewStreamEncoder()
	bRole, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m", DeltaRole: adapter.RoleAssistant})

	// Text content.
	bText, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m", DeltaContent: "calling tool"})

	// Tool call open.
	bOpen, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 0, ID: "x", Type: "function", Function: adapter.FunctionCallDelta{Name: "go"}}},
	})
	// Args fragment.
	bArgs, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m",
		DeltaToolCalls: []adapter.ToolCallDelta{{Index: 0, Function: adapter.FunctionCallDelta{Arguments: "{}"}}},
	})

	// Finish.
	bFin, _ := enc.EncodeChunk(adapter.Chunk{ID: "c", Model: "m", FinishReason: "tool_calls", Usage: &adapter.Usage{TotalTokens: 1}})
	closer := enc.(interface{ Close() ([]byte, error) })
	bclose, _ := closer.Close()
	bterm := new(codec).StreamTerminator()

	all := concat(bRole, bText, bOpen, bArgs, bFin, bclose, bterm)
	events := parseSSE(t, all)

	// Expect 2 content_block_stop events (one for text, one for tool_use).
	stops := 0
	for _, ev := range events {
		if ev.Event == "content_block_stop" {
			stops++
		}
	}
	if stops != 2 {
		t.Errorf("content_block_stop count = %d, want 2 (text + tool_use); events=%+v", stops, eventNames(events))
	}
}

// concat concatenates byte slices.
func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

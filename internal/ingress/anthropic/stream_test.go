package anthropic

import (
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/pkg/sse"
)

// parseSSE splits an SSE byte stream into a list of (event, data) pairs for
// assertion. Uses the same pkg/sse decoder the gateway uses elsewhere so the
// test validates real wire frames (event:, data:, blank-line terminator).
func parseSSE(t *testing.T, b []byte) []sse.Event {
	t.Helper()
	dec := sse.NewDecoder(strings.NewReader(string(b)))
	var out []sse.Event
	for {
		ev, err := dec.Next()
		if err != nil {
			break
		}
		out = append(out, ev)
	}
	return out
}

// findEvent returns the first event with the given event type, or fails.
func findEvent(t *testing.T, events []sse.Event, name string) sse.Event {
	t.Helper()
	for _, ev := range events {
		if ev.Event == name {
			return ev
		}
	}
	t.Fatalf("event %q not found in stream: %+v", name, eventNames(events))
	return sse.Event{}
}

func eventNames(events []sse.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Event
	}
	return out
}

// TestStream_PlainText verifies the canonical Anthropic SSE sequence for a
// pure-text streaming response: message_start → content_block_start (text) →
// content_block_delta (text_delta, possibly multiple) → content_block_stop →
// message_delta (with output_tokens in usage) → message_stop (terminator).
func TestStream_PlainText(t *testing.T) {
	enc := new(codec).NewStreamEncoder()

	// Chunk 1: role marker (first chunk from OpenAI carries delta.role).
	b1, err := enc.EncodeChunk(adapter.Chunk{
		ID:        "chatcmpl-1",
		Model:     "gpt-4o",
		DeltaRole: adapter.RoleAssistant,
	})
	if err != nil {
		t.Fatalf("EncodeChunk role: %v", err)
	}

	// Chunk 2: first content delta — opens the text block.
	b2, err := enc.EncodeChunk(adapter.Chunk{
		ID:           "chatcmpl-1",
		Model:        "gpt-4o",
		DeltaContent: "Hello",
	})
	if err != nil {
		t.Fatalf("EncodeChunk text1: %v", err)
	}

	// Chunk 3: another content delta — same block.
	b3, err := enc.EncodeChunk(adapter.Chunk{
		ID:           "chatcmpl-1",
		Model:        "gpt-4o",
		DeltaContent: ", world",
	})
	if err != nil {
		t.Fatalf("EncodeChunk text2: %v", err)
	}

	// Chunk 4: finish + usage (the trailing OpenAI chunk).
	b4, err := enc.EncodeChunk(adapter.Chunk{
		ID:           "chatcmpl-1",
		Model:        "gpt-4o",
		FinishReason: "stop",
		Usage:        &adapter.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})
	if err != nil {
		t.Fatalf("EncodeChunk finish: %v", err)
	}

	closer, ok := enc.(interface{ Close() ([]byte, error) })
	if !ok {
		t.Fatalf("encoder does not implement Close")
	}
	b5, err := closer.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The terminal message_stop comes from the codec's StreamTerminator
	// (mirrors the proxy layer's defer order).
	b6 := new(codec).StreamTerminator()

	all := append(append(append(append(b1, b2...), b3...), b4...), b5...)
	all = append(all, b6...)

	events := parseSSE(t, all)
	// Expected event order: message_start, content_block_start, content_block_delta,
	// content_block_delta, content_block_stop, message_delta, message_stop.
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if len(events) != len(want) {
		t.Fatalf("event count = %d (%+v), want %d (%+v)", len(events), eventNames(events), len(want), want)
	}
	for i, name := range want {
		if events[i].Event != name {
			t.Errorf("events[%d].Event = %q, want %q", i, events[i].Event, name)
		}
	}

	// message_start must carry input_tokens:0 (we don't have them yet — OpenAI
	// reports usage only on the trailing chunk; ADR-0045 §gap).
	ms := events[0]
	if !strings.Contains(ms.Data, `"input_tokens":0`) {
		t.Errorf("message_start missing input_tokens:0: %s", ms.Data)
	}

	// content_block_start at index 0 must be a text block.
	cbs := events[1]
	if !strings.Contains(cbs.Data, `"index":0`) {
		t.Errorf("content_block_start missing index 0: %s", cbs.Data)
	}
	if !strings.Contains(cbs.Data, `"type":"text"`) {
		t.Errorf("content_block_start not a text block: %s", cbs.Data)
	}

	// content_block_delta text must match the streamed content.
	if !strings.Contains(events[2].Data, `"text":"Hello"`) {
		t.Errorf("delta1 not Hello: %s", events[2].Data)
	}
	if !strings.Contains(events[3].Data, `"text":", world"`) {
		t.Errorf("delta2 not ', world': %s", events[3].Data)
	}

	// message_delta must carry output_tokens (5) and stop_reason end_turn.
	md := events[5]
	if !strings.Contains(md.Data, `"output_tokens":5`) {
		t.Errorf("message_delta missing output_tokens:5: %s", md.Data)
	}
	if !strings.Contains(md.Data, `"stop_reason":"end_turn"`) {
		t.Errorf("message_delta stop_reason not end_turn: %s", md.Data)
	}
}

// TestStream_FinishReasonMapping verifies the OpenAI finish_reason → Anthropic
// stop_reason mapping for the streaming path.
func TestStream_FinishReasonMapping(t *testing.T) {
	cases := []struct {
		finish string
		want   string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
	}
	for _, c := range cases {
		enc := new(codec).NewStreamEncoder()
		// Initial role chunk so the encoder enters "started" state.
		_, _ = enc.EncodeChunk(adapter.Chunk{ID: "x", Model: "m", DeltaRole: adapter.RoleAssistant})
		b, err := enc.EncodeChunk(adapter.Chunk{
			ID:           "x",
			Model:        "m",
			FinishReason: c.finish,
			Usage:        &adapter.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
		if err != nil {
			t.Fatalf("EncodeChunk(%q): %v", c.finish, err)
		}
		events := parseSSE(t, b)
		md := findEvent(t, events, "message_delta")
		if !strings.Contains(md.Data, `"stop_reason":"`+c.want+`"`) {
			t.Errorf("finish=%q → message_delta %s, want stop_reason %q", c.finish, md.Data, c.want)
		}
	}
}

// TestStream_NoContentJustFinish verifies the encoder handles a chunk sequence
// where finish arrives with no prior content (e.g. an empty upstream reply).
func TestStream_NoContentJustFinish(t *testing.T) {
	enc := new(codec).NewStreamEncoder()
	_, _ = enc.EncodeChunk(adapter.Chunk{ID: "x", Model: "m", DeltaRole: adapter.RoleAssistant})
	b, err := enc.EncodeChunk(adapter.Chunk{
		ID:           "x",
		Model:        "m",
		FinishReason: "stop",
		Usage:        &adapter.Usage{PromptTokens: 2, CompletionTokens: 0, TotalTokens: 2},
	})
	if err != nil {
		t.Fatalf("EncodeChunk: %v", err)
	}
	closer := enc.(interface{ Close() ([]byte, error) })
	bclose, _ := closer.Close()
	bterm := new(codec).StreamTerminator()
	all := append(b, bclose...)
	all = append(all, bterm...)
	events := parseSSE(t, all)
	// No content_block_stop — text block was never opened (no DeltaContent).
	// Must see message_delta + message_stop.
	if findEvent(t, events, "message_stop").Event != "message_stop" {
		t.Fatalf("no message_stop emitted")
	}
	if findEvent(t, events, "message_delta").Event != "message_delta" {
		t.Fatalf("no message_delta emitted")
	}
}

// TestStream_UsageOnlyFirstChunk verifies the encoder handles a stream whose
// FIRST chunk is usage-only (no role, no delta — e.g. an OpenAI upstream that
// sends a stream_options probe or a provider that reports usage early). The
// encoder must still emit message_start (it takes ID/Model from that chunk),
// and the finish path must produce message_delta + message_stop without any
// content_block_* events (no content ever streamed).
//
// This pins down the behavior that messageStarted is set on the first chunk
// regardless of whether it carries a role marker — a real-world edge case
// that would otherwise silently produce an empty stream.
func TestStream_UsageOnlyFirstChunk(t *testing.T) {
	enc := new(codec).NewStreamEncoder()

	// Chunk 1: usage only (no role, no delta).
	b1, err := enc.EncodeChunk(adapter.Chunk{
		ID:    "c1",
		Model: "m",
		Usage: &adapter.Usage{PromptTokens: 5, CompletionTokens: 0, TotalTokens: 5},
	})
	if err != nil {
		t.Fatalf("EncodeChunk usage-first: %v", err)
	}

	// Chunk 2: finish with usage.
	b2, err := enc.EncodeChunk(adapter.Chunk{
		ID:           "c1",
		Model:        "m",
		FinishReason: "stop",
		Usage:        &adapter.Usage{PromptTokens: 5, CompletionTokens: 0, TotalTokens: 5},
	})
	if err != nil {
		t.Fatalf("EncodeChunk finish: %v", err)
	}

	closer := enc.(interface{ Close() ([]byte, error) })
	bclose, _ := closer.Close()
	bterm := new(codec).StreamTerminator()

	all := append(append(append(b1, b2...), bclose...), bterm...)
	events := parseSSE(t, all)

	// Expected: message_start (from chunk 1), message_delta (from finish),
	// message_stop (terminator). NO content_block_* events.
	want := []string{"message_start", "message_delta", "message_stop"}
	if len(events) != len(want) {
		t.Fatalf("event count = %d (%+v), want %d (%+v)", len(events), eventNames(events), len(want), want)
	}
	for i, name := range want {
		if events[i].Event != name {
			t.Errorf("events[%d].Event = %q, want %q", i, events[i].Event, name)
		}
	}
}

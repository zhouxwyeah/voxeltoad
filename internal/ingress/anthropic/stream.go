package anthropic

import (
	"encoding/json"
	"fmt"
	"sort"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/ingress"
	"voxeltoad/pkg/sse"
)

func (c *codec) NewStreamEncoder() ingress.StreamEncoder {
	return &streamEncoder{}
}

// streamEncoder is the Anthropic SSE state machine: a sequence of unified
// Chunks is translated into Anthropic message_start / content_block_* /
// message_delta events. Anthropic stream framing is block-oriented — every
// content element (text run, tool_use) is wrapped in content_block_start →
// content_block_delta(s) → content_block_stop — whereas OpenAI chunks carry
// flat delta fields. The encoder therefore maintains a tiny bit of state:
//   - messageStarted: have we emitted message_start yet?
//   - textBlockIdx / textBlockOpen: is the text block currently open, and at
//     which Anthropic content-block index? Text is opened lazily on the first
//     DeltaContent and closed once at stream end.
//   - nextBlockIdx: monotonic allocator for Anthropic block indices (text
//     usually gets 0; tool_use blocks get 1, 2, …). Step 5 adds the tool map.
//
// Trailing usage: OpenAI reports usage only on the final chunk; Anthropic
// splits it across message_start (input_tokens) and message_delta
// (output_tokens). We emit input_tokens:0 in message_start and the full usage
// in message_delta. See ADR-0045 §gap for the cosmetic input_tokens caveat.
type streamEncoder struct {
	messageStarted bool
	messageID      string
	model          string

	textBlockOpen bool
	textBlockIdx  int

	// toolBlocks tracks the Anthropic content_block index for each OpenAI
	// tool-call Index. OpenAI streams tool-call fragments grouped by Index
	// (0, 1, 2, … for parallel calls); each Index maps to one Anthropic
	// content_block (type=tool_use) with its own start/delta/stop lifecycle.
	toolBlocks map[int]int

	nextBlockIdx int // next Anthropic block index to allocate

	outputTokens int // cached from trailing usage

	finished bool // emitFinish has run; Close should not emit another message_delta
}

// EncodeChunk translates one unified Chunk into 0 or more Anthropic SSE event
// bytes. Returns nil bytes for chunks that map to no Anthropic output (e.g.
// a usage-only chunk that is folded into message_delta on the next call).
//
// Passthrough (ADR-0047): when the hit provider's adapter is claude, Chunk.Raw
// carries a complete Anthropic SSE frame from the upstream. Relaying it
// verbatim preserves provider-specific fields and avoids a re-encode round-
// trip. Raw is only populated by the claude adapter; protocol-aware routing
// (ADR-0047) prefers claude-adapter providers for anthropic ingress, so Raw
// reaching here is always the right protocol.
func (e *streamEncoder) EncodeChunk(c adapter.Chunk) ([]byte, error) {
	// Passthrough (ADR-0047): relay the upstream's Anthropic frame verbatim,
	// but only when RawProtocol confirms it's Anthropic-shaped (failover to an
	// openai provider would set RawProtocol="openai" — we must re-encode those).
	if len(c.Raw) > 0 && c.RawProtocol == "anthropic" {
		// Still track state so the encoder's Close/terminator behave
		// consistently (messageStarted, finish, usage for billing).
		if !e.messageStarted {
			e.messageStarted = true
		}
		if c.FinishReason != "" && !e.finished {
			e.finished = true
		}
		if c.Usage != nil {
			e.outputTokens = c.Usage.CompletionTokens
		}
		out := make([]byte, len(c.Raw))
		copy(out, c.Raw)
		return out, nil
	}

	var out []byte

	// First chunk: emit message_start. OpenAI's first chunk usually carries
	// delta.role=assistant (and possibly usage on the trailing chunk only).
	if !e.messageStarted {
		e.messageID = c.ID
		e.model = c.Model
		e.messageStarted = true
		out = append(out, e.emitMessageStart()...)
	}

	// Open the text block on the first content delta or role marker. The
	// role-only chunk doesn't carry text but Anthropic expects a block open
	// before any delta. Open lazily so a usage-only chunk doesn't open one.
	if c.DeltaContent != "" {
		if !e.textBlockOpen {
			e.textBlockIdx = e.nextBlockIdx
			e.nextBlockIdx++
			e.textBlockOpen = true
			out = append(out, e.emitContentBlockStartText(e.textBlockIdx)...)
		}
		out = append(out, e.emitTextDelta(e.textBlockIdx, c.DeltaContent)...)
	}

	// Tool-call deltas: each new Index opens a tool_use block; known Indexes
	// emit input_json_delta fragments (from Arguments) or, when Arguments is
	// empty on a re-seen Index, nothing (the name was already in the start).
	for _, tc := range c.DeltaToolCalls {
		if e.toolBlocks == nil {
			e.toolBlocks = make(map[int]int)
		}
		blockIdx, seen := e.toolBlocks[tc.Index]
		if !seen {
			// First time we see this Index → open a tool_use block.
			blockIdx = e.nextBlockIdx
			e.nextBlockIdx++
			e.toolBlocks[tc.Index] = blockIdx
			out = append(out, e.emitContentBlockStartToolUse(blockIdx, tc.ID, tc.Function.Name)...)
			// If the opening delta also carries an Arguments fragment, emit it
			// as the first input_json_delta.
			if tc.Function.Arguments != "" {
				out = append(out, e.emitInputJSONDelta(blockIdx, tc.Function.Arguments)...)
			}
		} else {
			// Subsequent fragment for a known Index.
			if tc.Function.Arguments != "" {
				out = append(out, e.emitInputJSONDelta(blockIdx, tc.Function.Arguments)...)
			}
		}
	}

	// Cache trailing usage so Close can emit message_delta with the final
	// output_tokens (and the finish-chunk path can include it).
	if c.Usage != nil {
		e.outputTokens = c.Usage.CompletionTokens
	}

	// Finish chunk: close any open blocks and emit message_delta with the
	// stop_reason (and the cached output_tokens).
	if c.FinishReason != "" {
		out = append(out, e.emitFinish(c.FinishReason)...)
	}

	return out, nil
}

// Close emits the pre-terminator end-of-stream events: closes any open
// content block and emits message_delta with the final stop_reason and usage.
// The terminal message_stop event is emitted separately by the codec's
// StreamTerminator (the proxy's defer writes Close() output, then the
// terminator). This split mirrors how the OpenAI codec's Close returns nothing
// and its terminator emits [DONE].
//
// If the upstream ended without an explicit finish chunk (e.g. OpenAI's
// trailing usage-only chunk), a default end_turn stop_reason is emitted here so
// the Anthropic client sees a well-formed stream end.
func (e *streamEncoder) Close() ([]byte, error) {
	var out []byte
	if !e.finished {
		// Reuse emitFinish with a default end_turn reason; it also closes any
		// open text/tool blocks. We then skip the duplicate message_delta
		// below (finished is now true).
		out = append(out, e.emitFinish("stop")...)
		// emitFinish maps "stop" → "end_turn".
	}
	return out, nil
}

// ---- event emitters -----------------------------------------------------

func (e *streamEncoder) emitMessageStart() []byte {
	// input_tokens:0 because we don't know prompt_tokens yet (OpenAI gives
	// usage only on the trailing chunk). See ADR-0045 §gap.
	return encodeSSE("message_start", map[string]any{
		"message": map[string]any{
			"id":            e.messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         e.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})
}

func (e *streamEncoder) emitContentBlockStartText(idx int) []byte {
	return encodeSSE("content_block_start", map[string]any{
		"index": idx,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})
}

// emitContentBlockStartToolUse opens a tool_use block. id and name come from
// the OpenAI delta.tool_calls opening fragment (Index + ID + Name). The input
// JSON object is built incrementally via input_json_delta events, so the block
// starts with an empty object placeholder.
func (e *streamEncoder) emitContentBlockStartToolUse(idx int, id, name string) []byte {
	return encodeSSE("content_block_start", map[string]any{
		"index": idx,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]any{},
		},
	})
}

func (e *streamEncoder) emitTextDelta(idx int, text string) []byte {
	return encodeSSE("content_block_delta", map[string]any{
		"index": idx,
		"delta": map[string]any{
			"type": "text_delta",
			"text": text,
		},
	})
}

// emitInputJSONDelta carries one fragment of the tool-use input JSON. OpenAI's
// streamed function.arguments is an incremental JSON string; Anthropic's
// input_json_delta.partial_json is the same incremental JSON string, so the
// fragment is passed through verbatim. The Anthropic SDK concatenates the
// partial_json fragments to reconstruct the input object.
func (e *streamEncoder) emitInputJSONDelta(idx int, partialJSON string) []byte {
	return encodeSSE("content_block_delta", map[string]any{
		"index": idx,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	})
}

func (e *streamEncoder) emitContentBlockStop(idx int) []byte {
	return encodeSSE("content_block_stop", map[string]any{
		"index": idx,
	})
}

// emitFinish closes any open content blocks (text + tool_use, in index order)
// and emits message_delta with the stop_reason and output_tokens usage.
func (e *streamEncoder) emitFinish(finishReason string) []byte {
	if e.finished {
		return nil
	}
	e.finished = true
	var out []byte
	// Close text block first (its index is always lower than tool blocks').
	if e.textBlockOpen {
		out = append(out, e.emitContentBlockStop(e.textBlockIdx)...)
		e.textBlockOpen = false
	}
	// Close tool blocks in Anthropic-index order (deterministic).
	toolIdxs := make([]int, 0, len(e.toolBlocks))
	for _, idx := range e.toolBlocks {
		toolIdxs = append(toolIdxs, idx)
	}
	sort.Ints(toolIdxs)
	for _, idx := range toolIdxs {
		out = append(out, e.emitContentBlockStop(idx)...)
	}
	e.toolBlocks = nil

	stopReason := mapFinishReasonToStop(finishReason)
	out = append(out, encodeSSE("message_delta", map[string]any{
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  0,
			"output_tokens": e.outputTokens,
		},
	})...)
	return out
}

// encodeSSE marshals payload to JSON and wraps it in an SSE event frame with
// both the event: line and the JSON "type" field set. Anthropic's wire format
// uses BOTH (event: message_start as the SSE field, and "type":"message_start"
// in the JSON body); clients can match either.
func encodeSSE(eventType string, payload map[string]any) []byte {
	if _, ok := payload["type"]; !ok {
		payload["type"] = eventType
	}
	b, err := json.Marshal(payload)
	if err != nil {
		// All payloads here are maps of strings/ints/nil — json.Marshal cannot
		// fail. If it does, surface a programming error rather than silently
		// corrupting the stream.
		panic(fmt.Sprintf("anthropic: marshal SSE payload: %v", err))
	}
	return sse.Encode(sse.Event{Event: eventType, Data: string(b)})
}

//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"voxeltoad/test/testsupport"
)

// jsonUpstream returns a mock upstream that always replies with a fixed
// OpenAI-shaped non-streaming completion carrying the given content + token
// usage. It increments *hits on each call.
func jsonUpstream(content string, promptTokens, completionTokens int, hits *int) *testsupport.MockUpstream {
	body := fmt.Sprintf(
		`{"id":"chatcmpl-x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
		content, promptTokens, completionTokens, promptTokens+completionTokens,
	)
	return testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

// failingUpstream returns a mock upstream that replies with the given status
// (e.g. 500 → retryable → failover). It increments *hits on each call.
func failingUpstream(status int, hits *int) *testsupport.MockUpstream {
	return testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			*hits++
		}
		w.WriteHeader(status)
	})
}

// sseUpstream returns a mock upstream that streams an OpenAI-style SSE sequence:
// a content chunk, a trailing usage-only chunk, then [DONE].
func sseUpstream(content string, promptTokens, completionTokens int, hits *int) *testsupport.MockUpstream {
	return sseUpstreamDelayed(content, promptTokens, completionTokens, hits, 0)
}

// sseUpstreamDelayed is sseUpstream with a delay inserted between the first
// content chunk and the trailing usage/[DONE] chunks. Used to assert TTFT: the
// first chunk must reach the client well before chunkDelay elapses, proving the
// forwarding layer flushes immediately rather than buffering the whole stream
// (design/e2e.md pitfall: "SSE 缓冲攒包").
func sseUpstreamDelayed(content string, promptTokens, completionTokens int, hits *int, chunkDelay time.Duration) *testsupport.MockUpstream {
	return testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		writeChunk := func(s string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", s)
			if fl != nil {
				fl.Flush()
			}
		}
		writeChunk(fmt.Sprintf(
			`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`, content))
		if chunkDelay > 0 {
			time.Sleep(chunkDelay)
		}
		writeChunk(fmt.Sprintf(
			`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
			promptTokens, completionTokens, promptTokens+completionTokens))
		writeChunk("[DONE]")
	})
}

// capturingUpstream returns a mock upstream that behaves like jsonUpstream
// (fixed non-streaming reply) but also records the raw request body it
// received into *captured, so a test can assert what the gateway actually
// forwarded upstream (e.g. a full multi-turn message history).
func capturingUpstream(content string, promptTokens, completionTokens int, captured *[]byte) *testsupport.MockUpstream {
	body := fmt.Sprintf(
		`{"id":"chatcmpl-x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
		content, promptTokens, completionTokens, promptTokens+completionTokens,
	)
	return newMockUpstreamCapturing(body, captured)
}

// newMockUpstreamCapturing returns a mock upstream that replies with a fixed
// pre-built response body (any OpenAI-shape string) and captures the raw
// upstream request body. Used by tests that need an exotic upstream reply
// (e.g. tool_calls) while still asserting what was forwarded.
func newMockUpstreamCapturing(replyBody string, captured *[]byte) *testsupport.MockUpstream {
	return testsupport.NewMockUpstream(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			b, _ := io.ReadAll(r.Body)
			*captured = b
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(replyBody))
	})
}

// sseToolCallUpstream returns a mock upstream that streams an OpenAI-style
// tool-call SSE sequence: role + tool_calls first chunk, argument fragments
// at each index, finish_reason=tool_calls, usage chunk, [DONE].
// toolChunks is a list of SSE data lines (without "data: " prefix) to emit
// in order; the helper appends a usage-only chunk and [DONE] automatically.
// Each line should be a valid JSON string representing one SSE data event.
func sseToolCallUpstream(toolChunkLines []string, promptTokens, completionTokens int, hits *int) *testsupport.MockUpstream {
	return testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		emit := func(s string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", s)
			if fl != nil {
				fl.Flush()
			}
		}
		for _, line := range toolChunkLines {
			emit(line)
		}
		// Trailing usage chunk (empty choices, OpenAI convention).
		emit(fmt.Sprintf(
			`{"id":"s","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
			promptTokens, completionTokens, promptTokens+completionTokens))
		emit("[DONE]")
	})
}

// claudeJSONUpstream returns a mock Anthropic-protocol upstream that replies
// with a fixed Messages API non-streaming response. Used by protocol-aware
// passthrough e2e tests (ADR-0047): the gateway's claude adapter parses this
// and preserves Raw for the anthropic ingress codec to relay verbatim.
func claudeJSONUpstream(text string, inputTokens, outputTokens int, hits *int) *testsupport.MockUpstream {
	body := fmt.Sprintf(
		`{"id":"msg_x","type":"message","role":"assistant","model":"claude-opus-4-5","content":[{"type":"text","text":%q}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":%d}}`,
		text, inputTokens, outputTokens,
	)
	return testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("anthropic-version", "2023-06-01")
		_, _ = w.Write([]byte(body))
	})
}

// claudeSSEUpstream streams an Anthropic Messages SSE sequence: message_start,
// content_block_start, content_block_delta(s), content_block_stop,
// message_delta, message_stop. Each line in events is a complete SSE frame body
// (the JSON after "data: "); the helper wraps each in event:/data: lines using
// the type field inside the JSON.
func claudeSSEUpstream(events []claudeEvent, hits *int) *testsupport.MockUpstream {
	return testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("anthropic-version", "2023-06-01")
		fl, _ := w.(http.Flusher)
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			if fl != nil {
				fl.Flush()
			}
		}
	})
}

// claudeEvent is one Anthropic SSE event for the claudeSSEUpstream mock.
type claudeEvent struct {
	Type string // SSE event: field (message_start / content_block_delta / …)
	Data string // JSON payload
}

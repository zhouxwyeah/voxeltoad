// Package openai implements the ingress Codec for the OpenAI wire format.
//
// The unified model IS the OpenAI-compatible shape, so this codec is nearly an
// identity transform. It exists so the proxy layer talks to every ingress
// protocol through the same Codec interface (eliminating the implicit
// "inbound = OpenAI" assumption) and so a third ingress protocol can be added
// without touching the handlers.
//
// DecodeRequest delegates to UnifiedRequest.UnmarshalJSON (which captures
// unknown fields into Extra for passthrough fidelity, ADR-0032). EncodeResponse
// prefers resp.Raw (byte-for-byte passthrough) and falls back to re-marshalling.
// The stream encoder emits chat.completion.chunk SSE frames, preferring
// chunk.Raw and falling back to a re-encoded wire struct.
package openai

import (
	"encoding/json"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/ingress"
	"voxeltoad/pkg/sse"
)

func init() {
	ingress.Register(&codec{})
}

// codec is the OpenAI ingress codec. It is stateless; NewStreamEncoder
// produces a fresh per-stream encoder.
type codec struct{}

func (*codec) Protocol() ingress.Protocol { return ingress.ProtocolOpenAI }

// DecodeRequest parses an OpenAI-shaped request body into the unified model.
// UnifiedRequest.UnmarshalJSON captures unknown fields into Extra so nothing
// is silently dropped (ADR-0032).
func (*codec) DecodeRequest(body []byte) (*adapter.UnifiedRequest, error) {
	var req adapter.UnifiedRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// EncodeResponse prefers the original upstream response body (resp.Raw) to
// avoid data loss from a re-encode round-trip (system_fingerprint, logprobs,
// extra choice fields, …). Falls back to re-marshalling the unified response
// when Raw is nil (e.g. an adapter that does not preserve the original body).
func (*codec) EncodeResponse(resp *adapter.UnifiedResponse) ([]byte, error) {
	if len(resp.Raw) > 0 && resp.RawProtocol == "openai" {
		// Copy so the caller cannot mutate the cached slice via the returned
		// bytes (Raw is retained by the adapter).
		out := make([]byte, len(resp.Raw))
		copy(out, resp.Raw)
		return out, nil
	}
	return json.Marshal(resp)
}

func (*codec) NewStreamEncoder() ingress.StreamEncoder { return &streamEncoder{} }

// EncodeError emits the OpenAI error envelope {"error":{"message":...,"type":...}}.
func (*codec) EncodeError(_ int, errType, message string) []byte {
	// json.Marshal cannot fail for a map[string]any of strings; ignore the error.
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
		},
	})
	return b
}

func (*codec) StreamContentType() string { return "text/event-stream" }

// StreamTerminator returns the [DONE] sentinel that terminates every OpenAI
// stream, as wire bytes ready to write.
func (*codec) StreamTerminator() []byte {
	return sse.Encode(sse.Event{Data: sse.Done})
}

// streamEncoder emits OpenAI chat.completion.chunk frames. For each chunk it
// prefers chunk.Raw (byte-for-byte passthrough from the upstream) and falls
// back to re-encoding a wire struct when Raw is empty.
type streamEncoder struct{}

func (e *streamEncoder) EncodeChunk(c adapter.Chunk) ([]byte, error) {
	if len(c.Raw) > 0 && c.RawProtocol == "openai" {
		// The upstream data line is already a complete chat.completion.chunk
		// JSON; wrap it in an SSE data: frame.
		return sse.Encode(sse.Event{Data: string(c.Raw)}), nil
	}
	b, err := json.Marshal(toWireChunk(c))
	if err != nil {
		return nil, err
	}
	return sse.Encode(sse.Event{Data: string(b)}), nil
}

func (e *streamEncoder) Close() ([]byte, error) { return nil, nil }

// wireStreamChunk is the OpenAI-compatible chat.completion.chunk shape. It
// lives here (moved from internal/proxy/stream.go) because the proxy layer no
// longer needs to know the inbound wire shape — the ingress codec owns it.
type wireStreamChunk struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Model   string             `json:"model"`
	Choices []wireStreamChoice `json:"choices"`
	Usage   *adapter.Usage     `json:"usage,omitempty"`
}

type wireStreamChoice struct {
	Index        int             `json:"index"`
	Delta        wireStreamDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type wireStreamDelta struct {
	Role      string                    `json:"role,omitempty"`
	Content   string                    `json:"content,omitempty"`
	ToolCalls []wireStreamToolCallDelta `json:"tool_calls,omitempty"`
}

// wireStreamToolCallDelta is the OpenAI delta.tool_calls[*] entry. Index groups
// fragments of the same call so clients can reassemble streamed tool calls.
type wireStreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// toWireChunk converts a unified adapter.Chunk into the OpenAI-compatible wire
// shape. A usage-only chunk (no delta, no tool calls, no finish reason) is
// emitted with an empty choices array, matching OpenAI's trailing usage chunk
// convention.
func toWireChunk(c adapter.Chunk) wireStreamChunk {
	out := wireStreamChunk{
		ID:      c.ID,
		Object:  "chat.completion.chunk",
		Model:   c.Model,
		Choices: []wireStreamChoice{},
		Usage:   c.Usage,
	}
	if c.DeltaRole != "" || c.DeltaContent != "" || c.FinishReason != "" || len(c.DeltaToolCalls) > 0 {
		choice := wireStreamChoice{
			Delta: wireStreamDelta{
				Role:    string(c.DeltaRole),
				Content: c.DeltaContent,
			},
		}
		if len(c.DeltaToolCalls) > 0 {
			choice.Delta.ToolCalls = make([]wireStreamToolCallDelta, len(c.DeltaToolCalls))
			for i, tc := range c.DeltaToolCalls {
				w := &choice.Delta.ToolCalls[i]
				w.Index = tc.Index
				w.ID = tc.ID
				w.Type = tc.Type
				w.Function.Name = tc.Function.Name
				w.Function.Arguments = tc.Function.Arguments
			}
		}
		if c.FinishReason != "" {
			fr := c.FinishReason
			choice.FinishReason = &fr
		}
		out.Choices = []wireStreamChoice{choice}
	}
	return out
}

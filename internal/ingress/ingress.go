// Package ingress defines the ingress protocol codec abstraction.
//
// Each ingress protocol (openai, anthropic, ...) implements Codec to translate
// between a client wire format and the gateway's unified (OpenAI-compatible)
// request/response model. Codecs are pure translators (no HTTP transport),
// mirroring the adapter contract on the inbound side — adapters translate
// unified → upstream-provider-native on the outbound side; codecs translate
// client-wire ↔ unified on the inbound side. Both are values-in/values-out
// (bytes / unified values, no *http.Request / *http.Response), which keeps them
// testable with byte-level testdata samples (see design/unit-test.md).
//
// The ingress layer sits at L2 (peer of internal/adapter/). It is imported by
// internal/proxy/ (L3) and only imports internal/adapter/ (for the unified
// types) and pkg/sse/. Two ingress implementations never import each other
// (cf. architecture.md Dependency Rules, rule 2).
package ingress

import (
	"voxeltoad/internal/adapter"
)

// Protocol is the ingress protocol identifier (also the registry key).
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
)

// Codec translates between a client wire format and the unified model. Every
// method is values-in/values-out so codecs remain testable without an HTTP
// transport (see the package doc).
type Codec interface {
	// Protocol returns the protocol identifier.
	Protocol() Protocol

	// DecodeRequest parses a client request body into the unified model.
	// The caller owns body; implementations must not retain it.
	DecodeRequest(body []byte) (*adapter.UnifiedRequest, error)

	// EncodeResponse serializes a unified non-streaming response into the
	// client's wire format bytes.
	EncodeResponse(resp *adapter.UnifiedResponse) ([]byte, error)

	// NewStreamEncoder returns a stateful StreamEncoder that converts a
	// sequence of unified Chunks into the client's SSE wire bytes. The
	// encoder maintains per-stream state (message framing, content block
	// indices, …) across calls to EncodeChunk; callers must Close it once.
	NewStreamEncoder() StreamEncoder

	// EncodeError builds a wire-format error body. The HTTP status is taken
	// from the caller (the handler decides it via apperr / mapForwardError);
	// the body shape is protocol-specific (OpenAI: {"error":{...}};
	// Anthropic: {"type":"error","error":{...}}). errType is the stable
	// machine-readable code (OpenAI convention: snake_case strings like
	// "authentication_error"); Anthropic maps it to its own type vocabulary.
	EncodeError(status int, errType string, message string) []byte

	// StreamContentType returns the Content-Type header for SSE responses.
	StreamContentType() string

	// StreamTerminator returns the bytes that terminate a stream (OpenAI:
	// "data: [DONE]\n\n"; Anthropic: the message_stop event bytes).
	StreamTerminator() []byte
}

// StreamEncoder converts unified Chunks into protocol-specific SSE event bytes.
// Implementations maintain internal state (e.g. Anthropic content_block
// indices). Callers must call Close once to emit the terminal event(s); after
// Close the encoder is unusable.
type StreamEncoder interface {
	// EncodeChunk converts one unified Chunk into 0 or more SSE wire bytes.
	// Returns nil bytes when the chunk maps to no output.
	// Implementations must not retain the chunk after returning.
	EncodeChunk(c adapter.Chunk) ([]byte, error)

	// Close emits the terminal event(s) for this stream. Called exactly once
	// by the handler when the upstream stream ends (clean, dropped, or
	// errored).
	Close() ([]byte, error)
}

// Registry =============================================================

var registry = map[Protocol]Codec{}

// Register adds a codec to the registry. Intended to be called from init() in
// each codec package; the registry is populated at init time and read-only
// thereafter. A second registration of the same protocol panics — it would
// indicate a wiring bug.
func Register(c Codec) {
	p := c.Protocol()
	if _, dup := registry[p]; dup {
		panic("ingress: duplicate codec for protocol " + string(p))
	}
	registry[p] = c
}

// Lookup returns the codec for the given protocol. It panics if the protocol
// has no registered codec — the registry is populated at init time and the set
// of protocols is a closed small enum, so a missing codec indicates a wiring
// bug rather than a runtime condition.
func Lookup(p Protocol) Codec {
	c, ok := registry[p]
	if !ok {
		panic("ingress: no codec registered for protocol " + string(p))
	}
	return c
}

package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
	"voxeltoad/internal/ingress"
	_ "voxeltoad/internal/ingress/openai" // register OpenAI ingress codec
	"voxeltoad/internal/plugin"
)

// failingStreamEncoder is a StreamEncoder whose EncodeChunk always fails. Used
// to verify the proxy's error path (telemetry + log) when an ingress codec
// can't translate a chunk (P0 correctness: previously the error was silently
// swallowed, the client saw a truncated stream with no signal).
type failingStreamEncoder struct{}

func (failingStreamEncoder) EncodeChunk(_ adapter.Chunk) ([]byte, error) {
	return nil, errors.New("synthetic encode failure")
}
func (failingStreamEncoder) Close() ([]byte, error) { return nil, nil }

// failingStreamCodec is an ingress.Codec that produces failingStreamEncoder.
type failingStreamCodec struct{ inner ingress.Codec }

func (c failingStreamCodec) Protocol() ingress.Protocol { return c.inner.Protocol() }
func (c failingStreamCodec) DecodeRequest(b []byte) (*adapter.UnifiedRequest, error) {
	return c.inner.DecodeRequest(b)
}
func (c failingStreamCodec) EncodeResponse(r *adapter.UnifiedResponse) ([]byte, error) {
	return c.inner.EncodeResponse(r)
}
func (c failingStreamCodec) NewStreamEncoder() ingress.StreamEncoder {
	return failingStreamEncoder{}
}
func (c failingStreamCodec) EncodeError(status int, errType, msg string) []byte {
	return c.inner.EncodeError(status, errType, msg)
}
func (c failingStreamCodec) StreamContentType() string { return c.inner.StreamContentType() }
func (c failingStreamCodec) StreamTerminator() []byte  { return c.inner.StreamTerminator() }

// TestStreamChatCompletions_EncodeChunkFailureRecordsTelemetry verifies that
// when the ingress codec's stream encoder fails mid-stream, the request is
// recorded with error_type "api_error" (not silently swallowed as a clean
// stream). The HTTP status stays 200 (headers already sent) but telemetry and
// logs must reflect the failure so operators can diagnose truncated streams.
func TestStreamChatCompletions_EncodeChunkFailureRecordsTelemetry(t *testing.T) {
	// Mock upstream that streams one content chunk then [DONE].
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"s\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	// Build a dispatcher routing to the mock upstream.
	disp := NewSingleProviderDispatcher(newTestForwarder(t, upstream.URL))

	// Telemetry accumulator + a failing codec wrapping the OpenAI codec.
	acc := newTelemetryAcc("m", true, "rid", "", "sid", "tid", nil)
	codec := failingStreamCodec{inner: ingress.Lookup(ingress.ProtocolOpenAI)}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	pc := &plugin.Context{Ctx: req.Context(), Request: &adapter.UnifiedRequest{Model: "m"}}

	streamChatCompletions(rr, req, disp, "m", &adapter.UnifiedRequest{Model: "m", Stream: true}, nil, pc, acc, codec)

	if acc.errType != "api_error" {
		t.Errorf("acc.errType = %q, want api_error (encode failure must be recorded, not silently swallowed)", acc.errType)
	}
	if acc.errMsg == "" {
		t.Error("acc.errMsg empty; encode failure cause should be captured for diagnostics")
	}
}

// newTestForwarder builds a Forwarder pointing at the given upstream URL with
// a dummy API key. Mirrors the helper in forward_test.go but kept package-internal
// so stream_encode_error_test.go can use it without the _test package boundary.
func newTestForwarder(t *testing.T, upstreamURL string) *Forwarder {
	t.Helper()
	a, err := adapter.New("openai", adapter.Options{BaseURL: upstreamURL, APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("adapter.New: %v", err)
	}
	return NewForwarder(a, config.ProviderTimeouts{})
}

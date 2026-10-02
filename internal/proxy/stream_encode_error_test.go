package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/billing"
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

type disconnectWriter struct {
	*httptest.ResponseRecorder
	writes, failAt int
}

func (w *disconnectWriter) Write(body []byte) (int, error) {
	w.writes++
	if w.failAt > 0 && w.writes >= w.failAt {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(body)
}

func TestStreamSettlementOnDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		failAt                              int
		zeroUsage, priorRisk, upstreamError bool
		want                                string
		cost                                int64
	}{
		{"before final usage", 1, false, false, false, "unknown", 0},
		{"final usage received", 2, false, false, false, "known", 25},
		{"known zero usage", 2, true, false, false, "known", 0},
		{"prior attempt risk settles known", 2, false, true, false, "known", 25},
		{"upstream failure remains unknown", 0, false, false, true, "unknown", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt, completion := 11, 7
			if tc.zeroUsage {
				prompt, completion = 0, 0
			}
			body := "data: {\"id\":\"s\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
				fmt.Sprintf("data: {\"id\":\"s\",\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", prompt, completion, prompt+completion)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.upstreamError {
					w.Header().Set("Content-Length", fmt.Sprint(len(body)+1))
					_, _ = io.WriteString(w, body)
					return
				}
				_, _ = io.WriteString(w, body+"data: [DONE]\n\n")
			}))
			defer upstream.Close()
			disp := NewSingleProviderDispatcher(newTestForwarder(t, upstream.URL))
			dyn := &config.Dynamic{Models: []config.Model{{Alias: "m", Upstreams: []config.ModelUpstream{{Provider: "default", Pricing: config.Pricing{Currency: "usd", PromptPer1M: 1_000_000, CompletionPer1M: 2_000_000}}}}}}
			pc := &plugin.Context{Ctx: context.Background(), Tenant: "tenant", Request: &adapter.UnifiedRequest{Model: "m", Stream: true}, PricingSnapshot: dyn, BillingAttemptRisk: tc.priorRisk}
			pc.Ctx = context.WithValue(pc.Ctx, accountingContextKey{}, pc)
			accounting := &accountingRecorder{}
			usage := billing.NewMemoryUsageRecorder()
			bill := billing.NewPlugin(func() *config.Dynamic { return dyn }, nil, usage, billing.WithAccounting(accounting))
			if err := bill.Execute(pc, plugin.PhasePre); err != nil || pc.Stop {
				t.Fatalf("reserve: %v", err)
			}
			writer := &disconnectWriter{ResponseRecorder: httptest.NewRecorder(), failAt: tc.failAt}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(pc.Ctx)
			streamChatCompletions(writer, req, disp, "m", pc.Request, plugin.NewChain(bill), pc, newTelemetryAcc("m", true, "rid", "", "", "", nil), ingress.Lookup(ingress.ProtocolOpenAI))
			if err := pc.BillingFinalize(); err != nil {
				t.Fatal(err)
			}
			if len(accounting.settlements) != 1 || accounting.settlements[0].Outcome != tc.want || accounting.settlements[0].Actual != tc.cost {
				t.Fatalf("settlements=%+v, want %s cost=%d", accounting.settlements, tc.want, tc.cost)
			}
			if tc.priorRisk && !accounting.settlements[0].AttemptRisk {
				t.Fatalf("prior attempt risk not carried: %+v", accounting.settlements[0])
			}
			settled := accounting.settlements[0].Usage
			if tc.want == "known" {
				if settled == nil || settled.Cost != tc.cost || settled.PromptTokens != prompt || settled.CompletionTokens != completion {
					t.Fatalf("known usage not carried in the settlement payload: %+v", settled)
				}
			} else if settled != nil {
				t.Fatalf("unconfirmed charge carried usage: %+v", settled)
			}
			if records := usage.Records(); len(records) != 0 {
				t.Fatalf("accounting mode bypasses the async ledger: %+v", records)
			}
		})
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

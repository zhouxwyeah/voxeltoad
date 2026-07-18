package proxy_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/adapter/openai"
	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
	"voxeltoad/pkg/sse"
)

// newUpstream starts a mock OpenAI upstream with the given handler and returns
// its base URL (with the /v1 suffix the adapter expects to append paths to).
func newUpstream(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func newForwarder(t *testing.T, upstreamURL string) *proxy.Forwarder {
	t.Helper()
	a, err := openai.New(openai.Options{BaseURL: upstreamURL, APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	return proxy.NewForwarder(a, config.ProviderTimeouts{
		Connect:   2 * time.Second,
		FirstByte: 2 * time.Second,
		Overall:   5 * time.Second,
	})
}

// newDispatcherFor wraps a single upstream as a single-provider Dispatcher,
// matching the previous Router(forwarder) call sites.
func newDispatcherFor(t *testing.T, upstreamURL string) *proxy.Dispatcher {
	t.Helper()
	return proxy.NewSingleProviderDispatcher(newForwarder(t, upstreamURL))
}

const clientChatReq = `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`

func TestForward_NonStreamingHappyPath(t *testing.T) {
	const upstreamBody = `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello there!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":12,"total_tokens":21}}`

	var gotAuth, gotPath, gotBody string
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamBody))
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	proxy.Router(newDispatcherFor(t, upstream)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// Upstream received a correctly-built request.
	if gotAuth != "Bearer sk-test" {
		t.Errorf("upstream Authorization = %q", gotAuth)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("upstream path = %q, want /chat/completions", gotPath)
	}
	if !strings.Contains(gotBody, `"model":"gpt-4o"`) {
		t.Errorf("upstream body missing model: %s", gotBody)
	}
	// Client received the unified response.
	var resp struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode client response: %v; body=%s", err, rr.Body.String())
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello there!" {
		t.Errorf("client content wrong: %+v", resp)
	}
	if resp.Usage.TotalTokens != 21 {
		t.Errorf("usage total = %d, want 21", resp.Usage.TotalTokens)
	}
}

// TestForward_CapturesUpstreamRequestIDHeader asserts the Forwarder reads the
// upstream provider's request id from the response header and attaches it to
// the UnifiedResponse (header is authoritative; body fallback is provider-
// specific and tested in each adapter).
func TestForward_CapturesUpstreamRequestIDHeader(t *testing.T) {
	const upstreamBody = `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

	upstream := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-request-id", "req_abc123")
		_, _ = w.Write([]byte(upstreamBody))
	})

	fwd := newForwarder(t, upstream)
	resp, err := fwd.Forward(context.Background(), &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.UpstreamRequestID != "req_abc123" {
		t.Errorf("UpstreamRequestID = %q, want req_abc123", resp.UpstreamRequestID)
	}
}

// TestForward_UpstreamRequestIDAbsentWhenHeaderMissing asserts that when the
// provider returns no request-id header, UpstreamRequestID stays empty (no
// synthetic value is invented).
func TestForward_UpstreamRequestIDAbsentWhenHeaderMissing(t *testing.T) {
	const upstreamBody = `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

	upstream := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		// No x-request-id header set.
		_, _ = w.Write([]byte(upstreamBody))
	})

	fwd := newForwarder(t, upstream)
	resp, err := fwd.Forward(context.Background(), &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.UpstreamRequestID != "" {
		t.Errorf("UpstreamRequestID = %q, want ''", resp.UpstreamRequestID)
	}
}

// TestForwardStream_CapturesUpstreamRequestIDHeader asserts ForwardStream also
// surfaces the upstream request id from the response header (returned alongside
// the StreamReader).
func TestForwardStream_CapturesUpstreamRequestIDHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-request-id", "req_stream_xyz")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(openAIStreamBody))
	}))
	t.Cleanup(srv.Close)

	fwd := newForwarder(t, srv.URL)
	sr, upstreamID, err := fwd.ForwardStream(context.Background(), &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Stream:   true,
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
	})
	if err != nil {
		t.Fatalf("ForwardStream: %v", err)
	}
	defer func() { _ = sr.Close() }()
	if upstreamID != "req_stream_xyz" {
		t.Errorf("upstreamID = %q, want req_stream_xyz", upstreamID)
	}
}

func TestForward_UpstreamErrorMapsToBadGateway(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	proxy.Router(newDispatcherFor(t, upstream)).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	// Error is reported in OpenAI-compatible shape: {"error":{...}}.
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, rr.Body.String())
	}
	if e.Error.Message == "" {
		t.Errorf("expected non-empty error.message; body=%s", rr.Body.String())
	}
}

func TestForward_InvalidClientJSONIsBadRequest(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("upstream must not be called on invalid client request")
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("not json"))
	proxy.Router(newDispatcherFor(t, upstream)).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestForward_OverallTimeoutMapsToGatewayTimeout(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	})
	a, err := openai.New(openai.Options{BaseURL: upstream, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	fwd := proxy.NewForwarder(a, config.ProviderTimeouts{
		Connect:   time.Second,
		FirstByte: 50 * time.Millisecond, // upstream is slower than this
		Overall:   time.Second,
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	proxy.Router(proxy.NewSingleProviderDispatcher(fwd)).ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%s", rr.Code, rr.Body.String())
	}
}

func TestRouter_NilForwarderReturnsNotImplemented(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	proxy.Router(nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rr.Code)
	}
}

func TestHealthz(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	proxy.Router(nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Errorf("healthz = %d %q", rr.Code, rr.Body.String())
	}
}

// ---- Step 3.5: streaming ----

const clientStreamReq = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`

// openAIStreamBody is a canonical OpenAI SSE stream: two content deltas, a
// finish chunk, a trailing usage-only chunk, then [DONE].
const openAIStreamBody = "data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2,\"total_tokens\":11}}\n\n" +
	"data: [DONE]\n\n"

// streamUpstream writes an SSE stream, optionally flushing after each frame and
// pausing, so tests can exercise chunked delivery and TTFT.
func streamUpstream(t *testing.T, frames []string, pause time.Duration) string {
	t.Helper()
	return newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream ResponseWriter is not a Flusher")
		}
		w.WriteHeader(http.StatusOK)
		for _, f := range frames {
			_, _ = io.WriteString(w, f)
			fl.Flush()
			if pause > 0 {
				time.Sleep(pause)
			}
		}
	})
}

// collectSSE parses an SSE response body into events.
func collectSSE(t *testing.T, body io.Reader) []sse.Event {
	t.Helper()
	d := sse.NewDecoder(body)
	var out []sse.Event
	for {
		e, err := d.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("decode client SSE: %v", err)
		}
		out = append(out, e)
	}
}

func TestForwardStream_HappyPathRelaysOpenAICompatibleSSE(t *testing.T) {
	upstream := streamUpstream(t, []string{openAIStreamBody}, 0)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientStreamReq))
	proxy.Router(newDispatcherFor(t, upstream)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	events := collectSSE(t, rr.Body)
	if len(events) == 0 {
		t.Fatal("no SSE events relayed to client")
	}
	// Last event must be the [DONE] sentinel.
	if events[len(events)-1].Data != sse.Done {
		t.Errorf("last event = %q, want [DONE]", events[len(events)-1].Data)
	}

	// Reassemble content and find trailing usage from the relayed chunks.
	var content strings.Builder
	var finish string
	var usageTotal int
	for _, e := range events {
		if e.Data == sse.Done {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta        struct{ Content string } `json:"delta"`
				FinishReason string                   `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(e.Data), &chunk); err != nil {
			t.Fatalf("relayed chunk not valid JSON: %v (%q)", err, e.Data)
		}
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
			if chunk.Choices[0].FinishReason != "" {
				finish = chunk.Choices[0].FinishReason
			}
		}
		if chunk.Usage != nil {
			usageTotal = chunk.Usage.TotalTokens
		}
	}
	if content.String() != "Hello" {
		t.Errorf("relayed content = %q, want Hello", content.String())
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q, want stop", finish)
	}
	if usageTotal != 11 {
		t.Errorf("trailing usage total = %d, want 11", usageTotal)
	}
}

// TestForwardStream_FlushesIncrementally guards against buffering the whole
// stream: the client must observe the first chunk well before the upstream has
// finished sending later chunks (see design/e2e.md: TTFT / no attic buffering).
func TestForwardStream_FlushesIncrementally(t *testing.T) {
	frames := []string{
		"data: {\"id\":\"s1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"s1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"second\"},\"finish_reason\":\"stop\"}]}\n\n",
		"data: [DONE]\n\n",
	}
	// Big pause between frames; if the proxy buffered, the whole handler would
	// block until the end and the test would take >2s. We assert the handler
	// streams by reading via a pipe and timing the first byte.
	upstream := streamUpstream(t, frames, 800*time.Millisecond)

	// Use a real server so the response body streams incrementally (httptest
	// ResponseRecorder buffers, so it can't show TTFT).
	srv := httptest.NewServer(proxy.Router(newDispatcherFor(t, upstream)))
	t.Cleanup(srv.Close)

	start := time.Now()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(clientStreamReq))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	br := bufio.NewReader(resp.Body)
	// Read until we get the first non-empty SSE data line.
	var firstByteAt time.Duration
	for {
		line, err := br.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			firstByteAt = time.Since(start)
			break
		}
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
	}
	// First chunk must arrive long before the upstream finishes (2 pauses ≈
	// 1.6s). Allow generous headroom but well under that.
	if firstByteAt > 600*time.Millisecond {
		t.Errorf("first chunk took %v; proxy appears to buffer instead of stream", firstByteAt)
	}
}

// TestForwardStream_PartialFramesAcrossReads feeds the SSE one byte at a time
// from the upstream to ensure frame reassembly survives split reads.
func TestForwardStream_PartialFramesAcrossReads(t *testing.T) {
	// Split the canonical body into single-byte writes.
	frames := make([]string, 0, len(openAIStreamBody))
	for i := 0; i < len(openAIStreamBody); i++ {
		frames = append(frames, openAIStreamBody[i:i+1])
	}
	upstream := streamUpstream(t, frames, 0)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientStreamReq))
	proxy.Router(newDispatcherFor(t, upstream)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	events := collectSSE(t, rr.Body)
	var content strings.Builder
	for _, e := range events {
		if e.Data == sse.Done {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		_ = json.Unmarshal([]byte(e.Data), &chunk)
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if content.String() != "Hello" {
		t.Errorf("content = %q, want Hello", content.String())
	}
}

// TestForwardStream_UpstreamDropsMidStream: when the upstream connection ends
// without a [DONE], the proxy must still terminate the client stream cleanly
// (emit [DONE]) so the client isn't left hanging, and surface that some content
// was delivered. Already-consumed tokens are not lost.
func TestForwardStream_UpstreamDropsMidStream(t *testing.T) {
	truncated := "data: {\"id\":\"s1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"par\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"s1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"tial\"},\"finish_reason\":null}]}\n\n"
	// No [DONE]; upstream handler returns, closing the body.
	upstream := streamUpstream(t, []string{truncated}, 0)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientStreamReq))
	proxy.Router(newDispatcherFor(t, upstream)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (headers already sent before drop)", rr.Code)
	}
	events := collectSSE(t, rr.Body)
	if len(events) == 0 {
		t.Fatal("expected partial content relayed before drop")
	}
	// The proxy must close the client stream with [DONE] even on an abrupt
	// upstream end.
	if events[len(events)-1].Data != sse.Done {
		t.Errorf("last event = %q, want [DONE] even after mid-stream drop", events[len(events)-1].Data)
	}
	var content strings.Builder
	for _, e := range events {
		if e.Data == sse.Done {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		_ = json.Unmarshal([]byte(e.Data), &chunk)
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if content.String() != "partial" {
		t.Errorf("content = %q, want partial", content.String())
	}
}

// TestForwardStream_DirectReaderContract exercises Forwarder.ForwardStream
// directly (no HTTP), asserting it yields unified chunks ending in io.EOF and
// that the trailing chunk carries usage.
func TestForwardStream_DirectReaderContract(t *testing.T) {
	upstream := streamUpstream(t, []string{openAIStreamBody}, 0)
	fwd := newForwarder(t, upstream)

	sr, _, err := fwd.ForwardStream(context.Background(), &adapter.UnifiedRequest{
		Model:    "gpt-4o",
		Stream:   true,
		Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
	})
	if err != nil {
		t.Fatalf("ForwardStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var content strings.Builder
	var lastUsage *adapter.Usage
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		content.WriteString(c.DeltaContent)
		if c.Usage != nil {
			lastUsage = c.Usage
		}
	}
	if content.String() != "Hello" {
		t.Errorf("content = %q, want Hello", content.String())
	}
	if lastUsage == nil || lastUsage.TotalTokens != 11 {
		t.Errorf("trailing usage = %+v, want total 11", lastUsage)
	}
}

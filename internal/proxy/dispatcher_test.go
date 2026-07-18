package proxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/adapter/openai"
	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
)

const okBody = `{"id":"x","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

// fwdTo builds a Forwarder whose adapter targets the given upstream URL.
func fwdTo(t *testing.T, url string) *proxy.Forwarder {
	t.Helper()
	a, err := openai.New(openai.Options{BaseURL: url, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	return proxy.NewForwarder(a, config.ProviderTimeouts{
		Connect: time.Second, FirstByte: time.Second, Overall: 3 * time.Second,
	})
}

func dispatchReq() *adapter.UnifiedRequest {
	return &adapter.UnifiedRequest{Model: "gpt-4o", Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}}}
}

func TestDispatcher_FirstProviderSucceeds(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, up.URL), "b": fwdTo(t, up.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)

	resp, dr, err := d.Forward(context.Background(), "gpt-4o", dispatchReq())
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if dr.Provider != "a" {
		t.Errorf("hit = %q, want a (first priority)", dr.Provider)
	}
	if dr.Fallback {
		t.Error("Fallback = true, want false (first candidate hit)")
	}
	if dr.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0", dr.RetryCount)
	}
	if dr.ModelResolved != "gpt-4o" {
		t.Errorf("ModelResolved = %q, want gpt-4o (no preparer: echoes requested model)", dr.ModelResolved)
	}
	if resp.Choices[0].Message.Content.Text() != "ok" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content.Text())
	}
}

func TestDispatcher_FailsOverOn5xx(t *testing.T) {
	var aHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&aHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer good.Close()

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, bad.URL), "b": fwdTo(t, good.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)

	resp, dr, err := d.Forward(context.Background(), "gpt-4o", dispatchReq())
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if dr.Provider != "b" {
		t.Errorf("hit = %q, want b (failed over from a's 5xx)", dr.Provider)
	}
	if !dr.Fallback {
		t.Error("Fallback = false, want true (a failed before b succeeded)")
	}
	if dr.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", dr.RetryCount)
	}
	if resp == nil || resp.Choices[0].Message.Content.Text() != "ok" {
		t.Error("should have gotten b's good response")
	}
	if atomic.LoadInt32(&aHits) != 1 {
		t.Errorf("a hits = %d, want 1 (tried once then failed over)", aHits)
	}
}

func TestDispatcher_DoesNotFailOverOn4xx(t *testing.T) {
	var bHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 4xx → not retryable
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&bHits, 1)
		_, _ = w.Write([]byte(okBody))
	}))
	defer good.Close()

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, bad.URL), "b": fwdTo(t, good.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)

	_, _, err := d.Forward(context.Background(), "gpt-4o", dispatchReq())
	if err == nil {
		t.Fatal("expected error: 4xx must not fail over")
	}
	if atomic.LoadInt32(&bHits) != 0 {
		t.Errorf("b hits = %d, want 0 (no failover on 4xx)", bHits)
	}
}

func TestDispatcher_AllFail_ReturnsLastError(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, bad.URL), "b": fwdTo(t, bad.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)
	if _, _, err := d.Forward(context.Background(), "gpt-4o", dispatchReq()); err == nil {
		t.Error("expected error when all providers fail")
	}
}

// TestDispatcher_TripsBreakerAfterRepeatedFailures: repeated 5xx from a provider
// trips its breaker, so it is skipped on subsequent requests.
func TestDispatcher_TripsBreakerAfterRepeatedFailures(t *testing.T) {
	var aHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&aHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer good.Close()

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, bad.URL), "b": fwdTo(t, good.URL)},
		proxy.DispatcherConfig{FailureThreshold: 2, Cooldown: time.Minute},
	)

	// Two requests fail over a→b and trip a's breaker (threshold 2).
	for i := 0; i < 2; i++ {
		if _, dr, err := d.Forward(context.Background(), "gpt-4o", dispatchReq()); err != nil || dr.Provider != "b" {
			t.Fatalf("req %d: hit=%q err=%v", i, dr.Provider, err)
		}
	}
	hitsAfterTrip := atomic.LoadInt32(&aHits)
	// Third request: a's breaker open → a skipped → b directly, a not hit again.
	if _, dr, err := d.Forward(context.Background(), "gpt-4o", dispatchReq()); err != nil || dr.Provider != "b" {
		t.Fatalf("3rd: hit=%q err=%v", dr.Provider, err)
	}
	if atomic.LoadInt32(&aHits) != hitsAfterTrip {
		t.Errorf("a was hit again after breaker should be open (hits %d → %d)", hitsAfterTrip, atomic.LoadInt32(&aHits))
	}
}

func TestDispatcher_StreamFailsOverBeforeFirstByte(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	streamBody := "data: {\"id\":\"s\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(streamBody))
	}))
	defer good.Close()

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, bad.URL), "b": fwdTo(t, good.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)

	sr, dr, err := d.ForwardStream(context.Background(), "gpt-4o", &adapter.UnifiedRequest{Model: "gpt-4o", Stream: true, Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}}})
	if err != nil {
		t.Fatalf("ForwardStream: %v", err)
	}
	defer func() { _ = sr.Close() }()
	if dr.Provider != "b" {
		t.Errorf("hit = %q, want b (failed over before first byte)", dr.Provider)
	}
	if !dr.Fallback {
		t.Error("Fallback = false, want true")
	}
	var content strings.Builder
	for {
		c, err := sr.Recv()
		if err != nil {
			break
		}
		content.WriteString(c.DeltaContent)
	}
	if content.String() != "hi" {
		t.Errorf("content = %q, want hi", content.String())
	}
}

func TestDispatcher_UnknownModelErrors(t *testing.T) {
	d := proxy.NewDispatcher(nil, map[string]*proxy.Forwarder{}, proxy.DispatcherConfig{})
	if _, _, err := d.Forward(context.Background(), "nope", dispatchReq()); err == nil {
		t.Error("unknown model should error")
	}
}

// TestDispatcher_WithModelPreparation_ResolvesUpstreamName: with preparation
// enabled, the upstream receives the provider-native model name (alias →
// gpt-4o), not the client alias.
func TestDispatcher_WithModelPreparation_ResolvesUpstreamName(t *testing.T) {
	var gotModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	dyn := &config.Dynamic{
		Providers: []config.Provider{{Name: "a", Adapter: "openai"}},
		Models: []config.Model{{
			Alias:     "default-chat",
			Upstreams: []config.ModelUpstream{{Provider: "a", UpstreamModel: "gpt-4o"}},
		}},
	}
	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "default-chat", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, up.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	).WithModelPreparation(dyn)

	_, dr, err := d.Forward(context.Background(), "default-chat", &adapter.UnifiedRequest{
		Model: "default-chat", Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if gotModel != "gpt-4o" {
		t.Errorf("upstream received model = %q, want gpt-4o (alias resolved)", gotModel)
	}
	if dr.ModelResolved != "gpt-4o" {
		t.Errorf("ModelResolved = %q, want gpt-4o", dr.ModelResolved)
	}
}

// TestDispatcher_ConfigMismatch_AllCandidatesFailPrepare: when every candidate
// fails at the prepare step (provider not an upstream of the model), the error
// must say "configuration mismatch" rather than the generic "all providers
// failed" — so operators can distinguish a routing misconfiguration from an
// upstream outage.
func TestDispatcher_ConfigMismatch_AllCandidatesFailPrepare(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	// Model "chat" only has upstream "c"; route candidates are a and b, neither
	// of which serves the model — both will fail at prepare.
	dyn := &config.Dynamic{
		Providers: []config.Provider{{Name: "a", Adapter: "openai"}, {Name: "b", Adapter: "openai"}},
		Models: []config.Model{{
			Alias:     "chat",
			Upstreams: []config.ModelUpstream{{Provider: "c", UpstreamModel: "gpt-4o"}},
		}},
	}
	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "chat", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, up.URL), "b": fwdTo(t, up.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	).WithModelPreparation(dyn)

	_, _, err := d.Forward(context.Background(), "chat", &adapter.UnifiedRequest{
		Model: "chat", Messages: []adapter.Message{{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")}},
	})
	if err == nil {
		t.Fatal("expected configuration mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "configuration mismatch") {
		t.Errorf("err = %q, want it to contain \"configuration mismatch\"", err.Error())
	}
}

// TestDispatcher_AllFail_GivesAllProvidersFailed: upstream failures (not a config
// mismatch) must still report "all providers failed" (regression guard for the
// improved error message).
func TestDispatcher_AllFail_GivesAllProvidersFailed(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[string]*proxy.Forwarder{"a": fwdTo(t, bad.URL), "b": fwdTo(t, bad.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)

	_, _, err := d.Forward(context.Background(), "gpt-4o", dispatchReq())
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !strings.Contains(err.Error(), "all providers failed") {
		t.Errorf("err = %q, want it to contain \"all providers failed\"", err.Error())
	}
}

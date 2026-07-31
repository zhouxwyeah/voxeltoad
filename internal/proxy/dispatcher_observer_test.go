package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/adapter/openai"
	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
)

// panickingObserver panics on every call to OnDispatchStep. Used to verify
// that observeStep's defer-recover prevents observer panics from breaking
// the dispatch path (ADR-0051).
type panickingObserver struct{}

func (panickingObserver) OnDispatchStep(_ context.Context, _ string, _ proxy.DispatchStepEvent) {
	panic("observer should not break dispatch")
}

// TestDispatchObserver_PanicDoesNotBreakDispatch verifies that a panicking
// DispatchObserver is recovered and never crashes the Forward path.
// Regression for the critical bug where observeStep's comment claimed
// "panics in the observer are recovered" but had no defer recover().
func TestDispatchObserver_PanicDoesNotBreakDispatch(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	a, err := openai.New(openai.Options{BaseURL: up.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}}}},
		map[proxy.EndpointKey]*proxy.Forwarder{
			{Provider: "a", Endpoint: "default"}: proxy.NewForwarder(a, config.ProviderTimeouts{
				Connect: time.Second, FirstByte: time.Second, Overall: 3 * time.Second,
			}),
		},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)
	d.WithObserver(panickingObserver{})

	resp, _, err := d.Forward(context.Background(), "gpt-4o", dispatchReq())
	if err != nil {
		t.Fatalf("Forward with panicking observer: %v (panic was not recovered)", err)
	}
	if resp == nil {
		t.Fatal("resp is nil despite no error")
	}
}

// recordingObserver is a safe observer that records all steps for assertion.
type recordingObserver struct {
	steps []proxy.DispatchStepEvent
}

func (r *recordingObserver) OnDispatchStep(_ context.Context, _ string, step proxy.DispatchStepEvent) {
	r.steps = append(r.steps, step)
}

// TestDispatchObserver_RecordsFailoverSteps verifies that the observer
// receives the correct sequence of attempted/skipped steps during a
// failover scenario, and that the dispatch path is unaffected.
func TestDispatchObserver_RecordsFailoverSteps(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer good.Close()

	aBad, _ := openai.New(openai.Options{BaseURL: bad.URL, APIKey: "k"})
	bGood, _ := openai.New(openai.Options{BaseURL: good.URL, APIKey: "k"})

	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}, {Name: "b"}}}},
		map[proxy.EndpointKey]*proxy.Forwarder{
			{Provider: "a", Endpoint: "default"}: proxy.NewForwarder(aBad, config.ProviderTimeouts{
				Connect: time.Second, FirstByte: time.Second, Overall: 3 * time.Second,
			}),
			{Provider: "b", Endpoint: "default"}: proxy.NewForwarder(bGood, config.ProviderTimeouts{
				Connect: time.Second, FirstByte: time.Second, Overall: 3 * time.Second,
			}),
		},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)
	obs := &recordingObserver{}
	d.WithObserver(obs)

	resp, dr, err := d.Forward(context.Background(), "gpt-4o", dispatchReq())
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}
	if dr.Provider != "b" {
		t.Errorf("hit = %q, want b (failed over from a's 5xx)", dr.Provider)
	}

	// The observer should have received 2 steps:
	// 1. provider "a": attempted, retryable_failure
	// 2. provider "b": attempted, selected
	if len(obs.steps) != 2 {
		t.Fatalf("observed steps = %d, want 2", len(obs.steps))
	}
	if obs.steps[0].Provider != "a" || obs.steps[0].Action != "attempted" || obs.steps[0].SelectionOutcome != "retryable_failure" {
		t.Errorf("step 0 = %+v, want provider=a attempted retryable_failure", obs.steps[0])
	}
	if obs.steps[1].Provider != "b" || obs.steps[1].Action != "attempted" || obs.steps[1].SelectionOutcome != "selected" {
		t.Errorf("step 1 = %+v, want provider=b attempted selected", obs.steps[1])
	}
	// Ordinals should be 0 and 1.
	if obs.steps[0].Ordinal != 0 || obs.steps[1].Ordinal != 1 {
		t.Errorf("ordinals = %d, %d, want 0, 1", obs.steps[0].Ordinal, obs.steps[1].Ordinal)
	}
}

// TestDispatchObserver_NilObserverIsZeroOverhead verifies that a nil observer
// (the default) does not panic and dispatches normally.
func TestDispatchObserver_NilObserverIsZeroOverhead(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	a, _ := openai.New(openai.Options{BaseURL: up.URL, APIKey: "k"})
	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}}}},
		map[proxy.EndpointKey]*proxy.Forwarder{
			{Provider: "a", Endpoint: "default"}: proxy.NewForwarder(a, config.ProviderTimeouts{
				Connect: time.Second, FirstByte: time.Second, Overall: 3 * time.Second,
			}),
		},
		proxy.DispatcherConfig{},
	)
	// No WithObserver — observer is nil.

	resp, _, err := d.Forward(context.Background(), "gpt-4o", dispatchReq())
	if err != nil {
		t.Fatalf("Forward with nil observer: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}
}

// TestDispatchObserver_StreamPanicRecovered verifies the same panic recovery
// for ForwardStream (the streaming path).
func TestDispatchObserver_StreamPanicRecovered(t *testing.T) {
	// SSE upstream that returns a minimal stream.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
	defer up.Close()

	a, err := openai.New(openai.Options{BaseURL: up.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "a"}}}},
		map[proxy.EndpointKey]*proxy.Forwarder{
			{Provider: "a", Endpoint: "default"}: proxy.NewForwarder(a, config.ProviderTimeouts{
				Connect: time.Second, FirstByte: time.Second, Overall: 3 * time.Second,
			}),
		},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	)
	d.WithObserver(panickingObserver{})

	req := dispatchReq()
	req.Stream = true
	sr, _, err := d.ForwardStream(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("ForwardStream with panicking observer: %v (panic was not recovered)", err)
	}
	if sr == nil {
		t.Fatal("stream reader is nil despite no error")
	}
	// Drain the stream to completion.
	for {
		_, err := sr.Recv()
		if err != nil {
			break
		}
	}
}

// Ensure adapter import is used.
var _ = adapter.RoleUser

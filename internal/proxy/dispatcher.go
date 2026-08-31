package proxy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

// DispatcherConfig configures failover behavior.
type DispatcherConfig struct {
	// FailureThreshold and Cooldown configure the circuit breaker (ADR-0011).
	FailureThreshold int
	Cooldown         time.Duration
}

// Dispatcher orchestrates routing and failover above the single-endpoint
// Forwarders (ADR-0011/0049): it resolves a model alias to ordered candidates,
// expands each provider to the endpoint matching the ingress protocol, tries
// each (provider, endpoint) in turn (retrying only retryable failures), tracks
// per-endpoint circuit health, and reports the endpoint actually hit (for
// billing / llm.provider / provider_endpoint).
type Dispatcher struct {
	router     *router
	breaker    *circuitBreaker
	forwarders map[EndpointKey]*Forwarder
	preparer   *modelPreparer   // optional; nil = pass the request through unchanged
	observer   DispatchObserver // optional; nil = no observation (ADR-0051)
}

// NewDispatcher builds a Dispatcher from routes, an EndpointKey→Forwarder map,
// and failover config. The forwarder map is rebuilt on config change (P0:
// built once at wiring). router's endpoint-health resolution is wired to the
// preparer's endpoint selection once WithModelPreparation is called.
func NewDispatcher(routes []config.Route, forwarders map[EndpointKey]*Forwarder, cfg DispatcherConfig) *Dispatcher {
	breaker := newCircuitBreaker(circuitConfig(cfg))
	rnd := rand.New(rand.NewSource(rand.Int63())) //nolint:gosec // load-balancing, not security
	d := &Dispatcher{breaker: breaker, forwarders: forwarders}
	r := newRouterWithRand(routes, breaker, func(n int) int {
		if n <= 0 {
			return 0
		}
		return rnd.Intn(n)
	}, d.endpointFor)
	d.router = r
	return d
}

// NewSingleProviderDispatcher builds a Dispatcher that always forwards to one
// provider's default endpoint with no routing or failover. It is the simplest
// deployment shape (one upstream) and is convenient for tests. Any model alias
// resolves to this single endpoint.
func NewSingleProviderDispatcher(fwd *Forwarder) *Dispatcher {
	const only = "default"
	breaker := newCircuitBreaker(circuitConfig{})
	d := &Dispatcher{breaker: breaker, forwarders: map[EndpointKey]*Forwarder{{Provider: only, Endpoint: "default"}: fwd}}
	d.router = newRouterWithRand(
		[]config.Route{{ModelAlias: wildcardAlias, Providers: []config.RouteProvider{{Name: only}}}},
		breaker,
		func(int) int { return 0 },
		d.endpointFor,
	)
	return d
}

// wildcardAlias is the route key matched when no exact route exists (single
// provider mode).
const wildcardAlias = "*"

// WithModelPreparation enables per-candidate request preparation (alias →
// endpoint-native upstream model + protocol normalization, ADR-0002/0009/0049)
// using the given dynamic config. Without it, the Dispatcher forwards the
// request as-is (used in tests that target a single mock upstream).
func (d *Dispatcher) WithModelPreparation(dyn *config.Dynamic) *Dispatcher {
	d.preparer = newModelPreparer(dyn)
	return d
}

// WithObserver installs an optional DispatchObserver (ADR-0051). The observer
// receives one DispatchStepEvent per candidate evaluation in Forward/ForwardStream.
// Nil observer (the default) means zero overhead. The observer must not block.
func (d *Dispatcher) WithObserver(obs DispatchObserver) *Dispatcher {
	d.observer = obs
	return d
}

// endpointFor resolves which endpoint of a provider a request under the given
// ingress protocol would use (ADR-0049). Returns (endpointID, adapterName).
// Falls back to ("default", "") when no preparer is configured (single-
// provider test mode) or the provider is unknown.
func (d *Dispatcher) endpointFor(provider, ingressProtocol string) (string, string) {
	if d.preparer == nil {
		return "default", ""
	}
	ep, ok := d.preparer.pickEndpoint(provider, ingressProtocol)
	if !ok {
		return "default", ""
	}
	return ep.EndpointID(), ep.Adapter
}

// prepare returns the request to send to the given provider endpoint: prepared
// (resolved + normalized) when a preparer is configured, else the original.
func (d *Dispatcher) prepare(req *adapter.UnifiedRequest, key EndpointKey) (*adapter.UnifiedRequest, error) {
	if d.preparer == nil {
		return req, nil
	}
	return d.preparer.Prepare(req, key.Provider, key.Endpoint)
}

// DispatchStepEvent records one candidate evaluation during a GatewayRequest's
// dispatch path. It is a read-only observation of a decision already made; the
// observer must not influence routing. ADR-0051.
type DispatchStepEvent struct {
	Ordinal           int // 0-based candidate index (post breaker filtering)
	Provider          string
	Endpoint          string
	Action            string // "skipped" | "attempted"
	SkipReason        string // "no_forwarder" | "prepare_failed" (skipped only)
	SelectionOutcome  string // "selected" | "retryable_failure" | "terminal_failure" (attempted only)
	ErrorType         string // upstream error class (attempted only)
	UpstreamRequestID string // provider-assigned id (selected only)
	DurationMs        int    // attempted: Forward/ForwardStream 耗时
}

// DispatchObserver receives DispatchStepEvents as the Dispatcher evaluates each
// candidate. Nil observer = zero overhead. The implementation must be safe for
// concurrent use and must not block (fail-open). ADR-0051.
type DispatchObserver interface {
	OnDispatchStep(ctx context.Context, requestID string, step DispatchStepEvent)
}

// DispatchResult carries the routing-layer facts an emit()/billing caller needs
// beyond the response body itself: which provider endpoint was actually hit,
// what upstream model name was resolved to (ADR-0002; equals the requested
// alias when no preparer is configured), whether a fallback across candidates
// occurred, and how many candidates were tried before the outcome (success or
// final failure). These mirror the mandatory llm.model.resolved / llm.fallback
// / llm.retry.count fields (design/observability.md) plus the multi-endpoint
// provider_endpoint attribution (ADR-0049).
type DispatchResult struct {
	Provider      string
	Endpoint      string
	ModelResolved string
	Fallback      bool
	RetryCount    int
	// UpstreamRequestID is the provider-assigned id from the successful
	// (final) attempt's response. Empty on failure or when the provider
	// returned no id. Captured for support/reconciliation (ADR-0021 §5).
	UpstreamRequestID string
}

// expandCandidates resolves each candidate provider to the endpoint this
// request should use under its ingress protocol (ADR-0049): the endpoint whose
// adapter matches the protocol, else the provider's primary endpoint. Provider
// order is preserved (strategy-driven, ADR-0011); there is NO cross-provider
// protocol partition anymore because each multi-endpoint provider natively
// speaks the client's protocol via its matching endpoint.
func (d *Dispatcher) expandCandidates(ctx context.Context, names []string) []EndpointKey {
	protocol := ingressProtocolFrom(ctx)
	out := make([]EndpointKey, 0, len(names))
	for _, name := range names {
		epID, _ := d.endpointFor(name, protocol)
		out = append(out, EndpointKey{Provider: name, Endpoint: epID})
	}
	return out
}

// Forward routes a non-streaming request and fails over across candidates on
// retryable errors. It returns the unified response and routing-layer result
// facts (provider endpoint actually hit, resolved model, fallback/retry count).
func (d *Dispatcher) Forward(ctx context.Context, alias string, req *adapter.UnifiedRequest) (*adapter.UnifiedResponse, DispatchResult, error) {
	candidates, err := d.router.Candidates(alias, sessionKeyFrom(ctx), ingressProtocolFrom(ctx))
	if err != nil {
		return nil, DispatchResult{}, err
	}
	keys := d.expandCandidates(ctx, candidates)
	rid := middleware.GetReqID(ctx)
	var lastErr error
	configMismatches := 0
	for i, key := range keys {
		fwd, ok := d.forwarders[key]
		if !ok {
			lastErr = fmt.Errorf("proxy: no forwarder for %s", key)
			d.observeStep(ctx, rid, i, key, "skipped", "no_forwarder", "", "", "", 0)
			continue
		}
		preq, err := d.prepare(req, key)
		if err != nil {
			lastErr = err // provider doesn't serve this alias; try the next
			configMismatches++
			d.observeStep(ctx, rid, i, key, "skipped", "prepare_failed", "", "", "", 0)
			continue
		}
		start := time.Now()
		resp, err := fwd.Forward(ctx, preq)
		dur := int(time.Since(start).Milliseconds())
		if err == nil {
			d.breaker.MarkSuccess(key)
			d.observeStep(ctx, rid, i, key, "attempted", "", "selected", "", resp.UpstreamRequestID, dur)
			return resp, DispatchResult{
				Provider: key.Provider, Endpoint: key.Endpoint, ModelResolved: preq.Model,
				Fallback: i > 0, RetryCount: i,
				UpstreamRequestID: resp.UpstreamRequestID,
			}, nil
		}
		lastErr = err
		if !retryable(err) {
			// non-retryable (e.g. 4xx): stop, don't fail over. This attempt did
			// resolve a model on this provider even though forwarding failed.
			d.observeStep(ctx, rid, i, key, "attempted", "", "terminal_failure", errorClass(err), "", dur)
			return nil, DispatchResult{Provider: key.Provider, Endpoint: key.Endpoint, ModelResolved: preq.Model, Fallback: i > 0, RetryCount: i}, err
		}
		d.breaker.MarkFailure(key)
		d.observeStep(ctx, rid, i, key, "attempted", "", "retryable_failure", errorClass(err), "", dur)
	}
	return nil, DispatchResult{RetryCount: len(keys)}, failoverExhausted(lastErr, configMismatches, len(keys))
}

// ForwardStream routes a streaming request and fails over across candidates.
// Per ADR-0011, failover only happens before the first byte: opening the stream
// (ForwardStream) succeeds only after the upstream status is checked and before
// any byte is relayed, so retrying the open across candidates is the boundary.
// Once a reader is returned, that provider endpoint is locked.
func (d *Dispatcher) ForwardStream(ctx context.Context, alias string, req *adapter.UnifiedRequest) (adapter.StreamReader, DispatchResult, error) {
	candidates, err := d.router.Candidates(alias, sessionKeyFrom(ctx), ingressProtocolFrom(ctx))
	if err != nil {
		return nil, DispatchResult{}, err
	}
	keys := d.expandCandidates(ctx, candidates)
	rid := middleware.GetReqID(ctx)
	var lastErr error
	configMismatches := 0
	for i, key := range keys {
		fwd, ok := d.forwarders[key]
		if !ok {
			lastErr = fmt.Errorf("proxy: no forwarder for %s", key)
			d.observeStep(ctx, rid, i, key, "skipped", "no_forwarder", "", "", "", 0)
			continue
		}
		preq, err := d.prepare(req, key)
		if err != nil {
			lastErr = err
			configMismatches++
			d.observeStep(ctx, rid, i, key, "skipped", "prepare_failed", "", "", "", 0)
			continue
		}
		start := time.Now()
		sr, upstreamID, err := fwd.ForwardStream(ctx, preq)
		dur := int(time.Since(start).Milliseconds())
		if err == nil {
			d.breaker.MarkSuccess(key)
			d.observeStep(ctx, rid, i, key, "attempted", "", "selected", "", upstreamID, dur)
			return sr, DispatchResult{Provider: key.Provider, Endpoint: key.Endpoint, ModelResolved: preq.Model, Fallback: i > 0, RetryCount: i, UpstreamRequestID: upstreamID}, nil
		}
		lastErr = err
		if !retryable(err) {
			d.observeStep(ctx, rid, i, key, "attempted", "", "terminal_failure", errorClass(err), "", dur)
			return nil, DispatchResult{Provider: key.Provider, Endpoint: key.Endpoint, ModelResolved: preq.Model, Fallback: i > 0, RetryCount: i}, err
		}
		d.breaker.MarkFailure(key)
		d.observeStep(ctx, rid, i, key, "attempted", "", "retryable_failure", errorClass(err), "", dur)
	}
	return nil, DispatchResult{RetryCount: len(keys)}, failoverExhausted(lastErr, configMismatches, len(keys))
}

// observeStep publishes a DispatchStepEvent to the observer (if installed).
// Nil observer = zero overhead. Fail-open: panics in the observer are
// recovered so they can never break the dispatch path. ADR-0051.
func (d *Dispatcher) observeStep(ctx context.Context, requestID string, ordinal int, key EndpointKey, action, skipReason, outcome, errType, upstreamID string, durMs int) {
	if d.observer == nil {
		return
	}
	defer func() { _ = recover() }() // ADR-0051: observer must never break dispatch
	d.observer.OnDispatchStep(ctx, requestID, DispatchStepEvent{
		Ordinal:           ordinal,
		Provider:          key.Provider,
		Endpoint:          key.Endpoint,
		Action:            action,
		SkipReason:        skipReason,
		SelectionOutcome:  outcome,
		ErrorType:         errType,
		UpstreamRequestID: upstreamID,
		DurationMs:        durMs,
	})
}

// errorClass extracts a short error classification from a forwarding error for
// DispatchStepEvent.ErrorType. Mirrors the upstreamError.kind taxonomy.
func errorClass(err error) string {
	var ue *upstreamError
	if errors.As(err, &ue) {
		switch ue.kind {
		case errTimeout:
			return "timeout"
		case errUpstream4xx:
			return "upstream_4xx"
		case errUpstream5xx:
			return "upstream_5xx"
		case errBuild:
			return "build_error"
		}
	}
	return "unknown"
}

// retryable reports whether a forwarding error is eligible for failover.
func retryable(err error) bool {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return ue.Retryable()
	}
	return false
}

// BreakerStates returns a snapshot of circuit breaker states keyed by provider
// name. Used by the heartbeat goroutine to report per-instance breaker health
// to the admin plane for the B2' multi-instance visibility plan.
func (d *Dispatcher) BreakerStates() map[string]string {
	return d.breaker.States()
}

// failoverExhausted builds the error returned when every candidate has been
// tried without success. When every candidate failed at the prepare step (the
// provider does not serve the requested model), the error calls out a
// "configuration mismatch" so operators can distinguish a routing
// misconfiguration (Route.Providers ⊄ Model.Upstreams) from a genuine upstream
// outage.
func failoverExhausted(last error, configMismatches, total int) error {
	if last == nil {
		return errors.New("proxy: no candidate providers")
	}
	if configMismatches == total && total > 0 {
		return fmt.Errorf("proxy: configuration mismatch -- no candidate provider serves this model (check Route.Providers ⊆ Model.Upstreams): %w", last)
	}
	return fmt.Errorf("proxy: all providers failed: %w", last)
}

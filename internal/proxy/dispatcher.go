package proxy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
	"voxeltoad/internal/ingress"
)

// DispatcherConfig configures failover behavior.
type DispatcherConfig struct {
	// FailureThreshold and Cooldown configure the circuit breaker (ADR-0011).
	FailureThreshold int
	Cooldown         time.Duration
}

// Dispatcher orchestrates routing and failover above the single-provider
// Forwarders (ADR-0011): it resolves a model alias to ordered candidates, tries
// each in turn (retrying only retryable failures), tracks per-provider circuit
// health, and reports the provider actually hit (for billing / llm.provider).
type Dispatcher struct {
	router     *router
	breaker    *circuitBreaker
	forwarders map[string]*Forwarder
	preparer   *modelPreparer // optional; nil = pass the request through unchanged
}

// NewDispatcher builds a Dispatcher from routes, a provider→Forwarder map, and
// failover config. The forwarder map is rebuilt on config change (P0: built
// once at wiring).
func NewDispatcher(routes []config.Route, forwarders map[string]*Forwarder, cfg DispatcherConfig) *Dispatcher {
	breaker := newCircuitBreaker(circuitConfig(cfg))
	rnd := rand.New(rand.NewSource(rand.Int63())) //nolint:gosec // load-balancing, not security
	r := newRouterWithRand(routes, breaker, func(n int) int {
		if n <= 0 {
			return 0
		}
		return rnd.Intn(n)
	})
	return &Dispatcher{router: r, breaker: breaker, forwarders: forwarders}
}

// NewSingleProviderDispatcher builds a Dispatcher that always forwards to one
// provider with no routing or failover. It is the simplest deployment shape
// (one upstream) and is convenient for tests. Any model alias resolves to this
// single provider.
func NewSingleProviderDispatcher(fwd *Forwarder) *Dispatcher {
	const only = "default"
	breaker := newCircuitBreaker(circuitConfig{})
	r := newRouterWithRand(
		[]config.Route{{ModelAlias: wildcardAlias, Providers: []config.RouteProvider{{Name: only}}}},
		breaker,
		func(int) int { return 0 },
	)
	return &Dispatcher{router: r, breaker: breaker, forwarders: map[string]*Forwarder{only: fwd}}
}

// wildcardAlias is the route key matched when no exact route exists (single
// provider mode).
const wildcardAlias = "*"

// WithModelPreparation enables per-candidate request preparation (alias →
// provider-native upstream model + protocol normalization, ADR-0002/0009) using
// the given dynamic config. Without it, the Dispatcher forwards the request
// as-is (used in tests that target a single mock upstream).
func (d *Dispatcher) WithModelPreparation(dyn *config.Dynamic) *Dispatcher {
	d.preparer = newModelPreparer(dyn)
	return d
}

// prepare returns the request to send to the named provider: prepared
// (resolved + normalized) when a preparer is configured, else the original.
func (d *Dispatcher) prepare(req *adapter.UnifiedRequest, provider string) (*adapter.UnifiedRequest, error) {
	if d.preparer == nil {
		return req, nil
	}
	return d.preparer.Prepare(req, provider)
}

// DispatchResult carries the routing-layer facts an emit()/billing caller needs
// beyond the response body itself: which provider was actually hit, what
// upstream model name was resolved to (ADR-0002; equals the requested alias
// when no preparer is configured), whether a fallback across candidates
// occurred, and how many candidates were tried before the outcome (success or
// final failure). These mirror the mandatory llm.model.resolved / llm.fallback
// / llm.retry.count fields (design/observability.md).
type DispatchResult struct {
	Provider      string
	ModelResolved string
	Fallback      bool
	RetryCount    int
	// UpstreamRequestID is the provider-assigned id from the successful
	// (final) attempt's response. Empty on failure or when the provider
	// returned no id. Captured for support/reconciliation (ADR-0021 §5).
	UpstreamRequestID string
}

// Forward routes a non-streaming request and fails over across candidates on
// retryable errors. It returns the unified response and routing-layer result
// facts (provider actually hit, resolved model, fallback/retry count).
func (d *Dispatcher) Forward(ctx context.Context, alias string, req *adapter.UnifiedRequest) (*adapter.UnifiedResponse, DispatchResult, error) {
	candidates, err := d.router.Candidates(alias, sessionKeyFrom(ctx))
	if err != nil {
		return nil, DispatchResult{}, err
	}
	candidates = d.preferProtocol(ctx, candidates)
	var lastErr error
	configMismatches := 0
	for i, name := range candidates {
		fwd, ok := d.forwarders[name]
		if !ok {
			lastErr = fmt.Errorf("proxy: no forwarder for provider %q", name)
			continue
		}
		preq, err := d.prepare(req, name)
		if err != nil {
			lastErr = err // provider doesn't serve this alias; try the next
			configMismatches++
			continue
		}
		resp, err := fwd.Forward(ctx, preq)
		if err == nil {
			d.breaker.MarkSuccess(name)
			return resp, DispatchResult{
				Provider: name, ModelResolved: preq.Model,
				Fallback: i > 0, RetryCount: i,
				UpstreamRequestID: resp.UpstreamRequestID,
			}, nil
		}
		lastErr = err
		if !retryable(err) {
			// non-retryable (e.g. 4xx): stop, don't fail over. This attempt did
			// resolve a model on this provider even though forwarding failed.
			return nil, DispatchResult{Provider: name, ModelResolved: preq.Model, Fallback: i > 0, RetryCount: i}, err
		}
		d.breaker.MarkFailure(name)
	}
	return nil, DispatchResult{RetryCount: len(candidates)}, failoverExhausted(lastErr, configMismatches, len(candidates))
}

// ForwardStream routes a streaming request and fails over across candidates.
// Per ADR-0011, failover only happens before the first byte: opening the stream
// (ForwardStream) succeeds only after the upstream status is checked and before
// any byte is relayed, so retrying the open across candidates is the boundary.
// Once a reader is returned, that provider is locked.
func (d *Dispatcher) ForwardStream(ctx context.Context, alias string, req *adapter.UnifiedRequest) (adapter.StreamReader, DispatchResult, error) {
	candidates, err := d.router.Candidates(alias, sessionKeyFrom(ctx))
	if err != nil {
		return nil, DispatchResult{}, err
	}
	candidates = d.preferProtocol(ctx, candidates)
	var lastErr error
	configMismatches := 0
	for i, name := range candidates {
		fwd, ok := d.forwarders[name]
		if !ok {
			lastErr = fmt.Errorf("proxy: no forwarder for provider %q", name)
			continue
		}
		preq, err := d.prepare(req, name)
		if err != nil {
			lastErr = err
			configMismatches++
			continue
		}
		sr, upstreamID, err := fwd.ForwardStream(ctx, preq)
		if err == nil {
			d.breaker.MarkSuccess(name)
			return sr, DispatchResult{Provider: name, ModelResolved: preq.Model, Fallback: i > 0, RetryCount: i, UpstreamRequestID: upstreamID}, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, DispatchResult{Provider: name, ModelResolved: preq.Model, Fallback: i > 0, RetryCount: i}, err
		}
		d.breaker.MarkFailure(name)
	}
	return nil, DispatchResult{RetryCount: len(candidates)}, failoverExhausted(lastErr, configMismatches, len(candidates))
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

// preferProtocol stably partitions candidates so that providers whose adapter
// matches the request's ingress protocol come first, preserving the strategy
// order within each group. This makes protocol-matched providers the primary
// path (passthrough — codec Raw-priority applies) and others the failover path
// (translated — codec re-encodes). See ADR-0047.
//
// No-op when:
//   - the context carries no ingress protocol (unknown / single-provider test
//     mode), or
//   - the dispatcher has no preparer (NewSingleProviderDispatcher, no
//     adapterByPvd map available).
func (d *Dispatcher) preferProtocol(ctx context.Context, names []string) []string {
	protocol := ingressProtocolFrom(ctx)
	if protocol == "" || d.preparer == nil {
		return names
	}
	want := ingress.Protocol(protocol).AdapterName()
	matched := make([]string, 0, len(names))
	others := make([]string, 0, len(names))
	for _, n := range names {
		if d.preparer.adapterByPvd[n] == want {
			matched = append(matched, n)
		} else {
			others = append(others, n)
		}
	}
	return append(matched, others...)
}

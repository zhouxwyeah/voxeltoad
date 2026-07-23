package proxy

import (
	"fmt"
	"sync"

	"voxeltoad/internal/config"
)

// router resolves a model alias to an ordered list of candidate provider names
// for failover, applying the route's strategy and filtering out providers whose
// endpoint (for this request's ingress protocol) the circuit breaker considers
// unhealthy (ADR-0011/0049). It is separate from the HTTP Router in router.go.
type router struct {
	breaker *circuitBreaker
	// randn returns a pseudo-random int in [0, n); injectable for tests.
	randn func(n int) int
	// endpointFor resolves which endpoint of a provider a request under the
	// given ingress protocol would use (ADR-0049 endpoint-grain health check).
	// Injected at construction so the router stays decoupled from the preparer.
	// Returns (endpointID, adapterName).
	endpointFor func(provider, ingressProtocol string) (endpointID, adapter string)

	mu      sync.Mutex
	routes  map[string]config.Route
	rrIndex map[string]int // round-robin cursor per alias
}

// newRouterWithRand builds a router with an injectable randomness source and
// endpoint resolver. endpointFor may be nil (single-provider tests): the router
// then treats every provider as a single default endpoint.
func newRouterWithRand(routes []config.Route, b *circuitBreaker, randn func(int) int, endpointFor func(provider, ingressProtocol string) (string, string)) *router {
	m := make(map[string]config.Route, len(routes))
	for _, rt := range routes {
		m[rt.ModelAlias] = rt
	}
	if endpointFor == nil {
		endpointFor = func(provider, _ string) (string, string) { return "default", "" }
	}
	return &router{
		breaker:     b,
		randn:       randn,
		endpointFor: endpointFor,
		routes:      m,
		rrIndex:     make(map[string]int),
	}
}

// Candidates returns the ordered provider names to try for alias, given an
// optional sessionKey (used only by the "session_affinity" strategy; ignored by
// others) and the client's ingress protocol (used to evaluate per-endpoint
// health in the multi-endpoint model, ADR-0049). Unhealthy endpoints are
// dropped; if that leaves none, the full ordered set is returned as a degraded
// fallback (better to try a likely-bad provider than not at all).
func (r *router) Candidates(alias, sessionKey, ingressProtocol string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	route, ok := r.routes[alias]
	if !ok {
		// Fall back to a wildcard route if configured (single-provider mode).
		route, ok = r.routes[wildcardAlias]
		if !ok {
			return nil, fmt.Errorf("proxy: no route for model %q", alias)
		}
	}

	var ordered []config.RouteProvider
	switch route.Strategy {
	case "round_robin":
		ordered = r.rotate(alias, route.Providers)
	case "weighted":
		ordered = r.weightedOrder(route.Providers)
	case "session_affinity":
		// Same session → same provider (deterministic HRW order), so its shared
		// prefix stays in that provider's prompt cache (ADR-0018). The full HRW
		// ordering also fixes the failover order per session.
		ordered = hrwOrder(sessionKey, route.Providers)
	default: // "priority" and unknown → config order
		ordered = route.Providers
	}

	names := make([]string, 0, len(ordered))
	for _, p := range ordered {
		names = append(names, p.Name)
	}
	return r.filterHealthy(names, ingressProtocol), nil
}

// filterHealthy drops providers whose endpoint (the one this request would use
// under ingressProtocol) is breaker-unhealthy, but returns the full list if all
// are unhealthy (degraded fallback). endpointFor resolves which endpoint a
// provider serves this request over; it is injected so the router stays
// decoupled from the preparer.
func (r *router) filterHealthy(names []string, ingressProtocol string) []string {
	healthy := make([]string, 0, len(names))
	for _, n := range names {
		epID, _ := r.endpointFor(n, ingressProtocol)
		if r.breaker.Healthy(EndpointKey{Provider: n, Endpoint: epID}) {
			healthy = append(healthy, n)
		}
	}
	if len(healthy) == 0 {
		return names
	}
	return healthy
}

// rotate returns the providers rotated by a per-alias cursor that advances each
// call, so successive requests start at different providers.
func (r *router) rotate(alias string, ps []config.RouteProvider) []config.RouteProvider {
	if len(ps) == 0 {
		return ps
	}
	start := r.rrIndex[alias] % len(ps)
	r.rrIndex[alias] = (r.rrIndex[alias] + 1) % len(ps)
	out := make([]config.RouteProvider, 0, len(ps))
	out = append(out, ps[start:]...)
	out = append(out, ps[:start]...)
	return out
}

// weightedOrder produces a full ordering by weighted random selection without
// replacement: pick a head proportional to weight, then repeat on the rest.
// Zero weights are treated as weight 1 so every provider participates.
func (r *router) weightedOrder(ps []config.RouteProvider) []config.RouteProvider {
	remaining := make([]config.RouteProvider, len(ps))
	copy(remaining, ps)

	out := make([]config.RouteProvider, 0, len(ps))
	for len(remaining) > 0 {
		total := 0
		for _, p := range remaining {
			total += weightOf(p)
		}
		pick := r.randn(total)
		idx := 0
		cum := 0
		for i, p := range remaining {
			cum += weightOf(p)
			if pick < cum {
				idx = i
				break
			}
		}
		out = append(out, remaining[idx])
		remaining = append(remaining[:idx], remaining[idx+1:]...)
	}
	return out
}

func weightOf(p config.RouteProvider) int {
	if p.Weight <= 0 {
		return 1
	}
	return p.Weight
}

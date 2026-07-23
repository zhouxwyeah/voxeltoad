package proxy

import (
	"reflect"
	"testing"
	"time"

	"voxeltoad/internal/config"
)

func rp(name string, weight int) config.RouteProvider {
	return config.RouteProvider{Name: name, Weight: weight}
}

func newRouter(routes []config.Route, b *circuitBreaker) *router {
	return newRouterWithRand(routes, b, func(int) int { return 0 }, nil)
}

func TestRouter_UnknownAliasErrors(t *testing.T) {
	r := newRouter(nil, newCircuitBreaker(circuitConfig{}))
	if _, err := r.Candidates("nope", "", ""); err == nil {
		t.Error("unknown alias should error")
	}
}

func TestRouter_Priority_ConfigOrder(t *testing.T) {
	routes := []config.Route{{
		ModelAlias: "chat",
		Strategy:   "priority",
		Providers:  []config.RouteProvider{rp("a", 0), rp("b", 0), rp("c", 0)},
	}}
	r := newRouter(routes, newCircuitBreaker(circuitConfig{}))
	got, err := r.Candidates("chat", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v, want [a b c]", got)
	}
}

func TestRouter_FiltersUnhealthy(t *testing.T) {
	routes := []config.Route{{
		ModelAlias: "chat",
		Strategy:   "priority",
		Providers:  []config.RouteProvider{rp("a", 0), rp("b", 0), rp("c", 0)},
	}}
	b := newCircuitBreaker(circuitConfig{FailureThreshold: 1, Cooldown: time.Hour})
	b.MarkFailure(ep("b")) // trip b open
	r := newRouter(routes, b)

	got, _ := r.Candidates("chat", "", "")
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("got %v, want [a c] (b filtered)", got)
	}
}

// TestRouter_AllUnhealthy_ReturnsAll: if every candidate is unhealthy, return
// the full list anyway (degraded mode) rather than nothing — better to try a
// likely-bad provider than to fail with no attempt.
func TestRouter_AllUnhealthy_ReturnsAll(t *testing.T) {
	routes := []config.Route{{
		ModelAlias: "chat",
		Strategy:   "priority",
		Providers:  []config.RouteProvider{rp("a", 0), rp("b", 0)},
	}}
	b := newCircuitBreaker(circuitConfig{FailureThreshold: 1, Cooldown: time.Hour})
	b.MarkFailure(ep("a"))
	b.MarkFailure(ep("b"))
	r := newRouter(routes, b)

	got, _ := r.Candidates("chat", "", "")
	if len(got) != 2 {
		t.Errorf("got %v, want both as a degraded fallback", got)
	}
}

func TestRouter_RoundRobin_RotatesAcrossCalls(t *testing.T) {
	routes := []config.Route{{
		ModelAlias: "chat",
		Strategy:   "round_robin",
		Providers:  []config.RouteProvider{rp("a", 0), rp("b", 0), rp("c", 0)},
	}}
	r := newRouter(routes, newCircuitBreaker(circuitConfig{}))

	first, _ := r.Candidates("chat", "", "")
	second, _ := r.Candidates("chat", "", "")
	third, _ := r.Candidates("chat", "", "")

	if first[0] != "a" || second[0] != "b" || third[0] != "c" {
		t.Errorf("round-robin heads = %q/%q/%q, want a/b/c", first[0], second[0], third[0])
	}
	// Each result is a full rotation of the 3 providers.
	if !reflect.DeepEqual(second, []string{"b", "c", "a"}) {
		t.Errorf("second = %v, want [b c a]", second)
	}
}

// TestRouter_Weighted_PicksByWeight: with the injected rand returning 0, the
// weighted head is the first provider whose cumulative weight covers 0 — i.e.
// the first listed. We assert the ordering is a stable, weight-aware permutation
// and that all providers are present.
func TestRouter_Weighted_DeterministicWithInjectedRand(t *testing.T) {
	routes := []config.Route{{
		ModelAlias: "chat",
		Strategy:   "weighted",
		Providers:  []config.RouteProvider{rp("a", 1), rp("b", 9)},
	}}
	// rand returns a value landing in b's weight band (e.g. pick = 5 of total 10).
	r := newRouterWithRand(routes, newCircuitBreaker(circuitConfig{}), func(n int) int {
		if n == 10 {
			return 5 // 5 >= a's 1 → falls in b
		}
		return 0
	}, nil)
	got, _ := r.Candidates("chat", "", "")
	if got[0] != "b" {
		t.Errorf("weighted head = %q, want b (rand landed in b's band)", got[0])
	}
	if len(got) != 2 {
		t.Errorf("got %v, want both providers present", got)
	}
}

func TestRouter_Weighted_ZeroWeightsTreatedAsEqual(t *testing.T) {
	routes := []config.Route{{
		ModelAlias: "chat",
		Strategy:   "weighted",
		Providers:  []config.RouteProvider{rp("a", 0), rp("b", 0)},
	}}
	r := newRouter(routes, newCircuitBreaker(circuitConfig{})) // rand → 0
	got, err := r.Candidates("chat", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want both", got)
	}
}

func TestRouter_UnknownStrategy_DefaultsToPriority(t *testing.T) {
	routes := []config.Route{{
		ModelAlias: "chat",
		Strategy:   "",
		Providers:  []config.RouteProvider{rp("a", 0), rp("b", 0)},
	}}
	r := newRouter(routes, newCircuitBreaker(circuitConfig{}))
	got, _ := r.Candidates("chat", "", "")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %v, want config order [a b]", got)
	}
}

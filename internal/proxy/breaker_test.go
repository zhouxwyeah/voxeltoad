package proxy

import (
	"testing"
	"time"
)

func newTestBreaker() (*circuitBreaker, *time.Time) {
	now := time.Unix(1_000_000, 0)
	b := newCircuitBreaker(circuitConfig{FailureThreshold: 3, Cooldown: 30 * time.Second})
	b.now = func() time.Time { return now }
	return b, &now
}

// ep is a shorthand for a single-endpoint key in breaker/router tests. The
// router's nil-endpointFor fallback resolves to endpoint "default", so test
// breaker keys must match that.
func ep(provider string) EndpointKey { return EndpointKey{Provider: provider, Endpoint: "default"} }

func TestBreaker_HealthyByDefault(t *testing.T) {
	b, _ := newTestBreaker()
	if !b.Healthy(ep("p1")) {
		t.Error("unknown provider should be healthy by default")
	}
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	b, _ := newTestBreaker()
	b.MarkFailure(ep("p1"))
	b.MarkFailure(ep("p1"))
	if !b.Healthy(ep("p1")) {
		t.Error("should stay healthy below threshold (2 < 3)")
	}
	b.MarkFailure(ep("p1"))
	if b.Healthy(ep("p1")) {
		t.Error("should open after reaching threshold (3)")
	}
}

func TestBreaker_SuccessResetsFailures(t *testing.T) {
	b, _ := newTestBreaker()
	b.MarkFailure(ep("p1"))
	b.MarkFailure(ep("p1"))
	b.MarkSuccess(ep("p1")) // reset
	b.MarkFailure(ep("p1"))
	b.MarkFailure(ep("p1"))
	if !b.Healthy(ep("p1")) {
		t.Error("success should have reset the failure count; 2 < 3 again")
	}
}

func TestBreaker_HalfOpenAfterCooldown(t *testing.T) {
	b, now := newTestBreaker()
	b.MarkFailure(ep("p1"))
	b.MarkFailure(ep("p1"))
	b.MarkFailure(ep("p1"))
	if b.Healthy(ep("p1")) {
		t.Fatal("should be open right after tripping")
	}
	// Still within cooldown.
	*now = now.Add(20 * time.Second)
	b.now = func() time.Time { return *now }
	if b.Healthy(ep("p1")) {
		t.Error("should remain open within cooldown")
	}
	// Past cooldown → half-open (allowed to try again).
	*now = now.Add(11 * time.Second) // total 31s > 30s
	b.now = func() time.Time { return *now }
	if !b.Healthy(ep("p1")) {
		t.Error("should be half-open (healthy) after cooldown")
	}
}

// TestBreaker_HalfOpenFailureReopens: a failure during half-open trips it again
// for another cooldown (does not require re-reaching the full threshold).
func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b, now := newTestBreaker()
	for i := 0; i < 3; i++ {
		b.MarkFailure(ep("p1"))
	}
	*now = now.Add(31 * time.Second)
	b.now = func() time.Time { return *now }
	if !b.Healthy(ep("p1")) {
		t.Fatal("should be half-open after cooldown")
	}
	// Fail during half-open.
	b.MarkFailure(ep("p1"))
	if b.Healthy(ep("p1")) {
		t.Error("a failure during half-open should re-open the breaker")
	}
}

// TestBreaker_HalfOpenSuccessCloses: a success during half-open fully closes the
// breaker and clears state.
func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	b, now := newTestBreaker()
	for i := 0; i < 3; i++ {
		b.MarkFailure(ep("p1"))
	}
	*now = now.Add(31 * time.Second)
	b.now = func() time.Time { return *now }
	b.MarkSuccess(ep("p1"))
	if !b.Healthy(ep("p1")) {
		t.Error("success during half-open should close the breaker")
	}
	// And it should take another full threshold to trip again.
	b.MarkFailure(ep("p1"))
	b.MarkFailure(ep("p1"))
	if !b.Healthy(ep("p1")) {
		t.Error("after closing, 2 failures (< threshold) must not re-open")
	}
}

func TestBreaker_IndependentPerProvider(t *testing.T) {
	b, _ := newTestBreaker()
	for i := 0; i < 3; i++ {
		b.MarkFailure(ep("p1"))
	}
	if b.Healthy(ep("p1")) {
		t.Fatal("p1 should be open")
	}
	if !b.Healthy(ep("p2")) {
		t.Error("p2 must be unaffected by p1's failures")
	}
}

// TestBreaker_EndpointIsolation verifies the multi-endpoint breaker key
// (ADR-0049): the breaker is keyed by (provider, endpoint), so a provider's
// openai endpoint can be tripped while its anthropic endpoint stays healthy.
// This is the isolation that lets a dual-protocol vendor degrade one protocol
// without taking down the other.
func TestBreaker_EndpointIsolation(t *testing.T) {
	b, _ := newTestBreaker()
	openaiEP := EndpointKey{Provider: "p1", Endpoint: "openai"}
	anthropicEP := EndpointKey{Provider: "p1", Endpoint: "anthropic"}

	for i := 0; i < 5; i++ {
		b.MarkFailure(openaiEP)
	}
	if b.Healthy(openaiEP) {
		t.Fatal("p1/openai should be open after threshold failures")
	}
	if !b.Healthy(anthropicEP) {
		t.Error("p1/anthropic must stay healthy when p1/openai trips (endpoint isolation)")
	}
}

// TestBreaker_StatesKeyedByProviderEndpoint verifies States() serializes
// breaker keys as "<provider>/<endpoint>" so admin/heartbeat views can show
// per-endpoint health distinctly.
func TestBreaker_StatesKeyedByProviderEndpoint(t *testing.T) {
	b, _ := newTestBreaker()
	b.MarkFailure(EndpointKey{Provider: "p1", Endpoint: "openai"})
	b.MarkFailure(EndpointKey{Provider: "p1", Endpoint: "openai"})
	b.MarkFailure(EndpointKey{Provider: "p2", Endpoint: "anthropic"})

	states := b.States()
	if _, ok := states["p1/openai"]; !ok {
		t.Errorf("States() missing key 'p1/openai': %v", states)
	}
	if _, ok := states["p2/anthropic"]; !ok {
		t.Errorf("States() missing key 'p2/anthropic': %v", states)
	}
}

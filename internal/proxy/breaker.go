package proxy

import (
	"sync"
	"time"
)

// circuitConfig configures the breaker.
type circuitConfig struct {
	// FailureThreshold is the number of consecutive failures that trips the
	// breaker open.
	FailureThreshold int
	// Cooldown is how long the breaker stays open before allowing a half-open
	// trial.
	Cooldown time.Duration
}

// breakerState is one provider's failure state.
type breakerState struct {
	failures int
	openedAt time.Time // zero = closed (not open)
}

// circuitBreaker tracks per-provider health for failover (ADR-0011). It is a
// classic closed → open → half-open breaker:
//   - closed: consecutive failures below threshold; healthy.
//   - open: threshold reached; unhealthy until the cooldown elapses.
//   - half-open: cooldown elapsed; healthy (one trial allowed) — a failure
//     re-opens it, a success closes it.
//
// In-memory and therefore per-data-plane-instance in P0 (documented gap, see
// ADR-0008/0011); a shared store is the multi-instance upgrade.
type circuitBreaker struct {
	cfg circuitConfig

	mu     sync.Mutex
	states map[string]*breakerState
	now    func() time.Time // injectable for tests
}

// newCircuitBreaker builds a breaker, applying sensible defaults (5 consecutive
// failures to trip, 30s cooldown) when the config leaves them unset.
func newCircuitBreaker(cfg circuitConfig) *circuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	return &circuitBreaker{
		cfg:    cfg,
		states: make(map[string]*breakerState),
		now:    time.Now,
	}
}

// Healthy reports whether the provider may currently be tried. Unknown
// providers are healthy. An open breaker becomes healthy again (half-open) once
// the cooldown has elapsed.
func (b *circuitBreaker) Healthy(provider string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.states[provider]
	if !ok || s.openedAt.IsZero() {
		return true // closed
	}
	// Open: healthy only if the cooldown has elapsed (half-open trial).
	return b.now().Sub(s.openedAt) >= b.cfg.Cooldown
}

// MarkFailure records a failed attempt. It trips the breaker when consecutive
// failures reach the threshold, and re-opens it (restarting the cooldown) if a
// failure occurs while half-open.
func (b *circuitBreaker) MarkFailure(provider string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stateFor(provider)

	if !s.openedAt.IsZero() {
		// Currently open or half-open. If half-open (cooldown elapsed), a
		// failure restarts the cooldown.
		if b.now().Sub(s.openedAt) >= b.cfg.Cooldown {
			s.openedAt = b.now()
		}
		return
	}

	s.failures++
	if s.failures >= b.cfg.FailureThreshold {
		s.openedAt = b.now()
	}
}

// MarkSuccess records a successful attempt, fully closing the breaker and
// clearing failure state (covers both closed-reset and half-open-close).
func (b *circuitBreaker) MarkSuccess(provider string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stateFor(provider)
	s.failures = 0
	s.openedAt = time.Time{}
}

func (b *circuitBreaker) stateFor(provider string) *breakerState {
	s, ok := b.states[provider]
	if !ok {
		s = &breakerState{}
		b.states[provider] = s
	}
	return s
}

// States returns a snapshot of all known circuit breaker states keyed by
// provider name. Values: "closed" | "open" | "half-open". Thread-safe.
func (b *circuitBreaker) States() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]string, len(b.states))
	now := b.now()
	for name, s := range b.states {
		if s.openedAt.IsZero() {
			out[name] = "closed"
		} else if now.Sub(s.openedAt) < b.cfg.Cooldown {
			out[name] = "open"
		} else {
			out[name] = "half-open"
		}
	}
	return out
}

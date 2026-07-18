package ratelimit

import (
	"context"
	"testing"
	"time"
)

// fixedClock is an injectable clock for deterministic window tests.
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter() (*MemoryLimiter, *fixedClock) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	l := NewMemoryLimiter()
	l.now = clk.now
	return l, clk
}

// hasScope reports whether a counter exists for the given RPM scope.
func hasScope(l *MemoryLimiter, scope string) bool {
	return l.hasCounter(dim(scope, RPM, 0, 0).key())
}

// dim builds a single dimension for tests.
func dim(scope string, metric Metric, limit int, window time.Duration) Dimension {
	return Dimension{Scope: scope, Metric: metric, Limit: limit, Window: window}
}

func TestAllow_WithinLimitPasses(t *testing.T) {
	l, _ := newTestLimiter()
	ctx := context.Background()
	d := []Dimension{dim("tenant:a", RPM, 3, time.Minute)}

	for i := 0; i < 3; i++ {
		dec, err := l.Allow(ctx, d, 1)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !dec.OK {
			t.Fatalf("request %d rejected, want allowed", i)
		}
	}
}

func TestAllow_OverLimitRejectedWithRetryAfter(t *testing.T) {
	l, _ := newTestLimiter()
	ctx := context.Background()
	d := []Dimension{dim("tenant:a", RPM, 2, time.Minute)}

	_, _ = l.Allow(ctx, d, 1)
	_, _ = l.Allow(ctx, d, 1)
	dec, err := l.Allow(ctx, d, 1)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if dec.OK {
		t.Fatal("3rd request should be rejected (limit 2)")
	}
	if dec.RetryAfter <= 0 || dec.RetryAfter > time.Minute {
		t.Errorf("RetryAfter = %v, want (0, 1m]", dec.RetryAfter)
	}
}

// TestAllow_SlidingWindowRecovers verifies window total semantics: as old
// events age out of the window, capacity returns (no token-bucket burst).
func TestAllow_SlidingWindowRecovers(t *testing.T) {
	l, clk := newTestLimiter()
	ctx := context.Background()
	d := []Dimension{dim("tenant:a", RPM, 2, time.Minute)}

	if dec, _ := l.Allow(ctx, d, 1); !dec.OK { // t=0
		t.Fatal("1st should pass")
	}
	clk.advance(30 * time.Second)
	if dec, _ := l.Allow(ctx, d, 1); !dec.OK { // t=30s
		t.Fatal("2nd should pass")
	}
	if dec, _ := l.Allow(ctx, d, 1); dec.OK { // t=30s, window has 2
		t.Fatal("3rd should be rejected")
	}
	// Advance so the first event (t=0) ages out; window now holds only the t=30s
	// event, leaving room for one more.
	clk.advance(31 * time.Second) // t=61s
	if dec, _ := l.Allow(ctx, d, 1); !dec.OK {
		t.Error("after first event aged out, a slot should be free")
	}
}

func TestAllow_MultiDimensional_StrictestWins(t *testing.T) {
	l, _ := newTestLimiter()
	ctx := context.Background()
	// tenant allows 10, but the key only allows 1.
	dims := []Dimension{
		dim("tenant:a", RPM, 10, time.Minute),
		dim("key:k1", RPM, 1, time.Minute),
	}
	if dec, _ := l.Allow(ctx, dims, 1); !dec.OK {
		t.Fatal("1st should pass")
	}
	if dec, _ := l.Allow(ctx, dims, 1); dec.OK {
		t.Error("2nd should be rejected by the stricter key dimension")
	}
}

// TestAllow_AtomicAcrossDimensions: if any dimension would be exceeded, NO
// dimension is charged (so a reject doesn't silently consume the tenant budget).
func TestAllow_AtomicAcrossDimensions(t *testing.T) {
	l, _ := newTestLimiter()
	ctx := context.Background()
	dims := []Dimension{
		dim("tenant:a", RPM, 5, time.Minute),
		dim("key:k1", RPM, 1, time.Minute),
	}
	_, _ = l.Allow(ctx, dims, 1) // ok: tenant=1, key=1
	if dec, _ := l.Allow(ctx, dims, 1); dec.OK {
		t.Fatal("2nd should be rejected by key limit")
	}
	// The rejected request must not have charged the tenant dimension: tenant
	// should still have 4 slots. Use a key dimension with room to prove it.
	dims2 := []Dimension{
		dim("tenant:a", RPM, 5, time.Minute),
		dim("key:k2", RPM, 5, time.Minute),
	}
	for i := 0; i < 4; i++ {
		if dec, _ := l.Allow(ctx, dims2, 1); !dec.OK {
			t.Fatalf("tenant slot %d rejected; rejected request leaked a charge", i)
		}
	}
	// 5th on tenant (total tenant would be 6) must now be rejected.
	if dec, _ := l.Allow(ctx, dims2, 1); dec.OK {
		t.Error("tenant should be exhausted at 5")
	}
}

// TestAllowThenDebit_TPM: ingress checks "already over?", actual usage debited
// after the response (ADR-0008). n at Allow is the pre-check cost (0 for TPM
// ingress = "only reject if already over"); Debit records real tokens.
func TestAllowThenDebit_TPM(t *testing.T) {
	l, _ := newTestLimiter()
	ctx := context.Background()
	d := []Dimension{dim("tenant:a", TPM, 100, time.Minute)}

	// Ingress with n=0: allowed because nothing consumed yet.
	if dec, _ := l.Allow(ctx, d, 0); !dec.OK {
		t.Fatal("ingress should pass when window empty")
	}
	// Response consumed 80 tokens — debit them.
	if err := l.Debit(ctx, d, 80); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	// Next ingress: still under 100, allowed (allow-then-debit overshoot model).
	if dec, _ := l.Allow(ctx, d, 0); !dec.OK {
		t.Fatal("ingress should pass while under limit")
	}
	l.Debit(ctx, d, 80) // now 160, over 100
	// Now ingress must reject because the window is already over the limit.
	if dec, _ := l.Allow(ctx, d, 0); dec.OK {
		t.Error("ingress should reject once window already over limit")
	}
}

func TestAllow_EmptyDimsAlwaysPasses(t *testing.T) {
	l, _ := newTestLimiter()
	if dec, err := l.Allow(context.Background(), nil, 1); err != nil || !dec.OK {
		t.Errorf("no dimensions should pass: dec=%+v err=%v", dec, err)
	}
}

// --- #1: counter eviction (LRU capacity + idle TTL) ---

// TestEviction_IdleTTLRemovesStaleCounters: a counter whose events have all
// aged out AND that has been idle past idleTTL is removed, so high-cardinality
// scopes (e.g. rotated/deleted keys) don't leak memory.
func TestEviction_IdleTTLRemovesStaleCounters(t *testing.T) {
	l, clk := newTestLimiter()
	l.idleTTL = 5 * time.Minute
	ctx := context.Background()
	d := []Dimension{dim("key:gone", RPM, 10, time.Minute)}

	if _, err := l.Allow(ctx, d, 1); err != nil {
		t.Fatal(err)
	}
	if l.numCounters() != 1 {
		t.Fatalf("counters = %d, want 1", l.numCounters())
	}

	// Idle long past both the window and the idle TTL, then touch an unrelated
	// scope to trigger sweep.
	clk.advance(10 * time.Minute)
	_, _ = l.Allow(ctx, []Dimension{dim("key:other", RPM, 10, time.Minute)}, 1)

	if hasScope(l, "key:gone") {
		t.Error("idle stale counter should have been evicted")
	}
}

// TestEviction_LRUCapacityBound: with a small capacity, the least-recently-used
// counter is evicted when a new one would exceed the cap.
func TestEviction_LRUCapacityBound(t *testing.T) {
	l, clk := newTestLimiter()
	l.maxCounters = 2
	l.idleTTL = time.Hour // disable TTL path; isolate LRU
	ctx := context.Background()
	mk := func(s string) []Dimension { return []Dimension{dim(s, RPM, 10, time.Minute)} }

	_, _ = l.Allow(ctx, mk("a"), 1)
	clk.advance(time.Second)
	_, _ = l.Allow(ctx, mk("b"), 1)
	clk.advance(time.Second)
	// Touch "a" so "b" becomes least-recently-used.
	_, _ = l.Allow(ctx, mk("a"), 1)
	clk.advance(time.Second)
	// Adding "c" exceeds cap(2) → evict LRU, which is "b".
	_, _ = l.Allow(ctx, mk("c"), 1)

	if l.numCounters() > 2 {
		t.Fatalf("counters = %d, want <= 2", l.numCounters())
	}
	if hasScope(l, "b") {
		t.Error("LRU counter b should have been evicted")
	}
	if !hasScope(l, "a") || !hasScope(l, "c") {
		t.Error("recently-used a and new c should remain")
	}
}

// TestRetryAfter_TPMAccountsForRequiredCapacity (#3): for TPM, RetryAfter must
// reflect when enough capacity for n frees up, not just when the single oldest
// event expires.
func TestRetryAfter_TPMAccountsForRequiredCapacity(t *testing.T) {
	l, clk := newTestLimiter()
	ctx := context.Background()
	d := []Dimension{dim("tenant:a", TPM, 100, time.Minute)}

	// Three debits of 40 at staggered times → sum 120 > 100.
	_ = l.Debit(ctx, d, 40) // t=0
	clk.advance(10 * time.Second)
	_ = l.Debit(ctx, d, 40) // t=10s
	clk.advance(10 * time.Second)
	_ = l.Debit(ctx, d, 40) // t=20s, sum=120

	// Need to fit n=30: 120+30=150, must drop 50. Dropping only the first event
	// (40) leaves 80 → still no room for 30 (80+30=110>100). Must also wait for
	// the second event. So RetryAfter should point to the SECOND event's expiry
	// (t=10s+60s=70s; now=20s ⇒ 50s), not the first (60s-20s=40s).
	dec, _ := l.Allow(ctx, d, 30)
	if dec.OK {
		t.Fatal("should be rejected: 120+30 > 100")
	}
	if dec.RetryAfter != 50*time.Second {
		t.Errorf("RetryAfter = %v, want 50s (until enough capacity for n=30)", dec.RetryAfter)
	}
}

// TestAllow_ChargesNAtomically (#4 bounded path): when n>0 is charged at Allow,
// concurrent callers cannot all slip through — the charge happens under the same
// lock as the check, so the window reflects each grant immediately.
func TestAllow_ChargesNExactly(t *testing.T) {
	l, _ := newTestLimiter()
	ctx := context.Background()
	d := []Dimension{dim("tenant:a", TPM, 100, time.Minute)}

	// Charging n=60 then n=60 at ingress: first ok (60<=100), second rejected
	// (60+60>100). This is the bounded alternative to allow-then-debit for
	// callers that know the cost up front.
	if dec, _ := l.Allow(ctx, d, 60); !dec.OK {
		t.Fatal("first 60 should pass")
	}
	if dec, _ := l.Allow(ctx, d, 60); dec.OK {
		t.Error("second 60 should be rejected (120 > 100)")
	}
}

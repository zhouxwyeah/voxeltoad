// Package ratelimit provides rate limiting behind a multi-dimensional interface
// (see ADR-0008). The default implementation is an in-memory sliding-window
// counter, which is correct for a single data-plane instance only: with N
// instances each enforces limits independently, so the effective limit is
// multiplied by N. A Redis-backed implementation is the cluster-correct upgrade
// behind the same interface.
//
// Algorithm: sliding window (window total), NOT a token bucket — it smooths load
// toward upstreams (no burst pass-through) and matches the "quota within a
// window" model. RPM charges n=1 per request; TPM uses allow-then-debit (ingress
// checks "already over?", real usage debited after the response).
//
// Memory is bounded by two evictions: an idle-TTL sweep drops counters that have
// been untouched and whose windows are empty, and an LRU cap evicts the
// least-recently-used counter when the number of counters would exceed
// maxCounters. This prevents unbounded growth from high-cardinality scopes such
// as rotated/deleted API keys.
package ratelimit

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// Default eviction bounds.
const (
	defaultMaxCounters = 100_000
	defaultIdleTTL     = 30 * time.Minute
)

// Metric identifies what a dimension counts.
type Metric string

const (
	// RPM limits requests per window.
	RPM Metric = "rpm"
	// TPM limits tokens per window (allow-then-debit).
	TPM Metric = "tpm"
)

// Dimension identifies one scope+metric+limit+window the limiter enforces, e.g.
// {Scope: "tenant:acme", Metric: TPM, Limit: 100000, Window: time.Minute}.
// Limits exist per tenant/group/key (ADR-0005); the caller builds the dimension
// list for a request.
type Dimension struct {
	Scope  string
	Metric Metric
	Limit  int
	Window time.Duration
}

// key uniquely identifies a dimension's counter. Metric and Scope are joined
// with a separator; scopes are controlled values (e.g. "tenant:x") that do not
// contain the separator.
func (d Dimension) key() string { return string(d.Metric) + "\x00" + d.Scope }

// Decision is the result of an Allow check.
type Decision struct {
	// OK is true if the request is within all dimensions' limits.
	OK bool
	// RetryAfter is a hint for the 429 Retry-After header; set when !OK.
	RetryAfter time.Duration
	// HitScope is the scope of the dimension that caused a rejection (for
	// observability); empty when OK.
	HitScope string
}

// Limiter enforces rate limits across one or more dimensions.
type Limiter interface {
	// Allow checks n units against all dims and, if all pass, charges them
	// atomically (no dimension is charged if any would be exceeded). For TPM
	// ingress checks, pass n=0 to mean "reject only if already over limit".
	Allow(ctx context.Context, dims []Dimension, n int) (Decision, error)
	// Debit records actual consumption after the fact (TPM allow-then-debit).
	Debit(ctx context.Context, dims []Dimension, n int) error
}

// event is a timestamped charge in a window.
type event struct {
	at   time.Time
	cost int
}

// window holds the recent events for one dimension counter, plus bookkeeping
// for eviction.
type window struct {
	events     []event
	sum        int
	lastAccess time.Time
	dur        time.Duration // last-seen window duration, for idle-sweep pruning
	elem       *list.Element // position in the LRU list; value is the counter key
}

// MemoryLimiter is an in-process sliding-window limiter. Single-instance only
// (see package doc / ADR-0008). Memory is bounded by idle-TTL and LRU eviction.
type MemoryLimiter struct {
	mu       sync.Mutex
	counters map[string]*window
	lru      *list.List // front = most recently used; values are keys (string)
	now      func() time.Time

	maxCounters int
	idleTTL     time.Duration
}

// NewMemoryLimiter builds an empty in-memory sliding-window limiter with default
// eviction bounds.
func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{
		counters:    make(map[string]*window),
		lru:         list.New(),
		now:         time.Now,
		maxCounters: defaultMaxCounters,
		idleTTL:     defaultIdleTTL,
	}
}

// Allow checks and (on success) charges n units against every dimension.
func (l *MemoryLimiter) Allow(_ context.Context, dims []Dimension, n int) (Decision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	// Resolve each dimension's window once (fixes the previous double-lookup),
	// pruning as we go, and check limits atomically.
	wins := make([]*window, len(dims))
	for i, d := range dims {
		w := l.touch(d.key(), now)
		w.dur = d.Window
		l.prune(w, now, d.Window)
		wins[i] = w
		if w.sum+n > d.Limit {
			return Decision{
				OK:         false,
				RetryAfter: retryAfter(w, now, d.Window, n, d.Limit),
				HitScope:   d.Scope,
			}, nil
		}
	}

	if n > 0 {
		for _, w := range wins {
			w.events = append(w.events, event{at: now, cost: n})
			w.sum += n
		}
	}
	l.evictIfNeeded(now)
	return Decision{OK: true}, nil
}

// Debit records actual consumption (e.g. real token usage) against dims.
func (l *MemoryLimiter) Debit(_ context.Context, dims []Dimension, n int) error {
	if n == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, d := range dims {
		w := l.touch(d.key(), now)
		w.dur = d.Window
		w.events = append(w.events, event{at: now, cost: n})
		w.sum += n
	}
	l.evictIfNeeded(now)
	return nil
}

// touch returns the window for key, creating it if absent, and marks it
// most-recently-used.
func (l *MemoryLimiter) touch(key string, now time.Time) *window {
	w, ok := l.counters[key]
	if !ok {
		w = &window{elem: l.lru.PushFront(key)}
		l.counters[key] = w
	} else {
		l.lru.MoveToFront(w.elem)
	}
	w.lastAccess = now
	return w
}

// prune drops events older than the window and recomputes the sum.
func (l *MemoryLimiter) prune(w *window, now time.Time, dur time.Duration) {
	cutoff := now.Add(-dur)
	i := 0
	for i < len(w.events) && !w.events[i].at.After(cutoff) {
		w.sum -= w.events[i].cost
		i++
	}
	if i > 0 {
		w.events = w.events[i:]
	}
}

// evictIfNeeded enforces the memory bounds: first drop idle-and-empty counters
// past idleTTL, then, if still over capacity, evict least-recently-used ones.
func (l *MemoryLimiter) evictIfNeeded(now time.Time) {
	// Idle-TTL sweep from the LRU tail (least-recently-used end). We only need
	// to inspect the tail because anything more recently used is not idle.
	for e := l.lru.Back(); e != nil; {
		prev := e.Prev()
		key := e.Value.(string)
		w := l.counters[key]
		if now.Sub(w.lastAccess) < l.idleTTL {
			break // tail is the oldest; nothing beyond is idle
		}
		// Prune stale events before deciding; only evict when the window is also
		// empty so we never drop live counting state.
		l.prune(w, now, w.dur)
		if len(w.events) > 0 {
			break
		}
		l.removeElem(e, key)
		e = prev
	}

	// LRU capacity bound.
	for len(l.counters) > l.maxCounters {
		e := l.lru.Back()
		if e == nil {
			break
		}
		l.removeElem(e, e.Value.(string))
	}
}

func (l *MemoryLimiter) removeElem(e *list.Element, key string) {
	l.lru.Remove(e)
	delete(l.counters, key)
}

// retryAfter estimates when enough capacity for n units frees up: it accumulates
// the costs of the oldest events until dropping them would leave room for n,
// then returns the time until that event expires. For RPM (cost=1) this reduces
// to the oldest event's expiry; for TPM it accounts for the required capacity
// (fixes the previously over-optimistic single-event estimate).
func retryAfter(w *window, now time.Time, dur time.Duration, n, limit int) time.Duration {
	if len(w.events) == 0 {
		return 0
	}
	// We must free at least this much to fit n.
	needToFree := w.sum + n - limit
	if needToFree <= 0 {
		return 0
	}
	freed := 0
	for _, ev := range w.events {
		freed += ev.cost
		if freed >= needToFree {
			d := ev.at.Add(dur).Sub(now)
			if d < 0 {
				return 0
			}
			return d
		}
	}
	// Even dropping every event isn't enough (n alone exceeds limit): point to
	// the last event's expiry as the best available hint.
	last := w.events[len(w.events)-1]
	d := last.at.Add(dur).Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// --- test-only inspection helpers ---

func (l *MemoryLimiter) numCounters() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.counters)
}

func (l *MemoryLimiter) hasCounter(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.counters[key]
	return ok
}

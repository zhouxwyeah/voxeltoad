package billing

import (
	"context"
	"sync"
)

// QuotaStore holds spendable balances per scope (e.g. "tenant:acme") in int64
// micro-units and reserves/reconciles them as cost is consumed (ADR-0013/0016).
// Implementations MUST be strongly consistent across data-plane instances in
// production (quota is money); the in-memory implementation here is for tests
// only.
type QuotaStore interface {
	// TryDebit atomically and conditionally debits est from every scope in the
	// set, all-or-nothing: if any configured scope has balance < est, nothing is
	// debited and ok=false (reject). Unconfigured scopes (no balance set) are
	// unlimited and skipped. A non-nil error (e.g. store unreachable) is distinct
	// from ok=false and drives fail-closed handling.
	TryDebit(ctx context.Context, scopes []string, est int64) (ok bool, err error)
	// Settle reconciles a prior reservation across the scope set by delta =
	// est - actual (positive = refund, negative = extra charge). Unconditional;
	// always called in Post (full refund when actual is 0).
	Settle(ctx context.Context, scopes []string, delta int64) error
}

// UsageRecord is one billable request, written for audit/reconciliation. Cost is
// int64 micro-units (ADR-0013).
type UsageRecord struct {
	Tenant           string
	Group            string
	APIKeyID         string
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	Cost             int64
	// CachedPromptTokens is the portion of PromptTokens that hit the upstream
	// prompt cache and was billed at CacheHitMultiplier (OpenAI cached_tokens /
	// Claude cache_read_input_tokens). Zero when no cache hit.
	CachedPromptTokens int
	// CacheDiscountMicros is the micro-units saved by the cache discount
	// (= FullCost - Cost). Zero when no cache hit or no multiplier configured.
	CacheDiscountMicros int64
	// RequestID is the gateway-assigned correlation id; SessionID is the
	// client-provided session key; TraceID is the W3C trace id from traceparent.
	// All three enable cross-system cost tracing (ADR-0021 §5, migration 00015).
	RequestID string
	SessionID string
	TraceID   string
}

// UsageRecorder persists usage records. Production implementations may batch /
// write asynchronously to PG (separate from the fast quota debit, ADR-0012).
type UsageRecorder interface {
	Record(ctx context.Context, rec UsageRecord) error
}

// MemoryQuotaStore is an in-process QuotaStore for tests only.
type MemoryQuotaStore struct {
	mu       sync.Mutex
	balances map[string]int64
	set      map[string]bool // which scopes have a configured balance
}

// NewMemoryQuotaStore returns an empty in-memory quota store.
func NewMemoryQuotaStore() *MemoryQuotaStore {
	return &MemoryQuotaStore{balances: map[string]int64{}, set: map[string]bool{}}
}

// SetBalance configures a scope's starting balance in micro-units (test setup).
func (m *MemoryQuotaStore) SetBalance(scope string, balance int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balances[scope] = balance
	m.set[scope] = true
}

// Balance returns the current balance in micro-units (test inspection).
func (m *MemoryQuotaStore) Balance(scope string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balances[scope]
}

// TryDebit conditionally debits est from every configured scope, all-or-nothing
// (mirrors the single-transaction PG semantics, ADR-0016). Unconfigured scopes
// are unlimited and skipped.
func (m *MemoryQuotaStore) TryDebit(_ context.Context, scopes []string, est int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check all configured scopes first; debit only if every one fits.
	for _, s := range scopes {
		if m.set[s] && m.balances[s] < est {
			return false, nil
		}
	}
	for _, s := range scopes {
		if m.set[s] {
			m.balances[s] -= est
		}
	}
	return true, nil
}

// Settle adds delta back to every configured scope (refund when positive, extra
// charge when negative). Unconfigured scopes are skipped.
func (m *MemoryQuotaStore) Settle(_ context.Context, scopes []string, delta int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range scopes {
		if m.set[s] {
			m.balances[s] += delta
		}
	}
	return nil
}

// MemoryUsageRecorder collects usage records in memory for tests only.
type MemoryUsageRecorder struct {
	mu      sync.Mutex
	records []UsageRecord
}

// NewMemoryUsageRecorder returns an empty in-memory recorder.
func NewMemoryUsageRecorder() *MemoryUsageRecorder {
	return &MemoryUsageRecorder{}
}

// Record appends a usage record.
func (m *MemoryUsageRecorder) Record(_ context.Context, rec UsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, rec)
	return nil
}

// Records returns a copy of the collected records (test inspection).
func (m *MemoryUsageRecorder) Records() []UsageRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]UsageRecord, len(m.records))
	copy(out, m.records)
	return out
}

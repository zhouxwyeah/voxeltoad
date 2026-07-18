package billing_test

import (
	"context"
	"testing"

	"voxeltoad/internal/billing"
)

// QuotaStore is now TryDebit/Settle over a scope set (ADR-0013/0016), int64
// micro-units. TryDebit is all-or-nothing across scopes.
func TestMemoryQuotaStore_TryDebitAndSettle(t *testing.T) {
	ctx := context.Background()
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:a", 1000)

	// Reserve 900 of 1000.
	ok, err := q.TryDebit(ctx, []string{"tenant:a"}, 900)
	if err != nil || !ok {
		t.Fatalf("TryDebit = %v,%v; want true,nil", ok, err)
	}
	if bal := q.Balance("tenant:a"); bal != 100 {
		t.Fatalf("balance = %d, want 100", bal)
	}

	// Settle: actual 600 < est 900 → refund 300.
	if err := q.Settle(ctx, []string{"tenant:a"}, 900-600); err != nil {
		t.Fatal(err)
	}
	if bal := q.Balance("tenant:a"); bal != 400 {
		t.Errorf("balance after settle = %d, want 400", bal)
	}
}

// Insufficient balance on any scope ⇒ ok=false and NOTHING is debited
// (all-or-nothing across the scope set).
func TestMemoryQuotaStore_TryDebitAllOrNothing(t *testing.T) {
	ctx := context.Background()
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:a", 1000) // plenty
	q.SetBalance("key:k", 50)      // too little

	ok, err := q.TryDebit(ctx, []string{"tenant:a", "key:k"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("TryDebit should fail when any scope is insufficient")
	}
	if bal := q.Balance("tenant:a"); bal != 1000 {
		t.Errorf("tenant:a debited despite group rollback: balance = %d, want 1000", bal)
	}
	if bal := q.Balance("key:k"); bal != 50 {
		t.Errorf("key:k must be untouched: balance = %d, want 50", bal)
	}
}

// Unconfigured scopes are unlimited: skipped inside TryDebit (no row), never a
// failure (absence = unlimited, ADR-0014).
func TestMemoryQuotaStore_UnconfiguredScopeUnlimited(t *testing.T) {
	ctx := context.Background()
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:a", 10)

	// key:k unconfigured → unlimited; tenant:a has 10, est 5 fits.
	ok, err := q.TryDebit(ctx, []string{"tenant:a", "key:k"}, 5)
	if err != nil || !ok {
		t.Fatalf("TryDebit = %v,%v; want true,nil (unconfigured scope is unlimited)", ok, err)
	}
	if bal := q.Balance("tenant:a"); bal != 5 {
		t.Errorf("tenant:a balance = %d, want 5", bal)
	}
}

func TestMemoryUsageRecorder_Records(t *testing.T) {
	rec := billing.NewMemoryUsageRecorder()
	_ = rec.Record(context.Background(), billing.UsageRecord{
		Tenant: "a", Provider: "openai", PromptTokens: 10, CompletionTokens: 5, Cost: 1000,
	})
	got := rec.Records()
	if len(got) != 1 || got[0].Tenant != "a" || got[0].Cost != 1000 {
		t.Errorf("recorded = %+v, want one record for tenant a cost 1000", got)
	}
}

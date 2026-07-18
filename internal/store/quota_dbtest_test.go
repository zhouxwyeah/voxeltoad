//go:build dbtest

package store_test

import (
	"context"
	"sync"
	"testing"

	"voxeltoad/internal/store"
)

// freshQuotaRepo migrates the shared DB, clears the quotas table, and returns a
// QuotaRepo plus a seed helper.
func freshQuotaRepo(t *testing.T) *store.QuotaRepo {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE quotas`).Error; err != nil {
		t.Fatalf("truncate quotas: %v", err)
	}
	return store.NewQuotaRepo(db)
}

func setQuota(t *testing.T, repo *store.QuotaRepo, scope string, balance int64) {
	t.Helper()
	if err := repo.SetBalance(context.Background(), scope, balance, "usd"); err != nil {
		t.Fatalf("set balance %s: %v", scope, err)
	}
}

func TestQuotaRepo_TryDebitAndSettle(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	setQuota(t, repo, "tenant:a", 1000)

	ok, err := repo.TryDebit(ctx, []string{"tenant:a"}, 900)
	if err != nil || !ok {
		t.Fatalf("TryDebit = %v,%v; want true,nil", ok, err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 100 {
		t.Fatalf("balance = %d, want 100", bal)
	}

	// Settle: actual 600 < est 900 → refund 300.
	if err := repo.Settle(ctx, []string{"tenant:a"}, 300); err != nil {
		t.Fatal(err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 400 {
		t.Errorf("balance after settle = %d, want 400", bal)
	}
}

// All-or-nothing across scopes in a single transaction: if any configured scope
// is insufficient, NOTHING is debited.
func TestQuotaRepo_TryDebitAllOrNothing(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	setQuota(t, repo, "tenant:a", 1000) // plenty
	setQuota(t, repo, "key:k", 50)      // too little

	ok, err := repo.TryDebit(ctx, []string{"tenant:a", "key:k"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("TryDebit should fail when any scope is insufficient")
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 1000 {
		t.Errorf("tenant:a must roll back: balance = %d, want 1000", bal)
	}
	if bal := mustBalance(t, repo, "key:k"); bal != 50 {
		t.Errorf("key:k must be untouched: balance = %d, want 50", bal)
	}
}

// Unconfigured scope (no row) is unlimited: skipped, never a failure.
func TestQuotaRepo_UnconfiguredScopeUnlimited(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	setQuota(t, repo, "tenant:a", 10)

	ok, err := repo.TryDebit(ctx, []string{"tenant:a", "key:unconfigured"}, 5)
	if err != nil || !ok {
		t.Fatalf("TryDebit = %v,%v; want true,nil (unconfigured scope unlimited)", ok, err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 5 {
		t.Errorf("tenant:a balance = %d, want 5", bal)
	}
}

// Concurrent TryDebit must not oversell: with balance for exactly N reservations,
// at most N succeed (the atomic conditional UPDATE is the consistency primitive).
func TestQuotaRepo_ConcurrentNoOversell(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	const est = 10
	const n = 20
	setQuota(t, repo, "tenant:race", est*n) // exactly n reservations fit

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < n*2; i++ { // twice as many attempts as can fit
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.TryDebit(ctx, []string{"tenant:race"}, est)
			if err != nil {
				t.Errorf("TryDebit error: %v", err)
				return
			}
			if ok {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if success != n {
		t.Errorf("successful debits = %d, want exactly %d (no oversell)", success, n)
	}
	if bal := mustBalance(t, repo, "tenant:race"); bal != 0 {
		t.Errorf("final balance = %d, want 0", bal)
	}
}

func mustBalance(t *testing.T, repo *store.QuotaRepo, scope string) int64 {
	t.Helper()
	bal, err := repo.Balance(context.Background(), scope)
	if err != nil {
		t.Fatalf("balance %s: %v", scope, err)
	}
	return bal
}

// TopUp is an atomic increment: it creates the scope at delta when absent, and
// adds to the existing balance otherwise. Unlike SetBalance it never overwrites,
// so it cannot clobber a concurrent hot-path debit.
func TestQuotaRepo_TopUpCreatesAndIncrements(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)

	// First top-up creates the row.
	if err := repo.TopUp(ctx, "tenant:a", 500, "usd"); err != nil {
		t.Fatalf("TopUp create: %v", err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 500 {
		t.Fatalf("balance after create = %d, want 500", bal)
	}

	// Second top-up increments, does not overwrite.
	if err := repo.TopUp(ctx, "tenant:a", 250, "usd"); err != nil {
		t.Fatalf("TopUp increment: %v", err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 750 {
		t.Errorf("balance after increment = %d, want 750 (500+250)", bal)
	}
}

// The critical correctness property (ADR-0019): concurrent TopUp and TryDebit
// must not lose updates. With a starting balance, T top-ups of +delta and D
// successful debits of -delta interleaved, the final balance must equal
// start + T*delta - (debited)*delta exactly — no increment silently clobbered.
func TestQuotaRepo_TopUpConcurrentWithDebitNoLostUpdate(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)

	const start = 1000
	const delta = 10
	const nTopUp = 40
	const nDebit = 40
	setQuota(t, repo, "tenant:race", start)

	var wg sync.WaitGroup
	var mu sync.Mutex
	debited := 0

	// Interleave top-ups and debits. Because start + top-ups always keeps the
	// balance well above delta, every debit should succeed; the point is that
	// no concurrent increment is lost.
	for i := 0; i < nTopUp; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := repo.TopUp(ctx, "tenant:race", delta, "usd"); err != nil {
				t.Errorf("TopUp: %v", err)
			}
		}()
	}
	for i := 0; i < nDebit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.TryDebit(ctx, []string{"tenant:race"}, delta)
			if err != nil {
				t.Errorf("TryDebit: %v", err)
				return
			}
			if ok {
				mu.Lock()
				debited++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	want := int64(start + nTopUp*delta - debited*delta)
	if bal := mustBalance(t, repo, "tenant:race"); bal != want {
		t.Errorf("final balance = %d, want %d (start %d + %d topups*%d - %d debits*%d) — a lost update indicates non-atomic increment",
			bal, want, start, nTopUp, delta, debited, delta)
	}
}

// TryDebit's boundary: balance == est succeeds (>= is inclusive); balance ==
// est-1 fails. Guards the `balance >= ?` predicate's edge.
func TestQuotaRepo_TryDebitExactBoundary(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)

	// Exactly enough: balance == est → succeeds.
	setQuota(t, repo, "tenant:exact", 100)
	ok, err := repo.TryDebit(ctx, []string{"tenant:exact"}, 100)
	if err != nil || !ok {
		t.Fatalf("balance==est: TryDebit = %v,%v; want true,nil", ok, err)
	}
	if bal := mustBalance(t, repo, "tenant:exact"); bal != 0 {
		t.Errorf("balance after exact debit = %d, want 0", bal)
	}

	// One short: balance == est-1 → fails, nothing debited.
	setQuota(t, repo, "tenant:short", 99)
	ok2, err := repo.TryDebit(ctx, []string{"tenant:short"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Error("balance==est-1: TryDebit should fail")
	}
	if bal := mustBalance(t, repo, "tenant:short"); bal != 99 {
		t.Errorf("balance after failed boundary debit = %d, want 99", bal)
	}
}

// Settle refunds delta to EACH scope in the set (multi-scope cross-layer
// refund). Previously only single-scope Settle was tested.
func TestQuotaRepo_SettleMultiScope(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	setQuota(t, repo, "tenant:a", 1000)
	setQuota(t, repo, "group:g", 1000)
	setQuota(t, repo, "key:k", 1000)

	scopes := []string{"tenant:a", "group:g", "key:k"}
	// Debit 100 from each.
	ok, err := repo.TryDebit(ctx, scopes, 100)
	if err != nil || !ok {
		t.Fatalf("TryDebit = %v,%v", ok, err)
	}
	for _, s := range scopes {
		if bal := mustBalance(t, repo, s); bal != 900 {
			t.Errorf("%s after debit = %d, want 900", s, bal)
		}
	}

	// Settle refund 30 to each scope.
	if err := repo.Settle(ctx, scopes, 30); err != nil {
		t.Fatal(err)
	}
	for _, s := range scopes {
		if bal := mustBalance(t, repo, s); bal != 930 {
			t.Errorf("%s after settle = %d, want 930 (900+30)", s, bal)
		}
	}
}

// Settle with negative delta charges each scope (extra charge path, quota.go
// comment: "extra charge when negative"). Guards the balance + delta branch.
func TestQuotaRepo_SettleNegativeDeltaCharges(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	setQuota(t, repo, "tenant:a", 1000)

	// Negative delta = extra charge: balance decreases by |delta|.
	if err := repo.Settle(ctx, []string{"tenant:a"}, -50); err != nil {
		t.Fatal(err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 950 {
		t.Errorf("balance after negative settle = %d, want 950 (1000-50)", bal)
	}
}

// BalanceWithCurrency distinguishes an unconfigured scope (no row → currency "")
// from a configured zero-balance scope (row with currency). Admin read paths
// depend on this distinction.
func TestQuotaRepo_BalanceWithCurrency_DistinguishesUnconfigured(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)

	// Configured with zero balance.
	setQuota(t, repo, "tenant:zero", 0)
	bal, cur, err := repo.BalanceWithCurrency(ctx, "tenant:zero")
	if err != nil {
		t.Fatal(err)
	}
	if bal != 0 || cur != "usd" {
		t.Errorf("configured zero: (%d,%q); want (0,\"usd\")", bal, cur)
	}

	// Unconfigured: no row → currency "" (the distinguishing signal).
	bal2, cur2, err := repo.BalanceWithCurrency(ctx, "tenant:ghost")
	if err != nil {
		t.Fatal(err)
	}
	if bal2 != 0 || cur2 != "" {
		t.Errorf("unconfigured: (%d,%q); want (0,\"\") — currency is the unconfigured signal", bal2, cur2)
	}
}

// Early-exit branches in TryDebit: est <= 0 or empty scopes → no-op (true).
// Guards the guard clauses at quota.go:84.
func TestQuotaRepo_TryDebitEarlyExitBranches(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	setQuota(t, repo, "tenant:a", 100)

	// est <= 0 → always ok, no debit.
	ok, err := repo.TryDebit(ctx, []string{"tenant:a"}, 0)
	if err != nil || !ok {
		t.Errorf("est=0: TryDebit = %v,%v; want true,nil", ok, err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 100 {
		t.Errorf("est=0 should not debit; balance = %d, want 100", bal)
	}

	// empty scopes → always ok.
	ok2, err := repo.TryDebit(ctx, []string{}, 50)
	if err != nil || !ok2 {
		t.Errorf("empty scopes: TryDebit = %v,%v; want true,nil", ok2, err)
	}
}

// Settle early-exit: delta == 0 or empty scopes → no-op. Guards quota.go:130.
func TestQuotaRepo_SettleEarlyExitBranches(t *testing.T) {
	ctx := context.Background()
	repo := freshQuotaRepo(t)
	setQuota(t, repo, "tenant:a", 500)

	// delta == 0 → no-op.
	if err := repo.Settle(ctx, []string{"tenant:a"}, 0); err != nil {
		t.Fatal(err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 500 {
		t.Errorf("delta=0 settle should be no-op; balance = %d, want 500", bal)
	}

	// empty scopes → no-op.
	if err := repo.Settle(ctx, []string{}, 100); err != nil {
		t.Fatal(err)
	}
	if bal := mustBalance(t, repo, "tenant:a"); bal != 500 {
		t.Errorf("empty-scope settle should be no-op; balance = %d, want 500", bal)
	}
}

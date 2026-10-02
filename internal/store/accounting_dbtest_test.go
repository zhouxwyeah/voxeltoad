//go:build dbtest

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

type accountingFixture struct {
	db                       *store.DB
	accounting               *store.AccountingRepo
	budgets                  *store.BudgetRepo
	quota                    *store.QuotaRepo
	tenantID, groupID, appID int64
	req                      billing.ReservationRequest
}

func (f accountingFixture) usageRecord(cost int64) *billing.UsageRecord {
	return &billing.UsageRecord{
		Tenant: f.req.Tenant, APIKeyID: f.req.APIKeyID, Provider: "openai", Model: "frozen",
		PromptTokens: 10, CompletionTokens: 5, Cost: cost, Currency: f.req.Currency,
		RequestID: f.req.RequestID, ApplicationID: f.req.ApplicationID, Environment: f.req.Environment,
	}
}

func mustUsageRows(t *testing.T, f accountingFixture) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(`SELECT count(*) FROM usage_records WHERE tenant = ?`, f.req.Tenant).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func newAccountingFixture(t *testing.T) accountingFixture {
	t.Helper()
	db := mustMigratedDB(t)
	tenant, group, app := seedApplication(t, db, "accounting-tenant", "accounting-group", "accounting-app")
	if err := db.Exec(`INSERT INTO api_keys (key_id,hash,tenant_id,group_id,application_id,environment,allowed_models) VALUES ('accounting-key','accounting-hash',?,?,?,'prod','[]'::jsonb)`, tenant, group, app).Error; err != nil {
		t.Fatal(err)
	}
	return accountingFixture{db: db, accounting: store.NewAccountingRepo(db), budgets: store.NewBudgetRepo(db, tenant), quota: store.NewQuotaRepo(db), tenantID: tenant, groupID: group, appID: app,
		req: billing.ReservationRequest{ID: "reservation-one", RequestID: "client-correlation", Tenant: "accounting-tenant", Group: "accounting-group", APIKeyID: "accounting-key", ApplicationID: &app, Environment: "prod", Currency: "usd", Estimate: 100, PriceSnapshot: json.RawMessage(`{"model":"frozen","prompt_per_1m":1}`)}}
}

func createBudget(t *testing.T, f accountingFixture, kind, ref string, limit int64, mode string) billing.BudgetPolicy {
	t.Helper()
	spec := billing.BudgetSpec{Name: kind, ScopeKind: kind, ScopeRef: ref, Period: "daily", Currency: "usd", Limit: limit, Mode: mode, Thresholds: []int{50, 80, 100}}
	if kind == "application_env" {
		spec.Environment = "prod"
	}
	p, err := f.budgets.Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func accountFor(t *testing.T, f accountingFixture, policy billing.BudgetPolicy) billing.BudgetAccount {
	t.Helper()
	accounts, err := f.budgets.Accounts(context.Background(), policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts=%d want 1", len(accounts))
	}
	return accounts[0]
}

func reservationFor(t *testing.T, f accountingFixture, id string) billing.Reservation {
	t.Helper()
	rows, _, err := f.budgets.Reservations(context.Background(), "", "", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("missing reservation %s", id)
	return billing.Reservation{}
}

func TestAccountingFiveDimensionsRollbackOverspendAndIdempotency(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	scopes := []string{"tenant:" + f.req.Tenant, billing.GroupScope(f.req.Tenant, f.req.Group), "key:" + f.req.APIKeyID}
	for _, scope := range scopes {
		setQuota(t, f.quota, scope, 1000)
	}
	policies := []billing.BudgetPolicy{
		createBudget(t, f, "tenant", "", 100, "enforce"),
		createBudget(t, f, "group", strconv.FormatInt(f.groupID, 10), 100, "enforce"),
		createBudget(t, f, "application", strconv.FormatInt(f.appID, 10), 100, "enforce"),
		createBudget(t, f, "application_env", strconv.FormatInt(f.appID, 10), 100, "enforce"),
		createBudget(t, f, "key", f.req.APIKeyID, 50, "enforce"),
	}
	if err := f.accounting.Reserve(ctx, f.req); !errors.Is(err, billing.ErrInsufficient) {
		t.Fatalf("reserve=%v", err)
	}
	for _, scope := range scopes {
		if got := mustBalance(t, f.quota, scope); got != 1000 {
			t.Fatalf("partial debit %s=%d", scope, got)
		}
	}
	var count int64
	if err := f.db.Raw(`SELECT count(*) FROM billing_reservations`).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed reservation persisted: %d %v", count, err)
	}
	for _, p := range policies {
		accounts, err := f.budgets.Accounts(ctx, p.ID)
		if err != nil || len(accounts) != 0 {
			t.Fatalf("partial account persisted: %+v %v", accounts, err)
		}
	}
	limit := int64(100)
	if _, err := f.budgets.Patch(ctx, policies[4].ID, billing.BudgetPatch{Version: 1, Limit: &limit}); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatalf("duplicate reserve: %v", err)
	}
	for _, p := range policies {
		a := accountFor(t, f, p)
		if a.Reserved != 100 || a.Committed != 0 {
			t.Fatalf("reserve %+v", a)
		}
	}
	s := billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 150, Usage: f.usageRecord(150)}
	if err := f.accounting.Finish(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Finish(ctx, s); err != nil {
		t.Fatalf("duplicate finish: %v", err)
	}
	// The known usage row is persisted by the settlement transaction itself and
	// survives duplicate finishes exactly once — a crash between settlement and
	// an async flush can no longer lose the ledger row.
	if rows := mustUsageRows(t, f); rows != 1 {
		t.Fatalf("usage rows = %d, want exactly one", rows)
	}
	for _, scope := range scopes {
		if got := mustBalance(t, f.quota, scope); got != 850 {
			t.Fatalf("bad charge %s=%d", scope, got)
		}
	}
	for _, p := range policies {
		a := accountFor(t, f, p)
		if a.Reserved != 0 || a.Committed != 150 || a.Available != -50 {
			t.Fatalf("overspend %+v", a)
		}
	}
	events, _, err := f.budgets.Events(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 20 {
		t.Fatalf("events=%d want 15 thresholds + 5 overspend", len(events))
	}
	s.Actual = 149
	if err := f.accounting.Finish(ctx, s); !errors.Is(err, billing.ErrReservationConflict) {
		t.Fatalf("conflicting settlement=%v", err)
	}
	f.req.ID = "next-request"
	f.req.Estimate = 0
	if err := f.accounting.Reserve(ctx, f.req); !errors.Is(err, billing.ErrInsufficient) {
		t.Fatalf("zero estimate after exhaustion=%v", err)
	}
}

func TestAccountingConcurrentReservationsDoNotOversell(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	p := createBudget(t, f, "tenant", "", 100, "enforce")
	var success atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := f.req
			req.ID = fmt.Sprintf("parallel-%02d", i)
			req.Estimate = 10
			err := f.accounting.Reserve(ctx, req)
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, billing.ErrInsufficient) {
				t.Errorf("reserve %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 10 {
		t.Fatalf("success=%d want10", success.Load())
	}
	a := accountFor(t, f, p)
	if a.Reserved != 100 || a.Available != 0 {
		t.Fatalf("account=%+v", a)
	}
}

func TestAccountingConcurrentSameSettlementChargesOnce(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	p := createBudget(t, f, "tenant", "", 1000, "enforce")
	scopes := []string{"tenant:" + f.req.Tenant, billing.GroupScope(f.req.Tenant, f.req.Group), "key:" + f.req.APIKeyID}
	for _, scope := range scopes {
		setQuota(t, f.quota, scope, 1000)
	}
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	s := billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 70}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.accounting.Finish(ctx, s); err != nil {
				t.Errorf("concurrent finish: %v", err)
			}
		}()
	}
	wg.Wait()
	for _, scope := range scopes {
		if got := mustBalance(t, f.quota, scope); got != 930 {
			t.Fatalf("double settlement %s=%d", scope, got)
		}
	}
	if a := accountFor(t, f, p); a.Reserved != 0 || a.Committed != 70 || a.Released != 30 {
		t.Fatalf("concurrent account=%+v", a)
	}
}

func TestAccountingConcurrentPolicyPatchAndSettlement(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	p := createBudget(t, f, "tenant", "", 1000, "enforce")
	for i := 0; i < 5; i++ {
		req := f.req
		req.ID = fmt.Sprintf("patch-race-%d", i)
		if err := f.accounting.Reserve(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		version := p.Version
		for i := 0; i < 20; i++ {
			limit := int64(1000 + i)
			updated, err := f.budgets.Patch(ctx, p.ID, billing.BudgetPatch{Version: version, Limit: &limit})
			if err != nil {
				t.Errorf("concurrent patch: %v", err)
				return
			}
			version = updated.Version
		}
	}()
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: fmt.Sprintf("patch-race-%d", i), Outcome: "known", Actual: 150}); err != nil {
				t.Errorf("settlement during policy patch: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if a := accountFor(t, f, p); a.Committed != 750 || a.Reserved != 0 || a.Limit != 1019 {
		t.Fatalf("patch race reset counters: %+v", a)
	}
}

func TestAccountingZeroSoftCurrencyAndUnconfigured(t *testing.T) {
	for _, mode := range []string{"enforce", "soft"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newAccountingFixture(t)
			p := createBudget(t, f, "tenant", "", 0, mode)
			f.req.Estimate = 0
			err := f.accounting.Reserve(ctx, f.req)
			if mode == "enforce" {
				if !errors.Is(err, billing.ErrInsufficient) {
					t.Fatalf("zero enforce=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 15}); err != nil {
				t.Fatal(err)
			}
			if a := accountFor(t, f, p); a.Committed != 15 || a.Available != -15 {
				t.Fatalf("soft=%+v", a)
			}
		})
	}
	t.Run("no policies still idempotent", func(t *testing.T) {
		ctx := context.Background()
		f := newAccountingFixture(t)
		scope := "tenant:" + f.req.Tenant
		setQuota(t, f.quota, scope, 1000)
		if err := f.accounting.Reserve(ctx, f.req); err != nil {
			t.Fatal(err)
		}
		s := billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 100}
		for i := 0; i < 2; i++ {
			if err := f.accounting.Finish(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if got := mustBalance(t, f.quota, scope); got != 900 {
			t.Fatalf("double debit=%d", got)
		}
		f.req.ID = "same-client-request-id"
		if err := f.accounting.Reserve(ctx, f.req); err != nil {
			t.Fatal(err)
		}
		if got := mustBalance(t, f.quota, scope); got != 800 {
			t.Fatalf("client request_id was deduped: %d", got)
		}
	})
	t.Run("quota zero estimate exhausted", func(t *testing.T) {
		f := newAccountingFixture(t)
		setQuota(t, f.quota, "tenant:"+f.req.Tenant, 0)
		f.req.Estimate = 0
		if err := f.accounting.Reserve(context.Background(), f.req); !errors.Is(err, billing.ErrInsufficient) {
			t.Fatal(err)
		}
	})
	t.Run("budget currency mismatch rolls quota back", func(t *testing.T) {
		f := newAccountingFixture(t)
		createBudget(t, f, "tenant", "", 1000, "soft")
		f.req.Currency = "cny"
		if err := f.quota.SetBalance(context.Background(), "tenant:"+f.req.Tenant, 1000, "cny"); err != nil {
			t.Fatal(err)
		}
		if err := f.accounting.Reserve(context.Background(), f.req); !errors.Is(err, billing.ErrCurrencyMismatch) {
			t.Fatalf("currency=%v", err)
		}
		if got := mustBalance(t, f.quota, "tenant:"+f.req.Tenant); got != 1000 {
			t.Fatalf("partial debit=%d", got)
		}
	})
	t.Run("quota currency mismatch", func(t *testing.T) {
		f := newAccountingFixture(t)
		setQuota(t, f.quota, "tenant:"+f.req.Tenant, 1000)
		f.req.Currency = "cny"
		if err := f.accounting.Reserve(context.Background(), f.req); !errors.Is(err, billing.ErrCurrencyMismatch) {
			t.Fatal(err)
		}
	})
}

func TestAccountingMidnightDSTDoesNotResetWeeklyBudget(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	f.accounting.SetClock(func() time.Time { return now })
	f.budgets.SetClock(func() time.Time { return now })
	policy, err := f.budgets.Create(ctx, billing.BudgetSpec{
		Name: "DST weekly", ScopeKind: "tenant", Period: "weekly", Timezone: "America/Santiago", Currency: "usd", Limit: 200, Mode: "enforce",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 1)
	f.req.ID = "after-midnight-gap"
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	account := accountFor(t, f, policy)
	if account.Reserved != 200 || account.Available != 0 || account.PeriodStart.UTC().Format(time.RFC3339) != "2026-08-31T04:00:00Z" || account.PeriodEnd.UTC().Format(time.RFC3339) != "2026-09-07T03:00:00Z" {
		t.Fatalf("same civil week must share its account: %+v", account)
	}
	f.req.ID, f.req.Estimate = "after-weekly-exhaustion", 1
	if err := f.accounting.Reserve(ctx, f.req); !errors.Is(err, billing.ErrInsufficient) {
		t.Fatalf("DST restored exhausted budget: %v", err)
	}
}

func TestAccountingRolloverFreezesOriginalAccountAndPrice(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	now := time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC)
	f.accounting.SetClock(func() time.Time { return now })
	f.budgets.SetClock(func() time.Time { return now })
	p := createBudget(t, f, "tenant", "", 500, "enforce")
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	next := f.req
	next.ID = "next-period"
	if err := f.accounting.Reserve(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 200}); err != nil {
		t.Fatal(err)
	}
	accounts, err := f.budgets.Accounts(ctx, p.ID)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts=%+v %v", accounts, err)
	}
	if accounts[0].Reserved != 100 || accounts[0].Committed != 0 || accounts[1].Committed != 200 || accounts[1].Reserved != 0 {
		t.Fatalf("wrong period %+v", accounts)
	}
	r := reservationFor(t, f, f.req.ID)
	var snapshot map[string]any
	if err := json.Unmarshal(r.PriceSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["model"] != "frozen" {
		t.Fatalf("snapshot=%s", r.PriceSnapshot)
	}
}

func TestAccountingUnknownRiskReleaseAndLateKnownCharge(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	p := createBudget(t, f, "tenant", "", 1000, "enforce")
	scope := "tenant:" + f.req.Tenant
	setQuota(t, f.quota, scope, 1000)
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.MarkDispatched(ctx, f.req.ID); err != nil {
		t.Fatal(err)
	}
	unknown := billing.Settlement{ReservationID: f.req.ID, Outcome: "unknown", Reason: "missing upstream usage"}
	if err := f.accounting.Finish(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Finish(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if a := accountFor(t, f, p); a.Reserved != 100 || a.Committed != 0 {
		t.Fatalf("unknown lost hold: %+v", a)
	}
	r := reservationFor(t, f, f.req.ID)
	res := billing.Resolution{Version: r.Version, Action: "release_unknown", Reason: "accept uncertainty", Evidence: "incident record", OperatorID: 1}
	resolved, err := f.budgets.Resolve(ctx, r.ID, res)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "released_unknown" || resolved.Actual != nil {
		t.Fatalf("risk release invented zero: %+v", resolved)
	}
	if _, err := f.budgets.Resolve(ctx, r.ID, res); !errors.Is(err, billing.ErrReservationConflict) {
		t.Fatalf("stale resolve=%v", err)
	}
	if got := mustBalance(t, f.quota, scope); got != 1000 {
		t.Fatalf("refund=%d", got)
	}
	if a := accountFor(t, f, p); a.Reserved != 0 || a.Released != 100 || a.Committed != 0 {
		t.Fatalf("released=%+v", a)
	}
	known := billing.Settlement{ReservationID: r.ID, Outcome: "known", Actual: 160}
	if err := f.accounting.Finish(ctx, known); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Finish(ctx, known); err != nil {
		t.Fatal(err)
	}
	if got := mustBalance(t, f.quota, scope); got != 840 {
		t.Fatalf("late charge double refund/debit=%d", got)
	}
	if a := accountFor(t, f, p); a.Reserved != 0 || a.Committed != 160 || a.Released != 100 {
		t.Fatalf("late account=%+v", a)
	}
	events, _, err := f.budgets.Events(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("unknown/resolution dedupe=%+v", events)
	}
}

func TestAccountingStaleNeverRefundsAndConfirmedRelease(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	p := createBudget(t, f, "tenant", "", 1000, "enforce")
	now := time.Now().Add(-time.Hour)
	f.accounting.SetClock(func() time.Time { return now })
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	n, err := f.accounting.MarkStale(ctx, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("mark stale=%d %v", n, err)
	}
	if a := accountFor(t, f, p); a.Reserved != 100 {
		t.Fatalf("stale refund=%+v", a)
	}
	r := reservationFor(t, f, f.req.ID)
	resolved, err := f.budgets.Resolve(ctx, r.ID, billing.Resolution{Version: r.Version, Action: "release", Reason: "no upstream call", Evidence: "provider confirms", OperatorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "released" || resolved.Actual == nil || *resolved.Actual != 0 {
		t.Fatalf("confirmed release=%+v", resolved)
	}
	if a := accountFor(t, f, p); a.Reserved != 0 || a.Released != 100 {
		t.Fatalf("released account=%+v", a)
	}
	if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: r.ID, Outcome: "known", Actual: 10}); !errors.Is(err, billing.ErrReservationConflict) {
		t.Fatalf("conflicting finalized charge=%v", err)
	}
}

func TestAccountingPendingKnownResultSurvivesApplyFailure(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	p := createBudget(t, f, "tenant", "", 1000, "enforce")
	scope := "tenant:" + f.req.Tenant
	setQuota(t, f.quota, scope, 1000)
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`CREATE FUNCTION test_reject_accounting_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected account apply failure'; END $$;
		CREATE TRIGGER test_accounting_apply BEFORE UPDATE OF committed ON budget_accounts FOR EACH ROW EXECUTE FUNCTION test_reject_accounting_apply()`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.db.Exec(`DROP TRIGGER IF EXISTS test_accounting_apply ON budget_accounts; DROP FUNCTION IF EXISTS test_reject_accounting_apply()`).Error
	})
	s := billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 75, Usage: f.usageRecord(75)}
	if err := f.accounting.Finish(ctx, s); err == nil {
		t.Fatal("injected failure not surfaced")
	}
	var persisted struct {
		Result        []byte
		ResultApplied bool
	}
	if err := f.db.Raw(`SELECT result,result_applied FROM billing_reservations WHERE id=?`, f.req.ID).Scan(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if len(persisted.Result) == 0 || persisted.ResultApplied {
		t.Fatalf("result not recoverable: %+v", persisted)
	}
	var recorded billing.Settlement
	if err := json.Unmarshal(persisted.Result, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Usage == nil || recorded.Actual != 75 {
		t.Fatalf("financial result missing usage: %+v", recorded)
	}
	if got := mustBalance(t, f.quota, scope); got != 900 {
		t.Fatalf("quota apply not rolled back: %d", got)
	}
	if err := f.db.Exec(`DROP TRIGGER test_accounting_apply ON budget_accounts; DROP FUNCTION test_reject_accounting_apply()`).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.RetryPending(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.RetryPending(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if got := mustBalance(t, f.quota, scope); got != 925 {
		t.Fatalf("retry charge=%d", got)
	}
	if a := accountFor(t, f, p); a.Committed != 75 || a.Reserved != 0 || a.Released != 25 {
		t.Fatalf("retry account=%+v", a)
	}
	// Recovery replays the usage row from the durable result exactly once.
	if rows := mustUsageRows(t, f); rows != 1 {
		t.Fatalf("usage rows after recovery = %d, want exactly one", rows)
	}
}

func TestAccountingResultPersistenceFailureRemainsUnknown(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	p := createBudget(t, f, "tenant", "", 1000, "enforce")
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`CREATE FUNCTION test_reject_accounting_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected result persistence failure'; END $$;
		CREATE TRIGGER test_accounting_result BEFORE UPDATE OF result ON billing_reservations FOR EACH ROW EXECUTE FUNCTION test_reject_accounting_result()`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.db.Exec(`DROP TRIGGER IF EXISTS test_accounting_result ON billing_reservations; DROP FUNCTION IF EXISTS test_reject_accounting_result()`).Error
	})
	if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 75}); err == nil {
		t.Fatal("lost result falsely acknowledged")
	}
	var hasResult bool
	if err := f.db.Raw(`SELECT result IS NOT NULL FROM billing_reservations WHERE id=?`, f.req.ID).Scan(&hasResult).Error; err != nil {
		t.Fatal(err)
	}
	if hasResult {
		t.Fatal("failed result reported as durable")
	}
	if err := f.accounting.RetryPending(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.accounting.MarkStale(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r := reservationFor(t, f, f.req.ID); r.Status != "unknown" || r.Actual != nil {
		t.Fatalf("lost-result state=%+v", r)
	}
	if a := accountFor(t, f, p); a.Reserved != 100 || a.Committed != 0 {
		t.Fatalf("lost result refunded=%+v", a)
	}
}

func TestAccountingGroupScopesAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	otherTenant, otherGroup, otherApp := seedApplication(t, f.db, "other-tenant", f.req.Group, "other-app")
	_ = otherTenant
	_ = otherGroup
	setQuota(t, f.quota, billing.GroupScope(f.req.Tenant, f.req.Group), 1000)
	setQuota(t, f.quota, billing.GroupScope("other-tenant", f.req.Group), 2000)
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if got := mustBalance(t, f.quota, billing.GroupScope("other-tenant", f.req.Group)); got != 2000 {
		t.Fatalf("cross tenant group debit=%d", got)
	}
	f.req.ID = "cross-tenant-app"
	f.req.ApplicationID = &otherApp
	if err := f.accounting.Reserve(ctx, f.req); !errors.Is(err, billing.ErrInvalidPolicy) {
		t.Fatalf("cross tenant app=%v", err)
	}
	f.req.ApplicationID = &f.appID
	f.req.ID = "ambiguous-group"
	setQuota(t, f.quota, "group:"+f.req.Group, 500)
	if err := f.accounting.Reserve(ctx, f.req); !errors.Is(err, billing.ErrInvalidPolicy) {
		t.Fatalf("legacy ambiguous group accepted: %v", err)
	}
	if got := mustBalance(t, f.quota, "group:"+f.req.Group); got != 500 {
		t.Fatal("ambiguous historical scope mutated")
	}
}

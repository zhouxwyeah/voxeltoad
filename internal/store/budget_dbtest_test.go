//go:build dbtest

package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

func TestBudgetPolicyValidationAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	otherTenant, otherGroup, otherApp := seedApplication(t, f.db, "other-budget-tenant", "other-budget-group", "other-budget-app")
	base := billing.BudgetSpec{Name: "monthly", ScopeKind: "tenant", Period: "monthly", Currency: "USD", Limit: 500, Mode: "enforce", Thresholds: []int{100, 50}}
	for _, tc := range []struct {
		name   string
		mutate func(*billing.BudgetSpec)
	}{
		{"foreign group", func(s *billing.BudgetSpec) { s.ScopeKind = "group"; s.ScopeRef = strconv.FormatInt(otherGroup, 10) }},
		{"foreign app", func(s *billing.BudgetSpec) { s.ScopeKind = "application"; s.ScopeRef = strconv.FormatInt(otherApp, 10) }},
		{"foreign key", func(s *billing.BudgetSpec) { s.ScopeKind = "key"; s.ScopeRef = "missing" }},
		{"invalid scope", func(s *billing.BudgetSpec) { s.ScopeKind = "owner_group" }},
		{"name", func(s *billing.BudgetSpec) { s.Name = " " }},
		{"negative", func(s *billing.BudgetSpec) { s.Limit = -1 }},
		{"mode", func(s *billing.BudgetSpec) { s.Mode = "hard" }},
		{"zone", func(s *billing.BudgetSpec) { s.Timezone = "invalid/zone" }},
		{"period", func(s *billing.BudgetSpec) { s.Period = "hourly" }},
		{"zero threshold", func(s *billing.BudgetSpec) { s.Thresholds = []int{0} }},
		{"over threshold", func(s *billing.BudgetSpec) { s.Thresholds = []int{101} }},
		{"duplicate threshold", func(s *billing.BudgetSpec) { s.Thresholds = []int{50, 50} }},
		{"environment", func(s *billing.BudgetSpec) {
			s.ScopeKind = "application_env"
			s.ScopeRef = strconv.FormatInt(f.appID, 10)
			s.Environment = "unknown"
		}},
		{"irrelevant environment", func(s *billing.BudgetSpec) { s.Environment = "prod" }},
		{"noncanonical id", func(s *billing.BudgetSpec) {
			s.ScopeKind = "group"
			s.ScopeRef = "0" + strconv.FormatInt(f.groupID, 10)
		}},
		{"currency", func(s *billing.BudgetSpec) { s.Currency = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mutate(&s)
			if _, err := f.budgets.Create(ctx, s); !errors.Is(err, billing.ErrInvalidPolicy) {
				t.Fatalf("Create=%v", err)
			}
		})
	}
	p, err := f.budgets.Create(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if p.Timezone != "UTC" || p.Currency != "usd" || len(p.Thresholds) != 2 || p.Thresholds[0] != 50 || !p.Enabled || p.Version != 1 {
		t.Fatalf("policy=%+v", p)
	}
	other := store.NewBudgetRepo(f.db, otherTenant)
	if _, err := other.Get(ctx, p.ID); !errors.Is(err, billing.ErrNotFound) {
		t.Fatalf("cross read=%v", err)
	}
	if _, err := other.Accounts(ctx, p.ID); !errors.Is(err, billing.ErrNotFound) {
		t.Fatalf("cross accounts=%v", err)
	}
	limit := int64(700)
	if _, err := other.Patch(ctx, p.ID, billing.BudgetPatch{Version: 1, Limit: &limit}); !errors.Is(err, billing.ErrNotFound) {
		t.Fatalf("cross patch=%v", err)
	}
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: f.req.ID, Outcome: "unknown", Reason: "unknown"}); err != nil {
		t.Fatal(err)
	}
	r := reservationFor(t, f, f.req.ID)
	if _, err := other.Resolve(ctx, r.ID, billing.Resolution{Version: r.Version, Action: "release", Reason: "evidence", Evidence: "record", OperatorID: 1}); !errors.Is(err, billing.ErrNotFound) {
		t.Fatalf("cross resolution=%v", err)
	}
	rows, _, err := other.Reservations(ctx, "", "", 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross reservation list=%+v %v", rows, err)
	}
	events, _, err := other.Events(ctx, "", 50)
	if err != nil || len(events) != 0 {
		t.Fatalf("cross event list=%+v %v", events, err)
	}
	policies, _, err := other.List(ctx, "", 50)
	if err != nil || len(policies) != 0 {
		t.Fatalf("cross policy list=%+v %v", policies, err)
	}
}

func TestBudgetPatchPreservesCurrentSpendAndHistoricalLimit(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	f.accounting.SetClock(func() time.Time { return now })
	f.budgets.SetClock(func() time.Time { return now })
	p := createBudget(t, f, "tenant", "", 500, "enforce")
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: f.req.ID, Outcome: "known", Actual: 80}); err != nil {
		t.Fatal(err)
	}
	f.req.ID = "still-held"
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	limit := int64(150)
	patched, err := f.budgets.Patch(ctx, p.ID, billing.BudgetPatch{Version: 1, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if patched.Version != 2 || patched.Limit != 150 {
		t.Fatalf("patched=%+v", patched)
	}
	a := accountFor(t, f, p)
	if a.Committed != 80 || a.Reserved != 100 || a.Available != -30 {
		t.Fatalf("patch reset account=%+v", a)
	}
	if _, err := f.budgets.Patch(ctx, p.ID, billing.BudgetPatch{Version: 1, Limit: &limit}); !errors.Is(err, billing.ErrReservationConflict) {
		t.Fatalf("stale version=%v", err)
	}
	enabled := false
	if _, err := f.budgets.Patch(ctx, p.ID, billing.BudgetPatch{Version: 2, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	f.req.ID = "disabled-skips-budget"
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if a := accountFor(t, f, p); a.Reserved != 100 || a.Committed != 80 {
		t.Fatal("disabled policy changed old account")
	}
	enabled = true
	if _, err := f.budgets.Patch(ctx, p.ID, billing.BudgetPatch{Version: 3, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	f.req.ID = "reenabled-rejects"
	f.req.Estimate = 0
	if err := f.accounting.Reserve(ctx, f.req); !errors.Is(err, billing.ErrInsufficient) {
		t.Fatalf("reenable erased spend=%v", err)
	}
	now = now.AddDate(0, 0, 1)
	f.req.ID = "new-calendar-period"
	f.req.Estimate = 10
	if err := f.accounting.Reserve(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	limit = 200
	if _, err := f.budgets.Patch(ctx, p.ID, billing.BudgetPatch{Version: 4, Limit: &limit}); err != nil {
		t.Fatal(err)
	}
	accounts, err := f.budgets.Accounts(ctx, p.ID)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts=%+v %v", accounts, err)
	}
	if accounts[0].Limit != 200 || accounts[0].Reserved != 10 || accounts[1].Limit != 150 || accounts[1].Committed != 80 || accounts[1].Reserved != 100 {
		t.Fatalf("historical/current patch=%+v", accounts)
	}
}

func TestBudgetPaginationAndManualKnownSettlement(t *testing.T) {
	ctx := context.Background()
	f := newAccountingFixture(t)
	for i := 0; i < 3; i++ {
		createBudget(t, f, "tenant", "", 1000, "enforce")
	}
	first, next, err := f.budgets.List(ctx, "", 2)
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("first=%+v next=%s err=%v", first, next, err)
	}
	last, next, err := f.budgets.List(ctx, next, 2)
	if err != nil || len(last) != 1 || next != "" || last[0].ID <= first[1].ID {
		t.Fatalf("last=%+v next=%s err=%v", last, next, err)
	}
	if _, _, err := f.budgets.List(ctx, "not-valid", 2); !errors.Is(err, billing.ErrInvalidPolicy) {
		t.Fatal("invalid cursor accepted")
	}
	for i := 0; i < 3; i++ {
		req := f.req
		req.ID = "manual-" + strconv.Itoa(i)
		if err := f.accounting.Reserve(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := f.accounting.Finish(ctx, billing.Settlement{ReservationID: req.ID, Outcome: "unknown", Reason: "usage missing"}); err != nil {
			t.Fatal(err)
		}
	}
	rows, next, err := f.budgets.Reservations(ctx, "unknown", "", 2)
	if err != nil || len(rows) != 2 || next == "" {
		t.Fatalf("reservation page=%+v %s %v", rows, next, err)
	}
	remaining, next, err := f.budgets.Reservations(ctx, "unknown", next, 2)
	if err != nil || len(remaining) != 1 || next != "" {
		t.Fatalf("reservation tail=%+v %s %v", remaining, next, err)
	}
	actual := int64(125)
	resolved, err := f.budgets.Resolve(ctx, rows[0].ID, billing.Resolution{Version: rows[0].Version, Action: "settle", Actual: &actual, Reason: "reconciled provider bill", Evidence: "invoice line item", OperatorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "settled" || resolved.Actual == nil || *resolved.Actual != 125 {
		t.Fatalf("manual settle=%+v", resolved)
	}
	for _, p := range append(first, last...) {
		a := accountFor(t, f, p)
		if a.Reserved != 200 || a.Committed != 125 {
			t.Fatalf("manual accounting=%+v", a)
		}
	}
	events, next, err := f.budgets.Events(ctx, "", 2)
	if err != nil || len(events) != 2 || next == "" {
		t.Fatalf("event page=%+v %s %v", events, next, err)
	}
	more, next, err := f.budgets.Events(ctx, next, 2)
	if err != nil || len(more) != 2 || next != "" {
		t.Fatalf("event tail=%+v %s %v", more, next, err)
	}
	if more[1].OperatorID == nil || *more[1].OperatorID != 1 || more[1].Evidence != "invoice line item" {
		t.Fatalf("audit evidence missing=%+v", more[1])
	}
}

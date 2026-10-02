//go:build dbtest

package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

func budgetTenant(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	id, err := store.CreateTenant(context.Background(), db, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func budgetOperator(t *testing.T, h http.Handler, db *store.DB, name, scope string, tenantID *int64, permissions ...string) (string, int64) {
	t.Helper()
	seedRole(t, db, name, scope, permissions...)
	hash, err := operator.HashPassword("budget-test-password")
	if err != nil {
		t.Fatal(err)
	}
	op, err := store.NewOperatorRepo(db).Create(context.Background(), name+"@budget.test", hash, operator.Role(name), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	return login(t, h, name+"@budget.test", "budget-test-password"), op.ID
}

func createBudget(t *testing.T, h http.Handler, token, tenant string) billing.BudgetPolicy {
	t.Helper()
	rr := doAuth(t, h, token, http.MethodPost, "/api/v1/budgets?tenant="+tenant, billing.BudgetSpec{
		Name: "period-spend", ScopeKind: "tenant", Period: "monthly", Currency: "usd", Limit: 1000, Mode: "enforce", Thresholds: []int{50, 100},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create budget: %d %s", rr.Code, rr.Body.String())
	}
	var policy billing.BudgetPolicy
	if err := json.Unmarshal(rr.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestBudgetAPI_PermissionsAndTenantIsolation(t *testing.T) {
	h, db, root := authedAdmin(t)
	a := budgetTenant(t, db, "budget-a")
	budgetTenant(t, db, "budget-b")
	policy := createBudget(t, h, root, "budget-a")
	other := createBudget(t, h, root, "budget-b")
	reader, _ := budgetOperator(t, h, db, "budget-reader", "tenant", &a, "budget.read")
	writer, _ := budgetOperator(t, h, db, "budget-writer", "global", nil, "budget.write")
	globalReader, _ := budgetOperator(t, h, db, "budget-global-reader", "global", nil, "budget.read")
	tenantWriter, _ := budgetOperator(t, h, db, "budget-tenant-writer", "tenant", &a, "budget.read", "budget.write", "budget.resolve")
	noBudget, _ := budgetOperator(t, h, db, "budget-unrelated", "global", nil, "usage.read")

	for _, tc := range []struct {
		name, token, path string
		want              int
	}{
		{"own implicit", reader, "/api/v1/budgets", 200},
		{"own explicit", reader, "/api/v1/budgets?tenant=budget-a", 200},
		{"tenant override", reader, "/api/v1/budgets?tenant=budget-b", 403},
		{"own policy", reader, fmt.Sprintf("/api/v1/budgets/%d", policy.ID), 200},
		{"other policy", reader, fmt.Sprintf("/api/v1/budgets/%d", other.ID), 404},
		{"other accounts", reader, fmt.Sprintf("/api/v1/budgets/%d/accounts", other.ID), 404},
		{"budget only events", reader, "/api/v1/budget-events", 200},
		{"budget only reservations", reader, "/api/v1/billing-reservations", 200},
		{"global tenant mandatory", globalReader, "/api/v1/budgets", 400},
		{"global read", globalReader, "/api/v1/budgets?tenant=budget-a", 200},
		{"unknown tenant", globalReader, "/api/v1/budgets?tenant=missing", 404},
		{"write does not imply read", writer, "/api/v1/budgets?tenant=budget-a", 403},
		{"unrelated permission", noBudget, "/api/v1/budgets?tenant=budget-a", 403},
		{"invalid id", reader, "/api/v1/budgets/0", 400},
		{"invalid status", reader, "/api/v1/billing-reservations?status=bogus", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := doAuth(t, h, tc.token, http.MethodGet, tc.path, nil)
			if rr.Code != tc.want {
				t.Fatalf("status %d want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
	for _, token := range []string{reader, tenantWriter, globalReader} {
		for _, methodPath := range [][2]string{{http.MethodPost, "/api/v1/budgets?tenant=budget-a"}, {http.MethodPatch, fmt.Sprintf("/api/v1/budgets/%d?tenant=budget-a", policy.ID)}, {http.MethodPost, "/api/v1/billing-reservations/unknown/resolve?tenant=budget-a"}} {
			rr := doAuth(t, h, token, methodPath[0], methodPath[1], map[string]string{})
			if rr.Code != http.StatusForbidden {
				t.Fatalf("write must be forbidden: %d %s", rr.Code, rr.Body.String())
			}
		}
	}
	created := createBudget(t, h, writer, "budget-a")
	if created.TenantID != a {
		t.Fatalf("tenant = %d want %d", created.TenantID, a)
	}
}

func TestBudgetAPI_StrictValidationAndVersionedPatch(t *testing.T) {
	h, db, root := authedAdmin(t)
	budgetTenant(t, db, "budget-validation")
	for _, tc := range []struct {
		field string
		value any
	}{
		{"scope_kind", "global"}, {"scope_ref", "2"}, {"period", "yearly"}, {"timezone", "Local"},
		{"timezone", "Not/AZone"}, {"mode", "hard"}, {"currency", "USD"}, {"limit", -1},
		{"limit", nil}, {"thresholds", []int{0}}, {"thresholds", []int{101}}, {"thresholds", []int{50, 50}},
		{"thresholds", []float64{0.8}}, {"enabled", true}, {"tenant_id", 1}, {"environment", "prod"},
	} {
		t.Run(fmt.Sprintf("%s=%v", tc.field, tc.value), func(t *testing.T) {
			body := map[string]any{"name": "monthly", "scope_kind": "tenant", "period": "monthly", "currency": "usd", "limit": 100, "mode": "enforce"}
			body[tc.field] = tc.value
			rr := doAuth(t, h, root, http.MethodPost, "/api/v1/budgets?tenant=budget-validation", body)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "errors.budget.") {
				t.Fatalf("validation: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
	policy := createBudget(t, h, root, "budget-validation")
	path := fmt.Sprintf("/api/v1/budgets/%d?tenant=budget-validation", policy.ID)
	for _, body := range []map[string]any{{"version": 1}, {"version": 1, "currency": "cny"}, {"limit": 10}, {"version": 1, "enabled": nil}} {
		rr := doAuth(t, h, root, http.MethodPatch, path, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid patch: %d %s", rr.Code, rr.Body.String())
		}
	}
	rr := doAuth(t, h, root, http.MethodPatch, path, map[string]any{"version": policy.Version, "limit": 0, "enabled": false})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rr.Code, rr.Body.String())
	}
	var updated billing.BudgetPolicy
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Limit != 0 || updated.Enabled || updated.Version != policy.Version+1 {
		t.Fatalf("patch result: %+v", updated)
	}
	rr = doAuth(t, h, root, http.MethodPatch, path, map[string]any{"version": policy.Version, "limit": 100})
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale patch: %d %s", rr.Code, rr.Body.String())
	}
	var count int64
	if err := db.Raw(`SELECT count(*) FROM audit_logs WHERE resource_type='budget' AND resource_id=? AND tenant='budget-validation'`, fmt.Sprint(policy.ID)).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("successful mutations audited %d times, want 2", count)
	}
}

func TestBudgetAPI_ResolveUsesAuthenticatedActorAndPreservesUnknown(t *testing.T) {
	h, db, _ := authedAdmin(t)
	tenantID := budgetTenant(t, db, "budget-resolution")
	budgetTenant(t, db, "budget-resolution-other")
	resolver, operatorID := budgetOperator(t, h, db, "budget-resolver", "global", nil, "budget.resolve", "budget.read")
	identity, err := json.Marshal(billing.ReservationRequest{ID: "budget-resolution-r", RequestID: "request-r", Tenant: "budget-resolution", Currency: "usd", PriceSnapshot: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO billing_reservations (id,tenant_id,request_id,identity,status) VALUES ('budget-resolution-r',?,'request-r',?::jsonb,'unknown')`, tenantID, string(identity)).Error; err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/billing-reservations/budget-resolution-r/resolve?tenant=budget-resolution"
	for _, body := range []map[string]any{
		{"version": 1, "action": "settle", "actual": 10, "reason": "invoice", "evidence": "invoice-1", "operator_id": 9999},
		{"version": 1, "action": "settle", "reason": "invoice", "evidence": "invoice-1"},
		{"version": 1, "action": "release", "actual": 0, "reason": "invoice", "evidence": "invoice-1"},
		{"version": 1, "action": "release_unknown", "reason": "assumed", "evidence": ""},
	} {
		rr := doAuth(t, h, resolver, http.MethodPost, path, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid resolution: %d %s", rr.Code, rr.Body.String())
		}
	}
	body := map[string]any{"version": 1, "action": "release_unknown", "reason": "manual occupancy release", "evidence": "cost remains unknown; accepted for investigation"}
	rr := doAuth(t, h, resolver, http.MethodPost, path+"-other", body)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross tenant resolution: %d %s", rr.Code, rr.Body.String())
	}
	rr = doAuth(t, h, resolver, http.MethodPost, path, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("resolution: %d %s", rr.Code, rr.Body.String())
	}
	var reservation billing.Reservation
	if err := json.Unmarshal(rr.Body.Bytes(), &reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "released_unknown" || reservation.Actual != nil {
		t.Fatalf("unknown must remain unknown: %+v", reservation)
	}
	rr = doAuth(t, h, resolver, http.MethodPost, path, body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate resolution: %d %s", rr.Code, rr.Body.String())
	}
	var event struct {
		OperatorID       int64
		Reason, Evidence string
	}
	if err := db.Raw(`SELECT operator_id,reason,evidence FROM budget_events WHERE reservation_id='budget-resolution-r' AND kind='resolution_release_unknown'`).Scan(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.OperatorID != operatorID || event.Reason != body["reason"] || event.Evidence != body["evidence"] {
		t.Fatalf("event actor/evidence mismatch: %+v", event)
	}
}

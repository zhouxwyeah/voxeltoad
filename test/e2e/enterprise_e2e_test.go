//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/test/testsupport"
)

// These tests issue enterprise keys through the real admin API, then exercise
// the same PostgreSQL accounting coordinator as the production gateway.
func TestEnterprise_AttributionProtocolsAndDisablement(t *testing.T) {
	h := NewHarness(t, WithAuthCacheTTL(50*time.Millisecond))
	identity := enterpriseIdentity(t, h, "enterprise-a")
	other := enterpriseIdentity(t, h, "enterprise-b")

	nonstream := jsonUpstream("enterprise answer", 11, 7, nil)
	defer nonstream.Close()
	stream := sseUpstream("enterprise answer", 11, 7, nil)
	defer stream.Close()
	enterpriseRoute(h, "chat", nonstream.URL())
	enterpriseRoute(h, "stream", stream.URL())
	enterpriseAdmin(t, h, h.AdminToken, http.MethodPut, "/api/v1/gateway-settings", config.GatewaySettings{
		Trace: config.TraceSettings{CapturePayloadEnabled: true, MaxBodyKB: 64, RetentionDays: 7},
	}, http.StatusOK, nil)
	h.SyncConfig()

	var requestIDs []string
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, streaming), func(t *testing.T) {
				model := "chat"
				if streaming {
					model = "stream"
				}
				req := enterpriseRequest(t, h, identity.Key, protocol, model, streaming)
				// Caller-supplied attribution must not override the trusted key.
				req.Header.Set("X-Application-Id", strconv.FormatInt(other.ApplicationID, 10))
				req.Header.Set("X-Environment", "dev")
				req.Header.Set("X-Tenant", other.Tenant)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body := enterpriseResponse(t, resp, http.StatusOK)
				if !strings.Contains(string(body), "enterprise answer") {
					t.Fatalf("missing upstream content: %s", body)
				}
				if streaming {
					terminal := "[DONE]"
					if protocol == "anthropic" {
						terminal = "message_stop"
					}
					if !strings.Contains(string(body), terminal) {
						t.Fatalf("missing stream terminal %q: %s", terminal, body)
					}
				}
				requestID := resp.Header.Get("X-Request-Id")
				if requestID == "" {
					t.Fatal("gateway did not return a request ID")
				}
				requestIDs = append(requestIDs, requestID)
			})
		}
	}

	for _, table := range []string{"usage_records", "request_logs", "trace_payloads"} {
		enterpriseWaitRows(t, h, table, identity.Tenant, 4)
		var count int64
		if err := h.DB.Raw("SELECT count(*) FROM "+table+" WHERE tenant = ? AND application_id = ? AND environment = 'prod'", identity.Tenant, identity.ApplicationID).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 4 {
			t.Errorf("%s trusted application/prod snapshots = %d, want 4", table, count)
		}
	}
	filter := fmt.Sprintf("?application_id=%d&environment=prod", identity.ApplicationID)
	for _, path := range []string{"/api/v1/usage", "/api/v1/request-logs"} {
		var result struct {
			Data []enterpriseAttribution `json:"data"`
		}
		enterpriseAdmin(t, h, identity.Token, http.MethodGet, path+filter, nil, http.StatusOK, &result)
		if len(result.Data) != 4 {
			t.Fatalf("%s returned %d attributed rows, want 4", path, len(result.Data))
		}
		for _, row := range result.Data {
			if row.ApplicationID == nil || *row.ApplicationID != identity.ApplicationID || row.Environment != "prod" {
				t.Errorf("%s lost attribution: %+v", path, row)
			}
		}
		enterpriseAdmin(t, h, other.Token, http.MethodGet, path+filter, nil, http.StatusOK, &result)
		if len(result.Data) != 0 {
			t.Errorf("%s leaked tenant A records to tenant B", path)
		}
	}
	var tracePaths []string
	for _, requestID := range requestIDs {
		var trace struct{ ID int64 }
		if err := h.DB.Table("trace_payloads").Select("id").Where("tenant = ? AND request_id = ?", identity.Tenant, requestID).Take(&trace).Error; err != nil {
			t.Fatal(err)
		}
		// Chi-generated request IDs contain '/', so use the stable row-ID route.
		path := fmt.Sprintf("/api/v1/trace/rows/%d", trace.ID)
		tracePaths = append(tracePaths, path)
		var detail enterpriseAttribution
		enterpriseAdmin(t, h, identity.Token, http.MethodGet, path, nil, http.StatusOK, &detail)
		if detail.ApplicationID == nil || *detail.ApplicationID != identity.ApplicationID || detail.Environment != "prod" {
			t.Errorf("trace detail lost attribution: %+v", detail)
		}
		enterpriseAdmin(t, h, other.Token, http.MethodGet, path, nil, http.StatusNotFound, nil)
	}

	// Same-tenant ownership is validated on issuance, not merely at lookup.
	enterpriseAdmin(t, h, identity.Token, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "foreign-app-key", "group_id": identity.GroupID,
		"application_id": other.ApplicationID, "environment": "prod",
	}, http.StatusBadRequest, nil)
	enterpriseAdmin(t, h, identity.Token, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "foreign-group-key", "group_id": other.GroupID,
		"application_id": identity.ApplicationID, "environment": "prod",
	}, http.StatusBadRequest, nil)

	var patched struct {
		Enabled bool `json:"enabled"`
	}
	enterpriseAdmin(t, h, identity.Token, http.MethodPatch, "/api/v1/applications/workload", map[string]bool{"enabled": false}, http.StatusOK, &patched)
	if patched.Enabled {
		t.Fatal("application PATCH did not return the disabled record")
	}
	waitFor(t, 2*time.Second, func() bool {
		resp := h.Chat(identity.Key, "chat", false)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("disabled application response = %d, want cached 200 or 401", resp.StatusCode)
		}
		return resp.StatusCode == http.StatusUnauthorized
	}, "application disablement after auth cache TTL")
	// Previously captured identity remains queryable after disablement.
	for _, path := range tracePaths {
		var detail enterpriseAttribution
		enterpriseAdmin(t, h, identity.Token, http.MethodGet, path, nil, http.StatusOK, &detail)
		if detail.ApplicationID == nil || *detail.ApplicationID != identity.ApplicationID || detail.Environment != "prod" {
			t.Errorf("disablement changed a historic trace: %+v", detail)
		}
	}
}

func TestEnterprise_LegacyKeyBindingPreservesHistory(t *testing.T) {
	h := NewHarness(t, WithAuthCacheTTL(time.Nanosecond))
	identity := enterpriseIdentity(t, h, "enterprise-legacy")
	up := jsonUpstream("legacy-compatible", 11, 7, nil)
	defer up.Close()
	enterpriseRoute(h, "chat", up.URL())
	h.SeedKey("sk-legacy-enterprise", identity.Tenant, "legacy-group", "key-legacy", nil)
	h.SyncConfig()

	var unbound struct {
		Data []struct {
			KeyID   string `json:"key_id"`
			GroupID int64  `json:"group_id"`
		} `json:"data"`
	}
	enterpriseAdmin(t, h, identity.Token, http.MethodGet, "/api/v1/api-keys?unbound=true", nil, http.StatusOK, &unbound)
	if len(unbound.Data) != 1 || unbound.Data[0].KeyID != "key-legacy" {
		t.Fatalf("unbound key listing = %+v, want only the legacy key", unbound.Data)
	}
	enterpriseResponse(t, h.Chat("sk-legacy-enterprise", "chat", false), http.StatusOK)
	enterpriseWaitRows(t, h, "usage_records", identity.Tenant, 1)

	// Binding completes missing identity; it must not replace the legacy consumer group.
	legacyGroupID := unbound.Data[0].GroupID
	if legacyGroupID == 0 {
		t.Fatal("legacy key omitted its existing consumer group")
	}
	binding := map[string]any{"group_id": legacyGroupID, "application_id": identity.ApplicationID, "environment": "prod"}
	enterpriseAdmin(t, h, identity.Token, http.MethodPatch, "/api/v1/api-keys/key-legacy", binding, http.StatusNoContent, nil)
	binding["environment"] = "staging"
	enterpriseAdmin(t, h, identity.Token, http.MethodPatch, "/api/v1/api-keys/key-legacy", binding, http.StatusBadRequest, nil)
	enterpriseAdmin(t, h, identity.Token, http.MethodGet, "/api/v1/api-keys?unbound=true", nil, http.StatusOK, &unbound)
	if len(unbound.Data) != 0 {
		t.Error("bound legacy key remains in unbound listing")
	}
	enterpriseResponse(t, h.Chat("sk-legacy-enterprise", "chat", false), http.StatusOK)
	enterpriseWaitRows(t, h, "usage_records", identity.Tenant, 2)
	for _, tc := range []struct {
		filter string
		bound  bool
	}{
		{"?unattributed=true", false},
		{fmt.Sprintf("?application_id=%d&environment=prod", identity.ApplicationID), true},
	} {
		var result struct {
			Data []enterpriseAttribution `json:"data"`
		}
		enterpriseAdmin(t, h, identity.Token, http.MethodGet, "/api/v1/usage"+tc.filter, nil, http.StatusOK, &result)
		if len(result.Data) != 1 {
			t.Fatalf("legacy history filter %s returned %d rows, want 1", tc.filter, len(result.Data))
		}
		row := result.Data[0]
		if row.GroupName != "legacy-group" {
			t.Errorf("binding changed the legacy consumer group: %+v", row)
		}
		if tc.bound {
			if row.ApplicationID == nil || *row.ApplicationID != identity.ApplicationID || row.Environment != "prod" {
				t.Errorf("new request lost completed identity: %+v", row)
			}
		} else if row.ApplicationID != nil || row.Environment != "" {
			t.Errorf("historical usage was retroactively attributed: %+v", row)
		}
	}
}

func TestEnterprise_BudgetReserveSettleThresholdAndExhaustion(t *testing.T) {
	h := NewHarness(t)
	identity := enterpriseIdentity(t, h, "enterprise-budget")
	other := enterpriseIdentity(t, h, "enterprise-other")
	var hits atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	up := testsupport.NewMockUpstream(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"budget","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"paid"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	})
	defer up.Close()
	defer unblock()
	enterpriseRoute(h, "chat", up.URL())
	h.SetQuota("tenant:"+identity.Tenant, 10_000)
	groupScope := "group:" + identity.Tenant + "/consumers"
	h.SetQuota(groupScope, 10_000)
	h.SyncConfig()

	var policies []billing.BudgetPolicy
	for _, scope := range []struct{ kind, ref, environment string }{
		{"tenant", "", ""},
		{"group", strconv.FormatInt(identity.GroupID, 10), ""},
		{"application", strconv.FormatInt(identity.ApplicationID, 10), ""},
		{"application_env", strconv.FormatInt(identity.ApplicationID, 10), "prod"},
		{"key", identity.KeyID, ""},
	} {
		var policy billing.BudgetPolicy
		enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, "/api/v1/budgets?tenant="+identity.Tenant, billing.BudgetSpec{
			Name: "cap-" + scope.kind, ScopeKind: scope.kind, ScopeRef: scope.ref, Environment: scope.environment,
			Period: "daily", Timezone: "UTC", Currency: "usd", Limit: 20, Mode: "enforce", Thresholds: []int{50, 100},
		}, http.StatusCreated, &policy)
		policies = append(policies, policy)
	}

	req := enterpriseRequest(t, h, identity.Key, "openai", "chat", false)
	type result struct {
		response *http.Response
		err      error
	}
	finished := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		finished <- result{resp, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the mock upstream")
	}
	for _, policy := range policies {
		account := enterpriseAccount(t, h, identity.Token, policy.ID)
		if account.Reserved != 2 || account.Committed != 0 || account.Available != 18 {
			t.Errorf("%s in-flight account = %+v, want reserved 2 / committed 0 / available 18", policy.ScopeKind, account)
		}
	}
	if got := h.Balance("tenant:" + identity.Tenant); got != 9_998 {
		t.Errorf("legacy quota not reserved in same transaction: %d", got)
	}
	unblock()
	select {
	case got := <-finished:
		if got.err != nil {
			t.Fatal(got.err)
		}
		enterpriseResponse(t, got.response, http.StatusOK)
	case <-time.After(3 * time.Second):
		t.Fatal("released upstream request did not complete")
	}

	// Actual usage exceeds the output-only estimate; it is still booked in full.
	// This is period spend control, not a strict upper bound on real cost.
	waitFor(t, 3*time.Second, func() bool {
		return enterpriseAccount(t, h, identity.Token, policies[0].ID).Committed == 25
	}, "actual budget settlement")
	for _, policy := range policies {
		account := enterpriseAccount(t, h, identity.Token, policy.ID)
		if account.Reserved != 0 || account.Committed != 25 || account.Available != -5 {
			t.Errorf("%s settled account = %+v, want reserved 0 / committed 25 / available -5", policy.ScopeKind, account)
		}
		enterpriseAdmin(t, h, other.Token, http.MethodGet, fmt.Sprintf("/api/v1/budgets/%d/accounts", policy.ID), nil, http.StatusNotFound, nil)
	}
	for _, scope := range []string{"tenant:" + identity.Tenant, groupScope} {
		if got := h.Balance(scope); got != 9_975 {
			t.Errorf("%s balance = %d, want exact once-only debit 25", scope, got)
		}
	}
	var events struct {
		Data []billing.BudgetEvent `json:"data"`
	}
	enterpriseAdmin(t, h, identity.Token, http.MethodGet, "/api/v1/budget-events", nil, http.StatusOK, &events)
	thresholds := map[string]bool{}
	for _, event := range events.Data {
		if event.Kind != "threshold" {
			continue
		}
		if event.AccountID == nil {
			t.Fatal("threshold event has no account")
		}
		key := fmt.Sprintf("%d/%d", *event.AccountID, event.Threshold)
		if thresholds[key] {
			t.Errorf("duplicate threshold event %s", key)
		}
		thresholds[key] = true
	}
	if len(thresholds) != 10 {
		t.Errorf("threshold events = %d, want two per each of five accounts", len(thresholds))
	}

	resp, err := http.DefaultClient.Do(enterpriseRequest(t, h, identity.Key, "openai", "chat", false))
	if err != nil {
		t.Fatal(err)
	}
	enterpriseResponse(t, resp, http.StatusPaymentRequired)
	if hits.Load() != 1 {
		t.Errorf("exhausted budget reached upstream: hits=%d", hits.Load())
	}
	enterpriseWaitRows(t, h, "usage_records", identity.Tenant, 1)
	enterpriseWaitRows(t, h, "request_logs", identity.Tenant, 2)
	var reservations struct {
		Data []billing.Reservation `json:"data"`
	}
	enterpriseAdmin(t, h, identity.Token, http.MethodGet, "/api/v1/billing-reservations", nil, http.StatusOK, &reservations)
	if len(reservations.Data) != 1 {
		t.Fatalf("reservations = %+v, rejected request must not create a partial reservation", reservations.Data)
	}
	r := reservations.Data[0]
	if r.Status != "settled" || r.Estimate != 2 || r.Actual == nil || *r.Actual != 25 || r.ApplicationID == nil || *r.ApplicationID != identity.ApplicationID || r.Environment != "prod" {
		t.Errorf("reservation lost settlement/identity: %+v", r)
	}
	// Tenant admins can read budgets but cannot change funding policy.
	enterpriseAdmin(t, h, identity.Token, http.MethodPost, "/api/v1/budgets", billing.BudgetSpec{}, http.StatusForbidden, nil)
	enterpriseAdmin(t, h, other.Token, http.MethodGet, "/api/v1/billing-reservations", nil, http.StatusOK, &reservations)
	if len(reservations.Data) != 0 {
		t.Error("reservation list leaked another tenant's accounting")
	}
}

func TestEnterprise_HTTP5xxRetainsUnknown(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback=%t", fallback), func(t *testing.T) {
			h := NewHarness(t)
			identity := enterpriseIdentity(t, h, "enterprise-5xx")
			bad := testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `{"error":"provider timed out after accepting the request"}`)
			})
			defer bad.Close()
			good := jsonUpstream("fallback answer", 11, 7, nil)
			defer good.Close()
			h.AddProvider("bad", bad.URL(), "plain://test")
			h.AddProvider("good", good.URL(), "plain://test")
			h.AddModel("chat", 1_000_000, 2_000_000,
				config.ModelUpstream{Provider: "bad", UpstreamModel: "m"},
				config.ModelUpstream{Provider: "good", UpstreamModel: "m"})
			providers := []config.RouteProvider{{Name: "bad"}}
			if fallback {
				providers = append(providers, config.RouteProvider{Name: "good"})
			}
			h.AddRoute("chat", "priority", providers...)
			h.SetQuota("tenant:"+identity.Tenant, 10_000)
			h.SyncConfig()
			var policy billing.BudgetPolicy
			enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, "/api/v1/budgets?tenant="+identity.Tenant, billing.BudgetSpec{
				Name: "failed-attempt", ScopeKind: "tenant", Period: "daily", Currency: "usd", Limit: 20, Mode: "enforce", Thresholds: []int{},
			}, http.StatusCreated, &policy)
			resp, err := http.DefaultClient.Do(enterpriseRequest(t, h, identity.Key, "openai", "chat", false))
			if err != nil {
				t.Fatal(err)
			}
			status := http.StatusBadGateway
			if fallback {
				status = http.StatusOK
			}
			enterpriseResponse(t, resp, status)
			enterpriseWaitRows(t, h, "request_logs", identity.Tenant, 1)
			var pending struct {
				Data []billing.Reservation `json:"data"`
			}
			enterpriseAdmin(t, h, identity.Token, http.MethodGet, "/api/v1/billing-reservations", nil, http.StatusOK, &pending)
			if len(pending.Data) != 1 {
				t.Fatalf("reservations = %+v", pending.Data)
			}
			account := enterpriseAccount(t, h, identity.Token, policy.ID)
			var usages int64
			if err := h.DB.Table("usage_records").Where("tenant = ?", identity.Tenant).Count(&usages).Error; err != nil {
				t.Fatal(err)
			}
			if !fallback {
				// The 5xx is the final outcome: the charge is unconfirmed, so the
				// reservation stays unknown and keeps its hold for manual review.
				if pending.Data[0].Status != "unknown" || pending.Data[0].Actual != nil {
					t.Fatalf("terminal 5xx must retain unknown charge: %+v", pending.Data)
				}
				if account.Reserved != 2 || account.Committed != 0 || h.Balance("tenant:"+identity.Tenant) != 9_998 {
					t.Fatalf("unknown attempt released funds: %+v", account)
				}
				if usages != 0 {
					t.Fatalf("unconfirmed total charge created %d usage records", usages)
				}
				return
			}
			// Failover succeeded: the final usage settles the reservation as
			// known, and the earlier attempt's residual exposure is persisted
			// as an attempt-risk event instead of an unknown hold.
			if pending.Data[0].Status != "settled" || pending.Data[0].Actual == nil || *pending.Data[0].Actual != 25 {
				t.Fatalf("successful failover must settle its known usage: %+v", pending.Data)
			}
			if account.Reserved != 0 || account.Committed != 25 || h.Balance("tenant:"+identity.Tenant) != 9_975 {
				t.Fatalf("successful failover settlement: %+v", account)
			}
			if usages != 1 {
				t.Fatalf("known settlement persisted %d usage rows, want 1", usages)
			}
			var risks int64
			if err := h.DB.Raw(`SELECT count(*) FROM budget_events e JOIN billing_reservations r ON r.id=e.reservation_id
				WHERE e.kind='attempt_risk' AND r.tenant_id=(SELECT id FROM tenants WHERE name=?)`, identity.Tenant).Scan(&risks).Error; err != nil {
				t.Fatal(err)
			}
			if risks != 1 {
				t.Fatalf("attempt-risk events = %d, want 1", risks)
			}
		})
	}
}

func TestEnterprise_UnknownUsageRetainedAndReconciled(t *testing.T) {
	h := NewHarness(t)
	identity := enterpriseIdentity(t, h, "enterprise-unknown")
	up := testsupport.NewMockUpstream(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A successful completion without usage does not establish its charge.
		_, _ = io.WriteString(w, `{"id":"unknown-usage","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"usage unavailable"},"finish_reason":"stop"}]}`)
	})
	defer up.Close()
	enterpriseRoute(h, "chat", up.URL())
	h.SetQuota("tenant:"+identity.Tenant, 10_000)
	h.SyncConfig()
	var policy billing.BudgetPolicy
	enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, "/api/v1/budgets?tenant="+identity.Tenant, billing.BudgetSpec{
		Name: "unknown-cost", ScopeKind: "application", ScopeRef: strconv.FormatInt(identity.ApplicationID, 10),
		Period: "weekly", Timezone: "UTC", Currency: "usd", Limit: 20, Mode: "enforce", Thresholds: []int{100},
	}, http.StatusCreated, &policy)
	resp, err := http.DefaultClient.Do(enterpriseRequest(t, h, identity.Key, "openai", "chat", false))
	if err != nil {
		t.Fatal(err)
	}
	body := enterpriseResponse(t, resp, http.StatusOK)
	if !strings.Contains(string(body), "usage unavailable") {
		t.Fatalf("missing successful completion content: %s", body)
	}
	enterpriseWaitRows(t, h, "request_logs", identity.Tenant, 1)
	var pending struct {
		Data []billing.Reservation `json:"data"`
	}
	enterpriseAdmin(t, h, identity.Token, http.MethodGet, "/api/v1/billing-reservations?status=unknown", nil, http.StatusOK, &pending)
	if len(pending.Data) != 1 || pending.Data[0].Reason == "" {
		t.Fatalf("successful completion without usage must retain a reasoned unknown reservation: %+v", pending.Data)
	}
	account := enterpriseAccount(t, h, identity.Token, policy.ID)
	if account.Reserved != 2 || account.Committed != 0 || account.Available != 18 {
		t.Errorf("unknown usage was silently refunded: %+v", account)
	}
	path := "/api/v1/billing-reservations/" + pending.Data[0].ID + "/resolve?tenant=" + identity.Tenant
	var resolved billing.Reservation
	riskRelease := billing.Resolution{
		Version: pending.Data[0].Version, Action: "release_unknown",
		Reason: "operator accepts unresolved upstream charge", Evidence: "test-only upstream incident record",
	}
	enterpriseAdmin(t, h, identity.Token, http.MethodPost, path, riskRelease, http.StatusForbidden, nil)
	enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, path, riskRelease, http.StatusOK, &resolved)
	if resolved.Status != "released_unknown" {
		t.Fatalf("risk release erased uncertainty: %+v", resolved)
	}
	account = enterpriseAccount(t, h, identity.Token, policy.ID)
	if account.Reserved != 0 || account.Committed != 0 || account.Available != 20 || h.Balance("tenant:"+identity.Tenant) != 10_000 {
		t.Errorf("risk release did not free only the outstanding reservation: %+v", account)
	}
	actual := int64(25)
	settlement := billing.Resolution{
		Version: resolved.Version, Action: "settle", Actual: &actual,
		Reason: "provider charge confirmed", Evidence: "test-only settlement statement: usd micros 25",
	}
	enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, path, settlement, http.StatusOK, &resolved)
	enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, path, settlement, http.StatusConflict, nil)
	account = enterpriseAccount(t, h, identity.Token, policy.ID)
	if resolved.Status != "settled" || account.Reserved != 0 || account.Committed != 25 || account.Available != -5 || h.Balance("tenant:"+identity.Tenant) != 9_975 {
		t.Errorf("post-release actual charge was not booked exactly once: reservation=%+v account=%+v", resolved, account)
	}
	var usages int64
	if err := h.DB.Table("usage_records").Count(&usages).Error; err != nil {
		t.Fatal(err)
	}
	if usages != 0 {
		t.Errorf("unknown/risk-released/manual-cost request invented token usage: %d rows", usages)
	}
}

func TestEnterprise_BudgetSoftAndCurrencyMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, mode, currency string
		limit                int64
		status               int
	}{
		{"soft-over-limit", "soft", "usd", 1, http.StatusOK},
		{"foreign-currency", "enforce", "eur", 100, http.StatusServiceUnavailable},
		{"zero-budget", "enforce", "usd", 0, http.StatusPaymentRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHarness(t)
			identity := enterpriseIdentity(t, h, "enterprise-policy")
			up := jsonUpstream("accounted", 11, 7, nil)
			defer up.Close()
			enterpriseRoute(h, "chat", up.URL())
			h.SetQuota("tenant:"+identity.Tenant, 10_000)
			h.SyncConfig()
			var policy billing.BudgetPolicy
			enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, "/api/v1/budgets?tenant="+identity.Tenant, billing.BudgetSpec{
				Name: tc.name, ScopeKind: "application", ScopeRef: strconv.FormatInt(identity.ApplicationID, 10),
				Period: "monthly", Timezone: "UTC", Currency: tc.currency, Limit: tc.limit, Mode: tc.mode, Thresholds: []int{100},
			}, http.StatusCreated, &policy)
			resp, err := http.DefaultClient.Do(enterpriseRequest(t, h, identity.Key, "openai", "chat", false))
			if err != nil {
				t.Fatal(err)
			}
			enterpriseResponse(t, resp, tc.status)
			if tc.status == http.StatusOK {
				waitFor(t, 3*time.Second, func() bool {
					return enterpriseAccount(t, h, identity.Token, policy.ID).Committed == 25
				}, "soft budget settlement")
				account := enterpriseAccount(t, h, identity.Token, policy.ID)
				if account.Available != -24 || account.Reserved != 0 {
					t.Errorf("soft account = %+v, want -24 available", account)
				}
				return
			}
			enterpriseWaitRows(t, h, "request_logs", identity.Tenant, 1)
			for _, table := range []string{"usage_records", "billing_reservations"} {
				var n int64
				if err := h.DB.Table(table).Count(&n).Error; err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Errorf("rejected request fabricated %s rows: %d", table, n)
				}
			}
			if got := h.Balance("tenant:" + identity.Tenant); got != 10_000 {
				t.Errorf("failed reserve partially debited legacy balance: %d", got)
			}
		})
	}
}

type enterpriseAttribution struct {
	GroupName     string `json:"group_name"`
	ApplicationID *int64 `json:"application_id"`
	Environment   string `json:"environment"`
}

type enterpriseFixture struct {
	Tenant, Token, Key, KeyID        string
	TenantID, GroupID, ApplicationID int64
}

func enterpriseIdentity(t *testing.T, h *Harness, tenant string) enterpriseFixture {
	t.Helper()
	var tenantResult struct {
		ID int64 `json:"id"`
	}
	enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, "/api/v1/tenants", map[string]string{"name": tenant}, http.StatusCreated, &tenantResult)
	email, password := tenant+"@enterprise.test", "enterprise-test-password"
	enterpriseAdmin(t, h, h.AdminToken, http.MethodPost, "/api/v1/operators", map[string]any{
		"email": email, "password": password, "role": "tenant-admin", "tenant_id": tenantResult.ID,
	}, http.StatusCreated, nil)
	var session struct {
		Token string `json:"token"`
	}
	enterpriseAdmin(t, h, "", http.MethodPost, "/auth/login", map[string]string{"email": email, "password": password}, http.StatusOK, &session)
	var consumer struct {
		ID int64 `json:"id"`
	}
	enterpriseAdmin(t, h, session.Token, http.MethodPost, "/api/v1/groups", map[string]string{"name": "owners"}, http.StatusCreated, nil)
	enterpriseAdmin(t, h, session.Token, http.MethodPost, "/api/v1/groups", map[string]string{"name": "consumers"}, http.StatusCreated, &consumer)
	var application struct {
		ID           int64 `json:"id"`
		OwnerGroupID int64 `json:"owner_group_id"`
	}
	enterpriseAdmin(t, h, session.Token, http.MethodPost, "/api/v1/applications", map[string]string{"name": "workload", "owner_group": "owners"}, http.StatusCreated, &application)
	if application.OwnerGroupID == 0 || application.OwnerGroupID == consumer.ID {
		t.Fatal("application owner group must differ from the key's consumer group")
	}
	keyID := "key-" + tenant
	var key struct {
		Key string `json:"api_key"`
	}
	enterpriseAdmin(t, h, session.Token, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": keyID, "group_id": consumer.ID, "application_id": application.ID, "environment": "prod",
	}, http.StatusCreated, &key)
	if key.Key == "" || application.ID == 0 || consumer.ID == 0 {
		t.Fatal("enterprise issuance omitted key or governance identity")
	}
	return enterpriseFixture{tenant, session.Token, key.Key, keyID, tenantResult.ID, consumer.ID, application.ID}
}

func enterpriseRoute(h *Harness, alias, upstreamURL string) {
	h.t.Helper()
	h.AddProvider(alias, upstreamURL, "plain://enterprise-test-only")
	h.AddModel(alias, 1_000_000, 2_000_000, config.ModelUpstream{Provider: alias, UpstreamModel: "m"})
	h.AddRoute(alias, "priority", config.RouteProvider{Name: alias})
}

func enterpriseRequest(t *testing.T, h *Harness, key, protocol, model string, stream bool) *http.Request {
	t.Helper()
	path := "/v1/chat/completions"
	if protocol == "anthropic" {
		path = "/v1/messages"
	}
	// The mock deliberately returns a larger actual usage than this output
	// estimate, exercising permitted post-dispatch overspend.
	body, err := json.Marshal(map[string]any{
		"model": model, "stream": stream, "max_tokens": 1,
		"messages": []map[string]string{{"role": "user", "content": "enterprise prompt"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, h.GatewayURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if protocol == "anthropic" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req
}

func enterpriseAdmin(t *testing.T, h *Harness, token, method, path string, payload any, status int, dst any) {
	t.Helper()
	var input []byte
	var err error
	if payload != nil {
		input, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, h.AdminURL+path, bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	body := enterpriseResponse(t, resp, status)
	if dst != nil {
		if err := json.Unmarshal(body, dst); err != nil {
			t.Fatalf("decode %s %s: %v; body=%s", method, path, err, body)
		}
	}
}

func enterpriseResponse(t *testing.T, resp *http.Response, status int) []byte {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", resp.Request.URL.Path, err)
	}
	if resp.StatusCode != status {
		t.Fatalf("%s %s status=%d want=%d; body=%s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, status, body)
	}
	return body
}

func enterpriseAccount(t *testing.T, h *Harness, token string, policyID int64) billing.BudgetAccount {
	t.Helper()
	var response struct {
		Data []billing.BudgetAccount `json:"data"`
	}
	enterpriseAdmin(t, h, token, http.MethodGet, fmt.Sprintf("/api/v1/budgets/%d/accounts", policyID), nil, http.StatusOK, &response)
	if len(response.Data) != 1 {
		t.Fatalf("policy %d accounts=%+v, want one current period", policyID, response.Data)
	}
	return response.Data[0]
}

func enterpriseWaitRows(t *testing.T, h *Harness, table, tenant string, count int64) {
	t.Helper()
	waitFor(t, 3*time.Second, func() bool {
		var n int64
		if err := h.DB.Table(table).Where("tenant = ?", tenant).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n == count
	}, fmt.Sprintf("%s tenant=%s count=%d", table, tenant, count))
}

//go:build dbtest

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

func TestAttributionAPI_ScopePermissionsAndTimeRange(t *testing.T) {
	h, db, root := authedAdmin(t)
	ctx := context.Background()
	tenantID, err := store.CreateTenant(ctx, db, "attribution-a")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := store.CreateTenant(ctx, db, "attribution-b")
	if err != nil {
		t.Fatal(err)
	}
	seedTenantAdmin(t, db, "attribution@tenant", "attribution-password", tenantID)
	tenantToken := login(t, h, "attribution@tenant", "attribution-password")
	seedRole(t, db, "application-only", "tenant", "application.read", "group.read")
	hash, err := operator.HashPassword("attribution-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewOperatorRepo(db).Create(ctx, "application-only@tenant", hash, operator.Role("application-only"), &tenantID); err != nil {
		t.Fatal(err)
	}
	noUsageToken := login(t, h, "application-only@tenant", "attribution-password")
	seedRole(t, db, "attribution-reader", "global", "usage.read")
	if _, err := store.NewOperatorRepo(db).Create(ctx, "attribution-reader@global", hash, operator.Role("attribution-reader"), nil); err != nil {
		t.Fatal(err)
	}
	globalReader := login(t, h, "attribution-reader@global", "attribution-password")
	for _, row := range []struct {
		tenantID int64
		key      string
	}{{tenantID, "a"}, {otherID, "b1"}, {otherID, "b2"}} {
		if err := store.NewTenantRepo(db, row.tenantID).CreateAPIKey(ctx, store.APIKeySpec{KeyID: row.key, Hash: row.key}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	if err := db.Exec(`INSERT INTO request_logs (tenant, application_id, created_at) VALUES
		('attribution-a', NULL, ?), ('attribution-a', 901, ?), ('attribution-b', NULL, ?)`, at, at, at).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO usage_records (tenant, api_key_id, provider, model, currency, cost, created_at) VALUES
		('attribution-a', 'a', 'provider', 'model', 'USD', 100, ?),
		('attribution-a', 'a', 'provider', 'model', 'CNY', 200, ?),
		('attribution-b', 'b1', 'provider', 'model', 'USD', 9999, ?)`, at, at, at).Error; err != nil {
		t.Fatal(err)
	}
	window := "&from=" + url.QueryEscape(at.Format(time.RFC3339)) + "&to=" + url.QueryEscape(at.Add(time.Hour).Format(time.RFC3339))
	for _, tc := range []struct {
		name, token, query string
		status             int
	}{
		{"unauthenticated", "", "?tenant=attribution-a", http.StatusUnauthorized},
		{"global reader needs tenant", globalReader, "", http.StatusBadRequest},
		{"global reader explicit", globalReader, "?tenant=attribution-a" + window, http.StatusOK},
		{"global needs tenant", root, "", http.StatusBadRequest},
		{"global unknown tenant", root, "?tenant=unknown", http.StatusBadRequest},
		{"global explicit tenant", root, "?tenant=attribution-a" + window, http.StatusOK},
		{"tenant override ignored", tenantToken, "?tenant=attribution-b" + window, http.StatusOK},
		{"tenant implicit", tenantToken, "?from=" + url.QueryEscape(at.Format(time.RFC3339)), http.StatusOK},
		{"missing usage permission", noUsageToken, "", http.StatusForbidden},
		{"bad from", tenantToken, "?from=bad", http.StatusBadRequest},
		{"bad to", tenantToken, "?to=bad", http.StatusBadRequest},
		{"reversed range", tenantToken, "?from=2026-07-06T00:00:00Z&to=2026-07-05T00:00:00Z", http.StatusBadRequest},
		{"empty range", tenantToken, "?from=2026-07-05T00:00:00Z&to=2026-07-05T00:00:00Z", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := doAuth(t, h, tc.token, http.MethodGet, "/api/v1/usage/attribution"+tc.query, nil)
			if rr.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.status, rr.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var got store.AttributionSummary
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.UnboundKeyCount != 1 || got.RequestCount != 2 || got.UnattributedRequestCount != 1 || got.UnattributedRequestRatio != 0.5 || len(got.RecordedUnattributedCosts) != 2 {
				t.Fatalf("wrong tenant or attribution: %+v", got)
			}
			for _, cost := range got.RecordedUnattributedCosts {
				if cost.Cost != map[string]int64{"USD": 100, "CNY": 200}[cost.Currency] {
					t.Fatalf("currency mixed or tenant leaked: %+v", cost)
				}
			}
		})
	}
	for _, path := range []string{"/api/v1/applications", "/api/v1/groups"} {
		rr := doAuth(t, h, noUsageToken, http.MethodGet, path, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("no usage.read must not block %s: %d %s", path, rr.Code, rr.Body.String())
		}
	}
}

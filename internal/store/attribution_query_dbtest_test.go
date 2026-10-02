//go:build dbtest

package store_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

func TestAttributionQuery_Summary(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, err := store.CreateTenant(ctx, db, "attribution-a")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := store.CreateTenant(ctx, db, "attribution-b")
	if err != nil {
		t.Fatal(err)
	}
	keys := store.NewTenantRepo(db, tenantID)
	groupID, err := keys.CreateGroup(ctx, "owners")
	if err != nil {
		t.Fatal(err)
	}
	appID, err := store.NewApplicationRepo(db, tenantID).Create(ctx, "app", "owners")
	if err != nil {
		t.Fatal(err)
	}
	for i, identity := range []struct {
		group, app *int64
		env        string
	}{
		{}, {nil, &appID, "prod"}, {&groupID, nil, "prod"}, {&groupID, &appID, ""},
		{&groupID, &appID, "prod"}, {}, {},
	} {
		if err := keys.CreateAPIKey(ctx, store.APIKeySpec{
			KeyID: fmt.Sprintf("key-%d", i), Hash: fmt.Sprintf("%064d", i),
			GroupID: identity.group, ApplicationID: identity.app, Environment: identity.env,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := keys.RevokeAPIKey(ctx, "key-5"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 day' WHERE key_id = 'key-6'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.NewTenantRepo(db, otherID).CreateAPIKey(ctx, store.APIKeySpec{KeyID: "other", Hash: "other-hash"}); err != nil {
		t.Fatal(err)
	}
	// Disabled entities and expired keys are still migration debt, matching the
	// existing unbound-key list rather than claiming these keys are callable.
	if _, err := store.SetTenantEnabled(ctx, db, "attribution-a", false); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	for _, row := range []struct {
		tenant string
		app    *int64
		at     time.Time
	}{
		{"attribution-a", nil, from},
		{"attribution-a", &appID, from.Add(time.Minute)},
		{"attribution-a", nil, from.Add(2 * time.Minute)},
		{"attribution-a", nil, from.Add(3 * time.Minute)},
		{"attribution-a", nil, from.Add(-time.Second)},
		{"attribution-a", nil, to},
		{"attribution-b", nil, from},
	} {
		if err := db.Exec(`INSERT INTO request_logs (tenant, application_id, created_at) VALUES (?, ?, ?)`, row.tenant, row.app, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		tenant, currency string
		app              *int64
		cost             int64
		at               time.Time
	}{
		{"attribution-a", "USD", nil, 100, from},
		{"attribution-a", "USD", nil, 200, from.Add(time.Minute)},
		{"attribution-a", "CNY", nil, 400, from},
		{"attribution-a", "", nil, 50, from},
		{"attribution-a", "USD", &appID, 900, from},
		{"attribution-a", "USD", nil, 800, from.Add(-time.Second)},
		{"attribution-a", "USD", nil, 700, to},
		{"attribution-b", "USD", nil, 9999, from},
	} {
		if err := db.Exec(`INSERT INTO usage_records (tenant, api_key_id, provider, model, application_id, currency, cost, created_at)
			VALUES (?, 'key', 'provider', 'model', ?, ?, ?, ?)`, row.tenant, row.app, row.currency, row.cost, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := store.NewAttributionQueryRepo(db, "attribution-a")
	got, err := repo.Summary(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got.UnboundKeyCount != 5 || got.RequestCount != 4 || got.UnattributedRequestCount != 3 || got.UnattributedRequestRatio != 0.75 {
		t.Fatalf("wrong counts or scope: %+v", got)
	}
	wantCosts := []store.CurrencyCost{{Currency: "", Cost: 50}, {Currency: "CNY", Cost: 400}, {Currency: "USD", Cost: 300}}
	if !reflect.DeepEqual(got.RecordedUnattributedCosts, wantCosts) {
		t.Fatalf("currency totals = %+v, want %+v", got.RecordedUnattributedCosts, wantCosts)
	}
	if got.From == nil || got.To == nil || !got.From.Equal(from) || !got.To.Equal(to) {
		t.Fatalf("wrong time bounds: %+v", got)
	}
	unbound, _, err := keys.ListAPIKeysFiltered(ctx, "", 100, true)
	if err != nil || int64(len(unbound)) != got.UnboundKeyCount {
		t.Fatalf("overview differs from migration list: %+v, %v", unbound, err)
	}
	empty, err := repo.Summary(ctx, to.Add(time.Hour), to.Add(2*time.Hour))
	if err != nil || empty.UnboundKeyCount != 5 || empty.RequestCount != 0 || empty.UnattributedRequestCount != 0 || empty.UnattributedRequestRatio != 0 || empty.RecordedUnattributedCosts == nil || len(empty.RecordedUnattributedCosts) != 0 {
		t.Fatalf("empty window = %+v, %v", empty, err)
	}
	all, err := repo.Summary(ctx, time.Time{}, time.Time{})
	if err != nil || all.From != nil || all.To != nil || all.RequestCount != 6 || all.UnattributedRequestCount != 5 {
		t.Fatalf("unbounded history changed = %+v, %v", all, err)
	}
	injected, err := store.NewAttributionQueryRepo(db, "attribution-a' OR 1=1 --").Summary(ctx, from, to)
	if err != nil || injected.UnboundKeyCount != 0 || injected.RequestCount != 0 || len(injected.RecordedUnattributedCosts) != 0 {
		t.Fatalf("tenant input escaped scope: %+v, %v", injected, err)
	}
	if _, err := store.NewAttributionQueryRepo(db, "").Summary(ctx, from, to); err == nil {
		t.Fatal("empty tenant must not create a global query")
	}
}

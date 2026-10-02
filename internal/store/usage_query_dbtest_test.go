//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

func TestUsageQuery_AttributionAndCurrencies(t *testing.T) {
	ctx := context.Background()
	sink, db := freshUsageRepo(t)
	appA, appB := int64(901), int64(902)
	records := []billing.UsageRecord{
		{Tenant: "acme", ApplicationID: &appA, Environment: "prod", Currency: "USD", Provider: "openai", Model: "chat", Cost: 100, PromptTokens: 10, SessionID: "shared"},
		{Tenant: "acme", ApplicationID: &appA, Environment: "prod", Currency: "CNY", Provider: "openai", Model: "chat", Cost: 200, PromptTokens: 20, SessionID: "shared"},
		{Tenant: "acme", ApplicationID: &appA, Environment: "dev", Currency: "USD", Provider: "openai", Model: "chat", Cost: 300},
		{Tenant: "acme", ApplicationID: &appB, Environment: "prod", Currency: "USD", Provider: "openai", Model: "chat", Cost: 400},
		{Tenant: "acme", Provider: "openai", Model: "chat", Cost: 50, SessionID: "shared"},
		{Tenant: "other", ApplicationID: &appA, Environment: "prod", Currency: "USD", Provider: "openai", Model: "chat", Cost: 9999, SessionID: "shared"},
		{Tenant: "other", Provider: "openai", Model: "chat", Cost: 8888, SessionID: "shared"},
	}
	if err := sink.RecordBatch(ctx, records); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	if err := db.Exec(`UPDATE usage_records SET created_at = ?`, at).Error; err != nil {
		t.Fatal(err)
	}
	query := store.NewUsageQueryRepo(db, "acme")
	for _, tc := range []struct {
		name   string
		filter store.UsageFilter
		want   int
	}{
		{"all", store.UsageFilter{}, 5},
		{"application", store.UsageFilter{ApplicationID: &appA}, 3},
		{"environment", store.UsageFilter{Environment: "prod"}, 3},
		{"intersection", store.UsageFilter{ApplicationID: &appA, Environment: "prod"}, 2},
		{"unattributed", store.UsageFilter{Unattributed: true}, 1},
		{"unattributed-environment", store.UsageFilter{Unattributed: true, Environment: "prod"}, 0},
		{"parameterized", store.UsageFilter{Environment: "prod' OR 1=1 --"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := query.List(ctx, tc.filter, "", 100)
			if err != nil || len(rows) != tc.want {
				t.Fatalf("List = %+v, %v; want %d", rows, err, tc.want)
			}
			for _, row := range rows {
				if row.Tenant != "acme" {
					t.Fatalf("cross-tenant row: %+v", row)
				}
				if row.Cost == 100 && (row.ApplicationID == nil || *row.ApplicationID != appA || row.Environment != "prod" || row.Currency != "USD") {
					t.Fatalf("lost snapshot: %+v", row)
				}
				if row.Cost == 50 && (row.ApplicationID != nil || row.Environment != "" || row.Currency != "") {
					t.Fatalf("historical dimensions invented: %+v", row)
				}
			}
		})
	}
	filter := store.UsageFilter{ApplicationID: &appA, Environment: "prod", Provider: "openai", Model: "chat", From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	for _, dimension := range []string{"application_id", "environment"} {
		rows, err := query.Summary(ctx, filter.From, filter.To, dimension, filter)
		if err != nil || len(rows) != 2 {
			t.Fatalf("Summary(%s) = %+v, %v", dimension, rows, err)
		}
		wantKey := "901"
		if dimension == "environment" {
			wantKey = "prod"
		}
		for _, row := range rows {
			wantCost := map[string]int64{"USD": 100, "CNY": 200}[row.Currency]
			if row.GroupKey != wantKey || row.Cost != wantCost || row.RequestCount != 1 {
				t.Fatalf("mixed currencies or lost filter: %+v", row)
			}
		}
	}
	legacy, err := query.Summary(ctx, time.Time{}, time.Time{}, "application_id", store.UsageFilter{Unattributed: true})
	if err != nil || len(legacy) != 1 || legacy[0].GroupKey != "unattributed" || legacy[0].Currency != "" || legacy[0].Cost != 50 {
		t.Fatalf("legacy summary = %+v, %v", legacy, err)
	}
	buckets, err := query.Timeseries(ctx, filter, "day")
	if err != nil || len(buckets) != 2 {
		t.Fatalf("Timeseries = %+v, %v", buckets, err)
	}
	for _, bucket := range buckets {
		if bucket.Cost != map[string]int64{"USD": 100, "CNY": 200}[bucket.Currency] || bucket.RequestCount != 1 {
			t.Fatalf("mixed time-series currencies: %+v", bucket)
		}
	}
	summary, err := query.SummaryBySession(ctx, "shared")
	if err != nil || summary.Cost != nil || summary.RequestCount != 3 || summary.PromptTokens != 30 || len(summary.CostsByCurrency) != 3 {
		t.Fatalf("mixed session summary = %+v, %v", summary, err)
	}
	costs, err := query.CostBySessions(ctx, []string{"shared", "missing"})
	if err != nil || len(costs["shared"]) != 3 || len(costs["missing"]) != 0 {
		t.Fatalf("session costs = %+v, %v", costs, err)
	}
	for _, amount := range costs["shared"] {
		if amount.Cost != map[string]int64{"": 50, "USD": 100, "CNY": 200}[amount.Currency] {
			t.Fatalf("mixed currencies or tenant leak: %+v", amount)
		}
	}
}

// seedUsage inserts usage rows with explicit created_at so keyset/time-range
// behavior is deterministic.
func seedUsageAt(t *testing.T, db *store.DB, tenant string, cost int64, at time.Time) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO usage_records
		   (tenant, group_name, api_key_id, provider, provider_endpoint, model,
		    prompt_tokens, completion_tokens, cost,
		    request_id, session_id, trace_id,
		    cached_prompt_tokens, cache_discount_micros, created_at)
		 VALUES (?, '', 'k', 'openai', '', 'gpt-4o', 10, 20, ?, '', '', '', 0, 0, ?)`,
		tenant, cost, at,
	).Error; err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

// A tenant-scoped query returns only that tenant's rows; the global (empty
// tenant) query sees everything. Structural isolation for reads.
func TestUsageQuery_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	_, db := freshUsageRepo(t)
	now := time.Now().UTC()
	seedUsageAt(t, db, "acme", 100, now)
	seedUsageAt(t, db, "acme", 200, now.Add(-time.Minute))
	seedUsageAt(t, db, "other", 999, now)

	// Tenant-scoped: only acme.
	scoped := store.NewUsageQueryRepo(db, "acme")
	rows, _, err := scoped.List(ctx, store.UsageFilter{}, "", 100)
	if err != nil {
		t.Fatalf("List scoped: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("scoped rows = %d, want 2 (acme only)", len(rows))
	}
	for _, r := range rows {
		if r.Tenant != "acme" {
			t.Errorf("scoped query leaked tenant %q", r.Tenant)
		}
	}

	// Global: all three.
	global := store.NewUsageQueryRepo(db, "")
	all, _, err := global.List(ctx, store.UsageFilter{}, "", 100)
	if err != nil {
		t.Fatalf("List global: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("global rows = %d, want 3", len(all))
	}
}

// Keyset pagination walks the full set in (created_at, id) DESC order with no
// gaps or dupes, and the final page returns an empty next_cursor.
func TestUsageQuery_KeysetPagination(t *testing.T) {
	ctx := context.Background()
	_, db := freshUsageRepo(t)
	base := time.Now().UTC()
	const total = 5
	for i := 0; i < total; i++ {
		seedUsageAt(t, db, "acme", int64(i+1), base.Add(-time.Duration(i)*time.Second))
	}

	repo := store.NewUsageQueryRepo(db, "acme")
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		rows, next, err := repo.List(ctx, store.UsageFilter{}, cursor, 2)
		if err != nil {
			t.Fatalf("List page %d: %v", pages, err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Errorf("row id %d returned twice across pages", r.ID)
			}
			seen[r.ID] = true
		}
		pages++
		if next == "" {
			break
		}
		cursor = next
		if pages > total+2 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != total {
		t.Errorf("saw %d distinct rows across pages, want %d", len(seen), total)
	}
}

// A time range bounds results: [from, to). Rows outside are excluded.
func TestUsageQuery_TimeRange(t *testing.T) {
	ctx := context.Background()
	_, db := freshUsageRepo(t)
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedUsageAt(t, db, "acme", 1, t0.Add(-time.Hour))   // before window
	seedUsageAt(t, db, "acme", 2, t0.Add(time.Hour))    // inside
	seedUsageAt(t, db, "acme", 3, t0.Add(48*time.Hour)) // after window (to is exclusive)

	repo := store.NewUsageQueryRepo(db, "acme")
	from := t0
	to := t0.Add(24 * time.Hour)
	rows, _, err := repo.List(ctx, store.UsageFilter{From: from, To: to}, "", 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].Cost != 2 {
		t.Errorf("time-ranged rows = %+v, want exactly the one inside [from,to)", rows)
	}
}

// Summary groups by a dimension and sums cost/tokens/count.
func TestUsageQuery_SummaryGroupBy(t *testing.T) {
	ctx := context.Background()
	repo, db := freshUsageRepo(t)
	now := time.Now().UTC()
	// Two providers via direct records (RecordBatch uses created_at=now()).
	_ = repo.RecordBatch(ctx, []billing.UsageRecord{
		{Tenant: "acme", Provider: "openai", Model: "m", PromptTokens: 10, CompletionTokens: 5, Cost: 100},
		{Tenant: "acme", Provider: "openai", Model: "m", PromptTokens: 20, CompletionTokens: 5, Cost: 200},
		{Tenant: "acme", Provider: "claude", Model: "m", PromptTokens: 1, CompletionTokens: 1, Cost: 50},
	})
	_ = now

	q := store.NewUsageQueryRepo(db, "acme")
	rows, err := q.Summary(ctx, time.Time{}, time.Time{}, "provider")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	byKey := map[string]store.UsageSummaryRow{}
	for _, r := range rows {
		byKey[r.GroupKey] = r
	}
	if got := byKey["openai"]; got.Cost != 300 || got.RequestCount != 2 || got.PromptTokens != 30 {
		t.Errorf("openai aggregate = %+v, want cost 300 count 2 prompt 30", got)
	}
	if got := byKey["claude"]; got.Cost != 50 || got.RequestCount != 1 {
		t.Errorf("claude aggregate = %+v, want cost 50 count 1", got)
	}
}

// Timeseries buckets usage by date_trunc precision. Rows in the same bucket
// are aggregated; buckets are returned in ASC order. Respects tenant scope.
func TestUsageQuery_Timeseries(t *testing.T) {
	ctx := context.Background()
	_, db := freshUsageRepo(t)

	// Seed rows across two days for tenant acme, plus one row for "other".
	day1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 6, 2, 14, 0, 0, 0, time.UTC)
	seedUsageAt(t, db, "acme", 100, day1)
	seedUsageAt(t, db, "acme", 200, day1.Add(2*time.Hour)) // same day as day1
	seedUsageAt(t, db, "acme", 300, day2)                  // next day
	seedUsageAt(t, db, "other", 999, day1)                 // different tenant

	repo := store.NewUsageQueryRepo(db, "acme")
	rows, err := repo.Timeseries(ctx, store.UsageFilter{}, "day")
	if err != nil {
		t.Fatalf("Timeseries: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("buckets = %d, want 2 (day1 + day2)", len(rows))
	}
	// ASC order: day1 before day2.
	if !rows[0].BucketStart.Before(rows[1].BucketStart) {
		t.Errorf("buckets not in ASC order: %v then %v", rows[0].BucketStart, rows[1].BucketStart)
	}
	// Day1 aggregates the two acme rows: cost 100+200=300, count 2.
	if rows[0].Cost != 300 || rows[0].RequestCount != 2 {
		t.Errorf("day1 bucket = %+v, want cost 300 count 2", rows[0])
	}
	if rows[0].PromptTokens != 20 { // 10 + 10
		t.Errorf("day1 prompt_tokens = %d, want 20", rows[0].PromptTokens)
	}
	// Day2: single row, cost 300.
	if rows[1].Cost != 300 || rows[1].RequestCount != 1 {
		t.Errorf("day2 bucket = %+v, want cost 300 count 1", rows[1])
	}
}

// Timeseries with an unknown bucket returns an error (400 from the handler).
func TestUsageQuery_Timeseries_InvalidBucket(t *testing.T) {
	ctx := context.Background()
	_, db := freshUsageRepo(t)
	repo := store.NewUsageQueryRepo(db, "acme")
	if _, err := repo.Timeseries(ctx, store.UsageFilter{}, "fortnight"); err == nil {
		t.Error("expected error for invalid bucket, got nil")
	}
}

// Timeseries hour bucket groups within the same day by hour.
func TestUsageQuery_Timeseries_HourBucket(t *testing.T) {
	ctx := context.Background()
	_, db := freshUsageRepo(t)
	base := time.Date(2026, 6, 1, 10, 30, 0, 0, time.UTC)
	// Two rows in the 10:00 hour, one in the 11:00 hour.
	seedUsageAt(t, db, "acme", 100, base)
	seedUsageAt(t, db, "acme", 200, base.Add(15*time.Minute)) // still 10:xx
	seedUsageAt(t, db, "acme", 300, base.Add(time.Hour))      // 11:xx

	repo := store.NewUsageQueryRepo(db, "acme")
	rows, err := repo.Timeseries(ctx, store.UsageFilter{}, "hour")
	if err != nil {
		t.Fatalf("Timeseries hour: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("hour buckets = %d, want 2 (10:00 + 11:00)", len(rows))
	}
	// First bucket = 10:00 hour: cost 100+200=300.
	if rows[0].Cost != 300 || rows[0].RequestCount != 2 {
		t.Errorf("10:00 bucket = %+v, want cost 300 count 2", rows[0])
	}
}

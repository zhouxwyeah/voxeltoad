//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

// seedUsage inserts usage rows with explicit created_at so keyset/time-range
// behavior is deterministic.
func seedUsageAt(t *testing.T, db *store.DB, tenant string, cost int64, at time.Time) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO usage_records
		   (tenant, group_name, api_key_id, provider, model,
		    prompt_tokens, completion_tokens, cost, created_at)
		 VALUES (?, '', 'k', 'openai', 'gpt-4o', 10, 20, ?, ?)`,
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

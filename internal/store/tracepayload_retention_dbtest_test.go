//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

// TestTracePayloadRetention_DropsExpiredPartitions verifies the partition-DROP
// TTL (ADR-0039 §4) drops monthly partitions whose ENTIRE range is before the
// cutoff (strict <), leaving the current month intact even when cutoff falls
// inside it. It hand-creates partitions so the test is independent of the
// current calendar month.
func TestTracePayloadRetention_DropsExpiredPartitions(t *testing.T) {
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE trace_payloads`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Create three extra named partitions:
	//   - 2020_01: fully expired (before cutoff).
	//   - 2026_01: same month as cutoff — must be PRESERVED (rows in it may still
	//     be within the retention window; e.g. a 7-day retention cutoff landing
	//     mid-month must not drop the whole month).
	//   - 2099_01: future — must be preserved.
	// The 00018 migration already created the current 12-month window.
	mustExec(t, db, `CREATE TABLE IF NOT EXISTS trace_payloads_2020_01
		PARTITION OF trace_payloads FOR VALUES FROM ('2020-01-01') TO ('2020-02-01')`)
	mustExec(t, db, `CREATE TABLE IF NOT EXISTS trace_payloads_2026_01
		PARTITION OF trace_payloads FOR VALUES FROM ('2026-01-01') TO ('2026-02-01')`)
	mustExec(t, db, `CREATE TABLE IF NOT EXISTS trace_payloads_2099_01
		PARTITION OF trace_payloads FOR VALUES FROM ('2099-01-01') TO ('2099-02-01')`)

	// Cutoff at the start of 2026-01: 2020_01 is fully expired; 2026_01 and
	// 2099_01 are not (their range is not entirely before cutoff).
	repo := store.NewTracePayloadRepo(db)
	n, err := repo.DropTracePayloadPartitionsBefore(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 partition dropped (2020_01), got %d", n)
	}

	// The expired partition must be gone; the current-month and future ones remain.
	if exists(t, db, "trace_payloads_2020_01") {
		t.Error("trace_payloads_2020_01 should have been dropped (fully expired)")
	}
	if !exists(t, db, "trace_payloads_2026_01") {
		t.Error("trace_payloads_2026_01 should NOT be dropped (same month as cutoff)")
	}
	if !exists(t, db, "trace_payloads_2099_01") {
		t.Error("trace_payloads_2099_01 should NOT have been dropped (future)")
	}
}

func mustExec(t *testing.T, db *store.DB, q string) {
	t.Helper()
	if err := db.Exec(q).Error; err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func exists(t *testing.T, db *store.DB, table string) bool {
	t.Helper()
	var ok bool
	if err := db.Raw(`SELECT to_regclass(?) IS NOT NULL`, table).Scan(&ok).Error; err != nil {
		t.Fatalf("check %s: %v", table, err)
	}
	return ok
}

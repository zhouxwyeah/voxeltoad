package desktopstore

import (
	"context"
	"testing"
	"time"
)

// seedRetentionData inserts one old (40d) and one recent (1d) row into each
// ledger table, so a 30d cutoff must remove exactly the old ones.
func seedRetentionData(t *testing.T, db *DB) {
	t.Helper()
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)

	logs := []RequestLogRow{
		{Tenant: "default", Provider: "p", ModelRequested: "m", RequestID: "old-log", CreatedAt: old},
		{Tenant: "default", Provider: "p", ModelRequested: "m", RequestID: "new-log", CreatedAt: recent},
	}
	for _, l := range logs {
		if err := db.Create(&l).Error; err != nil {
			t.Fatalf("seed request_logs: %v", err)
		}
	}
	traces := []TracePayloadRow{
		{RequestID: "old-trace", SessionID: "s", Provider: "p", ModelRequested: "m", CreatedAt: old},
		{RequestID: "new-trace", SessionID: "s", Provider: "p", ModelRequested: "m", CreatedAt: recent},
	}
	for _, tr := range traces {
		if err := db.Create(&tr).Error; err != nil {
			t.Fatalf("seed trace_payloads: %v", err)
		}
	}
}

func countRows(t *testing.T, db *DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestRetention_DeletesOnlyExpiredRows(t *testing.T) {
	db := openTestDB(t)
	seedRetentionData(t, db)

	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	nLogs, err := db.DeleteRequestLogsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	if nLogs != 1 {
		t.Errorf("deleted request_logs = %d, want 1", nLogs)
	}
	nTraces, err := db.DeleteTracePayloadsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteTracePayloadsBefore: %v", err)
	}
	if nTraces != 1 {
		t.Errorf("deleted trace_payloads = %d, want 1", nTraces)
	}

	if n := countRows(t, db, "request_logs"); n != 1 {
		t.Errorf("remaining request_logs = %d, want 1", n)
	}
	if n := countRows(t, db, "trace_payloads"); n != 1 {
		t.Errorf("remaining trace_payloads = %d, want 1", n)
	}

	// The survivors are the recent rows.
	var reqID string
	if err := db.Raw("SELECT request_id FROM request_logs").Scan(&reqID).Error; err != nil {
		t.Fatal(err)
	}
	if reqID != "new-log" {
		t.Errorf("surviving request log = %q, want new-log", reqID)
	}
	if err := db.Raw("SELECT request_id FROM trace_payloads").Scan(&reqID).Error; err != nil {
		t.Fatal(err)
	}
	if reqID != "new-trace" {
		t.Errorf("surviving trace = %q, want new-trace", reqID)
	}

	if err := db.Checkpoint(); err != nil {
		t.Errorf("Checkpoint: %v", err)
	}
}

// A second sweep with nothing to delete is a clean no-op.
func TestRetention_NothingToDelete(t *testing.T) {
	db := openTestDB(t)
	seedRetentionData(t, db)

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := db.DeleteRequestLogsBefore(context.Background(), cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteTracePayloadsBefore(context.Background(), cutoff); err != nil {
		t.Fatal(err)
	}

	n, err := db.DeleteRequestLogsBefore(context.Background(), cutoff)
	if err != nil || n != 0 {
		t.Errorf("second sweep = (%d, %v), want (0, nil)", n, err)
	}
	if n := countRows(t, db, "request_logs"); n != 1 {
		t.Errorf("remaining request_logs = %d, want 1", n)
	}
}

// TestRetention_DispatchStepsSweeperOrder verifies that dispatch_steps are
// cleaned up even when DeleteRequestLogsBefore runs first (the sweeper order).
// Regression for the orphan bug: DeleteDispatchStepsBefore previously used a
// request_id IN (SELECT ... FROM request_logs WHERE created_at < ?) subquery,
// which returned empty after request_logs were already deleted.
func TestRetention_DispatchStepsSweeperOrder(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)

	// Seed request_logs + matching dispatch_steps for old and recent requests.
	for _, ts := range []time.Time{old, recent} {
		rid := "req-" + ts.Format("20060102")
		if err := db.Create(&RequestLogRow{
			Tenant: "default", Provider: "p", ModelRequested: "m",
			RequestID: rid, SessionID: "sess-" + rid, CreatedAt: ts,
		}).Error; err != nil {
			t.Fatalf("seed request_logs: %v", err)
		}
		if err := db.Create(&DispatchStepRow{
			RequestID: rid, Ordinal: 0, Provider: "p", Endpoint: "default",
			Action: "attempted", SelectionOutcome: "selected", CreatedAt: ts,
		}).Error; err != nil {
			t.Fatalf("seed dispatch_steps: %v", err)
		}
	}

	cutoff := now.Add(-30 * 24 * time.Hour)

	// Sweeper order: request_logs first, then dispatch_steps.
	if _, err := db.DeleteRequestLogsBefore(context.Background(), cutoff); err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	nSteps, err := db.DeleteDispatchStepsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteDispatchStepsBefore: %v", err)
	}
	if nSteps != 1 {
		t.Errorf("deleted dispatch_steps = %d, want 1 (old step must be cleaned even after request_logs deleted)", nSteps)
	}
	if n := countRows(t, db, "dispatch_steps"); n != 1 {
		t.Errorf("remaining dispatch_steps = %d, want 1 (recent)", n)
	}
}

// TestRetention_DispatchStepsFavoritesExempt verifies that dispatch_steps
// belonging to a favorited session are NOT deleted by the time-based sweep,
// even when the step's created_at is older than the cutoff.
func TestRetention_DispatchStepsFavoritesExempt(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)

	// Seed a favorited session with an old request_log + dispatch_step.
	favSession := "fav-session"
	if err := db.Create(&RequestLogRow{
		Tenant: "default", Provider: "p", ModelRequested: "m",
		RequestID: "fav-req", SessionID: favSession, CreatedAt: old,
	}).Error; err != nil {
		t.Fatalf("seed request_logs: %v", err)
	}
	if err := db.Create(&SessionFavoriteRow{SessionID: favSession, CreatedAt: now}).Error; err != nil {
		t.Fatalf("seed session_favorites: %v", err)
	}
	if err := db.Create(&DispatchStepRow{
		RequestID: "fav-req", Ordinal: 0, Provider: "p", Endpoint: "default",
		Action: "attempted", SelectionOutcome: "selected", CreatedAt: old,
	}).Error; err != nil {
		t.Fatalf("seed dispatch_steps: %v", err)
	}

	cutoff := now.Add(-30 * 24 * time.Hour)

	// Sweeper order: request_logs first (exempts favorite), then dispatch_steps.
	nLogs, err := db.DeleteRequestLogsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	if nLogs != 0 {
		t.Errorf("deleted request_logs = %d, want 0 (favorited session exempt)", nLogs)
	}

	nSteps, err := db.DeleteDispatchStepsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteDispatchStepsBefore: %v", err)
	}
	if nSteps != 0 {
		t.Errorf("deleted dispatch_steps = %d, want 0 (favorited session exempt)", nSteps)
	}

	// Both rows must survive.
	if n := countRows(t, db, "dispatch_steps"); n != 1 {
		t.Errorf("remaining dispatch_steps = %d, want 1 (favorited)", n)
	}
	if n := countRows(t, db, "request_logs"); n != 1 {
		t.Errorf("remaining request_logs = %d, want 1 (favorited)", n)
	}
}

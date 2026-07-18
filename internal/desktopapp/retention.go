package desktopapp

import (
	"context"
	"log"
	"time"

	cfg "voxeltoad/internal/config"
	"voxeltoad/internal/desktopstore"
)

// defaultRetentionDays is the fallback window when settings.trace.retention_days
// is unset/<=0. It matches the seeded desktop template (cmd/desktop/seed.go);
// the enterprise fallback (7d) is intentionally shorter because PG partition
// storage is more expensive than a local SQLite file.
const defaultRetentionDays = 30

// retentionInterval is how often the sweeper re-runs after the startup pass.
const retentionInterval = 24 * time.Hour

// startRetentionSweeper deletes request_logs/trace_payloads older than the
// hot-reloadable retention window (settings.trace.retention_days) once at
// startup and then daily (design/desktop.md §6.4). Mirrors the enterprise
// admin TTL job (cmd/admin/main.go) with plain DELETEs instead of partition
// drops — personal-scale SQLite makes that cheap, and both ledgers share the
// window so the session browser never links into deleted trace rows.
//
// Failures are logged and skipped: a failed sweep must never take down the
// gateway. The goroutine runs until process exit; a late sweep racing the
// shutdown db.Close surfaces as a benign logged error.
func startRetentionSweeper(db *desktopstore.DB, settingsFn func() *cfg.GatewaySettings) {
	sweep := func() {
		days := defaultRetentionDays
		if s := settingsFn(); s != nil && s.Trace.RetentionDays > 0 {
			days = s.Trace.RetentionDays
		}
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		nLogs, err := db.DeleteRequestLogsBefore(ctx, cutoff)
		if err != nil {
			log.Printf("retention: delete request_logs failed: %v", err)
		}
		nTraces, err := db.DeleteTracePayloadsBefore(ctx, cutoff)
		if err != nil {
			log.Printf("retention: delete trace_payloads failed: %v", err)
		}
		if err := db.Checkpoint(); err != nil {
			log.Printf("retention: WAL checkpoint failed: %v", err)
		}
		if nLogs+nTraces > 0 {
			log.Printf("retention: removed %d request logs + %d trace payloads older than %dd", nLogs, nTraces, days)
		}
	}

	go func() {
		sweep()
		tick := time.NewTicker(retentionInterval)
		defer tick.Stop()
		for range tick.C {
			sweep()
		}
	}()
}

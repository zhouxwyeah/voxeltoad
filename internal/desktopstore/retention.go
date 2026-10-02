package desktopstore

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Retention deletes for the desktop ledgers (design/desktop.md §6.4). The
// enterprise side drops monthly trace_payloads partitions (O(1) DDL); SQLite
// has no partitions, but at personal scale a plain DELETE over an indexed
// created_at is cheap. Both tables share the same cutoff so the session
// browser never links into trace rows that no longer exist.
//
// Favorited sessions (session_favorites) are exempt from time-based retention
// (ADR-0057): their request_logs, trace_payloads, and dispatch_steps survive
// until the favorite is removed. The exemption is enforced via a NOT IN
// subquery in each Delete*Before method.
//
// There is no soft-delete on these tables (no DeletedAt column), so GORM
// Delete issues a hard DELETE.

// favoritedSessionExempt is the WHERE clause fragment that excludes favorited
// sessions from time-based retention deletes.
const favoritedSessionExempt = "session_id NOT IN (SELECT session_id FROM session_favorites)"

// DeleteRequestLogsBefore hard-deletes request_logs rows older than cutoff,
// except those belonging to favorited sessions. Returns rows removed.
func (db *DB) DeleteRequestLogsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := db.WithContext(ctx).
		Where("created_at < ? AND "+favoritedSessionExempt, cutoff).
		Delete(&RequestLogRow{})
	return res.RowsAffected, res.Error
}

// DeleteTracePayloadsBefore hard-deletes trace_payloads rows older than cutoff,
// except those belonging to favorited sessions. Returns rows removed.
func (db *DB) DeleteTracePayloadsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := db.WithContext(ctx).
		Where("created_at < ? AND "+favoritedSessionExempt, cutoff).
		Delete(&TracePayloadRow{})
	return res.RowsAffected, res.Error
}

// DeleteDispatchStepsBefore hard-deletes dispatch_steps older than cutoff,
// except those belonging to favorited sessions. dispatch_steps has its own
// created_at column, so we filter directly on it (not through a request_logs
// subquery) — this avoids orphaning steps when the sweeper deletes
// request_logs first. The favorites exemption uses a NOT IN subquery against
// the surviving request_logs rows (favorited sessions' request_logs are
// never deleted by DeleteRequestLogsBefore, so they're always present).
// ADR-0057: dispatch_steps share the parent request's lifecycle.
func (db *DB) DeleteDispatchStepsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	exempt := "request_id NOT IN (SELECT request_id FROM request_logs WHERE session_id IN (SELECT session_id FROM session_favorites))"
	res := db.WithContext(ctx).
		Where("created_at < ? AND "+exempt, cutoff).
		Delete(&DispatchStepRow{})
	return res.RowsAffected, res.Error
}

// DeleteSession hard-deletes all observation data for one session: its
// request_logs, trace_payloads, and dispatch_steps. Used by the "delete
// session" API (design/desktop.md §6.4). Favorites are NOT exempt here —
// this is an explicit user action. Returns total rows removed.
// Wrapped in a transaction so partial failure doesn't leave orphans.
func (db *DB) DeleteSession(ctx context.Context, sessionID string) (int64, error) {
	var total int64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// dispatch_steps: join through request_logs to find this session's steps.
		res := tx.Where("request_id IN (SELECT request_id FROM request_logs WHERE session_id = ?)", sessionID).
			Delete(&DispatchStepRow{})
		if res.Error != nil {
			return res.Error
		}
		total += res.RowsAffected

		// trace_payloads
		res = tx.Where("session_id = ?", sessionID).Delete(&TracePayloadRow{})
		if res.Error != nil {
			return res.Error
		}
		total += res.RowsAffected

		// request_logs
		res = tx.Where("session_id = ?", sessionID).Delete(&RequestLogRow{})
		if res.Error != nil {
			return res.Error
		}
		total += res.RowsAffected
		return nil
	})
	return total, err
}

// ClearAllObservation hard-deletes all request_logs, trace_payloads, and
// dispatch_steps. Preserves api_keys, prompt_templates, and session_favorites.
// Used by the "clear all" API (design/desktop.md §6.4). Returns total rows
// removed.
func (db *DB) ClearAllObservation(ctx context.Context) (int64, error) {
	res := db.WithContext(ctx).Where("1=1").Delete(&DispatchStepRow{})
	if res.Error != nil {
		return 0, res.Error
	}
	n := res.RowsAffected
	res = db.WithContext(ctx).Where("1=1").Delete(&TracePayloadRow{})
	if res.Error != nil {
		return n, res.Error
	}
	n += res.RowsAffected
	res = db.WithContext(ctx).Where("1=1").Delete(&RequestLogRow{})
	if res.Error != nil {
		return n, res.Error
	}
	n += res.RowsAffected
	return n, nil
}

// Checkpoint runs PRAGMA wal_checkpoint(TRUNCATE): folds the WAL back into
// the main database file and truncates the WAL, so retention deletes actually
// shrink on-disk usage instead of just growing the WAL.
func (db *DB) Checkpoint() error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return err
	}
	_, err = sqlDB.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	return err
}

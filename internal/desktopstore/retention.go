package desktopstore

import (
	"context"
	"time"
)

// Retention deletes for the desktop ledgers (design/desktop.md §6.4). The
// enterprise side drops monthly trace_payloads partitions (O(1) DDL); SQLite
// has no partitions, but at personal scale a plain DELETE over an indexed
// created_at is cheap. Both tables share the same cutoff so the session
// browser never links into trace rows that no longer exist.
//
// There is no soft-delete on these tables (no DeletedAt column), so GORM
// Delete issues a hard DELETE.

// DeleteRequestLogsBefore hard-deletes request_logs rows older than cutoff.
// Returns the number of rows removed.
func (db *DB) DeleteRequestLogsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&RequestLogRow{})
	return res.RowsAffected, res.Error
}

// DeleteTracePayloadsBefore hard-deletes trace_payloads rows older than
// cutoff. Returns the number of rows removed.
func (db *DB) DeleteTracePayloadsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&TracePayloadRow{})
	return res.RowsAffected, res.Error
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

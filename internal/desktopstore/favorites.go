package desktopstore

import (
	"context"
	"time"
)

// SessionFavoriteRepo manages session_favorites: local metadata that pins a
// session's observation records (request_logs, trace_payloads, dispatch_steps)
// exempt from ordinary retention. ADR-0057.
type SessionFavoriteRepo struct {
	db *DB
}

func NewSessionFavoriteRepo(db *DB) *SessionFavoriteRepo { return &SessionFavoriteRepo{db: db} }

// Put adds a favorite (idempotent — upsert on primary key).
func (r *SessionFavoriteRepo) Put(ctx context.Context, sessionID string) error {
	row := SessionFavoriteRow{SessionID: sessionID, CreatedAt: time.Now()}
	return r.db.WithContext(ctx).Save(&row).Error
}

// Delete removes a favorite. Returns false when no row matched.
func (r *SessionFavoriteRepo) Delete(ctx context.Context, sessionID string) (bool, error) {
	res := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&SessionFavoriteRow{})
	return res.RowsAffected > 0, res.Error
}

// List returns all favorited session IDs as a set for fast lookup.
func (r *SessionFavoriteRepo) List(ctx context.Context) (map[string]bool, error) {
	var rows []SessionFavoriteRow
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.SessionID] = true
	}
	return out, nil
}

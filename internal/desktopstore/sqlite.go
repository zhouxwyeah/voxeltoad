// Package store implements the desktop gateway's SQLite-backed persistence.
//
// It is intentionally isolated from internal/store (which is PostgreSQL-only):
// the desktop gateway cannot depend on a running PostgreSQL. The three surfaces
// the data plane needs — API-key lookup (auth.KeyStore), request-audit capture
// (observability.RequestLogSink) and trace-payload capture
// (observability.TracePayloadSink) — are provided here as SQLite implementations
// behind the same interfaces the enterprise data plane uses, so proxy/adapter/
// plugin/observability are reused verbatim (zero core change). See
// design/desktop.md.
package desktopstore

import (
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB wraps the gorm connection to SQLite.
type DB struct {
	*gorm.DB
}

// Open opens (or creates) the SQLite database at the given file path and runs
// schema migration for the desktop tables (api_keys, request_logs,
// trace_payloads, prompt_templates). WAL is enabled for reader/writer
// concurrency on a personal-scale workload.
func Open(path string) (*DB, error) {
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Silent),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	// WAL: allows the read API (UI) to run concurrently with the async
	// recorders flushing captures. Busy timeout avoids "database is locked" on
	// the single-writer SQLite model.
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			return nil, err
		}
	}
	if err := gdb.AutoMigrate(&APIKeyRow{}, &RequestLogRow{}, &TracePayloadRow{}, &PromptTemplateRow{}); err != nil {
		return nil, err
	}
	return &DB{gdb}, nil
}

// Close closes the underlying connection pool.
func (db *DB) Close() error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// --- gorm models (SQLite column types) ---

// APIKeyRow is the single seeded default key for the personal desktop gateway.
type APIKeyRow struct {
	ID            uint       `gorm:"primaryKey;autoIncrement"`
	KeyID         string     `gorm:"column:key_id;uniqueIndex"`
	Hash          string     `gorm:"column:hash;uniqueIndex"`
	Tenant        string     `gorm:"column:tenant_id"`
	Group         string     `gorm:"column:group_id"`
	ExpiresAt     *time.Time `gorm:"column:expires_at"`
	AllowedModels string     `gorm:"column:allowed_models;type:text"` // JSON array of model aliases; "[]" = all
	RevokedAt     *time.Time `gorm:"column:revoked_at"`
}

func (*APIKeyRow) TableName() string { return "api_keys" }

// RequestLogRow mirrors observability.RequestLog (metadata only, no bodies).
type RequestLogRow struct {
	ID                 uint      `gorm:"primaryKey;autoIncrement"`
	Tenant             string    `gorm:"column:tenant;index"`
	Group              string    `gorm:"column:group_name"`
	APIKeyID           string    `gorm:"column:api_key_id"`
	Provider           string    `gorm:"column:provider"`
	ModelRequested     string    `gorm:"column:model_requested"`
	ModelResolved      string    `gorm:"column:model_resolved"`
	Stream             bool      `gorm:"column:stream"`
	PromptTokens       int       `gorm:"column:prompt_tokens"`
	CompletionTokens   int       `gorm:"column:completion_tokens"`
	TotalTokens        int       `gorm:"column:total_tokens"`
	TTFTms             int       `gorm:"column:ttft_ms"`
	Durationms         int       `gorm:"column:duration_ms"`
	ErrorType          string    `gorm:"column:error_type"`
	BlockedBy          string    `gorm:"column:blocked_by"`
	Fallback           bool      `gorm:"column:fallback"`
	CacheHit           bool      `gorm:"column:cache_hit"`
	CachedPromptTokens int       `gorm:"column:cached_prompt_tokens"`
	CacheTier          string    `gorm:"column:cache_tier"`
	CacheSource        string    `gorm:"column:cache_source"`
	RequestID          string    `gorm:"column:request_id;index"`
	ClientRequestID    string    `gorm:"column:client_request_id;index"`
	SessionID          string    `gorm:"column:session_id;index"`
	TraceID            string    `gorm:"column:trace_id"`
	UpstreamRequestID  string    `gorm:"column:upstream_request_id"`
	SessionSource      string    `gorm:"column:session_source"`
	AgentType          string    `gorm:"column:agent_type;index"`
	CreatedAt          time.Time `gorm:"column:created_at;index"`
}

func (*RequestLogRow) TableName() string { return "request_logs" }

// TracePayloadRow mirrors observability.TracePayload: the message + raw layers.
// messages / request_raw are JSON text; response_raw / error_raw are verbatim
// text (SSE transcripts are not JSON).
type TracePayloadRow struct {
	ID              uint      `gorm:"primaryKey;autoIncrement"`
	RequestID       string    `gorm:"column:request_id;index"`
	ClientRequestID string    `gorm:"column:client_request_id;index"`
	SessionID       string    `gorm:"column:session_id;index"`
	TraceID         string    `gorm:"column:trace_id"`
	Tenant          string    `gorm:"column:tenant"`
	Group           string    `gorm:"column:group_name"`
	APIKeyID        string    `gorm:"column:api_key_id"`
	Provider        string    `gorm:"column:provider"`
	ModelRequested  string    `gorm:"column:model_requested"`
	Stream          bool      `gorm:"column:stream"`
	AgentType       string    `gorm:"column:agent_type;index"`
	StatusCode      int       `gorm:"column:status_code"`
	StopReason      string    `gorm:"column:stop_reason"`
	NMessages       int       `gorm:"column:n_messages"`
	NToolUse        int       `gorm:"column:n_tool_use"`
	Messages        string    `gorm:"column:messages;type:text"`     // JSON array (adapter.Message[])
	RequestRaw      string    `gorm:"column:request_raw;type:text"`  // JSON object
	ResponseRaw     string    `gorm:"column:response_raw;type:text"` // TEXT (SSE transcript)
	ErrorRaw        string    `gorm:"column:error_raw;type:text"`
	CreatedAt       time.Time `gorm:"column:created_at;index"`
}

func (*TracePayloadRow) TableName() string { return "trace_payloads" }

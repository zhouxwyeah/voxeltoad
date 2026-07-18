//go:build dbtest

package store_test

import (
	"context"
	"database/sql"
	"testing"

	"voxeltoad/internal/observability"
	"voxeltoad/internal/store"
)

// RequestLogRepo must satisfy observability.RequestLogSink (the async request
// audit recorder's durable backend).
var _ observability.RequestLogSink = (*store.RequestLogRepo)(nil)

func freshRequestLogRepo(t *testing.T) (*store.RequestLogRepo, *store.DB) {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE request_logs`).Error; err != nil {
		t.Fatalf("truncate request_logs: %v", err)
	}
	return store.NewRequestLogRepo(db), db
}

func TestRequestLogRepo_RecordInsertsRow(t *testing.T) {
	ctx := context.Background()
	repo, db := freshRequestLogRepo(t)

	rec := observability.RequestLog{
		Tenant: "acme", Group: "team-a", APIKeyID: "key_01H",
		Provider: "openai", ModelRequested: "chat", ModelResolved: "gpt-4o",
		Stream: true, PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18,
		TTFTms: 120, Durationms: 350, ErrorType: "", BlockedBy: "", Fallback: false,
		RequestID: "req-1", SessionID: "sess-1", TraceID: "trace-1",
		SessionSource: "header-config",
	}
	if err := repo.Record(ctx, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var got struct {
		Tenant         string
		GroupName      string
		APIKeyID       string
		Provider       string
		ModelRequested string
		ModelResolved  string
		Stream         bool
		PromptTokens   int
		TotalTokens    int
		TTFTms         int    `gorm:"column:ttft_ms"`
		Durationms     int    `gorm:"column:duration_ms"`
		RequestID      string `gorm:"column:request_id"`
		SessionID      string `gorm:"column:session_id"`
		TraceID        string `gorm:"column:trace_id"`
		SessionSource  string `gorm:"column:session_source"`
	}
	err := db.Raw(
		`SELECT tenant, group_name, api_key_id, provider,
		        model_requested, model_resolved, stream,
		        prompt_tokens, total_tokens, ttft_ms, duration_ms,
		        request_id, session_id, trace_id, session_source
		 FROM request_logs`).Scan(&got).Error
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Tenant != "acme" || got.APIKeyID != "key_01H" {
		t.Errorf("identity = %+v", got)
	}
	if got.ModelRequested != "chat" || got.ModelResolved != "gpt-4o" || !got.Stream {
		t.Errorf("model/stream = %s/%s/%v", got.ModelRequested, got.ModelResolved, got.Stream)
	}
	if got.TotalTokens != 18 || got.TTFTms != 120 || got.Durationms != 350 {
		t.Errorf("tokens/timings = %d/%d/%d, want 18/120/350", got.TotalTokens, got.TTFTms, got.Durationms)
	}
	if got.RequestID != "req-1" || got.SessionID != "sess-1" || got.TraceID != "trace-1" {
		t.Errorf("correlation ids = %s/%s/%s, want req-1/sess-1/trace-1", got.RequestID, got.SessionID, got.TraceID)
	}
	if got.SessionSource != "header-config" {
		t.Errorf("session_source = %q, want header-config", got.SessionSource)
	}
}

func TestRequestLogRepo_RecordRejection(t *testing.T) {
	ctx := context.Background()
	repo, db := freshRequestLogRepo(t)

	// A rejected request (no usage) must still be auditable.
	rec := observability.RequestLog{
		Tenant: "acme", ModelRequested: "chat",
		ErrorType: "insufficient_quota", BlockedBy: "billing",
	}
	if err := repo.Record(ctx, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var got struct {
		ErrorType string
		BlockedBy string
	}
	if err := db.Raw(`SELECT error_type, blocked_by FROM request_logs`).Scan(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ErrorType != "insufficient_quota" || got.BlockedBy != "billing" {
		t.Errorf("rejection fields = %s/%s", got.ErrorType, got.BlockedBy)
	}
}

// TestRequestLogRepo_RecordUpstreamRequestID asserts the upstream provider's
// request id (OpenAI x-request-id, Anthropic request-id, …) is persisted to
// request_logs when set on the RequestLog record.
func TestRequestLogRepo_RecordUpstreamRequestID(t *testing.T) {
	ctx := context.Background()
	repo, db := freshRequestLogRepo(t)

	rec := observability.RequestLog{
		Tenant: "acme", Provider: "openai",
		ModelRequested: "chat", ModelResolved: "gpt-4o",
		RequestID: "gw-req-1", UpstreamRequestID: "req_abc123",
	}
	if err := repo.Record(ctx, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var got string
	if err := db.Raw(`SELECT upstream_request_id FROM request_logs`).Scan(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != "req_abc123" {
		t.Errorf("upstream_request_id = %q, want req_abc123", got)
	}
}

// TestRequestLogRepo_RecordUpstreamRequestIDDefault asserts a row inserted
// without an upstream id lands as ” (NOT NULL DEFAULT ”), not NULL — so
// existing code paths that don't set the field keep working.
func TestRequestLogRepo_RecordUpstreamRequestIDDefault(t *testing.T) {
	ctx := context.Background()
	repo, db := freshRequestLogRepo(t)

	rec := observability.RequestLog{
		Tenant: "acme", Provider: "openai", ModelRequested: "chat",
		// UpstreamRequestID intentionally left empty
	}
	if err := repo.Record(ctx, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var got sql.NullString
	if err := db.Raw(`SELECT upstream_request_id FROM request_logs`).Scan(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !got.Valid || got.String != "" {
		t.Errorf("upstream_request_id = %+v, want valid '' (NOT NULL DEFAULT '')", got)
	}
}

// TestRequestLogQuery_FilterByUpstreamRequestID asserts the reverse-lookup
// filter works: "which gateway request does this upstream id belong to?"
func TestRequestLogQuery_FilterByUpstreamRequestID(t *testing.T) {
	ctx := context.Background()
	repo, db := freshRequestLogRepo(t)

	if err := repo.Record(ctx, observability.RequestLog{
		Tenant: "acme", Provider: "openai", RequestID: "gw-1",
		UpstreamRequestID: "req_alpha",
	}); err != nil {
		t.Fatalf("Record #1: %v", err)
	}
	if err := repo.Record(ctx, observability.RequestLog{
		Tenant: "acme", Provider: "anthropic", RequestID: "gw-2",
		UpstreamRequestID: "req_beta",
	}); err != nil {
		t.Fatalf("Record #2: %v", err)
	}

	qrepo := store.NewRequestLogQueryRepo(db, "acme")
	rows, _, err := qrepo.ListPage(ctx, store.RequestLogFilter{UpstreamRequestID: "req_alpha"}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].RequestID != "gw-1" {
		t.Errorf("RequestID = %q, want gw-1", rows[0].RequestID)
	}
	if rows[0].UpstreamRequestID != "req_alpha" {
		t.Errorf("UpstreamRequestID = %q, want req_alpha", rows[0].UpstreamRequestID)
	}
}

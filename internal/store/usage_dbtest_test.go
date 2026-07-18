//go:build dbtest

package store_test

import (
	"context"
	"testing"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

// UsageRepo must satisfy billing.UsageSink (the async recorder's durable
// backend, ADR-0016).
var _ billing.UsageSink = (*store.UsageRepo)(nil)

func freshUsageRepo(t *testing.T) (*store.UsageRepo, *store.DB) {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE usage_records`).Error; err != nil {
		t.Fatalf("truncate usage_records: %v", err)
	}
	return store.NewUsageRepo(db), db
}

func TestUsageRepo_RecordInsertsRow(t *testing.T) {
	ctx := context.Background()
	repo, db := freshUsageRepo(t)

	rec := billing.UsageRecord{
		Tenant: "acme", Group: "team-a", APIKeyID: "key_01H",
		Provider: "openai", Model: "gpt-4o",
		PromptTokens: 1000, CompletionTokens: 500, Cost: 12500,
		RequestID: "req_01H", SessionID: "sess_42",
	}
	if err := repo.Record(ctx, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var got struct {
		Tenant           string
		GroupName        string
		APIKeyID         string
		Provider         string
		Model            string
		PromptTokens     int
		CompletionTokens int
		Cost             int64
		RequestID        string
		SessionID        string
	}
	err := db.Raw(
		`SELECT tenant, group_name, api_key_id, provider, model,
		        prompt_tokens, completion_tokens, cost,
		        request_id, session_id
		 FROM usage_records`).Scan(&got).Error
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Tenant != "acme" || got.GroupName != "team-a" || got.APIKeyID != "key_01H" {
		t.Errorf("identity = %+v, want acme/team-a/key_01H", got)
	}
	if got.Provider != "openai" || got.Model != "gpt-4o" {
		t.Errorf("provider/model = %s/%s", got.Provider, got.Model)
	}
	if got.PromptTokens != 1000 || got.CompletionTokens != 500 || got.Cost != 12500 {
		t.Errorf("tokens/cost = %d/%d/%d, want 1000/500/12500", got.PromptTokens, got.CompletionTokens, got.Cost)
	}
	if got.RequestID != "req_01H" || got.SessionID != "sess_42" {
		t.Errorf("tracing = %s/%s, want req_01H/sess_42", got.RequestID, got.SessionID)
	}
}

func TestUsageRepo_RecordBatch(t *testing.T) {
	ctx := context.Background()
	repo, db := freshUsageRepo(t)

	recs := []billing.UsageRecord{
		{Tenant: "a", Provider: "openai", Model: "m", Cost: 1},
		{Tenant: "b", Provider: "claude", Model: "m", Cost: 2},
		{Tenant: "c", Provider: "openai", Model: "m", Cost: 3},
	}
	if err := repo.RecordBatch(ctx, recs); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}

	var count int64
	if err := db.Raw(`SELECT count(*) FROM usage_records`).Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("row count = %d, want 3", count)
	}
}

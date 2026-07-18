//go:build dbtest

package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"voxeltoad/internal/observability"
	"voxeltoad/internal/store"
)

// TracePayloadRepo must satisfy observability.TracePayloadSink (the async trace
// recorder's durable backend).
var _ observability.TracePayloadSink = (*store.TracePayloadRepo)(nil)

func freshTracePayloadRepo(t *testing.T) (*store.TracePayloadRepo, *store.DB) {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE trace_payloads`).Error; err != nil {
		t.Fatalf("truncate trace_payloads: %v", err)
	}
	return store.NewTracePayloadRepo(db), db
}

func TestTracePayloadRepo_RecordInsertsRow(t *testing.T) {
	ctx := context.Background()
	repo, db := freshTracePayloadRepo(t)

	p := observability.TracePayload{
		RequestID: "req-1", SessionID: "sess-1", TraceID: "trace-1",
		Tenant: "acme", Group: "team-a", APIKeyID: "key_01H",
		Provider: "openai", ModelRequested: "chat", Stream: true,
		StatusCode: 200, StopReason: "stop", NMessages: 3, NToolUse: 1,
		Messages:    jsonRaw(`[{"role":"user","content":"hi"}]`),
		RequestRaw:  jsonRaw(`{"model":"chat","messages":[]}`),
		ResponseRaw: `{"id":"r","choices":[]}`,
		ErrorRaw:    "",
	}
	if err := repo.Record(ctx, p); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var got struct {
		RequestID  string `gorm:"column:request_id"`
		Tenant     string
		Provider   string
		Stream     bool
		NMessages  int    `gorm:"column:n_messages"`
		NToolUse   int    `gorm:"column:n_tool_use"`
		StopReason string `gorm:"column:stop_reason"`
		Messages   []byte
	}
	if err := db.Raw(`SELECT request_id, tenant, provider, stream, n_messages, n_tool_use, stop_reason, messages FROM trace_payloads`).Scan(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.RequestID != "req-1" || got.Tenant != "acme" || !got.Stream {
		t.Errorf("identity/stream = %+v", got)
	}
	if got.NMessages != 3 || got.NToolUse != 1 || got.StopReason != "stop" {
		t.Errorf("summary dims = %d/%d/%q, want 3/1/stop", got.NMessages, got.NToolUse, got.StopReason)
	}
	if string(got.Messages) == "" {
		t.Errorf("messages JSONB is empty")
	}
	// JSONB re-normalizes whitespace on read-back; compare structurally.
	var msg []map[string]any
	if err := json.Unmarshal(got.Messages, &msg); err != nil {
		t.Fatalf("messages not valid JSON: %v (raw=%s)", err, got.Messages)
	}
	if len(msg) != 1 || msg[0]["role"] != "user" || msg[0]["content"] != "hi" {
		t.Errorf("messages = %+v, want one user message 'hi'", msg)
	}
}

// TestTracePayloadRepo_RecordStreamingSSE verifies that a streaming response
// transcript (an SSE wire-frame, which is not valid JSON) can be inserted into
// the now-TEXT response_raw column and round-trips verbatim.
func TestTracePayloadRepo_RecordStreamingSSE(t *testing.T) {
	ctx := context.Background()
	repo, db := freshTracePayloadRepo(t)

	sse := "data: {\"id\":\"chunk-1\",\"object\":\"chat.completion.chunk\"}\n\ndata: [DONE]\n\n"
	p := observability.TracePayload{
		RequestID:      "req-stream",
		SessionID:      "sess-stream",
		TraceID:        "trace-stream",
		Tenant:         "acme",
		Provider:       "openai",
		ModelRequested: "chat",
		Stream:         true,
		StatusCode:     200,
		StopReason:     "stop",
		ResponseRaw:    sse,
	}
	if err := repo.Record(ctx, p); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var got string
	if err := db.Raw(`SELECT response_raw FROM trace_payloads`).Scan(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != sse {
		t.Errorf("response_raw = %q, want %q", got, sse)
	}
}

func TestTracePayloadRepo_EmptyBodiesDefaultToLiterals(t *testing.T) {
	// An empty RawMessage must be stored as the JSON literal default ('[]'/'{}'),
	// never NULL — the columns are NOT NULL.
	ctx := context.Background()
	repo, db := freshTracePayloadRepo(t)

	if err := repo.Record(ctx, observability.TracePayload{RequestID: "req-2"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var got struct {
		Messages    []byte
		RequestRaw  []byte `gorm:"column:request_raw"`
		ResponseRaw string `gorm:"column:response_raw"`
	}
	if err := db.Raw(`SELECT messages, request_raw, response_raw FROM trace_payloads`).Scan(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	// An empty literal '[]' round-trips through JSONB unchanged; '{}' likewise.
	// If not, parse structurally so a re-normalization ('{ }') doesn't false-fail.
	var mArr []any
	if err := json.Unmarshal(got.Messages, &mArr); err != nil {
		t.Fatalf("messages not valid JSON: %v (raw=%s)", err, got.Messages)
	}
	if len(mArr) != 0 {
		t.Errorf("empty-body messages default = %s, want empty array", got.Messages)
	}
	var rObj map[string]any
	if err := json.Unmarshal(got.RequestRaw, &rObj); err != nil {
		t.Fatalf("request_raw not valid JSON: %v", err)
	}
	if len(rObj) != 0 {
		t.Errorf("empty-body request_raw default = %s, want empty object", got.RequestRaw)
	}
}

func jsonRaw(s string) []byte { return []byte(s) }

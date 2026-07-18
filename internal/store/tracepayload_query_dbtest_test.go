//go:build dbtest

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

// seedTracePayload inserts a trace_payloads row with explicit fields for the
// query tests.
func seedTracePayload(t *testing.T, db *store.DB, tenant, requestID, sessionID, provider string, at time.Time) {
	t.Helper()
	if err := db.Exec(`INSERT INTO trace_payloads
	    (request_id, session_id, tenant, provider, model_requested,
	     status_code, stop_reason, n_messages, n_tool_use, messages, request_raw, response_raw, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?)`,
		requestID, sessionID, tenant, provider, "chat",
		200, "stop", 2, 0,
		[]byte(`[{"role":"user","content":"hi"}]`), []byte(`{"model":"chat"}`), at).Error; err != nil {
		t.Fatalf("seed trace_payloads: %v", err)
	}
}

func TestTracePayloadQuery_ListBySessionTimelineOrder(t *testing.T) {
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE trace_payloads`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t0 := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)
	seedTracePayload(t, db, "acme", "r1", "sess-1", "openai", t1)
	seedTracePayload(t, db, "acme", "r2", "sess-1", "openai", t0) // older
	seedTracePayload(t, db, "acme", "r3", "sess-2", "openai", t2) // other session

	repo := store.NewTracePayloadQueryRepo(db, "") // global
	rows, err := repo.ListBySession(context.Background(), "sess-1", 0)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows for sess-1, want 2", len(rows))
	}
	// ASC timeline order: r2 (older) before r1.
	if rows[0].RequestID != "r2" || rows[1].RequestID != "r1" {
		t.Errorf("order = %s,%s; want r2,r1 (ASC by created_at)", rows[0].RequestID, rows[1].RequestID)
	}
	// Summary row must NOT carry the bodies — verify via the summary fields only.
	if rows[0].NMessages != 2 || rows[0].StatusCode != 200 {
		t.Errorf("summary dims = %+v", rows[0])
	}
}

func TestTracePayloadQuery_TenantScopeIsolates(t *testing.T) {
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE trace_payloads`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	at := time.Now()
	seedTracePayload(t, db, "acme", "ra", "sess-1", "openai", at)
	seedTracePayload(t, db, "globex", "rb", "sess-1", "openai", at) // other tenant, same session

	// tenant-admin of "acme" must see only its own row.
	repo := store.NewTracePayloadQueryRepo(db, "acme")
	rows, err := repo.ListBySession(context.Background(), "sess-1", 0)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "ra" {
		t.Errorf("tenant scope leaked: got %+v", rows)
	}
}

func TestTracePayloadQuery_GetByRequestIDReturnsBodies(t *testing.T) {
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE trace_payloads`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	seedTracePayload(t, db, "acme", "r-detail", "sess-1", "openai", time.Now())

	repo := store.NewTracePayloadQueryRepo(db, "")
	d, ok, err := repo.GetByRequestID(context.Background(), "r-detail")
	if err != nil {
		t.Fatalf("GetByRequestID: %v", err)
	}
	if !ok {
		t.Fatal("payload not found")
	}
	if d.RequestID != "r-detail" {
		t.Errorf("request_id = %s", d.RequestID)
	}
	// Detail must carry the message body.
	var msg []map[string]any
	if err := json.Unmarshal(d.Messages, &msg); err != nil {
		t.Fatalf("messages not valid JSON: %v", err)
	}
	if len(msg) != 1 || msg[0]["role"] != "user" {
		t.Errorf("messages = %+v", msg)
	}
	// response_raw is TEXT; an empty upstream body should round-trip as ''.
	if d.ResponseRaw != "" {
		t.Errorf("response_raw = %q, want empty string", d.ResponseRaw)
	}
}

func TestTracePayloadQuery_GetByRequestIDNotFound(t *testing.T) {
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE trace_payloads`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	repo := store.NewTracePayloadQueryRepo(db, "")
	_, ok, err := repo.GetByRequestID(context.Background(), "nope")
	if err != nil || ok {
		t.Errorf("expected (nil,false,nil), got ok=%v err=%v", ok, err)
	}
}

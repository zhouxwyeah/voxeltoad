//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

func freshDpRepo(t *testing.T) (*store.DataPlaneRepo, *store.DB) {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE data_plane_nodes`).Error; err != nil {
		t.Fatalf("truncate data_plane_nodes: %v", err)
	}
	return store.NewDataPlaneRepo(db), db
}

func TestDataPlaneRepo_RegisterAndList(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshDpRepo(t)

	n := &store.DataPlaneNode{
		InstanceID:       "host-1-1234",
		Hostname:         "host-1",
		Addr:             ":8080",
		Version:          "v1.0",
		Commit:           "abc12345",
		ConfigGeneration: 5,
	}
	if err := repo.Register(ctx, n); err != nil {
		t.Fatalf("Register: %v", err)
	}

	rows, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.InstanceID != "host-1-1234" {
		t.Errorf("InstanceID = %q", r.InstanceID)
	}
	if r.Status != "online" {
		t.Errorf("Status = %q, want online", r.Status)
	}
	if r.Version != "v1.0" {
		t.Errorf("Version = %q", r.Version)
	}
	if r.ConfigGeneration != 5 {
		t.Errorf("ConfigGeneration = %d, want 5", r.ConfigGeneration)
	}
}

func TestDataPlaneRepo_RegisterIdempotent(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshDpRepo(t)

	n1 := &store.DataPlaneNode{InstanceID: "h1", Hostname: "h1", Addr: "a1", Version: "v1"}
	n2 := &store.DataPlaneNode{InstanceID: "h1", Hostname: "h1", Addr: "a2", Version: "v2"}

	if err := repo.Register(ctx, n1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Register(ctx, n2); err != nil {
		t.Fatal(err)
	}

	rows, _ := repo.List(ctx)
	if len(rows) != 1 {
		t.Fatalf("List len = %d, want 1 (upsert by instance_id)", len(rows))
	}
	if rows[0].Addr != "a2" {
		t.Errorf("Addr = %q, want a2 (latest upsert)", rows[0].Addr)
	}
	if rows[0].Version != "v2" {
		t.Errorf("Version = %q, want v2 (latest upsert)", rows[0].Version)
	}
}

func TestDataPlaneRepo_Heartbeat(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshDpRepo(t)

	n := &store.DataPlaneNode{InstanceID: "hb-test", Hostname: "h", Addr: ":8080", Version: "dev"}
	_ = repo.Register(ctx, n)

	before, _ := repo.List(ctx)
	initial := before[0].LastHeartbeatAt

	time.Sleep(10 * time.Millisecond)
	if err := repo.Heartbeat(ctx, "hb-test"); err != nil {
		t.Fatal(err)
	}

	after, _ := repo.List(ctx)
	if !after[0].LastHeartbeatAt.After(initial) {
		t.Errorf("heartbeat did not advance: before=%v after=%v", initial, after[0].LastHeartbeatAt)
	}
}

func TestDataPlaneRepo_Drain(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshDpRepo(t)

	n := &store.DataPlaneNode{InstanceID: "drain-me", Hostname: "h", Addr: ":8080", Version: "dev"}
	_ = repo.Register(ctx, n)

	if err := repo.Drain(ctx, "drain-me"); err != nil {
		t.Fatal(err)
	}

	rows, _ := repo.List(ctx)
	if rows[0].Status != "draining" {
		t.Errorf("Status = %q, want draining", rows[0].Status)
	}
}

func TestDataPlaneRepo_CleanupStale(t *testing.T) {
	ctx := context.Background()
	repo, db := freshDpRepo(t)

	// Register a "live" node and a node with a stale heartbeat.
	live := &store.DataPlaneNode{InstanceID: "fresh", Hostname: "h", Addr: ":8080", Version: "dev"}
	_ = repo.Register(ctx, live)
	_ = repo.Heartbeat(ctx, "fresh") // fresh heartbeat

	stale := &store.DataPlaneNode{InstanceID: "stale", Hostname: "h", Addr: ":8081", Version: "dev"}
	_ = repo.Register(ctx, stale)
	// Backdate the stale node's heartbeat by 60s using raw SQL.
	if err := db.Exec(`UPDATE data_plane_nodes SET last_heartbeat_at = now() - interval '60 seconds' WHERE instance_id = 'stale'`).Error; err != nil {
		t.Fatalf("backdate stale heartbeat: %v", err)
	}

	// CleanupStale with 45s threshold — only the stale node should be affected.
	n, err := repo.CleanupStale(ctx, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CleanupStale affected = %d, want 1", n)
	}

	rows, _ := repo.List(ctx)
	for _, r := range rows {
		if r.InstanceID == "stale" && r.Status != "offline" {
			t.Errorf("stale node status = %q, want offline", r.Status)
		}
		if r.InstanceID == "fresh" && r.Status != "online" {
			t.Errorf("fresh node status = %q, want online", r.Status)
		}
	}
}

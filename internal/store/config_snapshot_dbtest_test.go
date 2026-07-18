//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

// waitSnapshot polls until a snapshot at version exists, or fails after 2s.
func waitSnapshot(t *testing.T, snap *store.ConfigSnapshotRepo, version int64) *config.Dynamic {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d, err := snap.Get(context.Background(), version)
		if err != nil {
			t.Fatalf("Get v%d: %v", version, err)
		}
		if d != nil {
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("snapshot v%d not saved after 2s (async goroutine not flushed)", version)
	return nil
}

// freshSnapshotRepo returns a ConfigSnapshotRepo wired to a fresh migrated DB
// and a ConfigRepo that snapshots after each mutation.
func freshSnapshotRepo(t *testing.T) (*store.ConfigSnapshotRepo, *store.ConfigRepo) {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE provider_credentials, providers, models, routes, plugins`).Error; err != nil {
		t.Fatalf("truncate config tables: %v", err)
	}
	if err := db.Exec(`UPDATE gateway_settings SET spec = '{}', updated_at = now() WHERE id = 1`).Error; err != nil {
		t.Fatalf("reset gateway_settings: %v", err)
	}
	if err := db.Exec(`DELETE FROM config_snapshots`).Error; err != nil {
		t.Fatalf("truncate config_snapshots: %v", err)
	}
	if err := db.Exec(`UPDATE config_generation SET version = 0`).Error; err != nil {
		t.Fatalf("reset config_generation: %v", err)
	}
	snap := store.NewConfigSnapshotRepo(db)
	repo := store.NewConfigRepo(db, snap)
	return snap, repo
}

// A config write saves a snapshot whose version matches config_generation.
func TestSnapshotRepo_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	if err := repo.UpsertProvider(ctx, config.Provider{
		Name: "p", Type: "openai", Adapter: "openai", BaseURL: "https://api.example.com",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	d := waitSnapshot(t, snap, 1)
	if d.Version != "1" {
		t.Errorf("Version = %q, want \"1\"", d.Version)
	}
	if len(d.Providers) != 1 || d.Providers[0].Name != "p" {
		t.Errorf("providers = %+v, want [p]", d.Providers)
	}
}

// Get for a non-existent version returns nil without error.
func TestSnapshotRepo_GetMissing(t *testing.T) {
	ctx := context.Background()
	snap, _ := freshSnapshotRepo(t)

	d, err := snap.Get(ctx, 99999)
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Errorf("Get missing version: got %+v, want nil", d)
	}
}

// SaveSnapshot is idempotent — calling it twice with the same version does not
// error or duplicate rows.
func TestSnapshotRepo_SaveIdempotent(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	_ = repo.UpsertProvider(ctx, config.Provider{Name: "a", Type: "openai", Adapter: "openai"})
	d := waitSnapshot(t, snap, 1)

	// Save again with same version.
	if err := snap.SaveSnapshot(ctx, 1, d); err != nil {
		t.Fatalf("save again (same version): %v", err)
	}

	rows, _, err := snap.ListSnapshots(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, r := range rows {
		if r.Version == 1 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("version 1 count = %d, want 1 (idempotent)", count)
	}
}

// Multiple writes produce multiple snapshots in descending order.
func TestSnapshotRepo_ListDescending(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	_ = repo.UpsertProvider(ctx, config.Provider{Name: "a", Type: "openai", Adapter: "openai"})
	waitSnapshot(t, snap, 1)
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "b", Type: "openai", Adapter: "openai"})
	waitSnapshot(t, snap, 2)

	rows, _, err := snap.ListSnapshots(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 snapshots, got %d", len(rows))
	}
	// Most recent first.
	if rows[0].Version <= rows[len(rows)-1].Version {
		t.Errorf("snapshots not in descending order: %v", rows)
	}
}

// Diff detects added and deleted resources between two versions.
func TestSnapshotRepo_Diff(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p1", Type: "openai", Adapter: "openai"})
	waitSnapshot(t, snap, 1)
	_ = repo.DeleteProvider(ctx, "p1")
	waitSnapshot(t, snap, 2)

	diff, err := snap.Diff(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.AddedProviders) != 0 {
		t.Errorf("v1→v2 should add no providers, got %v", diff.AddedProviders)
	}
	if len(diff.DeletedProviders) != 1 || diff.DeletedProviders[0] != "p1" {
		t.Errorf("v1→v2 should delete p1, got %v", diff.DeletedProviders)
	}
}

// Rollback restores config to a previous snapshot.
func TestSnapshotRepo_Rollback(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	// v1: create p1
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p1", Type: "openai", Adapter: "openai"})
	waitSnapshot(t, snap, 1)
	// v2: create p2
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p2", Type: "claude", Adapter: "claude"})
	waitSnapshot(t, snap, 2)

	// Rollback to v1 (should have only p1).
	if err := snap.Rollback(ctx, 1); err != nil {
		t.Fatalf("Rollback to v1: %v", err)
	}

	d, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Providers) != 1 || d.Providers[0].Name != "p1" {
		t.Errorf("after rollback providers = %+v, want [p1]", d.Providers)
	}
	if d.Version != "3" {
		t.Errorf("after rollback Version = %q, want \"3\" (bump after rollback)", d.Version)
	}
}

// Rollback restores all resource types: models, routes, and plugins alongside
// providers. Verifies the full config is restored atomically.
func TestSnapshotRepo_RollbackWithAllResources(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	// v1: create provider + model + route + plugin
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p", Type: "openai", Adapter: "openai"})
	_ = repo.UpsertModel(ctx, config.Model{
		Alias: "chat", Upstreams: []config.ModelUpstream{{Provider: "p", UpstreamModel: "gpt-4o"}},
	})
	_ = repo.UpsertRoute(ctx, config.Route{
		ModelAlias: "chat", Strategy: "priority",
		Providers: []config.RouteProvider{{Name: "p"}},
	})
	_ = repo.UpsertPlugin(ctx, config.PluginConfig{
		Name: "audit", Phase: "post", Scope: "", Enabled: true,
	})
	waitSnapshot(t, snap, 4)
	// v5: delete everything
	_ = repo.DeleteProvider(ctx, "p")
	_ = repo.DeleteModel(ctx, "chat")
	_ = repo.DeleteRoute(ctx, "chat")
	_ = repo.DeletePlugin(ctx, "audit", "")
	waitSnapshot(t, snap, 8)

	// Rollback to v4: all resources should be restored.
	if err := snap.Rollback(ctx, 4); err != nil {
		t.Fatalf("Rollback to v4: %v", err)
	}
	d, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Providers) != 1 || d.Providers[0].Name != "p" {
		t.Errorf("providers = %+v, want [p]", d.Providers)
	}
	if len(d.Models) != 1 || d.Models[0].Alias != "chat" {
		t.Errorf("models = %+v, want [chat]", d.Models)
	}
	if len(d.Routes) != 1 || d.Routes[0].ModelAlias != "chat" {
		t.Errorf("routes = %+v, want [chat]", d.Routes)
	}
	if len(d.Plugins) != 1 || d.Plugins[0].Name != "audit" {
		t.Errorf("plugins = %+v, want [audit]", d.Plugins)
	}
	if d.Version != "9" {
		t.Errorf("Version = %q, want \"9\" (4 creates + 4 deletes + 1 rollback)", d.Version)
	}
}

// Rollback restores gateway_settings alongside the resource tables, so a
// rollback yields a fully consistent restore (ADR-0025). Verifies the settings
// dimension is not silently dropped.
func TestSnapshotRepo_RollbackRestoresSettings(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	// v1: settings with capture ON + 64KB cap.
	if err := repo.UpdateSettings(ctx, &config.GatewaySettings{Trace: config.TraceSettings{
		CapturePayloadEnabled: true, MaxBodyKB: 64,
	}}); err != nil {
		t.Fatalf("UpdateSettings v1: %v", err)
	}
	waitSnapshot(t, snap, 1)
	// v2: settings with capture OFF.
	if err := repo.UpdateSettings(ctx, &config.GatewaySettings{Trace: config.TraceSettings{
		CapturePayloadEnabled: false,
	}}); err != nil {
		t.Fatalf("UpdateSettings v2: %v", err)
	}
	waitSnapshot(t, snap, 2)

	// Live settings now reflect v2 (capture off).
	live, err := repo.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if live.Trace.CapturePayloadEnabled {
		t.Fatal("precondition: capture should be off after v2")
	}

	// Rollback to v1: capture must be restored to ON + 64KB.
	if err := snap.Rollback(ctx, 1); err != nil {
		t.Fatalf("Rollback to v1: %v", err)
	}
	live, err = repo.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !live.Trace.CapturePayloadEnabled || live.Trace.MaxBodyKB != 64 {
		t.Errorf("after rollback, settings = %+v, want capture ON + 64KB", live.Trace)
	}
}

// Diff with all resource types validates added and deleted detection across
// providers, models, routes, and plugins.
func TestSnapshotRepo_Diff_AllResources(t *testing.T) {
	ctx := context.Background()
	snap, repo := freshSnapshotRepo(t)

	// v1: baseline with one of each
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p1", Type: "openai", Adapter: "openai"})
	_ = repo.UpsertModel(ctx, config.Model{
		Alias: "m1", Upstreams: []config.ModelUpstream{{Provider: "p1", UpstreamModel: "gpt-4o"}},
	})
	waitSnapshot(t, snap, 2)

	// v3: add p2 and m2, create route, add plugin
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p2", Type: "claude", Adapter: "claude"})
	_ = repo.UpsertModel(ctx, config.Model{
		Alias: "m2", Upstreams: []config.ModelUpstream{{Provider: "p1", UpstreamModel: "gpt-3.5"}},
	})
	_ = repo.UpsertRoute(ctx, config.Route{
		ModelAlias: "m1", Strategy: "weighted",
		Providers: []config.RouteProvider{{Name: "p1"}},
	})
	_ = repo.UpsertPlugin(ctx, config.PluginConfig{
		Name: "ratelimit", Phase: "pre", Scope: "", Enabled: true,
	})
	waitSnapshot(t, snap, 6)

	diff, err := snap.Diff(ctx, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.AddedProviders) != 1 || diff.AddedProviders[0] != "p2" {
		t.Errorf("added providers = %v, want [p2]", diff.AddedProviders)
	}
	if len(diff.DeletedProviders) != 0 {
		t.Errorf("unexpected deleted providers: %v", diff.DeletedProviders)
	}
	if len(diff.AddedModels) != 1 || diff.AddedModels[0] != "m2" {
		t.Errorf("added models = %v, want [m2]", diff.AddedModels)
	}
	if len(diff.AddedRoutes) != 1 || diff.AddedRoutes[0] != "m1" {
		t.Errorf("added routes = %v, want [m1]", diff.AddedRoutes)
	}
	if len(diff.AddedPlugins) != 1 || diff.AddedPlugins[0] != "ratelimit/" {
		t.Errorf("added plugins = %v, want [ratelimit/]", diff.AddedPlugins)
	}
}

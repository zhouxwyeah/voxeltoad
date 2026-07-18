//go:build dbtest

package store_test

import (
	"context"
	"testing"

	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

func freshConfigRepo(t *testing.T) *store.ConfigRepo {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE provider_credentials, providers, models, routes, plugins`).Error; err != nil {
		t.Fatalf("truncate config tables: %v", err)
	}
	if err := db.Exec(`UPDATE config_generation SET version = 0`).Error; err != nil {
		t.Fatalf("reset config_generation: %v", err)
	}
	return store.NewConfigRepo(db, nil)
}

// Each config write bumps config_generation (same tx), and Snapshot reflects the
// stored config with that version.
func TestConfigRepo_UpsertAndSnapshot(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)

	if err := repo.UpsertProvider(ctx, config.Provider{
		Name: "openai-prod", Type: "openai", Adapter: "openai",
		BaseURL: "https://api.openai.com/v1", APIKeyRef: "env://OPENAI_KEY", Weight: 1,
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if err := repo.UpsertModel(ctx, config.Model{
		Alias: "chat",
		Upstreams: []config.ModelUpstream{{
			Provider: "openai-prod", UpstreamModel: "gpt-4o",
			Pricing: config.Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000, Currency: "usd"},
		}},
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}
	if err := repo.UpsertRoute(ctx, config.Route{
		ModelAlias: "chat", Strategy: "priority",
		Providers: []config.RouteProvider{{Name: "openai-prod"}},
	}); err != nil {
		t.Fatalf("UpsertRoute: %v", err)
	}

	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Version is the config_generation after 3 writes.
	if snap.Version != "3" {
		t.Errorf("Version = %q, want \"3\" (3 writes)", snap.Version)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].Name != "openai-prod" ||
		snap.Providers[0].APIKeyRef != "env://OPENAI_KEY" {
		t.Errorf("providers = %+v, want one openai-prod", snap.Providers)
	}
	if len(snap.Models) != 1 || snap.Models[0].Alias != "chat" ||
		len(snap.Models[0].Upstreams) != 1 || snap.Models[0].Upstreams[0].Pricing.CompletionPer1M != 15_000_000 {
		t.Errorf("models = %+v, want chat with pricing", snap.Models)
	}
	if len(snap.Routes) != 1 || snap.Routes[0].ModelAlias != "chat" {
		t.Errorf("routes = %+v, want one chat route", snap.Routes)
	}
}

// Upsert on an existing identity updates in place (no duplicate row) and still
// bumps the version.
func TestConfigRepo_UpsertReplaces(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)

	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p", Adapter: "openai", BaseURL: "u1"})
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p", Adapter: "openai", BaseURL: "u2"})

	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 {
		t.Fatalf("provider count = %d, want 1 (upsert replaces)", len(snap.Providers))
	}
	if snap.Providers[0].BaseURL != "u2" {
		t.Errorf("BaseURL = %q, want u2 (latest)", snap.Providers[0].BaseURL)
	}
	if snap.Version != "2" {
		t.Errorf("Version = %q, want \"2\"", snap.Version)
	}
}

// Delete removes a resource from the snapshot and bumps the version.
func TestConfigRepo_Delete(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)

	_ = repo.UpsertProvider(ctx, config.Provider{Name: "a", Adapter: "openai", BaseURL: "u"})
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "b", Adapter: "openai", BaseURL: "u"})
	if err := repo.DeleteProvider(ctx, "a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].Name != "b" {
		t.Errorf("providers = %+v, want only b", snap.Providers)
	}
	if snap.Version != "3" { // 2 upserts + 1 delete
		t.Errorf("Version = %q, want \"3\"", snap.Version)
	}
}

// List returns the stored resources (admin read path).
func TestConfigRepo_ListProviders(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "a", Adapter: "openai", BaseURL: "u"})
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "b", Adapter: "claude", BaseURL: "u"})

	got, err := repo.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("list len = %d, want 2", len(got))
	}
}

// An empty config still yields a valid snapshot at the current version.
func TestConfigRepo_EmptySnapshot(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)
	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "0" {
		t.Errorf("empty Version = %q, want \"0\"", snap.Version)
	}
	if len(snap.Providers) != 0 || len(snap.Models) != 0 || len(snap.Routes) != 0 {
		t.Errorf("empty snapshot should have no config: %+v", snap)
	}
}

// GetModel returns a single model by alias; ok=false when not found.
func TestConfigRepo_GetModel(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p", Adapter: "openai", BaseURL: "u"})
	if err := repo.UpsertModel(ctx, config.Model{
		Alias:     "chat",
		Upstreams: []config.ModelUpstream{{Provider: "p", UpstreamModel: "gpt-4o"}},
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	got, ok, err := repo.GetModel(ctx, "chat")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if !ok {
		t.Fatal("GetModel: not found, want found")
	}
	if got.Alias != "chat" || len(got.Upstreams) != 1 || got.Upstreams[0].UpstreamModel != "gpt-4o" {
		t.Errorf("GetModel = %+v, want chat/gpt-4o", got)
	}

	_, ok2, err := repo.GetModel(ctx, "ghost")
	if err != nil {
		t.Fatalf("GetModel ghost: %v", err)
	}
	if ok2 {
		t.Error("GetModel ghost: ok=true, want false")
	}
}

// GetRoute returns a single route by model_alias; ok=false when not found.
func TestConfigRepo_GetRoute(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)
	if err := repo.UpsertRoute(ctx, config.Route{
		ModelAlias: "chat", Strategy: "priority",
		Providers: []config.RouteProvider{{Name: "p"}},
	}); err != nil {
		t.Fatalf("UpsertRoute: %v", err)
	}

	got, ok, err := repo.GetRoute(ctx, "chat")
	if err != nil {
		t.Fatalf("GetRoute: %v", err)
	}
	if !ok {
		t.Fatal("GetRoute: not found, want found")
	}
	if got.ModelAlias != "chat" || got.Strategy != "priority" || len(got.Providers) != 1 {
		t.Errorf("GetRoute = %+v, want chat/priority/[p]", got)
	}

	_, ok2, err := repo.GetRoute(ctx, "ghost")
	if err != nil {
		t.Fatalf("GetRoute ghost: %v", err)
	}
	if ok2 {
		t.Error("GetRoute ghost: ok=true, want false")
	}
}

// PatchModel applies metadata fields (description, context_length,
// capabilities, tags) per ADR-0030: a present key overwrites; an omitted key
// is left unchanged. A zero-value slice clears the field.
func TestConfigRepo_PatchModelMetadata(t *testing.T) {
	ctx := context.Background()
	repo := freshConfigRepo(t)
	_ = repo.UpsertProvider(ctx, config.Provider{Name: "p", Adapter: "openai", BaseURL: "u"})
	if err := repo.UpsertModel(ctx, config.Model{
		Alias:         "chat",
		Description:   "original",
		ContextLength: 8000,
		Capabilities:  []string{"vision"},
		Tags:          []string{"old"},
		Upstreams:     []config.ModelUpstream{{Provider: "p", UpstreamModel: "gpt-4o"}},
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	// Patch description + context_length + capabilities; leave tags unchanged.
	desc := "patched"
	cl := 128000
	caps := []string{"vision", "function_calling"}
	patched, ok, err := repo.PatchModel(ctx, "chat", store.ModelPatch{
		Description:   &desc,
		ContextLength: &cl,
		Capabilities:  &caps,
	})
	if err != nil {
		t.Fatalf("PatchModel: %v", err)
	}
	if !ok {
		t.Fatal("PatchModel: not found")
	}
	if patched.Description != "patched" || patched.ContextLength != 128000 {
		t.Errorf("patched metadata = %+v, want patched/128000", patched)
	}
	if len(patched.Capabilities) != 2 || patched.Capabilities[1] != "function_calling" {
		t.Errorf("patched capabilities = %+v, want [vision function_calling]", patched.Capabilities)
	}
	// Tags untouched (omitted from patch).
	if len(patched.Tags) != 1 || patched.Tags[0] != "old" {
		t.Errorf("tags should be unchanged, got %+v", patched.Tags)
	}

	// Verify persistence via GetModel.
	got, _, err := repo.GetModel(ctx, "chat")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.Description != "patched" || got.ContextLength != 128000 {
		t.Errorf("GetModel metadata = %+v, want patched/128000", got)
	}

	// Clear capabilities by sending an empty slice.
	empty := []string{}
	patched2, _, err := repo.PatchModel(ctx, "chat", store.ModelPatch{Capabilities: &empty})
	if err != nil {
		t.Fatalf("PatchModel clear: %v", err)
	}
	if len(patched2.Capabilities) != 0 {
		t.Errorf("cleared capabilities = %+v, want empty", patched2.Capabilities)
	}
	// Tags still intact from original.
	if len(patched2.Tags) != 1 || patched2.Tags[0] != "old" {
		t.Errorf("tags should survive capabilities-clear, got %+v", patched2.Tags)
	}
}

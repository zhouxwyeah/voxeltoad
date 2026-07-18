//go:build dbtest

package store_test

import (
	"context"
	"testing"

	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

// TestGatewaySettings_GetUpdateRoundTrip verifies the settings document survives
// a write→read cycle and bumps the snapshot version (so the data plane picks it
// up). ADR: gateway_settings single-row JSONB.
func TestGatewaySettings_GetUpdateRoundTrip(t *testing.T) {
	db := mustMigratedDB(t)
	repo := store.NewConfigRepo(db, nil)

	// Default (seeded '{}') → zero-value GatewaySettings (capture off).
	got, err := repo.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Trace.CapturePayloadEnabled {
		t.Errorf("default CapturePayloadEnabled = true, want false")
	}

	// Version before update.
	before := snapshotVersion(t, db)

	// Update: turn on trace capture with a body cap.
	in := &config.GatewaySettings{Trace: config.TraceSettings{
		CapturePayloadEnabled: true,
		MaxBodyKB:             64,
		RetentionDays:         14,
	}}
	if err := repo.UpdateSettings(context.Background(), in); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// Version bumped.
	after := snapshotVersion(t, db)
	if after <= before {
		t.Errorf("config_generation not bumped: before=%d after=%d", before, after)
	}

	// Read back matches.
	got, err = repo.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings after update: %v", err)
	}
	if !got.Trace.CapturePayloadEnabled || got.Trace.MaxBodyKB != 64 || got.Trace.RetentionDays != 14 {
		t.Errorf("round-trip mismatch: %+v", got.Trace)
	}
}

// TestGatewaySettings_SnapshotIncludesSettings verifies Snapshot() populates
// Dynamic.Settings from the table, so the data plane receives it.
func TestGatewaySettings_SnapshotIncludesSettings(t *testing.T) {
	db := mustMigratedDB(t)
	repo := store.NewConfigRepo(db, nil)

	in := &config.GatewaySettings{Trace: config.TraceSettings{CapturePayloadEnabled: true, MaxBodyKB: 32}}
	if err := repo.UpdateSettings(context.Background(), in); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	snap, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Settings == nil {
		t.Fatal("Snapshot.Settings is nil")
	}
	if !snap.Settings.Trace.CapturePayloadEnabled || snap.Settings.Trace.MaxBodyKB != 32 {
		t.Errorf("Snapshot.Settings = %+v", snap.Settings.Trace)
	}
}

func snapshotVersion(t *testing.T, db *store.DB) int64 {
	t.Helper()
	var v int64
	if err := db.Raw(`SELECT version FROM config_generation`).Scan(&v).Error; err != nil {
		t.Fatalf("read version: %v", err)
	}
	return v
}

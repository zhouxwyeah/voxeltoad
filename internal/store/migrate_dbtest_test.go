//go:build dbtest

package store_test

import (
	"testing"

	"voxeltoad/internal/store"
)

// TestMigrateCreatesAllTables asserts store.Migrate brings up every table the
// management-plane schema requires (ADR-0014).
func TestMigrateCreatesAllTables(t *testing.T) {
	db := mustMigratedDB(t)

	want := []string{
		"providers", "models", "routes", "plugins",
		"tenants", "groups", "api_keys", "quotas",
		"usage_records", "audit_logs", "config_generation",
		"config_snapshots", "data_plane_nodes",
		"trace_payloads", "gateway_settings",
	}
	for _, table := range want {
		var exists bool
		err := db.Raw(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = ?)`, table,
		).Scan(&exists).Error
		if err != nil {
			t.Fatalf("check table %q: %v", table, err)
		}
		if !exists {
			t.Errorf("table %q does not exist after Migrate", table)
		}
	}
}

// TestMigrateTracePayloadsPartitioned asserts trace_payloads is RANGE-partitioned
// by created_at with a default partition (ADR-0039), mirroring request_logs.
func TestMigrateTracePayloadsPartitioned(t *testing.T) {
	db := mustMigratedDB(t)

	// The default partition must exist and be flagged as a partition by pg.
	var isPartitioned bool
	if err := db.Raw(`SELECT COALESCE(relispartition, false) FROM pg_class WHERE relname = 'trace_payloads_default'`).
		Scan(&isPartitioned).Error; err != nil {
		t.Fatalf("query default partition: %v", err)
	}
	if !isPartitioned {
		t.Error("trace_payloads_default partition does not exist or is not a partition")
	}

	// Inserting a row must land in the default partition (no monthly partition yet).
	if err := db.Exec(`INSERT INTO trace_payloads (request_id) VALUES ('part-test')`).Error; err != nil {
		t.Fatalf("insert into trace_payloads: %v", err)
	}
	var n int64
	if err := db.Raw(`SELECT count(*) FROM trace_payloads`).Scan(&n).Error; err != nil {
		t.Fatalf("count trace_payloads: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row in trace_payloads, got %d", n)
	}
}

// TestMigrateKeyConstraints asserts the load-bearing constraints from ADR-0014:
// unique provider name / model alias, and quotas keyed by scope.
func TestMigrateKeyConstraints(t *testing.T) {
	db := mustMigratedDB(t)

	// providers.name is UNIQUE: a duplicate insert must fail.
	if err := db.Exec(
		`INSERT INTO providers (name, type, adapter, enabled, spec)
		 VALUES ('p1', 'openai', 'openai', true, '{}')`).Error; err != nil {
		t.Fatalf("insert provider p1: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO providers (name, type, adapter, enabled, spec)
		 VALUES ('p1', 'openai', 'openai', true, '{}')`).Error; err == nil {
		t.Error("duplicate provider name was allowed; expected UNIQUE violation")
	}

	// models.alias is UNIQUE.
	if err := db.Exec(
		`INSERT INTO models (alias, enabled, spec) VALUES ('m1', true, '{}')`).Error; err != nil {
		t.Fatalf("insert model m1: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO models (alias, enabled, spec) VALUES ('m1', true, '{}')`).Error; err == nil {
		t.Error("duplicate model alias was allowed; expected UNIQUE violation")
	}

	// quotas is keyed by scope: a duplicate scope must fail (PRIMARY KEY).
	if err := db.Exec(
		`INSERT INTO quotas (scope, balance, currency) VALUES ('tenant:a', 1000, 'usd')`).Error; err != nil {
		t.Fatalf("insert quota: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO quotas (scope, balance, currency) VALUES ('tenant:a', 5, 'usd')`).Error; err == nil {
		t.Error("duplicate quota scope was allowed; expected PRIMARY KEY violation")
	}
}

// TestMigrateConfigGenerationSeed asserts the snapshot-version counter starts at
// a single seed row = 0 (ADR-0015).
func TestMigrateConfigGenerationSeed(t *testing.T) {
	db := mustMigratedDB(t)

	var count int64
	if err := db.Raw(`SELECT count(*) FROM config_generation`).Scan(&count).Error; err != nil {
		t.Fatalf("count config_generation: %v", err)
	}
	if count != 1 {
		t.Fatalf("config_generation row count = %d, want exactly 1", count)
	}

	var version int64
	if err := db.Raw(`SELECT version FROM config_generation`).Scan(&version).Error; err != nil {
		t.Fatalf("read config_generation.version: %v", err)
	}
	if version != 0 {
		t.Errorf("seed config_generation.version = %d, want 0", version)
	}
}

// TestMigrateUsageRecordsPartitioned asserts usage_records is a partitioned
// table (ADR-0014: month range-partitioned from day one).
func TestMigrateUsageRecordsPartitioned(t *testing.T) {
	db := mustMigratedDB(t)

	var partitioned bool
	// relkind 'p' marks a partitioned table in pg_class.
	err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM pg_class
		 WHERE relname = 'usage_records' AND relkind = 'p')`).Scan(&partitioned).Error
	if err != nil {
		t.Fatalf("check usage_records partitioning: %v", err)
	}
	if !partitioned {
		t.Error("usage_records is not a partitioned table; ADR-0014 requires month range-partitioning")
	}
}

// TestMigrateIsIdempotent asserts running Migrate twice is a no-op the second
// time (goose tracks applied versions).
func TestMigrateIsIdempotent(t *testing.T) {
	db := mustMigratedDB(t)

	// Second run must not error.
	if err := store.Migrate(db); err != nil {
		t.Fatalf("second Migrate returned error: %v", err)
	}
}

// TestMigrateAuditTenantColumn asserts migration 00003 adds the affected-tenant
// column to audit_logs plus the indexes that back the audit read endpoint
// (ADR-0019): (created_at) for the global feed and (tenant, created_at) for the
// tenant-scoped feed.
func TestMigrateAuditTenantColumn(t *testing.T) {
	db := mustMigratedDB(t)

	var hasCol bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_name = 'audit_logs' AND column_name = 'tenant')`).Scan(&hasCol).Error; err != nil {
		t.Fatalf("check audit_logs.tenant: %v", err)
	}
	if !hasCol {
		t.Error("audit_logs.tenant column missing after migrate")
	}

	for _, idx := range []string{"idx_audit_logs_created_at", "idx_audit_logs_tenant_created"} {
		var hasIdx bool
		if err := db.Raw(
			`SELECT EXISTS (SELECT 1 FROM pg_indexes
			 WHERE tablename = 'audit_logs' AND indexname = ?)`, idx).Scan(&hasIdx).Error; err != nil {
			t.Fatalf("check index %q: %v", idx, err)
		}
		if !hasIdx {
			t.Errorf("index %q missing after migrate", idx)
		}
	}
}

// TestMigrateUpstreamRequestID asserts migration 00024 adds the
// upstream_request_id column to request_logs plus the index that backs
// reverse lookups ("which gateway request does this upstream req_xxx belong
// to?"). The column captures the provider-assigned request ID (OpenAI
// x-request-id header, Anthropic request-id header/body, etc.) for
// support/reconciliation.
func TestMigrateUpstreamRequestID(t *testing.T) {
	db := mustMigratedDB(t)

	var hasCol bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_name = 'request_logs' AND column_name = 'upstream_request_id')`).Scan(&hasCol).Error; err != nil {
		t.Fatalf("check request_logs.upstream_request_id: %v", err)
	}
	if !hasCol {
		t.Fatal("request_logs.upstream_request_id column missing after migrate")
	}

	// The column must default to '' so existing rows and un-filled inserts
	// remain valid (NOT NULL DEFAULT '').
	var def string
	if err := db.Raw(
		`SELECT column_default FROM information_schema.columns
		 WHERE table_name = 'request_logs' AND column_name = 'upstream_request_id'`).Scan(&def).Error; err != nil {
		t.Fatalf("read column_default: %v", err)
	}
	if def != "''::text" {
		t.Errorf("upstream_request_id default = %q, want ''::text", def)
	}

	var hasIdx bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM pg_indexes
		 WHERE tablename = 'request_logs' AND indexname = 'idx_request_logs_upstream_request_id')`).Scan(&hasIdx).Error; err != nil {
		t.Fatalf("check index: %v", err)
	}
	if !hasIdx {
		t.Error("idx_request_logs_upstream_request_id missing after migrate")
	}
}

// mustMigratedDB opens a fresh connection to the shared embedded PostgreSQL and
// applies all migrations. The harness migrates once in TestMain; calling again
// is safe (idempotent) and gives each test a guaranteed-migrated handle.
func mustMigratedDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(testDSN)
	if err != nil {
		t.Fatalf("open embedded postgres: %v", err)
	}
	t.Cleanup(func() {
		// Reset the shared database so the next test starts from a clean
		// migrated state. We truncate every user table except the goose
		// migration tracking table, then reset the snapshot version counter.
		if err := db.Exec(`
			TRUNCATE TABLE
				usage_records, request_logs, trace_payloads,
				providers, models, routes, plugins,
				tenants, groups, api_keys, quotas,
				audit_logs, operators, sessions,
				config_snapshots, data_plane_nodes, gateway_settings
			RESTART IDENTITY CASCADE
		`).Error; err != nil {
			t.Logf("cleanup truncate: %v", err)
		}
		if err := db.Exec(`UPDATE config_generation SET version = 0`).Error; err != nil {
			t.Logf("cleanup reset config_generation: %v", err)
		}
		_ = db.Close()
	})
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

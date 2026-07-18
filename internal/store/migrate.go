// Package store provides PostgreSQL persistence for the admin plane via gorm,
// and the migration entry point shared by tests and production (ADR-0015).
package store

import (
	"context"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

// migrationsFS embeds the ordered SQL migrations applied by Migrate. The same
// embedded set is used by the dbtest harness and by the admin process at
// startup — one artifact, zero test/prod divergence (ADR-0015).
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations to db. It is the single canonical
// migration path (ADR-0015): the dbtest TestMain and the admin startup both call
// it. Concurrent callers are serialized by a PostgreSQL session-scoped advisory
// lock so multiple admin instances starting together do not race. Running with
// no pending migrations is a no-op (idempotent).
func Migrate(db *DB) error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return err
	}

	sessionLocker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}

	// The embedded FS keeps the "migrations/" prefix; goose expects the
	// migration files at the root of the FS it is handed, so sub into it.
	migrationsRoot, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	provider, err := goose.NewProvider(
		database.DialectPostgres,
		sqlDB,
		migrationsRoot,
		goose.WithSessionLocker(sessionLocker),
	)
	if err != nil {
		return err
	}

	_, err = provider.Up(context.Background())
	return err
}

package store

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DB wraps the gorm connection.
type DB struct {
	*gorm.DB
}

// Open connects to PostgreSQL using the given DSN.
func Open(dsn string) (*DB, error) {
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return &DB{gdb}, nil
}

// Close closes the underlying connection pool.
func (db *DB) Close() error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

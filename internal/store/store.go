// Package store owns the SQLite database: opening it, running migrations, and
// (later) reading and writing domain records.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store wraps the database handle for a single Curator site.
type Store struct {
	DB *sql.DB
}

// Open opens (creating if needed) the SQLite database at path with foreign keys
// and WAL enabled. SQLite is single-writer, so the pool is capped to one
// connection to avoid "database is locked" errors under concurrency.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.DB.Close()
}

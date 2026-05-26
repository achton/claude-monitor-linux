package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/achton/claude-monitor-linux/internal/xdg"
)

// Store wraps the SQLite database connection.
type Store struct {
	DB *sql.DB
}

// Open opens (or creates) the database at xdg.DBPath(), enforces secure perms,
// applies the schema, and returns a Store ready for use.
//
// File modes: parent dir 0700, db file 0600. Open() fails if existing perms
// are wider than that.
func Open() (*Store, error) {
	if err := xdg.EnsureSecureDir(xdg.DataDir()); err != nil {
		return nil, err
	}
	if err := xdg.EnsureSecureFile(xdg.DBPath()); err != nil {
		return nil, err
	}
	return openAt(xdg.DBPath())
}

// OpenInMemory is for tests.
func OpenInMemory() (*Store, error) {
	return openAt(":memory:")
}

func openAt(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create db file: %w", err)
		}
		_ = f.Close()

		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read user_version: %w", err)
	}
	if ver < schemaVersion {
		if _, err := db.Exec(wipeOldSchema); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("wipe old schema: %w", err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if ver < schemaVersion {
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set user_version: %w", err)
		}
	}

	if path != ":memory:" {
		for _, sfx := range []string{"", "-wal", "-shm"} {
			_ = os.Chmod(path+sfx, 0o600)
		}
	}
	return &Store{DB: db}, nil
}

// Close closes the DB.
func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

// WithTx runs fn inside a write transaction (BEGIN IMMEDIATE due to the DSN).
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

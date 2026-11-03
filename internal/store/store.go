package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/achton/claude-monitor-linux/internal/api"
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

		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := applySchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	if path != ":memory:" {
		for _, sfx := range []string{"", "-wal", "-shm"} {
			_ = os.Chmod(path+sfx, 0o600)
		}
	}
	return &Store{DB: db}, nil
}

// applySchema brings the database up to schemaVersion. Usage history is the
// point of the app, so v2 and v3 databases are migrated; anything older
// predates a shape we can map and is wiped.
//
// The version is read, migrated and bumped in one write transaction, so a
// process that opens the database concurrently waits and then sees the result.
func applySchema(db *sql.DB) error {
	if _, err := db.Exec(pragmas); err != nil {
		return fmt.Errorf("apply pragmas: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var ver int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	// Create current tables first: the v2 migration copies into them.
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if ver >= schemaVersion {
		return tx.Commit()
	}

	switch ver {
	case 3:
		if err := migrateV3(tx); err != nil {
			return fmt.Errorf("migrate v3 limits: %w", err)
		}
	case 2:
		if err := migrateV2(tx); err != nil {
			return fmt.Errorf("migrate v2 history: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS usage_history`); err != nil {
			return fmt.Errorf("drop usage_history: %w", err)
		}
	default:
		if _, err := tx.Exec(wipeOldSchema); err != nil {
			return fmt.Errorf("wipe old schema: %w", err)
		}
		if _, err := tx.Exec(schema); err != nil {
			return fmt.Errorf("reapply schema: %w", err)
		}
	}

	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return tx.Commit()
}

// migrateV3 rebuilds usage_limit to add scope_surface to its primary key,
// which SQLite cannot alter in place.
func migrateV3(tx *sql.Tx) error {
	const cols = `reading_id, kind, limit_group, scope_model, percent, severity, resets_at, is_active`
	for _, stmt := range []string{
		`ALTER TABLE usage_limit RENAME TO usage_limit_v3`,
		usageLimitTable,
		`INSERT INTO usage_limit (` + cols + `) SELECT ` + cols + ` FROM usage_limit_v3`,
		`DROP TABLE usage_limit_v3`,
		`CREATE INDEX IF NOT EXISTS idx_limit_reading ON usage_limit(reading_id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// migrateV2 copies v2's fixed-column readings into generic limit rows.
// weekly_sonnet_percent is dropped: the API stopped reporting seven_day_sonnet,
// so every stored value is zero and carrying it forward would recreate exactly
// the stale-column problem v3 exists to remove.
func migrateV2(tx *sql.Tx) error {
	type v2row struct {
		ts           string
		session      sql.NullFloat64
		weekly       sql.NullFloat64
		sessionReset sql.NullString
		weeklyReset  sql.NullString
		raw          sql.NullString
		synthetic    int
	}

	// Collect before inserting: an insert while the read cursor is open would
	// deadlock on the single connection.
	rows, err := tx.Query(`
		SELECT timestamp, session_percent, weekly_percent,
		       session_reset, weekly_reset, raw_data, is_synthetic
		FROM usage_history ORDER BY id ASC
	`)
	if err != nil {
		// No v2 table (fresh DB that merely reported an old user_version):
		// nothing to carry forward.
		return nil
	}
	var out []v2row
	for rows.Next() {
		var r v2row
		if err := rows.Scan(&r.ts, &r.session, &r.weekly,
			&r.sessionReset, &r.weeklyReset, &r.raw, &r.synthetic); err != nil {
			rows.Close()
			return err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, r := range out {
		res, err := tx.Exec(`
			INSERT INTO usage_reading (timestamp, raw_data, is_synthetic)
			VALUES (?, ?, ?)
		`, r.ts, r.raw, r.synthetic)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}

		add := func(kind, group string, pct sql.NullFloat64, reset sql.NullString, active bool) error {
			if !pct.Valid {
				return nil
			}
			a := 0
			if active {
				a = 1
			}
			var resets any
			if reset.Valid && reset.String != "" {
				resets = reset.String
			}
			_, err := tx.Exec(`
				INSERT INTO usage_limit (
					reading_id, kind, limit_group, scope_model,
					percent, severity, resets_at, is_active
				) VALUES (?, ?, ?, '', ?, 'normal', ?, ?)
			`, id, kind, group, pct.Float64, resets, a)
			return err
		}

		// v2 had no is_active flag and nothing displays one, so leave it unset.
		if err := add(api.KindSession, api.GroupSession, r.session, r.sessionReset, false); err != nil {
			return err
		}
		if err := add(api.KindWeeklyAll, api.GroupWeekly, r.weekly, r.weeklyReset, false); err != nil {
			return err
		}
	}
	return nil
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

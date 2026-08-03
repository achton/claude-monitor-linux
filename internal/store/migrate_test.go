package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// v2Schema is the shape shipped in v0.2.x, recreated here so the migration is
// tested against the real thing rather than a guess.
const v2Schema = `
CREATE TABLE usage_history (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp             TEXT NOT NULL,
    session_percent       REAL,
    weekly_percent        REAL,
    weekly_sonnet_percent REAL,
    session_reset         TEXT,
    weekly_reset          TEXT,
    raw_data              TEXT,
    is_synthetic          INTEGER DEFAULT 0
);
CREATE TABLE notification_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    dimension       TEXT NOT NULL,
    threshold       INTEGER NOT NULL,
    reset_timestamp TEXT NOT NULL,
    fired_at        TEXT NOT NULL,
    UNIQUE (dimension, threshold, reset_timestamp)
);
`

// seedV2DB writes a v2 database with a handful of readings and returns its path.
func seedV2DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(v2Schema); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	sessionReset := base.Add(3 * time.Hour).Format(time.RFC3339Nano)
	weeklyReset := base.Add(72 * time.Hour).Format(time.RFC3339Nano)

	rows := []struct {
		off             time.Duration
		session, weekly float64
		synthetic       int
	}{
		{0, 10, 30, 0},
		{10 * time.Minute, 55, 88, 0},
		{20 * time.Minute, 0, 0, 1}, // synthetic reset anchor
		{30 * time.Minute, 5, 2, 0},
	}
	for _, r := range rows {
		if _, err := db.Exec(`
			INSERT INTO usage_history (timestamp, session_percent, weekly_percent,
				weekly_sonnet_percent, session_reset, weekly_reset, raw_data, is_synthetic)
			VALUES (?, ?, ?, 0, ?, ?, '{"legacy":true}', ?)
		`, base.Add(r.off).Format(time.RFC3339Nano), r.session, r.weekly,
			sessionReset, weeklyReset, r.synthetic); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateV2PreservesHistory(t *testing.T) {
	path := seedV2DB(t)

	s, err := openAt(path)
	if err != nil {
		t.Fatalf("open after v2: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	rows, err := s.ReadingRange(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("readings after migration: got %d, want 4", len(rows))
	}

	// Each migrated reading should carry session + weekly limits, and nothing
	// resembling the retired sonnet column.
	for i, r := range rows {
		if _, ok := r.Session(); !ok {
			t.Errorf("reading %d: session limit missing", i)
		}
		if _, ok := r.Weekly(); !ok {
			t.Errorf("reading %d: weekly limit missing", i)
		}
		if len(r.Limits) != 2 {
			t.Errorf("reading %d: got %d limits, want 2", i, len(r.Limits))
		}
	}

	// The synthetic marker survives, so reset cliffs still render.
	syn := 0
	for _, r := range rows {
		if r.IsSynthetic {
			syn++
		}
	}
	if syn != 1 {
		t.Errorf("synthetic rows: got %d, want 1", syn)
	}

	// The peak is still discoverable — the whole point of keeping the history.
	peaks := Peaks(rows, time.Hour)
	if len(peaks) == 0 || peaks[0].Limit.Percent != 88 {
		t.Errorf("peak after migration: %+v", peaks)
	}

	// Reset timestamps carried over.
	if l, _ := rows[0].Weekly(); l.ResetsAt.IsZero() {
		t.Error("weekly reset timestamp lost in migration")
	}

	// The highest utilization is what PrimaryPercent reports: weekly 30 > session 10.
	if got := rows[0].PrimaryPercent(); got != 30 {
		t.Errorf("primary: got %v, want 30", got)
	}

	// The old table is gone and the version is current.
	var n int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='usage_history'`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("usage_history should be dropped after migration")
	}
	var ver int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != schemaVersion {
		t.Errorf("user_version: got %d, want %d", ver, schemaVersion)
	}
}

// Reopening a migrated DB must be a no-op, not a second migration or a wipe.
func TestMigrateV2Idempotent(t *testing.T) {
	path := seedV2DB(t)

	s, err := openAt(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := openAt(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	rows, err := s2.ReadingRange(context.Background(),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Errorf("readings after reopen: got %d, want 4", len(rows))
	}
}

// A pre-v2 database has no mappable shape and is wiped, as before.
func TestPreV2IsWiped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE accounts (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO accounts (name) VALUES ('old')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, 1)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := openAt(path)
	if err != nil {
		t.Fatalf("open after v1: %v", err)
	}
	defer s.Close()

	var n int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='accounts'`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("pre-v2 tables should be wiped")
	}
}

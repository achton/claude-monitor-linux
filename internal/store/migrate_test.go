package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
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

const v3UsageLimit = `
CREATE TABLE usage_reading (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp    TEXT NOT NULL,
    raw_data     TEXT,
    is_synthetic INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE usage_limit (
    reading_id  INTEGER NOT NULL REFERENCES usage_reading(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    limit_group TEXT NOT NULL,
    scope_model TEXT NOT NULL DEFAULT '',
    percent     REAL NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'normal',
    resets_at   TEXT,
    is_active   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (reading_id, kind, scope_model)
);
CREATE INDEX idx_limit_reading ON usage_limit(reading_id);
INSERT INTO usage_reading (timestamp) VALUES ('2026-10-01T12:00:00Z');
INSERT INTO usage_limit VALUES (1, 'session', 'session', '', 20, 'normal', NULL, 0);
INSERT INTO usage_limit VALUES (1, 'weekly_scoped', 'weekly', 'Fable', 12, 'normal', NULL, 0);
PRAGMA user_version = 3;
`

func TestMigrateV3AddsSurface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(v3UsageLimit); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := openAt(path)
	if err != nil {
		t.Fatalf("open after v3: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	r, err := s.LatestReading(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Limits) != 2 {
		t.Fatalf("limits after migration: got %d, want 2", len(r.Limits))
	}
	if _, ok := r.Find("weekly_scoped:Fable"); !ok {
		t.Error("scoped limit lost in migration")
	}

	// Two limits that differ only by surface now fit in one reading.
	limits := []api.Limit{
		{Kind: api.KindWeeklyScoped, Group: api.GroupWeekly, Percent: 1, ScopeSurface: "Cowork"},
		{Kind: api.KindWeeklyScoped, Group: api.GroupWeekly, Percent: 2, ScopeSurface: "Desktop"},
	}
	if _, err := s.InsertReading(ctx, nil, time.Now(), limits, "", false); err != nil {
		t.Fatalf("insert surface-scoped limits: %v", err)
	}
	r, err = s.LatestReading(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Find("weekly_scoped@Desktop"); !ok {
		t.Errorf("surface limit not round-tripped: %+v", r.Limits)
	}
}

// Processes that open a v3 database at the same time must migrate it once:
// a second migration would rebuild usage_limit without scope_surface.
func TestConcurrentOpensMigrateOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Databases the app created are already in WAL mode.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;` + v3UsageLimit); err != nil {
		t.Fatal(err)
	}
	db.Close()

	const n = 6
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := range n {
		go func() {
			<-start
			s, err := openAt(path)
			if err != nil {
				errs <- fmt.Errorf("open %d: %w", i, err)
				return
			}
			defer s.Close()
			_, err = s.InsertReading(context.Background(), nil, time.Now(), []api.Limit{{
				Kind: api.KindWeeklyScoped, Group: api.GroupWeekly, Percent: 1,
				ScopeSurface: fmt.Sprintf("surface-%d", i),
			}}, "", false)
			errs <- err
		}()
	}
	close(start)
	for range n {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}

	s, err := openAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var surfaces int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM usage_limit WHERE scope_surface != ''`).Scan(&surfaces); err != nil {
		t.Fatal(err)
	}
	if surfaces != n {
		t.Errorf("surface-scoped rows: got %d, want %d", surfaces, n)
	}
}

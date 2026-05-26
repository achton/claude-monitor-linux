package store

import (
	"context"
	"database/sql"
	"time"
)

// UsageRecord is one row in the usage_history table.
type UsageRecord struct {
	ID                  int64
	Timestamp           time.Time
	SessionPercent      sql.NullFloat64
	WeeklyPercent       sql.NullFloat64
	WeeklySonnetPercent sql.NullFloat64
	SessionReset        sql.NullString
	WeeklyReset         sql.NullString
	RawData             sql.NullString
	IsSynthetic         bool
}

// PrimaryPercent returns max(session, weekly).
func (r UsageRecord) PrimaryPercent() float64 {
	var s, w float64
	if r.SessionPercent.Valid {
		s = r.SessionPercent.Float64
	}
	if r.WeeklyPercent.Valid {
		w = r.WeeklyPercent.Float64
	}
	if s > w {
		return s
	}
	return w
}

// InsertUsageReading writes a real (non-synthetic) reading.
func (s *Store) InsertUsageReading(ctx context.Context, tx *sql.Tx,
	sessionPct, weeklyPct, weeklySonnetPct float64,
	sessionReset, weeklyReset, rawJSON string,
) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec := s.DB.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	_, err := exec(ctx, `
		INSERT INTO usage_history (
			timestamp, session_percent, weekly_percent, weekly_sonnet_percent,
			session_reset, weekly_reset, raw_data, is_synthetic
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0)
	`, now, sessionPct, weeklyPct, weeklySonnetPct,
		nullableString(sessionReset), nullableString(weeklyReset),
		nullableString(rawJSON))
	return err
}

// InsertSyntheticUsage writes a synthetic row used to anchor reset transitions.
func (s *Store) InsertSyntheticUsage(ctx context.Context, tx *sql.Tx,
	timestamp time.Time, sessionPct, weeklyPct, weeklySonnetPct float64,
) error {
	exec := s.DB.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	_, err := exec(ctx, `
		INSERT INTO usage_history (
			timestamp, session_percent, weekly_percent, weekly_sonnet_percent,
			session_reset, weekly_reset, raw_data, is_synthetic
		) VALUES (?, ?, ?, ?, NULL, NULL, NULL, 1)
	`, timestamp.UTC().Format(time.RFC3339Nano),
		sessionPct, weeklyPct, weeklySonnetPct)
	return err
}

// LatestUsage returns the most recent usage row, or sql.ErrNoRows.
func (s *Store) LatestUsage(ctx context.Context) (UsageRecord, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, timestamp, session_percent, weekly_percent, weekly_sonnet_percent,
		       session_reset, weekly_reset, raw_data, is_synthetic
		FROM usage_history
		ORDER BY timestamp DESC LIMIT 1
	`)
	return scanUsageRow(row)
}

// LatestUsageInTx is LatestUsage but reads from the supplied tx.
func (s *Store) LatestUsageInTx(ctx context.Context, tx *sql.Tx) (UsageRecord, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, timestamp, session_percent, weekly_percent, weekly_sonnet_percent,
		       session_reset, weekly_reset, raw_data, is_synthetic
		FROM usage_history
		ORDER BY timestamp DESC LIMIT 1
	`)
	return scanUsageRow(row)
}

// HasRecentSynthetic returns true if a synthetic row was inserted within `within`.
func (s *Store) HasRecentSynthetic(ctx context.Context, tx *sql.Tx, within time.Duration) (bool, error) {
	cutoff := time.Now().UTC().Add(-within).Format(time.RFC3339Nano)
	q := s.DB.QueryRowContext
	if tx != nil {
		q = tx.QueryRowContext
	}
	var n int
	if err := q(ctx, `
		SELECT COUNT(*) FROM usage_history
		WHERE is_synthetic = 1 AND timestamp >= ?
	`, cutoff).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// UsageRange returns usage rows from `since` to now, oldest first.
func (s *Store) UsageRange(ctx context.Context, since time.Time) ([]UsageRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, timestamp, session_percent, weekly_percent, weekly_sonnet_percent,
		       session_reset, weekly_reset, raw_data, is_synthetic
		FROM usage_history
		WHERE timestamp >= ?
		ORDER BY timestamp ASC
	`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRecord
	for rows.Next() {
		r, err := scanUsageRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUsageRow(r rowScanner) (UsageRecord, error) {
	var u UsageRecord
	var ts string
	var isSyn int
	if err := r.Scan(
		&u.ID, &ts, &u.SessionPercent, &u.WeeklyPercent, &u.WeeklySonnetPercent,
		&u.SessionReset, &u.WeeklyReset, &u.RawData, &isSyn,
	); err != nil {
		return u, err
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		u.Timestamp = t
	} else if t, err := time.Parse(time.RFC3339, ts); err == nil {
		u.Timestamp = t
	}
	u.IsSynthetic = isSyn == 1
	return u, nil
}

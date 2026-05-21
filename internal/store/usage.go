package store

import (
	"context"
	"database/sql"
	"time"
)

// UsageRecord is one row in the usage_history table.
type UsageRecord struct {
	ID                  int64
	AccountID           string
	Timestamp           time.Time
	PrimaryPercent      sql.NullFloat64
	SessionPercent      sql.NullFloat64
	WeeklyAllPercent    sql.NullFloat64
	WeeklySonnetPercent sql.NullFloat64
	SessionReset        sql.NullString
	WeeklyReset         sql.NullString
	RawData             sql.NullString
	Source              sql.NullString
	IsSynthetic         bool
}

// InsertUsageReading writes a real (non-synthetic) reading. Caller may pass a tx;
// nil tx means: use the store's pool directly.
func (s *Store) InsertUsageReading(ctx context.Context, tx *sql.Tx, accountID string,
	primary, sessionPct, weeklyPct, weeklySonnetPct float64,
	sessionReset, weeklyReset, rawJSON, source string,
) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec := s.DB.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	_, err := exec(ctx, `
		INSERT INTO usage_history (
			account_id, timestamp, primary_percent, session_percent,
			weekly_all_percent, weekly_sonnet_percent,
			session_reset, weekly_reset, raw_data, source, is_synthetic
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
	`, accountID, now, primary, sessionPct, weeklyPct, weeklySonnetPct,
		nullableString(sessionReset), nullableString(weeklyReset),
		nullableString(rawJSON), nullableString(source))
	return err
}

// InsertSyntheticUsage writes a synthetic row used to anchor reset transitions.
func (s *Store) InsertSyntheticUsage(ctx context.Context, tx *sql.Tx, accountID string,
	timestamp time.Time, primary, sessionPct, weeklyPct, weeklySonnetPct float64,
) error {
	exec := s.DB.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	_, err := exec(ctx, `
		INSERT INTO usage_history (
			account_id, timestamp, primary_percent, session_percent,
			weekly_all_percent, weekly_sonnet_percent,
			session_reset, weekly_reset, raw_data, source, is_synthetic
		) VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, NULL, NULL, 1)
	`, accountID, timestamp.UTC().Format(time.RFC3339Nano),
		primary, sessionPct, weeklyPct, weeklySonnetPct)
	return err
}

// LatestUsage returns the most recent usage row for an account, or sql.ErrNoRows.
func (s *Store) LatestUsage(ctx context.Context, accountID string) (UsageRecord, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, account_id, timestamp, primary_percent, session_percent,
		       weekly_all_percent, weekly_sonnet_percent, session_reset, weekly_reset,
		       raw_data, source, is_synthetic
		FROM usage_history
		WHERE account_id = ?
		ORDER BY timestamp DESC LIMIT 1
	`, accountID)
	return scanUsageRow(row)
}

// LatestUsageInTx is LatestUsage but reads from the supplied tx (for reset-detection critical section).
func (s *Store) LatestUsageInTx(ctx context.Context, tx *sql.Tx, accountID string) (UsageRecord, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, account_id, timestamp, primary_percent, session_percent,
		       weekly_all_percent, weekly_sonnet_percent, session_reset, weekly_reset,
		       raw_data, source, is_synthetic
		FROM usage_history
		WHERE account_id = ?
		ORDER BY timestamp DESC LIMIT 1
	`, accountID)
	return scanUsageRow(row)
}

// HasRecentSynthetic returns true if a synthetic row was inserted for this account
// in the last `within` duration (idempotency belt-and-suspenders).
func (s *Store) HasRecentSynthetic(ctx context.Context, tx *sql.Tx, accountID string, within time.Duration) (bool, error) {
	cutoff := time.Now().UTC().Add(-within).Format(time.RFC3339Nano)
	q := s.DB.QueryRowContext
	if tx != nil {
		q = tx.QueryRowContext
	}
	var n int
	if err := q(ctx, `
		SELECT COUNT(*) FROM usage_history
		WHERE account_id = ? AND is_synthetic = 1 AND timestamp >= ?
	`, accountID, cutoff).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// UsageRange returns usage rows for an account from `since` to now, oldest first.
func (s *Store) UsageRange(ctx context.Context, accountID string, since time.Time) ([]UsageRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, account_id, timestamp, primary_percent, session_percent,
		       weekly_all_percent, weekly_sonnet_percent, session_reset, weekly_reset,
		       raw_data, source, is_synthetic
		FROM usage_history
		WHERE account_id = ? AND timestamp >= ?
		ORDER BY timestamp ASC
	`, accountID, since.UTC().Format(time.RFC3339Nano))
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
		&u.ID, &u.AccountID, &ts, &u.PrimaryPercent, &u.SessionPercent,
		&u.WeeklyAllPercent, &u.WeeklySonnetPercent, &u.SessionReset, &u.WeeklyReset,
		&u.RawData, &u.Source, &isSyn,
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

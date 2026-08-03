package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
)

// Reading is one poll of the usage endpoint together with the limits it
// reported.
type Reading struct {
	ID          int64
	Timestamp   time.Time
	RawData     sql.NullString
	IsSynthetic bool
	Limits      []api.Limit
}

// Find returns the limit with the given key (see api.Limit.Key).
func (r Reading) Find(key string) (api.Limit, bool) {
	for _, l := range r.Limits {
		if l.Key() == key {
			return l, true
		}
	}
	return api.Limit{}, false
}

// Session returns the 5-hour session limit.
func (r Reading) Session() (api.Limit, bool) { return r.Find(api.KindSession) }

// Weekly returns the all-model weekly limit.
func (r Reading) Weekly() (api.Limit, bool) { return r.Find(api.KindWeeklyAll) }

// PrimaryPercent is the highest utilization across all reported limits.
func (r Reading) PrimaryPercent() float64 {
	return api.UsageReading{Limits: r.Limits}.PrimaryPercent()
}

// InsertReading writes one reading plus its limits and returns the reading id.
// synthetic rows anchor reset transitions in the chart and carry no raw body.
func (s *Store) InsertReading(ctx context.Context, tx *sql.Tx,
	timestamp time.Time, limits []api.Limit, rawJSON string, synthetic bool,
) (int64, error) {
	exec := s.DB.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	syn := 0
	if synthetic {
		syn = 1
	}
	res, err := exec(ctx, `
		INSERT INTO usage_reading (timestamp, raw_data, is_synthetic)
		VALUES (?, ?, ?)
	`, timestamp.UTC().Format(time.RFC3339Nano), nullableString(rawJSON), syn)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, l := range limits {
		active := 0
		if l.IsActive {
			active = 1
		}
		var resets any
		if !l.ResetsAt.IsZero() {
			resets = l.ResetsAt.UTC().Format(time.RFC3339Nano)
		}
		if _, err := exec(ctx, `
			INSERT INTO usage_limit (
				reading_id, kind, limit_group, scope_model,
				percent, severity, resets_at, is_active
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, id, l.Kind, l.Group, l.ScopeModel,
			l.Percent, l.Severity, resets, active); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// LatestReading returns the most recent reading, or sql.ErrNoRows.
func (s *Store) LatestReading(ctx context.Context) (Reading, error) {
	return s.latestReading(ctx, nil)
}

// LatestReadingInTx is LatestReading but reads from the supplied tx.
func (s *Store) LatestReadingInTx(ctx context.Context, tx *sql.Tx) (Reading, error) {
	return s.latestReading(ctx, tx)
}

func (s *Store) latestReading(ctx context.Context, tx *sql.Tx) (Reading, error) {
	q := s.DB.QueryRowContext
	if tx != nil {
		q = tx.QueryRowContext
	}
	var r Reading
	var ts string
	var syn int
	if err := q(ctx, `
		SELECT id, timestamp, raw_data, is_synthetic
		FROM usage_reading
		ORDER BY timestamp DESC, id DESC LIMIT 1
	`).Scan(&r.ID, &ts, &r.RawData, &syn); err != nil {
		return Reading{}, err
	}
	r.Timestamp = parseStoredTime(ts)
	r.IsSynthetic = syn == 1

	limits, err := s.limitsFor(ctx, tx, r.ID)
	if err != nil {
		return Reading{}, err
	}
	r.Limits = limits
	return r, nil
}

func (s *Store) limitsFor(ctx context.Context, tx *sql.Tx, readingID int64) ([]api.Limit, error) {
	query := s.DB.QueryContext
	if tx != nil {
		query = tx.QueryContext
	}
	rows, err := query(ctx, `
		SELECT kind, limit_group, scope_model, percent, severity, resets_at, is_active
		FROM usage_limit WHERE reading_id = ?
	`, readingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.Limit
	for rows.Next() {
		l, err := scanLimit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	api.SortLimits(out)
	return out, nil
}

func scanLimit(r rowScanner) (api.Limit, error) {
	var l api.Limit
	var resets sql.NullString
	var active int
	if err := r.Scan(&l.Kind, &l.Group, &l.ScopeModel,
		&l.Percent, &l.Severity, &resets, &active); err != nil {
		return l, err
	}
	if resets.Valid {
		l.ResetsAt = parseStoredTime(resets.String)
	}
	l.IsActive = active == 1
	return l, nil
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
		SELECT COUNT(*) FROM usage_reading
		WHERE is_synthetic = 1 AND timestamp >= ?
	`, cutoff).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReadingRange returns readings from `since` to now, oldest first, each with
// its limits attached. One query per table rather than per reading.
func (s *Store) ReadingRange(ctx context.Context, since time.Time) ([]Reading, error) {
	cutoff := since.UTC().Format(time.RFC3339Nano)
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, timestamp, raw_data, is_synthetic
		FROM usage_reading
		WHERE timestamp >= ?
		ORDER BY timestamp ASC, id ASC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Reading
	byID := map[int64]int{}
	for rows.Next() {
		var r Reading
		var ts string
		var syn int
		if err := rows.Scan(&r.ID, &ts, &r.RawData, &syn); err != nil {
			return nil, err
		}
		r.Timestamp = parseStoredTime(ts)
		r.IsSynthetic = syn == 1
		byID[r.ID] = len(out)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	lrows, err := s.DB.QueryContext(ctx, `
		SELECT l.reading_id, l.kind, l.limit_group, l.scope_model,
		       l.percent, l.severity, l.resets_at, l.is_active
		FROM usage_limit l
		JOIN usage_reading r ON r.id = l.reading_id
		WHERE r.timestamp >= ?
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var readingID int64
		var l api.Limit
		var resets sql.NullString
		var active int
		if err := lrows.Scan(&readingID, &l.Kind, &l.Group, &l.ScopeModel,
			&l.Percent, &l.Severity, &resets, &active); err != nil {
			return nil, err
		}
		if resets.Valid {
			l.ResetsAt = parseStoredTime(resets.String)
		}
		l.IsActive = active == 1
		if i, ok := byID[readingID]; ok {
			out[i].Limits = append(out[i].Limits, l)
		}
	}
	if err := lrows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		api.SortLimits(out[i].Limits)
	}
	return out, nil
}

// LimitKeysSince returns the distinct limit keys seen since `since`, in display
// order. The chart uses it to decide which series exist without hard-coding a
// list of limit kinds.
func (s *Store) LimitKeysSince(ctx context.Context, since time.Time) ([]api.Limit, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT l.kind, l.limit_group, l.scope_model
		FROM usage_limit l
		JOIN usage_reading r ON r.id = l.reading_id
		WHERE r.timestamp >= ? AND r.is_synthetic = 0
	`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.Limit
	for rows.Next() {
		var l api.Limit
		if err := rows.Scan(&l.Kind, &l.Group, &l.ScopeModel); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	api.SortLimits(out)
	return out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func parseStoredTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

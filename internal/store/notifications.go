package store

import (
	"context"
	"time"
)

// MarkNotificationFired returns (fired bool, err). If false, the (account,
// dimension, threshold, reset) tuple already exists — caller should skip.
func (s *Store) MarkNotificationFired(ctx context.Context,
	accountID, dimension string, threshold int, resetTimestamp time.Time,
) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	resetISO := resetTimestamp.UTC().Format(time.RFC3339Nano)
	res, err := s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_log
		    (account_id, dimension, threshold, reset_timestamp, fired_at)
		VALUES (?, ?, ?, ?, ?)
	`, accountID, dimension, threshold, resetISO, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GCNotificationLog deletes rows whose reset_timestamp has elapsed.
// Anchored on API-provided reset_timestamp, NOT local clock (see §5.6 / §14 row 22).
func (s *Store) GCNotificationLog(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `DELETE FROM notification_log WHERE reset_timestamp < ?`, now)
	return err
}

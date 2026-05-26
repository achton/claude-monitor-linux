package store

import (
	"context"
	"time"
)

// MarkNotificationFired returns (fired bool, err). If false, the
// (dimension, threshold, reset) tuple already exists — caller should skip.
func (s *Store) MarkNotificationFired(ctx context.Context,
	dimension string, threshold int, resetTimestamp time.Time,
) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	resetISO := resetTimestamp.UTC().Format(time.RFC3339Nano)
	res, err := s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_log
		    (dimension, threshold, reset_timestamp, fired_at)
		VALUES (?, ?, ?, ?)
	`, dimension, threshold, resetISO, now)
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
func (s *Store) GCNotificationLog(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `DELETE FROM notification_log WHERE reset_timestamp < ?`, now)
	return err
}

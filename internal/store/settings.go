package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting returns the value of a key, or "" + sql.ErrNoRows if absent.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", err
	}
	if !v.Valid {
		return "", nil
	}
	return v.String, nil
}

// GetSettingDefault returns the value or the given default if missing.
func (s *Store) GetSettingDefault(ctx context.Context, key, def string) string {
	v, err := s.GetSetting(ctx, key)
	if err != nil {
		return def
	}
	return v
}

// SetSetting upserts a setting. A nil value deletes the key.
func (s *Store) SetSetting(ctx context.Context, key string, value *string) error {
	if value == nil {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, *value)
	return err
}

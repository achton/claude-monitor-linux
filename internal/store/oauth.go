package store

import (
	"context"
	"database/sql"
	"time"
)

// Credential is an OAuth token + per-account state for the polling endpoint.
type Credential struct {
	ID                    int64
	AccountID             sql.NullString
	Label                 string
	Source                string // 'token' | 'env'
	AccessToken           sql.NullString
	RefreshToken          sql.NullString
	ExpiresAt             sql.NullInt64
	UsageEndpointState    string // 'healthy' | 'backoff:<unix>' | 'disabled'
	UsageEndpointAttempts int
	LastPollAt            sql.NullTime
	LastError             sql.NullString
	IsActive              bool
}

// UpsertCredentialForAccount inserts a new credential or updates the access token
// for an existing one belonging to the same account_id.
func (s *Store) UpsertCredentialForAccount(ctx context.Context, tx *sql.Tx,
	accountID, label, source, accessToken string,
) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := s.DB.QueryRowContext
	exec := s.DB.ExecContext
	if tx != nil {
		q = tx.QueryRowContext
		exec = tx.ExecContext
	}
	var existing int64
	err := q(ctx, `SELECT id FROM oauth_credentials WHERE account_id = ? LIMIT 1`, accountID).Scan(&existing)
	switch {
	case err == sql.ErrNoRows:
		_, err = exec(ctx, `
			INSERT INTO oauth_credentials (
				account_id, label, source, access_token, is_active,
				usage_endpoint_state, usage_endpoint_attempts,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 1, 'healthy', 0, ?, ?)
		`, accountID, label, source, accessToken, now, now)
		return err
	case err != nil:
		return err
	default:
		_, err = exec(ctx, `
			UPDATE oauth_credentials
			SET access_token = ?, source = ?, is_active = 1, updated_at = ?
			WHERE id = ?
		`, accessToken, source, now, existing)
		return err
	}
}

// ListActiveCredentials returns all active credentials with non-null access tokens.
func (s *Store) ListActiveCredentials(ctx context.Context) ([]Credential, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, account_id, label, source, access_token, refresh_token, expires_at,
		       usage_endpoint_state, usage_endpoint_attempts,
		       last_poll_at, last_error, is_active
		FROM oauth_credentials
		WHERE is_active = 1 AND access_token IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		var lpa sql.NullString
		var active int
		if err := rows.Scan(
			&c.ID, &c.AccountID, &c.Label, &c.Source,
			&c.AccessToken, &c.RefreshToken, &c.ExpiresAt,
			&c.UsageEndpointState, &c.UsageEndpointAttempts,
			&lpa, &c.LastError, &active,
		); err != nil {
			return nil, err
		}
		if lpa.Valid {
			if t, err := time.Parse(time.RFC3339Nano, lpa.String); err == nil {
				c.LastPollAt = sql.NullTime{Time: t, Valid: true}
			}
		}
		c.IsActive = active == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCredentialPollState writes last_poll_at, last_error, and endpoint state.
func (s *Store) UpdateCredentialPollState(ctx context.Context, credID int64,
	lastError string, state string, attempts int,
) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
		UPDATE oauth_credentials
		SET last_poll_at = ?, last_error = ?, usage_endpoint_state = ?,
		    usage_endpoint_attempts = ?, updated_at = ?
		WHERE id = ?
	`, now, nullableString(lastError), state, attempts, now, credID)
	return err
}

// DeactivateCredential marks a credential inactive (used on token revocation).
func (s *Store) DeactivateCredential(ctx context.Context, credID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
		UPDATE oauth_credentials SET is_active = 0, updated_at = ? WHERE id = ?
	`, now, credID)
	return err
}

// ResetCredentialEndpointState sets state to 'healthy' (used by `probe`).
func (s *Store) ResetCredentialEndpointState(ctx context.Context, credID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
		UPDATE oauth_credentials
		SET usage_endpoint_state = 'healthy', usage_endpoint_attempts = 0, updated_at = ?
		WHERE id = ?
	`, now, credID)
	return err
}

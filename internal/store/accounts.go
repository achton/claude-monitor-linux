package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Account is one Claude Pro/Max subscription tracked by the app.
type Account struct {
	ID          string
	AccountName sql.NullString
	Email       sql.NullString
	Plan        sql.NullString
	LastUpdated sql.NullTime
	SortOrder   int
}

// DisplayName returns the best label for the account.
func (a Account) DisplayName() string {
	if a.AccountName.Valid && a.AccountName.String != "" {
		return a.AccountName.String
	}
	if a.Email.Valid && a.Email.String != "" {
		return a.Email.String
	}
	return a.ID
}

// UpsertAccount inserts or updates an account. Never overwrites account_name
// if the user has already renamed it (the COALESCE clause preserves it).
func (s *Store) UpsertAccount(ctx context.Context, tx *sql.Tx, id, label, email, plan string) error {
	if id == "" {
		return errors.New("UpsertAccount: empty id")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec := s.DB.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	_, err := exec(ctx, `
		INSERT INTO accounts (id, account_name, email, plan, last_updated, sort_order)
		VALUES (?, ?, ?, ?, ?, COALESCE((SELECT MAX(sort_order) + 1 FROM accounts), 0))
		ON CONFLICT(id) DO UPDATE SET
			email = COALESCE(excluded.email, accounts.email),
			plan = COALESCE(excluded.plan, accounts.plan),
			last_updated = excluded.last_updated
	`, id, label, nullableString(email), nullableString(plan), now)
	return err
}

// TouchAccountLastUpdated bumps the last_updated column.
func (s *Store) TouchAccountLastUpdated(ctx context.Context, tx *sql.Tx, id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec := s.DB.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	_, err := exec(ctx, `UPDATE accounts SET last_updated = ? WHERE id = ?`, now, id)
	return err
}

// ListAccounts returns all accounts ordered by sort_order then last_updated DESC.
func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, account_name, email, plan, last_updated, sort_order
		FROM accounts ORDER BY sort_order ASC, last_updated DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var lu sql.NullString
		if err := rows.Scan(&a.ID, &a.AccountName, &a.Email, &a.Plan, &lu, &a.SortOrder); err != nil {
			return nil, err
		}
		if lu.Valid {
			if t, err := time.Parse(time.RFC3339Nano, lu.String); err == nil {
				a.LastUpdated = sql.NullTime{Time: t, Valid: true}
			} else if t, err := time.Parse(time.RFC3339, lu.String); err == nil {
				a.LastUpdated = sql.NullTime{Time: t, Valid: true}
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccount returns one account by id.
func (s *Store) GetAccount(ctx context.Context, id string) (Account, error) {
	var a Account
	var lu sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, account_name, email, plan, last_updated, sort_order
		FROM accounts WHERE id = ?
	`, id).Scan(&a.ID, &a.AccountName, &a.Email, &a.Plan, &lu, &a.SortOrder)
	if err != nil {
		return a, err
	}
	if lu.Valid {
		if t, err := time.Parse(time.RFC3339Nano, lu.String); err == nil {
			a.LastUpdated = sql.NullTime{Time: t, Valid: true}
		}
	}
	return a, nil
}

// FindAccountByName resolves an account by its id or display name (case-insensitive on name).
// Used by CLI verbs that accept "<id|name>".
func (s *Store) FindAccountByName(ctx context.Context, idOrName string) (Account, error) {
	if a, err := s.GetAccount(ctx, idOrName); err == nil {
		return a, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, account_name, email, plan, last_updated, sort_order
		FROM accounts
		WHERE LOWER(account_name) = LOWER(?) OR LOWER(email) = LOWER(?)
		LIMIT 1
	`, idOrName, idOrName)
	if err != nil {
		return Account{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Account{}, sql.ErrNoRows
	}
	var a Account
	var lu sql.NullString
	if err := rows.Scan(&a.ID, &a.AccountName, &a.Email, &a.Plan, &lu, &a.SortOrder); err != nil {
		return Account{}, err
	}
	if lu.Valid {
		if t, err := time.Parse(time.RFC3339Nano, lu.String); err == nil {
			a.LastUpdated = sql.NullTime{Time: t, Valid: true}
		}
	}
	return a, nil
}

// RenameAccount updates the account_name.
func (s *Store) RenameAccount(ctx context.Context, id, newName string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE accounts SET account_name = ? WHERE id = ?`, newName, id)
	return err
}

// DeleteAccount removes the account; ON DELETE CASCADE removes its usage_history rows.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	return err
}

// SetSortOrders applies a new ordering for the named accounts.
// Accounts not in the slice retain their previous order.
func (s *Store) SetSortOrders(ctx context.Context, orderedIDs []string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for i, id := range orderedIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE accounts SET sort_order = ? WHERE id = ?`, i, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

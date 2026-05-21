// Package store provides SQLite persistence for claude-monitor.
// See docs/DESIGN.md §5.2 for schema reference.
package store

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS accounts (
    id           TEXT PRIMARY KEY,
    account_name TEXT,
    email        TEXT,
    plan         TEXT,
    last_updated TEXT,
    sort_order   INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS usage_history (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id            TEXT NOT NULL,
    timestamp             TEXT NOT NULL,
    primary_percent       REAL,
    session_percent       REAL,
    weekly_all_percent    REAL,
    weekly_sonnet_percent REAL,
    session_reset         TEXT,
    weekly_reset          TEXT,
    raw_data              TEXT,
    source                TEXT,
    is_synthetic          INTEGER DEFAULT 0,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_usage_account ON usage_history(account_id);
CREATE INDEX IF NOT EXISTS idx_usage_timestamp ON usage_history(timestamp DESC);

CREATE TABLE IF NOT EXISTS oauth_credentials (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id              TEXT,
    label                   TEXT NOT NULL,
    source                  TEXT DEFAULT 'token',
    access_token            TEXT,
    refresh_token           TEXT,
    expires_at              INTEGER,
    scopes                  TEXT,
    subscription_type       TEXT,
    rate_limit_tier         TEXT,
    last_poll_at            TEXT,
    last_error              TEXT,
    usage_endpoint_state    TEXT DEFAULT 'healthy',
    usage_endpoint_attempts INTEGER DEFAULT 0,
    is_active               INTEGER DEFAULT 1,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE IF NOT EXISTS notification_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      TEXT NOT NULL,
    dimension       TEXT NOT NULL,
    threshold       INTEGER NOT NULL,
    reset_timestamp TEXT NOT NULL,
    fired_at        TEXT NOT NULL,
    UNIQUE (account_id, dimension, threshold, reset_timestamp)
);
`

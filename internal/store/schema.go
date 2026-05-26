// Package store provides SQLite persistence for claude-monitor.
package store

// schemaVersion is bumped whenever schema.go changes. openAt() wipes any
// older-versioned DB to avoid migration code — the only persisted state we
// care about (usage history) rebuilds within a few polls.
const schemaVersion = 2

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS usage_history (
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

CREATE INDEX IF NOT EXISTS idx_usage_timestamp ON usage_history(timestamp DESC);

CREATE TABLE IF NOT EXISTS notification_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    dimension       TEXT NOT NULL,
    threshold       INTEGER NOT NULL,
    reset_timestamp TEXT NOT NULL,
    fired_at        TEXT NOT NULL,
    UNIQUE (dimension, threshold, reset_timestamp)
);
`

// wipeOldSchema drops every table that ever existed in the v0.1.x DBs. Run
// only when user_version < schemaVersion. The new tables are created
// immediately after via the main schema string.
const wipeOldSchema = `
DROP TABLE IF EXISTS oauth_credentials;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS usage_history;
DROP TABLE IF EXISTS notification_log;
`

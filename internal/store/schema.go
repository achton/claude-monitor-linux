// Package store provides SQLite persistence for claude-monitor.
package store

// schemaVersion is bumped whenever schema.go changes. openAt() wipes any
// older-versioned DB to avoid migration code — the only persisted state we
// care about (usage history) rebuilds within a few polls.
const schemaVersion = 3

// One row per poll in usage_reading, and one row per reported limit in
// usage_limit. Percentages live only in usage_limit: the API's set of limits is
// open-ended, so fixed columns per limit go stale (v2 carried a
// weekly_sonnet_percent that the API stopped reporting).
const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS usage_reading (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp    TEXT NOT NULL,
    raw_data     TEXT,
    is_synthetic INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_reading_timestamp ON usage_reading(timestamp DESC);

CREATE TABLE IF NOT EXISTS usage_limit (
    reading_id  INTEGER NOT NULL REFERENCES usage_reading(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    limit_group TEXT NOT NULL,
    scope_model TEXT NOT NULL DEFAULT '',
    percent     REAL NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'normal',
    resets_at   TEXT,
    is_active   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (reading_id, kind, scope_model)
);

CREATE INDEX IF NOT EXISTS idx_limit_reading ON usage_limit(reading_id);

CREATE TABLE IF NOT EXISTS notification_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    dimension       TEXT NOT NULL,
    threshold       INTEGER NOT NULL,
    reset_timestamp TEXT NOT NULL,
    fired_at        TEXT NOT NULL,
    UNIQUE (dimension, threshold, reset_timestamp)
);
`

// wipeOldSchema drops every table that ever existed in an older DB. Run only
// when user_version < schemaVersion. The new tables are created immediately
// after via the main schema string.
const wipeOldSchema = `
DROP TABLE IF EXISTS oauth_credentials;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS usage_history;
DROP TABLE IF EXISTS usage_limit;
DROP TABLE IF EXISTS usage_reading;
DROP TABLE IF EXISTS notification_log;
`

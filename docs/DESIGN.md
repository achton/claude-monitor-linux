# Claude Monitor (Linux) — Design Document

**Status:** Single-account live-read architecture — 2026-05-26
**Module:** `github.com/achton/claude-monitor-linux`
**License:** MIT

---

## 1. Overview

A Linux desktop widget + CLI that monitors Claude AI subscription usage by
polling the Anthropic OAuth Usage endpoint with the access token Claude Code
keeps in `~/.claude/.credentials.json`.

The app is a **companion to Claude Code**. It does not manage credentials,
it does not authenticate, and it does not store tokens. Every poll, it reads
the file live, calls the API, and writes one row to a local usage history
table. That history drives the dashboard chart, the tray icon, the CLI
status command, and threshold notifications.

This is a follow-up architecture to an earlier multi-account, paste-token
design. That design had a structural bug: tokens stored in the local DB
went stale once Claude Code rotated them, and auto-refresh only fired for
credentials marked `source=claude-code`. Eliminating the local credential
store removes the bug class entirely. See decision #1 in the log.

## 2. Goals and non-goals

### Goals

1. Show current 5h-session and 7d-weekly Claude API quota usage in the
   system tray.
2. Provide a CLI surface (`status`, `poll`) for status-bar tools (waybar,
   polybar, tmux, shell prompts) via plain text, JSON, and Go template.
3. Send libnotify alerts as the user crosses configurable usage thresholds
   (default 75/90/95% plus a rate-limited "rejected" alert).
4. Preserve a per-account usage timeline for the dashboard chart.
5. Be maintainable: small dependency tree, idiomatic Go, prefer stdlib.
6. Follow XDG Base Directory and freedesktop standards.

### Non-goals

- Multi-account support. Claude Code only holds one logged-in account at a
  time; the app is single-account by construction.
- Credential management (add, paste, import, remove, pin). Claude Code owns
  this; the user uses `claude /login`.
- Multiple polling endpoints / fallbacks / state machines. One endpoint, one
  shape. If it fails, surface the error and retry on the next tick.
- Pre-rotation watching, fsnotify, refresh-token flows. The next poll
  (≤ 10 min) reads the file again, so any rotation is picked up automatically.
- Account autodetection from JSONL logs, secret-service storage, internationalization,
  remote telemetry.

## 3. Architecture

```
┌────────────────────────────────────────────────────────────────────────┐
│  Anthropic API — GET /api/oauth/usage                                  │
└──────────────────────────────┬─────────────────────────────────────────┘
                               │ every cfg.Polling.IntervalSeconds (default 600)
                               ▼
┌────────────────────────────────────────────────────────────────────────┐
│  internal/poller                                                       │
│    PollNow():                                                          │
│      1. read access token from ~/.claude/.credentials.json             │
│      2. call api.Client.OAuthUsage(token)                              │
│      3. insert one row into usage_history                              │
│      4. notify.Evaluator.EvaluateReading(label, reading)               │
│      5. update in-memory Status() snapshot for the UI                  │
└──────────────────────────────┬─────────────────────────────────────────┘
              writes usage_history rows
                               ▼
┌────────────────────────────────────────────────────────────────────────┐
│  SQLite WAL @ ~/.local/share/claude-monitor/usage.db                   │
│    Tables: usage_history, notification_log                             │
└──────────────────────────────┬─────────────────────────────────────────┘
                               │ shared read access (WAL)
        ┌──────────────────────┼──────────────────────┐
        ▼                      ▼                      ▼
┌──────────────┐      ┌────────────────┐    ┌─────────────────┐
│  internal/   │      │   internal/    │    │  internal/cli   │
│    tray      │      │      ui        │    │  status, poll   │
│  (SNI badge) │      │ (Fyne windows) │    │                 │
└──────────────┘      └────────────────┘    └─────────────────┘
```

Everything is one Go binary. Bare invocation launches the tray; subcommands
give CLI access without launching a GUI.

## 4. Project layout

```
claude-monitor-linux/
├── cmd/claude-monitor/
│   ├── main.go                     # subcommand dispatcher; launches tray or CLI
│   └── tray_entry.go               # GUI-only import barrier (see §10)
├── internal/
│   ├── api/                        # OAuth Usage HTTP client
│   │   ├── client.go               # OAuthUsage() — the only endpoint
│   │   ├── types.go                # UsageReading, ErrUnauthorized, ErrHTTP
│   │   └── client_test.go
│   ├── poller/                     # Live-read poll engine
│   │   ├── poller.go               # PollNow(), Status()
│   │   └── import_cc.go            # ReadClaudeCodeToken(), file parsing
│   ├── store/                      # SQLite persistence
│   │   ├── store.go                # Open, schema-version wipe, WithTx
│   │   ├── schema.go               # tables + DROP-old-schema block
│   │   ├── usage.go                # usage_history CRUD
│   │   ├── notifications.go        # notification_log dedupe
│   │   └── store_test.go
│   ├── notify/                     # libnotify (org.freedesktop.Notifications)
│   │   ├── notify.go               # Notifier (DBus client)
│   │   └── threshold.go            # Evaluator — fires at 75/90/95/rejected
│   ├── tray/                       # Fyne tray + DBus service
│   │   ├── tray.go                 # Run(), DBus surface, pollLoop ticker
│   │   ├── icon.go                 # Two-bar PNG renderer
│   │   ├── menu.go                 # SNI menu
│   │   └── assets/
│   ├── ui/                         # Fyne windows (dashboard, settings)
│   │   ├── account_list.go         # Dashboard (single account)
│   │   ├── chart.go                # 24h/7d/30d history chart
│   │   └── settings.go             # Threshold/interval/autostart config
│   ├── cli/                        # CLI handlers
│   │   ├── cli.go                  # Dispatcher: status, poll, version, help
│   │   ├── status.go               # plain/--json/--format/--quiet
│   │   ├── poll.go                 # DBus-delegated or in-process
│   │   └── dbus.go                 # Tray delegation client
│   ├── config/                     # TOML config (XDG_CONFIG_HOME)
│   ├── log/                        # slog→file (XDG_STATE_HOME)
│   └── xdg/                        # Paths, perms, single-instance flock
├── assets/                         # SVG icon + rendered PNGs
├── packaging/                      # nfpm spec, manpage, .desktop
├── Makefile
└── docs/DESIGN.md                  # this file
```

## 5. Data flow per poll

```
tick (every cfg.Polling.IntervalSeconds, min 60s)
  ↓
poller.PollNow(ctx)
  ↓
ReadClaudeCodeToken("")
  → resolveCCPath() — tries ~/.claude/.credentials.json then a couple fallbacks
  → readCredentialsFileWithRetry() — retries once on truncated read
  → extractCCCredentials(json) — walks the JSON for accessToken + label
  ↓
api.Client.OAuthUsage(ctx, token)
  → GET /api/oauth/usage with Bearer + anthropic-beta: oauth-2025-04-20
  → 200: parse five_hour/seven_day/seven_day_sonnet utilization + resets_at
  → 401: return ErrUnauthorized
  → other: return ErrHTTP
  ↓
WithTx:
  - LatestUsageInTx → detect weekly reset (drop > 5%)
  - if reset detected and no synthetic row in the last minute: insert two
    synthetic rows (pre-reset peak, post-reset zero) so the chart draws
    the discontinuity correctly
  - InsertUsageReading(session%, weekly%, weekly_sonnet%, resets, raw_json)
  ↓
poller stores label/lastSuccess in its in-memory state
  ↓
notify.Evaluator.EvaluateReading(label, reading)
  → for each threshold {95, 90, 75} desc, dim {session, weekly}:
    if utilization ≥ threshold AND reset > now:
      MarkNotificationFired(dim, threshold, reset) → INSERT OR IGNORE
      if newly fired: Notifier.Send(...)
      break (one threshold per dim per cycle)
```

On error, the poller stores the error string in memory and returns. The
dashboard banner reads that error directly; numbers below the banner are
labeled stale.

**First-poll suppression.** The `Poller.suppressFirstNotify` flag prevents
notifications on the launch-time poll. After the first successful poll it
flips to false for the rest of the process lifetime.

## 6. Data model

### `usage_history`

```sql
CREATE TABLE usage_history (
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
CREATE INDEX idx_usage_timestamp ON usage_history(timestamp DESC);
```

One row per poll (plus pairs of synthetic rows at weekly reset boundaries).
No `account_id` column — the app is single-account by construction.

### `notification_log`

```sql
CREATE TABLE notification_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    dimension       TEXT NOT NULL,        -- 'session' | 'weekly'
    threshold       INTEGER NOT NULL,     -- 75 | 90 | 95 | 100
    reset_timestamp TEXT NOT NULL,
    fired_at        TEXT NOT NULL,
    UNIQUE (dimension, threshold, reset_timestamp)
);
```

Each (dim, threshold, reset) tuple fires at most once. The `UNIQUE`
constraint + `INSERT OR IGNORE` is the entire dedupe mechanism. Rows are
GC'd when `reset_timestamp < now`.

### Schema versioning

`PRAGMA user_version` is bumped to `schemaVersion = 2` after the new tables
are created. On open, if `user_version < schemaVersion`, every old table is
dropped (idempotent `DROP TABLE IF EXISTS`) before the new schema runs.

This is the one-shot migration from the v0.1.x multi-account schema. There
is no other migration code, and there is no schema-evolution roadmap; if
the schema ever changes again, bump `schemaVersion` and add the relevant
drops.

## 7. Filesystem layout

```
~/.claude/.credentials.json                  # read-only, owned by Claude Code
~/.local/share/claude-monitor/usage.db       # SQLite WAL, mode 0600
~/.config/claude-monitor/config.toml         # TOML, mode 0600
~/.local/state/claude-monitor/debug.log      # slog JSON, mode 0600
~/.config/autostart/claude-monitor.desktop   # only if autostart enabled
$XDG_RUNTIME_DIR/claude-monitor.lock         # flock single-instance
```

All app-owned files: dir 0700, file 0600. The store refuses to start if
existing perms are wider than that (see `internal/xdg/perms.go`).

## 8. Config

`~/.config/claude-monitor/config.toml`. Defaults applied if absent; created
on first run.

```toml
[polling]
interval_seconds = 600  # min 60

[notifications]
enabled = true
thresholds = [75, 90, 95]

[logging]
level = "info"  # debug | info | warn | error
```

No multi-account knobs, no pinned-account setting, no adaptive throttling
toggle. The schema is intentionally tiny.

## 9. Distribution

Build from source for v0.1.x:

```bash
make build
install -Dm0755 bin/claude-monitor ~/.local/bin/claude-monitor
```

User-space install, no sudo. `~/.local/bin` is on PATH by default on most
modern distros (Ubuntu 22.04+, Debian 12+, Fedora 38+, Arch, etc.).

A `.deb` (and possibly AppImage) will return for v0.2.0 once the design has
settled. The pre-refactor 0.1.5 `.deb` shipped the multi-account code and
the bug it caused; building from source is the only supported path right now.

## 10. Headless CLI safety

The bare CLI must work with `DISPLAY` and `WAYLAND_DISPLAY` unset. This is
enforced structurally:

- `cmd/claude-monitor/main.go` only imports stdlib + `internal/...`. No
  `fyne/...`, no `fyne.io/systray`. Anything that would touch a display
  is reached via `cmd/claude-monitor/tray_entry.go`, which is referenced
  only on the `tray` subcommand path.
- `internal/cli/...` must not import `internal/ui/...` or `internal/tray/...`.
- `internal/ui/...` constructors take an existing `fyne.App` rather than
  calling `app.New()`, so even an accidental import doesn't trigger a
  display connection.

A CI step under `env -i` (Makefile target `headless-test`) runs `claude-monitor
version` without DISPLAY/WAYLAND_DISPLAY/XDG_RUNTIME_DIR to verify.

## 11. Security

- Tokens are never persisted by claude-monitor. They live in
  `~/.claude/.credentials.json` (owned by Claude Code) and in process memory
  for the duration of a poll.
- The DB never contains a credential, an OAuth secret, or any
  user-identifying data beyond the org label parsed from the credentials
  file.
- File modes are 0600 / dir 0700. Wider perms cause refuse-to-start.
- HTTPS-only; no plaintext fallback.

## 12. Decision log

| # | Decision | Alternatives considered |
|---|---|---|
| 1 | Read access token live from `~/.claude/.credentials.json` on every poll; no DB-resident credentials | Store tokens in DB and refresh on rotation (the pre-refactor design — caused the May-2026 stale-token bug); Secret Service storage; offer both modes |
| 2 | Single account by construction (Claude Code holds one at a time) | Multi-account with pin/unpin (pre-refactor); profile directories via `CLAUDE_CONFIG_DIR` |
| 3 | One API endpoint (`GET /api/oauth/usage`); no Ping fallback, no CountTokens, no state machine | Tri-endpoint with backoff/disabled states (pre-refactor); switch primary based on response |
| 4 | One-shot schema wipe via `PRAGMA user_version` < 2 | Migration code; never wipe |
| 5 | Go + Fyne v2 + fyne.io/systray | Python+PySide6, Rust+egui, Electron+TS |
| 6 | Single binary, tray + poller in one process | Split daemon + UI via systemd user service |
| 7 | Chart: weekly+session lines, 24h/7d/30d, reset markers via synthetic rows | Weekly-only-7d; combined-accounts (n/a); hand-rolled hover canvas |
| 8 | 75/90/95/rejected thresholds, both dims, godbus, suppress first poll | Daily digest; 90/95-only; shell-out to notify-send |
| 9 | Plaintext SQLite + 0600/0700, refuse-to-start on bad perms | Encrypted envelope; Secret Service |
| 10 | TOML config, 600s default poll, opt-in autostart | DB-only settings; 300s default |
| 11 | `modernc.org/sqlite` (pure Go), stdlib `log/slog` and `flag`, BurntSushi/toml | `mattn/go-sqlite3` (cgo), logrus, viper, cobra |
| 12 | CLI `poll` delegates to running tray via DBus; falls back to in-process under flock | Refuse CLI poll while tray runs; race both |
| 13 | `notification_log` GC anchored on each row's `reset_timestamp` | Local clock — vulnerable to drift |
| 14 | Synthetic-row insert wrapped in `BEGIN IMMEDIATE` transaction + 60 s idempotency guard | No transaction (race) |
| 15 | Headless CLI safety enforced by (a) import discipline in `cmd/claude-monitor/main.go` and (b) CI test under `env -i` | Trust fyne's import to remain side-effect-free indefinitely |
| 16 | User-space install at `~/.local/bin/` for v0.1.x; `.deb` returns at v0.2.0 | System-wide `.deb` only; AppImage-first |

# Claude Monitor (Linux) — Design Document

**Status:** Single-account live-read architecture, generic limit model — 2026-08-03
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
│      3. insert a usage_reading + one usage_limit row per limit         │
│      4. notify.Evaluator.EvaluateReading(label, reading)               │
│      5. update in-memory Status() snapshot for the UI                  │
└──────────────────────────────┬─────────────────────────────────────────┘
            writes usage_reading + usage_limit rows
                               ▼
┌────────────────────────────────────────────────────────────────────────┐
│  SQLite WAL @ ~/.local/share/claude-monitor/usage.db                   │
│    Tables: usage_reading, usage_limit, notification_log                │
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
│   │   ├── usage.go                # reading + limit CRUD
│   │   ├── peaks.go                # peaks, gap tolerance, chart segments
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
│   │   ├── account_list.go         # Dashboard: per-limit rows, close-calls
│   │   │                            #   summary, live-ticking countdowns
│   │   ├── chart.go                # 24h/7d/30d chart: legend, 75/90%
│   │   │                            #   guides, gap bridges, peak markers
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
  → 200: parse the self-describing limits[] array (kind, group, percent,
         severity, resets_at, scope.model); fall back to five_hour/seven_day
         when limits[] is absent
  → 401: return ErrUnauthorized
  → other: return ErrHTTP
  ↓
WithTx:
  - LatestReadingInTx → resetKeys(): per-limit drop > 5%
  - if any rolled over and no synthetic row in the last minute: insert two
    synthetic rows (pre-reset values, then zero for the rolled-over limits
    only) so the chart draws the discontinuity without faking a cliff on
    limits that did not reset
  - InsertReading(limits, raw_json)
  ↓
poller stores label/lastSuccess in its in-memory state
  ↓
notify.Evaluator.EvaluateReading(label, reading)
  → for each limit the API reported (not a fixed session/weekly pair):
    highest crossed threshold of {100, 95, 90, 75}, if reset > now:
      MarkNotificationFiredKey(limit.Key(), threshold, window) → INSERT OR IGNORE
      if newly fired: Notifier.Send(...)
    a limit with no reset window (credit spend) debounces on the calendar
    month, else its first alert would be its only one
```

On error, the poller stores the error string in memory and returns. The
dashboard banner reads that error directly; numbers below the banner are
labeled stale.

**First-poll suppression.** The `Poller.suppressFirstNotify` flag prevents
notifications on the launch-time poll. After the first successful poll it
flips to false for the rest of the process lifetime.

## 6. Data model

### `usage_reading` + `usage_limit` (schema v3)

```sql
CREATE TABLE usage_reading (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp    TEXT NOT NULL,
    raw_data     TEXT,
    is_synthetic INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE usage_limit (
    reading_id  INTEGER NOT NULL REFERENCES usage_reading(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,          -- 'session' | 'weekly_all' | 'weekly_scoped' | 'spend' | ...
    limit_group TEXT NOT NULL,
    scope_model TEXT NOT NULL DEFAULT '',
    percent     REAL NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'normal',
    resets_at   TEXT,
    is_active   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (reading_id, kind, scope_model)
);
```

One `usage_reading` per poll (plus synthetic pairs at reset boundaries), and one
`usage_limit` row per limit the API reported. Percentages live only in
`usage_limit`: the set of limits is open-ended, so fixed columns per limit go
stale — v2 carried a `weekly_sonnet_percent` that the API stopped reporting.
Limits are keyed by `(kind, scope_model)` so a model-scoped weekly limit does not
collide with the unscoped one. `is_active` is stored as a raw passthrough and is
deliberately not used for display. No `account_id` — single-account by
construction.

### `notification_log`

```sql
CREATE TABLE notification_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    dimension       TEXT NOT NULL,        -- limit key, e.g. 'session', 'weekly_scoped:Fable'
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

`PRAGMA user_version` tracks `schemaVersion = 3`. On open, v2 databases are
*migrated*: their fixed-column readings are copied into `usage_reading` +
`usage_limit` rows, then `usage_history` is dropped. Usage history is the point
of the app, so it is preserved rather than discarded. `weekly_sonnet_percent` is
dropped on the way through — the API stopped reporting it, so every stored value
is zero.

Anything older than v2 predates a mappable shape and is still wiped
(idempotent `DROP TABLE IF EXISTS`).

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

Build from source:

```bash
make build
install -Dm0755 bin/claude-monitor ~/.local/bin/claude-monitor
```

User-space install, no sudo. `~/.local/bin` is on PATH by default on most
modern distros (Ubuntu 22.04+, Debian 12+, Fedora 38+, Arch, etc.).

`.deb` and AppImage builds returned in v0.2.0 and are attached to GitHub
Releases. Building from source needs the Wayland dev headers as well as the X11
ones: GLFW 3.4 (via Fyne 2.8) compiles both backends regardless of session type.

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
| 16 | User-space install at `~/.local/bin/`; `.deb` + AppImage from v0.2.0 | System-wide `.deb` only; AppImage-first |
| 17 | Limits keyed off the API's self-describing `limits[]` array, by `(kind, scope_model)` | Fixed fields per limit — v2's `weekly_sonnet_percent` went stale when the API dropped it |
| 18 | v2 databases are migrated into the v3 tables, not wiped | Wipe-on-bump as before — but usage history is the product, so discarding it defeats the point |
| 19 | `is_active` is stored raw and never displayed; "highest utilization" drives the icon and exit codes | Treating the undocumented flag as "the binding limit" in the UI |

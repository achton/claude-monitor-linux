# Claude Monitor (Linux) — Design Document

**Status:** Locked design for v0.1.0 — 2026-05-21
**Module:** `github.com/achton/claude-monitor-linux`
**License:** MIT
**Working dir:** `/home/achton/Code/claude-monitor-linux/`

---

## 1. Overview

A Linux desktop widget + CLI that monitors Claude AI subscription usage by polling the Anthropic API with the user's Claude Code OAuth tokens. Inspired by the macOS app
[rjwalters/claude-monitor](https://github.com/rjwalters/claude-monitor) but redesigned for Linux conventions (XDG paths, libnotify, freedesktop SNI tray, distro packaging) and CLI-friendly UX (waybar / polybar / tmux / shell prompts).

This is **not a 1:1 port**. We re-use the macOS app's data model and API integration (because that's the proven, working contract with Anthropic), but the Linux app is a fresh design optimized for maintainability and idiomatic Linux behavior.

## 2. Goals and non-goals

### Goals

1. Show current Claude API quota usage (session 5h + weekly 7d) in the system tray.
2. Support multiple accounts; one of them is "pinned" and drives the tray badge.
3. Provide a CLI surface that mirrors every tray action and that can feed status-bar tools (waybar / polybar / tmux / shell prompts) via plain text, JSON, and Go-template output.
4. Send libnotify alerts as the user crosses configurable usage thresholds.
5. Ship a clean `.deb` and `AppImage` for v0.1.0; add a Launchpad PPA in v0.2.0.
6. Be maintainable: small dependency tree, idiomatic Go, prefer stdlib.
7. Follow XDG Base Directory and freedesktop standards.

### Non-goals (for v0.1.0)

- Drag-to-reorder accounts (use CLI `pin`/`reorder` or menu instead).
- Token-history overlay in the chart (no data pipeline on Linux; the macOS native host that scrapes Claude Code's local JSONL logs is not ported).
- In-app update checker (apt / AppImageUpdate own this).
- Secret Service / KWallet / GNOME Keyring token storage (planned v1.1).
- Daily summary digest notification (planned v1.1+).
- Internationalization — English only.
- Telemetry, remote error reporting, anonymous metrics — never.

## 3. Architecture

```
┌────────────────────────────────────────────────────────────────────────┐
│  Anthropic API (https://api.anthropic.com/v1/messages)                 │
└──────────────────────────────┬─────────────────────────────────────────┘
                               │ minimal-cost ping per account every 10 min
                               ▼
┌────────────────────────────────────────────────────────────────────────┐
│  internal/poller  ←──── reads tokens from ────  internal/store         │
│   - round-robin polling                                                │
│   - retry + backoff                                                    │
│   - reset detection                                                    │
└──────────────────────────────┬─────────────────────────────────────────┘
              writes usage_history rows
                               ▼
┌────────────────────────────────────────────────────────────────────────┐
│  SQLite WAL @ ~/.local/share/claude-monitor/usage.db                   │
└──────────────────────────────┬─────────────────────────────────────────┘
                               │ shared read access (WAL)
        ┌──────────────────────┼──────────────────────┬────────────────┐
        ▼                      ▼                      ▼                ▼
┌──────────────┐      ┌────────────────┐    ┌─────────────────┐  ┌─────────┐
│  internal/   │      │   internal/    │    │  internal/cli   │  │internal/│
│    tray      │      │      ui        │    │  status/list/…  │  │ notify  │
│  (SNI badge) │      │ (Fyne windows) │    │                 │  │ (DBus)  │
└──────────────┘      └────────────────┘    └─────────────────┘  └─────────┘
```

Everything is one Go binary. Bare invocation launches the tray; subcommands give CLI access without launching a GUI.

## 4. Project layout

```
claude-monitor-linux/
├── cmd/claude-monitor/
│   └── main.go                     # subcommand dispatcher; launches tray or CLI
├── internal/
│   ├── api/                        # Anthropic API client + types
│   │   ├── client.go               # http.Client, ping, identify
│   │   ├── types.go                # PingResponse, errors
│   │   └── client_test.go          # httptest-based unit tests
│   ├── store/                      # SQLite store
│   │   ├── schema.go               # CREATE TABLE statements
│   │   ├── store.go                # Open, ensure perms, WAL pragma
│   │   ├── accounts.go             # accounts CRUD
│   │   ├── usage.go                # usage_history CRUD + range queries
│   │   ├── credentials.go          # oauth_credentials CRUD
│   │   ├── settings.go             # key/value settings
│   │   └── store_test.go
│   ├── poller/                     # OAuth polling loop
│   │   ├── poller.go               # PollAll, PollDue, retry/backoff
│   │   ├── add.go                  # AddAccountWithToken, ImportFromEnv
│   │   ├── reset.go                # synthetic-row reset detection
│   │   └── poller_test.go
│   ├── tray/                       # SNI tray
│   │   ├── tray.go                 # systray.Run lifecycle, menu
│   │   ├── icon.go                 # dynamic "LLM nn%" PNG rendering
│   │   └── menu.go                 # right-click submenus
│   ├── ui/                         # Fyne windows
│   │   ├── account_list.go         # account-list window (cards)
│   │   ├── add_account.go          # paste token + .env import
│   │   ├── chart.go                # per-account chart window
│   │   └── settings.go             # pin + autostart + thresholds
│   ├── notify/                     # libnotify via godbus
│   │   ├── notify.go               # SendNotification wrapper
│   │   └── debounce.go             # per-(account,dim,threshold,reset) state
│   ├── config/                     # TOML configuration
│   │   ├── config.go               # Load, Save, defaults
│   │   └── config_test.go
│   ├── cli/                        # Subcommand handlers
│   │   ├── status.go               # plain / --json / --format
│   │   ├── accounts.go
│   │   ├── add_token.go
│   │   ├── import_env.go
│   │   ├── remove.go
│   │   ├── poll.go
│   │   ├── pin.go
│   │   └── version.go
│   ├── xdg/                        # paths + flock single-instance
│   │   ├── paths.go                # DB / config / log / lock / autostart
│   │   ├── perms.go                # enforce 0600/0700
│   │   └── lock.go                 # flock-based single instance
│   └── log/                        # slog wrapper + file rotation
│       └── log.go
├── assets/
│   ├── icon.svg
│   └── icons/                      # pre-rendered hicolor PNGs
│       ├── 16x16/apps/claude-monitor.png
│       ├── 24x24/apps/…
│       ├── 32x32/apps/…
│       ├── 48x48/apps/…
│       ├── 64x64/apps/…
│       ├── 128x128/apps/…
│       ├── 256x256/apps/…
│       └── 512x512/apps/…
├── packaging/
│   ├── deb/
│   │   ├── nfpm.yaml
│   │   ├── postinst
│   │   └── prerm
│   ├── appimage/
│   │   ├── AppRun
│   │   └── claude-monitor.AppDir.template
│   └── claude-monitor.desktop
├── .github/workflows/
│   └── release.yml                 # tag-triggered .deb + AppImage build
├── docs/
│   ├── DESIGN.md                   # this file
│   ├── QA.md                       # manual GUI test checklist
│   └── BUILD.md                    # local build instructions
├── go.mod
├── go.sum
├── Makefile
├── README.md
└── LICENSE
```

## 5. Components

### 5.1 `internal/api` — Anthropic API client

Three endpoints, used for different purposes:

1. **OAuth Usage** (`GET /api/oauth/usage`) — **primary polling endpoint**. Undocumented but stable; discovered by the community and used in production by third-party tools (`ohugonnot/claude-code-statusline`, etc.). Returns a JSON body with everything we need; zero quota cost (it's a metadata endpoint, not inference). Auth: `Authorization: Bearer <token>` + `anthropic-beta: oauth-2025-04-20`. Response body shape:
   ```json
   {
     "five_hour":        {"utilization": 23.4, "resets_at": "2026-05-21T15:00:00Z"},
     "seven_day":        {"utilization": 67.1, "resets_at": "2026-05-26T09:00:00Z"},
     "seven_day_sonnet": {"utilization": 41.2, "resets_at": "2026-05-26T09:00:00Z"}   // optional
   }
   ```
   `utilization` is 0–100 (not 0.0–1.0).

2. **Ping** (`POST /v1/messages`) — **fallback polling endpoint**. Minimal Haiku inference (`max_tokens: 1`) that returns `200` or `429`. Both responses include the `anthropic-ratelimit-unified-*` rate-limit headers (these headers are undocumented in the official rate-limits docs but confirmed in production by the macOS upstream and community status-line tools). Cost: ~1 Haiku output token per call — small but **not zero**, and it counts against the user's actual quota. Used only when OAuth Usage is in backoff (see §5.1.1).

3. **CountTokens** (`POST /v1/messages/count_tokens`) — used **only for org identification at add-account time** (return header `anthropic-organization-id`). Not part of the polling path. Zero quota cost.

#### 5.1.1 The OAuth Usage 429 problem and our backoff strategy

`/api/oauth/usage` has a documented reliability flaw: it rate-limits aggressively and persistently. Anthropic's own claude-code repo has multiple open issues (`#30930`, `#31021`, `#31637`) reporting that polling more frequently than ~5 minutes can trigger 429s that persist for 30+ minutes with no `Retry-After` header. Our 10-min default cadence should be safe most of the time, but we must handle the failure case robustly.

**Per-account state machine**, stored in DB `oauth_credentials.usage_endpoint_state`:

| State | Meaning | Polling source for this account |
|---|---|---|
| `healthy` | OAuth Usage is responding | `GET /api/oauth/usage` |
| `backoff:<until_ts>` | Got a 429 from OAuth Usage; use Ping until `until_ts` | `POST /v1/messages` (Ping) |
| `disabled` | OAuth Usage 429'd repeatedly even after backoff | `POST /v1/messages` (Ping) permanently — re-enabled via `claude-monitor probe --account <id>` |

**Transitions:**
- `healthy` → `backoff:now+15m` on first 429
- `backoff` re-tries OAuth Usage when `until_ts` elapses; on another 429, doubles the backoff (15m → 30m → 1h → 2h → 4h, capped)
- `backoff:4h+1` (third hop in the doubling sequence past the cap) → `disabled`
- `disabled` → `healthy` only via explicit `claude-monitor probe` command

While in `backoff` or `disabled`, the account is polled via Ping, which incurs the 1-Haiku-token cost. The adaptive throttling in §5.3.3 still applies on top, so high-utilization accounts in Ping mode are polled less frequently.

**Why not just always use Ping?** Because OAuth Usage is the right tool: zero cost, complete data, no inference quota burned. The macOS upstream didn't use it — likely because the rate-limit issue makes it look broken at first glance — but with proper backoff it's strictly better than Ping when it works. The fallback to Ping is for resilience, not preference.

**Why not always use OAuth Usage?** The persistent 429 case (issue #31637 reports "stuck for 30+ minutes continuously, retrying every 5 minutes still fails") means a naïve always-OAuth-Usage poller could provide stale data for hours. The state machine ensures we keep producing fresh data via Ping during outages.

#### 5.1.2 Response normalization

Both `OAuthUsage` and `Ping` produce the same internal `UsageReading` struct:

```go
type UsageReading struct {
    OrganizationID    string
    FiveHourPercent   float64   // 0–100
    FiveHourReset     time.Time
    FiveHourStatus    string    // "" | "allowed" | "allowed_warning" | "rejected"
    SevenDayPercent   float64
    SevenDayReset     time.Time
    SevenDayStatus    string
    SevenDaySonnetPercent  float64   // 0 if not present in response
    SevenDaySonnetReset    time.Time
    OverallStatus     string
    Source            string    // "oauth_usage" | "ping"
    RawJSON           string    // for the usage_history.raw_data column
}
```

The `Source` field is persisted on each `usage_history` row so we can later diagnose whether a given data point came from the primary or fallback endpoint.

Headers we send (matching what Claude Code sends so we look identical to the official client):
```
Authorization: Bearer <access_token>
anthropic-version: 2023-06-01
anthropic-beta: oauth-2025-04-20[,token-counting-2024-11-01]
User-Agent: claude-code/<version-frozen-at-build-time>
Content-Type: application/json
```

The User-Agent is a build-time constant updated by the release script. We don't need to track every Claude Code release — just any reasonably current one.

Errors are typed and bucketed:
- `Unauthorized` (401) — token expired/revoked → mark credential as such
- `HTTPError(status)` with `IsTransient()` true for 5xx
- `NetworkError(err)` (wraps DNS / socket errors) — transient
- `InvalidResponse` — body parsing or missing headers

### 5.2 `internal/store` — SQLite store

Schema (DDL applied idempotently on first start):

```sql
PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS accounts (
  id            TEXT PRIMARY KEY,        -- org id from API headers
  account_name  TEXT,                    -- user-editable display name
  email         TEXT,                    -- if known (from .env import)
  plan          TEXT,                    -- "Pro" / "Max" / etc.
  last_updated  TEXT,                    -- ISO8601
  sort_order    INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS usage_history (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id             TEXT NOT NULL,
  timestamp              TEXT NOT NULL,  -- ISO8601
  primary_percent        REAL,           -- max(session, weekly) — the headline
  session_percent        REAL,
  weekly_all_percent     REAL,
  weekly_sonnet_percent  REAL,           -- 0 unless API returns seven_day_sonnet
  session_reset          TEXT,
  weekly_reset           TEXT,
  raw_data               TEXT,           -- JSON body (OAuth Usage) or ping headers (Ping)
  source                 TEXT,           -- 'oauth_usage' | 'ping' (§5.1.2)
  is_synthetic           INTEGER DEFAULT 0,
  FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE INDEX IF NOT EXISTS idx_usage_account   ON usage_history(account_id);
CREATE INDEX IF NOT EXISTS idx_usage_timestamp ON usage_history(timestamp DESC);

CREATE TABLE IF NOT EXISTS oauth_credentials (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id             TEXT,
  label                  TEXT NOT NULL,
  source                 TEXT DEFAULT 'token',  -- 'token' | 'env'
  access_token           TEXT,
  refresh_token          TEXT,                  -- nullable; we don't refresh on Linux yet
  expires_at             INTEGER,               -- epoch ms
  scopes                 TEXT,
  subscription_type      TEXT,
  rate_limit_tier        TEXT,
  last_poll_at           TEXT,
  last_error             TEXT,
  usage_endpoint_state   TEXT DEFAULT 'healthy',-- 'healthy' | 'backoff:<ts>' | 'disabled' (§5.1.1)
  usage_endpoint_attempts INTEGER DEFAULT 0,    -- backoff doubling counter
  is_active              INTEGER DEFAULT 1,
  created_at             TEXT NOT NULL,
  updated_at             TEXT NOT NULL,
  FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS settings (
  key    TEXT PRIMARY KEY,
  value  TEXT
);

-- Per (account, dimension, threshold, reset_timestamp) debounce log.
-- Prevents duplicate notifications until the next reset window.
CREATE TABLE IF NOT EXISTS notification_log (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id        TEXT NOT NULL,
  dimension         TEXT NOT NULL,    -- 'session' | 'weekly'
  threshold         INTEGER NOT NULL, -- 75 / 90 / 95 / 100
  reset_timestamp   TEXT NOT NULL,    -- the reset that this notification's bucket belongs to
  fired_at          TEXT NOT NULL,
  UNIQUE (account_id, dimension, threshold, reset_timestamp)
);
```

The schema deliberately keeps the macOS app's column names so the data file is structurally compatible, though we use different storage paths. There is no migration tool that imports a macOS DB — users add their accounts fresh on Linux.

Driver: `modernc.org/sqlite` (pure-Go translation of SQLite). Concurrency: WAL allows the CLI and tray to read while the poller writes. Writes are short-lived so there's no contention concern at this scale.

Permissions enforcement (`internal/xdg/perms.go`):
- Parent dir created with mode `0700`.
- DB file created with mode `0600`.
- On every `Open()`: stat both, refuse-to-start if either is more permissive than expected. Print a clear remediation message:
  ```
  Refusing to open ~/.local/share/claude-monitor: mode 0755 is too permissive.
  Run: chmod 0700 ~/.local/share/claude-monitor && chmod 0600 ~/.local/share/claude-monitor/usage.db
  ```

### 5.3 `internal/poller` — OAuth poller

Single goroutine ticker loop, started by the tray subcommand. Default interval 600s (configurable via `[polling] interval_seconds`).

Two entry points:
- **`PollAll(ctx)`** — used on startup and explicit refresh: polls every active credential sequentially, staggering each subsequent account's next-poll time by spacing requests.
- **`PollDue(ctx)`** — called on each tick: polls accounts whose `last_poll_at + interval` has passed.

Per-account retry policy (matches macOS `pollWithRetry`):
- Up to 2 retries on transient errors (5xx, network)
- Exponential backoff: 2s, 4s
- Permanent errors (401, 4xx other than 429): no retry; credential marked `revoked` / `error`

After a successful API call we:
1. Insert a `usage_history` row (see §5.3.1 for transactional handling).
2. Update the credential's `last_poll_at` and clear `last_error`.
3. If a reset just occurred (previous `weekly_all_percent` was >5% higher than current), insert two **synthetic** rows: one at the old level just before the reset boundary, one at 0 right at it. This produces a clean step-down in the chart instead of a misleading interpolated line.
4. Hand a `(account_id, session_percent, weekly_percent, session_reset, weekly_reset)` event to `internal/notify` to evaluate against thresholds.

#### 5.3.1 Concurrency & atomicity of synthetic rows

WAL mode allows the CLI subcommands to read while the tray writes, but a naïve "SELECT latest row → if reset, INSERT synthetic + INSERT actual" sequence is a classic read-modify-write race: if two pollers ran concurrently, both could detect the same reset and insert duplicate synthetic rows.

The reset-detection critical section is wrapped in a single transaction with `BEGIN IMMEDIATE`, which acquires SQLite's write-lock at transaction start and serializes any concurrent writer:

```go
tx := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
// SELECT latest row for this account
// IF reset detected:
//   IF NOT EXISTS (synthetic row for same account in last 60 s):
//     INSERT synthetic (old level)
//     INSERT synthetic (zero)
// INSERT real row
tx.Commit()
```

The second guard (the `NOT EXISTS` within the last 60 s) is belt-and-suspenders: it neutralizes the duplicate even if `BEGIN IMMEDIATE` ever failed to serialize for any reason (e.g., a future schema change that splits this across statements).

#### 5.3.2 Avoiding duplicate polls (tray vs. CLI)

If the user runs `claude-monitor poll` from a terminal while the tray daemon is already running in the background, we must not (a) hit the API twice for the same account or (b) write conflicting `last_poll_at`. The lock model addresses this via DBus delegation:

- The tray daemon owns the well-known DBus name `org.claude_monitor.Tray` on the user-session bus.
- CLI `poll` probes for that name. If owned, it calls the method `org.claude_monitor.Tray.Poll(account_id)` and prints whatever the running tray returns. The tray is the sole poller.
- If the DBus name is unowned (no tray running), the CLI takes the flock at `$XDG_RUNTIME_DIR/claude-monitor.lock` briefly, runs the poll in-process, and releases. This is the "user runs the binary on a server with no display" path.

The DBus surface (§5.9) is therefore: `Focus()` (existing) + `Poll(account_id string) -> (rows int, err string)` (new).

#### 5.3.3 Adaptive throttling near limits

When `primary_percent` is high we care less about granular polling and more about not contributing to the limit. The poller adjusts its per-account next-poll interval based on the most recent usage:

| primary_percent | effective interval |
|---|---|
| < 75% | base (600 s) |
| 75–89% | base |
| 90–94% | base × 2 (1200 s) |
| ≥ 95% | base × 4 (2400 s) |

Toggleable via `[polling] adaptive = true` (default true). This caps the worst-case quota self-consumption right when it matters most.

### 5.4 `internal/tray` — System tray (SNI)

Uses `fyne.io/systray`. The library implements the freedesktop StatusNotifierItem spec over D-Bus — works on KDE, XFCE (with sni plugin), Cinnamon, MATE, Pantheon, and any Wayland compositor with a SNI host (Waybar, etc.). GNOME requires the **AppIndicator and KStatusNotifierItem Support** extension.

**Startup probe:** before calling `systray.Run`, probe `org.kde.StatusNotifierWatcher` on the session bus. If absent:
- If launched as `claude-monitor tray` explicitly → print remediation to stderr, exit 1.
- If launched as bare `claude-monitor` (e.g., from a .desktop launcher) → fire one D-Bus notification with the same remediation, set `settings.no_tray_warned = 1`, exit 0.
- If `settings.no_tray_warned = 1` → don't re-notify; just exit.

**Icon rendering** (`icon.go`):
- The icon is a small PNG generated in-process every time the tray needs to refresh: we render text "LLM" (label) + "nn%" or "(nn%)" (value, parentheses indicating weekly-limit dominant) into a 22-pixel-tall image using `image/draw` + `golang.org/x/image/font/basicfont`.
- Color thresholds: <90% = default fg (white on dark theme, black on light), ≥90 = orange (`#FF9500`), ≥95 = red (`#FF3B30`). Match macOS palette.
- **Dark-mode detection: Freedesktop Settings Portal via D-Bus.** Call `org.freedesktop.portal.Settings.Read("org.freedesktop.appearance", "color-scheme")` on the session bus. Returns `Variant<uint32>`: `0` = no preference, `1` = prefer dark, `2` = prefer light. Cached for 5 seconds; refreshed on each redraw to react to live theme switches. If the portal interface is absent (extremely old desktop / non-flatpak-aware DE), fall back to: `GTK_THEME` env override → assume dark. We deliberately do not parse `gsettings`/`kdeglobals` files — the portal is the standard surface across GNOME, KDE, Cosmic, and modern Wayland compositors as of 2026.
- Background: transparent (the tray host paints behind).

**Menu** (`menu.go`):
- Header: "Claude Monitor v0.1.0"
- One entry per account (sorted by `sort_order`, fallback by `last_updated`):
  - Account label + current `(nn%)`
  - Submenu: "📈 Chart", "📌 Show in tray", "✏ Rename", "🗑 Remove"
- Separator
- "Show accounts window…"
- "Settings…"
- "Refresh now" → triggers `poller.PollAll`
- Separator
- "Quit"

The menu is rebuilt on every account-state change rather than dynamically updated (simpler).

### 5.5 `internal/ui` — Fyne windows

Fyne v2.4+. Three windows are visible in v0.1.0:

#### 5.5.1 Account-list window (`account_list.go`)
Opened by tray menu "Show accounts window…" or `claude-monitor accounts --gui`. Contents:
- One card per account, vertically stacked, scrollable.
- Each card: account display name + plan + `(pinned)` badge if applicable + last-updated time + bar showing session% and weekly% as two horizontal bars + reset countdowns + "📈 Chart" button + "✏ Rename" / "🗑 Remove" overflow menu.
- Footer: "Add account…" + "Refresh now" + "Settings…".
- Size: 480 × 600 default, resizable.

#### 5.5.2 Add-account window (`add_account.go`)
Two tabs:
1. **Paste token** — text field for the access token; "Validate & Add" button calls `poller.AddAccountWithToken`, which pings, identifies, and inserts. Shows the discovered org name on success.
2. **Import .env** — file picker; on selection, calls `poller.ImportFromEnvFile`, shows a results table (`ACCOUNT_EMAIL_N`, success/fail, error if any).

Size: 480 × 360, modal-on-top of account-list.

#### 5.5.3 Chart window (`chart.go`)
Opened from tray submenu or account-list "📈 Chart" button. Per-account. Contents:
- Account name + plan header.
- Toggle row: **24h** / **7d** / **30d** buttons (mutually exclusive). Default `7d`.
- Chart area: PNG image of size `window_width - margins` × `300`. Re-rendered whenever the range changes or new data arrives.
- Chart contents:
  - Solid line: weekly % over time (primary, thicker, Claude-orange).
  - Dashed/lighter line: session % over time (secondary, semi-transparent grey).
  - Y axis: 0–100% with gridlines at 25/50/75/90/95.
  - X axis: time, formatted by range (24h = HH:MM, 7d = Mon DD, 30d = MMM DD).
  - Vertical dashed lines at every `is_synthetic = 1` row (reset markers).
- No tooltip / hover interactivity.

Rendering: `github.com/wcharczuk/go-chart/v2` → PNG bytes → `canvas.NewImageFromImage`.

Size: 640 × 480 default, resizable.

#### 5.5.4 Settings window (`settings.go`)
Small window invoked from tray or account-list "Settings…":
- "Pin in tray" — dropdown of accounts (+ "First found")
- "Start at login" — checkbox; toggles `~/.config/autostart/claude-monitor.desktop` presence.
- "Notifications enabled" — checkbox; mirrors `config.toml [notifications] enabled`.
- "Threshold percentages" — comma-separated input, default `75, 90, 95`.
- "Polling interval (seconds)" — number input, default 600, min 60.

Changes write through to both `config.toml` (for prefs) and DB `settings` (for pin) and are applied live.

### 5.6 `internal/notify` — D-Bus notifications

`github.com/godbus/dbus/v5`. Calls `org.freedesktop.Notifications.Notify` directly. No `notify-send` binary dependency.

**Evaluation** (after each successful poll):
For each account, for each dimension in `[session, weekly]`, compute the integer percent. Find the highest threshold from `[75, 90, 95]` that the percent crosses, plus the synthetic `100` for `status == "rejected"`. If `notification_log` already has a row for `(account_id, dim, threshold, reset_timestamp)`, skip. Otherwise:
- Compose message: e.g. `"acme — weekly at 95%, resets in 2d 4h"`
- Urgency: 75=`low`, 90=`normal`, 95+=`critical`
- App icon: claude-monitor app icon
- Insert into `notification_log` to debounce
- Call DBus `Notify` and capture the returned notification id

**Suppress first poll after launch** — keep a transient in-memory flag; bypass evaluation on the first cycle. Re-arm on next reset naturally.

**Garbage collection:** every 24 polling ticks (≈4 h at default cadence), `DELETE FROM notification_log WHERE reset_timestamp < <now>` — the boundary uses each row's own `reset_timestamp` (the API-provided ISO8601 from the ping headers), not system local time. This means: even if the user's clock drifts, the GC always tracks the actual reset windows reported by Anthropic.

### 5.7 `internal/config` — TOML config

`github.com/BurntSushi/toml`. File path: `$XDG_CONFIG_HOME/claude-monitor/config.toml` (`~/.config/claude-monitor/config.toml`).

Auto-created on first run with comments:

```toml
# Claude Monitor configuration
# https://github.com/achton/claude-monitor-linux

[polling]
interval_seconds = 600    # 10 minutes. Lowering below 300 not recommended.

[notifications]
enabled = true
thresholds = [75, 90, 95] # The "rejected" (rate-limit-hit) alert always fires
                          # when enabled = true.

[logging]
level = "info"            # "debug" | "info" | "warn" | "error"

[tray]
# Optional: pin a specific account by its org id to drive the tray badge.
# Leave empty to use the first account found.
pinned_account_id = ""
```

Loaded once at startup. Settings window edits write through.

Schema is permissive — unknown keys are warnings, not errors.

### 5.8 `internal/cli` — Subcommand handlers

Dispatcher in `cmd/claude-monitor/main.go` using stdlib `flag.FlagSet` per subcommand.

Bare invocation (no args) → `tray` subcommand.

Full surface:

```
claude-monitor                        Launch tray (default; same as `tray`)
claude-monitor tray                   Launch tray explicitly
claude-monitor status [opts]          Print current usage
   --account <id|name>                Specific account (else: pinned/first)
   --json                             JSON output
   --format <go template>             Custom format string
   --quiet                            No output; exit code only
claude-monitor accounts [--json]      List accounts
claude-monitor add-token [<token>]    Add account by token (stdin if no arg)
claude-monitor import-env <file>      Import ACCOUNT_EMAIL_N/ACCOUNT_KEY_N pairs
claude-monitor remove <id|name>       Remove an account and its data
claude-monitor poll [--account <id>]  Force one poll cycle (all or specified).
                                      If tray is running, delegates to it via DBus
                                      method org.claude_monitor.Tray.Poll; otherwise
                                      acquires the flock and polls in-process.
claude-monitor probe [--account <id>] Re-test /api/oauth/usage and reset its state
                                      machine to 'healthy'. Use after a long 429
                                      backoff window to attempt re-enabling the
                                      primary endpoint (§5.1.1).
claude-monitor pin <id|name>          Pin an account to the tray badge
claude-monitor unpin                  Clear pin (revert to first-found)
claude-monitor version                Print version
claude-monitor help [<cmd>]           Help text
```

**`status` exit codes:**

| Exit | Meaning |
|---|---|
| 0 | OK; primary usage <75% |
| 10 | ≥75% (caution) |
| 20 | ≥90% (warning) |
| 30 | ≥95% (critical) |
| 1 | Error (no data, network, etc.) |

Power-user examples:

```bash
# Waybar custom module
"custom/claude": {
  "exec": "claude-monitor status --format='{\"text\":\"LLM {{.PrimaryPercent}}%\"}'",
  "return-type": "json",
  "interval": 60
}

# Tmux right-status segment
set -g status-right '#(claude-monitor status --format="LLM {{.PrimaryPercent}}%")'

# Shell prompt warning
if ! claude-monitor status --quiet --account work; then
  PS1+="$(claude-monitor status --format='⚠ LLM {{.PrimaryPercent}}%') "
fi
```

`--format` exposes Go template variables: `.AccountName`, `.AccountID`, `.PrimaryPercent`, `.SessionPercent`, `.WeeklyPercent`, `.SessionResetIn`, `.WeeklyResetIn`, `.LastUpdated`, `.IsRateLimited`.

### 5.9 `internal/xdg` — Paths, perms, single-instance lock

**Paths:**
- `DataDir()` → `$XDG_DATA_HOME/claude-monitor` (default `~/.local/share/claude-monitor`)
- `ConfigDir()` → `$XDG_CONFIG_HOME/claude-monitor` (default `~/.config/claude-monitor`)
- `StateDir()` → `$XDG_STATE_HOME/claude-monitor` (default `~/.local/state/claude-monitor`)
- `RuntimeDir()` → `$XDG_RUNTIME_DIR` (typically `/run/user/$UID`)
- `AutostartFile()` → `$XDG_CONFIG_HOME/autostart/claude-monitor.desktop`
- `DBPath()` → `DataDir/usage.db`
- `ConfigPath()` → `ConfigDir/config.toml`
- `LogPath()` → `StateDir/debug.log`
- `LockPath()` → `RuntimeDir/claude-monitor.lock`

If `$XDG_RUNTIME_DIR` is unset (some sysvinit/Devuan installs), fall back to `/tmp/claude-monitor-$UID.lock`.

**Permissions enforcement:** `EnsureSecurePaths()` — at startup, for each dir we create or touch, set / verify `0700`; for the DB file, verify `0600`. Refuse-to-start with remediation if wrong.

**Single-instance lock:** `flock(2)` advisory exclusive lock on `LockPath()`. The tray subcommand acquires it for its whole lifetime. The CLI `poll` subcommand acquires it briefly (only when delegating-to-tray-via-DBus is not possible). Read-only CLI subcommands (`status`, `accounts`, `version`) never touch it.

On collision with another `tray` instance:
- Probe DBus for the well-known name `org.claude_monitor.Tray` on the user-session bus.
- If owned: call `org.claude_monitor.Tray.Focus` — the running tray opens its account-list window; second instance exits 0.
- If unowned (stale lock from a crashed instance with lingering file): print remediation and exit 1.

**DBus service** (`org.claude_monitor.Tray` on the session bus, owned only while the tray is running). Object path `/org/claude_monitor/Tray`. Methods:

| Method | Signature | Behavior |
|---|---|---|
| `Focus` | `() -> ()` | Open or raise the account-list window |
| `Poll` | `(account_id s) -> (rows_written i, err s)` | Trigger an immediate poll. Empty `account_id` polls all. Returns row count and an error string (empty on success) |
| `Probe` | `(account_id s) -> (state s, err s)` | Reset the OAuth Usage state machine for the account to `healthy` and re-test (§5.1.1) |

No system-bus surface. The names + paths are stable from v0.1.0; anything new gets a versioned suffix.

## 6. Data model

The `usage_history` table is append-only. We never UPDATE or DELETE history rows except when the user removes an account (cascades delete) or via the future "trim old data" cron (not in v0.1.0).

Synthetic rows (`is_synthetic = 1`) are inserted by the poller around resets to produce visually clean charts. They carry the same percent values as a real row but `raw_data = NULL` and `session_reset = weekly_reset = NULL`. They are not exposed via the CLI `status` command.

The `settings` table holds:
- `tray_pinned_account` — account id, or empty string
- `no_tray_warned` — `"1"` once we've fired the bare-invocation no-SNI notification
- `last_dbus_focus_ok` — debugging timestamp for the IPC focus mechanism
- `popover_height` — not used (we don't ship a popover)

## 7. Filesystem layout (installed)

```
/usr/bin/claude-monitor                                        # binary
/usr/share/applications/claude-monitor.desktop                 # launcher entry
/usr/share/icons/hicolor/{16x16,…,512x512}/apps/claude-monitor.png
/usr/share/man/man1/claude-monitor.1.gz                        # man page
/usr/share/doc/claude-monitor/README.md
/usr/share/doc/claude-monitor/copyright
```

Per-user runtime (created on first launch):
```
~/.local/share/claude-monitor/usage.db                         # 0600
~/.local/share/claude-monitor/                                  # 0700
~/.config/claude-monitor/config.toml                           # 0600 (sensitive only if user puts secrets in comments)
~/.config/claude-monitor/                                       # 0700
~/.local/state/claude-monitor/debug.log                        # 0600
~/.local/state/claude-monitor/debug.log.1                      # 0600 (rotated)
~/.config/autostart/claude-monitor.desktop                     # iff autostart enabled
/run/user/<uid>/claude-monitor.lock                            # 0600 (transient)
```

## 8. Distribution

### 8.1 `.deb` (v0.1.0 via GitHub releases; v0.2.0+ via Launchpad PPA)

Built with [`nfpm`](https://nfpm.goreleaser.com/) from `packaging/deb/nfpm.yaml`. Single source format, no Debian source-package complexity. Produces an architecture-specific deb (`amd64` for v0.1.0; `arm64` can be added when there's user demand).

Dependencies declared in the `.deb`:
- `libc6 (>= 2.35)` — glibc floor from Ubuntu 22.04
- `libgl1` — OpenGL for Fyne
- `libxcursor1`, `libxrandr2`, `libxinerama1`, `libxi6`, `libxxf86vm1` — X11 deps for Fyne
- *No hard dep on libnotify* — we speak DBus directly.

`postinst`:
- `update-desktop-database -q` (refresh launcher cache)
- `gtk-update-icon-cache -qf /usr/share/icons/hicolor` if present (best-effort)

`prerm`:
- No data deletion (per-user data lives in `$HOME`, not owned by the package).

### 8.2 AppImage

Built with `linuxdeploy` + `appimagetool`. Bundle:
- The Go binary
- The Fyne X11/GL runtime dependencies that aren't part of the glibc baseline
- The `.desktop` entry
- Hicolor icons (root + 256×256 for AppImage thumb)

Embed `zsync` info so `AppImageUpdate` works for in-place updates.

The AppImage runs unmodified on Fedora, Arch, openSUSE, Mint, Pop!_OS, etc. Built on Ubuntu 22.04 → glibc 2.35 is the lower bound (covers ~99% of in-support distros).

### 8.3 What we don't ship (v0.1.0)

- Snap — needs Canonical store account and confinement decisions
- Flatpak — sandboxing complicates SQLite paths and DBus access
- `.rpm` — possible later via `nfpm` (it supports both); not committed
- Arch AUR PKGBUILD — community can add it; we don't maintain

## 9. CI / release pipeline

`.github/workflows/release.yml`, triggered on tag push matching `v*.*.*`:

```yaml
on:
  push:
    tags: ['v*.*.*']

jobs:
  release:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - name: Install system deps
        run: |
          sudo apt-get update
          sudo apt-get install -y \
            gcc libgl1-mesa-dev xorg-dev libgl1 libxcursor-dev \
            libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev
      - name: Build binary
        run: make build
      - name: Run tests
        run: make test
      - name: Build .deb
        run: make deb
      - name: Build AppImage
        run: make appimage
      - name: Generate checksums
        run: cd dist && sha256sum *.deb *.AppImage > SHA256SUMS
      - uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/*.deb
            dist/*.AppImage
            dist/SHA256SUMS
          generate_release_notes: true
```

A separate `ci.yml` runs `make test lint` on every PR + push to `main`.

## 10. Build & testing

### Makefile targets

```
build           # go build → bin/claude-monitor
test            # go test ./... -cover
lint            # go vet + staticcheck
deb             # nfpm pkg --packager deb → dist/*.deb
appimage        # build the AppImage → dist/*.AppImage
release         # build + test + deb + appimage + checksums
clean           # rm -rf bin/ dist/
install         # local install via sudo make install
icons           # regenerate hicolor PNGs from assets/icon.svg via inkscape
run             # build + run the binary
```

### Testing strategy

Tier 1 (mandatory; ≥70% coverage target):
- `internal/api` — `net/http/httptest` mock server for 200/401/429 cases.
- `internal/store` — in-memory SQLite (`:memory:`), exercises schema + queries.
- `internal/poller` — combines store + mocked API; verifies retry, reset detection, debounce table writes.
- `internal/config` — round-trips TOML.
- `internal/cli` — verifies output formatting for `status` (plain / json / template).

Tier 2 (best-effort):
- `internal/notify` — mock DBus session bus; verify debounce table inserts and that we don't double-fire.
- `internal/xdg` — temp dirs, permission checks, lock acquisition.

Tier 3 (manual; `docs/QA.md`):
- Tray icon appears + updates colors at threshold boundaries.
- Right-click menu rebuilds when accounts change.
- Account-list window scrolls, cards refresh on poll.
- Chart re-renders on range change.
- Notifications fire and don't repeat within a reset window.
- Settings window edits persist across restart.
- Bare invocation on a GNOME-without-extension session fires the one-shot notification and exits.

## 11. Headless CLI safety (import discipline)

The single-binary design ships CLI subcommands and a Fyne tray in the same executable. CLI invocations must work in headless contexts (SSH session, systemd timer, cron job, container) where `$DISPLAY` / `$WAYLAND_DISPLAY` are unset. Fyne's driver and `fyne.io/systray`'s D-Bus connection open the display / bus when their entry points run; importing the packages alone is currently side-effect-free, but that's not a guarantee future versions are obliged to keep.

We enforce safety at two layers:

### 11.1 Structural (compile-time)

- `cmd/claude-monitor/main.go` imports **only** stdlib + non-GUI internal packages: `internal/cli`, `internal/store`, `internal/poller`, `internal/api`, `internal/config`, `internal/xdg`, `internal/notify`, `internal/log`. Any change adding a GUI import to `main.go` is rejected at PR review.
- GUI imports live in a sibling file `cmd/claude-monitor/tray_entry.go` whose package-scope is one function:
  ```go
  package main

  import (
      "fyne.io/fyne/v2/app"
      "github.com/achton/claude-monitor-linux/internal/tray"
      "github.com/achton/claude-monitor-linux/internal/ui"
  )

  func trayMain(deps *Deps) error { /* … */ }
  ```
- The dispatcher in `main.go` calls `trayMain` only when the `tray` subcommand is selected. CLI dispatches never reach that code path.
- Window constructors in `internal/ui` accept an `fyne.App` parameter rather than constructing one. This makes it impossible for ad-hoc `internal/ui` imports to drag in a fyne.App side effect.

### 11.2 Runtime (defense in depth)

- `app.New()` and `systray.Run()` are called **only** inside `trayMain`. They never appear in package-level `init()` functions or `var` declarations anywhere in the codebase. Static-analysis check enforced by a custom `go vet` analyzer in `make lint`.
- A CI test runs the built binary under `env -i DISPLAY= WAYLAND_DISPLAY= XAUTHORITY= ./claude-monitor status --json` with no stdin and a 5-second deadline. If it returns anything other than expected JSON or exit code 1 (no accounts), the build fails. Mirror test for `accounts`, `version`, `add-token`.

This invariant survives future fyne refactors because the structural rule (no GUI imports in `main.go`) is checked independently of fyne internals.

## 12. Security considerations

- **Tokens are plaintext** in `~/.local/share/claude-monitor/usage.db`. Threat model: equivalent to `~/.ssh/id_rsa`. Anyone with read access to the user's home directory is already past the perimeter.
- **File modes enforced**: `0600` on DB, `0700` on data/config/state dirs. Refuse-to-start on detection of weakened modes.
- **No remote network access** other than `https://api.anthropic.com`. No telemetry, no auto-update phone-home.
- **No PII in logs** — token values are never logged (only the first 8 chars when debugging refresh flows, and even that is gated behind `--log-level=debug`).
- **No privileged operations**: the binary is installed to `/usr/bin` by the .deb but never runs setuid or as root. All user data lives in `$HOME`.
- **DBus surface**: we expose `org.claude_monitor.Tray.Focus` (no-arg method, brings the account-list window forward) on the user-session bus only. No system-bus surface.

## 13. Deferred to v1.1+

| Feature | Reason for deferral |
|---|---|
| Drag-to-reorder accounts in account-list | Fyne drag-and-drop is rough; CLI `reorder` covers the need |
| Token-history overlay in chart | Requires ingesting Claude Code's local JSONL logs; the macOS native host that does this is not ported |
| Secret Service (KWallet / GNOME Keyring) token storage | Breaks on minimal WMs without a Secret Service daemon; requires migration UX |
| In-app update checker | apt + AppImageUpdate own this; no value in duplicating |
| Daily summary digest notification | Needs a robust local cron / timer; can be added when we have systemd unit shipped |
| RPM packaging | nfpm supports it; defer until there's user demand |
| arm64 builds | Same — defer until demand surfaces |
| Internationalization (gettext) | English-only is fine for v1; revisit if contributors offer translations |

## 14. Decision log

Documented as a process artifact. See conversation 2026-05-21 for full rationale per decision.

| # | Decision | Alternatives considered |
|---|---|---|
| 1 | Go + Fyne v2 + fyne.io/systray | Python+PySide6, Rust+egui, Electron+TS |
| 2 | Manual paste + .env import (no autoderive from Claude Code on Linux) | Read Claude Code's credential file, Secret Service |
| 3 | .deb + AppImage | .deb only, .deb + .rpm, .deb + AppImage + Flatpak |
| 4 | Single binary, tray + poller in one process | Split daemon + UI via systemd user service |
| 5 | Linux-native shape (CLI peer + libnotify + XDG), parity-minus | Lean MVP, full 1:1 parity, parity+token overlay |
| 6 | Flat verbs + plain/json/format + bare = tray | Noun-verb, json-only, bare = help |
| 7 | Pinned tray account, default = first found | Most-available, highest-usage, rotating |
| 8 | Chart: weekly+session lines, 24h/7d/30d, reset markers | Weekly-only-7d, +combined-accounts, hand-rolled hover canvas |
| 9 | 75/90/95/rejected, both dims, godbus, suppress first poll | + daily digest, simpler 90/95-only, shell-out to notify-send |
| 10 | Plaintext SQLite + 0600/0700, refuse-to-start on bad perms | Secret Service only, hybrid, encrypted envelope |
| 11 | Hard fail on explicit `tray`, one-shot notify on bare invocation | Always headless, always refuse, inert mini-window |
| 12 | TOML config + 600s poll + opt-in autostart | DB-only settings, 300s poll, autostart on by default |
| 13 | github.com/achton/claude-monitor-linux + MIT + SemVer 0.1.0 | Apache-2.0, 1.0.0 start, CalVer |
| 14 | GH releases v0.1.0 → PPA v0.2.0, GH Actions + Makefile, unsigned, hand-rolled SVG icon | PPA from day 1, GH-only forever, AI-generated icon |
| 15 | modernc.org/sqlite, slog→file, flock single-instance, standard cmd/internal layout, GNU Make | mattn/go-sqlite3, journald, no lock |
| 16 | Tri-endpoint client: primary `/api/oauth/usage` (JSON body, zero cost) → fallback `Ping` (1 Haiku token, header-driven) → `CountTokens` only for org-id at add-account time. Per-account state-machine handles `/api/oauth/usage` 429s with exponential backoff. | Match macOS upstream and lock to Ping (burns subscription quota); lock to OAuth Usage (fragile during 429 storms); lock to CountTokens (unconfirmed header coverage, now moot) |
| 17 | Synthetic-row insert wrapped in `BEGIN IMMEDIATE` transaction + 60 s idempotency guard | No transaction (race); table-level write-lock pragma |
| 18 | CLI `poll` delegates to running tray via DBus `org.claude_monitor.Tray.Poll`; falls back to in-process under flock when tray not running | Refuse CLI poll while tray runs; let both poll (race) |
| 19 | Adaptive throttling: poll interval × 2 at ≥90%, × 4 at ≥95% (default on, toggle in TOML) | Fixed cadence regardless of utilization |
| 20 | Dark-mode detection via `org.freedesktop.portal.Settings.Read("org.freedesktop.appearance", "color-scheme")` | gsettings + kdeglobals file parsing |
| 21 | Headless CLI safety enforced by (a) import discipline in `cmd/claude-monitor/main.go` and (b) CI test under `env -i` | Trust fyne's import to remain side-effect-free indefinitely |
| 22 | `notification_log` GC anchored on each row's `reset_timestamp` (API-provided), not system clock | Use local clock; vulnerable to clock drift |

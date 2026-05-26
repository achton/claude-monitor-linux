# Claude Monitor (Linux)

A Linux usage widget + CLI for monitoring Claude AI subscription usage.

Sits in your system tray showing 5h-session and 7d-weekly usage bars, fires
desktop notifications at configurable thresholds, and exposes a CLI surface so
you can integrate with **waybar, polybar, tmux, shell prompts, cron** —
anywhere you'd want a fresh Claude usage number without launching a GUI.

This is a from-scratch Go implementation inspired by the macOS app
[rjwalters/claude-monitor](https://github.com/rjwalters/claude-monitor),
re-architected for Linux conventions: XDG paths, freedesktop SNI tray,
libnotify, distro packaging.

> **Status:** pre-stable (`v0.1.x`). Things will change.
> See [`docs/DESIGN.md`](docs/DESIGN.md) for the architecture.

## How it works

The app is a **companion to [Claude Code](https://github.com/anthropics/claude-code)**.
On every poll, it reads the live OAuth access token from
`~/.claude/.credentials.json` and calls
`GET https://api.anthropic.com/api/oauth/usage`. Claude Code keeps that
file fresh by rotating tokens as you use it, so claude-monitor stays in sync
with whatever account you're currently logged into.

**Single account by construction.** Claude Code only holds one logged-in
account at a time; the app simply reflects that. To switch accounts, run
`claude /login` in a terminal — the next poll picks it up automatically.

## Features

- **Tray icon** with two-bar 5h/7d visualization (color-coded 90/95%).
- **Dashboard window** showing session/weekly percentages, reset countdowns,
  and an inline 24h/7d/30d history chart.
- **Threshold notifications** via `org.freedesktop.Notifications` (75/90/95%
  plus a rate-limited "rejected" alert), debounced per reset window.
- **CLI subcommands**: `status`, `poll`, `version`, `help`.
- **`status` output formats**: plain text, `--json`, or `--format` Go template.
- **Autostart-at-login** toggle (writes `~/.config/autostart/`).
- **XDG-compliant** paths; SQLite + WAL.
- **Headless-safe**: CLI works without a display.

## Install

### From source (recommended for now)

```bash
git clone https://github.com/achton/claude-monitor-linux
cd claude-monitor-linux
sudo apt-get install -y libgl1-mesa-dev libxcursor-dev libxrandr-dev \
                        libxinerama-dev libxi-dev libxxf86vm-dev xorg-dev
make build
install -Dm0755 bin/claude-monitor ~/.local/bin/claude-monitor
```

User-space install — no sudo needed. `~/.local/bin` is on PATH by default on
most distros.

Requires the Go version pinned in `go.mod` (currently 1.25+, driven by
Fyne's minimum).

### Ubuntu / Debian (.deb) — coming back in v0.2.0

The pre-0.2.0 `.deb` is deprecated. Build from source for now.

## Quick start

You need [Claude Code](https://github.com/anthropics/claude-code) installed
and logged in — that's where the access token comes from.

```bash
# Verify Claude Code is logged in:
ls ~/.claude/.credentials.json

# Launch the tray
claude-monitor tray --detach

# Or just print current usage to stdout
claude-monitor status
```

That's it. No `add-token`, no account import, no configuration required.
Open the dashboard from the tray menu to see the history chart and adjust
thresholds.

## Integrating with status bars

### Waybar (`~/.config/waybar/config`)

```json
"custom/claude": {
  "exec": "claude-monitor status --format='{\"text\":\"LLM {{.PrimaryPercent}}%\"}'",
  "return-type": "json",
  "interval": 60
}
```

### tmux

```tmux
set -g status-right '#(claude-monitor status --format="LLM {{.PrimaryPercent}}%%")'
```

### Shell prompt (bash)

```bash
PROMPT_COMMAND='claude-monitor status --quiet || echo "⚠ Claude quota high"'
```

`status` exits 0 below 75%, 10 ≥75%, 20 ≥90%, 30 ≥95%, 1 on error.

## File layout (per-user)

```
~/.claude/.credentials.json                 # read-only, owned by Claude Code
~/.local/share/claude-monitor/usage.db      # SQLite WAL, mode 0600 (history only)
~/.config/claude-monitor/config.toml        # TOML config, mode 0600
~/.local/state/claude-monitor/debug.log     # slog JSON, mode 0600
~/.config/autostart/claude-monitor.desktop  # only if autostart enabled
```

The app's DB stores only the usage timeline used for the chart. No tokens, no
account metadata — those come from `~/.claude/.credentials.json` on every poll.

## GNOME users

GNOME removed legacy system tray support. To see the tray icon you must
install the **AppIndicator and KStatusNotifierItem Support** extension:

```bash
sudo apt install gnome-shell-extension-appindicator
gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com
```

(Then log out and back in.)

On KDE, XFCE, Cinnamon, MATE, Sway/Hyprland-with-Waybar, the tray works
out of the box.

## Troubleshooting

**"No Claude Code credentials" in the dashboard banner**
You haven't installed Claude Code, or you're not logged into it. Run
`claude /login`.

**"unauthorized — token may be expired or revoked"**
Claude Code's token was rejected. Run `claude /login` to force a fresh one;
the next poll cycle picks it up.

**Tray icon missing on GNOME**
See the GNOME section above.

## License

MIT.

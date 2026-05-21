# Claude Monitor (Linux)

A Linux usage widget + CLI for monitoring Claude AI subscription usage.

Sits in your system tray showing "LLM nn%", fires desktop notifications at
configurable thresholds, and exposes a full CLI surface so you can integrate
with **waybar, polybar, tmux, shell prompts, cron** — anywhere you'd want a
fresh Claude usage number without launching a GUI.

This is a from-scratch Go implementation inspired by the macOS app
[rjwalters/claude-monitor](https://github.com/rjwalters/claude-monitor),
re-architected for Linux conventions: XDG paths, freedesktop SNI tray,
libnotify, distro packaging.

> **Status:** pre-stable (`v0.1.0`). Things will change.
> See [`docs/DESIGN.md`](docs/DESIGN.md) for the full architecture.

## Features

- **Tray icon** with live "LLM nn%" rendering (color-coded thresholds 90/95%).
- **CLI subcommands** for `status`, `accounts`, `add-token`, `import-env`,
  `remove`, `poll`, `pin`, `unpin`, `probe`, `version`.
- **`status` output formats**: plain text, `--json`, or `--format` Go template.
- **Threshold notifications** via `org.freedesktop.Notifications` (75/90/95%
  plus a rate-limited "rejected" alert), debounced per reset window.
- **Multi-account** support; pin one to the tray badge.
- **Per-account chart window** (weekly + session lines, 24h / 7d / 30d ranges,
  reset markers).
- **Adaptive throttling** near limits — slows polling when you're near 90%+
  to avoid burning more quota.
- **XDG-compliant** paths; SQLite + WAL; tokens stored at 0600.
- **Headless-safe**: CLI subcommands work without a display
  (verified by CI under `env -i`).

## Install

### Ubuntu / Debian (.deb)

Download the latest `claude-monitor_<version>_amd64.deb` from the
[Releases](https://github.com/achton/claude-monitor-linux/releases) page:

```bash
sudo dpkg -i claude-monitor_*_amd64.deb
sudo apt-get install -f   # if dependencies need resolving
```

Targets glibc ≥ 2.35 — works on Ubuntu 22.04+, 24.04+, Debian 12+, Mint 21+,
Pop!_OS 22.04+, and derivatives.

### Other distros (AppImage)

```bash
chmod +x claude-monitor-*-x86_64.AppImage
./claude-monitor-*-x86_64.AppImage tray
```

Runs on Fedora, Arch, openSUSE, etc.

### Build from source

```bash
git clone https://github.com/achton/claude-monitor-linux
cd claude-monitor-linux
sudo apt-get install -y libgl1-mesa-dev libxcursor-dev libxrandr-dev \
                        libxinerama-dev libxi-dev libxxf86vm-dev xorg-dev
make build
./bin/claude-monitor version
```

Requires Go 1.22+.

## Quick start

```bash
# Paste a Claude Code OAuth access token (long string starting sk-ant-…)
echo "$TOKEN" | claude-monitor add-token

# Verify
claude-monitor accounts

# Launch the tray (or set it to autostart via the Settings window)
claude-monitor tray
```

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

### Shell prompt (bash, post-execute hook)

```bash
PROMPT_COMMAND='claude-monitor status --quiet || echo "⚠ Claude quota high"'
```

`status` exits 0 below 75%, 10 ≥75%, 20 ≥90%, 30 ≥95%, 1 on error — handy
for shell threshold checks.

## File layout (per-user)

```
~/.local/share/claude-monitor/usage.db      # SQLite WAL, mode 0600
~/.config/claude-monitor/config.toml        # TOML config, mode 0600
~/.local/state/claude-monitor/debug.log     # slog JSON, mode 0600
~/.config/autostart/claude-monitor.desktop  # only if autostart enabled
```

## GNOME users

GNOME removed legacy system tray support. To see the tray icon you must
install the **AppIndicator and KStatusNotifierItem Support** extension:

```
sudo apt install gnome-shell-extension-appindicator
gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com
```

(Then log out and back in.)

On KDE, XFCE, Cinnamon, MATE, Sway/Hyprland-with-Waybar, the tray works
out of the box.

## License

MIT.

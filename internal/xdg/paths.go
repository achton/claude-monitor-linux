// Package xdg provides XDG Base Directory paths for claude-monitor.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

const appName = "claude-monitor"

// DataDir returns $XDG_DATA_HOME/claude-monitor (default ~/.local/share/claude-monitor).
func DataDir() string {
	return appDir("XDG_DATA_HOME", ".local/share")
}

// ConfigDir returns $XDG_CONFIG_HOME/claude-monitor (default ~/.config/claude-monitor).
func ConfigDir() string {
	return appDir("XDG_CONFIG_HOME", ".config")
}

// StateDir returns $XDG_STATE_HOME/claude-monitor (default ~/.local/state/claude-monitor).
func StateDir() string {
	return appDir("XDG_STATE_HOME", ".local/state")
}

// RuntimeDir returns $XDG_RUNTIME_DIR (e.g. /run/user/$UID) or /tmp fallback.
func RuntimeDir() string {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return v
	}
	return fmt.Sprintf("/tmp/claude-monitor-%d", os.Getuid())
}

// DBPath returns DataDir/usage.db.
func DBPath() string { return filepath.Join(DataDir(), "usage.db") }

// ConfigPath returns ConfigDir/config.toml.
func ConfigPath() string { return filepath.Join(ConfigDir(), "config.toml") }

// LogPath returns StateDir/debug.log.
func LogPath() string { return filepath.Join(StateDir(), "debug.log") }

// LockPath returns RuntimeDir/claude-monitor.lock.
func LockPath() string {
	rd := RuntimeDir()
	if rd == fmt.Sprintf("/tmp/claude-monitor-%d", os.Getuid()) {
		return rd + ".lock"
	}
	return filepath.Join(rd, "claude-monitor.lock")
}

// AutostartFile returns ConfigDir's neighbouring autostart entry path.
func AutostartFile() string {
	return filepath.Join(configHomeRaw(), "autostart", "claude-monitor.desktop")
}

func appDir(env, defaultSub string) string {
	if v := os.Getenv(env); v != "" {
		return filepath.Join(v, appName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, defaultSub, appName)
}

func configHomeRaw() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, ".config")
}

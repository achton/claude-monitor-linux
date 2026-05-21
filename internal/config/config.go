// Package config loads and saves the TOML configuration file at $XDG_CONFIG_HOME/claude-monitor/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/achton/claude-monitor-linux/internal/xdg"
)

// Config is the on-disk configuration shape.
type Config struct {
	Polling       PollingConfig       `toml:"polling"`
	Notifications NotificationsConfig `toml:"notifications"`
	Logging       LoggingConfig       `toml:"logging"`
	Tray          TrayConfig          `toml:"tray"`
}

type PollingConfig struct {
	// IntervalSeconds is the base per-account polling cadence in seconds.
	IntervalSeconds int  `toml:"interval_seconds"`
	// Adaptive turns on adaptive throttling near limits (§5.3.3 of DESIGN.md).
	Adaptive        bool `toml:"adaptive"`
}

type NotificationsConfig struct {
	Enabled    bool  `toml:"enabled"`
	Thresholds []int `toml:"thresholds"`
}

type LoggingConfig struct {
	Level string `toml:"level"`
}

type TrayConfig struct {
	// PinnedAccountID is the account org id whose usage is shown in the tray.
	// Empty = first found.
	PinnedAccountID string `toml:"pinned_account_id"`
}

// Default returns the v0.1.0 default configuration.
func Default() Config {
	return Config{
		Polling: PollingConfig{
			IntervalSeconds: 600,
			Adaptive:        true,
		},
		Notifications: NotificationsConfig{
			Enabled:    true,
			Thresholds: []int{75, 90, 95},
		},
		Logging: LoggingConfig{Level: "info"},
		Tray:    TrayConfig{PinnedAccountID: ""},
	}
}

// Load reads $XDG_CONFIG_HOME/claude-monitor/config.toml.
// If the file does not exist, Default() is returned and the file is created on disk.
func Load() (Config, error) {
	path := xdg.ConfigPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		def := Default()
		if err := Save(def); err != nil {
			return def, err
		}
		return def, nil
	}
	cfg := Default()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("decode %s: %w", path, err)
	}
	// Defensive: enforce minimum cadence to avoid burning quota.
	if cfg.Polling.IntervalSeconds < 60 {
		cfg.Polling.IntervalSeconds = 60
	}
	return cfg, nil
}

// Save writes the configuration to disk with comments preserved as a header.
func Save(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(xdg.ConfigPath()), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(xdg.ConfigPath()), "config.toml.*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmp.Name())
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	header := `# Claude Monitor configuration
# https://github.com/achton/claude-monitor-linux
#
# Edits made here are loaded on next launch. Some keys can also be edited
# via the GUI Settings window or the CLI.

`
	if _, err := tmp.WriteString(header); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), xdg.ConfigPath())
}

// Package log wires log/slog to a rotating file at $XDG_STATE_HOME/claude-monitor/debug.log.
package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/achton/claude-monitor-linux/internal/xdg"
)

const maxLogBytes int64 = 5 * 1024 * 1024 // 5 MB

var (
	mu      sync.Mutex
	current *os.File
	logger  *slog.Logger
)

// Init opens the log file and configures slog with the given level.
// level is one of "debug" | "info" | "warn" | "error".
func Init(level string) error {
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(xdg.StateDir(), 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if err := rotateIfNeededLocked(); err != nil {
		return err
	}
	f, err := os.OpenFile(xdg.LogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	current = f

	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})
	fileHandler := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: lvl})
	logger = slog.New(multiHandler{file: fileHandler, stderr: stderrHandler})
	slog.SetDefault(logger)
	return nil
}

// Logger returns the configured slog.Logger, or slog.Default() if not initialized.
func Logger() *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	if logger == nil {
		return slog.Default()
	}
	return logger
}

// Close closes the underlying file.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return nil
	}
	err := current.Close()
	current = nil
	return err
}

func rotateIfNeededLocked() error {
	st, err := os.Stat(xdg.LogPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Size() < maxLogBytes {
		return nil
	}
	old := filepath.Join(xdg.StateDir(), "debug.log.1")
	_ = os.Remove(old)
	return os.Rename(xdg.LogPath(), old)
}

type multiHandler struct {
	file   slog.Handler
	stderr slog.Handler
}

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return m.file.Enabled(ctx, level) || m.stderr.Enabled(ctx, level)
}
func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	if m.file.Enabled(ctx, r.Level) {
		if err := m.file.Handle(ctx, r.Clone()); err != nil {
			return err
		}
	}
	if m.stderr.Enabled(ctx, r.Level) {
		if err := m.stderr.Handle(ctx, r); err != nil {
			return err
		}
	}
	return nil
}
func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return multiHandler{file: m.file.WithAttrs(attrs), stderr: m.stderr.WithAttrs(attrs)}
}
func (m multiHandler) WithGroup(name string) slog.Handler {
	return multiHandler{file: m.file.WithGroup(name), stderr: m.stderr.WithGroup(name)}
}

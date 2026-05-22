package poller

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/achton/claude-monitor-linux/internal/log"
)

// StartCredentialsWatcher runs an fsnotify watch on
// ~/.claude/.credentials.json (resolved via the same logic ImportFromClaudeCode
// uses). When the file is written/renamed, all claude-code-sourced credentials
// are re-read so the cached access token never lags behind Claude Code's own
// rotation.
//
// The watcher gracefully handles the no-Claude-Code case: if the file does
// not exist at startup, an info-level message explains that auto-refresh is
// disabled and the goroutine returns. Users with paste-only accounts get
// the same behavior as before — polls fail with a clear error in the UI.
//
// Returns immediately after starting the goroutine. The watcher exits when
// ctx is cancelled.
func (p *Poller) StartCredentialsWatcher(ctx context.Context) {
	go p.runCredentialsWatcher(ctx)
}

func (p *Poller) runCredentialsWatcher(ctx context.Context) {
	path, err := resolveCCPath("")
	if err != nil {
		log.Logger().Info(
			"claude-code credentials file not found; auto-refresh disabled",
			slog.String("hint", "install Claude Code and run `claude-monitor import-claude-code`, or use `add-token` for a paste-only account"),
		)
		return
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Logger().Warn("fsnotify watcher init failed", slog.String("err", err.Error()))
		return
	}
	defer w.Close()

	// Watch the parent directory rather than the file itself: editors (and
	// Claude Code's rewrite-on-rotation) usually write to a tempfile and
	// rename over the target. A direct file watch would lose track of the
	// inode on rename; a parent-dir watch catches both Write and Create.
	parent := filepath.Dir(path)
	if err := w.Add(parent); err != nil {
		log.Logger().Warn("fsnotify add parent dir failed",
			slog.String("dir", parent), slog.String("err", err.Error()))
		return
	}
	log.Logger().Info("claude-code credentials watcher started",
		slog.String("path", path))

	// Debounce — Claude Code's rotation can fire multiple events in
	// quick succession. Coalesce into one re-read.
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// Only act on changes to OUR file.
			if filepath.Clean(ev.Name) != filepath.Clean(path) {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if pending == nil {
				pending = time.After(250 * time.Millisecond)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Logger().Warn("fsnotify error", slog.String("err", err.Error()))
		case <-pending:
			pending = nil
			p.refreshAllClaudeCodeCreds(ctx)
		}
	}
}

// refreshAllClaudeCodeCreds is invoked when the credentials file changed on
// disk. It re-reads the file and updates the access token for every active
// credential whose source is "claude-code".
func (p *Poller) refreshAllClaudeCodeCreds(ctx context.Context) {
	creds, err := p.Store.ListActiveCredentials(ctx)
	if err != nil {
		log.Logger().Warn("list credentials for watcher refresh failed",
			slog.String("err", err.Error()))
		return
	}
	updated := 0
	for _, c := range creds {
		if c.Source != "claude-code" {
			continue
		}
		if _, ok := p.tryReReadClaudeCode(ctx, c); ok {
			updated++
		}
	}
	if updated > 0 {
		log.Logger().Info("credentials watcher refreshed tokens",
			slog.Int("updated", updated))
	}
}

// ClaudeCodeCredentialsMissing reports whether the local Claude Code
// credentials file (~/.claude/.credentials.json or its conventional fallbacks)
// is absent. The UI uses this to tell paste-only users that auto-refresh
// won't work without Claude Code.
func ClaudeCodeCredentialsMissing() bool {
	_, err := resolveCCPath("")
	return err != nil
}

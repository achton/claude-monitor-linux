package poller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// minClaudeVersion is the first Claude Code release whose `auth status` saves
// a token refresh before it exits.
var minClaudeVersion = [3]int{2, 1, 317}

const (
	refreshInterval = 15 * time.Minute
	versionTimeout  = 15 * time.Second
)

// refreshTimeout is how long Refresh waits for `claude auth status`.
var refreshTimeout = 60 * time.Second

// ErrRefreshThrottled means a refresh ran within refreshInterval, or is still
// running.
var ErrRefreshThrottled = errors.New("token refresh attempted recently")

// Refresher gets Claude Code to refresh its own access token by running
// `claude auth status`. The refresh token is only ever used by Claude Code,
// which rotates it under its own lock.
type Refresher struct {
	// Path is the claude binary. Empty searches PATH, then the usual install
	// locations, since an autostarted tray may not have the shell's PATH.
	Path string
	// ConfigDir is passed on as CLAUDE_CONFIG_DIR when set.
	ConfigDir string

	mu      sync.Mutex
	last    time.Time
	running bool // an `auth status` started here has not exited yet
}

// Refresh runs `claude auth status`, at most once per refreshInterval and
// never twice at once. It waits up to refreshTimeout or until ctx ends, but
// never signals the process: killing it between the server rotating the
// refresh token and Claude Code saving it would sign the user out.
func (r *Refresher) Refresh(ctx context.Context) error {
	r.mu.Lock()
	if r.running || (!r.last.IsZero() && time.Since(r.last) < refreshInterval) {
		r.mu.Unlock()
		return ErrRefreshThrottled
	}
	r.last = time.Now()
	r.running = true
	r.mu.Unlock()

	done, err := r.start(ctx)
	if err != nil {
		r.setRunning(false)
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("claude auth status: %w", ctx.Err())
	case <-time.After(refreshTimeout):
		return fmt.Errorf("claude auth status still running after %s", refreshTimeout)
	}
}

// start checks the claude version, then starts `auth status` and returns a
// channel that receives its result once it exits.
func (r *Refresher) start(ctx context.Context) (<-chan error, error) {
	bin, err := r.binary()
	if err != nil {
		return nil, err
	}
	vctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	ver := exec.CommandContext(vctx, bin, "--version")
	ver.Env = r.env()
	out, err := ver.Output()
	if err != nil {
		return nil, fmt.Errorf("claude --version: %w", err)
	}
	if v, ok := parseVersion(string(out)); !ok || versionLess(v, minClaudeVersion) {
		return nil, fmt.Errorf("claude %q is older than %d.%d.%d",
			strings.TrimSpace(string(out)), minClaudeVersion[0], minClaudeVersion[1], minClaudeVersion[2])
	}

	var output bytes.Buffer
	cmd := exec.Command(bin, "auth", "status", "--json")
	cmd.Env = r.env()
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude auth status: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		r.setRunning(false)
		if err != nil {
			err = fmt.Errorf("claude auth status: %w: %s", err, firstLine(output.Bytes()))
		}
		done <- err
	}()
	return done, nil
}

func (r *Refresher) setRunning(v bool) {
	r.mu.Lock()
	r.running = v
	r.mu.Unlock()
}

// env passes ConfigDir on as CLAUDE_CONFIG_DIR. Nil inherits the environment.
func (r *Refresher) env() []string {
	if dir := ClaudeConfigDir(r.ConfigDir); dir != "" {
		return append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	}
	return nil
}

func (r *Refresher) binary() (string, error) {
	if r.Path != "" {
		return expandHome(r.Path), nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".claude", "local", "claude"),
		filepath.Join(home, ".npm-global", "bin", "claude"),
		filepath.Join(home, ".bun", "bin", "claude"),
		"/usr/local/bin/claude",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("claude not found on PATH or in the usual install locations")
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func parseVersion(s string) ([3]int, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

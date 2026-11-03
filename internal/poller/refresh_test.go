package poller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// stubClaude stands in for the claude CLI: `auth status` writes the
// credentials in $STUB_NEW_CREDS into $CLAUDE_CONFIG_DIR and logs the call.
const stubClaude = `#!/bin/sh
case "$1" in
--version) echo "$STUB_VERSION (Claude Code)" ;;
auth)
	echo auth >> "$STUB_LOG"
	sleep "${STUB_SLEEP:-0}"
	printf '%s' "$STUB_NEW_CREDS" > "$CLAUDE_CONFIG_DIR/.credentials.json" ;;
esac
`

func credsJSON(token string, expires, signInExpires time.Time) string {
	return fmt.Sprintf(`{"claudeAiOauth": {"accessToken": %q, "expiresAt": %d, "refreshTokenExpiresAt": %d}}`,
		token, expires.UnixMilli(), signInExpires.UnixMilli())
}

type refreshFixture struct {
	dir, log string
	poller   *Poller
}

func newRefreshFixture(t *testing.T, version string, creds string) *refreshFixture {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(stubClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("STUB_VERSION", version)
	t.Setenv("STUB_LOG", log)
	t.Setenv("STUB_SLEEP", "0")
	t.Setenv("STUB_NEW_CREDS", credsJSON("new-token", time.Now().Add(8*time.Hour), time.Now().Add(30*24*time.Hour)))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"limits": [{"kind": "session", "group": "session", "percent": 5}]}`))
	}))
	t.Cleanup(srv.Close)

	s, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	p := New(s, &api.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}, nil)
	p.ConfigDir = dir
	p.Refresher = &Refresher{Path: bin, ConfigDir: dir}
	return &refreshFixture{dir: dir, log: log, poller: p}
}

func (f *refreshFixture) authCalls(t *testing.T) int {
	b, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "auth")
}

func TestExpiredTokenIsRefreshedViaClaude(t *testing.T) {
	f := newRefreshFixture(t, "2.1.317",
		credsJSON("old-token", time.Now().Add(-time.Hour), time.Now().Add(20*24*time.Hour)))

	if err := f.poller.PollNow(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if n := f.authCalls(t); n != 1 {
		t.Errorf("auth status calls: got %d, want 1", n)
	}
}

// A 401 on a token that has not reached its recorded expiry also refreshes.
func TestUnauthorizedTokenIsRefreshed(t *testing.T) {
	f := newRefreshFixture(t, "2.1.320",
		credsJSON("old-token", time.Now().Add(time.Hour), time.Now().Add(20*24*time.Hour)))

	if err := f.poller.PollNow(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if n := f.authCalls(t); n != 1 {
		t.Errorf("auth status calls: got %d, want 1", n)
	}
}

func TestOldClaudeIsNotAskedToRefresh(t *testing.T) {
	f := newRefreshFixture(t, "2.1.300",
		credsJSON("old-token", time.Now().Add(-time.Hour), time.Now().Add(20*24*time.Hour)))

	if err := f.poller.PollNow(context.Background()); err == nil {
		t.Fatal("poll succeeded with an expired token")
	}
	if n := f.authCalls(t); n != 0 {
		t.Errorf("auth status calls: got %d, want 0", n)
	}
	if _, msg, _, _ := f.poller.Status(); !strings.Contains(msg, "older than 2.1.317") {
		t.Errorf("error should name the version gate: %q", msg)
	}
}

func TestRefreshIsThrottled(t *testing.T) {
	old := credsJSON("old-token", time.Now().Add(-time.Hour), time.Now().Add(20*24*time.Hour))
	f := newRefreshFixture(t, "2.1.317", old)
	t.Setenv("STUB_NEW_CREDS", old)

	for range 3 {
		_ = f.poller.PollNow(context.Background())
	}
	if n := f.authCalls(t); n != 1 {
		t.Errorf("auth status calls: got %d, want 1", n)
	}
	if _, msg, _, _ := f.poller.Status(); !strings.Contains(msg, "token unchanged") {
		t.Errorf("error should keep the refresh failure: %q", msg)
	}
}

func TestExpiredSignInIsNotRefreshed(t *testing.T) {
	f := newRefreshFixture(t, "2.1.317",
		credsJSON("old-token", time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)))

	if err := f.poller.PollNow(context.Background()); err == nil {
		t.Fatal("poll succeeded with an expired sign-in")
	}
	if n := f.authCalls(t); n != 0 {
		t.Errorf("auth status calls: got %d, want 0", n)
	}
	if _, msg, _, _ := f.poller.Status(); !strings.Contains(msg, "claude /login") {
		t.Errorf("error should point at /login: %q", msg)
	}
}

func TestParseVersion(t *testing.T) {
	v, ok := parseVersion("2.1.317 (Claude Code)\n")
	if !ok || v != [3]int{2, 1, 317} {
		t.Fatalf("got %v %v", v, ok)
	}
	if !versionLess([3]int{2, 1, 99}, minClaudeVersion) || versionLess([3]int{3, 0, 0}, minClaudeVersion) {
		t.Error("versionLess ordering")
	}
}

// A slow `auth status` must finish saving the token even after Refresh gives
// up waiting; killing it could sign the user out.
func TestSlowRefreshIsNotKilled(t *testing.T) {
	f := newRefreshFixture(t, "2.1.317",
		credsJSON("old-token", time.Now().Add(-time.Hour), time.Now().Add(20*24*time.Hour)))
	t.Setenv("STUB_SLEEP", "0.5")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := f.poller.Refresher.Refresh(ctx); err == nil {
		t.Fatal("Refresh returned before auth status finished, without an error")
	}
	if err := f.poller.Refresher.Refresh(context.Background()); !errors.Is(err, ErrRefreshThrottled) {
		t.Errorf("second Refresh while running: got %v, want ErrRefreshThrottled", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := ReadClaudeCodeCredentials(f.dir)
		if err == nil && c.Token == "new-token" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("auth status never saved the new token")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestConcurrentPollsShareOneRefresh(t *testing.T) {
	f := newRefreshFixture(t, "2.1.317",
		credsJSON("old-token", time.Now().Add(-time.Hour), time.Now().Add(20*24*time.Hour)))
	t.Setenv("STUB_SLEEP", "0.3")

	errs := make(chan error, 3)
	for range 3 {
		go func() { errs <- f.poller.PollNow(context.Background()) }()
	}
	for range 3 {
		if err := <-errs; err != nil {
			t.Errorf("poll: %v", err)
		}
	}
	if n := f.authCalls(t); n != 1 {
		t.Errorf("auth status calls: got %d, want 1", n)
	}
	if _, msg, _, _ := f.poller.Status(); msg != "" {
		t.Errorf("status error after success: %q", msg)
	}
}

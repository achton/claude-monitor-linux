package poller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

func TestRateLimitPausesPolling(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(credsJSON("tok", time.Now().Add(time.Hour), time.Now().Add(20*24*time.Hour))), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	s, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p := New(s, &api.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}, nil)
	p.ConfigDir = dir
	for range 3 {
		var rl *RateLimitedError
		if err := p.PollNow(context.Background()); !errors.As(err, &rl) {
			t.Fatalf("got %v, want RateLimitedError", err)
		}
	}
	if requests != 1 {
		t.Errorf("requests: got %d, want 1", requests)
	}
	if until := p.RateLimitedUntil(); time.Until(until) < 9*time.Minute {
		t.Errorf("paused until %v, want about 10 minutes out", until)
	}
}

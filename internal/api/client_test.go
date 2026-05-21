package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOAuthUsage_OK(t *testing.T) {
	body := `{
		"five_hour":  {"utilization": 23.4, "resets_at": "2026-05-21T15:00:00Z"},
		"seven_day":  {"utilization": 67.1, "resets_at": "2026-05-26T09:00:00Z"},
		"seven_day_sonnet": {"utilization": 41.2, "resets_at": "2026-05-26T09:00:00Z"}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/api/oauth/usage") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing Bearer")
		}
		if r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Errorf("missing anthropic-beta")
		}
		w.Header().Set("anthropic-organization-id", "org-abc123")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatalf("OAuthUsage: %v", err)
	}
	if r.OrganizationID != "org-abc123" {
		t.Errorf("org id: got %q", r.OrganizationID)
	}
	if r.FiveHourPercent != 23.4 {
		t.Errorf("five_hour utilization: got %v", r.FiveHourPercent)
	}
	if r.SevenDayPercent != 67.1 {
		t.Errorf("seven_day utilization: got %v", r.SevenDayPercent)
	}
	if r.SevenDaySonnetPercent != 41.2 {
		t.Errorf("seven_day_sonnet utilization: got %v", r.SevenDaySonnetPercent)
	}
	if r.Source != "oauth_usage" {
		t.Errorf("source: got %q", r.Source)
	}
	if r.PrimaryPercent() != 67.1 {
		t.Errorf("primary: got %v", r.PrimaryPercent())
	}
}

func TestOAuthUsage_429WithRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error"}}`))
	}))
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	_, err := c.OAuthUsage(context.Background(), "tok")
	tr, ok := errAsTooMany(err)
	if !ok {
		t.Fatalf("expected ErrTooManyRequests, got %v", err)
	}
	if tr.RetryAfter != 120*time.Second {
		t.Errorf("retry-after: got %v", tr.RetryAfter)
	}
}

func TestOAuthUsage_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	_, err := c.OAuthUsage(context.Background(), "tok")
	if err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPing_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST")
		}
		w.Header().Set("anthropic-organization-id", "org-xyz")
		w.Header().Set("anthropic-ratelimit-unified-5h-utilization", "0.42")
		w.Header().Set("anthropic-ratelimit-unified-5h-reset", "1747836000")
		w.Header().Set("anthropic-ratelimit-unified-5h-status", "allowed")
		w.Header().Set("anthropic-ratelimit-unified-7d-utilization", "0.91")
		w.Header().Set("anthropic-ratelimit-unified-7d-reset", "1748000000")
		w.Header().Set("anthropic-ratelimit-unified-7d-status", "allowed_warning")
		w.Header().Set("anthropic-ratelimit-unified-status", "allowed_warning")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"msg_x","content":[{"type":"text","text":""}]}`))
	}))
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.Ping(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if r.OrganizationID != "org-xyz" {
		t.Errorf("org: got %q", r.OrganizationID)
	}
	if r.FiveHourPercent != 42 {
		t.Errorf("5h pct: got %v", r.FiveHourPercent)
	}
	if r.SevenDayPercent != 91 {
		t.Errorf("7d pct: got %v", r.SevenDayPercent)
	}
	if r.OverallStatus != "allowed_warning" {
		t.Errorf("overall: got %q", r.OverallStatus)
	}
	if r.Source != "ping" {
		t.Errorf("source: got %q", r.Source)
	}
}

func TestCountTokens_OrgID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages/count_tokens") {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("anthropic-organization-id", "org-7777")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"input_tokens": 2}`))
	}))
	defer srv.Close()
	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	org, err := c.CountTokens(context.Background(), "tok")
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if org != "org-7777" {
		t.Errorf("org: got %q", org)
	}
}

// errAsTooMany unwraps the typed error from a wrapped chain (no errors.As alias).
func errAsTooMany(err error) (*ErrTooManyRequests, bool) {
	for err != nil {
		if tr, ok := err.(*ErrTooManyRequests); ok {
			return tr, true
		}
		unwrap, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = unwrap.Unwrap()
	}
	return nil, false
}

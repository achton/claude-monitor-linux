package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatalf("OAuthUsage: %v", err)
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
	if r.PrimaryPercent() != 67.1 {
		t.Errorf("primary: got %v", r.PrimaryPercent())
	}
}

func TestOAuthUsage_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	_, err := c.OAuthUsage(context.Background(), "tok")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestOAuthUsage_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()
	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	_, err := c.OAuthUsage(context.Background(), "tok")
	var he *ErrHTTP
	if !errors.As(err, &he) || he.Status != 500 {
		t.Fatalf("expected ErrHTTP 500, got %v", err)
	}
}

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveBody(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}

// The current response shape: a self-describing limits[] array, with the legacy
// per-window fields present but the model-scoped ones nulled out.
const currentBody = `{
	"five_hour": {"utilization": 2.0, "resets_at": "2026-08-03T18:00:00.317099+00:00"},
	"seven_day": {"utilization": 30.0, "resets_at": "2026-08-06T04:00:00.317126+00:00"},
	"seven_day_sonnet": null,
	"seven_day_opus": null,
	"extra_usage": {"is_enabled": true, "monthly_limit": 5000, "used_credits": 0.0},
	"limits": [
		{"kind": "session", "group": "session", "percent": 2, "severity": "normal",
		 "resets_at": "2026-08-03T18:00:00.317099+00:00", "scope": null, "is_active": false},
		{"kind": "weekly_all", "group": "weekly", "percent": 30, "severity": "normal",
		 "resets_at": "2026-08-06T04:00:00.317126+00:00", "scope": null, "is_active": true},
		{"kind": "weekly_scoped", "group": "weekly", "percent": 2, "severity": "normal",
		 "resets_at": "2026-08-06T04:00:00.317421+00:00",
		 "scope": {"model": {"id": null, "display_name": "Fable"}}, "is_active": false}
	],
	"spend": {
		"used": {"amount_minor": 0, "currency": "EUR", "exponent": 2},
		"limit": {"amount_minor": 5000, "currency": "EUR", "exponent": 2},
		"percent": 0, "severity": "normal", "enabled": true
	}
}`

func TestOAuthUsage_ParsesLimitsArray(t *testing.T) {
	srv := serveBody(t, currentBody)
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatalf("OAuthUsage: %v", err)
	}

	// 3 from limits[] plus a synthesized spend limit.
	if len(r.Limits) != 4 {
		t.Fatalf("limits: got %d, want 4: %+v", len(r.Limits), r.Limits)
	}
	if l, ok := r.Session(); !ok || l.Percent != 2 {
		t.Errorf("session: got %+v ok=%v", l, ok)
	}
	if l, ok := r.Weekly(); !ok || l.Percent != 30 {
		t.Errorf("weekly: got %+v ok=%v", l, ok)
	}
	scoped, ok := r.Find("weekly_scoped:Fable")
	if !ok {
		t.Fatal("scoped limit not found")
	}
	if scoped.ScopeModel != "Fable" {
		t.Errorf("scope model: got %q", scoped.ScopeModel)
	}
	if _, ok := r.Find(KindSpend); !ok {
		t.Error("spend limit not synthesized from spend block")
	}

	// PrimaryPercent is simply the highest utilization.
	if r.PrimaryPercent() != 30 {
		t.Errorf("primary: got %v", r.PrimaryPercent())
	}
	if r.IsRateLimited() {
		t.Error("should not be rate limited at 30%")
	}
	if r.ResetsAtFor(KindSession).IsZero() {
		t.Error("session reset should parse from an offset-form timestamp")
	}
	if !r.Spend.Enabled || r.Spend.Currency != "EUR" || r.Spend.LimitMinor != 5000 {
		t.Errorf("spend: got %+v", r.Spend)
	}
}

// Limits the client has never heard of must still flow through untouched: that
// is the whole point of keying off the self-describing array.
func TestOAuthUsage_UnknownLimitKindPassesThrough(t *testing.T) {
	body := `{"limits": [
		{"kind": "weekly_frobnicator", "group": "weekly", "percent": 61,
		 "severity": "warning", "resets_at": "2026-08-06T04:00:00Z", "is_active": true}
	]}`
	srv := serveBody(t, body)
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	l, ok := r.Find("weekly_frobnicator")
	if !ok {
		t.Fatal("unknown kind was dropped")
	}
	if l.Percent != 61 || l.Severity != "warning" {
		t.Errorf("unknown limit: got %+v", l)
	}
	// Unrecognised kinds fall back to the raw kind as their label.
	if l.Label() != "weekly_frobnicator" {
		t.Errorf("label: got %q", l.Label())
	}
	if r.PrimaryPercent() != 61 {
		t.Errorf("primary: got %v", r.PrimaryPercent())
	}
}

// Older/other responses with no limits[] must still work via the flat windows.
func TestOAuthUsage_LegacyFlatFallback(t *testing.T) {
	body := `{
		"five_hour": {"utilization": 23.4, "resets_at": "2026-05-21T15:00:00Z"},
		"seven_day": {"utilization": 67.1, "resets_at": "2026-05-26T09:00:00Z"}
	}`
	srv := serveBody(t, body)
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Limits) != 2 {
		t.Fatalf("limits: got %d, want 2", len(r.Limits))
	}
	if l, _ := r.Session(); l.Percent != 23.4 {
		t.Errorf("session: got %v", l.Percent)
	}
	if l, _ := r.Weekly(); l.Percent != 67.1 {
		t.Errorf("weekly: got %v", l.Percent)
	}
	if r.PrimaryPercent() != 67.1 {
		t.Errorf("primary: got %v", r.PrimaryPercent())
	}
}

func TestIsRateLimited(t *testing.T) {
	body := `{"limits": [
		{"kind": "session", "group": "session", "percent": 100, "is_active": true,
		 "resets_at": "2026-08-03T18:00:00Z"},
		{"kind": "weekly_all", "group": "weekly", "percent": 40,
		 "resets_at": "2026-08-06T04:00:00Z"}
	]}`
	srv := serveBody(t, body)
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsRateLimited() {
		t.Error("a limit at 100% should report rate limited")
	}
}

// Spend must not be double-counted when limits[] already carries it.
func TestSpendNotDuplicated(t *testing.T) {
	body := `{
		"limits": [{"kind": "spend", "group": "spend", "percent": 12, "severity": "normal"}],
		"spend": {
			"used": {"amount_minor": 600, "currency": "EUR", "exponent": 2},
			"limit": {"amount_minor": 5000, "currency": "EUR", "exponent": 2},
			"percent": 12, "severity": "normal", "enabled": true
		}
	}`
	srv := serveBody(t, body)
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range r.Limits {
		if l.Kind == KindSpend {
			n++
		}
	}
	if n != 1 {
		t.Errorf("spend limits: got %d, want 1", n)
	}
}

// Disabled credit spend should not appear as a limit at all.
func TestSpendOmittedWhenDisabled(t *testing.T) {
	body := `{
		"limits": [{"kind": "session", "group": "session", "percent": 5, "is_active": true}],
		"spend": {
			"used": {"amount_minor": 0, "currency": "EUR", "exponent": 2},
			"limit": {"amount_minor": 0, "currency": "EUR", "exponent": 2},
			"percent": 0, "enabled": false
		}
	}`
	srv := serveBody(t, body)
	defer srv.Close()

	c := &Client{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.OAuthUsage(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Find(KindSpend); ok {
		t.Error("disabled spend should not be reported as a limit")
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

func TestSpendSummary(t *testing.T) {
	cases := []struct {
		name string
		in   Spend
		want string
	}{
		{"eur cents", Spend{Enabled: true, Currency: "EUR", DecimalPlaces: 2,
			UsedMinor: 0, LimitMinor: 5000}, "0.00 of 50.00 EUR"},
		{"partial spend", Spend{Enabled: true, Currency: "EUR", DecimalPlaces: 2,
			UsedMinor: 637, LimitMinor: 5000}, "6.37 of 50.00 EUR"},
		{"zero exponent", Spend{Enabled: true, Currency: "JPY", DecimalPlaces: 0,
			UsedMinor: 120, LimitMinor: 5000}, "120 of 5000 JPY"},
		{"no currency", Spend{Enabled: true, DecimalPlaces: 2,
			UsedMinor: 100, LimitMinor: 200}, "1.00 of 2.00"},
		// Disabled or capless spend has nothing meaningful to show; callers fall
		// back to the reset-window text.
		{"disabled", Spend{Enabled: false, LimitMinor: 5000}, ""},
		{"no limit", Spend{Enabled: true, LimitMinor: 0}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Summary(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSpendFromRaw(t *testing.T) {
	s, ok := SpendFromRaw(currentBody)
	if !ok {
		t.Fatal("spend not found in a body that carries it")
	}
	if got := s.Summary(); got != "0.00 of 50.00 EUR" {
		t.Errorf("summary: got %q", got)
	}

	for _, raw := range []string{"", "not json", `{}`, `{"spend":{"enabled":false}}`} {
		if _, ok := SpendFromRaw(raw); ok {
			t.Errorf("SpendFromRaw(%q) should report absent", raw)
		}
	}
}

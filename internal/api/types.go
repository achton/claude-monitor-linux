// Package api implements the Anthropic API client used by the poller.
//
// Three endpoints are exposed:
//
//  1. OAuthUsage (GET /api/oauth/usage) — primary, zero-cost, JSON body.
//  2. Ping (POST /v1/messages, max_tokens=1) — fallback, costs 1 Haiku token,
//     extracts the unified-* rate-limit headers.
//  3. CountTokens (POST /v1/messages/count_tokens) — used only for org-id
//     identification at add-account time.
//
// See docs/DESIGN.md §5.1 for the full architecture.
package api

import (
	"errors"
	"fmt"
	"time"
)

// UsageReading is the normalized result of an OAuthUsage or Ping call.
type UsageReading struct {
	OrganizationID string

	FiveHourPercent float64 // 0–100
	FiveHourReset   time.Time
	FiveHourStatus  string // "" | "allowed" | "allowed_warning" | "rejected"

	SevenDayPercent float64
	SevenDayReset   time.Time
	SevenDayStatus  string

	SevenDaySonnetPercent float64
	SevenDaySonnetReset   time.Time

	OverallStatus string

	HTTPStatus int
	Source     string // "oauth_usage" | "ping"
	RawJSON    string // raw body or header-derived JSON for usage_history.raw_data
}

// PrimaryPercent is max(session, weekly).
func (u UsageReading) PrimaryPercent() float64 {
	if u.FiveHourPercent > u.SevenDayPercent {
		return u.FiveHourPercent
	}
	return u.SevenDayPercent
}

// IsRateLimited returns true if the overall status indicates the account is currently rate-limited.
func (u UsageReading) IsRateLimited() bool { return u.OverallStatus == "rejected" }

// Error types

// ErrUnauthorized signals an invalid or expired token (HTTP 401).
var ErrUnauthorized = errors.New("unauthorized — token may be expired or revoked")

// ErrTooManyRequests signals a 429 from the OAuth Usage endpoint (or a 429 from any endpoint).
type ErrTooManyRequests struct {
	Endpoint   string
	RetryAfter time.Duration // 0 if not provided
}

func (e *ErrTooManyRequests) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("429 from %s; retry-after %s", e.Endpoint, e.RetryAfter)
	}
	return fmt.Sprintf("429 from %s; no retry-after header", e.Endpoint)
}

// ErrHTTP wraps any non-2xx, non-401, non-429 response.
type ErrHTTP struct {
	Endpoint string
	Status   int
	Body     string
}

func (e *ErrHTTP) Error() string {
	return fmt.Sprintf("HTTP %d from %s: %s", e.Status, e.Endpoint, truncate(e.Body, 200))
}

// IsTransient indicates whether a retry might succeed.
func IsTransient(err error) bool {
	var tr *ErrTooManyRequests
	if errors.As(err, &tr) {
		return false // 429 is "transient" in clock-time but not in retry-loop time; we handle separately
	}
	var he *ErrHTTP
	if errors.As(err, &he) {
		return he.Status >= 500 && he.Status < 600
	}
	if errors.Is(err, ErrUnauthorized) {
		return false
	}
	// Network errors are transient by default.
	return err != nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

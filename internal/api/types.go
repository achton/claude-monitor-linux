// Package api implements the Anthropic /api/oauth/usage client.
package api

import (
	"errors"
	"fmt"
	"time"
)

// UsageReading is the normalized result of an OAuthUsage call.
type UsageReading struct {
	FiveHourPercent       float64 // 0–100
	FiveHourReset         time.Time
	SevenDayPercent       float64
	SevenDayReset         time.Time
	SevenDaySonnetPercent float64
	SevenDaySonnetReset   time.Time
	RawJSON               string
}

// PrimaryPercent is max(session, weekly).
func (u UsageReading) PrimaryPercent() float64 {
	if u.FiveHourPercent > u.SevenDayPercent {
		return u.FiveHourPercent
	}
	return u.SevenDayPercent
}

// IsRateLimited returns true when either dimension hit 100%.
func (u UsageReading) IsRateLimited() bool {
	return u.FiveHourPercent >= 100 || u.SevenDayPercent >= 100
}

// ErrUnauthorized signals an invalid or expired token (HTTP 401).
var ErrUnauthorized = errors.New("unauthorized — token may be expired or revoked")

// ErrHTTP wraps any non-2xx, non-401 response.
type ErrHTTP struct {
	Status int
	Body   string
}

func (e *ErrHTTP) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, truncate(e.Body, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

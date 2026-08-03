// Package api implements the Anthropic /api/oauth/usage client.
package api

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Limit kinds the API reports today. The set is open — anything unrecognised
// still flows through to the store and UI, which is the point of keying off
// the self-describing limits array rather than fixed field names.
const (
	KindSession      = "session"
	KindWeeklyAll    = "weekly_all"
	KindWeeklyScoped = "weekly_scoped"
	KindSpend        = "spend"
)

// Limit groups. Used to order and cluster limits for display.
const (
	GroupSession = "session"
	GroupWeekly  = "weekly"
	GroupSpend   = "spend"
)

// Limit is one usage constraint reported by /api/oauth/usage.
type Limit struct {
	Kind       string    // e.g. "session", "weekly_all", "weekly_scoped", "spend"
	Group      string    // e.g. "session", "weekly", "spend"
	Percent    float64   // 0–100
	Severity   string    // e.g. "normal", "warning"
	ResetsAt   time.Time // zero when the limit has no reset (e.g. spend)
	IsActive   bool      // raw passthrough of the API's flag; not used for display
	ScopeModel string    // e.g. "Fable"; empty when the limit is unscoped
}

// Key is a stable identifier for one limit across polls. Notification debounce
// and chart series are keyed on it, so a scoped limit must not collide with the
// unscoped limit of the same kind.
func (l Limit) Key() string {
	if l.ScopeModel != "" {
		return l.Kind + ":" + l.ScopeModel
	}
	return l.Kind
}

// Label is the human-readable name shown in the UI, tray and CLI.
func (l Limit) Label() string {
	base := ""
	switch l.Kind {
	case KindSession:
		base = "Session (5h)"
	case KindWeeklyAll:
		base = "Weekly (7d)"
	case KindWeeklyScoped:
		base = "Weekly"
	case KindSpend:
		base = "Extra usage"
	default:
		base = l.Kind
	}
	if l.ScopeModel != "" {
		return base + " · " + l.ScopeModel
	}
	return base
}

// Spend describes the extra-usage credit balance, which is reported in money
// rather than as a percentage and so needs its own shape for display.
type Spend struct {
	Enabled       bool
	Currency      string
	DecimalPlaces int
	UsedMinor     int64
	LimitMinor    int64
	Percent       float64
}

// Summary renders the credit balance as money, e.g. "0.00 of 50.00 EUR".
// Empty when spend is disabled or carries no limit, so callers can fall back.
func (s Spend) Summary() string {
	if !s.Enabled || s.LimitMinor <= 0 {
		return ""
	}
	cur := s.Currency
	if cur != "" {
		cur = " " + cur
	}
	return fmt.Sprintf("%s of %s%s",
		minorToString(s.UsedMinor, s.DecimalPlaces),
		minorToString(s.LimitMinor, s.DecimalPlaces),
		cur)
}

// minorToString renders a minor-unit amount using the currency's exponent, so
// EUR cents (exponent 2) print as "50.00" and a zero-exponent currency as "50".
func minorToString(minor int64, exponent int) string {
	if exponent <= 0 {
		return strconv.FormatInt(minor, 10)
	}
	div := int64(1)
	for i := 0; i < exponent; i++ {
		div *= 10
	}
	neg := ""
	if minor < 0 {
		neg = "-"
		minor = -minor
	}
	return fmt.Sprintf("%s%d.%0*d", neg, minor/div, exponent, minor%div)
}

// UsageReading is the normalized result of an OAuthUsage call.
type UsageReading struct {
	Limits  []Limit
	Spend   Spend
	RawJSON string
}

// Find returns the limit with the given key.
func (u UsageReading) Find(key string) (Limit, bool) {
	for _, l := range u.Limits {
		if l.Key() == key {
			return l, true
		}
	}
	return Limit{}, false
}

// ResetsAtFor returns the reset time for a limit key, or the zero time when the
// limit is absent or has no reset window.
func (u UsageReading) ResetsAtFor(key string) time.Time {
	if l, ok := u.Find(key); ok {
		return l.ResetsAt
	}
	return time.Time{}
}

// Session returns the 5-hour session limit.
func (u UsageReading) Session() (Limit, bool) { return u.Find(KindSession) }

// Weekly returns the all-model weekly limit.
func (u UsageReading) Weekly() (Limit, bool) { return u.Find(KindWeeklyAll) }

// PrimaryPercent is the highest utilization across all reported limits. It backs
// the CLI exit code, which answers "how close am I to any limit".
func (u UsageReading) PrimaryPercent() float64 {
	high := 0.0
	for _, l := range u.Limits {
		if l.Percent > high {
			high = l.Percent
		}
	}
	return high
}

// IsRateLimited returns true when any reported limit is exhausted.
func (u UsageReading) IsRateLimited() bool {
	for _, l := range u.Limits {
		if l.Percent >= 100 {
			return true
		}
	}
	return false
}

// SortLimits orders limits for stable display: session, then weekly, then
// spend, then anything unrecognised; unscoped before scoped within a group.
func SortLimits(limits []Limit) {
	rank := map[string]int{GroupSession: 0, GroupWeekly: 1, GroupSpend: 2}
	groupRank := func(g string) int {
		if r, ok := rank[g]; ok {
			return r
		}
		return 3
	}
	sort.SliceStable(limits, func(i, j int) bool {
		a, b := limits[i], limits[j]
		if ga, gb := groupRank(a.Group), groupRank(b.Group); ga != gb {
			return ga < gb
		}
		if (a.ScopeModel == "") != (b.ScopeModel == "") {
			return a.ScopeModel == ""
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ScopeModel < b.ScopeModel
	})
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

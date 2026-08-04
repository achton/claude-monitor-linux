package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UserAgent is sent to the usage endpoint, which is version-sensitive. The
// Makefile overrides it at build time via -ldflags (see USER_AGENT); this
// default keeps `go build` and `go test` working on their own.
var UserAgent = "claude-code/2.0.37"

const (
	defaultTimeout = 15 * time.Second
	anthropicBeta  = "oauth-2025-04-20"
)

// Client is an Anthropic /api/oauth/usage client.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string // typically "https://api.anthropic.com"
}

// NewClient returns a Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		BaseURL:    "https://api.anthropic.com",
	}
}

// OAuthUsage calls GET /api/oauth/usage and parses the JSON body.
// Returns ErrUnauthorized on 401.
func (c *Client) OAuthUsage(ctx context.Context, token string) (UsageReading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/oauth/usage", nil)
	if err != nil {
		return UsageReading{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", anthropicBeta)
	req.Header.Set("User-Agent", UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return UsageReading{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return UsageReading{}, ErrUnauthorized
	default:
		return UsageReading{}, &ErrHTTP{Status: resp.StatusCode, Body: string(body)}
	}

	var parsed oauthUsageBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return UsageReading{RawJSON: string(body)}, fmt.Errorf("decode oauth_usage body: %w", err)
	}

	r := UsageReading{RawJSON: string(body)}
	r.Limits = parsed.limits()
	r.Spend = parsed.spend()
	if l, ok := parsed.spendLimit(); ok {
		r.Limits = append(r.Limits, l)
	}
	SortLimits(r.Limits)
	return r, nil
}

// SpendFromRaw extracts the credit-spend block from a stored raw response body.
// Spend is money rather than a percentage, so it is read back from raw_data for
// display instead of being denormalised into its own columns; its percentage is
// already tracked as a limit and carries the history.
func SpendFromRaw(raw string) (Spend, bool) {
	if raw == "" {
		return Spend{}, false
	}
	var parsed oauthUsageBody
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return Spend{}, false
	}
	s := parsed.spend()
	return s, s.Enabled
}

type oauthUsageBody struct {
	// The self-describing surface. Preferred: it names its own kinds, so a
	// limit Anthropic adds or renames flows through without a code change.
	Limits []oauthLimit `json:"limits"`

	// Legacy flat windows, used only when limits[] is absent.
	FiveHour *oauthUsageWindow `json:"five_hour"`
	SevenDay *oauthUsageWindow `json:"seven_day"`

	Spend *oauthSpend `json:"spend"`
}

type oauthLimit struct {
	Kind     string  `json:"kind"`
	Group    string  `json:"group"`
	Percent  float64 `json:"percent"`
	Severity string  `json:"severity"`
	ResetsAt string  `json:"resets_at"`
	IsActive bool    `json:"is_active"`
	Scope    *struct {
		Model *struct {
			ID          *string `json:"id"`
			DisplayName string  `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

type oauthUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type oauthMoney struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Exponent    int    `json:"exponent"`
}

type oauthSpend struct {
	Used     oauthMoney `json:"used"`
	Limit    oauthMoney `json:"limit"`
	Percent  float64    `json:"percent"`
	Severity string     `json:"severity"`
	Enabled  bool       `json:"enabled"`
}

// limits converts the response into normalized Limits, preferring the
// self-describing array and falling back to the flat windows.
func (b oauthUsageBody) limits() []Limit {
	if len(b.Limits) > 0 {
		out := make([]Limit, 0, len(b.Limits))
		for _, l := range b.Limits {
			out = append(out, Limit{
				Kind:       l.Kind,
				Group:      l.Group,
				Percent:    l.Percent,
				Severity:   l.Severity,
				ResetsAt:   parseTime(l.ResetsAt),
				IsActive:   l.IsActive,
				ScopeModel: l.scopeModel(),
			})
		}
		return out
	}

	var out []Limit
	if b.FiveHour != nil {
		out = append(out, Limit{
			Kind: KindSession, Group: GroupSession,
			Percent: b.FiveHour.Utilization, Severity: "normal",
			ResetsAt: parseTime(b.FiveHour.ResetsAt),
		})
	}
	if b.SevenDay != nil {
		out = append(out, Limit{
			Kind: KindWeeklyAll, Group: GroupWeekly,
			Percent: b.SevenDay.Utilization, Severity: "normal",
			ResetsAt: parseTime(b.SevenDay.ResetsAt),
		})
	}
	return out
}

func (l oauthLimit) scopeModel() string {
	if l.Scope == nil || l.Scope.Model == nil {
		return ""
	}
	return l.Scope.Model.DisplayName
}

func (b oauthUsageBody) spend() Spend {
	if b.Spend == nil {
		return Spend{}
	}
	return Spend{
		Enabled:       b.Spend.Enabled,
		Currency:      b.Spend.Limit.Currency,
		DecimalPlaces: b.Spend.Limit.Exponent,
		UsedMinor:     b.Spend.Used.AmountMinor,
		LimitMinor:    b.Spend.Limit.AmountMinor,
		Percent:       b.Spend.Percent,
	}
}

// spendLimit exposes credit spend as a Limit so it gets history and threshold
// alerting like any other constraint. Omitted when spend is disabled, and when
// limits[] already carries a spend entry of its own.
func (b oauthUsageBody) spendLimit() (Limit, bool) {
	if b.Spend == nil || !b.Spend.Enabled || b.Spend.Limit.AmountMinor <= 0 {
		return Limit{}, false
	}
	for _, l := range b.Limits {
		if l.Kind == KindSpend {
			return Limit{}, false
		}
	}
	severity := b.Spend.Severity
	if severity == "" {
		severity = "normal"
	}
	return Limit{
		Kind:     KindSpend,
		Group:    GroupSpend,
		Percent:  b.Spend.Percent,
		Severity: severity,
	}, true
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

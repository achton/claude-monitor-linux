package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// User-Agent updated at build time via -ldflags. Set to a recent claude-code version.
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

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

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

	r := UsageReading{
		FiveHourPercent: parsed.FiveHour.Utilization,
		FiveHourReset:   parseTime(parsed.FiveHour.ResetsAt),
		SevenDayPercent: parsed.SevenDay.Utilization,
		SevenDayReset:   parseTime(parsed.SevenDay.ResetsAt),
		RawJSON:         string(body),
	}
	if parsed.SevenDaySonnet != nil {
		r.SevenDaySonnetPercent = parsed.SevenDaySonnet.Utilization
		r.SevenDaySonnetReset = parseTime(parsed.SevenDaySonnet.ResetsAt)
	}
	return r, nil
}

type oauthUsageBody struct {
	FiveHour       oauthUsageWindow  `json:"five_hour"`
	SevenDay       oauthUsageWindow  `json:"seven_day"`
	SevenDaySonnet *oauthUsageWindow `json:"seven_day_sonnet,omitempty"`
}

type oauthUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
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

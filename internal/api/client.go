package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// User-Agent updated at build time via -ldflags. Set to a recent claude-code version.
var UserAgent = "claude-code/2.0.37"

const (
	defaultTimeout = 15 * time.Second
	anthropicBeta  = "oauth-2025-04-20"
)

// Client is an Anthropic API client.
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
// Returns ErrTooManyRequests on 429, ErrUnauthorized on 401.
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
		// fall through
	case http.StatusUnauthorized:
		return UsageReading{HTTPStatus: 401, Source: "oauth_usage"}, ErrUnauthorized
	case http.StatusTooManyRequests:
		return UsageReading{HTTPStatus: 429, Source: "oauth_usage"},
			&ErrTooManyRequests{Endpoint: "oauth_usage", RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	default:
		return UsageReading{HTTPStatus: resp.StatusCode, Source: "oauth_usage"},
			&ErrHTTP{Endpoint: "oauth_usage", Status: resp.StatusCode, Body: string(body)}
	}

	var parsed oauthUsageBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return UsageReading{HTTPStatus: 200, Source: "oauth_usage", RawJSON: string(body)},
			fmt.Errorf("decode oauth_usage body: %w", err)
	}

	r := UsageReading{
		OrganizationID:  strings.TrimSpace(resp.Header.Get("anthropic-organization-id")),
		FiveHourPercent: parsed.FiveHour.Utilization,
		FiveHourReset:   parseTime(parsed.FiveHour.ResetsAt),
		SevenDayPercent: parsed.SevenDay.Utilization,
		SevenDayReset:   parseTime(parsed.SevenDay.ResetsAt),
		HTTPStatus:      200,
		Source:          "oauth_usage",
		RawJSON:         string(body),
	}
	if parsed.SevenDaySonnet != nil {
		r.SevenDaySonnetPercent = parsed.SevenDaySonnet.Utilization
		r.SevenDaySonnetReset = parseTime(parsed.SevenDaySonnet.ResetsAt)
	}
	// The OAuth Usage endpoint doesn't expose granular status fields; we treat
	// a successful 200 as "allowed" by default.
	r.FiveHourStatus = "allowed"
	r.SevenDayStatus = "allowed"
	r.OverallStatus = "allowed"
	return r, nil
}

// Ping calls POST /v1/messages with a minimal Haiku inference (1 output token).
// Both 200 and 429 responses include unified-* rate-limit headers, which we parse.
// Returns ErrUnauthorized on 401.
func (c *Client) Ping(ctx context.Context, token string) (UsageReading, error) {
	body := []byte(`{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return UsageReading{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", anthropicBeta)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return UsageReading{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))

	if resp.StatusCode == 401 {
		return UsageReading{HTTPStatus: 401, Source: "ping"}, ErrUnauthorized
	}
	if resp.StatusCode != 200 && resp.StatusCode != 429 {
		return UsageReading{HTTPStatus: resp.StatusCode, Source: "ping"},
			&ErrHTTP{Endpoint: "ping", Status: resp.StatusCode, Body: string(respBody)}
	}

	// Headers we care about.
	r := UsageReading{
		OrganizationID:  strings.TrimSpace(resp.Header.Get("anthropic-organization-id")),
		FiveHourPercent: 100 * parseFloat(resp.Header.Get("anthropic-ratelimit-unified-5h-utilization")),
		FiveHourReset:   parseEpoch(resp.Header.Get("anthropic-ratelimit-unified-5h-reset")),
		FiveHourStatus:  resp.Header.Get("anthropic-ratelimit-unified-5h-status"),
		SevenDayPercent: 100 * parseFloat(resp.Header.Get("anthropic-ratelimit-unified-7d-utilization")),
		SevenDayReset:   parseEpoch(resp.Header.Get("anthropic-ratelimit-unified-7d-reset")),
		SevenDayStatus:  resp.Header.Get("anthropic-ratelimit-unified-7d-status"),
		OverallStatus:   resp.Header.Get("anthropic-ratelimit-unified-status"),
		HTTPStatus:      resp.StatusCode,
		Source:          "ping",
	}

	// Synthesize a small RawJSON for the usage_history.raw_data column.
	rawJSON, _ := json.Marshal(map[string]any{
		"ping_status":     resp.StatusCode,
		"session_status":  r.FiveHourStatus,
		"weekly_status":   r.SevenDayStatus,
		"overall_status":  r.OverallStatus,
	})
	r.RawJSON = string(rawJSON)
	return r, nil
}

// CountTokens calls /v1/messages/count_tokens for the sole purpose of extracting
// the anthropic-organization-id header at add-account time. Zero quota cost.
func (c *Client) CountTokens(ctx context.Context, token string) (string, error) {
	body := []byte(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"x"}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages/count_tokens", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20,token-counting-2024-11-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
	if resp.StatusCode == 401 {
		return "", ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &ErrHTTP{Endpoint: "count_tokens", Status: resp.StatusCode, Body: string(respBody)}
	}
	org := strings.TrimSpace(resp.Header.Get("anthropic-organization-id"))
	if org == "" {
		return "", &ErrHTTP{Endpoint: "count_tokens", Status: resp.StatusCode, Body: "no anthropic-organization-id header"}
	}
	return org, nil
}

// ---- internal types ----

type oauthUsageBody struct {
	FiveHour       oauthUsageWindow  `json:"five_hour"`
	SevenDay       oauthUsageWindow  `json:"seven_day"`
	SevenDaySonnet *oauthUsageWindow `json:"seven_day_sonnet,omitempty"`
}

type oauthUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseEpoch(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	sec, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0).UTC()
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

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

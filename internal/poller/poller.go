// Package poller implements the OAuth polling loop with the tri-endpoint
// architecture described in docs/DESIGN.md §5.1, §5.3.
package poller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/log"
	"github.com/achton/claude-monitor-linux/internal/notify"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// Poller owns the polling state for an active session.
type Poller struct {
	Store      *store.Store
	API        *api.Client
	Evaluator  *notify.Evaluator // optional; nil disables notification eval
	Adaptive   bool              // adaptive throttling per §5.3.3
	BaseSecond int               // base poll interval in seconds (e.g. 600)

	// lastPolled tracks per-credential last-poll wall time across ticks.
	lastPolled map[int64]time.Time
	// suppressFirst marks credentials whose first-poll evaluation should be skipped.
	suppressFirst map[int64]bool
}

// New creates a Poller with sensible defaults.
func New(s *store.Store, c *api.Client, ev *notify.Evaluator, base int, adaptive bool) *Poller {
	if base <= 0 {
		base = 600
	}
	return &Poller{
		Store:         s,
		API:           c,
		Evaluator:     ev,
		Adaptive:      adaptive,
		BaseSecond:    base,
		lastPolled:    map[int64]time.Time{},
		suppressFirst: map[int64]bool{},
	}
}

// PollAll polls every active credential, returning the number of accounts polled
// successfully. Errors per-account are logged but don't abort the loop.
func (p *Poller) PollAll(ctx context.Context) (int, error) {
	creds, err := p.Store.ListActiveCredentials(ctx)
	if err != nil {
		return 0, err
	}
	ok := 0
	for _, c := range creds {
		if _, isFirst := p.lastPolled[c.ID]; !isFirst {
			p.suppressFirst[c.ID] = true
		}
		if err := p.pollOne(ctx, c); err != nil {
			log.Logger().Warn("poll failed", slog.String("label", c.Label), slog.String("err", err.Error()))
			continue
		}
		ok++
		p.lastPolled[c.ID] = time.Now()
	}
	return ok, nil
}

// PollDue polls only those credentials whose adaptive-adjusted interval has elapsed.
func (p *Poller) PollDue(ctx context.Context) (int, error) {
	creds, err := p.Store.ListActiveCredentials(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	ok := 0
	for _, c := range creds {
		last, has := p.lastPolled[c.ID]
		interval := p.intervalFor(ctx, c)
		if has && now.Sub(last) < interval {
			continue
		}
		if !has {
			p.suppressFirst[c.ID] = true
		}
		if err := p.pollOne(ctx, c); err != nil {
			log.Logger().Warn("poll failed", slog.String("label", c.Label), slog.String("err", err.Error()))
			continue
		}
		ok++
		p.lastPolled[c.ID] = time.Now()
	}
	return ok, nil
}

// PollAccount polls a single account by id immediately.
func (p *Poller) PollAccount(ctx context.Context, accountID string) error {
	creds, err := p.Store.ListActiveCredentials(ctx)
	if err != nil {
		return err
	}
	for _, c := range creds {
		if c.AccountID.Valid && c.AccountID.String == accountID {
			return p.pollOne(ctx, c)
		}
	}
	return fmt.Errorf("no active credential for account %s", accountID)
}

// pollOne runs the tri-endpoint state machine for one credential and writes results.
func (p *Poller) pollOne(ctx context.Context, c store.Credential) error {
	if !c.AccessToken.Valid || c.AccessToken.String == "" {
		return errors.New("missing access token")
	}
	token := c.AccessToken.String
	accountID := c.AccountID.String
	if accountID == "" {
		return errors.New("credential has no account_id")
	}

	r, source, newState, attempts, err := p.fetch(ctx, c, token)
	if err != nil {
		_ = p.Store.UpdateCredentialPollState(ctx, c.ID, err.Error(), newState, attempts)
		return err
	}
	_ = source

	if err := p.writeReading(ctx, accountID, r); err != nil {
		_ = p.Store.UpdateCredentialPollState(ctx, c.ID, err.Error(), newState, attempts)
		return err
	}
	_ = p.Store.UpdateCredentialPollState(ctx, c.ID, "", newState, attempts)

	// Notification evaluation.
	if p.Evaluator != nil && !p.suppressFirst[c.ID] {
		_ = p.Evaluator.EvaluateReading(ctx, accountID, c.Label, r)
	}
	p.suppressFirst[c.ID] = false
	return nil
}

// fetch applies the per-account state machine and returns a UsageReading.
func (p *Poller) fetch(ctx context.Context, c store.Credential, token string) (api.UsageReading, string, string, int, error) {
	state := c.UsageEndpointState
	attempts := c.UsageEndpointAttempts

	// If in backoff window, route to Ping.
	if strings.HasPrefix(state, "backoff:") {
		unix, _ := strconv.ParseInt(strings.TrimPrefix(state, "backoff:"), 10, 64)
		if time.Now().Before(time.Unix(unix, 0)) {
			r, err := p.API.Ping(ctx, token)
			return r, "ping", state, attempts, err
		}
		// Backoff window has elapsed — try OAuth Usage again.
	}

	if state == "disabled" {
		r, err := p.API.Ping(ctx, token)
		return r, "ping", state, attempts, err
	}

	// Try OAuth Usage (primary).
	r, err := p.API.OAuthUsage(ctx, token)
	if err == nil {
		return r, "oauth_usage", "healthy", 0, nil
	}

	var tr *api.ErrTooManyRequests
	if errors.As(err, &tr) {
		// Move to backoff.
		newAttempts := attempts + 1
		newState := nextBackoffState(newAttempts)
		log.Logger().Warn("oauth_usage 429; backing off",
			slog.String("label", c.Label),
			slog.Int("attempts", newAttempts),
			slog.String("new_state", newState))
		r2, err2 := p.API.Ping(ctx, token)
		return r2, "ping", newState, newAttempts, err2
	}

	if errors.Is(err, api.ErrUnauthorized) {
		// Don't fall through — token is bad.
		return r, "oauth_usage", state, attempts, err
	}

	// Other HTTP/network errors: try Ping as a fallback.
	log.Logger().Warn("oauth_usage error; trying ping",
		slog.String("label", c.Label), slog.String("err", err.Error()))
	r2, err2 := p.API.Ping(ctx, token)
	return r2, "ping", state, attempts, err2
}

func nextBackoffState(attempts int) string {
	// 15m → 30m → 1h → 2h → 4h, then 'disabled'.
	steps := []time.Duration{
		15 * time.Minute,
		30 * time.Minute,
		1 * time.Hour,
		2 * time.Hour,
		4 * time.Hour,
	}
	if attempts > len(steps) {
		return "disabled"
	}
	d := steps[attempts-1]
	return fmt.Sprintf("backoff:%d", time.Now().Add(d).Unix())
}

// writeReading persists one reading, applying reset detection in BEGIN IMMEDIATE.
func (p *Poller) writeReading(ctx context.Context, accountID string, r api.UsageReading) error {
	return p.Store.WithTx(ctx, func(tx *sql.Tx) error {
		prev, err := p.Store.LatestUsageInTx(ctx, tx, accountID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		// Reset detection: weekly dropped by >5%.
		if !errors.Is(err, sql.ErrNoRows) &&
			prev.WeeklyAllPercent.Valid &&
			prev.WeeklyAllPercent.Float64-r.SevenDayPercent > 5 {
			has, err := p.Store.HasRecentSynthetic(ctx, tx, accountID, time.Minute)
			if err != nil {
				return err
			}
			if !has {
				mid := time.Now().Add(-time.Second)
				_ = p.Store.InsertSyntheticUsage(ctx, tx, accountID, mid,
					prev.PrimaryPercent.Float64, prev.SessionPercent.Float64,
					prev.WeeklyAllPercent.Float64, valOrZero(prev.WeeklySonnetPercent))
				_ = p.Store.InsertSyntheticUsage(ctx, tx, accountID, time.Now(),
					0, 0, 0, 0)
			}
		}

		sessionResetISO := ""
		if !r.FiveHourReset.IsZero() {
			sessionResetISO = r.FiveHourReset.UTC().Format(time.RFC3339Nano)
		}
		weeklyResetISO := ""
		if !r.SevenDayReset.IsZero() {
			weeklyResetISO = r.SevenDayReset.UTC().Format(time.RFC3339Nano)
		}
		if err := p.Store.InsertUsageReading(ctx, tx, accountID,
			r.PrimaryPercent(), r.FiveHourPercent, r.SevenDayPercent, r.SevenDaySonnetPercent,
			sessionResetISO, weeklyResetISO, r.RawJSON, r.Source); err != nil {
			return err
		}
		return p.Store.TouchAccountLastUpdated(ctx, tx, accountID)
	})
}

func valOrZero(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}

// intervalFor returns the adaptive-adjusted poll interval for a credential.
func (p *Poller) intervalFor(ctx context.Context, c store.Credential) time.Duration {
	base := time.Duration(p.BaseSecond) * time.Second
	if !p.Adaptive || !c.AccountID.Valid {
		return base
	}
	rec, err := p.Store.LatestUsage(ctx, c.AccountID.String)
	if err != nil || !rec.PrimaryPercent.Valid {
		return base
	}
	pct := rec.PrimaryPercent.Float64
	switch {
	case pct >= 95:
		return base * 4
	case pct >= 90:
		return base * 2
	default:
		return base
	}
}

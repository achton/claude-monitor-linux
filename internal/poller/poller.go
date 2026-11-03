// Package poller polls /api/oauth/usage on a fixed cadence, reading the live
// access token from Claude Code's credentials file on every call. Single
// account by construction — Claude Code only holds one at a time.
package poller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	cmlog "github.com/achton/claude-monitor-linux/internal/log"
	"github.com/achton/claude-monitor-linux/internal/notify"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// Poller is the single-account live-read poll engine.
type Poller struct {
	Store     *store.Store
	API       *api.Client
	Evaluator *notify.Evaluator // optional; nil disables notification eval

	// ConfigDir is Claude Code's config directory; see ClaudeConfigDir.
	ConfigDir string
	// Refresher refreshes an expired access token. Optional.
	Refresher *Refresher

	// pollMu serializes PollNow, so concurrent callers never race on an
	// expired token.
	pollMu sync.Mutex

	mu               sync.Mutex
	rateLimitedUntil time.Time
	refreshErr       string
	lastError        string
	lastAttempt      time.Time
	lastSuccess      time.Time
	label            string

	suppressFirstNotify bool
}

// New creates a Poller. The first call to PollNow suppresses notification
// evaluation so we don't fire on launch.
func New(s *store.Store, c *api.Client, ev *notify.Evaluator) *Poller {
	return &Poller{Store: s, API: c, Evaluator: ev, suppressFirstNotify: true}
}

// Status returns a snapshot of poller state for the UI and CLI status.
func (p *Poller) Status() (label, lastError string, lastAttempt, lastSuccess time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.label, p.lastError, p.lastAttempt, p.lastSuccess
}

// defaultRateLimitBackoff applies when a 429 carries no Retry-After.
const defaultRateLimitBackoff = 5 * time.Minute

// RateLimitedError reports that polls are paused until Until, after the usage
// endpoint answered 429.
type RateLimitedError struct{ Until time.Time }

func (e *RateLimitedError) Error() string {
	return "usage endpoint rate-limited until " + e.Until.Local().Format("15:04")
}

// AccountLabel names the account, e.g. "Claude Team · Max 5x": from the last
// poll, else read from the credentials file.
func (p *Poller) AccountLabel() string {
	p.mu.Lock()
	label := p.label
	p.mu.Unlock()
	if label != "" {
		return label
	}
	if creds, err := ReadClaudeCodeCredentials(p.ConfigDir); err == nil {
		return creds.Label
	}
	return "Claude Code"
}

// RateLimitedUntil returns when polling resumes, or the zero time when it is
// not paused.
func (p *Poller) RateLimitedUntil() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.rateLimitedUntil) {
		return p.rateLimitedUntil
	}
	return time.Time{}
}

// PollNow runs one poll synchronously. Safe to call from multiple goroutines.
// While the endpoint is rate-limiting, it returns a RateLimitedError without
// sending a request.
func (p *Poller) PollNow(ctx context.Context) error {
	p.pollMu.Lock()
	defer p.pollMu.Unlock()

	if until := p.RateLimitedUntil(); !until.IsZero() {
		return &RateLimitedError{Until: until}
	}
	p.mu.Lock()
	p.lastAttempt = time.Now()
	p.mu.Unlock()

	creds, err := ReadClaudeCodeCredentials(p.ConfigDir)
	if err != nil {
		p.setError("no Claude Code credentials: " + err.Error())
		return err
	}
	if p.Evaluator != nil {
		_ = p.Evaluator.EvaluateSignIn(ctx, creds.Label, creds.SignInExpiresAt)
	}

	refreshed := false
	if creds.Expired() {
		creds, refreshed = p.refresh(ctx, creds)
	}
	r, err := p.API.OAuthUsage(ctx, creds.Token)
	if errors.Is(err, api.ErrUnauthorized) && !refreshed {
		if fresh, ok := p.refresh(ctx, creds); ok {
			creds = fresh
			r, err = p.API.OAuthUsage(ctx, creds.Token)
		}
	}
	var rl *api.ErrRateLimited
	if errors.As(err, &rl) {
		wait := rl.RetryAfter
		if wait <= 0 {
			wait = defaultRateLimitBackoff
		}
		rerr := &RateLimitedError{Until: time.Now().Add(wait)}
		p.mu.Lock()
		p.rateLimitedUntil = rerr.Until
		p.mu.Unlock()
		p.setError(rerr.Error())
		return rerr
	}
	if err != nil {
		p.setError(p.describePollError(err, creds))
		return err
	}

	if err := p.writeReading(ctx, r); err != nil {
		p.setError("db write: " + err.Error())
		return err
	}

	p.mu.Lock()
	p.label = creds.Label
	p.lastError = ""
	p.refreshErr = ""
	p.lastSuccess = time.Now()
	suppress := p.suppressFirstNotify
	p.suppressFirstNotify = false
	p.mu.Unlock()

	if p.Evaluator != nil && !suppress {
		_ = p.Evaluator.EvaluateReading(ctx, creds.Label, r)
	}
	return nil
}

// refresh asks Claude Code for a new access token and rereads the credentials.
// ok is false when no new token came of it.
func (p *Poller) refresh(ctx context.Context, old Credentials) (Credentials, bool) {
	if p.Refresher == nil || old.SignInExpired() {
		return old, false
	}
	err := p.Refresher.Refresh(ctx)
	// Reread even when throttled: an earlier refresh may have finished since.
	if creds, rerr := ReadClaudeCodeCredentials(p.ConfigDir); rerr == nil && creds.Token != old.Token {
		p.setRefreshErr("")
		cmlog.Logger().Info("access token refreshed via claude")
		return creds, true
	}
	switch {
	case errors.Is(err, ErrRefreshThrottled):
		return old, false
	case err == nil:
		err = errors.New("claude auth status left the token unchanged")
	}
	p.setRefreshErr(err.Error())
	cmlog.Logger().Warn("token refresh via claude failed", "err", err)
	return old, false
}

func (p *Poller) setRefreshErr(msg string) {
	p.mu.Lock()
	p.refreshErr = msg
	p.mu.Unlock()
}

// describePollError turns a bare 401 into something actionable: the usual cause
// is an access token Claude Code has not refreshed yet, not a revoked account.
func (p *Poller) describePollError(err error, creds Credentials) string {
	if !errors.Is(err, api.ErrUnauthorized) {
		return err.Error()
	}
	if creds.SignInExpired() {
		return "Claude Code sign-in expired: run `claude /login`"
	}
	if !creds.Expired() {
		return err.Error()
	}
	msg := fmt.Sprintf("Claude Code access token expired %s ago: run any Claude Code command to refresh it",
		time.Since(creds.ExpiresAt).Round(time.Minute))
	p.mu.Lock()
	refreshErr := p.refreshErr
	p.mu.Unlock()
	if refreshErr != "" {
		msg += " (automatic refresh failed: " + refreshErr + ")"
	}
	return msg
}

// setError records the failure and logs it. Logging here rather than only in
// pollLoop means a poll triggered from the UI or over D-Bus is diagnosable too;
// previously those failed silently and only surfaced as a banner.
func (p *Poller) setError(msg string) {
	p.mu.Lock()
	p.lastError = msg
	p.mu.Unlock()
	cmlog.Logger().Warn("poll failed", "err", msg)
}

// writeReading persists one reading, applying reset detection so the chart
// shows a clean cliff rather than a diagonal slide across the rollover.
func (p *Poller) writeReading(ctx context.Context, r api.UsageReading) error {
	return p.Store.WithTx(ctx, func(tx *sql.Tx) error {
		prev, err := p.Store.LatestReadingInTx(ctx, tx)
		hasPrev := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		if dropped := resetKeys(prev, r); hasPrev && len(dropped) > 0 {
			has, err := p.Store.HasRecentSynthetic(ctx, tx, time.Minute)
			if err != nil {
				return err
			}
			if !has {
				now := time.Now()
				if _, err := p.Store.InsertReading(ctx, tx,
					now.Add(-time.Second), prev.Limits, "", true); err != nil {
					return err
				}
				if _, err := p.Store.InsertReading(ctx, tx,
					now, rolledOver(prev.Limits, dropped), "", true); err != nil {
					return err
				}
			}
		}

		_, err = p.Store.InsertReading(ctx, tx, time.Now(), r.Limits, r.RawJSON, false)
		return err
	})
}

// resetKeys returns the keys of limits that dropped sharply since the previous
// reading, meaning their window rolled over. Per key, so a session rollover and
// a weekly rollover are detected independently.
func resetKeys(prev store.Reading, r api.UsageReading) map[string]bool {
	dropped := map[string]bool{}
	for _, pl := range prev.Limits {
		cur, ok := r.Find(pl.Key())
		if !ok {
			continue
		}
		if pl.Percent-cur.Percent > 5 {
			dropped[pl.Key()] = true
		}
	}
	return dropped
}

// rolledOver builds the second reset anchor: limits whose window rolled over
// drop to zero, and every other limit holds its previous value.
//
// Zeroing everything here is what made a session rollover draw a fabricated
// cliff down to 0% and back on the weekly line, which shares the anchor rows.
func rolledOver(limits []api.Limit, dropped map[string]bool) []api.Limit {
	out := make([]api.Limit, 0, len(limits))
	for _, l := range limits {
		if dropped[l.Key()] {
			l.Percent = 0
		}
		l.IsActive = false
		out = append(out, l)
	}
	return out
}

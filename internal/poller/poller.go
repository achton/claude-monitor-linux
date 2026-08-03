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

	mu          sync.Mutex
	lastError   string
	lastAttempt time.Time
	lastSuccess time.Time
	label       string

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

// PollNow runs one poll synchronously. Safe to call from multiple goroutines.
func (p *Poller) PollNow(ctx context.Context) error {
	p.mu.Lock()
	p.lastAttempt = time.Now()
	p.mu.Unlock()

	creds, err := ReadClaudeCodeCredentials("")
	if err != nil {
		p.setError("no Claude Code credentials: " + err.Error())
		return err
	}
	r, err := p.API.OAuthUsage(ctx, creds.Token)
	if err != nil {
		p.setError(describePollError(err, creds))
		return err
	}

	if err := p.writeReading(ctx, r); err != nil {
		p.setError("db write: " + err.Error())
		return err
	}

	p.mu.Lock()
	p.label = creds.Label
	p.lastError = ""
	p.lastSuccess = time.Now()
	suppress := p.suppressFirstNotify
	p.suppressFirstNotify = false
	p.mu.Unlock()

	if p.Evaluator != nil && !suppress {
		_ = p.Evaluator.EvaluateReading(ctx, creds.Label, r)
	}
	return nil
}

// describePollError turns a bare 401 into something actionable: the usual cause
// is an access token Claude Code has not refreshed yet, not a revoked account.
func describePollError(err error, creds Credentials) string {
	if errors.Is(err, api.ErrUnauthorized) && creds.Expired() {
		return fmt.Sprintf("Claude Code access token expired %s ago — run any Claude Code command to refresh it",
			time.Since(creds.ExpiresAt).Round(time.Minute))
	}
	return err.Error()
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

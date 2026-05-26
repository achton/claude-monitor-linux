// Package poller polls /api/oauth/usage on a fixed cadence, reading the live
// access token from Claude Code's credentials file on every call. Single
// account by construction — Claude Code only holds one at a time.
package poller

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
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

	token, label, err := ReadClaudeCodeToken("")
	if err != nil {
		p.setError("no Claude Code credentials: " + err.Error())
		return err
	}
	r, err := p.API.OAuthUsage(ctx, token)
	if err != nil {
		p.setError(err.Error())
		return err
	}

	if err := p.writeReading(ctx, r); err != nil {
		p.setError("db write: " + err.Error())
		return err
	}

	p.mu.Lock()
	p.label = label
	p.lastError = ""
	p.lastSuccess = time.Now()
	suppress := p.suppressFirstNotify
	p.suppressFirstNotify = false
	p.mu.Unlock()

	if p.Evaluator != nil && !suppress {
		_ = p.Evaluator.EvaluateReading(ctx, label, r)
	}
	return nil
}

func (p *Poller) setError(msg string) {
	p.mu.Lock()
	p.lastError = msg
	p.mu.Unlock()
}

// writeReading persists one reading, applying weekly-reset detection.
func (p *Poller) writeReading(ctx context.Context, r api.UsageReading) error {
	return p.Store.WithTx(ctx, func(tx *sql.Tx) error {
		prev, err := p.Store.LatestUsageInTx(ctx, tx)
		hasPrev := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		if hasPrev && prev.WeeklyPercent.Valid &&
			prev.WeeklyPercent.Float64-r.SevenDayPercent > 5 {
			has, err := p.Store.HasRecentSynthetic(ctx, tx, time.Minute)
			if err != nil {
				return err
			}
			if !has {
				mid := time.Now().Add(-time.Second)
				_ = p.Store.InsertSyntheticUsage(ctx, tx, mid,
					nullOrZero(prev.SessionPercent), nullOrZero(prev.WeeklyPercent),
					nullOrZero(prev.WeeklySonnetPercent))
				_ = p.Store.InsertSyntheticUsage(ctx, tx, time.Now(), 0, 0, 0)
			}
		}

		return p.Store.InsertUsageReading(ctx, tx,
			r.FiveHourPercent, r.SevenDayPercent, r.SevenDaySonnetPercent,
			isoOrEmpty(r.FiveHourReset), isoOrEmpty(r.SevenDayReset), r.RawJSON)
	})
}

func isoOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullOrZero(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}

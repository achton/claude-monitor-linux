package notify

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// Evaluator evaluates a usage reading against configured thresholds and fires
// notifications via the Notifier, persisting debounce state in the store.
type Evaluator struct {
	Store      *store.Store
	Notifier   *Notifier
	Thresholds []int  // e.g. {75, 90, 95}; the synthetic 100 (rejected) is always evaluated
	AppName    string // visible app name shown by the notification server
}

// EvaluateReading inspects the reading and fires notifications for any
// thresholds it just crossed since the last reset window.
func (e *Evaluator) EvaluateReading(ctx context.Context, accountLabel string, r api.UsageReading) error {
	if e == nil || e.Store == nil || e.Notifier == nil {
		return nil
	}
	if r.IsRateLimited() {
		_ = e.fireIfNew(ctx, accountLabel, "weekly", 100, r.SevenDayReset, "weekly limit hit (rate-limited)")
		_ = e.fireIfNew(ctx, accountLabel, "session", 100, r.FiveHourReset, "5h limit hit (rate-limited)")
	}
	thresholds := append([]int(nil), e.Thresholds...)
	sort.Sort(sort.Reverse(sort.IntSlice(thresholds)))

	for _, t := range thresholds {
		if r.FiveHourPercent >= float64(t) && r.FiveHourReset.After(time.Now()) {
			_ = e.fireIfNew(ctx, accountLabel, "session", t, r.FiveHourReset,
				fmt.Sprintf("5h at %.0f%% (resets %s)", r.FiveHourPercent, humanReset(r.FiveHourReset)))
			break
		}
	}
	for _, t := range thresholds {
		if r.SevenDayPercent >= float64(t) && r.SevenDayReset.After(time.Now()) {
			_ = e.fireIfNew(ctx, accountLabel, "weekly", t, r.SevenDayReset,
				fmt.Sprintf("weekly at %.0f%% (resets %s)", r.SevenDayPercent, humanReset(r.SevenDayReset)))
			break
		}
	}
	return nil
}

func (e *Evaluator) fireIfNew(ctx context.Context, accountLabel, dim string, threshold int, reset time.Time, msg string) error {
	if reset.IsZero() {
		return nil
	}
	fired, err := e.Store.MarkNotificationFired(ctx, dim, threshold, reset)
	if err != nil {
		return err
	}
	if !fired {
		return nil
	}
	u := UrgencyLow
	switch {
	case threshold >= 95:
		u = UrgencyCritical
	case threshold >= 90:
		u = UrgencyNormal
	}
	_, err = e.Notifier.Send(e.AppName,
		fmt.Sprintf("%s — %s", accountLabel, dim),
		msg, u)
	return err
}

func humanReset(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("in %dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("in %dd %dh", int(d.Hours()/24), int(d.Hours())%24)
}

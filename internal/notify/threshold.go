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
	Thresholds []int  // e.g. {75, 90, 95}; 100 (limit hit) is always evaluated
	AppName    string // visible app name shown by the notification server
}

// EvaluateReading inspects every limit in the reading and fires notifications
// for any threshold just crossed within the current reset window. Iterating the
// reported limits rather than a fixed session/weekly pair means a newly
// introduced limit alerts without a code change.
func (e *Evaluator) EvaluateReading(ctx context.Context, accountLabel string, r api.UsageReading) error {
	if e == nil || e.Store == nil || e.Notifier == nil {
		return nil
	}

	thresholds := append([]int(nil), e.Thresholds...)
	sort.Sort(sort.Reverse(sort.IntSlice(thresholds)))

	for _, l := range r.Limits {
		if l.Percent >= 100 {
			_ = e.fireIfNew(ctx, accountLabel, l, 100,
				fmt.Sprintf("%s limit hit (rate-limited)", l.Label()))
			continue
		}
		// Highest crossed threshold only, so one poll never fires 75/90/95 at once.
		for _, t := range thresholds {
			if l.Percent < float64(t) {
				continue
			}
			_ = e.fireIfNew(ctx, accountLabel, l, t,
				fmt.Sprintf("%s at %.0f%%%s", l.Label(), l.Percent, resetSuffix(l.ResetsAt)))
			break
		}
	}
	return nil
}

// fireIfNew debounces on (limit key, threshold, reset window) so a threshold
// fires once per window rather than on every poll.
func (e *Evaluator) fireIfNew(ctx context.Context, accountLabel string, l api.Limit, threshold int, msg string) error {
	// A limit with no reset (credit spend, which is a monthly cap the API does
	// not date) still needs a debounce window, else the first alert would be
	// the only one ever. Fall back to the calendar month.
	window := "month:" + time.Now().UTC().Format("2006-01")
	if !l.ResetsAt.IsZero() {
		if !l.ResetsAt.After(time.Now()) {
			// Stale window: the limit should have reset already. Don't alert on
			// a percentage that belongs to an expired window.
			return nil
		}
		window = l.ResetsAt.UTC().Format(time.RFC3339)
	}

	fired, err := e.Store.MarkNotificationFiredKey(ctx, l.Key(), threshold, window)
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
		fmt.Sprintf("%s — %s", accountLabel, l.Label()), msg, u)
	return err
}

func resetSuffix(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return " (resets " + humanReset(t) + ")"
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

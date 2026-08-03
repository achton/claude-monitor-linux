package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/achton/claude-monitor-linux/internal/api"
)

// A limit with a reset window must expose an updater so its countdown can be
// refreshed in place; one without a window must not.
func TestBuildLimitRowUpdater(t *testing.T) {
	test.NewApp()

	withReset := api.Limit{
		Kind: api.KindSession, Group: api.GroupSession, Percent: 20,
		ResetsAt: time.Now().Add(2 * time.Hour),
	}
	if _, update := buildLimitRow(withReset, ""); update == nil {
		t.Error("a limit with a reset window should return a time updater")
	}

	noReset := api.Limit{Kind: api.KindSpend, Group: api.GroupSpend, Percent: 4}
	if _, update := buildLimitRow(noReset, ""); update != nil {
		t.Error("a limit with no reset window should not return an updater")
	}

	// A money detail replaces the countdown, so there is nothing to tick.
	if _, update := buildLimitRow(noReset, "0.00 of 50.00 EUR"); update != nil {
		t.Error("a limit rendering a detail string should not return an updater")
	}
}

func TestAgoText(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{5 * time.Second, "just now"},
		{90 * time.Second, "1m ago"},
		{2 * time.Hour, "2h0m ago"},
	}
	for _, c := range cases {
		if got := agoText(c.in); got != c.want {
			t.Errorf("agoText(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanResetCountsDown(t *testing.T) {
	// Two resets a minute apart must render differently, else a ticking label
	// would never visibly change.
	a := humanReset(time.Now().Add(4 * time.Hour))
	b := humanReset(time.Now().Add(4*time.Hour + 90*time.Minute))
	if a == b {
		t.Errorf("expected distinct countdowns, both %q", a)
	}
	if got := humanReset(time.Time{}); got != "—" {
		t.Errorf("zero reset: got %q", got)
	}
}

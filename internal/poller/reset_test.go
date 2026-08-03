package poller

import (
	"context"
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

func limits(session, weekly float64) []api.Limit {
	return []api.Limit{
		{Kind: api.KindSession, Group: api.GroupSession, Percent: session, Severity: "normal"},
		{Kind: api.KindWeeklyAll, Group: api.GroupWeekly, Percent: weekly, Severity: "normal", IsActive: true},
	}
}

// A session window rolling over must not fabricate a cliff on the weekly line.
// The two limits share the synthetic anchor rows, so zeroing every limit made
// the weekly series plunge to 0% and back on every 5-hour reset.
func TestSessionResetDoesNotZeroWeekly(t *testing.T) {
	ctx := context.Background()
	s, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p := &Poller{Store: s}

	// Session climbs to 22%, weekly sits at 32%.
	if err := p.writeReading(ctx, api.UsageReading{Limits: limits(22, 32)}); err != nil {
		t.Fatal(err)
	}
	// Session rolls over to 0; weekly is untouched and even ticks up.
	if err := p.writeReading(ctx, api.UsageReading{Limits: limits(0, 33)}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ReadingRange(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	var sawSyntheticZeroSession bool
	for _, r := range rows {
		if !r.IsSynthetic {
			continue
		}
		w, ok := r.Weekly()
		if !ok {
			t.Fatal("synthetic row dropped the weekly limit entirely")
		}
		if w.Percent == 0 {
			t.Errorf("synthetic row zeroed weekly at %s: weekly never reset", r.Timestamp)
		}
		if sess, ok := r.Session(); ok && sess.Percent == 0 {
			sawSyntheticZeroSession = true
		}
	}
	// The limit that did roll over still gets its clean cliff.
	if !sawSyntheticZeroSession {
		t.Error("expected a synthetic row taking session to 0 for the rollover cliff")
	}
}

// The limit that rolls over should be zeroed, and only that one.
func TestWeeklyResetZeroesOnlyWeekly(t *testing.T) {
	ctx := context.Background()
	s, _ := store.OpenInMemory()
	defer s.Close()
	p := &Poller{Store: s}

	if err := p.writeReading(ctx, api.UsageReading{Limits: limits(40, 90)}); err != nil {
		t.Fatal(err)
	}
	if err := p.writeReading(ctx, api.UsageReading{Limits: limits(41, 1)}); err != nil {
		t.Fatal(err)
	}

	rows, _ := s.ReadingRange(ctx, time.Now().Add(-time.Hour))
	found := false
	for _, r := range rows {
		if !r.IsSynthetic {
			continue
		}
		w, _ := r.Weekly()
		sess, _ := r.Session()
		if w.Percent == 0 {
			found = true
			if sess.Percent == 0 {
				t.Error("session was zeroed by a weekly rollover")
			}
		}
	}
	if !found {
		t.Error("expected a synthetic row zeroing weekly")
	}
}

// Steady usage must not produce reset anchors at all.
func TestNoResetNoSyntheticRows(t *testing.T) {
	ctx := context.Background()
	s, _ := store.OpenInMemory()
	defer s.Close()
	p := &Poller{Store: s}

	for _, w := range []float64{10, 12, 15, 15, 18} {
		if err := p.writeReading(ctx, api.UsageReading{Limits: limits(5, w)}); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := s.ReadingRange(ctx, time.Now().Add(-time.Hour))
	for _, r := range rows {
		if r.IsSynthetic {
			t.Fatalf("unexpected synthetic row at %s with steadily rising usage", r.Timestamp)
		}
	}
	if len(rows) != 5 {
		t.Errorf("readings: got %d, want 5", len(rows))
	}
}

func TestResetKeys(t *testing.T) {
	prev := store.Reading{Limits: limits(22, 32)}
	got := resetKeys(prev, api.UsageReading{Limits: limits(0, 33)})
	if !got[api.KindSession] {
		t.Error("session drop not detected")
	}
	if got[api.KindWeeklyAll] {
		t.Error("weekly reported as reset when it rose")
	}
	// A small dip is noise, not a rollover.
	if k := resetKeys(prev, api.UsageReading{Limits: limits(20, 32)}); len(k) != 0 {
		t.Errorf("a 2-point dip should not count as a reset: %v", k)
	}
}

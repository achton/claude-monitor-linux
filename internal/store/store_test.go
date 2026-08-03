package store

import (
	"context"
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
)

func sampleLimits() []api.Limit {
	reset := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	weekReset := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	return []api.Limit{
		{Kind: api.KindSession, Group: api.GroupSession, Percent: 23, Severity: "normal", ResetsAt: reset},
		{Kind: api.KindWeeklyAll, Group: api.GroupWeekly, Percent: 50, Severity: "normal", ResetsAt: weekReset, IsActive: true},
		{Kind: api.KindWeeklyScoped, Group: api.GroupWeekly, Percent: 12, Severity: "normal", ResetsAt: weekReset, ScopeModel: "Fable"},
		{Kind: api.KindSpend, Group: api.GroupSpend, Percent: 4, Severity: "normal"},
	}
}

func TestInsertReadingAndLatest(t *testing.T) {
	ctx := context.Background()
	s, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.InsertReading(ctx, nil, time.Now(), sampleLimits(), `{"x":1}`, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestReading(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Limits) != 4 {
		t.Fatalf("limits: got %d, want 4", len(got.Limits))
	}
	if l, ok := got.Session(); !ok || l.Percent != 23 {
		t.Errorf("session: got %v %v", l.Percent, ok)
	}
	if l, ok := got.Weekly(); !ok || l.Percent != 50 {
		t.Errorf("weekly: got %v %v", l.Percent, ok)
	}
	if got.PrimaryPercent() != 50 {
		t.Errorf("primary: got %v", got.PrimaryPercent())
	}
	if !got.RawData.Valid || got.RawData.String != `{"x":1}` {
		t.Errorf("raw_data: got %v", got.RawData)
	}
}

// A scoped limit must not collide with the unscoped limit of the same kind:
// they share a `kind` and are distinguished only by scope_model.
func TestScopedLimitRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()

	if _, err := s.InsertReading(ctx, nil, time.Now(), sampleLimits(), "", false); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestReading(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scoped, ok := got.Find("weekly_scoped:Fable")
	if !ok {
		t.Fatal("scoped limit not found by key")
	}
	if scoped.ScopeModel != "Fable" || scoped.Percent != 12 {
		t.Errorf("scoped: got %+v", scoped)
	}
	if scoped.Label() != "Weekly · Fable" {
		t.Errorf("label: got %q", scoped.Label())
	}
}

// A limit with no reset window (credit spend) must round-trip as a zero time,
// not as a parse failure or a bogus epoch.
func TestLimitWithoutResetRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()

	if _, err := s.InsertReading(ctx, nil, time.Now(),
		[]api.Limit{{Kind: api.KindSpend, Group: api.GroupSpend, Percent: 4, Severity: "normal"}},
		"", false); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestReading(ctx)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := got.Find(api.KindSpend)
	if !ok {
		t.Fatal("spend limit missing")
	}
	if !l.ResetsAt.IsZero() {
		t.Errorf("resets_at: want zero, got %v", l.ResetsAt)
	}
}

func TestReadingRangeAttachesLimits(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()

	for i := 0; i < 3; i++ {
		ts := time.Now().Add(time.Duration(-i) * time.Minute)
		if _, err := s.InsertReading(ctx, nil, ts, sampleLimits(), "", false); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ReadingRange(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("readings: got %d, want 3", len(rows))
	}
	for i, r := range rows {
		if len(r.Limits) != 4 {
			t.Errorf("reading %d: got %d limits, want 4", i, len(r.Limits))
		}
	}
	// Oldest first.
	if rows[0].Timestamp.After(rows[len(rows)-1].Timestamp) {
		t.Error("ReadingRange should return oldest first")
	}
}

func TestLimitKeysSince(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()

	if _, err := s.InsertReading(ctx, nil, time.Now(), sampleLimits(), "", false); err != nil {
		t.Fatal(err)
	}
	keys, err := s.LimitKeysSince(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 4 {
		t.Fatalf("keys: got %d, want 4", len(keys))
	}
	// Display order: session, weekly (unscoped first), then spend.
	want := []string{"session", "weekly_all", "weekly_scoped:Fable", "spend"}
	for i, w := range want {
		if keys[i].Key() != w {
			t.Errorf("key %d: got %q, want %q", i, keys[i].Key(), w)
		}
	}
}

func TestNotificationLogDebounce(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()
	reset := time.Now().Add(2 * time.Hour)
	fired, err := s.MarkNotificationFired(ctx, "weekly_all", 90, reset)
	if err != nil || !fired {
		t.Fatalf("first fire: %v %v", fired, err)
	}
	fired, err = s.MarkNotificationFired(ctx, "weekly_all", 90, reset)
	if err != nil {
		t.Fatal(err)
	}
	if fired {
		t.Error("second fire should be deduped")
	}
}

// A limit with no reset window debounces on an opaque window string.
func TestNotificationLogDebounceKeyed(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()

	fired, err := s.MarkNotificationFiredKey(ctx, "spend", 75, "month:2026-08")
	if err != nil || !fired {
		t.Fatalf("first fire: %v %v", fired, err)
	}
	fired, _ = s.MarkNotificationFiredKey(ctx, "spend", 75, "month:2026-08")
	if fired {
		t.Error("same window should be deduped")
	}
	// A new window re-arms the alert.
	fired, err = s.MarkNotificationFiredKey(ctx, "spend", 75, "month:2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Error("new window should fire again")
	}
}

func TestSyntheticIdempotency(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()
	if _, err := s.InsertReading(ctx, nil, time.Now(),
		[]api.Limit{{Kind: api.KindWeeklyAll, Group: api.GroupWeekly, Percent: 0}},
		"", true); err != nil {
		t.Fatal(err)
	}
	yes, err := s.HasRecentSynthetic(ctx, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !yes {
		t.Error("expected recent synthetic to be detected")
	}
}

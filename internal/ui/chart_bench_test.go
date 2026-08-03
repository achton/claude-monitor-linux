package ui

import (
	"context"
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// seedWeek fills a store with a realistic week of polls: 10-minute cadence over
// 7 days, four limits each.
func seedWeek(tb testing.TB) (*store.Store, time.Time) {
	tb.Helper()
	s, err := store.OpenInMemory()
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	start := time.Now().Add(-7 * 24 * time.Hour)
	weekReset := time.Now().Add(48 * time.Hour)
	for t := start; t.Before(time.Now()); t = t.Add(10 * time.Minute) {
		limits := []api.Limit{
			{Kind: api.KindSession, Group: api.GroupSession, Percent: 20, Severity: "normal", ResetsAt: t.Add(3 * time.Hour)},
			{Kind: api.KindWeeklyAll, Group: api.GroupWeekly, Percent: 45, Severity: "normal", ResetsAt: weekReset, IsActive: true},
			{Kind: api.KindWeeklyScoped, Group: api.GroupWeekly, Percent: 8, Severity: "normal", ResetsAt: weekReset, ScopeModel: "Fable"},
			{Kind: api.KindSpend, Group: api.GroupSpend, Percent: 3, Severity: "normal"},
		}
		if _, err := s.InsertReading(ctx, nil, t, limits, "", false); err != nil {
			tb.Fatal(err)
		}
	}
	return s, start
}

func BenchmarkQueryWeek(b *testing.B) {
	s, since := seedWeek(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := s.ReadingRange(ctx, since)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) == 0 {
			b.Fatal("no rows")
		}
		if _, err := s.LimitKeysSince(ctx, since); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderChartWeek(b *testing.B) {
	s, since := seedWeek(b)
	ctx := context.Background()
	rows, err := s.ReadingRange(ctx, since)
	if err != nil {
		b.Fatal(err)
	}
	keys, err := s.LimitKeysSince(ctx, since)
	if err != nil {
		b.Fatal(err)
	}
	maxGap := store.GapTolerance(rows)
	peaks := store.Peaks(rows, maxGap)
	b.Logf("rows=%d keys=%d peaks=%d gap=%v", len(rows), len(keys), len(peaks), maxGap)
	in := chartInput{Rows: rows, Keys: keys, Peaks: peaks, Window: 7 * 24 * time.Hour, MaxGap: maxGap}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderChart(in, 620, 260)
	}
}

// The full dashboard data path: what "Open Claude Monitor" pays on each open.
func BenchmarkHistoryDataPath(b *testing.B) {
	s, since := seedWeek(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := s.ReadingRange(ctx, since)
		if err != nil {
			b.Fatal(err)
		}
		keys, err := s.LimitKeysSince(ctx, since)
		if err != nil {
			b.Fatal(err)
		}
		maxGap := store.GapTolerance(rows)
		peaks := store.Peaks(rows, maxGap)
		renderChart(chartInput{Rows: rows, Keys: keys, Peaks: peaks,
			Window: 7 * 24 * time.Hour, MaxGap: maxGap}, 620, 260)
	}
}

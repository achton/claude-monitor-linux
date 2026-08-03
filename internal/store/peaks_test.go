package store

import (
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
)

// reading builds a non-synthetic Reading with one weekly limit at pct.
func reading(t time.Time, pct float64) Reading {
	return Reading{
		Timestamp: t,
		Limits: []api.Limit{
			{Kind: api.KindWeeklyAll, Group: api.GroupWeekly, Percent: pct},
		},
	}
}

func TestPeaksFindsHighestAndWhen(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rows := []Reading{
		reading(base, 20),
		reading(base.Add(10*time.Minute), 92),
		reading(base.Add(20*time.Minute), 40),
	}
	peaks := Peaks(rows, 30*time.Minute)
	if len(peaks) != 1 {
		t.Fatalf("peaks: got %d, want 1", len(peaks))
	}
	if peaks[0].Limit.Percent != 92 {
		t.Errorf("peak: got %v, want 92", peaks[0].Limit.Percent)
	}
	if !peaks[0].At.Equal(base.Add(10 * time.Minute)) {
		t.Errorf("at: got %v", peaks[0].At)
	}
	if peaks[0].Samples != 3 {
		t.Errorf("samples: got %d, want 3", peaks[0].Samples)
	}
}

func TestPeaksAccumulatesTimeAboveThreshold(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Above threshold from t+0 through t+30m, back down at t+40m.
	rows := []Reading{
		reading(base, 80),
		reading(base.Add(10*time.Minute), 85),
		reading(base.Add(20*time.Minute), 90),
		reading(base.Add(30*time.Minute), 78),
		reading(base.Add(40*time.Minute), 12),
	}
	peaks := Peaks(rows, time.Hour)
	got := peaks[0].AboveHigh
	// Four consecutive above-threshold readings contribute 3 intervals of 10m,
	// plus the interval leading into the drop.
	if got != 40*time.Minute {
		t.Errorf("above: got %v, want 40m", got)
	}
}

// A polling outage between two high readings must not be counted as sustained
// high usage — that is exactly the bug that made the old chart draw a ramp
// across four days of missing data.
func TestPeaksClampsOutageGap(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rows := []Reading{
		reading(base, 90),
		reading(base.Add(4*24*time.Hour), 91), // four-day gap
	}
	peaks := Peaks(rows, 30*time.Minute)
	if peaks[0].AboveHigh != 30*time.Minute {
		t.Errorf("above: got %v, want the 30m clamp", peaks[0].AboveHigh)
	}
}

func TestPeaksIgnoresSyntheticRows(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	syn := reading(base.Add(5*time.Minute), 99)
	syn.IsSynthetic = true
	rows := []Reading{reading(base, 20), syn, reading(base.Add(10*time.Minute), 30)}

	peaks := Peaks(rows, time.Hour)
	if peaks[0].Limit.Percent != 30 {
		t.Errorf("synthetic row leaked into the peak: got %v, want 30", peaks[0].Limit.Percent)
	}
	if peaks[0].Samples != 2 {
		t.Errorf("samples: got %d, want 2", peaks[0].Samples)
	}
}

func TestPeaksSortsWorstFirst(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rows := []Reading{{
		Timestamp: base,
		Limits: []api.Limit{
			{Kind: api.KindSession, Group: api.GroupSession, Percent: 12},
			{Kind: api.KindWeeklyAll, Group: api.GroupWeekly, Percent: 88},
			{Kind: api.KindSpend, Group: api.GroupSpend, Percent: 40},
		},
	}}
	peaks := Peaks(rows, time.Hour)
	want := []string{api.KindWeeklyAll, api.KindSpend, api.KindSession}
	for i, w := range want {
		if peaks[i].Limit.Kind != w {
			t.Errorf("peak %d: got %q, want %q", i, peaks[i].Limit.Kind, w)
		}
	}
}

func TestPeaksEmpty(t *testing.T) {
	if got := Peaks(nil, time.Hour); len(got) != 0 {
		t.Errorf("want no peaks, got %d", len(got))
	}
}

func TestGapTolerance(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// A 10-minute cadence yields a 30-minute tolerance.
	var tenMin []Reading
	for i := 0; i < 10; i++ {
		tenMin = append(tenMin, reading(base.Add(time.Duration(i)*10*time.Minute), 5))
	}
	if got := GapTolerance(tenMin); got != 30*time.Minute {
		t.Errorf("10m cadence: got %v, want 30m", got)
	}

	// A single outage must not drag the estimate up: the median ignores it.
	outage := append([]Reading{}, tenMin...)
	outage = append(outage, reading(base.Add(4*24*time.Hour), 5))
	if got := GapTolerance(outage); got != 30*time.Minute {
		t.Errorf("with outage: got %v, want 30m", got)
	}

	// Too little data falls back rather than guessing.
	if got := GapTolerance(tenMin[:2]); got != 30*time.Minute {
		t.Errorf("sparse: got %v, want the 30m fallback", got)
	}

	// A very long cadence is clamped.
	var slow []Reading
	for i := 0; i < 10; i++ {
		slow = append(slow, reading(base.Add(time.Duration(i)*3*time.Hour), 5))
	}
	if got := GapTolerance(slow); got != 2*time.Hour {
		t.Errorf("slow cadence: got %v, want the 2h clamp", got)
	}
}

func TestSegmentsForBreaksOnGap(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rows := []Reading{
		reading(base, 10),
		reading(base.Add(10*time.Minute), 20),
		// Four-day outage.
		reading(base.Add(4*24*time.Hour), 30),
		reading(base.Add(4*24*time.Hour+10*time.Minute), 40),
	}
	segs := SegmentsFor(rows, api.KindWeeklyAll, 30*time.Minute)
	if len(segs) != 2 {
		t.Fatalf("segments: got %d, want 2", len(segs))
	}
	if len(segs[0].Values) != 2 || len(segs[1].Values) != 2 {
		t.Errorf("segment sizes: %d and %d", len(segs[0].Values), len(segs[1].Values))
	}
	if segs[0].Values[0] != 10 || segs[1].Values[0] != 30 {
		t.Errorf("segment contents wrong: %+v", segs)
	}
}

func TestSegmentsForContiguous(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	var rows []Reading
	for i := 0; i < 5; i++ {
		rows = append(rows, reading(base.Add(time.Duration(i)*10*time.Minute), float64(i)))
	}
	segs := SegmentsFor(rows, api.KindWeeklyAll, 30*time.Minute)
	if len(segs) != 1 {
		t.Fatalf("segments: got %d, want 1", len(segs))
	}
	if len(segs[0].Times) != 5 {
		t.Errorf("points: got %d, want 5", len(segs[0].Times))
	}
}

// A limit absent from some readings must not create spurious segment breaks
// beyond the gap rule.
func TestSegmentsForMissingKey(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rows := []Reading{reading(base, 10), reading(base.Add(10*time.Minute), 20)}
	if segs := SegmentsFor(rows, "nonexistent", 30*time.Minute); len(segs) != 0 {
		t.Errorf("want no segments for an unknown key, got %d", len(segs))
	}
}

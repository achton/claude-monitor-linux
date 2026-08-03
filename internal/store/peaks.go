package store

import (
	"sort"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
)

// HighWaterThreshold is the percentage above which a limit counts as a "close
// call". Matches the lowest default notification threshold so the dashboard and
// the notifications agree on what "close" means.
const HighWaterThreshold = 75.0

// GapTolerance estimates the polling cadence from the readings themselves and
// returns the interval beyond which a pair of readings counts as an outage
// rather than normal spacing. Derived from the data so it stays correct when
// interval_seconds is changed, and clamped so a pathological history can't make
// it absurd.
func GapTolerance(rows []Reading) time.Duration {
	const fallback = 30 * time.Minute
	var deltas []time.Duration
	var prev time.Time
	for _, r := range rows {
		if r.IsSynthetic {
			continue
		}
		if !prev.IsZero() {
			if d := r.Timestamp.Sub(prev); d > 0 {
				deltas = append(deltas, d)
			}
		}
		prev = r.Timestamp
	}
	if len(deltas) < 3 {
		return fallback
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	median := deltas[len(deltas)/2]

	tol := 3 * median
	switch {
	case tol < 5*time.Minute:
		return 5 * time.Minute
	case tol > 2*time.Hour:
		return 2 * time.Hour
	default:
		return tol
	}
}

// LimitPeak summarises one limit's worst moment within a window.
type LimitPeak struct {
	Limit     api.Limit     // Kind/Group/ScopeModel identify it; Percent is the peak
	At        time.Time     // when the peak was observed
	AboveHigh time.Duration // time spent at or above HighWaterThreshold
	Samples   int           // real (non-synthetic) readings that reported this limit
}

// Key identifies the limit this peak belongs to.
func (p LimitPeak) Key() string { return p.Limit.Key() }

// Peaks reports, per limit, the highest percentage seen in rows and when it
// occurred, plus how long the limit spent at or above HighWaterThreshold.
//
// maxGap bounds how much elapsed time a single pair of consecutive readings may
// contribute to AboveHigh. Without it an outage between two high readings would
// be counted as sustained high usage; with it, a four-day polling gap adds at
// most one interval. Synthetic rows are skipped: they are chart scaffolding for
// reset cliffs, not observations.
func Peaks(rows []Reading, maxGap time.Duration) []LimitPeak {
	type acc struct {
		peak      api.Limit
		at        time.Time
		above     time.Duration
		samples   int
		prevTime  time.Time
		prevAbove bool
		seeded    bool
	}
	byKey := map[string]*acc{}
	var order []string

	for _, r := range rows {
		if r.IsSynthetic {
			continue
		}
		for _, l := range r.Limits {
			k := l.Key()
			a, ok := byKey[k]
			if !ok {
				a = &acc{}
				byKey[k] = a
				order = append(order, k)
			}
			a.samples++

			if !a.seeded || l.Percent > a.peak.Percent {
				a.peak = l
				a.at = r.Timestamp
				a.seeded = true
			}

			// Accumulate time above the threshold across consecutive readings.
			isAbove := l.Percent >= HighWaterThreshold
			if a.prevAbove && !a.prevTime.IsZero() {
				delta := r.Timestamp.Sub(a.prevTime)
				if delta > 0 {
					if delta > maxGap {
						delta = maxGap
					}
					a.above += delta
				}
			}
			a.prevTime = r.Timestamp
			a.prevAbove = isAbove
		}
	}

	out := make([]LimitPeak, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		if !a.seeded {
			continue
		}
		out = append(out, LimitPeak{
			Limit:     a.peak,
			At:        a.at,
			AboveHigh: a.above,
			Samples:   a.samples,
		})
	}

	// Worst first: that is the answer to "did I come close?".
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Limit.Percent != out[j].Limit.Percent {
			return out[i].Limit.Percent > out[j].Limit.Percent
		}
		return out[i].Limit.Key() < out[j].Limit.Key()
	})
	return out
}

// Segment is a run of consecutive readings with no polling gap, used by the
// chart so an outage renders as a break rather than a straight line drawn
// through data that was never collected.
type Segment struct {
	Times  []time.Time
	Values []float64
}

// SegmentsFor splits one limit's series into gap-free segments. A gap longer
// than maxGap starts a new segment.
func SegmentsFor(rows []Reading, key string, maxGap time.Duration) []Segment {
	var segs []Segment
	var cur Segment
	var prev time.Time

	flush := func() {
		if len(cur.Times) > 0 {
			segs = append(segs, cur)
			cur = Segment{}
		}
	}

	for _, r := range rows {
		l, ok := r.Find(key)
		if !ok {
			continue
		}
		if !prev.IsZero() && r.Timestamp.Sub(prev) > maxGap {
			flush()
		}
		cur.Times = append(cur.Times, r.Timestamp)
		cur.Values = append(cur.Values, l.Percent)
		prev = r.Timestamp
	}
	flush()
	return segs
}

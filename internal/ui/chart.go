package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	chart "github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// seriesPalette colors chart series. Limits beyond its length reuse colors; the
// legend still distinguishes them by name. Shared with the Fyne-rendered legend
// so the swatch and the line can never drift apart.
var seriesPalette = []color.NRGBA{
	{R: 0xD9, G: 0x77, B: 0x57, A: 0xFF}, // terracotta
	{R: 0x78, G: 0x8C, B: 0x5D, A: 0xFF}, // sage
	{R: 0x6B, G: 0x8F, B: 0xA3, A: 0xFF}, // slate blue
	{R: 0xA3, G: 0x84, B: 0x6B, A: 0xFF}, // tan
	{R: 0x8C, G: 0x6D, B: 0x8C, A: 0xFF}, // mauve
	{R: 0x5D, G: 0x7A, B: 0x6B, A: 0xFF}, // pine
}

// paletteColor returns the color used for series i, wrapping if needed.
func paletteColor(i int) color.NRGBA { return seriesPalette[i%len(seriesPalette)] }

// chartInput is everything renderChart needs, gathered once by the dashboard so
// the chart and the peaks table cannot disagree.
type chartInput struct {
	Rows   []store.Reading
	Keys   []api.Limit
	Peaks  []store.LimitPeak
	Window time.Duration
	MaxGap time.Duration

	// Fyne follows the system theme, so the chart PNG has to as well or a dark
	// desktop gets a glaring white box.
	Background color.Color
	Foreground color.Color
}

// renderChart draws one line per limit present in the window.
//
// Spans with no readings are bridged with a dashed, dimmed connector rather than
// a solid line: the series stays visually continuous without implying that the
// bridged period was measured. It also marks the high-water threshold, so "did I
// come close?" is answerable by eye.
func renderChart(in chartInput, cw, chh int) image.Image {
	var series []chart.Series

	// Threshold guides first so the data lines draw over them.
	if len(in.Rows) > 0 {
		start := in.Rows[0].Timestamp
		end := in.Rows[len(in.Rows)-1].Timestamp
		for _, g := range []struct {
			at    float64
			color string
		}{
			{store.HighWaterThreshold, "C9A227"},
			{90, "C03A24"},
		} {
			series = append(series, chart.TimeSeries{
				Style: chart.Style{
					StrokeColor:     drawing.ColorFromHex(g.color).WithAlpha(140),
					StrokeWidth:     1,
					StrokeDashArray: []float64{3, 5},
				},
				XValues: []time.Time{start, end},
				YValues: []float64{g.at, g.at},
			})
		}
	}

	peakByKey := map[string]store.LimitPeak{}
	for _, p := range in.Peaks {
		peakByKey[p.Key()] = p
	}

	for i, k := range in.Keys {
		key := k.Key()
		seriesColor := toDrawing(paletteColor(i), drawing.ColorBlue)
		segs := store.SegmentsFor(in.Rows, key, in.MaxGap)

		// Bridge the gaps first so the solid data lines draw on top of them.
		for j := 1; j < len(segs); j++ {
			prev, cur := segs[j-1], segs[j]
			if len(prev.Times) == 0 || len(cur.Times) == 0 {
				continue
			}
			series = append(series, chart.TimeSeries{
				Style: chart.Style{
					// Dashed and dimmed: the line stays visually continuous, but a
					// span with no readings must not look like measured data.
					StrokeColor:     seriesColor.WithAlpha(90),
					StrokeWidth:     1.2,
					StrokeDashArray: []float64{2, 4},
				},
				XValues: []time.Time{prev.Times[len(prev.Times)-1], cur.Times[0]},
				YValues: []float64{prev.Values[len(prev.Values)-1], cur.Values[0]},
			})
		}

		for j, seg := range segs {
			if len(seg.Times) == 0 {
				continue
			}
			style := chart.Style{StrokeColor: seriesColor, StrokeWidth: 1.8}
			// A short burst is only a few pixels wide at 7d/30d and reads as
			// missing data; dots make it findable.
			if segmentIsNarrow(seg, in.Rows, cw-80) {
				style.DotColor = seriesColor
				style.DotWidth = 2.5
			}
			s := chart.TimeSeries{
				Style:   style,
				XValues: seg.Times,
				YValues: seg.Values,
			}
			// Only the first segment carries the name, else the legend repeats
			// the same limit once per gap.
			if j == 0 {
				s.Name = k.Label()
			}
			series = append(series, s)
		}

		// Mark where this limit peaked, so the eye lands on the answer.
		if p, ok := peakByKey[key]; ok && !p.At.IsZero() && p.Limit.Percent > 0 {
			series = append(series, chart.TimeSeries{
				Style: chart.Style{
					StrokeWidth: 0,
					DotColor:    seriesColor,
					DotWidth:    4,
				},
				XValues: []time.Time{p.At},
				YValues: []float64{p.Limit.Percent},
			})
		}
	}

	if len(series) == 0 {
		return blankImage(cw, chh)
	}

	bg := toDrawing(in.Background, drawing.ColorWhite)
	fg := toDrawing(in.Foreground, drawing.ColorFromHex("303030"))
	// Axis lines and gridlines read as chrome, not content: same hue as the
	// text, dialled back so they don't compete with the series.
	axisColor := fg.WithAlpha(150)

	c := chart.Chart{
		Width:  cw,
		Height: chh,
		Background: chart.Style{
			Padding:   chart.Box{Top: 8, Left: 12, Right: 16, Bottom: 12},
			FillColor: bg,
		},
		Canvas: chart.Style{FillColor: bg},
		XAxis: chart.XAxis{
			Style: chart.Style{StrokeColor: axisColor, FontColor: fg},
			// Pin the range to the data. Left to infer it, the axis and the series
			// disagreed and the most recent readings fell outside the plot.
			Range: domainRange(in.Rows),
			// Explicit ticks: go-chart's auto-ticks put two labels inside the
			// same day at 7d and 30d, so the axis read "Sat 01 … Sat 01".
			Ticks:          timeTicks(in.Rows, in.Window),
			ValueFormatter: chart.TimeValueFormatterWithFormat(xAxisFormat(in.Window)),
		},
		YAxis: chart.YAxis{
			// go-chart's primary Y axis renders on the right; the secondary one
			// is the left-hand axis readers expect to scan against.
			AxisType: chart.YAxisSecondary,
			Style:    chart.Style{StrokeColor: axisColor, FontColor: fg},
			Range:    &chart.ContinuousRange{Min: 0, Max: 100},
			// Quarters, not the thirds go-chart picks by default: 75 is the
			// threshold that matters, so it needs to be a labelled tick.
			Ticks: []chart.Tick{
				{Value: 0, Label: "0%"},
				{Value: 25, Label: "25%"},
				{Value: 50, Label: "50%"},
				{Value: 75, Label: "75%"},
				{Value: 100, Label: "100%"},
			},
		},
		Series: series,
	}
	var buf bytes.Buffer
	if err := c.Render(chart.PNG, &buf); err != nil {
		return blankImage(cw, chh)
	}
	if img, err := png.Decode(&buf); err == nil {
		return img
	}
	return blankImage(cw, chh)
}

// domainRange pins the X axis to the first and last reading.
func domainRange(rows []store.Reading) *chart.ContinuousRange {
	if len(rows) < 2 {
		return nil
	}
	first, last := rows[0].Timestamp, rows[len(rows)-1].Timestamp
	if !last.After(first) {
		return nil
	}
	return &chart.ContinuousRange{
		Min: float64(first.UnixNano()),
		Max: float64(last.UnixNano()),
	}
}

// segmentIsNarrow reports whether a segment would render too thin to notice, so
// its readings should be dotted as well as stroked. A busy hour inside a 30-day
// window is only a few pixels wide otherwise.
func segmentIsNarrow(seg store.Segment, rows []store.Reading, plotWidth int) bool {
	if len(seg.Times) <= 2 {
		return true
	}
	if len(rows) < 2 || plotWidth <= 0 {
		return false
	}
	domain := rows[len(rows)-1].Timestamp.Sub(rows[0].Timestamp)
	if domain <= 0 {
		return false
	}
	span := seg.Times[len(seg.Times)-1].Sub(seg.Times[0])
	return float64(span)/float64(domain)*float64(plotWidth) < 14
}

// timeTicks builds evenly spaced X ticks spanning the full data range.
//
// The endpoints must always be present: go-chart derives the axis range from the
// tick extents, so dropping the final tick silently clips the most recent
// readings out of the plot. Uniqueness is achieved by choosing a finer label
// format instead of discarding ticks.
func timeTicks(rows []store.Reading, window time.Duration) []chart.Tick {
	if len(rows) < 2 {
		return nil
	}
	first := rows[0].Timestamp
	last := rows[len(rows)-1].Timestamp
	if !last.After(first) {
		return nil
	}

	const want = 6
	for _, layout := range tickLayouts(window) {
		if ticks, unique := buildTicks(first, last, want, layout); unique {
			return ticks
		}
	}
	// Every candidate collided (a very short window): keep the last, coarsest
	// attempt rather than clip the range.
	layouts := tickLayouts(window)
	ticks, _ := buildTicks(first, last, want, layouts[len(layouts)-1])
	return ticks
}

// tickLayouts lists label formats from coarsest to finest for a given window.
func tickLayouts(window time.Duration) []string {
	switch {
	case window <= 24*time.Hour:
		return []string{"15:04", "Mon 15:04", "Jan 02 15:04"}
	case window <= 7*24*time.Hour:
		return []string{"Mon 02", "Mon 15:04", "Jan 02 15:04"}
	default:
		return []string{"Jan 02", "Jan 02 15:04"}
	}
}

// buildTicks spreads want ticks inclusively across [first, last] and reports
// whether every label came out distinct.
func buildTicks(first, last time.Time, want int, layout string) ([]chart.Tick, bool) {
	step := last.Sub(first) / time.Duration(want-1)
	ticks := make([]chart.Tick, 0, want)
	seen := map[string]bool{}
	unique := true
	for i := 0; i < want; i++ {
		at := first.Add(time.Duration(i) * step)
		if i == want-1 {
			at = last // exact endpoint, so the range covers all the data
		}
		label := at.Local().Format(layout)
		if seen[label] {
			unique = false
		}
		seen[label] = true
		ticks = append(ticks, chart.Tick{
			Value: float64(at.UnixNano()),
			Label: label,
		})
	}
	return ticks, unique
}

// toDrawing converts a Fyne theme color to go-chart's color type, falling back
// when the caller supplied none (tests, or a nil theme).
func toDrawing(c color.Color, fallback drawing.Color) drawing.Color {
	if c == nil {
		return fallback
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return fallback
	}
	return drawing.Color{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: uint8(a >> 8),
	}
}

func xAxisFormat(window time.Duration) string {
	switch {
	case window <= 24*time.Hour:
		return "15:04"
	case window <= 7*24*time.Hour:
		return "Mon 02"
	default:
		return "Jan 02"
	}
}

func blankImage(w, h int) image.Image {
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// humanWhen formats a peak's timestamp relative to the window being shown: a
// time of day is enough inside 24h, otherwise the weekday or date matters.
func humanWhen(t time.Time, window time.Duration) string {
	if t.IsZero() {
		return "—"
	}
	local := t.Local()
	switch {
	case window <= 24*time.Hour:
		return local.Format("15:04")
	case window <= 7*24*time.Hour:
		return local.Format("Mon 15:04")
	default:
		return local.Format("Jan 02 15:04")
	}
}

// humanSpan renders a duration spent above the high-water threshold.
func humanSpan(d time.Duration) string {
	switch {
	case d <= 0:
		return fmt.Sprintf("never above %.0f%%", store.HighWaterThreshold)
	case d < time.Hour:
		return fmt.Sprintf("%dm above %.0f%%", int(d.Minutes()), store.HighWaterThreshold)
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh above %.0f%%", h, store.HighWaterThreshold)
		}
		return fmt.Sprintf("%dh%dm above %.0f%%", h, m, store.HighWaterThreshold)
	}
}

package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	chart "github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"

	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// NewChartPane returns a chart widget for the single account.
func NewChartPane(env *cli.Env, minSize fyne.Size) fyne.CanvasObject {
	current := 7 * 24 * time.Hour

	cw, chh := int(minSize.Width), int(minSize.Height)
	if cw < 360 {
		cw = 360
	}
	if chh < 220 {
		chh = 220
	}

	img := canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, cw, chh)))
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(float32(cw), float32(chh)))

	redraw := func() {
		rec, err := env.Store.UsageRange(env.Ctx, time.Now().Add(-current))
		if err != nil || len(rec) == 0 {
			img.Image = blankImage(cw, chh)
			img.Refresh()
			return
		}
		img.Image = renderChart(rec, current, cw, chh)
		img.Refresh()
	}

	btn24 := widget.NewButton("24h", func() { current = 24 * time.Hour; redraw() })
	btn7d := widget.NewButton("7d", func() { current = 7 * 24 * time.Hour; redraw() })
	btn30d := widget.NewButton("30d", func() { current = 30 * 24 * time.Hour; redraw() })
	btn7d.Importance = widget.HighImportance

	header := container.NewHBox(
		widget.NewLabelWithStyle("History", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		btn24, btn7d, btn30d,
	)
	redraw()
	return container.NewBorder(header, nil, nil, nil, img)
}

func renderChart(rows []store.UsageRecord, window time.Duration, cw, chh int) image.Image {
	var times []time.Time
	var weekly []float64
	var session []float64
	var resetMarkers []time.Time
	for _, r := range rows {
		times = append(times, r.Timestamp)
		if r.WeeklyPercent.Valid {
			weekly = append(weekly, r.WeeklyPercent.Float64)
		} else {
			weekly = append(weekly, 0)
		}
		if r.SessionPercent.Valid {
			session = append(session, r.SessionPercent.Float64)
		} else {
			session = append(session, 0)
		}
		if r.IsSynthetic {
			resetMarkers = append(resetMarkers, r.Timestamp)
		}
	}

	c := chart.Chart{
		Width:  cw,
		Height: chh,
		Background: chart.Style{
			Padding: chart.Box{Top: 12, Left: 24, Right: 12, Bottom: 24},
		},
		XAxis: chart.XAxis{
			Style:          chart.Style{StrokeColor: drawing.ColorFromHex("707070")},
			ValueFormatter: chart.TimeValueFormatterWithFormat(xAxisFormat(window)),
		},
		YAxis: chart.YAxis{
			Style: chart.Style{StrokeColor: drawing.ColorFromHex("707070")},
			Range: &chart.ContinuousRange{Min: 0, Max: 100},
			ValueFormatter: func(v interface{}) string {
				if f, ok := v.(float64); ok {
					return fmt.Sprintf("%.0f%%", f)
				}
				return ""
			},
		},
		Series: []chart.Series{
			chart.TimeSeries{
				Name:    "weekly",
				Style:   chart.Style{StrokeColor: drawing.ColorFromHex("D97757"), StrokeWidth: 2.0},
				XValues: times,
				YValues: weekly,
			},
			chart.TimeSeries{
				Name:    "session",
				Style:   chart.Style{StrokeColor: drawing.ColorFromHex("788C5D"), StrokeWidth: 1.5},
				XValues: times,
				YValues: session,
			},
		},
	}
	for _, rm := range resetMarkers {
		c.Series = append(c.Series, chart.TimeSeries{
			Style: chart.Style{
				StrokeColor:     drawing.ColorFromHex("888888"),
				StrokeWidth:     1,
				StrokeDashArray: []float64{4, 4},
			},
			XValues: []time.Time{rm, rm},
			YValues: []float64{0, 100},
		})
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

// Package ui implements the dashboard window.
//
// Constructors take an existing fyne.App; they never call app.New(). This is
// part of the headless-CLI safety invariant: even if ui is imported from a
// non-tray path, no fyne side effects fire until the tray itself constructs
// the App.
package ui

import (
	"database/sql"
	"fmt"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/achton/claude-monitor-linux/internal/cli"
)

// NewAccountListWindow builds the dashboard. Name kept for call-site stability.
func NewAccountListWindow(app fyne.App, env *cli.Env) fyne.Window {
	w := app.NewWindow("Claude Monitor")
	w.Resize(fyne.NewSize(560, 640))

	body := container.NewVBox()

	var refresh func()
	refresh = func() {
		fyne.Do(func() {
			body.RemoveAll()
			body.Add(buildDashboard(app, env, refresh))
		})
	}
	refresh()

	footer := container.NewHBox(
		widget.NewButton("Refresh", func() {
			go func() {
				_ = env.Poller.PollNow(env.Ctx)
				refresh()
			}()
		}),
		widget.NewButton("Settings", func() {
			NewSettingsWindow(app, env, refresh).Show()
		}),
	)
	w.SetContent(container.NewBorder(nil, footer, nil, nil, container.NewPadded(body)))
	return w
}

// buildDashboard renders the single-account view.
func buildDashboard(app fyne.App, env *cli.Env, refresh func()) fyne.CanvasObject {
	rec, recErr := env.Store.LatestUsage(env.Ctx)
	label, lastErr, _, _ := env.Poller.Status()
	if label == "" {
		label = "Claude Code"
	}

	sessionPct := valOrZero(rec.SessionPercent)
	weeklyPct := valOrZero(rec.WeeklyPercent)
	sessionReset := parseISOOrZero(rec.SessionReset)
	weeklyReset := parseISOOrZero(rec.WeeklyReset)

	parts := []fyne.CanvasObject{}

	if lastErr != "" {
		parts = append(parts, buildPollErrorBanner(env, lastErr, rec.Timestamp, refresh))
	}

	name := widget.NewLabelWithStyle(label,
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	parts = append(parts, name)

	if recErr != nil {
		parts = append(parts, widget.NewLabel("Waiting for the first successful poll…"))
	} else {
		parts = append(parts,
			buildMetricBlock("Session (5h)", sessionPct, sessionReset),
			buildMetricBlock("Weekly (7d)", weeklyPct, weeklyReset),
		)
	}

	parts = append(parts, NewChartPane(env, fyne.NewSize(520, 220)))

	return container.NewVBox(parts...)
}

// buildMetricBlock renders one dimension — a big % number, a label + reset
// countdown to the right, then the bar below.
func buildMetricBlock(title string, pct float64, reset time.Time) fyne.CanvasObject {
	big := canvas.NewText(fmt.Sprintf("%.0f%%", pct), theme.ForegroundColor())
	big.TextSize = 36
	big.TextStyle = fyne.TextStyle{Bold: true}
	big.Color = colorForPct(pct)

	header := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	resetTxt := widget.NewLabel("resets " + humanReset(reset))
	right := container.NewVBox(header, resetTxt)

	row := container.NewBorder(nil, nil, big, nil, right)
	return container.NewVBox(row, newProgressBar(pct), widget.NewSeparator())
}

// newProgressBar creates a colored horizontal progress bar at the given %.
func newProgressBar(pct float64) fyne.CanvasObject {
	const h float32 = 10
	bg := canvas.NewRectangle(color.NRGBA{R: 0xB0, G: 0xAE, B: 0xA5, A: 0x40})
	fill := canvas.NewRectangle(colorForPct(pct))
	frac := math.Max(0, math.Min(1, pct/100))
	return container.New(&progressBarLayout{frac: frac, height: h}, bg, fill)
}

type progressBarLayout struct {
	frac   float64
	height float32
}

func (l *progressBarLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(200, l.height)
}

func (l *progressBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	bg, fill := objects[0], objects[1]
	bg.Resize(size)
	bg.Move(fyne.NewPos(0, 0))
	fw := float32(l.frac) * size.Width
	fill.Resize(fyne.NewSize(fw, size.Height))
	fill.Move(fyne.NewPos(0, 0))
}

func colorForPct(pct float64) color.Color {
	switch {
	case pct >= 95:
		return color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0xFF}
	case pct >= 90:
		return color.NRGBA{R: 0xD9, G: 0x77, B: 0x57, A: 0xFF}
	default:
		return color.NRGBA{R: 0x78, G: 0x8C, B: 0x5D, A: 0xFF}
	}
}

func humanReset(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Until(t)
	if d <= 0 {
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

func valOrZero(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}

func parseISOOrZero(s sql.NullString) time.Time {
	if !s.Valid {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s.String); err == nil {
			return t
		}
	}
	return time.Time{}
}

// buildPollErrorBanner renders the warning surface shown above the dashboard
// metrics when the latest poll failed.
func buildPollErrorBanner(env *cli.Env, lastErr string, lastTimestamp time.Time, refresh func()) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0x33})
	bg.StrokeColor = color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0xCC}
	bg.StrokeWidth = 1

	title := widget.NewLabelWithStyle("⚠  Polls failing — numbers below are stale",
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	lastGood := "last successful poll: never"
	if !lastTimestamp.IsZero() {
		lastGood = fmt.Sprintf("last successful poll %s ago",
			compactDuration(time.Since(lastTimestamp)))
	}
	body := fmt.Sprintf("%s\n%s\n\nIf this is a token problem, run `claude /login` in a terminal.",
		lastErr, lastGood)
	detail := widget.NewLabel(body)
	detail.Wrapping = fyne.TextWrapWord

	retry := widget.NewButton("Retry now", func() {
		go func() {
			_ = env.Poller.PollNow(env.Ctx)
			refresh()
		}()
	})

	inner := container.NewVBox(title, detail, container.NewHBox(retry))
	padded := container.NewPadded(inner)
	return container.NewStack(bg, padded)
}

func compactDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours()/24), int(d.Hours())%24)
}

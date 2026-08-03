// Package ui implements the dashboard window.
//
// Constructors take an existing fyne.App; they never call app.New(). This is
// part of the headless-CLI safety invariant: even if ui is imported from a
// non-tray path, no fyne side effects fire until the tray itself constructs
// the App.
package ui

import (
	"fmt"
	"image/color"
	"math"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// Dashboard couples the dashboard window with its refresh hooks so the tray can
// keep one window alive across open/close instead of rebuilding it. Rebuilding
// meant a fresh GLFW window and OpenGL context on every open, which dominated
// open latency — the data path itself is ~20ms for a week of history.
type Dashboard struct {
	Window fyne.Window

	app    fyne.App
	env    *cli.Env
	footer fyne.CanvasObject

	// scroll owns the body. Replacing its Content and refreshing it is what
	// actually repaints: mutating a container nested inside the scroll updates
	// the tree without re-laying out the viewport, so the window looked frozen.
	scroll  *container.Scroll
	content fyne.CanvasObject

	// span is the history window the peaks table and chart both cover.
	span time.Duration

	// autoSized guards the fit-to-content resize so it runs once and never
	// fights a size the user chose by dragging the window edge.
	autoSized bool

	// timeUpdaters refresh only the relative-time labels. Rebuilding the whole
	// body on a timer would re-render the chart and reset the scroll position,
	// so countdowns are updated in place instead.
	timeUpdaters []func()

	tickMu   sync.Mutex
	tickStop chan struct{}
}

// timeTick is how often relative times are refreshed. They are rendered at
// minute granularity, so this only needs to be comfortably under a minute.
const timeTick = 20 * time.Second

// NewDashboard builds the dashboard window. It is created hidden; call Show on
// Window (or use the tray's focus path) to present it.
func NewDashboard(app fyne.App, env *cli.Env) *Dashboard {
	d := &Dashboard{
		Window: app.NewWindow("Claude Monitor"),
		app:    app,
		env:    env,
		span:   7 * 24 * time.Hour,
	}
	d.scroll = container.NewVScroll(container.NewPadded(container.NewVBox()))
	d.footer = container.NewVBox(
		widget.NewSeparator(),
		container.NewHBox(
			widget.NewButton("Refresh", func() {
				go func() {
					// Errors are recorded and logged by the poller; the rebuild
					// surfaces them in the banner either way.
					_ = env.Poller.PollNow(env.Ctx)
					d.RefreshAsync()
				}()
			}),
			widget.NewButton("Settings", func() {
				NewSettingsWindow(app, env, d.RefreshAsync).Show()
			}),
		),
	)
	// The scroll container stays as the fallback for a short screen or an
	// unusually long limit list; sizeToContent below keeps it from being needed
	// in the normal case.
	d.Window.SetContent(container.NewBorder(
		nil, d.footer, nil, nil,
		d.scroll,
	))
	d.Rebuild()
	d.sizeToContent()
	return d
}

// Dashboard window sizing bounds. The maximum exists because Fyne offers no
// portable screen-size query here: past it, scrolling is the better answer than
// a window taller than the display.
const (
	dashMinWidth  float32 = 560
	dashMinHeight float32 = 480
	dashMaxWidth  float32 = 900
	dashMaxHeight float32 = 1000

	// dashHeightHeadroom absorbs layout chrome that MinSize does not report.
	dashHeightHeadroom float32 = 48
)

// sizeToContent grows the window to fit its content so the dashboard opens
// without a scrollbar. Runs once, on first build.
func (d *Dashboard) sizeToContent() {
	if d.autoSized {
		return
	}
	d.autoSized = true

	content := d.content.MinSize()
	// Padding on both axes, plus the footer row the border layout reserves, plus
	// headroom for the layout chrome MinSize does not account for (scroll
	// viewport insets and padding rounding). Fyne draws a full-length scrollbar
	// on even a few pixels of overflow, so it is worth over- rather than
	// under-estimating here.
	pad := theme.Padding() * 4
	want := fyne.NewSize(
		content.Width+pad,
		content.Height+d.footer.MinSize().Height+pad+dashHeightHeadroom,
	)

	d.Window.Resize(fyne.NewSize(
		clampF(want.Width, dashMinWidth, dashMaxWidth),
		clampF(want.Height, dashMinHeight, dashMaxHeight),
	))
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Rebuild repopulates the content. Must be called on the Fyne goroutine.
func (d *Dashboard) Rebuild() {
	d.content = d.buildBody()
	d.scroll.Content = container.NewPadded(d.content)
	d.scroll.Refresh()
}

// RefreshAsync repopulates the content from any goroutine.
func (d *Dashboard) RefreshAsync() { fyne.Do(d.Rebuild) }

// updateTimes refreshes every relative-time label. Runs on the Fyne goroutine.
func (d *Dashboard) updateTimes() {
	for _, u := range d.timeUpdaters {
		u()
	}
}

// StartTimeUpdates keeps countdowns and "updated ..." current while the window
// is on screen. Idempotent.
func (d *Dashboard) StartTimeUpdates() {
	d.tickMu.Lock()
	defer d.tickMu.Unlock()
	if d.tickStop != nil {
		return
	}
	stop := make(chan struct{})
	d.tickStop = stop
	go func() {
		t := time.NewTicker(timeTick)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fyne.Do(d.updateTimes)
			}
		}
	}()
}

// StopTimeUpdates halts the ticker when the window is hidden, so a closed
// dashboard costs nothing. Idempotent.
func (d *Dashboard) StopTimeUpdates() {
	d.tickMu.Lock()
	defer d.tickMu.Unlock()
	if d.tickStop == nil {
		return
	}
	close(d.tickStop)
	d.tickStop = nil
}

// buildBody renders the whole dashboard: what the limits are now, when they
// last came close, and the shape of that over time.
func (d *Dashboard) buildBody() fyne.CanvasObject {
	d.timeUpdaters = nil
	env := d.env
	rec, recErr := env.Store.LatestReading(env.Ctx)
	label, lastErr, _, _ := env.Poller.Status()
	if label == "" {
		label = "Claude Code"
	}

	parts := []fyne.CanvasObject{}
	if lastErr != "" {
		parts = append(parts, buildPollErrorBanner(env, lastErr, rec.Timestamp, d.RefreshAsync))
	}

	title := widget.NewLabelWithStyle(label,
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	if ts := rec.Timestamp; !ts.IsZero() {
		setHead := func() {
			title.SetText(label + "   ·   updated " + agoText(time.Since(ts)))
		}
		setHead()
		d.timeUpdaters = append(d.timeUpdaters, setHead)
	}
	parts = append(parts, title)
	parts = append(parts, widget.NewSeparator())

	switch {
	case recErr != nil:
		parts = append(parts, widget.NewLabel("Waiting for the first successful poll…"))
	case len(rec.Limits) == 0:
		parts = append(parts, widget.NewLabel("No limits reported by the API."))
	default:
		// Credit spend is money, not a window; read the amounts back out of the
		// stored response body so the row says what it actually costs.
		spendText := ""
		if rec.RawData.Valid {
			if s, ok := api.SpendFromRaw(rec.RawData.String); ok {
				spendText = s.Summary()
			}
		}
		for _, l := range rec.Limits {
			detail := ""
			if l.Kind == api.KindSpend {
				detail = spendText
			}
			row, update := buildLimitRow(l, detail)
			parts = append(parts, row)
			if update != nil {
				d.timeUpdaters = append(d.timeUpdaters, update)
			}
		}
	}

	parts = append(parts, widget.NewSeparator())
	parts = append(parts, d.buildHistory()...)
	return container.NewVBox(parts...)
}

// buildHistory renders the peaks table and chart for the selected span. This is
// the part that answers "when did I recently come close to a limit?".
func (d *Dashboard) buildHistory() []fyne.CanvasObject {
	env := d.env
	since := time.Now().Add(-d.span)

	rows, err := env.Store.ReadingRange(env.Ctx, since)
	if err != nil {
		return []fyne.CanvasObject{widget.NewLabel("Could not read history: " + err.Error())}
	}
	keys, err := env.Store.LimitKeysSince(env.Ctx, since)
	if err != nil {
		return []fyne.CanvasObject{widget.NewLabel("Could not read history: " + err.Error())}
	}

	maxGap := store.GapTolerance(rows)
	peaks := store.Peaks(rows, maxGap)

	header := container.NewHBox(
		widget.NewLabelWithStyle("Recent history", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		d.spanButton("24h", 24*time.Hour),
		d.spanButton("7d", 7*24*time.Hour),
		d.spanButton("30d", 30*24*time.Hour),
	)

	out := []fyne.CanvasObject{header}
	if len(peaks) == 0 {
		out = append(out, widget.NewLabel("No history in this window yet."))
		return out
	}

	out = append(out, buildCloseCalls(peaks, d.span)...)

	img := canvas.NewImageFromImage(renderChart(chartInput{
		Rows:       rows,
		Keys:       keys,
		Peaks:      peaks,
		Window:     d.span,
		MaxGap:     maxGap,
		Background: theme.Color(theme.ColorNameBackground),
		Foreground: theme.Color(theme.ColorNameForeground),
	}, 660, 250))
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(560, 250))
	out = append(out, buildChartLegend(keys), img)

	return out
}

// buildChartLegend renders the series key as Fyne widgets rather than inside the
// chart PNG: go-chart's legend hard-codes a white box, and native widgets also
// stay sharp instead of being scaled with the image.
func buildChartLegend(keys []api.Limit) fyne.CanvasObject {
	items := make([]fyne.CanvasObject, 0, len(keys)*2)
	for i, k := range keys {
		swatch := canvas.NewRectangle(paletteColor(i))
		swatch.SetMinSize(fyne.NewSize(16, 3))
		swatch.CornerRadius = 1.5

		label := widget.NewLabel(k.Label())
		label.TextStyle = fyne.TextStyle{Monospace: false}
		items = append(items, container.NewHBox(container.NewCenter(swatch), label))
	}
	// A single row sized to the labels: a fixed-width wrapping grid broke onto a
	// second line, and sizeToContent widens the window to fit this anyway.
	return container.NewHBox(items...)
}

func (d *Dashboard) spanButton(label string, span time.Duration) *widget.Button {
	b := widget.NewButton(label, func() {
		d.span = span
		d.Rebuild()
	})
	if d.span == span {
		b.Importance = widget.HighImportance
	}
	return b
}

// buildCloseCalls answers "when did I recently come close to a limit?".
//
// Only limits that actually crossed the high-water threshold get a row: within a
// single reset cycle a cumulative limit peaks at its current value, so a
// per-limit peak table restates the bars above it. When nothing came close, one
// sentence says so and names the highest limit for the headline number.
func buildCloseCalls(peaks []store.LimitPeak, span time.Duration) []fyne.CanvasObject {
	var close []store.LimitPeak
	for _, p := range peaks {
		if p.Limit.Percent >= store.HighWaterThreshold {
			close = append(close, p)
		}
	}

	if len(close) == 0 {
		msg := fmt.Sprintf("Nothing came within %.0f%% in the last %s.",
			store.HighWaterThreshold, humanSpanName(span))
		out := []fyne.CanvasObject{widget.NewLabel(msg)}
		// peaks is sorted worst-first, so the head is the highest limit.
		if len(peaks) > 0 {
			out = append(out, widget.NewLabel(fmt.Sprintf("Highest: %s at %.0f%%.",
				peaks[0].Limit.Label(), peaks[0].Limit.Percent)))
		}
		return out
	}

	grid := container.New(layout.NewGridLayoutWithColumns(3))
	for _, p := range close {
		name := widget.NewLabel(p.Limit.Label())
		name.TextStyle = fyne.TextStyle{Bold: true}

		hit := canvas.NewText(
			fmt.Sprintf("hit %.0f%% on %s", p.Limit.Percent, humanWhen(p.At, span)),
			colorForPct(p.Limit.Percent))
		hit.TextStyle = fyne.TextStyle{Bold: true}

		grid.Add(name)
		grid.Add(hit)
		grid.Add(widget.NewLabel(aboveText(p.AboveHigh)))
	}
	return []fyne.CanvasObject{grid}
}

// aboveText describes how long a limit stayed high. A lone reading above the
// threshold accumulates no duration, and must not be reported as "never above".
func aboveText(d time.Duration) string {
	if d <= 0 {
		return "one reading"
	}
	return humanSpan(d)
}

// humanSpanName names the selected window for prose.
func humanSpanName(span time.Duration) string {
	switch {
	case span <= 24*time.Hour:
		return "24 hours"
	case span <= 7*24*time.Hour:
		return "7 days"
	default:
		return "30 days"
	}
}

// buildLimitRow renders one limit compactly: label and reset on one line, with
// the bar beneath. The previous 36pt number dominated the layout while carrying
// less information than the reset countdown next to it.
// detail overrides the trailing text when a limit has something more useful to
// say than a reset countdown (credit spend reports money).
func buildLimitRow(l api.Limit, detail string) (fyne.CanvasObject, func()) {
	name := widget.NewLabel(l.Label())

	pct := canvas.NewText(fmt.Sprintf("%.0f%%", l.Percent), colorForPct(l.Percent))
	pct.TextSize = 16
	pct.TextStyle = fyne.TextStyle{Bold: true}

	reset := widget.NewLabel("")
	var update func()
	switch {
	case detail != "":
		reset.SetText(detail)
	case !l.ResetsAt.IsZero():
		at := l.ResetsAt
		update = func() { reset.SetText("resets " + humanReset(at)) }
		update()
	default:
		reset.SetText("no reset window")
	}

	top := container.NewBorder(nil, nil, name, container.NewHBox(pct, reset))
	return container.NewVBox(top, newProgressBar(l.Percent)), update
}

// agoText renders an elapsed duration as a phrase that already reads correctly,
// so callers don't append "ago" onto "just now".
func agoText(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return compactDuration(d) + " ago"
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

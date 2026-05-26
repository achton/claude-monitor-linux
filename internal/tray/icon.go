package tray

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/systray"
)

//go:embed assets/icon-64.png
var brandIconPNG []byte

func (st *state) refreshIcon() {
	if st.desk == nil {
		return
	}
	v, ok := st.iconNumbers()
	var iconBytes []byte
	tooltip := "Claude Monitor — no data yet"
	if !ok {
		iconBytes = brandIconPNG
	} else {
		iconBytes = renderDuoBarIcon(v.sessionUsage, v.weeklyUsage)
		tooltip = tooltipFor(v)
	}
	res := fyne.NewStaticResource("claude-monitor-tray", iconBytes)
	fyne.Do(func() {
		systray.SetTooltip(tooltip)
		st.desk.SetSystemTrayIcon(res)
	})
}

type iconValues struct {
	sessionUsage   float64
	weeklyUsage    float64
	sessionResetAt time.Time
	weeklyResetAt  time.Time
	accountName    string
}

func (st *state) iconNumbers() (iconValues, bool) {
	var v iconValues
	if st.env == nil || st.env.Store == nil {
		return v, false
	}
	ctx, cancel := context.WithTimeout(st.ctx, 2*time.Second)
	defer cancel()

	rec, err := st.env.Store.LatestUsage(ctx)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return v, false
	}
	v.sessionUsage = valOrZero(rec.SessionPercent)
	v.weeklyUsage = valOrZero(rec.WeeklyPercent)
	if rec.SessionReset.Valid {
		if t, ok := parseISOTime(rec.SessionReset.String); ok {
			v.sessionResetAt = t
		}
	}
	if rec.WeeklyReset.Valid {
		if t, ok := parseISOTime(rec.WeeklyReset.String); ok {
			v.weeklyResetAt = t
		}
	}
	label, _, _, _ := st.env.Poller.Status()
	if label == "" {
		label = "Claude Code"
	}
	v.accountName = label
	return v, true
}

func parseISOTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// renderDuoBarIcon draws a square-ish icon with two wide vertical bars:
// 5h session usage on the left, 7d weekly usage on the right.
func renderDuoBarIcon(sessionPct, weeklyPct float64) []byte {
	const (
		W, H      = 32, 32
		barTop    = 4
		barBottom = 28
		barAreaH  = barBottom - barTop
		barWidth  = 11
	)
	xs := [2]int{3, W - 3 - barWidth}
	values := [2]float64{sessionPct, weeklyPct}
	fills := [2]color.Color{colorForUsage(sessionPct), colorForUsage(weeklyPct)}

	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.Transparent}, image.Point{}, draw.Src)

	for i, x := range xs {
		fillRect(img, x, barTop, x+barWidth, barBottom, trackColor)
		frac := math.Max(0, math.Min(1, values[i]/100))
		fillH := int(math.Round(float64(barAreaH) * frac))
		if fillH > 0 {
			fillRect(img, x, barBottom-fillH, x+barWidth, barBottom, fills[i])
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func tooltipFor(v iconValues) string {
	return fmt.Sprintf("Claude Monitor — %s\nSession (5h): %.0f%% · resets %s\nWeekly  (7d): %.0f%% · resets %s",
		v.accountName,
		v.sessionUsage, humanResetCompact(v.sessionResetAt),
		v.weeklyUsage, humanResetCompact(v.weeklyResetAt),
	)
}

func humanResetCompact(t time.Time) string {
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

var trackColor = color.NRGBA{R: 0xB0, G: 0xAE, B: 0xA5, A: 0x55}

func colorForUsage(pct float64) color.Color {
	switch {
	case pct >= 95:
		return color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0xFF}
	case pct >= 90:
		return color.NRGBA{R: 0xD9, G: 0x77, B: 0x57, A: 0xFF}
	default:
		return color.NRGBA{R: 0x78, G: 0x8C, B: 0x5D, A: 0xFF}
	}
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y, c)
		}
	}
}

func valOrZero(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}

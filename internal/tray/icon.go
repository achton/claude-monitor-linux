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

// Anthropic-defined window lengths for the unified rate limits.
const (
	sessionWindow = 5 * time.Hour
	weeklyWindow  = 7 * 24 * time.Hour
)

// brandIconPNG is the official Anthropic-palette tray icon used when no usage
// data is available yet (right after install, before the first poll completes).
//
//go:embed assets/icon-64.png
var brandIconPNG []byte

// refreshIcon recomputes and applies the tray icon and tooltip.
//
// When no data is available, shows the brand icon. With data, renders a
// two-bar visual (5h usage on the left, 7d usage on the right) and sets a
// multi-line tooltip with the exact percentages + reset times.
//
// Callable from any goroutine — the systray + Fyne mutations are marshalled
// onto the Fyne goroutine via fyne.Do.
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

// iconValues holds the values used to render the icon + tooltip.
type iconValues struct {
	sessionUsage     float64 // 0–100 (quota used)
	weeklyUsage      float64
	sessionResetAt   time.Time
	weeklyResetAt    time.Time
	sessionTimeRem   float64 // 0–100, fraction of 5h window left (used in tooltip)
	weeklyTimeRem    float64
	accountName      string
}

// iconNumbers fetches the values for the active account.
func (st *state) iconNumbers() (iconValues, bool) {
	var v iconValues
	if st.env == nil || st.env.Store == nil {
		return v, false
	}
	ctx, cancel := context.WithTimeout(st.ctx, 2*time.Second)
	defer cancel()
	pin := st.env.Store.GetSettingDefault(ctx, "tray_pinned_account", "")
	var acct string
	if pin != "" {
		acct = pin
	} else {
		accs, _ := st.env.Store.ListAccounts(ctx)
		if len(accs) > 0 {
			acct = accs[0].ID
			v.accountName = accs[0].DisplayName()
		}
	}
	if acct == "" {
		return v, false
	}
	if v.accountName == "" {
		if a, err := st.env.Store.GetAccount(ctx, acct); err == nil {
			v.accountName = a.DisplayName()
		}
	}
	rec, err := st.env.Store.LatestUsage(ctx, acct)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return v, false
	}
	v.sessionUsage = valOrZero(rec.SessionPercent)
	v.weeklyUsage = valOrZero(rec.WeeklyAllPercent)
	if rec.SessionReset.Valid {
		if t, ok := parseISOTime(rec.SessionReset.String); ok {
			v.sessionResetAt = t
		}
		v.sessionTimeRem = remainingPercent(rec.SessionReset.String, sessionWindow)
	}
	if rec.WeeklyReset.Valid {
		if t, ok := parseISOTime(rec.WeeklyReset.String); ok {
			v.weeklyResetAt = t
		}
		v.weeklyTimeRem = remainingPercent(rec.WeeklyReset.String, weeklyWindow)
	}
	return v, true
}

// remainingPercent returns "time until reset" as a percent of the fixed window.
func remainingPercent(iso string, window time.Duration) float64 {
	t, ok := parseISOTime(iso)
	if !ok {
		return 0
	}
	d := time.Until(t)
	if d <= 0 {
		return 0
	}
	if d >= window {
		return 100
	}
	return float64(d) / float64(window) * 100
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
//
// Canvas is 32×32 (KDE-friendly), bars take ~75% of the height. Color follows
// the green / Claude orange / brick red threshold ladder per bar.
func renderDuoBarIcon(sessionPct, weeklyPct float64) []byte {
	const (
		W, H      = 32, 32
		barTop    = 4
		barBottom = 28
		barAreaH  = barBottom - barTop // 24
		barWidth  = 11
	)
	xs := [2]int{3, W - 3 - barWidth} // 3..14 and 18..29
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

// tooltipFor returns a multi-line tooltip describing the four key values.
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

var trackColor = color.NRGBA{R: 0xB0, G: 0xAE, B: 0xA5, A: 0x55} // Mid Gray @ 33%

// colorForUsage returns the threshold-aware color for usage bars.
func colorForUsage(pct float64) color.Color {
	switch {
	case pct >= 95:
		return color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0xFF} // critical (brick red)
	case pct >= 90:
		return color.NRGBA{R: 0xD9, G: 0x77, B: 0x57, A: 0xFF} // Claude orange (caution)
	default:
		return color.NRGBA{R: 0x78, G: 0x8C, B: 0x5D, A: 0xFF} // Anthropic green
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


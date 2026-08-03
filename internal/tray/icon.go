package tray

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
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

// trayTooltip is deliberately static.
const trayTooltip = "Claude Monitor"

func (st *state) refreshIcon() {
	if st.desk == nil {
		return
	}
	v, ok := st.iconNumbers()
	iconBytes := brandIconPNG
	if ok {
		iconBytes = renderDuoBarIcon(v.sessionUsage, v.weeklyUsage)
	}
	// Push the icon only when the pixels actually change: some SNI hosts flicker
	// on every SetSystemTrayIcon.
	st.iconMu.Lock()
	iconChanged := !bytes.Equal(iconBytes, st.lastIcon)
	if iconChanged {
		st.lastIcon = bytes.Clone(iconBytes)
	}
	st.iconMu.Unlock()

	fyne.Do(func() {
		// The tooltip is just the app name: clicking the icon opens the menu,
		// which already lists every limit with its countdown.
		systray.SetTooltip(trayTooltip)
		if iconChanged {
			st.desk.SetSystemTrayIcon(fyne.NewStaticResource("claude-monitor-tray", iconBytes))
		}
	})
}

// iconValues carries exactly what the icon draws: two bars.
type iconValues struct {
	sessionUsage float64
	weeklyUsage  float64
}

func (st *state) iconNumbers() (iconValues, bool) {
	var v iconValues
	if st.env == nil || st.env.Store == nil {
		return v, false
	}
	ctx, cancel := context.WithTimeout(st.ctx, 2*time.Second)
	defer cancel()

	rec, err := st.env.Store.LatestReading(ctx)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return v, false
	}
	if l, ok := rec.Session(); ok {
		v.sessionUsage = l.Percent
	}
	if l, ok := rec.Weekly(); ok {
		v.weeklyUsage = l.Percent
	}
	return v, true
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

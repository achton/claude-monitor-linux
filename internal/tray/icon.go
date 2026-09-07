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
		iconBytes = renderUsageIcon(v)
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

// iconValues carries exactly what the icon draws: two vertical bars and, when
// the account has a model-scoped weekly limit in use, the rail under them.
type iconValues struct {
	sessionUsage float64
	weeklyUsage  float64
	scopedUsage  float64
	hasScoped    bool
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
	// A scoped limit sitting at 0% earns no rail: the icon stays a clean pair of
	// bars until the scoped model is actually used.
	if l, ok := rec.WeeklyScoped(); ok && l.Percent > 0 {
		v.scopedUsage = l.Percent
		v.hasScoped = true
	}
	return v, true
}

// Icon geometry. Two vertical bars carry the 5h session (left) and 7d weekly
// (right) limits; the model-scoped weekly limit is a horizontal rail below
// them, filling left to right. Orientation, not colour, is what separates the
// three at 22px: colour already encodes severity. With no scoped limit, or one
// still at 0%, the bars use the full height and the rail is left out, which is
// the icon this app drew before scoped limits existed.
const (
	iconW, iconH  = 32, 32
	barPadX       = 3
	barWidth      = 11
	barTop        = 2
	barBottom     = 23
	railTop       = 26
	railBottom    = 30
	fullBarTop    = 4
	fullBarBottom = 28
)

// renderUsageIcon draws the tray icon as a PNG.
func renderUsageIcon(v iconValues) []byte {
	img := image.NewRGBA(image.Rect(0, 0, iconW, iconH))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.Transparent}, image.Point{}, draw.Src)

	top, bottom := barTop, barBottom
	if !v.hasScoped {
		top, bottom = fullBarTop, fullBarBottom
	}
	xs := [2]int{barPadX, iconW - barPadX - barWidth}
	values := [2]float64{v.sessionUsage, v.weeklyUsage}
	for i, x := range xs {
		drawBar(img, x, top, x+barWidth, bottom, values[i], fillUp)
	}
	if v.hasScoped {
		drawBar(img, barPadX, railTop, iconW-barPadX, railBottom, v.scopedUsage, fillRight)
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// fillDirection says which edge a bar's fill grows from.
type fillDirection int

const (
	fillUp fillDirection = iota
	fillRight
)

// drawBar paints one track and its fill, clamping the value to 0–100.
func drawBar(img *image.RGBA, x0, y0, x1, y1 int, pct float64, dir fillDirection) {
	fillRect(img, x0, y0, x1, y1, trackColor)
	frac := math.Max(0, math.Min(1, pct/100))
	c := colorForUsage(pct)
	switch dir {
	case fillUp:
		h := int(math.Round(float64(y1-y0) * frac))
		if h > 0 {
			fillRect(img, x0, y1-h, x1, y1, c)
		}
	case fillRight:
		w := int(math.Round(float64(x1-x0) * frac))
		if w > 0 {
			fillRect(img, x0, y0, x0+w, y1, c)
		}
	}
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

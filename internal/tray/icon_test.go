package tray

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func decodeIcon(t *testing.T, v iconValues) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(renderUsageIcon(v)))
	if err != nil {
		t.Fatalf("decode icon: %v", err)
	}
	return img
}

// opaqueRunFrom counts consecutive opaque-ish pixels along row y from x0.
func filledWidth(img image.Image, y, x0, x1 int) int {
	n := 0
	for x := x0; x < x1; x++ {
		_, _, _, a := img.At(x, y).RGBA()
		if a < 0xF000 {
			break
		}
		n++
	}
	return n
}

func rowIsEmpty(img image.Image, y int) bool {
	for x := 0; x < iconW; x++ {
		if _, _, _, a := img.At(x, y).RGBA(); a != 0 {
			return false
		}
	}
	return true
}

// railGapX is a column between the two bars, so it only carries pixels when
// the rail is drawn.
const railGapX = barPadX + barWidth + 1

func TestIconOmitsRailWithoutScopedLimit(t *testing.T) {
	img := decodeIcon(t, iconValues{sessionUsage: 50, weeklyUsage: 20})
	for y := railTop; y < railBottom; y++ {
		if _, _, _, a := img.At(railGapX, y).RGBA(); a != 0 {
			t.Fatalf("rail row %d drawn without a scoped limit", y)
		}
	}
	// The bars take the full height instead.
	if _, _, _, a := img.At(barPadX, fullBarBottom-1).RGBA(); a == 0 {
		t.Fatal("session bar missing at the full-height baseline")
	}
}

func TestIconDrawsRailForScopedLimit(t *testing.T) {
	img := decodeIcon(t, iconValues{sessionUsage: 50, weeklyUsage: 20, scopedUsage: 0, hasScoped: true})
	for y := railTop; y < railBottom; y++ {
		if _, _, _, a := img.At(railGapX, y).RGBA(); a == 0 {
			t.Fatalf("rail row %d missing its track", y)
		}
	}
	if _, _, _, a := img.At(barPadX, barBottom).RGBA(); a != 0 {
		t.Error("the session bar runs past its shortened baseline")
	}
}

func TestIconRailTracksScopedPercent(t *testing.T) {
	y := railTop + 1
	railWidth := iconW - 2*barPadX
	cases := []struct {
		pct  float64
		want int
	}{
		{0, 0},
		{50, railWidth / 2},
		{100, railWidth},
	}
	for _, c := range cases {
		img := decodeIcon(t, iconValues{sessionUsage: 10, weeklyUsage: 10, scopedUsage: c.pct, hasScoped: true})
		got := filledWidth(img, y, barPadX, iconW-barPadX)
		if got != c.want {
			t.Errorf("scoped %.0f%%: rail fill %d px, want %d", c.pct, got, c.want)
		}
	}
}

func TestIconRailIsSeparateFromTheBars(t *testing.T) {
	img := decodeIcon(t, iconValues{sessionUsage: 100, weeklyUsage: 100, scopedUsage: 100, hasScoped: true})
	for y := barBottom; y < railTop; y++ {
		if !rowIsEmpty(img, y) {
			t.Fatalf("row %d between the bars and the rail is not clear", y)
		}
	}
}

func TestIconClampsOutOfRangePercent(t *testing.T) {
	img := decodeIcon(t, iconValues{sessionUsage: -5, weeklyUsage: 240, scopedUsage: 240, hasScoped: true})
	if got := filledWidth(img, railTop, barPadX, iconW-barPadX); got != iconW-2*barPadX {
		t.Errorf("rail fill %d px for 240%%, want %d", got, iconW-2*barPadX)
	}
	if _, _, _, a := img.At(barPadX, barTop).RGBA(); a >= 0xF000 {
		t.Error("negative session percent drew a fill")
	}
}

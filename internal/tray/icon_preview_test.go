package tray

import (
	"fmt"
	"os"
	"testing"
)

// TestRenderQuadBarSamples writes a set of sample PNGs to /tmp/cm-icon-preview
// for visual inspection. Skipped unless CM_PREVIEW=1 is set.
func TestRenderQuadBarSamples(t *testing.T) {
	if os.Getenv("CM_PREVIEW") == "" {
		t.Skip("CM_PREVIEW=1 not set")
	}
	cases := []struct {
		name           string
		sessionUsage   float64
		weeklyUsage    float64
	}{
		{"01-fresh-low", 10, 5},
		{"02-current-real", 53, 7},
		{"03-mid", 60, 50},
		{"04-session-near-limit", 92, 40},
		{"05-session-critical", 96, 60},
		{"06-weekly-near-limit", 50, 92},
		{"07-weekly-critical", 30, 97},
		{"08-both-critical", 96, 96},
		{"09-zero", 0, 0},
		{"10-full", 100, 100},
	}
	if err := os.MkdirAll("/tmp/cm-icon-preview", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		b := renderDuoBarIcon(c.sessionUsage, c.weeklyUsage)
		path := fmt.Sprintf("/tmp/cm-icon-preview/%s.png", c.name)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

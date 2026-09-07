package tray

import (
	"fmt"
	"os"
	"testing"
)

// TestRenderIconSamples writes a set of sample PNGs to /tmp/cm-icon-preview
// for visual inspection. Skipped unless CM_PREVIEW=1 is set.
func TestRenderIconSamples(t *testing.T) {
	if os.Getenv("CM_PREVIEW") == "" {
		t.Skip("CM_PREVIEW=1 not set")
	}
	cases := []struct {
		name string
		v    iconValues
	}{
		{"01-fresh-low", iconValues{10, 5, 3, true}},
		{"02-current-real", iconValues{53, 7, 41, true}},
		{"03-mid", iconValues{60, 50, 50, true}},
		{"04-session-near-limit", iconValues{92, 40, 20, true}},
		{"05-session-critical", iconValues{96, 60, 30, true}},
		{"06-weekly-near-limit", iconValues{50, 92, 44, true}},
		{"07-weekly-critical", iconValues{30, 97, 60, true}},
		{"08-scoped-critical", iconValues{40, 30, 97, true}},
		{"09-all-critical", iconValues{96, 96, 96, true}},
		{"10-scoped-just-started", iconValues{30, 20, 2, true}},
		{"11-zero", iconValues{0, 0, 0, false}},
		{"12-full", iconValues{100, 100, 100, true}},
		{"13-no-scoped-limit", iconValues{53, 7, 0, false}},
	}
	if err := os.MkdirAll("/tmp/cm-icon-preview", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		b := renderUsageIcon(c.v)
		path := fmt.Sprintf("/tmp/cm-icon-preview/%s.png", c.name)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

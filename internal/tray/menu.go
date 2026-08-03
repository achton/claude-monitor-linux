package tray

import (
	"context"
	"fmt"
	"slices"
	"time"

	"fyne.io/fyne/v2"
)

// rebuildMenu re-creates the system tray menu and applies it via the desktop.App
// integration.
//
// Callable from any goroutine — SetSystemTrayMenu is marshalled onto the
// Fyne goroutine via fyne.Do.
func (st *state) rebuildMenu() { st.applyMenu(true) }

// rebuildMenuIfChanged re-pushes the menu only when its text would differ.
// The header carries minute-granularity countdowns, so a frequent ticker would
// otherwise replace the menu constantly — and replacing it while the user has
// it open can dismiss it.
func (st *state) rebuildMenuIfChanged() { st.applyMenu(false) }

func (st *state) applyMenu(force bool) {
	if st.desk == nil {
		return
	}

	lines := st.currentStatusLines()
	st.menuMu.Lock()
	unchanged := slices.Equal(lines, st.menuLines)
	if !force && unchanged {
		st.menuMu.Unlock()
		return
	}
	st.menuLines = slices.Clone(lines)
	st.menuMu.Unlock()

	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open Claude Monitor", st.focusAccountList),
	}

	if len(lines) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		for _, line := range lines {
			header := fyne.NewMenuItem(line, nil)
			header.Disabled = true
			items = append(items, header)
		}
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Refresh now", func() {
			go func() {
				ctx, cancel := context.WithTimeout(st.ctx, 30*time.Second)
				defer cancel()
				_ = st.env.Poller.PollNow(ctx)
				st.refreshIcon()
				st.rebuildMenu()
			}()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit Claude Monitor", func() {
			fyne.Do(st.app.Quit)
		}),
	)

	menu := fyne.NewMenu("Claude Monitor", items...)
	fyne.Do(func() {
		st.desk.SetSystemTrayMenu(menu)
	})
}

// currentStatusLines returns one disabled menu item per reported limit, so the
// menu shows everything the API reports rather than a fixed session/weekly
// pair. Returns nil when there is no data. The last line gains a "⚠ stale"
// suffix when the latest poll failed.
func (st *state) currentStatusLines() []string {
	rec, err := st.env.Store.LatestReading(st.ctx)
	if err != nil || len(rec.Limits) == 0 {
		return nil
	}

	width := 0
	for _, l := range rec.Limits {
		if n := len([]rune(l.Label())); n > width {
			width = n
		}
	}
	var lines []string
	for _, l := range rec.Limits {
		line := fmt.Sprintf("%-*s  %3.0f%%", width, l.Label(), l.Percent)
		if !l.ResetsAt.IsZero() {
			line += "   ·   resets " + humanReset(l.ResetsAt)
		}
		lines = append(lines, line)
	}

	if _, lastErr, _, _ := st.env.Poller.Status(); lastErr != "" {
		lines = append(lines, "⚠ polls failing — numbers are stale")
	}
	return lines
}

func humanReset(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours()/24), int(d.Hours())%24)
}

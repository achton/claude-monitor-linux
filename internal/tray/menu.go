package tray

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
)

// rebuildMenu re-creates the system tray menu and applies it via the desktop.App
// integration.
//
// Callable from any goroutine — SetSystemTrayMenu is marshalled onto the
// Fyne goroutine via fyne.Do.
func (st *state) rebuildMenu() {
	if st.desk == nil {
		return
	}

	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open Claude Monitor", st.focusAccountList),
	}

	if line := st.currentStatusLine(); line != "" {
		header := fyne.NewMenuItem(line, nil)
		header.Disabled = true
		items = append(items, fyne.NewMenuItemSeparator(), header)
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Refresh now", func() {
			go func() {
				ctx, cancel := context.WithTimeout(st.ctx, 30*1e9)
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

// currentStatusLine returns a short human-readable summary of the active
// account's usage for the tray menu header. Returns "" when there is no data.
// Appends a "· ⚠ stale" suffix when the latest poll failed.
func (st *state) currentStatusLine() string {
	rec, err := st.env.Store.LatestUsage(st.ctx)
	if err != nil {
		return ""
	}
	sess := 0
	weekly := 0
	if rec.SessionPercent.Valid {
		sess = int(rec.SessionPercent.Float64 + 0.5)
	}
	if rec.WeeklyPercent.Valid {
		weekly = int(rec.WeeklyPercent.Float64 + 0.5)
	}
	line := fmt.Sprintf("Session %d%%   ·   Weekly %d%%", sess, weekly)

	_, lastErr, _, _ := st.env.Poller.Status()
	if lastErr != "" {
		line += "   ·   ⚠ stale"
	}
	return line
}

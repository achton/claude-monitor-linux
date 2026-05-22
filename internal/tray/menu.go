package tray

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
)

// rebuildMenu re-creates the system tray menu and applies it via the desktop.App
// integration. Kept deliberately small — multi-account complexity lives in
// the GUI Settings → Manage accounts dialog, not inline here.
func (st *state) rebuildMenu() {
	if st.desk == nil {
		return
	}

	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open Claude Monitor", st.focusAccountList),
	}

	// One-line read-only status header, when we have data.
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
				_, _ = st.env.Poller.PollAll(ctx)
				st.refreshIcon()
				st.rebuildMenu()
			}()
		}),
		fyne.NewMenuItem("Add account…", st.openAddAccount),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit Claude Monitor", func() {
			st.app.Quit()
		}),
	)

	menu := fyne.NewMenu("Claude Monitor", items...)
	st.desk.SetSystemTrayMenu(menu)
}

// currentStatusLine returns a short human-readable summary of the active
// account's usage for the tray menu header. Returns "" when there is no data.
func (st *state) currentStatusLine() string {
	accs, _ := st.env.Store.ListAccounts(st.ctx)
	if len(accs) == 0 {
		return ""
	}
	pin := st.env.Store.GetSettingDefault(st.ctx, "tray_pinned_account", "")
	target := accs[0]
	if pin != "" {
		for _, a := range accs {
			if a.ID == pin {
				target = a
				break
			}
		}
	}
	rec, err := st.env.Store.LatestUsage(st.ctx, target.ID)
	if err != nil || !rec.PrimaryPercent.Valid {
		return ""
	}
	sess := 0
	weekly := 0
	if rec.SessionPercent.Valid {
		sess = int(rec.SessionPercent.Float64 + 0.5)
	}
	if rec.WeeklyAllPercent.Valid {
		weekly = int(rec.WeeklyAllPercent.Float64 + 0.5)
	}
	line := fmt.Sprintf("Session %d%%   ·   Weekly %d%%", sess, weekly)

	cred, err := st.env.Store.GetCredentialByAccountID(st.ctx, target.ID)
	if err == nil && cred.LastError.Valid && cred.LastError.String != "" {
		line += "   ·   ⚠ stale"
	}
	return line
}

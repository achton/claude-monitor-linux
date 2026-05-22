// Package ui implements Fyne windows used by the tray.
//
// Constructors take an existing fyne.App; they never call app.New(). This is
// part of the headless-CLI safety invariant (DESIGN.md §11): even if ui is
// imported from a non-tray path, no fyne side effects fire until the tray
// itself constructs the App.
package ui

import (
	"database/sql"
	"fmt"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// NewAccountListWindow builds the main dashboard. Despite the name (kept for
// call-site stability), it is a single-account dense view with an inline
// history chart; multi-account switching is a side path that only surfaces
// when there are >= 2 accounts.
func NewAccountListWindow(app fyne.App, env *cli.Env) fyne.Window {
	w := app.NewWindow("Claude Monitor")
	w.Resize(fyne.NewSize(560, 640))

	body := container.NewVBox()

	var refresh func()
	refresh = func() {
		body.RemoveAll()
		accs, err := env.Store.ListAccounts(env.Ctx)
		if err != nil {
			body.Add(widget.NewLabel("Error: " + err.Error()))
			return
		}
		if len(accs) == 0 {
			body.Add(buildEmptyState(app, env, refresh))
			return
		}
		pin := env.Store.GetSettingDefault(env.Ctx, "tray_pinned_account", "")
		active := pickActiveAccount(accs, pin)
		body.Add(buildDashboard(app, env, active, accs, refresh))
	}
	refresh()

	footer := container.NewHBox(
		widget.NewButton("Refresh", func() {
			go func() {
				_, _ = env.Poller.PollAll(env.Ctx)
				refresh()
			}()
		}),
		widget.NewButton("Settings", func() {
			NewSettingsWindow(app, env, refresh).Show()
		}),
	)
	w.SetContent(container.NewBorder(nil, footer, nil, nil, container.NewPadded(body)))
	return w
}

func pickActiveAccount(accs []store.Account, pinID string) store.Account {
	if pinID != "" {
		for _, a := range accs {
			if a.ID == pinID {
				return a
			}
		}
	}
	return accs[0]
}

// buildDashboard renders the dense single-account view.
func buildDashboard(app fyne.App, env *cli.Env, active store.Account, all []store.Account, refresh func()) fyne.CanvasObject {
	rec, _ := env.Store.LatestUsage(env.Ctx, active.ID)
	sessionPct := valOrZero(rec.SessionPercent)
	weeklyPct := valOrZero(rec.WeeklyAllPercent)
	sessionReset := parseISOOrZero(rec.SessionReset)
	weeklyReset := parseISOOrZero(rec.WeeklyReset)

	cred, _ := env.Store.GetCredentialByAccountID(env.Ctx, active.ID)

	parts := []fyne.CanvasObject{}

	// Polling-health banner — only when we have a real last_error.
	if cred.LastError.Valid && cred.LastError.String != "" {
		parts = append(parts, buildPollErrorBanner(app, env, active, cred, rec, refresh))
	}

	// Multi-account selector — only when relevant.
	if len(all) > 1 {
		names := make([]string, 0, len(all))
		byName := map[string]string{}
		for _, a := range all {
			names = append(names, a.DisplayName())
			byName[a.DisplayName()] = a.ID
		}
		sel := widget.NewSelect(names, func(name string) {
			if id, ok := byName[name]; ok {
				v := id
				_ = env.Store.SetSetting(env.Ctx, "tray_pinned_account", &v)
				refresh()
			}
		})
		sel.SetSelected(active.DisplayName())
		parts = append(parts,
			container.NewBorder(nil, nil, widget.NewLabel("Account"), nil, sel),
			widget.NewSeparator(),
		)
	} else {
		// Single-account: a discreet name header instead of the selector.
		name := widget.NewLabelWithStyle(active.DisplayName(),
			fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		parts = append(parts, name)
	}

	parts = append(parts,
		buildMetricBlock("Session (5h)", sessionPct, sessionReset),
		buildMetricBlock("Weekly (7d)", weeklyPct, weeklyReset),
	)

	// Inline history chart — directly inside the dashboard, no separate window.
	parts = append(parts, NewChartPane(env, active, fyne.NewSize(520, 220)))

	renameBtn := widget.NewButton("✏ Rename", func() {
		showRenameDialog(app, env, active)
	})
	addBtn := widget.NewButton("➕ Add another account", func() {
		NewAddAccountWindow(app, env, refresh).Show()
	})
	parts = append(parts, container.NewHBox(renameBtn, addBtn))

	return container.NewVBox(parts...)
}

// buildMetricBlock renders one dimension (session or weekly) — a big % number,
// a label + reset countdown to the right, then the bar below.
func buildMetricBlock(title string, pct float64, reset time.Time) fyne.CanvasObject {
	big := canvas.NewText(fmt.Sprintf("%.0f%%", pct), theme.ForegroundColor())
	big.TextSize = 36
	big.TextStyle = fyne.TextStyle{Bold: true}
	big.Color = colorForPct(pct)

	header := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	resetTxt := widget.NewLabel("resets " + humanReset(reset))
	right := container.NewVBox(header, resetTxt)

	row := container.NewBorder(nil, nil, big, nil, right)
	return container.NewVBox(row, newProgressBar(pct), widget.NewSeparator())
}

func buildEmptyState(app fyne.App, env *cli.Env, refresh func()) fyne.CanvasObject {
	headline := widget.NewLabelWithStyle("No Claude account yet",
		fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel("Add one to start tracking quota usage.")
	addBtn := widget.NewButton("Add account…", func() {
		NewAddAccountWindow(app, env, refresh).Show()
	})
	return container.NewVBox(headline, hint, addBtn)
}

// newProgressBar creates a colored horizontal progress bar at the given %.
// Custom layout (no HBox-in-Stack) — see fix in v0.1.2.
func newProgressBar(pct float64) fyne.CanvasObject {
	const h float32 = 10
	bg := canvas.NewRectangle(color.NRGBA{R: 0xB0, G: 0xAE, B: 0xA5, A: 0x40}) // Mid Gray @ 25%
	fill := canvas.NewRectangle(colorForPct(pct))
	frac := math.Max(0, math.Min(1, pct/100))
	return container.New(&progressBarLayout{frac: frac, height: h}, bg, fill)
}

type progressBarLayout struct {
	frac   float64
	height float32
}

func (l *progressBarLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(200, l.height)
}

func (l *progressBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	bg, fill := objects[0], objects[1]
	bg.Resize(size)
	bg.Move(fyne.NewPos(0, 0))
	fw := float32(l.frac) * size.Width
	fill.Resize(fyne.NewSize(fw, size.Height))
	fill.Move(fyne.NewPos(0, 0))
}

func colorForPct(pct float64) color.Color {
	switch {
	case pct >= 95:
		return color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0xFF} // critical
	case pct >= 90:
		return color.NRGBA{R: 0xD9, G: 0x77, B: 0x57, A: 0xFF} // Claude orange
	default:
		return color.NRGBA{R: 0x78, G: 0x8C, B: 0x5D, A: 0xFF} // Anthropic Green
	}
}

func humanReset(t time.Time) string {
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

func valOrZero(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}

func parseISOOrZero(s sql.NullString) time.Time {
	if !s.Valid {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s.String); err == nil {
			return t
		}
	}
	return time.Time{}
}

// buildPollErrorBanner renders the warning surface shown above the dashboard
// metrics when the active credential's most recent poll failed. The numbers
// below are stale — be honest about it.
func buildPollErrorBanner(app fyne.App, env *cli.Env, active store.Account, cred store.Credential, rec store.UsageRecord, refresh func()) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0x33})
	bg.StrokeColor = color.NRGBA{R: 0xC0, G: 0x3A, B: 0x24, A: 0xCC}
	bg.StrokeWidth = 1

	title := widget.NewLabelWithStyle("⚠  Polls failing — numbers below are stale",
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	lastGood := "last successful poll: never"
	if !rec.Timestamp.IsZero() {
		lastGood = fmt.Sprintf("last successful poll %s ago",
			compactDuration(time.Since(rec.Timestamp)))
	}
	body := fmt.Sprintf("%s\n%s", cred.LastError.String, lastGood)
	if cred.Source != "claude-code" {
		// Paste-only / env-imported account: there is no auto-refresh path.
		// Tell the user instead of leaving them to guess why no Re-import
		// button is offered.
		body += "\n\nThis account was added without Claude Code. Auto-refresh\n" +
			"isn't available — run `claude-monitor add-token` (or\n" +
			"`import-claude-code` if Claude Code is installed) with a fresh\n" +
			"token to recover."
	}
	detail := widget.NewLabel(body)
	detail.Wrapping = fyne.TextWrapWord

	actions := []fyne.CanvasObject{}
	if cred.Source == "claude-code" {
		actions = append(actions, widget.NewButton("Re-import token", func() {
			go func() {
				if _, err := env.Poller.ImportFromClaudeCode(env.Ctx, ""); err == nil {
					_ = env.Poller.PollAccount(env.Ctx, active.ID)
				}
				refresh()
			}()
		}))
	}
	actions = append(actions, widget.NewButton("Retry now", func() {
		go func() {
			_, _ = env.Poller.PollAll(env.Ctx)
			refresh()
		}()
	}))

	inner := container.NewVBox(title, detail, container.NewHBox(actions...))
	padded := container.NewPadded(inner)
	return container.NewStack(bg, padded)
}

func compactDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours()/24), int(d.Hours())%24)
}

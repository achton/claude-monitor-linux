package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/poller"
)

// NewAddAccountWindow builds the add-account window with two tabs plus a
// one-click "Import from Claude Code" button at the top (when applicable).
func NewAddAccountWindow(app fyne.App, env *cli.Env, onDone func()) fyne.Window {
	w := app.NewWindow("Add Claude Account")
	w.Resize(fyne.NewSize(520, 460))

	// Tab 1: Paste token.
	tokenInput := widget.NewMultiLineEntry()
	tokenInput.SetPlaceHolder("Paste your Claude Code OAuth access token (sk-ant-… or longer)")
	tokenInput.Wrapping = fyne.TextWrapBreak
	emailInput := widget.NewEntry()
	emailInput.SetPlaceHolder("Optional label/email")
	status := widget.NewLabel("")
	addBtn := widget.NewButton("Validate & Add", nil)
	addBtn.OnTapped = func() {
		tok := strings.TrimSpace(tokenInput.Text)
		if tok == "" {
			status.SetText("Paste a token first.")
			return
		}
		addBtn.Disable()
		status.SetText("Validating…")
		go func() {
			ctx, cancel := context.WithTimeout(env.Ctx, 30*time.Second)
			defer cancel()
			res, err := env.Poller.AddAccountWithToken(ctx, tok, strings.TrimSpace(emailInput.Text), "")
			addBtn.Enable()
			if err != nil {
				status.SetText("Failed: " + err.Error())
				return
			}
			status.SetText(fmt.Sprintf("Added: %s (org %s)", res.Label, res.OrgID))
			if onDone != nil {
				onDone()
			}
		}()
	}
	pasteTab := container.NewVBox(
		widget.NewLabelWithStyle("Paste an access token", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		tokenInput, emailInput, addBtn, status,
	)

	// Tab 2: Import .env.
	envStatus := widget.NewLabel("")
	envBtn := widget.NewButton("Choose .env file…", func() {
		fd := dialog.NewFileOpen(func(uc fyne.URIReadCloser, err error) {
			if err != nil || uc == nil {
				return
			}
			defer uc.Close()
			path := uc.URI().Path()
			f, err := os.Open(path)
			if err != nil {
				envStatus.SetText("open: " + err.Error())
				return
			}
			defer f.Close()
			results, ierr := env.Poller.ImportFromEnv(env.Ctx, f)
			if ierr != nil {
				envStatus.SetText("import: " + ierr.Error())
				return
			}
			ok, failed := 0, 0
			lines := []string{}
			for _, r := range results {
				if r.Success {
					ok++
					lines = append(lines, "ok    "+r.Email+" → "+r.OrgID)
				} else {
					failed++
					lines = append(lines, "fail  "+r.Email+": "+r.Error)
				}
			}
			envStatus.SetText(fmt.Sprintf("imported %d, failed %d\n\n%s", ok, failed, strings.Join(lines, "\n")))
			if onDone != nil {
				onDone()
			}
		}, w)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".env"}))
		fd.Show()
	})
	envTab := container.NewVBox(
		widget.NewLabelWithStyle("Import from .env file", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("File must contain ACCOUNT_EMAIL_N + ACCOUNT_KEY_N pairs (N=1..99)."),
		envBtn, envStatus,
	)

	tabs := container.NewAppTabs(
		container.NewTabItem("Paste token", pasteTab),
		container.NewTabItem("Import .env", envTab),
	)

	// Top section: one-click "Import from Claude Code" — only when the
	// conventional credentials file exists. Most users will use this.
	var top fyne.CanvasObject
	if poller.CredentialsFileExists() {
		ccStatus := widget.NewLabel("")
		ccBtn := widget.NewButton("Import from Claude Code", func() {
			go func() {
				ctx, cancel := context.WithTimeout(env.Ctx, 30*time.Second)
				defer cancel()
				res, err := env.Poller.ImportFromClaudeCode(ctx, "")
				if err != nil {
					ccStatus.SetText("Failed: " + err.Error())
					return
				}
				ccStatus.SetText(fmt.Sprintf("Added %s (org %s)", res.Label, res.OrgID))
				if onDone != nil {
					onDone()
				}
			}()
		})
		ccBtn.Importance = widget.HighImportance
		top = container.NewVBox(
			widget.NewLabelWithStyle("Quick import",
				fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewLabel("Use your existing Claude Code login (recommended)."),
			ccBtn, ccStatus,
			widget.NewSeparator(),
			widget.NewLabel("Or use one of the methods below:"),
		)
	}

	if top == nil {
		w.SetContent(tabs)
	} else {
		w.SetContent(container.NewBorder(top, nil, nil, nil, tabs))
	}
	return w
}

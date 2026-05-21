package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/store"
)

func showRenameDialog(app fyne.App, env *cli.Env, a store.Account) {
	w := app.NewWindow("Rename — " + a.DisplayName())
	w.Resize(fyne.NewSize(360, 120))
	entry := widget.NewEntry()
	entry.SetText(a.DisplayName())
	form := dialog.NewForm("Rename", "Save", "Cancel",
		[]*widget.FormItem{{Text: "Name", Widget: entry}},
		func(ok bool) {
			if !ok {
				w.Close()
				return
			}
			_ = env.Store.RenameAccount(env.Ctx, a.ID, entry.Text)
			w.Close()
		}, w)
	form.Show()
}

func showConfirmRemove(app fyne.App, env *cli.Env, a store.Account) {
	w := app.NewWindow("Remove — " + a.DisplayName())
	w.Resize(fyne.NewSize(360, 120))
	dialog.ShowConfirm("Remove account?",
		"This permanently deletes "+a.DisplayName()+" and its usage history.",
		func(ok bool) {
			if ok {
				_ = env.Store.DeleteAccount(env.Ctx, a.ID)
			}
			w.Close()
		}, w)
}

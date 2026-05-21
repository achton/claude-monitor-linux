package ui

import (
	"os"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/config"
	"github.com/achton/claude-monitor-linux/internal/xdg"
)

// NewSettingsWindow returns a small window for adjusting persistent prefs.
func NewSettingsWindow(app fyne.App, env *cli.Env, onChange func()) fyne.Window {
	w := app.NewWindow("Claude Monitor — Settings")
	w.Resize(fyne.NewSize(480, 360))

	cfg, _ := config.Load()

	notifEnabled := widget.NewCheck("Enable threshold notifications", func(v bool) {
		cfg.Notifications.Enabled = v
		_ = config.Save(cfg)
	})
	notifEnabled.SetChecked(cfg.Notifications.Enabled)

	thresholds := widget.NewEntry()
	thresholds.SetText(joinIntList(cfg.Notifications.Thresholds))
	thresholds.OnChanged = func(s string) {
		vals := parseIntList(s)
		if len(vals) > 0 {
			cfg.Notifications.Thresholds = vals
			_ = config.Save(cfg)
		}
	}

	pollEntry := widget.NewEntry()
	pollEntry.SetText(strconv.Itoa(cfg.Polling.IntervalSeconds))
	pollEntry.OnChanged = func(s string) {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err == nil && v >= 60 {
			cfg.Polling.IntervalSeconds = v
			_ = config.Save(cfg)
		}
	}

	adaptive := widget.NewCheck("Adaptive throttling near limits", func(v bool) {
		cfg.Polling.Adaptive = v
		_ = config.Save(cfg)
	})
	adaptive.SetChecked(cfg.Polling.Adaptive)

	autostart := widget.NewCheck("Start automatically at login", nil)
	autostart.SetChecked(autostartExists())
	autostart.OnChanged = func(v bool) {
		if v {
			if err := enableAutostart(); err != nil {
				dialog.ShowError(err, w)
				autostart.SetChecked(false)
			}
		} else {
			_ = disableAutostart()
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("Notifications", notifEnabled),
		widget.NewFormItem("Thresholds (%, comma)", thresholds),
		widget.NewFormItem("Poll interval (s)", pollEntry),
		widget.NewFormItem("Adaptive throttling", adaptive),
		widget.NewFormItem("Autostart", autostart),
	)

	close := widget.NewButton("Close", func() {
		if onChange != nil {
			onChange()
		}
		w.Close()
	})

	w.SetContent(container.NewVBox(form, close))
	return w
}

func joinIntList(in []int) string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, strconv.Itoa(v))
	}
	return strings.Join(out, ", ")
}

func parseIntList(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		v, err := strconv.Atoi(p)
		if err != nil || v <= 0 || v > 100 {
			continue
		}
		out = append(out, v)
	}
	return out
}

func autostartExists() bool {
	_, err := os.Stat(xdg.AutostartFile())
	return err == nil
}

const autostartTemplate = `[Desktop Entry]
Type=Application
Name=Claude Monitor
Comment=Monitor Claude AI usage
Exec=claude-monitor tray
Icon=claude-monitor
Terminal=false
Categories=Utility;
X-GNOME-Autostart-enabled=true
`

func enableAutostart() error {
	path := xdg.AutostartFile()
	if err := os.MkdirAll(xdg.ParentDir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(autostartTemplate), 0o600)
}

func disableAutostart() error {
	if err := os.Remove(xdg.AutostartFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

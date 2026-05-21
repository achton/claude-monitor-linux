// tray_entry.go isolates all fyne/systray imports. Per docs/DESIGN.md §11,
// runTray() must be the only function that drags GUI side effects into the
// binary. main.go reaches this only via the `tray` subcommand.
package main

import (
	"fmt"
	"os"

	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/config"
	"github.com/achton/claude-monitor-linux/internal/tray"
)

// runTray launches the tray daemon. Returns an exit code.
func runTray(env *cli.Env, cfg config.Config) int {
	if err := tray.Run(env, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "tray: %s\n", err)
		return 1
	}
	return 0
}

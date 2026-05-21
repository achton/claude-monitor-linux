// Command claude-monitor is the Linux usage widget + CLI for Claude.
//
// IMPORTANT: This file must remain GUI-free. Per docs/DESIGN.md §11, importing
// fyne/systray here would risk display-open side effects under headless CLI
// invocations (DISPLAY unset). GUI imports live in tray_entry.go and are
// reached only when the `tray` subcommand is selected.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/config"
	cmlog "github.com/achton/claude-monitor-linux/internal/log"
	"github.com/achton/claude-monitor-linux/internal/notify"
	"github.com/achton/claude-monitor-linux/internal/poller"
	"github.com/achton/claude-monitor-linux/internal/store"
)

func main() {
	args := os.Args[1:]
	subcmd := "tray"
	if len(args) > 0 && args[0] != "" {
		subcmd = args[0]
		args = args[1:]
	}

	// Help/version don't need the store opened.
	switch subcmd {
	case "help", "-h", "--help":
		os.Exit(cli.Run(stubEnv(), "help", args))
	case "version", "--version":
		os.Exit(cli.Run(stubEnv(), "version", args))
	}

	// Handle `tray --detach`: re-exec ourselves without --detach and exit.
	// Keeps the parent shell unblocked. Imports here are stdlib only so this
	// path does NOT pull GUI side effects (per §11).
	if subcmd == "tray" {
		filtered, detach := stripDetach(args)
		if detach {
			os.Exit(detachAndExec(filtered))
		}
		args = filtered
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %s\n", err)
		os.Exit(1)
	}
	if err := cmlog.Init(cfg.Logging.Level); err != nil {
		fmt.Fprintf(os.Stderr, "log init: %s\n", err)
		os.Exit(1)
	}
	defer cmlog.Close()

	s, err := store.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %s\n", err)
		os.Exit(1)
	}
	defer s.Close()

	apiClient := api.NewClient()
	notifier := notify.New()
	defer notifier.Close()
	ev := &notify.Evaluator{
		Store:      s,
		Notifier:   notifier,
		Thresholds: cfg.Notifications.Thresholds,
		AppName:    "Claude Monitor",
	}
	if !cfg.Notifications.Enabled {
		ev = nil
	}
	pl := poller.New(s, apiClient, ev, cfg.Polling.IntervalSeconds, cfg.Polling.Adaptive)

	env := &cli.Env{
		Ctx:    context.Background(),
		Store:  s,
		API:    apiClient,
		Poller: pl,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	if subcmd == "tray" {
		// Hand off to the GUI entry point. See cmd/claude-monitor/tray_entry.go.
		os.Exit(runTray(env, cfg))
	}
	os.Exit(cli.Run(env, subcmd, args))
}

// stubEnv returns an Env with only Stdout/Stderr populated, for help/version.
func stubEnv() *cli.Env {
	return &cli.Env{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// stripDetach removes the --detach / -d flag from args (if present) and reports
// whether the flag was set.
func stripDetach(args []string) ([]string, bool) {
	out := args[:0:0]
	out = append(out, args...)
	detach := false
	filtered := out[:0]
	for _, a := range out {
		if a == "--detach" || a == "-d" {
			detach = true
			continue
		}
		filtered = append(filtered, a)
	}
	return filtered, detach
}

// detachAndExec spawns a copy of the running binary in a new session running
// the tray subcommand, redirecting stdin/stdout/stderr to /dev/null. Returns
// the exit code of the parent (the child runs on).
func detachAndExec(extraArgs []string) int {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach: cannot resolve own path: %s\n", err)
		return 1
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach: open /dev/null: %s\n", err)
		return 1
	}
	defer devnull.Close()

	args := append([]string{"tray"}, extraArgs...)
	cmd := exec.Command(exePath, args...)
	cmd.Stdin = devnull
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "detach: start: %s\n", err)
		return 1
	}
	if err := cmd.Process.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "detach: release: %s\n", err)
	}
	fmt.Fprintf(os.Stdout, "claude-monitor tray detached (pid %d)\n", cmd.Process.Pid)
	return 0
}

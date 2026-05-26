// Package cli implements the subcommand handlers for claude-monitor.
//
// This package must remain GUI-free — it is imported by the bare-CLI dispatch
// path and must not pull fyne/systray side effects.
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/poller"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// Env carries the shared dependencies used by every subcommand.
type Env struct {
	Ctx    context.Context
	Store  *store.Store
	API    *api.Client
	Poller *poller.Poller
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run dispatches to the named subcommand handler.
// Returns the exit code that should be passed to os.Exit (0–30 per spec).
func Run(env *Env, name string, args []string) int {
	switch name {
	case "status":
		return Status(env, args)
	case "poll":
		return Poll(env, args)
	case "version":
		return Version(env, args)
	case "help", "-h", "--help":
		return Help(env, args)
	default:
		fmt.Fprintf(env.Stderr, "claude-monitor: unknown subcommand %q\n", name)
		_ = Help(env, nil)
		return 1
	}
}

// Help prints the top-level help text.
func Help(env *Env, _ []string) int {
	fmt.Fprintf(env.Stdout, `claude-monitor — Linux usage widget for Claude

USAGE
    claude-monitor                 Launch tray (default; blocking)
    claude-monitor tray [--detach] Launch tray; --detach forks and returns
    claude-monitor status [opts]   Print current usage
    claude-monitor poll            Force a poll cycle
    claude-monitor version         Print version
    claude-monitor help            Show help

STATUS OPTIONS
    --json                         Machine-readable JSON
    --format TEMPLATE              Go template string
    --quiet                        No output; exit code only

EXIT CODES (status)
    0   primary usage < 75%%
    10  >= 75%% (caution)
    20  >= 90%% (warning)
    30  >= 95%% (critical)
    1   error (no data, network)

Authentication is read live from ~/.claude/.credentials.json on every poll.
Run `+"`claude /login`"+` (Claude Code) if you see an unauthorized error.
`)
	return 0
}

// AppVersion is set at link time via -ldflags.
var AppVersion = "0.1.0-dev"

// Version prints the binary version.
func Version(env *Env, _ []string) int {
	fmt.Fprintln(env.Stdout, AppVersion)
	return 0
}

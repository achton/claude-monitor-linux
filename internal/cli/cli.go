// Package cli implements the subcommand handlers for claude-monitor.
//
// All handlers operate against the SQLite store and (for poll/probe) the
// Anthropic API client + the running tray's D-Bus surface when present.
//
// This package must remain GUI-free — it is imported by the bare-CLI dispatch
// path and must not pull fyne/systray side effects. See docs/DESIGN.md §11.
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
	case "accounts":
		return Accounts(env, args)
	case "add-token":
		return AddToken(env, args)
	case "import-env":
		return ImportEnv(env, args)
	case "import-claude-code":
		return ImportClaudeCode(env, args)
	case "remove":
		return Remove(env, args)
	case "poll":
		return Poll(env, args)
	case "pin":
		return Pin(env, args)
	case "unpin":
		return Unpin(env, args)
	case "probe":
		return Probe(env, args)
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
    claude-monitor accounts        List accounts
    claude-monitor add-token TOK   Add an account by access token
    claude-monitor import-env FILE Import ACCOUNT_EMAIL_N/KEY_N pairs from .env
    claude-monitor import-claude-code
                                   Import the active token from
                                   ~/.claude/.credentials.json (Claude Code)
    claude-monitor remove NAME     Remove an account
    claude-monitor poll [opts]     Force a poll cycle
    claude-monitor pin NAME        Pin an account to the tray badge
    claude-monitor unpin           Clear the tray pin
    claude-monitor probe [opts]    Re-test /api/oauth/usage state
    claude-monitor version         Print version
    claude-monitor help [CMD]      Show help

STATUS OPTIONS
    --account NAME                 Specific account (else: pinned or first)
    --json                         Machine-readable JSON
    --format TEMPLATE              Go template string (see man page)
    --quiet                        No output; exit code only

EXIT CODES (status)
    0   primary usage < 75%%
    10  >= 75%% (caution)
    20  >= 90%% (warning)
    30  >= 95%% (critical)
    1   error (no data, network)

See docs/DESIGN.md or the man page for details.
`)
	return 0
}

// AppVersion is set at link time via -ldflags "-X 'github.com/achton/claude-monitor-linux/internal/cli.AppVersion=0.1.0'".
var AppVersion = "0.1.0-dev"

// Version prints the binary version.
func Version(env *Env, _ []string) int {
	fmt.Fprintln(env.Stdout, AppVersion)
	return 0
}

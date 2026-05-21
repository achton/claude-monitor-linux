package cli

import (
	"flag"
	"fmt"
)

// Poll implements `claude-monitor poll [--account NAME]`.
//
// If the tray daemon is running (DBus name org.claude_monitor.Tray is owned),
// we delegate to it. Otherwise we poll in-process.
func Poll(env *Env, args []string) int {
	fs := flag.NewFlagSet("poll", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	account := fs.String("account", "", "account id or name (default: all)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	// Try DBus delegation first.
	if delegated, err := tryDelegatePoll(env, *account); err == nil {
		fmt.Fprintf(env.Stdout, "tray polled (rows: %d)\n", delegated)
		return 0
	}

	// Local poll.
	if *account != "" {
		a, err := env.Store.FindAccountByName(env.Ctx, *account)
		if err != nil {
			fmt.Fprintf(env.Stderr, "poll: %s\n", err)
			return 1
		}
		if err := env.Poller.PollAccount(env.Ctx, a.ID); err != nil {
			fmt.Fprintf(env.Stderr, "poll: %s\n", err)
			return 1
		}
		fmt.Fprintln(env.Stdout, "polled 1 account")
		return 0
	}
	n, err := env.Poller.PollAll(env.Ctx)
	if err != nil {
		fmt.Fprintf(env.Stderr, "poll: %s\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "polled %d account(s)\n", n)
	return 0
}

// Probe implements `claude-monitor probe [--account NAME]`. Re-tests the OAuth
// Usage endpoint by resetting the per-credential state to 'healthy' and
// running one poll.
func Probe(env *Env, args []string) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	account := fs.String("account", "", "account id or name (default: all)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	creds, err := env.Store.ListActiveCredentials(env.Ctx)
	if err != nil {
		fmt.Fprintf(env.Stderr, "probe: %s\n", err)
		return 1
	}
	var resetCount int
	for _, c := range creds {
		if *account != "" && (!c.AccountID.Valid || c.AccountID.String != *account) {
			// Try resolving by name too.
			a, err := env.Store.FindAccountByName(env.Ctx, *account)
			if err != nil || a.ID != c.AccountID.String {
				continue
			}
		}
		if err := env.Store.ResetCredentialEndpointState(env.Ctx, c.ID); err != nil {
			fmt.Fprintf(env.Stderr, "probe: %s\n", err)
			continue
		}
		resetCount++
	}
	fmt.Fprintf(env.Stdout, "reset %d credential(s) to healthy; polling...\n", resetCount)
	return Poll(env, args)
}

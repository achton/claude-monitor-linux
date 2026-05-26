package cli

import (
	"fmt"
)

// Poll implements `claude-monitor poll`.
//
// If the tray daemon is running, we delegate to it. Otherwise we poll in-process.
func Poll(env *Env, _ []string) int {
	if delegated, err := tryDelegatePoll(); err == nil {
		fmt.Fprintf(env.Stdout, "tray polled (rows: %d)\n", delegated)
		return 0
	}
	if err := env.Poller.PollNow(env.Ctx); err != nil {
		fmt.Fprintf(env.Stderr, "poll: %s\n", err)
		return 1
	}
	fmt.Fprintln(env.Stdout, "polled")
	return 0
}

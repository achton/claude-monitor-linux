package cli

import (
	"fmt"
)

// Pin implements `claude-monitor pin NAME`.
func Pin(env *Env, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(env.Stderr, "pin: missing account id or name")
		return 1
	}
	a, err := env.Store.FindAccountByName(env.Ctx, args[0])
	if err != nil {
		fmt.Fprintf(env.Stderr, "pin: %s\n", err)
		return 1
	}
	v := a.ID
	if err := env.Store.SetSetting(env.Ctx, "tray_pinned_account", &v); err != nil {
		fmt.Fprintf(env.Stderr, "pin: %s\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "pinned: %s\n", a.DisplayName())
	return 0
}

// Unpin implements `claude-monitor unpin`.
func Unpin(env *Env, _ []string) int {
	if err := env.Store.SetSetting(env.Ctx, "tray_pinned_account", nil); err != nil {
		fmt.Fprintf(env.Stderr, "unpin: %s\n", err)
		return 1
	}
	fmt.Fprintln(env.Stdout, "tray pin cleared")
	return 0
}

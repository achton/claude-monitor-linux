package cli

import (
	"fmt"
)

// Remove implements `claude-monitor remove NAME`.
func Remove(env *Env, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(env.Stderr, "remove: missing account id or name")
		return 1
	}
	a, err := env.Store.FindAccountByName(env.Ctx, args[0])
	if err != nil {
		fmt.Fprintf(env.Stderr, "remove: %s\n", err)
		return 1
	}
	if err := env.Store.DeleteAccount(env.Ctx, a.ID); err != nil {
		fmt.Fprintf(env.Stderr, "remove: %s\n", err)
		return 1
	}
	// If this was the pinned account, clear the pin silently.
	pin := env.Store.GetSettingDefault(env.Ctx, "tray_pinned_account", "")
	if pin == a.ID {
		_ = env.Store.SetSetting(env.Ctx, "tray_pinned_account", nil)
	}
	fmt.Fprintf(env.Stdout, "removed: %s\n", a.DisplayName())
	return 0
}

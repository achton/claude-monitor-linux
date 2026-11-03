package notify

import (
	"context"
	"fmt"
	"time"
)

// signInWarning is how long before the sign-in expires the warning fires.
const signInWarning = 3 * 24 * time.Hour

// EvaluateSignIn warns once per expiry time when Claude Code's sign-in is about
// to expire. After that Claude Code needs `claude /login`, and polls fail.
func (e *Evaluator) EvaluateSignIn(ctx context.Context, accountLabel string, expiresAt time.Time) error {
	if e == nil || e.Store == nil || e.Notifier == nil || expiresAt.IsZero() {
		return nil
	}
	if left := time.Until(expiresAt); left <= 0 || left > signInWarning {
		return nil
	}
	fired, err := e.Store.MarkNotificationFiredKey(ctx, "signin", 0,
		expiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil || !fired {
		return err
	}
	_, err = e.Notifier.Send(e.AppName,
		accountLabel+": Claude Code sign-in",
		fmt.Sprintf("Expires %s. Run `claude /login` to renew it.", humanReset(expiresAt)),
		UrgencyNormal)
	return err
}

package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"text/template"
	"time"

	"github.com/achton/claude-monitor-linux/internal/store"
)

// StatusView is the structure exposed to JSON and Go-template renderers.
// Field names are stable from v0.1.0.
type StatusView struct {
	AccountID         string    `json:"account_id"`
	AccountName       string    `json:"account_name"`
	PrimaryPercent    float64   `json:"primary_percent"`
	SessionPercent    float64   `json:"session_percent"`
	WeeklyPercent     float64   `json:"weekly_percent"`
	SessionResetIn    string    `json:"session_reset_in"`
	WeeklyResetIn     string    `json:"weekly_reset_in"`
	SessionResetAt    string    `json:"session_reset_at"`
	WeeklyResetAt     string    `json:"weekly_reset_at"`
	LastUpdated       string    `json:"last_updated"`
	IsRateLimited     bool      `json:"is_rate_limited"`
	IsWeeklyDominant  bool      `json:"is_weekly_dominant"`
	Source            string    `json:"source"`
	collectedAt       time.Time `json:"-"`
}

// Status implements `claude-monitor status`.
func Status(env *Env, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	account := fs.String("account", "", "account id or name (default: pinned, or first)")
	asJSON := fs.Bool("json", false, "JSON output")
	format := fs.String("format", "", "Go template format string")
	quiet := fs.Bool("quiet", false, "no output; exit code only")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	v, err := buildStatusView(env, *account)
	if err != nil {
		if !*quiet {
			fmt.Fprintf(env.Stderr, "status: %s\n", err)
		}
		return 1
	}

	switch {
	case *quiet:
		// silent
	case *format != "":
		t, err := template.New("status").Parse(*format)
		if err != nil {
			fmt.Fprintf(env.Stderr, "status: bad --format template: %s\n", err)
			return 1
		}
		if err := t.Execute(env.Stdout, v); err != nil {
			fmt.Fprintf(env.Stderr, "status: %s\n", err)
			return 1
		}
		fmt.Fprintln(env.Stdout)
	case *asJSON:
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return 1
		}
	default:
		printPlain(env, v)
	}
	return exitCodeFor(v.PrimaryPercent)
}

func printPlain(env *Env, v StatusView) {
	if v.IsRateLimited {
		fmt.Fprintf(env.Stdout, "LLM %.0f%% (RATE LIMITED) — %s\n", v.PrimaryPercent, v.AccountName)
		return
	}
	dom := "session"
	if v.IsWeeklyDominant {
		dom = "weekly"
	}
	fmt.Fprintf(env.Stdout,
		"LLM %.0f%% (%s dominant; session %.0f%%, weekly %.0f%%; resets %s) — %s\n",
		v.PrimaryPercent, dom, v.SessionPercent, v.WeeklyPercent, v.SessionResetIn, v.AccountName,
	)
}

// exitCodeFor maps primary % to the documented bash-integration exit codes.
func exitCodeFor(p float64) int {
	switch {
	case p >= 95:
		return 30
	case p >= 90:
		return 20
	case p >= 75:
		return 10
	default:
		return 0
	}
}

func buildStatusView(env *Env, account string) (StatusView, error) {
	target, err := pickAccount(env, account)
	if err != nil {
		return StatusView{}, err
	}
	rec, err := env.Store.LatestUsage(env.Ctx, target.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusView{}, errors.New("no usage data for account (run `claude-monitor poll`)")
	}
	if err != nil {
		return StatusView{}, err
	}
	v := StatusView{
		AccountID:      target.ID,
		AccountName:    target.DisplayName(),
		PrimaryPercent: round1(valOrZero(rec.PrimaryPercent)),
		SessionPercent: round1(valOrZero(rec.SessionPercent)),
		WeeklyPercent:  round1(valOrZero(rec.WeeklyAllPercent)),
		Source:         rec.Source.String,
		collectedAt:    rec.Timestamp,
	}
	if rec.SessionReset.Valid {
		if t, ok := parseISO(rec.SessionReset.String); ok {
			v.SessionResetAt = t.UTC().Format(time.RFC3339)
			v.SessionResetIn = humanDuration(time.Until(t))
		}
	}
	if rec.WeeklyReset.Valid {
		if t, ok := parseISO(rec.WeeklyReset.String); ok {
			v.WeeklyResetAt = t.UTC().Format(time.RFC3339)
			v.WeeklyResetIn = humanDuration(time.Until(t))
		}
	}
	v.LastUpdated = rec.Timestamp.UTC().Format(time.RFC3339)
	v.IsWeeklyDominant = v.WeeklyPercent >= v.SessionPercent
	if rec.RawData.Valid {
		// We don't extract sub-status here in v0.1.0; rate-limited is detected via 100% saturation.
	}
	if v.PrimaryPercent >= 100 {
		v.IsRateLimited = true
	}
	return v, nil
}

// pickAccount chooses the target account: explicit > pinned > first.
func pickAccount(env *Env, explicit string) (store.Account, error) {
	if explicit != "" {
		return env.Store.FindAccountByName(env.Ctx, explicit)
	}
	pin := env.Store.GetSettingDefault(env.Ctx, "tray_pinned_account", "")
	if pin != "" {
		if a, err := env.Store.GetAccount(env.Ctx, pin); err == nil {
			return a, nil
		}
	}
	accs, err := env.Store.ListAccounts(env.Ctx)
	if err != nil {
		return store.Account{}, err
	}
	if len(accs) == 0 {
		return store.Account{}, errors.New("no accounts (run `claude-monitor add-token`)")
	}
	return accs[0], nil
}

// ---- helpers ----

func valOrZero(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func parseISO(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours()/24), int(d.Hours())%24)
}

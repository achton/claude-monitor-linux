package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"
)

type accountView struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Email          string  `json:"email,omitempty"`
	Plan           string  `json:"plan,omitempty"`
	LastUpdated    string  `json:"last_updated,omitempty"`
	PrimaryPercent float64 `json:"primary_percent"`
	Pinned         bool    `json:"pinned"`
}

// Accounts implements `claude-monitor accounts`.
func Accounts(env *Env, args []string) int {
	fs := flag.NewFlagSet("accounts", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	accs, err := env.Store.ListAccounts(env.Ctx)
	if err != nil {
		fmt.Fprintf(env.Stderr, "accounts: %s\n", err)
		return 1
	}
	pin := env.Store.GetSettingDefault(env.Ctx, "tray_pinned_account", "")
	views := make([]accountView, 0, len(accs))
	for _, a := range accs {
		v := accountView{
			ID:     a.ID,
			Name:   a.DisplayName(),
			Pinned: a.ID == pin,
		}
		if a.Email.Valid {
			v.Email = a.Email.String
		}
		if a.Plan.Valid {
			v.Plan = a.Plan.String
		}
		if a.LastUpdated.Valid {
			v.LastUpdated = a.LastUpdated.Time.UTC().Format(time.RFC3339)
		}
		if rec, err := env.Store.LatestUsage(env.Ctx, a.ID); err == nil {
			v.PrimaryPercent = valOrZero(rec.PrimaryPercent)
		}
		views = append(views, v)
	}
	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(views)
		return 0
	}
	tw := tabwriter.NewWriter(env.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tPLAN\tUSAGE\tPIN")
	for _, v := range views {
		pin := ""
		if v.Pinned {
			pin = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.0f%%\t%s\n", v.Name, v.ID, v.Plan, v.PrimaryPercent, pin)
	}
	_ = tw.Flush()
	if len(views) == 0 {
		fmt.Fprintln(env.Stdout, "(no accounts; run `claude-monitor add-token`)")
	}
	return 0
}

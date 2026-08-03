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
)

// LimitView is one limit as exposed to JSON and Go-template renderers.
type LimitView struct {
	Key        string  `json:"key"`
	Kind       string  `json:"kind"`
	Group      string  `json:"group"`
	Label      string  `json:"label"`
	ScopeModel string  `json:"scope_model,omitempty"`
	Percent    float64 `json:"percent"`
	Severity   string  `json:"severity"`
	ResetIn    string  `json:"reset_in,omitempty"`
	ResetAt    string  `json:"reset_at,omitempty"`
}

// StatusView is the structure exposed to JSON and Go-template renderers.
//
// Limits carries every limit the API reported. The Session*/Weekly* fields are
// kept as conveniences for existing --format templates and status lines; they
// are empty when the API stops reporting that limit.
type StatusView struct {
	AccountName      string      `json:"account_name"`
	PrimaryPercent   float64     `json:"primary_percent"`
	Limits           []LimitView `json:"limits"`
	SessionPercent   float64     `json:"session_percent"`
	WeeklyPercent    float64     `json:"weekly_percent"`
	SessionResetIn   string      `json:"session_reset_in"`
	WeeklyResetIn    string      `json:"weekly_reset_in"`
	SessionResetAt   string      `json:"session_reset_at"`
	WeeklyResetAt    string      `json:"weekly_reset_at"`
	LastUpdated      string      `json:"last_updated"`
	IsRateLimited    bool        `json:"is_rate_limited"`
	IsWeeklyDominant bool        `json:"is_weekly_dominant"`
	collectedAt      time.Time   `json:"-"`
}

// Status implements `claude-monitor status`.
func Status(env *Env, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "JSON output")
	format := fs.String("format", "", "Go template format string")
	quiet := fs.Bool("quiet", false, "no output; exit code only")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	v, err := buildStatusView(env)
	if err != nil {
		if !*quiet {
			fmt.Fprintf(env.Stderr, "status: %s\n", err)
		}
		return 1
	}

	switch {
	case *quiet:
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

// printPlain keeps its summary first line stable for status bars and scripts
// that read only the first line, then details every reported limit below it.
func printPlain(env *Env, v StatusView) {
	if v.IsRateLimited {
		fmt.Fprintf(env.Stdout, "LLM %.0f%% (RATE LIMITED) — %s\n", v.PrimaryPercent, v.AccountName)
	} else {
		fmt.Fprintf(env.Stdout,
			"LLM %.0f%% (session %.0f%%, weekly %.0f%%; resets %s) — %s\n",
			v.PrimaryPercent, v.SessionPercent, v.WeeklyPercent, v.SessionResetIn, v.AccountName,
		)
	}

	width := 0
	for _, l := range v.Limits {
		if n := len([]rune(l.Label)); n > width {
			width = n
		}
	}
	for _, l := range v.Limits {
		line := fmt.Sprintf("  %-*s %5.0f%%", width, l.Label, l.Percent)
		if l.ResetIn != "" {
			line += "  resets " + l.ResetIn
		}
		fmt.Fprintln(env.Stdout, line)
	}
}

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

func buildStatusView(env *Env) (StatusView, error) {
	rec, err := env.Store.LatestReading(env.Ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusView{}, errors.New("no usage data yet (run `claude-monitor poll`)")
	}
	if err != nil {
		return StatusView{}, err
	}
	label, _, _, _ := env.Poller.Status()
	if label == "" {
		label = "Claude Code"
	}
	v := StatusView{
		AccountName:    label,
		PrimaryPercent: round1(rec.PrimaryPercent()),
		collectedAt:    rec.Timestamp,
		LastUpdated:    rec.Timestamp.UTC().Format(time.RFC3339),
	}

	for _, l := range rec.Limits {
		lv := LimitView{
			Key:        l.Key(),
			Kind:       l.Kind,
			Group:      l.Group,
			Label:      l.Label(),
			ScopeModel: l.ScopeModel,
			Percent:    round1(l.Percent),
			Severity:   l.Severity,
		}
		if !l.ResetsAt.IsZero() {
			lv.ResetAt = l.ResetsAt.UTC().Format(time.RFC3339)
			lv.ResetIn = humanDuration(time.Until(l.ResetsAt))
		}
		v.Limits = append(v.Limits, lv)
	}

	if l, ok := rec.Session(); ok {
		v.SessionPercent = round1(l.Percent)
		if !l.ResetsAt.IsZero() {
			v.SessionResetAt = l.ResetsAt.UTC().Format(time.RFC3339)
			v.SessionResetIn = humanDuration(time.Until(l.ResetsAt))
		}
	}
	if l, ok := rec.Weekly(); ok {
		v.WeeklyPercent = round1(l.Percent)
		if !l.ResetsAt.IsZero() {
			v.WeeklyResetAt = l.ResetsAt.UTC().Format(time.RFC3339)
			v.WeeklyResetIn = humanDuration(time.Until(l.ResetsAt))
		}
	}
	v.IsWeeklyDominant = v.WeeklyPercent >= v.SessionPercent
	if v.PrimaryPercent >= 100 {
		v.IsRateLimited = true
	}
	return v, nil
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

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

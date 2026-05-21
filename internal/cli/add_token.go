package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// AddToken implements `claude-monitor add-token [TOKEN]`. Reads from stdin if
// the positional arg is missing.
func AddToken(env *Env, args []string) int {
	fs := flag.NewFlagSet("add-token", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "optional email/label for the account")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	var token string
	switch {
	case len(rest) >= 1:
		token = strings.TrimSpace(rest[0])
	default:
		b, _ := io.ReadAll(env.Stdin)
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		fmt.Fprintln(env.Stderr, "add-token: no token provided (pass as argument or via stdin)")
		return 1
	}
	res, err := env.Poller.AddAccountWithToken(env.Ctx, token, *email, "")
	if err != nil {
		fmt.Fprintf(env.Stderr, "add-token: %s\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "added: %s (org %s)\n", res.Label, res.OrgID)
	return 0
}

// ImportEnv implements `claude-monitor import-env FILE`.
func ImportEnv(env *Env, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(env.Stderr, "import-env: missing file argument")
		return 1
	}
	f, err := os.Open(args[0])
	if err != nil {
		fmt.Fprintf(env.Stderr, "import-env: open %s: %s\n", args[0], err)
		return 1
	}
	defer f.Close()
	results, err := env.Poller.ImportFromEnv(env.Ctx, f)
	if err != nil {
		fmt.Fprintf(env.Stderr, "import-env: %s\n", err)
		return 1
	}
	ok, failed := 0, 0
	for _, r := range results {
		if r.Success {
			ok++
			fmt.Fprintf(env.Stdout, "ok     %s → org %s\n", r.Email, r.OrgID)
		} else {
			failed++
			fmt.Fprintf(env.Stdout, "failed %s: %s\n", r.Email, r.Error)
		}
	}
	fmt.Fprintf(env.Stdout, "imported %d, failed %d\n", ok, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

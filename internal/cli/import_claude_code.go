package cli

import (
	"flag"
	"fmt"
)

// ImportClaudeCode is the CLI handler for `import-claude-code`. The heavy
// lifting now lives in internal/poller so the UI can call the same code.
func ImportClaudeCode(env *Env, args []string) int {
	fs := flag.NewFlagSet("import-claude-code", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	path := fs.String("path", "", "explicit path to credentials.json (default: ~/.claude/.credentials.json)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	res, err := env.Poller.ImportFromClaudeCode(env.Ctx, *path)
	if err != nil {
		fmt.Fprintf(env.Stderr, "import-claude-code: %s\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "added: %s (org %s)\n", res.Label, res.OrgID)
	fmt.Fprintf(env.Stdout, "(from %s; token found under: %s)\n", res.SourcePath, res.TokenJSON)
	return 0
}

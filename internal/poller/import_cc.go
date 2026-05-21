package poller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CCImportResult describes the outcome of an ImportFromClaudeCode call.
type CCImportResult struct {
	OrgID      string
	Label      string
	SourcePath string // the credentials.json path that was read
	TokenJSON  string // dotted JSON path where the token was found (diagnostics)
}

// ImportFromClaudeCode reads Claude Code's local OAuth credential file and
// imports the access token. If `explicitPath` is empty, several conventional
// locations are tried.
//
// The credentials JSON is walked for any `access_token` / `accessToken` field
// (resilient to schema changes), and a friendly label is derived from
// `email` / `accountName` / `subscriptionType` fields when present.
func (p *Poller) ImportFromClaudeCode(ctx context.Context, explicitPath string) (CCImportResult, error) {
	path, err := resolveCCPath(explicitPath)
	if err != nil {
		return CCImportResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return CCImportResult{}, fmt.Errorf("read %s: %w", path, err)
	}
	token, where, err := extractCCToken(data)
	if err != nil {
		return CCImportResult{}, err
	}
	email, label := extractCCLabel(data)
	if label == "" {
		label = "Claude Code"
	}
	res, err := p.AddAccountWithToken(ctx, token, email, label)
	if err != nil {
		return CCImportResult{}, fmt.Errorf("add account: %w", err)
	}
	return CCImportResult{
		OrgID:      res.OrgID,
		Label:      res.Label,
		SourcePath: path,
		TokenJSON:  where,
	}, nil
}

// CredentialsFileExists reports whether one of the conventional Claude Code
// credentials.json paths exists. Useful for offering one-click import in the UI.
func CredentialsFileExists() bool {
	_, err := resolveCCPath("")
	return err == nil
}

func resolveCCPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	candidates := []string{
		filepath.Join(home, ".claude", ".credentials.json"),
		filepath.Join(home, ".config", "claude", "credentials.json"),
		filepath.Join(home, ".config", "claude-code", "credentials.json"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Claude Code credentials file not found (tried %v)", candidates)
}

func extractCCToken(raw []byte) (token, path string, err error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", "", fmt.Errorf("parse json: %w", err)
	}
	tok, where, ok := ccWalkForToken(v, "")
	if !ok {
		return "", "", errors.New("no access_token / accessToken field found")
	}
	return tok, where, nil
}

func extractCCLabel(raw []byte) (email, label string) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", ""
	}
	if e := ccWalkForString(v, []string{"email", "userEmail", "user_email"}); e != "" {
		return e, e
	}
	if name := ccWalkForString(v, []string{"accountName", "account_name", "organizationName", "organization_name", "displayName"}); name != "" {
		return "", name
	}
	if sub := ccWalkForString(v, []string{"subscriptionType", "subscription_type", "plan"}); sub != "" {
		return "", "Claude Code (" + sub + ")"
	}
	return "", ""
}

func ccWalkForToken(v any, prefix string) (string, string, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			here := prefix + "." + k
			if k == "access_token" || k == "accessToken" {
				if s, ok := child.(string); ok && s != "" {
					return s, here[1:], true
				}
			}
			if tok, where, ok := ccWalkForToken(child, here); ok {
				return tok, where, ok
			}
		}
	case []any:
		for i, child := range t {
			here := fmt.Sprintf("%s[%d]", prefix, i)
			if tok, where, ok := ccWalkForToken(child, here); ok {
				return tok, where, ok
			}
		}
	}
	return "", "", false
}

func ccWalkForString(v any, keys []string) string {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range keys {
			if s, ok := t[k].(string); ok && s != "" {
				return s
			}
		}
		for _, child := range t {
			if s := ccWalkForString(child, keys); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range t {
			if s := ccWalkForString(child, keys); s != "" {
				return s
			}
		}
	}
	return ""
}

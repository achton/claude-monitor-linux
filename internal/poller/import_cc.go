package poller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReadClaudeCodeToken reads ~/.claude/.credentials.json (or one of the
// conventional fallbacks) and returns the live access token plus a friendly
// account label derived from the file. Empty explicitPath uses defaults.
func ReadClaudeCodeToken(explicitPath string) (token, label string, err error) {
	path, err := resolveCCPath(explicitPath)
	if err != nil {
		return "", "", err
	}
	data, err := readCredentialsFileWithRetry(path)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}
	tok, lbl, err := extractCCCredentials(data)
	if err != nil {
		return "", "", err
	}
	return tok, lbl, nil
}

// CredentialsFileExists reports whether one of the conventional Claude Code
// credentials.json paths exists.
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

func readCredentialsFileWithRetry(path string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		data, err := os.ReadFile(path)
		if err != nil {
			if attempt == 0 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return nil, err
		}
		if looksLikeCompleteJSON(data) {
			return data, nil
		}
		if attempt == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return data, nil
	}
}

func looksLikeCompleteJSON(b []byte) bool {
	i, j := 0, len(b)-1
	for i < len(b) && isJSONSpace(b[i]) {
		i++
	}
	for j >= 0 && isJSONSpace(b[j]) {
		j--
	}
	if i > j {
		return false
	}
	open := b[i]
	close := b[j]
	return (open == '{' && close == '}') || (open == '[' && close == ']')
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// extractCCCredentials returns the access token and a display label from the
// credentials JSON. Walks the structure rather than hard-coding paths so we're
// resilient to Claude Code schema changes.
func extractCCCredentials(raw []byte) (token, label string, err error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", "", fmt.Errorf("parse json: %w", err)
	}
	tok, ok := ccWalkForToken(v)
	if !ok {
		return "", "", errors.New("no access_token / accessToken field found")
	}
	label = labelFromCCJSON(v)
	if label == "" {
		label = "Claude Code"
	}
	return tok, label, nil
}

func labelFromCCJSON(v any) string {
	if e := ccWalkForString(v, []string{"email", "userEmail", "user_email"}); e != "" {
		return e
	}
	if name := ccWalkForString(v, []string{"accountName", "account_name", "organizationName", "organization_name", "displayName"}); name != "" {
		return name
	}
	if sub := ccWalkForString(v, []string{"subscriptionType", "subscription_type", "plan"}); sub != "" {
		return "Claude Code (" + sub + ")"
	}
	return ""
}

func ccWalkForToken(v any) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "access_token" || k == "accessToken" {
				if s, ok := child.(string); ok && s != "" {
					return s, true
				}
			}
			if tok, ok := ccWalkForToken(child); ok {
				return tok, ok
			}
		}
	case []any:
		for _, child := range t {
			if tok, ok := ccWalkForToken(child); ok {
				return tok, ok
			}
		}
	}
	return "", false
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

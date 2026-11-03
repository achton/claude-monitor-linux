package poller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Credentials is the Claude Code OAuth state relevant to polling.
type Credentials struct {
	Token     string
	Label     string
	ExpiresAt time.Time // zero when the file carries no expiry
	// SignInExpiresAt is when the refresh token expires, after which Claude
	// Code needs `claude /login`. Zero when unknown.
	SignInExpiresAt time.Time
}

// Expired reports whether the access token's expiry has passed. Always false
// when the file carries no expiry, so an unknown schema never blocks a poll.
func (c Credentials) Expired() bool {
	return !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt)
}

// SignInExpired reports whether the refresh token has expired.
func (c Credentials) SignInExpired() bool {
	return !c.SignInExpiresAt.IsZero() && time.Now().After(c.SignInExpiresAt)
}

// ClaudeConfigDir returns Claude Code's config directory: configured when set,
// else $CLAUDE_CONFIG_DIR. Empty means the default locations.
func ClaudeConfigDir(configured string) string {
	if configured != "" {
		return expandHome(configured)
	}
	return os.Getenv("CLAUDE_CONFIG_DIR")
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// ReadClaudeCodeCredentials reads .credentials.json from Claude Code's config
// directory (see ClaudeConfigDir) and returns the live Claude access token plus
// a friendly account label.
func ReadClaudeCodeCredentials(configDir string) (Credentials, error) {
	path, err := resolveCCPath(configDir)
	if err != nil {
		return Credentials{}, err
	}
	data, err := readCredentialsFileWithRetry(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("read %s: %w", path, err)
	}
	return extractCCCredentials(data)
}

// CredentialsFileExists reports whether one of the conventional Claude Code
// credentials.json paths exists.
func CredentialsFileExists() bool {
	_, err := resolveCCPath("")
	return err == nil
}

func resolveCCPath(configDir string) (string, error) {
	if dir := ClaudeConfigDir(configDir); dir != "" {
		return filepath.Join(dir, ".credentials.json"), nil
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
	return "", fmt.Errorf("no Claude Code credentials file found (tried %v)", candidates)
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

// ccIgnoredSubtrees are containers whose accessToken fields belong to third
// parties, not to Claude. mcpOAuth holds one OAuth grant per authenticated MCP
// server; sending one of those as the bearer token 401s every poll.
var ccIgnoredSubtrees = map[string]bool{
	"mcpOAuth":    true,
	"mcp_oauth":   true,
	"mcpServers":  true,
	"mcp_servers": true,
}

// ccOAuthSectionKeys are the known locations of Claude's own OAuth grant.
var ccOAuthSectionKeys = []string{"claudeAiOauth", "claudeAiOAuth", "claude_ai_oauth"}

// extractCCCredentials pulls the token, label and expiry out of the credentials
// JSON. It reads Claude's own OAuth section directly and only falls back to
// walking the whole file for unrecognised schemas — a bare walk cannot
// distinguish Claude's token from an MCP server's.
func extractCCCredentials(raw []byte) (Credentials, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return Credentials{}, fmt.Errorf("parse json: %w", err)
	}

	var c Credentials
	if sec, ok := ccOAuthSection(v); ok {
		c.Token = ccStringField(sec, "accessToken", "access_token")
		c.ExpiresAt = ccExpiry(sec, "expiresAt", "expires_at")
		c.SignInExpiresAt = ccExpiry(sec, "refreshTokenExpiresAt", "refresh_token_expires_at")
		c.Label = labelFromCCJSON(sec)
	}
	if c.Token == "" {
		tok, ok := ccWalkForToken(v)
		if !ok {
			return Credentials{}, errors.New("no access_token / accessToken field found")
		}
		c.Token = tok
	}
	if c.Label == "" {
		c.Label = labelFromCCJSON(v)
	}
	if c.Label == "" {
		c.Label = "Claude Code"
	}
	return c, nil
}

// ccOAuthSection returns Claude's own top-level OAuth object.
func ccOAuthSection(v any) (map[string]any, bool) {
	root, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	for _, k := range ccOAuthSectionKeys {
		if m, ok := root[k].(map[string]any); ok {
			return m, true
		}
	}
	return nil, false
}

func ccStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// ccExpiry reads a timestamp field, which Claude Code writes as Unix
// milliseconds. Returns the zero time when absent or unparseable.
func ccExpiry(m map[string]any, keys ...string) time.Time {
	for _, k := range keys {
		switch n := m[k].(type) {
		case float64:
			return unixMsOrSec(int64(n))
		case string:
			if i, err := strconv.ParseInt(n, 10, 64); err == nil {
				return unixMsOrSec(i)
			}
			if t, err := time.Parse(time.RFC3339, n); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// unixMsOrSec accepts either unit; anything past the year 2001 in seconds is
// unambiguously milliseconds.
func unixMsOrSec(n int64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n > 1e12:
		return time.UnixMilli(n)
	default:
		return time.Unix(n, 0)
	}
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

// ccWalkForToken is the last-resort fallback for an unrecognised schema. It
// checks each level's own token fields before descending, skips third-party
// subtrees, and visits keys in sorted order so the result is deterministic:
// Go's randomised map iteration previously made a mis-pick intermittent.
func ccWalkForToken(v any) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		if s := ccStringField(t, "accessToken", "access_token"); s != "" {
			return s, true
		}
		for _, k := range sortedKeys(t) {
			if ccIgnoredSubtrees[k] {
				continue
			}
			if tok, ok := ccWalkForToken(t[k]); ok {
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
		if s := ccStringField(t, keys...); s != "" {
			return s
		}
		for _, k := range sortedKeys(t) {
			if ccIgnoredSubtrees[k] {
				continue
			}
			if s := ccWalkForString(t[k], keys); s != "" {
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

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

package poller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CCImportResult describes the outcome of an ImportFromClaudeCode call.
type CCImportResult struct {
	OrgID      string
	Label      string
	SourcePath string // the credentials.json path that was read
	TokenJSON  string // dotted JSON path where the token was found (diagnostics)
}

// ImportFromClaudeCode reads Claude Code's local OAuth credential file and
// imports the full credential (access token + refresh token + expiry +
// scopes/subscriptionType/rateLimitTier when present). If `explicitPath` is
// empty, several conventional locations are tried.
//
// The credentials JSON is walked for any `access_token` / `accessToken` field
// (resilient to schema changes), and a friendly label is derived from
// `email` / `accountName` / `subscriptionType` fields when present.
func (p *Poller) ImportFromClaudeCode(ctx context.Context, explicitPath string) (CCImportResult, error) {
	path, err := resolveCCPath(explicitPath)
	if err != nil {
		return CCImportResult{}, err
	}
	data, err := readCredentialsFileWithRetry(path)
	if err != nil {
		return CCImportResult{}, fmt.Errorf("read %s: %w", path, err)
	}
	creds, where, err := extractCCCredentials(data)
	if err != nil {
		return CCImportResult{}, err
	}
	creds.Source = "claude-code"
	email, label := extractCCLabel(data)
	if label == "" {
		label = "Claude Code"
	}
	res, err := p.AddAccountWithCredential(ctx, creds, email, label)
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

// ReReadClaudeCodeCredential reads the same file ImportFromClaudeCode uses
// and returns the current credential spec WITHOUT touching the store. The
// poller calls this when it needs to refresh a stale access token in place.
func ReReadClaudeCodeCredential(explicitPath string) (CredentialSpec, string, error) {
	path, err := resolveCCPath(explicitPath)
	if err != nil {
		return CredentialSpec{}, "", err
	}
	data, err := readCredentialsFileWithRetry(path)
	if err != nil {
		return CredentialSpec{}, path, fmt.Errorf("read %s: %w", path, err)
	}
	creds, _, err := extractCCCredentials(data)
	if err != nil {
		return CredentialSpec{}, path, err
	}
	creds.Source = "claude-code"
	return creds, path, nil
}

// readCredentialsFileWithRetry reads the credentials file, tolerating
// non-atomic writes by Claude Code: if the first read returns empty bytes
// or parses as invalid JSON, sleeps briefly and retries once. After two
// failures the underlying error is surfaced — caller treats it as a real
// problem rather than transient noise.
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
		// Return the partial bytes; the caller's JSON unmarshal will
		// produce a clear "parse json" error so the surface is the same.
		return data, nil
	}
}

// looksLikeCompleteJSON does a cheap structural check: a complete JSON
// object/array starts with { or [ and ends with } or ]. Truncated mid-write
// reads fail this trivially. We trim trailing whitespace before checking.
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

// extractCCCredentials walks the credentials JSON for the full set of fields
// claude-monitor cares about. Only AccessToken is required; missing optional
// fields are returned as zero values.
func extractCCCredentials(raw []byte) (CredentialSpec, string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return CredentialSpec{}, "", fmt.Errorf("parse json: %w", err)
	}
	tok, where, ok := ccWalkForToken(v, "")
	if !ok {
		return CredentialSpec{}, "", errors.New("no access_token / accessToken field found")
	}
	spec := CredentialSpec{
		AccessToken:      tok,
		RefreshToken:     ccWalkForString(v, []string{"refresh_token", "refreshToken"}),
		ExpiresAt:        ccWalkForExpiry(v),
		Scopes:           ccWalkForScopes(v),
		SubscriptionType: ccWalkForString(v, []string{"subscriptionType", "subscription_type"}),
		RateLimitTier:    ccWalkForString(v, []string{"rateLimitTier", "rate_limit_tier"}),
	}
	return spec, where, nil
}

// ccWalkForExpiry returns the access-token expiry in unix seconds, or 0.
// Tolerates `expiresAt` / `expires_at` in seconds OR milliseconds, plus
// `expires_in` (seconds-from-now).
func ccWalkForExpiry(v any) int64 {
	if n := ccWalkForNumber(v, []string{"expiresAt", "expires_at"}); n != 0 {
		// Claude Code writes this in milliseconds. Heuristic: anything >1e11
		// is ms (years 5138+ in seconds; ~Mar 1973 in ms — but Claude Code's
		// values are post-2024 so > 1.7e12 in ms, ~5.4e10 in seconds).
		if n > 1e11 {
			return n / 1000
		}
		return n
	}
	if n := ccWalkForNumber(v, []string{"expiresIn", "expires_in"}); n != 0 {
		return time.Now().Unix() + n
	}
	return 0
}

// ccWalkForScopes returns scopes as a space-joined string (claude.ai stores
// them as an array). Falls back to a plain string field if present.
func ccWalkForScopes(v any) string {
	if arr := ccWalkForStringArray(v, []string{"scopes", "scope"}); len(arr) > 0 {
		out := arr[0]
		for _, s := range arr[1:] {
			out += " " + s
		}
		return out
	}
	return ccWalkForString(v, []string{"scopes", "scope"})
}

func ccWalkForNumber(v any, keys []string) int64 {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range keys {
			switch n := t[k].(type) {
			case float64:
				return int64(n)
			case int64:
				return n
			case int:
				return int64(n)
			}
		}
		for _, child := range t {
			if n := ccWalkForNumber(child, keys); n != 0 {
				return n
			}
		}
	case []any:
		for _, child := range t {
			if n := ccWalkForNumber(child, keys); n != 0 {
				return n
			}
		}
	}
	return 0
}

func ccWalkForStringArray(v any, keys []string) []string {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range keys {
			if arr, ok := t[k].([]any); ok {
				out := make([]string, 0, len(arr))
				for _, x := range arr {
					if s, ok := x.(string); ok && s != "" {
						out = append(out, s)
					}
				}
				if len(out) > 0 {
					return out
				}
			}
		}
		for _, child := range t {
			if arr := ccWalkForStringArray(child, keys); len(arr) > 0 {
				return arr
			}
		}
	case []any:
		for _, child := range t {
			if arr := ccWalkForStringArray(child, keys); len(arr) > 0 {
				return arr
			}
		}
	}
	return nil
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

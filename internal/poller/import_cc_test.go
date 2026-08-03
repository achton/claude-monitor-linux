package poller

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	claudeToken  = "sk-ant-oat01-CLAUDE-REAL-TOKEN"
	harvestToken = "sk-mcp-HARVEST-FOREIGN-TOKEN"
)

// realWorldCredentials mirrors the shape of ~/.claude/.credentials.json once any
// MCP server has been authenticated: two accessToken fields, only one of which
// is Claude's.
func realWorldCredentials(expiresAt int64) string {
	return fmt.Sprintf(`{
	  "mcpOAuth": {
	    "harvest|58f5c652bb55bd38": {
	      "serverName": "harvest",
	      "serverUrl": "https://api.harvestapp.com/mcp",
	      "accessToken": %q,
	      "refreshToken": "refresh-harvest",
	      "clientId": "abc123",
	      "expiresAt": 1786646275837
	    }
	  },
	  "claudeAiOauth": {
	    "accessToken": %q,
	    "refreshToken": "refresh-claude",
	    "expiresAt": %d,
	    "refreshTokenExpiresAt": 1787929062700,
	    "subscriptionType": "team",
	    "rateLimitTier": "default_claude_max_5x"
	  }
	}`, harvestToken, claudeToken, expiresAt)
}

func futureMillis() int64 { return time.Now().Add(time.Hour).UnixMilli() }

// TestExtractPrefersClaudeTokenOverMCP is the regression test for the bug where
// the token walk returned whichever accessToken Go's randomised map iteration
// reached first. With an authenticated MCP server present that mis-picked the
// MCP server's token ~95% of the time and every poll 401'd. Repeat enough times
// that a reintroduced map-order dependency cannot pass by luck.
func TestExtractPrefersClaudeTokenOverMCP(t *testing.T) {
	raw := []byte(realWorldCredentials(futureMillis()))
	for i := 0; i < 500; i++ {
		c, err := extractCCCredentials(raw)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if c.Token != claudeToken {
			t.Fatalf("iteration %d: got token %q, want the claudeAiOauth token %q",
				i, c.Token, claudeToken)
		}
	}
}

func TestExtractCredentialFields(t *testing.T) {
	exp := futureMillis()
	c, err := extractCCCredentials([]byte(realWorldCredentials(exp)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Label != "Claude Code (team)" {
		t.Errorf("label: got %q, want %q", c.Label, "Claude Code (team)")
	}
	if got, want := c.ExpiresAt.UnixMilli(), exp; got != want {
		t.Errorf("expiry: got %d, want %d", got, want)
	}
	if c.Expired() {
		t.Error("Expired() true for a token that expires in an hour")
	}
}

// The MCP subtree must not supply the label either: a third-party grant can
// carry its own email or displayName.
func TestLabelIgnoresMCPSubtree(t *testing.T) {
	raw := []byte(`{
	  "mcpOAuth": {
	    "harvest|abc": {"accessToken": "t", "email": "billing@harvestapp.com"}
	  },
	  "claudeAiOauth": {"accessToken": "claude", "subscriptionType": "max"}
	}`)
	c, err := extractCCCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Label != "Claude Code (max)" {
		t.Errorf("label: got %q, want %q", c.Label, "Claude Code (max)")
	}
}

func TestExpiredToken(t *testing.T) {
	past := time.Now().Add(-90 * time.Minute).UnixMilli()
	c, err := extractCCCredentials([]byte(realWorldCredentials(past)))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Expired() {
		t.Error("Expired() false for a token that expired 90 minutes ago")
	}
}

func TestExpiryUnitsAndAbsence(t *testing.T) {
	ms := time.Now().Add(time.Hour).UnixMilli()
	sec := ms / 1000

	cases := []struct {
		name string
		json string
		want time.Time
	}{
		{"milliseconds", fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"t","expiresAt":%d}}`, ms), time.UnixMilli(ms)},
		{"seconds", fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"t","expiresAt":%d}}`, sec), time.Unix(sec, 0)},
		{"string millis", fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"t","expiresAt":"%d"}}`, ms), time.UnixMilli(ms)},
		{"rfc3339", `{"claudeAiOauth":{"accessToken":"t","expiresAt":"2026-08-03T18:13:58Z"}}`, time.Date(2026, 8, 3, 18, 13, 58, 0, time.UTC)},
		{"absent", `{"claudeAiOauth":{"accessToken":"t"}}`, time.Time{}},
		{"zero", `{"claudeAiOauth":{"accessToken":"t","expiresAt":0}}`, time.Time{}},
		{"garbage", `{"claudeAiOauth":{"accessToken":"t","expiresAt":"soon"}}`, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := extractCCCredentials([]byte(tc.json))
			if err != nil {
				t.Fatal(err)
			}
			if !c.ExpiresAt.Equal(tc.want) {
				t.Errorf("expiry: got %v, want %v", c.ExpiresAt, tc.want)
			}
			// An unparseable or absent expiry must never be treated as expired,
			// or an unknown schema would block polling outright.
			if tc.want.IsZero() && c.Expired() {
				t.Error("Expired() true with no usable expiry")
			}
		})
	}
}

// An unrecognised schema still falls back to a walk, but must skip MCP grants.
func TestFallbackWalkSkipsMCP(t *testing.T) {
	raw := []byte(`{
	  "mcpOAuth": {"harvest|abc": {"accessToken": "` + harvestToken + `"}},
	  "someFutureOauthKey": {"nested": {"accessToken": "` + claudeToken + `"}}
	}`)
	for i := 0; i < 200; i++ {
		c, err := extractCCCredentials(raw)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if c.Token != claudeToken {
			t.Fatalf("iteration %d: got %q, want %q", i, c.Token, claudeToken)
		}
	}
}

// With nothing but MCP grants there is no Claude token to find, and erroring is
// correct: polling with a foreign token yields a misleading 401.
func TestNoClaudeTokenIsAnError(t *testing.T) {
	raw := []byte(`{"mcpOAuth": {"harvest|abc": {"accessToken": "` + harvestToken + `"}}}`)
	if c, err := extractCCCredentials(raw); err == nil {
		t.Errorf("want error, got token %q", c.Token)
	}
}

func TestExtractErrors(t *testing.T) {
	for _, tc := range []struct{ name, json string }{
		{"malformed", `{"claudeAiOauth":`},
		{"no token", `{"claudeAiOauth":{"subscriptionType":"team"}}`},
		{"empty token", `{"claudeAiOauth":{"accessToken":""}}`},
		{"empty object", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractCCCredentials([]byte(tc.json)); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestReadClaudeCodeCredentialsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.json")
	if err := os.WriteFile(path, []byte(realWorldCredentials(futureMillis())), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadClaudeCodeCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != claudeToken {
		t.Errorf("token: got %q, want %q", c.Token, claudeToken)
	}
}

func TestSnakeCaseSchema(t *testing.T) {
	raw := []byte(`{"claude_ai_oauth":{"access_token":"` + claudeToken + `","subscription_type":"pro"}}`)
	c, err := extractCCCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != claudeToken {
		t.Errorf("token: got %q, want %q", c.Token, claudeToken)
	}
	if c.Label != "Claude Code (pro)" {
		t.Errorf("label: got %q", c.Label)
	}
}

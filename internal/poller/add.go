package poller

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

// AddAccountResult is the outcome of an AddAccountWithToken call.
type AddAccountResult struct {
	OrgID string
	Label string
}

// CredentialSpec carries the optional refresh-token and metadata that an
// import flow may have available alongside the access token. Empty fields are
// fine — UpsertCredentialForAccount preserves existing values on update.
type CredentialSpec struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAt        int64  // unix seconds; 0 = unknown
	Scopes           string // space-separated, or comma-separated — stored as-is
	SubscriptionType string
	RateLimitTier    string
	Source           string // 'token' | 'env' | 'claude-code'; defaults to 'token'
}

// AddAccountWithToken validates a raw access token via the API and writes a
// minimal credential. Kept for the existing CLI surface (`add-token`,
// import-env) that has no refresh token to thread through.
func (p *Poller) AddAccountWithToken(ctx context.Context, token, email, labelHint string) (AddAccountResult, error) {
	return p.AddAccountWithCredential(ctx, CredentialSpec{AccessToken: token, Source: "token"}, email, labelHint)
}

// AddAccountWithCredential validates the access token via the API (OAuth Usage
// first, CountTokens fallback for org-id) and persists the full credential
// spec. Source defaults to "token" when unset.
func (p *Poller) AddAccountWithCredential(ctx context.Context, spec CredentialSpec, email, labelHint string) (AddAccountResult, error) {
	spec.AccessToken = strings.TrimSpace(spec.AccessToken)
	if spec.AccessToken == "" {
		return AddAccountResult{}, errors.New("token is empty")
	}
	if spec.Source == "" {
		spec.Source = "token"
	}

	r, err := p.API.OAuthUsage(ctx, spec.AccessToken)
	if err == nil && r.OrganizationID != "" {
		label := pickLabel(labelHint, email, r.OrganizationID)
		if err := p.persistCredential(ctx, r.OrganizationID, label, email, spec, r); err != nil {
			return AddAccountResult{}, err
		}
		return AddAccountResult{OrgID: r.OrganizationID, Label: label}, nil
	}

	if errors.Is(err, api.ErrUnauthorized) {
		return AddAccountResult{}, errors.New("token rejected (401)")
	}

	org, ctErr := p.API.CountTokens(ctx, spec.AccessToken)
	if ctErr != nil {
		if err != nil {
			return AddAccountResult{}, fmt.Errorf("oauth_usage: %w; count_tokens: %v", err, ctErr)
		}
		return AddAccountResult{}, fmt.Errorf("count_tokens: %w", ctErr)
	}
	label := pickLabel(labelHint, email, org)
	if err := p.persistCredential(ctx, org, label, email, spec, api.UsageReading{}); err != nil {
		return AddAccountResult{}, err
	}
	return AddAccountResult{OrgID: org, Label: label}, nil
}

// pickLabel applies labelHint > email > short-org-id ordering.
func pickLabel(labelHint, email, orgID string) string {
	if labelHint != "" {
		return labelHint
	}
	if email != "" {
		return email
	}
	// Shorten an opaque UUID-like org id so the menu/cards don't show a
	// 36-character mess. We keep the first 8 chars prefixed with "org…".
	if len(orgID) > 12 {
		return "org-" + orgID[:8]
	}
	return orgID
}

func (p *Poller) persistCredential(ctx context.Context, accountID, label, email string, spec CredentialSpec, r api.UsageReading) error {
	if err := p.Store.UpsertAccount(ctx, nil, accountID, label, email, "Max"); err != nil {
		return err
	}
	if err := p.Store.UpsertCredentialForAccount(ctx, nil, store.CredentialUpsertSpec{
		AccountID:        accountID,
		Label:            label,
		Source:           spec.Source,
		AccessToken:      spec.AccessToken,
		RefreshToken:     spec.RefreshToken,
		ExpiresAt:        spec.ExpiresAt,
		Scopes:           spec.Scopes,
		SubscriptionType: spec.SubscriptionType,
		RateLimitTier:    spec.RateLimitTier,
	}); err != nil {
		return err
	}
	if !r.FiveHourReset.IsZero() || !r.SevenDayReset.IsZero() {
		return p.writeReading(ctx, accountID, r)
	}
	return nil
}

// EnvImportResult is the outcome of importing one ACCOUNT_EMAIL_N/ACCOUNT_KEY_N pair.
type EnvImportResult struct {
	Email   string
	OrgID   string
	Success bool
	Error   string
}

// ImportFromEnv parses an .env-style reader and imports each ACCOUNT_EMAIL_N /
// ACCOUNT_KEY_N pair as an account, returning per-pair results.
func (p *Poller) ImportFromEnv(ctx context.Context, r io.Reader) ([]EnvImportResult, error) {
	env, err := parseEnv(r)
	if err != nil {
		return nil, err
	}
	var pairs []struct{ idx int; email, key string }
	for i := 1; i <= 99; i++ {
		em := env[fmt.Sprintf("ACCOUNT_EMAIL_%d", i)]
		k := env[fmt.Sprintf("ACCOUNT_KEY_%d", i)]
		if em == "" || k == "" {
			continue
		}
		pairs = append(pairs, struct{ idx int; email, key string }{i, em, k})
	}
	if len(pairs) == 0 {
		return nil, errors.New("no ACCOUNT_EMAIL_N / ACCOUNT_KEY_N pairs found")
	}
	var out []EnvImportResult
	for _, pp := range pairs {
		res, err := p.AddAccountWithToken(ctx, pp.key, pp.email, "")
		r := EnvImportResult{Email: pp.email, Success: err == nil}
		if err != nil {
			r.Error = err.Error()
		} else {
			r.OrgID = res.OrgID
		}
		out = append(out, r)
	}
	return out, nil
}

func parseEnv(r io.Reader) (map[string]string, error) {
	env := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		// Strip surrounding quotes.
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		env[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return env, nil
}

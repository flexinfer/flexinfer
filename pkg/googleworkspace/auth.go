package googleworkspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"golang.org/x/oauth2"
	oauthgoogle "golang.org/x/oauth2/google"

	"github.com/crb2nu/loom/pkg/secrets"
)

const (
	SecretClientID     = "GOOGLE_WORKSPACE_CLIENT_ID"
	SecretClientSecret = "GOOGLE_WORKSPACE_CLIENT_SECRET"
	SecretRefreshToken = "GOOGLE_WORKSPACE_REFRESH_TOKEN"
	SecretScopes       = "GOOGLE_WORKSPACE_SCOPES"
	SecretAccountEmail = "GOOGLE_WORKSPACE_ACCOUNT_EMAIL"

	DefaultUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

	ScopeOpenID  = "openid"
	ScopeEmail   = "email"
	ScopeProfile = "profile"

	ScopeGmailReadonly = "https://www.googleapis.com/auth/gmail.readonly"
	ScopeGmailModify   = "https://www.googleapis.com/auth/gmail.modify"
	ScopeGmailSend     = "https://www.googleapis.com/auth/gmail.send"

	ScopeCalendarReadonly = "https://www.googleapis.com/auth/calendar.readonly"
	ScopeCalendar         = "https://www.googleapis.com/auth/calendar"

	ScopeDocsReadonly = "https://www.googleapis.com/auth/documents.readonly"
	ScopeDocs         = "https://www.googleapis.com/auth/documents"

	ScopeDriveMetadataReadonly = "https://www.googleapis.com/auth/drive.metadata.readonly"
)

var presetScopes = map[string][]string{
	"identity": {
		ScopeOpenID,
		ScopeEmail,
		ScopeProfile,
	},
	"gmail": {
		ScopeGmailReadonly,
		ScopeGmailModify,
		ScopeGmailSend,
	},
	"calendar": {
		ScopeCalendarReadonly,
		ScopeCalendar,
	},
	"docs": {
		ScopeDocsReadonly,
		ScopeDocs,
	},
	"drive": {
		ScopeDriveMetadataReadonly,
	},
}

// Credentials describes the Google OAuth client/session configuration Loom uses.
type Credentials struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	Scopes       []string
	AccountEmail string
}

// UserInfo is the subset of OpenID userinfo that Loom reports.
type UserInfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// ParseScopes expands preset names and normalizes a scope list.
func ParseScopes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultScopes(), nil
	}

	var scopes []string
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	for _, field := range fields {
		token := strings.TrimSpace(field)
		if token == "" {
			continue
		}
		key := strings.ToLower(token)
		switch key {
		case "full", "all":
			scopes = append(scopes, DefaultScopes()...)
		case "readonly", "read-only":
			scopes = append(scopes, ReadonlyScopes()...)
		default:
			if preset, ok := presetScopes[key]; ok {
				scopes = append(scopes, preset...)
				continue
			}
			scopes = append(scopes, token)
		}
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("no scopes requested")
	}
	return normalizeScopes(scopes), nil
}

// DefaultScopes returns the default v1 scope set for the Google Workspace MCP.
func DefaultScopes() []string {
	return normalizeScopes([]string{
		ScopeOpenID,
		ScopeEmail,
		ScopeProfile,
		ScopeGmailReadonly,
		ScopeGmailModify,
		ScopeGmailSend,
		ScopeCalendarReadonly,
		ScopeCalendar,
		ScopeDocsReadonly,
		ScopeDocs,
		ScopeDriveMetadataReadonly,
	})
}

// ReadonlyScopes returns the conservative read-only preset.
func ReadonlyScopes() []string {
	return normalizeScopes([]string{
		ScopeOpenID,
		ScopeEmail,
		ScopeProfile,
		ScopeGmailReadonly,
		ScopeCalendarReadonly,
		ScopeDocsReadonly,
		ScopeDriveMetadataReadonly,
	})
}

// FormatScopes returns a stable, comma-delimited scope string.
func FormatScopes(scopes []string) string {
	return strings.Join(normalizeScopes(scopes), ",")
}

// LoadClientCredentials loads the Google OAuth client configuration from env or Loom secrets.
func LoadClientCredentials(mgr *secrets.Manager) (*Credentials, error) {
	creds := &Credentials{
		ClientID:     resolveValue(SecretClientID, mgr),
		ClientSecret: resolveValue(SecretClientSecret, mgr),
		Scopes:       parseStoredScopes(resolveValue(SecretScopes, mgr)),
		AccountEmail: resolveValue(SecretAccountEmail, mgr),
	}
	if creds.ClientID == "" {
		return nil, fmt.Errorf("%s is not configured", SecretClientID)
	}
	if creds.ClientSecret == "" {
		return nil, fmt.Errorf("%s is not configured", SecretClientSecret)
	}
	if len(creds.Scopes) == 0 {
		creds.Scopes = DefaultScopes()
	}
	return creds, nil
}

// LoadRuntimeCredentials loads the full Google OAuth session from env or Loom secrets.
func LoadRuntimeCredentials(mgr *secrets.Manager) (*Credentials, error) {
	creds, err := LoadClientCredentials(mgr)
	if err != nil {
		return nil, err
	}
	creds.RefreshToken = resolveValue(SecretRefreshToken, mgr)
	if creds.RefreshToken == "" {
		return nil, fmt.Errorf("%s is not configured", SecretRefreshToken)
	}
	return creds, nil
}

// SaveClientCredentials stores the Google OAuth client configuration in Loom secrets.
func SaveClientCredentials(mgr *secrets.Manager, creds *Credentials) error {
	if mgr == nil {
		return fmt.Errorf("secret manager is required")
	}
	if strings.TrimSpace(creds.ClientID) == "" {
		return fmt.Errorf("client ID is required")
	}
	if strings.TrimSpace(creds.ClientSecret) == "" {
		return fmt.Errorf("client secret is required")
	}
	if err := mgr.Set(SecretClientID, strings.TrimSpace(creds.ClientID)); err != nil {
		return err
	}
	if err := mgr.Set(SecretClientSecret, strings.TrimSpace(creds.ClientSecret)); err != nil {
		return err
	}
	if len(creds.Scopes) > 0 {
		if err := mgr.Set(SecretScopes, FormatScopes(creds.Scopes)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(creds.AccountEmail) != "" {
		if err := mgr.Set(SecretAccountEmail, strings.TrimSpace(creds.AccountEmail)); err != nil {
			return err
		}
	}
	return nil
}

// SaveSession stores the refresh token and session metadata in Loom secrets.
func SaveSession(mgr *secrets.Manager, refreshToken string, scopes []string, accountEmail string) error {
	if mgr == nil {
		return fmt.Errorf("secret manager is required")
	}
	if strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("refresh token is required")
	}
	if err := mgr.Set(SecretRefreshToken, strings.TrimSpace(refreshToken)); err != nil {
		return err
	}
	if len(scopes) > 0 {
		if err := mgr.Set(SecretScopes, FormatScopes(scopes)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(accountEmail) != "" {
		if err := mgr.Set(SecretAccountEmail, strings.TrimSpace(accountEmail)); err != nil {
			return err
		}
	}
	return nil
}

// DeleteSession removes the stored Google OAuth session metadata.
func DeleteSession(mgr *secrets.Manager, clearClient bool) error {
	if mgr == nil {
		return fmt.Errorf("secret manager is required")
	}
	keys := []string{
		SecretRefreshToken,
		SecretScopes,
		SecretAccountEmail,
	}
	if clearClient {
		keys = append(keys, SecretClientID, SecretClientSecret)
	}
	var errs []string
	for _, key := range keys {
		if err := mgr.Delete(key); err != nil && !strings.Contains(err.Error(), "not found") {
			errs = append(errs, fmt.Sprintf("%s: %v", key, err))
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("delete google workspace secrets: %s", strings.Join(errs, "; "))
	}
	return nil
}

// OAuthConfig builds the OAuth 2.0 config for Google installed-app flows.
func (c *Credentials) OAuthConfig(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes:       normalizeScopes(c.Scopes),
		Endpoint:     oauthgoogle.Endpoint,
	}
}

// TokenSource builds a refresh-token-backed OAuth token source.
func (c *Credentials) TokenSource(ctx context.Context, baseHTTPClient *http.Client) oauth2.TokenSource {
	if baseHTTPClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, baseHTTPClient)
	}
	return c.OAuthConfig("").TokenSource(ctx, &oauth2.Token{RefreshToken: c.RefreshToken})
}

// AccessToken refreshes or returns the current access token.
func (c *Credentials) AccessToken(ctx context.Context, baseHTTPClient *http.Client) (*oauth2.Token, error) {
	if strings.TrimSpace(c.RefreshToken) == "" {
		return nil, fmt.Errorf("refresh token is required")
	}
	return c.TokenSource(ctx, baseHTTPClient).Token()
}

// NewHTTPClient returns an OAuth-authorized HTTP client.
func (c *Credentials) NewHTTPClient(ctx context.Context, baseHTTPClient *http.Client) (*http.Client, error) {
	if strings.TrimSpace(c.RefreshToken) == "" {
		return nil, fmt.Errorf("refresh token is required")
	}
	if baseHTTPClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, baseHTTPClient)
	}
	return oauth2.NewClient(ctx, c.TokenSource(ctx, baseHTTPClient)), nil
}

// FetchUserInfo calls Google's OpenID userinfo endpoint using the stored session.
func FetchUserInfo(ctx context.Context, baseHTTPClient *http.Client, creds *Credentials) (*UserInfo, error) {
	client, err := creds.NewHTTPClient(ctx, baseHTTPClient)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DefaultUserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read userinfo response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("userinfo request failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info UserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decode userinfo response: %w", err)
	}
	return &info, nil
}

// ParseClientCredentialsJSON extracts an installed-app or web OAuth client config.
func ParseClientCredentialsJSON(data []byte) (*Credentials, error) {
	type clientSpec struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	type envelope struct {
		Installed *clientSpec `json:"installed"`
		Web       *clientSpec `json:"web"`
	}

	var cfg envelope
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse Google OAuth client JSON: %w", err)
	}

	spec := cfg.Installed
	if spec == nil {
		spec = cfg.Web
	}
	if spec == nil {
		return nil, fmt.Errorf("google OAuth client JSON must contain installed or web credentials")
	}
	if strings.TrimSpace(spec.ClientID) == "" || strings.TrimSpace(spec.ClientSecret) == "" {
		return nil, fmt.Errorf("google OAuth client JSON is missing client_id or client_secret")
	}
	return &Credentials{
		ClientID:     strings.TrimSpace(spec.ClientID),
		ClientSecret: strings.TrimSpace(spec.ClientSecret),
		Scopes:       DefaultScopes(),
	}, nil
}

func parseStoredScopes(raw string) []string {
	scopes, err := ParseScopes(raw)
	if err != nil {
		return nil
	}
	return scopes
}

func resolveValue(key string, mgr *secrets.Manager) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	if mgr == nil {
		return ""
	}
	value, _, err := mgr.Get(key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	return result
}

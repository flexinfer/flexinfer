// Package daemon provides an OAuth 2.1 authorization server for MCP.
// Implements RFC 8414 (AS Metadata), RFC 9728 (Protected Resource Metadata),
// RFC 7591 (Dynamic Client Registration), and PKCE (S256).
package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OAuthConfig controls the built-in OAuth 2.1 authorization server.
type OAuthConfig struct {
	// Enabled activates the OAuth 2.1 authorization server endpoints.
	Enabled bool `yaml:"enabled"`

	// Issuer is the OAuth 2.1 issuer identifier (default: derived from HTTP addr).
	Issuer string `yaml:"issuer,omitempty"`

	// TokenTTLMinutes is the access token lifetime (default: 60).
	TokenTTLMinutes int `yaml:"token_ttl_minutes,omitempty"`

	// AuthCodeTTLSeconds is the authorization code lifetime (default: 600).
	AuthCodeTTLSeconds int `yaml:"auth_code_ttl_seconds,omitempty"`

	// AllowDynamicRegistration enables RFC 7591 dynamic client registration (default: true).
	AllowDynamicRegistration *bool `yaml:"allow_dynamic_registration,omitempty"`
}

// DefaultOAuthConfig returns a disabled OAuth configuration.
func DefaultOAuthConfig() OAuthConfig {
	t := true
	return OAuthConfig{
		Enabled:                  false,
		TokenTTLMinutes:          60,
		AuthCodeTTLSeconds:       600,
		AllowDynamicRegistration: &t,
	}
}

func (c *OAuthConfig) tokenTTL() time.Duration {
	if c.TokenTTLMinutes > 0 {
		return time.Duration(c.TokenTTLMinutes) * time.Minute
	}
	return 60 * time.Minute
}

func (c *OAuthConfig) codeTTL() time.Duration {
	if c.AuthCodeTTLSeconds > 0 {
		return time.Duration(c.AuthCodeTTLSeconds) * time.Second
	}
	return 600 * time.Second
}

func (c *OAuthConfig) dynamicRegistrationAllowed() bool {
	if c.AllowDynamicRegistration == nil {
		return true
	}
	return *c.AllowDynamicRegistration
}

// OAuthClient represents a registered OAuth 2.1 client.
type OAuthClient struct {
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	RedirectURIs []string  `json:"redirect_uris"`
	ClientName   string    `json:"client_name,omitempty"`
	GrantTypes   []string  `json:"grant_types"`
	Scope        string    `json:"scope,omitempty"`
	CreatedAt    time.Time `json:"client_id_issued_at,omitempty"`
}

type authCode struct {
	code                string
	clientID            string
	redirectURI         string
	scope               string
	codeChallenge       string
	codeChallengeMethod string
	expiresAt           time.Time
}

type tokenRecord struct {
	accessToken string
	clientID    string
	scope       string
	issuedAt    time.Time
	expiresAt   time.Time
}

// OAuthServer is the built-in OAuth 2.1 authorization server.
type OAuthServer struct {
	cfg     OAuthConfig
	issuer  string
	clients map[string]*OAuthClient
	codes   map[string]*authCode
	tokens  map[string]*tokenRecord
	mu      sync.RWMutex
	logger  *slog.Logger
}

// NewOAuthServer creates an OAuth 2.1 authorization server.
// Returns nil when OAuth is disabled.
func NewOAuthServer(cfg OAuthConfig, issuer string, logger *slog.Logger) *OAuthServer {
	if !cfg.Enabled {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("OAuth 2.1 authorization server enabled", "issuer", issuer)
	return &OAuthServer{
		cfg:     cfg,
		issuer:  issuer,
		clients: make(map[string]*OAuthClient),
		codes:   make(map[string]*authCode),
		tokens:  make(map[string]*tokenRecord),
		logger:  logger,
	}
}

// Authenticate implements gateway.Authenticator for OAuth2 bearer tokens.
func (s *OAuthServer) Authenticate(r *http.Request) error {
	token := extractBearerToken(r)
	if token == "" {
		return fmt.Errorf("missing bearer token")
	}
	if !s.validateToken(token) {
		return fmt.Errorf("invalid or expired token")
	}
	return nil
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return r.URL.Query().Get("token")
}

func (s *OAuthServer) validateToken(token string) bool {
	s.mu.RLock()
	rec, ok := s.tokens[token]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(rec.expiresAt) {
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
		return false
	}
	return true
}

// HandleMetadata serves the OAuth 2.1 Authorization Server Metadata (RFC 8414).
// GET /.well-known/oauth-authorization-server
func (s *OAuthServer) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	meta := map[string]any{
		"issuer":                                s.issuer,
		"authorization_endpoint":                s.issuer + "/oauth2/authorize",
		"token_endpoint":                        s.issuer + "/oauth2/token",
		"revocation_endpoint":                   s.issuer + "/oauth2/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp"},
	}
	if s.cfg.dynamicRegistrationAllowed() {
		meta["registration_endpoint"] = s.issuer + "/oauth2/register"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

// HandleResourceMetadata serves the Protected Resource Metadata (RFC 9728).
// GET /.well-known/oauth-protected-resource
func (s *OAuthServer) HandleResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	meta := map[string]any{
		"resource":                 s.issuer + "/mcp",
		"authorization_servers":    []string{s.issuer},
		"scopes_supported":         []string{"mcp"},
		"bearer_methods_supported": []string{"header"},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

// HandleRegister handles dynamic client registration (RFC 7591).
// POST /oauth2/register
func (s *OAuthServer) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.cfg.dynamicRegistrationAllowed() {
		http.Error(w, `{"error":"registration_not_supported"}`, http.StatusForbidden)
		return
	}

	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
		GrantTypes   []string `json:"grant_types"`
		Scope        string   `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		oauthError(w, "invalid_client_metadata", "malformed request body", http.StatusBadRequest)
		return
	}

	if len(req.RedirectURIs) == 0 {
		oauthError(w, "invalid_redirect_uri", "redirect_uris required", http.StatusBadRequest)
		return
	}

	// Default grant types to authorization_code per OAuth 2.1
	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{"authorization_code"}
	}
	for _, gt := range req.GrantTypes {
		if gt != "authorization_code" {
			oauthError(w, "invalid_client_metadata", "only authorization_code grant supported", http.StatusBadRequest)
			return
		}
	}

	clientID, err := generateRandomString(16)
	if err != nil {
		s.logger.Error("failed to generate client_id", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	client := &OAuthClient{
		ClientID:     clientID,
		RedirectURIs: req.RedirectURIs,
		ClientName:   req.ClientName,
		GrantTypes:   req.GrantTypes,
		Scope:        req.Scope,
		CreatedAt:    time.Now().UTC(),
	}

	s.mu.Lock()
	s.clients[clientID] = client
	s.mu.Unlock()

	s.logger.Info("OAuth client registered",
		"client_id", clientID,
		"client_name", req.ClientName,
		"redirect_uris", req.RedirectURIs,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(client)
}

// HandleAuthorize handles the authorization endpoint.
// GET /oauth2/authorize
// Auto-approves and redirects with authorization code (daemon has no consent UI).
func (s *OAuthServer) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	redirectURI := r.URL.Query().Get("redirect_uri")
	codeChallenge := r.URL.Query().Get("code_challenge")
	codeChallengeMethod := r.URL.Query().Get("code_challenge_method")
	state := r.URL.Query().Get("state")
	scope := r.URL.Query().Get("scope")

	// Validate required parameters
	if clientID == "" || redirectURI == "" {
		oauthError(w, "invalid_request", "client_id and redirect_uri required", http.StatusBadRequest)
		return
	}

	// PKCE is mandatory per OAuth 2.1
	if codeChallenge == "" || codeChallengeMethod != "S256" {
		oauthError(w, "invalid_request", "code_challenge with S256 method required", http.StatusBadRequest)
		return
	}

	// Validate client
	s.mu.RLock()
	client, ok := s.clients[clientID]
	s.mu.RUnlock()
	if !ok {
		oauthError(w, "invalid_client", "unknown client_id", http.StatusUnauthorized)
		return
	}

	// Validate redirect_uri matches registration
	if !containsString(client.RedirectURIs, redirectURI) {
		oauthError(w, "invalid_redirect_uri", "redirect_uri not registered", http.StatusBadRequest)
		return
	}

	// Generate authorization code
	code, err := generateRandomString(32)
	if err != nil {
		s.logger.Error("failed to generate auth code", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ac := &authCode{
		code:                code,
		clientID:            clientID,
		redirectURI:         redirectURI,
		scope:               scope,
		codeChallenge:       codeChallenge,
		codeChallengeMethod: codeChallengeMethod,
		expiresAt:           time.Now().Add(s.cfg.codeTTL()),
	}

	s.mu.Lock()
	s.codes[code] = ac
	s.mu.Unlock()

	// Redirect with code (auto-approve)
	location := redirectURI + "?code=" + code
	if state != "" {
		location += "&state=" + state
	}

	s.logger.Info("OAuth authorization code issued",
		"client_id", clientID,
		"scope", scope,
	)

	http.Redirect(w, r, location, http.StatusFound)
}

// HandleToken handles the token endpoint.
// POST /oauth2/token
func (s *OAuthServer) HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request", "malformed form data", http.StatusBadRequest)
		return
	}

	grantType := r.PostFormValue("grant_type")
	if grantType != "authorization_code" {
		oauthError(w, "unsupported_grant_type", "only authorization_code supported", http.StatusBadRequest)
		return
	}

	code := r.PostFormValue("code")
	redirectURI := r.PostFormValue("redirect_uri")
	codeVerifier := r.PostFormValue("code_verifier")
	clientID := r.PostFormValue("client_id")

	if code == "" || codeVerifier == "" || clientID == "" {
		oauthError(w, "invalid_request", "code, code_verifier, and client_id required", http.StatusBadRequest)
		return
	}

	// Consume the authorization code (one-time use)
	s.mu.Lock()
	ac, ok := s.codes[code]
	if ok {
		delete(s.codes, code)
	}
	s.mu.Unlock()

	if !ok {
		oauthError(w, "invalid_grant", "unknown or expired authorization code", http.StatusBadRequest)
		return
	}

	// Check expiration
	if time.Now().After(ac.expiresAt) {
		oauthError(w, "invalid_grant", "authorization code expired", http.StatusBadRequest)
		return
	}

	// Validate client_id matches
	if ac.clientID != clientID {
		oauthError(w, "invalid_grant", "client_id mismatch", http.StatusBadRequest)
		return
	}

	// Validate redirect_uri matches
	if redirectURI != "" && ac.redirectURI != redirectURI {
		oauthError(w, "invalid_grant", "redirect_uri mismatch", http.StatusBadRequest)
		return
	}

	// PKCE verification: BASE64URL(SHA256(code_verifier)) must equal code_challenge
	if pkceS256(codeVerifier) != ac.codeChallenge {
		oauthError(w, "invalid_grant", "PKCE verification failed", http.StatusBadRequest)
		return
	}

	// Issue access token
	accessToken, err := generateRandomString(32)
	if err != nil {
		s.logger.Error("failed to generate access token", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	ttl := s.cfg.tokenTTL()
	rec := &tokenRecord{
		accessToken: accessToken,
		clientID:    clientID,
		scope:       ac.scope,
		issuedAt:    now,
		expiresAt:   now.Add(ttl),
	}

	s.mu.Lock()
	s.tokens[accessToken] = rec
	s.mu.Unlock()

	s.logger.Info("OAuth access token issued",
		"client_id", clientID,
		"scope", ac.scope,
		"expires_in", int(ttl.Seconds()),
	)

	resp := map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(ttl.Seconds()),
	}
	if ac.scope != "" {
		resp["scope"] = ac.scope
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(resp)
}

// HandleRevoke handles token revocation (RFC 7009).
// POST /oauth2/revoke
func (s *OAuthServer) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request", "malformed form data", http.StatusBadRequest)
		return
	}

	token := r.PostFormValue("token")
	if token == "" {
		oauthError(w, "invalid_request", "token required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	_, existed := s.tokens[token]
	delete(s.tokens, token)
	s.mu.Unlock()

	if existed {
		s.logger.Info("OAuth token revoked")
	}

	// RFC 7009: always return 200, even if token was already invalid
	w.WriteHeader(http.StatusOK)
}

// ReapExpired removes expired codes and tokens. Called periodically.
func (s *OAuthServer) ReapExpired() int {
	now := time.Now()
	reaped := 0

	s.mu.Lock()
	defer s.mu.Unlock()

	for k, ac := range s.codes {
		if now.After(ac.expiresAt) {
			delete(s.codes, k)
			reaped++
		}
	}
	for k, rec := range s.tokens {
		if now.After(rec.expiresAt) {
			delete(s.tokens, k)
			reaped++
		}
	}
	return reaped
}

// oauthError writes a standard OAuth 2.1 error response.
func oauthError(w http.ResponseWriter, code, description string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// pkceS256 computes the PKCE S256 code challenge from a verifier.
func pkceS256(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// generateRandomString produces a URL-safe random string.
func generateRandomString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOAuth_DisabledReturnsNil(t *testing.T) {
	cfg := DefaultOAuthConfig()
	s := NewOAuthServer(cfg, "http://localhost:8088", slog.Default())
	if s != nil {
		t.Fatal("expected nil OAuth server when disabled")
	}
}

func TestOAuth_Metadata(t *testing.T) {
	s := newTestOAuthServer(t)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()

	s.HandleMetadata(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}

	var meta map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}

	if meta["issuer"] != "http://localhost:8088" {
		t.Errorf("issuer: got %v", meta["issuer"])
	}
	if meta["authorization_endpoint"] != "http://localhost:8088/oauth2/authorize" {
		t.Errorf("authorization_endpoint: got %v", meta["authorization_endpoint"])
	}
	if meta["token_endpoint"] != "http://localhost:8088/oauth2/token" {
		t.Errorf("token_endpoint: got %v", meta["token_endpoint"])
	}
	if meta["registration_endpoint"] != "http://localhost:8088/oauth2/register" {
		t.Errorf("registration_endpoint: got %v", meta["registration_endpoint"])
	}

	methods := meta["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code_challenge_methods: got %v", methods)
	}
}

func TestOAuth_ResourceMetadata(t *testing.T) {
	s := newTestOAuthServer(t)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	w := httptest.NewRecorder()

	s.HandleResourceMetadata(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}

	var meta map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}

	if meta["resource"] != "http://localhost:8088/mcp" {
		t.Errorf("resource: got %v", meta["resource"])
	}
	servers := meta["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != "http://localhost:8088" {
		t.Errorf("authorization_servers: got %v", servers)
	}
}

func TestOAuth_DynamicRegistration(t *testing.T) {
	s := newTestOAuthServer(t)

	body := `{"redirect_uris":["http://localhost:12345/callback"],"client_name":"test-agent"}`
	r := httptest.NewRequest(http.MethodPost, "/oauth2/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.HandleRegister(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201, body: %s", w.Code, w.Body.String())
	}

	var client OAuthClient
	if err := json.Unmarshal(w.Body.Bytes(), &client); err != nil {
		t.Fatal(err)
	}

	if client.ClientID == "" {
		t.Error("expected non-empty client_id")
	}
	if client.ClientName != "test-agent" {
		t.Errorf("client_name: got %q, want test-agent", client.ClientName)
	}
	if len(client.RedirectURIs) != 1 || client.RedirectURIs[0] != "http://localhost:12345/callback" {
		t.Errorf("redirect_uris: got %v", client.RedirectURIs)
	}
}

func TestOAuth_RegistrationDisabled(t *testing.T) {
	f := false
	cfg := OAuthConfig{Enabled: true, AllowDynamicRegistration: &f}
	s := NewOAuthServer(cfg, "http://localhost:8088", slog.Default())

	body := `{"redirect_uris":["http://localhost:12345/callback"]}`
	r := httptest.NewRequest(http.MethodPost, "/oauth2/register", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.HandleRegister(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", w.Code)
	}
}

func TestOAuth_FullFlow(t *testing.T) {
	s := newTestOAuthServer(t)

	// Step 1: Register client
	clientID := registerTestClient(t, s, "http://localhost:9999/cb")

	// Step 2: Generate PKCE verifier + challenge
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := pkceS256(verifier)

	// Step 3: Authorize
	authURL := "/oauth2/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape("http://localhost:9999/cb") +
		"&code_challenge=" + challenge +
		"&code_challenge_method=S256&state=xyz&scope=mcp"

	r := httptest.NewRequest(http.MethodGet, authURL, nil)
	w := httptest.NewRecorder()
	s.HandleAuthorize(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("authorize status: got %d, want 302", w.Code)
	}

	location := w.Header().Get("Location")
	locURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}

	code := locURL.Query().Get("code")
	if code == "" {
		t.Fatal("expected code in redirect")
	}
	if locURL.Query().Get("state") != "xyz" {
		t.Errorf("state: got %q, want xyz", locURL.Query().Get("state"))
	}

	// Step 4: Exchange code for token
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/cb"},
		"code_verifier": {verifier},
		"client_id":     {clientID},
	}

	r = httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.HandleToken(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("token status: got %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var tokenResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &tokenResp); err != nil {
		t.Fatal(err)
	}

	accessToken, ok := tokenResp["access_token"].(string)
	if !ok || accessToken == "" {
		t.Fatal("expected access_token in response")
	}
	if tokenResp["token_type"] != "Bearer" {
		t.Errorf("token_type: got %v", tokenResp["token_type"])
	}
	if tokenResp["scope"] != "mcp" {
		t.Errorf("scope: got %v", tokenResp["scope"])
	}

	// Step 5: Validate token via Authenticate
	authReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	authReq.Header.Set("Authorization", "Bearer "+accessToken)
	if err := s.Authenticate(authReq); err != nil {
		t.Errorf("Authenticate failed: %v", err)
	}

	// Invalid token should fail
	authReq.Header.Set("Authorization", "Bearer invalid-token")
	if err := s.Authenticate(authReq); err == nil {
		t.Error("expected Authenticate to fail with invalid token")
	}
}

func TestOAuth_PKCERequired(t *testing.T) {
	s := newTestOAuthServer(t)
	clientID := registerTestClient(t, s, "http://localhost:9999/cb")

	// Missing code_challenge
	authURL := "/oauth2/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape("http://localhost:9999/cb")

	r := httptest.NewRequest(http.MethodGet, authURL, nil)
	w := httptest.NewRecorder()
	s.HandleAuthorize(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}

	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "invalid_request" {
		t.Errorf("error: got %q", errResp["error"])
	}
}

func TestOAuth_InvalidCodeVerifier(t *testing.T) {
	s := newTestOAuthServer(t)
	clientID := registerTestClient(t, s, "http://localhost:9999/cb")

	verifier := "correct-verifier-value-for-testing-12345678901"
	challenge := pkceS256(verifier)

	// Get auth code
	code := getAuthCode(t, s, clientID, "http://localhost:9999/cb", challenge)

	// Exchange with wrong verifier
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/cb"},
		"code_verifier": {"wrong-verifier-value-for-testing-1234567890"},
		"client_id":     {clientID},
	}

	r := httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.HandleToken(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}

	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "invalid_grant" {
		t.Errorf("error: got %q", errResp["error"])
	}
}

func TestOAuth_CodeOneTimeUse(t *testing.T) {
	s := newTestOAuthServer(t)
	clientID := registerTestClient(t, s, "http://localhost:9999/cb")

	verifier := "one-time-use-verifier-test-value-123456789"
	challenge := pkceS256(verifier)
	code := getAuthCode(t, s, clientID, "http://localhost:9999/cb", challenge)

	// First exchange: should succeed
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/cb"},
		"code_verifier": {verifier},
		"client_id":     {clientID},
	}

	r := httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.HandleToken(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("first exchange: got %d, want 200", w.Code)
	}

	// Second exchange: should fail (code consumed)
	r = httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.HandleToken(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("second exchange: got %d, want 400", w.Code)
	}
}

func TestOAuth_TokenRevocation(t *testing.T) {
	s := newTestOAuthServer(t)
	clientID := registerTestClient(t, s, "http://localhost:9999/cb")

	verifier := "revocation-test-verifier-value-1234567890123"
	challenge := pkceS256(verifier)
	code := getAuthCode(t, s, clientID, "http://localhost:9999/cb", challenge)

	// Get token
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/cb"},
		"code_verifier": {verifier},
		"client_id":     {clientID},
	}
	r := httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.HandleToken(w, r)

	var tokenResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &tokenResp)
	accessToken := tokenResp["access_token"].(string)

	// Token should be valid
	if !s.validateToken(accessToken) {
		t.Fatal("token should be valid before revocation")
	}

	// Revoke
	revokeForm := url.Values{"token": {accessToken}}
	r = httptest.NewRequest(http.MethodPost, "/oauth2/revoke", strings.NewReader(revokeForm.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.HandleRevoke(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("revoke status: got %d, want 200", w.Code)
	}

	// Token should be invalid after revocation
	if s.validateToken(accessToken) {
		t.Fatal("token should be invalid after revocation")
	}
}

func TestOAuth_ExpiredCode(t *testing.T) {
	cfg := OAuthConfig{Enabled: true, AuthCodeTTLSeconds: 1}
	s := NewOAuthServer(cfg, "http://localhost:8088", slog.Default())
	clientID := registerTestClient(t, s, "http://localhost:9999/cb")

	verifier := "expired-code-test-verifier-value-123456789012"
	challenge := pkceS256(verifier)
	code := getAuthCode(t, s, clientID, "http://localhost:9999/cb", challenge)

	// Wait for code to expire
	time.Sleep(1100 * time.Millisecond)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/cb"},
		"code_verifier": {verifier},
		"client_id":     {clientID},
	}

	r := httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.HandleToken(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestOAuth_ReapExpired(t *testing.T) {
	cfg := OAuthConfig{Enabled: true, TokenTTLMinutes: 1, AuthCodeTTLSeconds: 1}
	s := NewOAuthServer(cfg, "http://localhost:8088", slog.Default())

	// Add expired entries directly
	s.mu.Lock()
	s.codes["expired-code"] = &authCode{expiresAt: time.Now().Add(-time.Minute)}
	s.tokens["expired-token"] = &tokenRecord{expiresAt: time.Now().Add(-time.Minute)}
	s.tokens["valid-token"] = &tokenRecord{expiresAt: time.Now().Add(time.Hour)}
	s.mu.Unlock()

	reaped := s.ReapExpired()
	if reaped != 2 {
		t.Errorf("reaped: got %d, want 2", reaped)
	}

	s.mu.RLock()
	if len(s.codes) != 0 {
		t.Errorf("codes remaining: %d", len(s.codes))
	}
	if len(s.tokens) != 1 {
		t.Errorf("tokens remaining: got %d, want 1", len(s.tokens))
	}
	s.mu.RUnlock()
}

func TestOAuth_UnknownClient(t *testing.T) {
	s := newTestOAuthServer(t)

	challenge := pkceS256("some-verifier-for-testing-purposes-12345")
	authURL := "/oauth2/authorize?response_type=code&client_id=nonexistent" +
		"&redirect_uri=" + url.QueryEscape("http://localhost:9999/cb") +
		"&code_challenge=" + challenge +
		"&code_challenge_method=S256"

	r := httptest.NewRequest(http.MethodGet, authURL, nil)
	w := httptest.NewRecorder()
	s.HandleAuthorize(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", w.Code)
	}
}

func TestOAuth_InvalidRedirectURI(t *testing.T) {
	s := newTestOAuthServer(t)
	clientID := registerTestClient(t, s, "http://localhost:9999/cb")

	challenge := pkceS256("redirect-test-verifier-value-12345678901234")
	authURL := "/oauth2/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape("http://evil.com/callback") +
		"&code_challenge=" + challenge +
		"&code_challenge_method=S256"

	r := httptest.NewRequest(http.MethodGet, authURL, nil)
	w := httptest.NewRecorder()
	s.HandleAuthorize(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestOAuth_MetadataMethodNotAllowed(t *testing.T) {
	s := newTestOAuthServer(t)

	r := httptest.NewRequest(http.MethodPost, "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	s.HandleMetadata(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
}

func TestOAuth_ConcurrentTokenExchange(t *testing.T) {
	s := newTestOAuthServer(t)

	const concurrency = 10

	// Prepare 10 separate clients, auth codes, and verifiers.
	type authSetup struct {
		clientID string
		code     string
		verifier string
	}

	setups := make([]authSetup, concurrency)
	for i := 0; i < concurrency; i++ {
		clientID := registerTestClient(t, s, "http://localhost:9999/cb")
		verifier := strings.Repeat("v", 43) + strings.ReplaceAll(strings.Repeat("0", 5), "0", string(rune('a'+i)))
		challenge := pkceS256(verifier)
		code := getAuthCode(t, s, clientID, "http://localhost:9999/cb", challenge)
		setups[i] = authSetup{clientID: clientID, code: code, verifier: verifier}
	}

	// Exchange all codes concurrently.
	tokens := make([]string, concurrency)
	errors := make([]error, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			setup := setups[idx]
			form := url.Values{
				"grant_type":    {"authorization_code"},
				"code":          {setup.code},
				"redirect_uri":  {"http://localhost:9999/cb"},
				"code_verifier": {setup.verifier},
				"client_id":     {setup.clientID},
			}
			r := httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.HandleToken(w, r)

			if w.Code != http.StatusOK {
				errors[idx] = fmt.Errorf("goroutine %d: status %d, body: %s", idx, w.Code, w.Body.String())
				return
			}
			var resp map[string]any
			json.Unmarshal(w.Body.Bytes(), &resp)
			tok, _ := resp["access_token"].(string)
			tokens[idx] = tok
		}(i)
	}
	wg.Wait()

	// Check for errors.
	for i, err := range errors {
		if err != nil {
			t.Errorf("exchange %d failed: %v", i, err)
		}
	}

	// Verify all tokens are unique and non-empty.
	seen := make(map[string]bool)
	for i, tok := range tokens {
		if tok == "" {
			t.Errorf("token %d is empty", i)
			continue
		}
		if seen[tok] {
			t.Errorf("duplicate token at index %d: %s", i, tok)
		}
		seen[tok] = true
	}
}

func TestOAuth_ClientIDCollisionResistance(t *testing.T) {
	s := newTestOAuthServer(t)

	const count = 100
	ids := make(map[string]bool, count)

	for i := 0; i < count; i++ {
		clientID := registerTestClient(t, s, "http://localhost:9999/cb")
		if ids[clientID] {
			t.Fatalf("duplicate client_id at iteration %d: %s", i, clientID)
		}
		ids[clientID] = true
	}

	if len(ids) != count {
		t.Errorf("expected %d unique client_ids, got %d", count, len(ids))
	}
}

// --- helpers ---

func newTestOAuthServer(t *testing.T) *OAuthServer {
	t.Helper()
	cfg := OAuthConfig{Enabled: true}
	s := NewOAuthServer(cfg, "http://localhost:8088", slog.Default())
	if s == nil {
		t.Fatal("expected non-nil OAuth server")
	}
	return s
}

func registerTestClient(t *testing.T, s *OAuthServer, redirectURI string) string {
	t.Helper()
	body := `{"redirect_uris":["` + redirectURI + `"],"client_name":"test"}`
	r := httptest.NewRequest(http.MethodPost, "/oauth2/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.HandleRegister(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("register: got %d, body: %s", w.Code, w.Body.String())
	}

	var client OAuthClient
	json.Unmarshal(w.Body.Bytes(), &client)
	return client.ClientID
}

func getAuthCode(t *testing.T, s *OAuthServer, clientID, redirectURI, challenge string) string {
	t.Helper()
	authURL := "/oauth2/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&code_challenge=" + challenge +
		"&code_challenge_method=S256&scope=mcp"

	r := httptest.NewRequest(http.MethodGet, authURL, nil)
	w := httptest.NewRecorder()
	s.HandleAuthorize(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("authorize: got %d, body: %s", w.Code, w.Body.String())
	}

	loc := w.Header().Get("Location")
	locURL, _ := url.Parse(loc)
	code := locURL.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}
	return code
}

// Ensure io import is used (test helpers may read response bodies).
var _ = io.Discard

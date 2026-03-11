package googleworkspace

import (
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/secrets"
)

type testBackend struct {
	values map[string]string
}

func (b *testBackend) Get(key string) (string, error) { return b.values[key], nil }
func (b *testBackend) Set(key, value string) error    { b.values[key] = value; return nil }
func (b *testBackend) Delete(key string) error        { delete(b.values, key); return nil }
func (b *testBackend) List() ([]string, error)        { return nil, nil }
func (b *testBackend) Name() string                   { return "test" }
func (b *testBackend) ReadOnly() bool                 { return false }

func TestParseScopesPresets(t *testing.T) {
	scopes, err := ParseScopes("readonly,gmail")
	if err != nil {
		t.Fatalf("ParseScopes returned error: %v", err)
	}
	if len(scopes) == 0 {
		t.Fatal("expected scopes")
	}
	if !contains(scopes, ScopeGmailSend) {
		t.Fatalf("expected gmail send scope in %v", scopes)
	}
	if !contains(scopes, ScopeDocsReadonly) {
		t.Fatalf("expected docs readonly scope in %v", scopes)
	}
}

func TestParseClientCredentialsJSON(t *testing.T) {
	data := []byte(`{"installed":{"client_id":"abc.apps.googleusercontent.com","client_secret":"secret","redirect_uris":["http://127.0.0.1"]}}`)
	creds, err := ParseClientCredentialsJSON(data)
	if err != nil {
		t.Fatalf("ParseClientCredentialsJSON returned error: %v", err)
	}
	if creds.ClientID != "abc.apps.googleusercontent.com" {
		t.Fatalf("unexpected client ID: %q", creds.ClientID)
	}
	if creds.ClientSecret != "secret" {
		t.Fatalf("unexpected client secret: %q", creds.ClientSecret)
	}
}

func TestSaveAndLoadRuntimeCredentials(t *testing.T) {
	backend := &testBackend{values: map[string]string{}}
	mgr := secrets.NewManager(backend)

	if err := SaveClientCredentials(mgr, &Credentials{
		ClientID:     "cid",
		ClientSecret: "csecret",
		Scopes:       []string{ScopeOpenID, ScopeEmail},
	}); err != nil {
		t.Fatalf("SaveClientCredentials returned error: %v", err)
	}
	if err := SaveSession(mgr, "refresh-token", []string{ScopeOpenID, ScopeEmail}, "user@example.com"); err != nil {
		t.Fatalf("SaveSession returned error: %v", err)
	}

	creds, err := LoadRuntimeCredentials(mgr)
	if err != nil {
		t.Fatalf("LoadRuntimeCredentials returned error: %v", err)
	}
	if creds.RefreshToken != "refresh-token" {
		t.Fatalf("unexpected refresh token: %q", creds.RefreshToken)
	}
	if creds.AccountEmail != "user@example.com" {
		t.Fatalf("unexpected account email: %q", creds.AccountEmail)
	}
	if got := FormatScopes(creds.Scopes); !strings.Contains(got, ScopeOpenID) {
		t.Fatalf("expected openid scope in %q", got)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

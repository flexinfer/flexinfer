package templatevars

import (
	"os"
	"testing"

	"github.com/crb2nu/loom/pkg/secrets"
)

// mockBackend implements secrets.Backend for testing.
type mockBackend struct {
	store map[string]string
}

func (m *mockBackend) Get(key string) (string, error) {
	v, ok := m.store[key]
	if !ok {
		return "", nil
	}
	return v, nil
}
func (m *mockBackend) Set(key, value string) error { m.store[key] = value; return nil }
func (m *mockBackend) Delete(key string) error     { delete(m.store, key); return nil }
func (m *mockBackend) List() ([]string, error) {
	keys := make([]string, 0, len(m.store))
	for k := range m.store {
		keys = append(keys, k)
	}
	return keys, nil
}
func (m *mockBackend) Name() string   { return "mock" }
func (m *mockBackend) ReadOnly() bool { return false }

func newMockSecrets(kv map[string]string) *secrets.Manager {
	return secrets.NewManager(&mockBackend{store: kv})
}

// =============================================================================
// Expand ${env:VAR} tests
// =============================================================================

func TestExpand_EnvVar(t *testing.T) {
	t.Setenv("TEST_EXPAND_VAR", "hello")
	e := New()
	got := e.Expand("prefix-${env:TEST_EXPAND_VAR}-suffix")
	if got != "prefix-hello-suffix" {
		t.Errorf("got %q, want %q", got, "prefix-hello-suffix")
	}
}

func TestExpand_EnvVarWithDefault_Present(t *testing.T) {
	t.Setenv("TEST_PRESENT", "real-value")
	e := New()
	got := e.Expand("${env:TEST_PRESENT:-fallback}")
	if got != "real-value" {
		t.Errorf("got %q, want %q", got, "real-value")
	}
}

func TestExpand_EnvVarWithDefault_Missing(t *testing.T) {
	os.Unsetenv("TEST_MISSING_DEFAULT")
	e := New()
	got := e.Expand("${env:TEST_MISSING_DEFAULT:-http://localhost:6333}")
	if got != "http://localhost:6333" {
		t.Errorf("got %q, want %q", got, "http://localhost:6333")
	}
}

func TestExpand_EnvVarMissing_NoDefault(t *testing.T) {
	os.Unsetenv("TEST_TOTALLY_MISSING")
	e := New()
	got := e.Expand("${env:TEST_TOTALLY_MISSING}")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExpand_MultipleEnvVars(t *testing.T) {
	t.Setenv("A", "1")
	t.Setenv("B", "2")
	e := New()
	got := e.Expand("${env:A}+${env:B}")
	if got != "1+2" {
		t.Errorf("got %q, want %q", got, "1+2")
	}
}

// =============================================================================
// Expand ${keychain:VAR} tests
// =============================================================================

func TestExpand_KeychainFound(t *testing.T) {
	mgr := newMockSecrets(map[string]string{"MY_API_KEY": "secret123"})
	e := New(WithSecretsManager(mgr))
	got := e.Expand("${keychain:MY_API_KEY}")
	if got != "secret123" {
		t.Errorf("got %q, want %q", got, "secret123")
	}
}

func TestExpand_KeychainFallsBackToEnv(t *testing.T) {
	// Secret not in manager, but is in env
	mgr := newMockSecrets(map[string]string{})
	t.Setenv("FALLBACK_KEY", "from-env")
	e := New(WithSecretsManager(mgr))
	got := e.Expand("${keychain:FALLBACK_KEY}")
	if got != "from-env" {
		t.Errorf("got %q, want %q", got, "from-env")
	}
}

func TestExpand_KeychainMissing(t *testing.T) {
	mgr := newMockSecrets(map[string]string{})
	os.Unsetenv("MISSING_KC")
	e := New(WithSecretsManager(mgr))
	got := e.Expand("${keychain:MISSING_KC}")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

// =============================================================================
// Expand ${secret:VAR} tests
// =============================================================================

func TestExpand_SecretFound(t *testing.T) {
	mgr := newMockSecrets(map[string]string{"DB_PASS": "p@ss"})
	e := New(WithSecretsManager(mgr))
	got := e.Expand("${secret:DB_PASS}")
	if got != "p@ss" {
		t.Errorf("got %q, want %q", got, "p@ss")
	}
}

func TestExpand_SecretMissing(t *testing.T) {
	mgr := newMockSecrets(map[string]string{})
	e := New(WithSecretsManager(mgr))
	got := e.Expand("${secret:NOPE}")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

// =============================================================================
// Mixed pattern tests
// =============================================================================

func TestExpand_MixedPatterns(t *testing.T) {
	t.Setenv("HOST", "localhost")
	mgr := newMockSecrets(map[string]string{"TOKEN": "abc"})
	e := New(WithSecretsManager(mgr))
	got := e.Expand("http://${env:HOST}:8080?token=${keychain:TOKEN}")
	if got != "http://localhost:8080?token=abc" {
		t.Errorf("got %q, want %q", got, "http://localhost:8080?token=abc")
	}
}

func TestExpand_NoPatterns(t *testing.T) {
	e := New()
	input := "just a plain string"
	got := e.Expand(input)
	if got != input {
		t.Errorf("got %q, want %q", got, input)
	}
}

func TestExpand_PreservesRepoAndHome(t *testing.T) {
	e := New()
	input := "${repo}/foo and ${HOME}/bar"
	got := e.Expand(input)
	// Expand should NOT touch ${repo} or ${HOME}
	if got != input {
		t.Errorf("got %q, want %q (should preserve ${repo} and ${HOME})", got, input)
	}
}

func TestExpand_UnclosedBrace(t *testing.T) {
	e := New()
	input := "${env:UNCLOSED"
	got := e.Expand(input)
	if got != input {
		t.Errorf("got %q, want %q (should leave unclosed patterns)", got, input)
	}
}

// =============================================================================
// ExpandMap tests
// =============================================================================

func TestExpandMap_Basic(t *testing.T) {
	t.Setenv("MAP_VAR", "val")
	e := New()
	m := map[string]string{
		"KEY1": "${env:MAP_VAR}",
		"KEY2": "literal",
	}
	got := e.ExpandMap(m)
	if got["KEY1"] != "val" {
		t.Errorf("KEY1: got %q, want %q", got["KEY1"], "val")
	}
	if got["KEY2"] != "literal" {
		t.Errorf("KEY2: got %q, want %q", got["KEY2"], "literal")
	}
}

func TestExpandMap_Nil(t *testing.T) {
	e := New()
	got := e.ExpandMap(nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestExpandMap_DoesNotMutateInput(t *testing.T) {
	t.Setenv("MUT_VAR", "new")
	e := New()
	orig := map[string]string{"K": "${env:MUT_VAR}"}
	_ = e.ExpandMap(orig)
	if orig["K"] != "${env:MUT_VAR}" {
		t.Errorf("original map was mutated: got %q", orig["K"])
	}
}

// =============================================================================
// No secrets manager tests
// =============================================================================

func TestExpand_NoSecretsManager_Keychain(t *testing.T) {
	t.Setenv("KC_FALLBACK", "env-val")
	e := New() // no secrets manager, no lazy
	got := e.Expand("${keychain:KC_FALLBACK}")
	// Should fall back to env since no secrets manager
	if got != "env-val" {
		t.Errorf("got %q, want %q", got, "env-val")
	}
}

func TestExpand_NoSecretsManager_Secret(t *testing.T) {
	e := New() // no secrets manager
	got := e.Expand("${secret:ANYTHING}")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

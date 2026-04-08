package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveHUDURLFromHub(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		hubURL string
		want   string
	}{
		{name: "wss mcp host", hubURL: "wss://mcp.flexinfer.ai/ws", want: "https://hud.flexinfer.ai"},
		{name: "ws mcp host", hubURL: "ws://mcp.internal.example/ws", want: "http://hud.internal.example"},
		{name: "non mcp host keeps host", hubURL: "wss://gateway.example.com/ws", want: "https://gateway.example.com"},
		{name: "invalid", hubURL: "://", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := deriveHUDURLFromHub(tt.hubURL); got != tt.want {
				t.Fatalf("deriveHUDURLFromHub(%q) = %q, want %q", tt.hubURL, got, tt.want)
			}
		})
	}
}

func TestHUDBaseURLFallsBackToDerivedHubURL(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LOOM_HUD_URL", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_ID", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_SECRET", "")

	configDir := filepath.Join(tmpHome, ".config", "loom")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	config := `hub:
  url: "wss://mcp.flexinfer.ai/ws"
  cf_access_client_id: "hub-id"
  cf_access_client_secret: "hub-secret"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	// No port file present -> should fall back to derived hub URL.
	if got := hudBaseURL("3333"); got != "https://hud.flexinfer.ai" {
		t.Fatalf("hudBaseURL() = %q, want %q", got, "https://hud.flexinfer.ai")
	}
	cfID, cfSecret := hudCFAccessHeaders()
	if cfID != "hub-id" || cfSecret != "hub-secret" {
		t.Fatalf("hudCFAccessHeaders() = (%q, %q), want (%q, %q)", cfID, cfSecret, "hub-id", "hub-secret")
	}
}

func TestHUDBaseURLPrefersLocalWhenPortFileExists(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LOOM_HUD_URL", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_ID", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_SECRET", "")

	configDir := filepath.Join(tmpHome, ".config", "loom")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	// Even though config.yaml points at a remote HUD, a present port file
	// must take priority — that's the whole point of the local-first fix.
	config := `hub:
  url: "wss://mcp.flexinfer.ai/ws"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "hud.port"), []byte("4321\n"), 0o644); err != nil {
		t.Fatalf("write port file: %v", err)
	}

	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	if got := hudBaseURL("9999"); got != "http://127.0.0.1:4321" {
		t.Fatalf("hudBaseURL() = %q, want %q", got, "http://127.0.0.1:4321")
	}
}

func TestHUDBaseURLEnvVarBeatsLocalPortFile(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LOOM_HUD_URL", "https://override.example")

	configDir := filepath.Join(tmpHome, ".config", "loom")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "hud.port"), []byte("4321"), 0o644); err != nil {
		t.Fatalf("write port file: %v", err)
	}

	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	if got := hudBaseURL("9999"); got != "https://override.example" {
		t.Fatalf("hudBaseURL() = %q, want %q", got, "https://override.example")
	}
}

func TestIsLocalHUDURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1:3333", true},
		{"http://localhost:3333", true},
		{"http://[::1]:3333", true},
		{"https://hud.flexinfer.ai", false},
		{"https://gateway.example.com", false},
		{"not a url", false},
	}
	for _, tc := range cases {
		if got := isLocalHUDURL(tc.url); got != tc.want {
			t.Errorf("isLocalHUDURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestHUDRequestRetriesHTTPSForLocalTLSHUD(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOOM_HUD_URL", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_ID", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_SECRET", "")
	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse tls server url: %v", err)
	}

	data, err := hudGetFast(parsed.Port(), "/api/ping", defaultHUDTimeout)
	if err != nil {
		t.Fatalf("hudGetFast(): %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("hudGetFast() = %s, want %s", string(data), `{"ok":true}`)
	}
}

func TestHUDRequestRejectsUnexpectedHTML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOOM_HUD_CF_ACCESS_ID", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_SECRET", "")
	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/presence" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Sign in ・ Cloudflare Access</title></head><body>login</body></html>`)
	}))
	defer ts.Close()

	t.Setenv("LOOM_HUD_URL", ts.URL)

	if _, err := hudGet("3333", "/api/presence"); err == nil {
		t.Fatal("expected hudGet() to reject unexpected HTML response")
	}
}

package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/crb2nu/loom/internal/hud"
)

// fakeReloadHUDApp captures SetAdminToken calls so tests can assert
// that reloadEnvFile pushed the rotated token through to the HUD.
type fakeReloadHUDApp struct {
	mu     sync.Mutex
	tokens []string
}

func (f *fakeReloadHUDApp) StopMonitors()                             {}
func (f *fakeReloadHUDApp) RefreshMonitors()                          {}
func (f *fakeReloadHUDApp) SpawnOrchestrator() *hud.SpawnOrchestrator { return nil }
func (f *fakeReloadHUDApp) SetAdminToken(t string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, t)
}
func (f *fakeReloadHUDApp) lastToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokens) == 0 {
		return ""
	}
	return f.tokens[len(f.tokens)-1]
}

func TestReloadEnvFile_PushesAdminTokenIntoHUD(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "hud.env")
	if err := os.WriteFile(envPath, []byte("HUD_ADMIN_TOKEN=hot-loaded-token\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	fake := &fakeReloadHUDApp{}
	d := &Daemon{
		cfg:    Config{EnvFilePath: envPath},
		hudApp: fake,
		logger: slog.Default(),
	}

	if err := d.reloadEnvFile(); err != nil {
		t.Fatalf("reloadEnvFile: %v", err)
	}
	if got := fake.lastToken(); got != "hot-loaded-token" {
		t.Errorf("hud SetAdminToken last value = %q, want %q", got, "hot-loaded-token")
	}
	if got := os.Getenv("HUD_ADMIN_TOKEN"); got != "hot-loaded-token" {
		t.Errorf("os.Getenv HUD_ADMIN_TOKEN = %q, want %q", got, "hot-loaded-token")
	}
}

func TestReloadEnvFile_MissingPathIsNoop(t *testing.T) {
	d := &Daemon{cfg: Config{EnvFilePath: ""}, logger: slog.Default()}
	if err := d.reloadEnvFile(); err != nil {
		t.Errorf("empty EnvFilePath should be no-op, got %v", err)
	}
}

func TestReloadEnvFile_MissingFileIsNoop(t *testing.T) {
	d := &Daemon{
		cfg:    Config{EnvFilePath: filepath.Join(t.TempDir(), "absent.env")},
		logger: slog.Default(),
	}
	if err := d.reloadEnvFile(); err != nil {
		t.Errorf("missing env file should not error, got %v", err)
	}
}

func TestReloadEnvFile_OnlyAllowlistedKeysApplied(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "hud.env")
	body := "HUD_ADMIN_TOKEN=tok-allowed\nROGUE_INJECT=should-not-leak\n"
	if err := os.WriteFile(envPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("ROGUE_INJECT"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	d := &Daemon{
		cfg:    Config{EnvFilePath: envPath},
		hudApp: &fakeReloadHUDApp{},
		logger: slog.Default(),
	}

	if err := d.reloadEnvFile(); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("ROGUE_INJECT"); got != "" {
		t.Errorf("non-allowlisted key leaked: %q", got)
	}
	if got := os.Getenv("HUD_ADMIN_TOKEN"); got != "tok-allowed" {
		t.Errorf("HUD_ADMIN_TOKEN = %q", got)
	}
}

func TestCacheEvictionConfigDefaults(t *testing.T) {
	d := &Daemon{cfg: Config{}}
	if got := d.cacheEvictionMaxAgeOrDefault(); got <= 0 {
		t.Errorf("default max age = %v, want positive", got)
	}
	if got := d.cacheEvictionIntervalOrDefault(); got <= 0 {
		t.Errorf("default interval = %v, want positive", got)
	}

	disabled := &Daemon{cfg: Config{CacheEvictionMaxAge: -1, CacheEvictionInterval: -1}}
	if got := disabled.cacheEvictionMaxAgeOrDefault(); got >= 0 {
		t.Errorf("disabled max age = %v, want negative", got)
	}
	if got := disabled.cacheEvictionIntervalOrDefault(); got >= 0 {
		t.Errorf("disabled interval = %v, want negative", got)
	}
}

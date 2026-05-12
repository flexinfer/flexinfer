package baseimage

import "testing"

func TestLookup_KnownVersions(t *testing.T) {
	tests := []struct {
		lang, ver string
		wantImage string
	}{
		{"go", "1.24", "registry.harbor.lan/mcp/devbox-base/go:1.24"},
		{"go", "1.25", "registry.harbor.lan/mcp/devbox-base/go:1.25"},
		{"go", "1.25.10", "registry.harbor.lan/mcp/devbox-base/go:1.25"},
		{"python", "3.12", "registry.harbor.lan/mcp/devbox-base/python:3.12"},
		{"python", "3.12.13", "registry.harbor.lan/mcp/devbox-base/python:3.12"},
		{"python", "3.13", "registry.harbor.lan/mcp/devbox-base/python:3.13"},
		{"node", "20", "registry.harbor.lan/mcp/devbox-base/node:20"},
		{"node", "20.11.1", "registry.harbor.lan/mcp/devbox-base/node:20"},
		{"node", "22", "registry.harbor.lan/mcp/devbox-base/node:22"},
	}
	for _, tt := range tests {
		got := Lookup(tt.lang, tt.ver)
		if got != tt.wantImage {
			t.Errorf("Lookup(%q, %q) = %q, want %q", tt.lang, tt.ver, got, tt.wantImage)
		}
	}
}

func TestLookup_Unknown(t *testing.T) {
	if got := Lookup("go", "1.19"); got != "" {
		t.Errorf("Lookup(go, 1.19) = %q, want empty", got)
	}
	if got := Lookup("ruby", "3.0"); got != "" {
		t.Errorf("Lookup(ruby, 3.0) = %q, want empty", got)
	}
}

func TestLookup_CaseInsensitive(t *testing.T) {
	if got := Lookup("Go", "1.25"); got == "" {
		t.Error("Lookup should be case-insensitive for language")
	}
}

func TestLanguages(t *testing.T) {
	langs := Languages()
	if len(langs) != 6 {
		t.Errorf("expected 6 registered base images, got %d", len(langs))
	}
}

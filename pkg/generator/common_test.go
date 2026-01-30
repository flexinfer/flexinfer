package generator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTokens(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name     string
		value    string
		repoPath string
		context  string
		want     string
	}{
		{
			name:     "repo token local",
			value:    "${repo}/scripts/run.sh",
			repoPath: "/workspace",
			context:  "local",
			want:     "/workspace/scripts/run.sh",
		},
		{
			name:     "repo token cluster",
			value:    "${repo}/scripts/run.sh",
			repoPath: "/workspace",
			context:  "cluster",
			want:     "/app/scripts/run.sh",
		},
		{
			name:     "HOME token local",
			value:    "${HOME}/.config/app",
			repoPath: "/workspace",
			context:  "local",
			want:     home + "/.config/app",
		},
		{
			name:     "HOME token cluster",
			value:    "${HOME}/.config/app",
			repoPath: "/workspace",
			context:  "cluster",
			want:     "/home/mcp/.config/app",
		},
		{
			name:     "no tokens",
			value:    "/absolute/path",
			repoPath: "/workspace",
			context:  "local",
			want:     "/absolute/path",
		},
		{
			name:     "multiple tokens",
			value:    "${repo}/bin:${HOME}/.local/bin",
			repoPath: "/workspace",
			context:  "local",
			want:     "/workspace/bin:" + home + "/.local/bin",
		},
		{
			name:     "preserves secret patterns",
			value:    "${env:API_KEY}",
			repoPath: "/workspace",
			context:  "local",
			want:     "${env:API_KEY}",
		},
		{
			name:     "preserves keychain patterns",
			value:    "${keychain:github-token}",
			repoPath: "/workspace",
			context:  "local",
			want:     "${keychain:github-token}",
		},
		{
			name:     "preserves secret patterns",
			value:    "${secret:my-secret}",
			repoPath: "/workspace",
			context:  "local",
			want:     "${secret:my-secret}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveTokens(tt.value, tt.repoPath, tt.context)
			if got != tt.want {
				t.Errorf("ResolveTokens() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveCommand(t *testing.T) {
	tests := []struct {
		name          string
		cmd           string
		workspaceRoot string
		registryRoot  string
		context       string
		want          string
	}{
		{
			name:          "empty command",
			cmd:           "",
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "local",
			want:          "",
		},
		{
			name:          "absolute path",
			cmd:           "/usr/bin/python",
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "local",
			want:          "/usr/bin/python",
		},
		{
			name:          "simple command",
			cmd:           "python3",
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "local",
			want:          "python3",
		},
		{
			name:          "repo token",
			cmd:           "${repo}/bin/server",
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "local",
			want:          "/workspace/bin/server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveCommand(tt.cmd, tt.workspaceRoot, tt.registryRoot, tt.context)
			if got != tt.want {
				t.Errorf("ResolveCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveArgs(t *testing.T) {
	tests := []struct {
		name          string
		args          []any
		workspaceRoot string
		registryRoot  string
		context       string
		want          []string
	}{
		{
			name:          "empty args",
			args:          []any{},
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "local",
			want:          nil,
		},
		{
			name:          "string args",
			args:          []any{"--config", "/etc/app.yaml"},
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "local",
			want:          []string{"--config", "/etc/app.yaml"},
		},
		{
			name:          "with tokens",
			args:          []any{"--home", "${HOME}/.config"},
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "cluster",
			want:          []string{"--home", "/home/mcp/.config"},
		},
		{
			name:          "mixed types",
			args:          []any{"--port", 8080, "--verbose"},
			workspaceRoot: "/workspace",
			registryRoot:  "/registry",
			context:       "local",
			want:          []string{"--port", "8080", "--verbose"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveArgs(tt.args, tt.workspaceRoot, tt.registryRoot, tt.context)
			if len(got) != len(tt.want) {
				t.Errorf("ResolveArgs() len = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ResolveArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestValidateNoPlaintextSecrets(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int // number of leaks expected
	}{
		{
			name:    "no secrets",
			content: "key: value\nother: data",
			want:    0,
		},
		{
			name:    "github PAT",
			content: "token: ghp_1234567890abcdefghijklmnopqrstuvwxyz",
			want:    1,
		},
		{
			name:    "gitlab PAT",
			content: "GITLAB_TOKEN: glpat-xxxxxxxxxxxxxxxxxxxx",
			want:    1,
		},
		{
			name:    "openai-style key",
			content: "OPENAI_API_KEY: sk-12345678901234567890123456789012",
			want:    1,
		},
		{
			name:    "morph key",
			content: "MORPH_API_KEY: sk-E-1234567890123456789012345678901234567890",
			want:    1,
		},
		{
			name:    "tavily key",
			content: "TAVILY_API_KEY: tvly-123456789012345678901234567890",
			want:    1,
		},
		{
			name:    "grafana token",
			content: "GRAFANA_TOKEN: glsa_12345678901234567890123456789012",
			want:    1,
		},
		{
			name:    "zep key",
			content: "ZEP_API_KEY: z_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			want:    1,
		},
		{
			name:    "huggingface token",
			content: "HF_TOKEN: hf_123456789012345678901234567890",
			want:    1,
		},
		{
			name:    "multiple secrets",
			content: "gh: ghp_1234567890abcdefghijklmnopqrstuvwxyz\ngl: glpat-xxxxxxxxxxxxxxxxxxxx",
			want:    2,
		},
		{
			name:    "safe secret reference",
			content: "token: ${env:GITHUB_TOKEN}",
			want:    0,
		},
		{
			name:    "long snippet truncation",
			content: "very_long_key_name_that_exceeds_eighty_characters_and_should_be_truncated: ghp_1234567890abcdefghijklmnopqrstuvwxyz more text here",
			want:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leaks := ValidateNoPlaintextSecrets("test.yaml", tt.content)
			if len(leaks) != tt.want {
				t.Errorf("ValidateNoPlaintextSecrets() found %d leaks, want %d", len(leaks), tt.want)
				for _, leak := range leaks {
					t.Logf("  - %s (line %d): %s", leak.Type, leak.Line, leak.Snippet)
				}
			}
		})
	}
}

func TestSecretLeak(t *testing.T) {
	leak := SecretLeak{
		File:    "config.yaml",
		Line:    10,
		Type:    "GitHub Personal Access Token",
		Snippet: "token: [REDACTED]",
	}

	if leak.File != "config.yaml" {
		t.Error("File not set correctly")
	}
	if leak.Line != 10 {
		t.Error("Line not set correctly")
	}
}

func TestIsValidSecretReference(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"${env:API_KEY}", true},
		{"${keychain:github-token}", true},
		{"${secret:my-secret}", true},
		{"plain-value", false},
		{"${invalid:format}", false},
		{"${env:}", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got := IsValidSecretReference(tt.value)
			if got != tt.want {
				t.Errorf("IsValidSecretReference(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	t.Run("existing file", func(t *testing.T) {
		// Create temp file
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "test.txt")
		os.WriteFile(tmpFile, []byte("test"), 0644)

		if !fileExists(tmpFile) {
			t.Error("fileExists() = false for existing file")
		}
	})

	t.Run("non-existing file", func(t *testing.T) {
		if fileExists("/nonexistent/path/file.txt") {
			t.Error("fileExists() = true for non-existing file")
		}
	})

	t.Run("empty path", func(t *testing.T) {
		if fileExists("") {
			t.Error("fileExists() = true for empty path")
		}
	})
}

func TestResolvePathLike(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "scripts", "run.sh")
	os.MkdirAll(filepath.Dir(scriptPath), 0755)
	os.WriteFile(scriptPath, []byte("#!/bin/bash"), 0755)

	tests := []struct {
		name          string
		value         string
		workspaceRoot string
		registryRoot  string
		context       string
		wantContains  string
	}{
		{
			name:          "absolute path unchanged",
			value:         "/usr/bin/python",
			workspaceRoot: tmpDir,
			registryRoot:  tmpDir,
			context:       "local",
			wantContains:  "/usr/bin/python",
		},
		{
			name:          "simple command unchanged",
			value:         "python3",
			workspaceRoot: tmpDir,
			registryRoot:  tmpDir,
			context:       "local",
			wantContains:  "python3",
		},
		{
			name:          "cluster context",
			value:         "scripts/run.sh",
			workspaceRoot: tmpDir,
			registryRoot:  tmpDir,
			context:       "cluster",
			wantContains:  "scripts/run.sh",
		},
		{
			name:          "scripts prefix resolved",
			value:         "scripts/run.sh",
			workspaceRoot: tmpDir,
			registryRoot:  tmpDir,
			context:       "local",
			wantContains:  tmpDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePathLike(tt.value, tt.workspaceRoot, tt.registryRoot, tt.context)
			if tt.wantContains != "" && !contains(got, tt.wantContains) {
				t.Errorf("resolvePathLike() = %q, want to contain %q", got, tt.wantContains)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ValidationResult Tests ---

func TestValidationResult_AddError(t *testing.T) {
	result := &ValidationResult{Target: "test", Valid: true}
	result.AddError("CODE1", "field.name", "error message")

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
	if result.Errors[0].Code != "CODE1" {
		t.Errorf("expected code CODE1, got %s", result.Errors[0].Code)
	}
	if result.Errors[0].Severity != SeverityError {
		t.Errorf("expected severity Error, got %v", result.Errors[0].Severity)
	}
}

func TestValidationResult_AddWarning(t *testing.T) {
	result := &ValidationResult{Target: "test", Valid: true}
	result.AddWarning("WARN1", "field.name", "warning message")

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error entry, got %d", len(result.Errors))
	}
	if result.Errors[0].Severity != SeverityWarning {
		t.Errorf("expected severity Warning, got %v", result.Errors[0].Severity)
	}
}

func TestValidationResult_HasErrors(t *testing.T) {
	tests := []struct {
		name   string
		errors []ValidationError
		want   bool
	}{
		{
			name:   "no errors",
			errors: nil,
			want:   false,
		},
		{
			name: "only warnings",
			errors: []ValidationError{
				{Severity: SeverityWarning},
			},
			want: false,
		},
		{
			name: "has error",
			errors: []ValidationError{
				{Severity: SeverityError},
			},
			want: true,
		},
		{
			name: "mixed",
			errors: []ValidationError{
				{Severity: SeverityWarning},
				{Severity: SeverityError},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ValidationResult{Errors: tt.errors}
			if got := result.HasErrors(); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidationResult_HasWarnings(t *testing.T) {
	tests := []struct {
		name   string
		errors []ValidationError
		want   bool
	}{
		{
			name:   "no errors",
			errors: nil,
			want:   false,
		},
		{
			name: "only errors",
			errors: []ValidationError{
				{Severity: SeverityError},
			},
			want: false,
		},
		{
			name: "has warning",
			errors: []ValidationError{
				{Severity: SeverityWarning},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ValidationResult{Errors: tt.errors}
			if got := result.HasWarnings(); got != tt.want {
				t.Errorf("HasWarnings() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidationResult_ErrorCount(t *testing.T) {
	result := &ValidationResult{
		Errors: []ValidationError{
			{Severity: SeverityError},
			{Severity: SeverityWarning},
			{Severity: SeverityError},
		},
	}
	if got := result.ErrorCount(); got != 2 {
		t.Errorf("ErrorCount() = %d, want 2", got)
	}
}

func TestValidationResult_WarningCount(t *testing.T) {
	result := &ValidationResult{
		Errors: []ValidationError{
			{Severity: SeverityError},
			{Severity: SeverityWarning},
			{Severity: SeverityWarning},
		},
	}
	if got := result.WarningCount(); got != 2 {
		t.Errorf("WarningCount() = %d, want 2", got)
	}
}

// --- Schema Validation Tests ---

func TestValidateJSONSchema_ValidConfig(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"test": {
				"command": "/usr/bin/test",
				"args": ["--flag"],
				"env": {"KEY": "value"}
			}
		}
	}`)

	result := ValidateJSONSchema("claude", "test.json", content)
	if result.HasErrors() {
		t.Errorf("expected valid config, got errors: %v", result.Errors)
	}
}

func TestValidateJSONSchema_InvalidJSON(t *testing.T) {
	content := []byte(`{invalid json}`)

	result := ValidateJSONSchema("claude", "test.json", content)
	if !result.HasErrors() {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidateJSONSchema_MissingMcpServers(t *testing.T) {
	content := []byte(`{"servers": {}}`)

	result := ValidateJSONSchema("claude", "test.json", content)
	if !result.HasErrors() {
		t.Error("expected error for missing mcpServers")
	}
}

func TestValidateJSONSchema_MissingCommand(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"test": {
				"args": ["--flag"]
			}
		}
	}`)

	result := ValidateJSONSchema("claude", "test.json", content)
	if !result.HasErrors() {
		t.Error("expected error for missing command")
	}
}

func TestValidateJSONSchema_EmptyCommand(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"test": {
				"command": ""
			}
		}
	}`)

	result := ValidateJSONSchema("claude", "test.json", content)
	if !result.HasErrors() {
		t.Error("expected error for empty command")
	}
}

func TestValidateJSONSchema_InvalidArgsType(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"test": {
				"command": "/bin/test",
				"args": "not-an-array"
			}
		}
	}`)

	result := ValidateJSONSchema("claude", "test.json", content)
	if !result.HasErrors() {
		t.Error("expected error for invalid args type")
	}
}

func TestValidateTOMLStructure_ValidConfig(t *testing.T) {
	content := []byte(`
[mcp_servers.test]
command = "/usr/bin/test"
args = ["--flag"]
timeout = 30
`)

	result := ValidateTOMLStructure("codex", "config.toml", content)
	if result.HasErrors() {
		t.Errorf("expected valid config, got errors: %v", result.Errors)
	}
}

func TestValidateTOMLStructure_InvalidTOML(t *testing.T) {
	content := []byte(`[invalid toml`)

	result := ValidateTOMLStructure("codex", "config.toml", content)
	if !result.HasErrors() {
		t.Error("expected error for invalid TOML")
	}
}

func TestValidateTOMLStructure_MissingMcpServers(t *testing.T) {
	content := []byte(`[other_section]
value = "test"
`)

	result := ValidateTOMLStructure("codex", "config.toml", content)
	if !result.HasErrors() {
		t.Error("expected error for missing mcp_servers")
	}
}

func TestValidateTOMLStructure_MissingCommand(t *testing.T) {
	content := []byte(`
[mcp_servers.test]
args = ["--flag"]
`)

	result := ValidateTOMLStructure("codex", "config.toml", content)
	if !result.HasErrors() {
		t.Error("expected error for missing command")
	}
}

func TestValidateTOMLStructure_NegativeTimeout(t *testing.T) {
	content := []byte(`
[mcp_servers.test]
command = "/usr/bin/test"
timeout = -1
`)

	result := ValidateTOMLStructure("codex", "config.toml", content)
	if !result.HasErrors() {
		t.Error("expected error for negative timeout")
	}
}

func TestIsJSONTarget(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{"claude", true},
		{"claude_desktop", true},
		{"vscode", true},
		{"antigravity", true},
		{"codex", false},
		{"kilocode", false},
		{"gemini", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if got := IsJSONTarget(tt.target); got != tt.want {
				t.Errorf("IsJSONTarget(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestIsTOMLTarget(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{"codex", true},
		{"kilocode", true},
		{"gemini", true},
		{"claude", false},
		{"vscode", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if got := IsTOMLTarget(tt.target); got != tt.want {
				t.Errorf("IsTOMLTarget(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

// --- Runtime Validation Tests ---

func TestRuntimeValidator_ValidateCommand_RelativeCommand(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	rv.validateCommand("field", "node", result)

	// Relative/PATH commands should produce a warning
	if !result.HasWarnings() {
		t.Error("expected warning for relative command")
	}
}

func TestRuntimeValidator_ValidateCommand_NonexistentAbsolute(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	rv.validateCommand("field", "/nonexistent/path/binary", result)

	if !result.HasWarnings() {
		t.Error("expected warning for nonexistent command")
	}
}

func TestRuntimeValidator_ValidateCommand_WithEnvToken(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	rv.validateCommand("field", "${env:PATH}/binary", result)

	// Should skip validation for runtime-resolved tokens
	if result.HasErrors() || result.HasWarnings() {
		t.Error("expected no errors/warnings for env token")
	}
}

func TestRuntimeValidator_ValidateCommand_UnresolvedRepoToken(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	rv.validateCommand("field", "${repo}/bin/server", result)

	if !result.HasWarnings() {
		t.Error("expected warning for unresolved ${repo} token")
	}
}

func TestRuntimeValidator_ValidateEnv_InvalidName(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	env := map[string]interface{}{
		"invalid-name": "value",
	}
	rv.validateEnv("field", env, result)

	if !result.HasWarnings() {
		t.Error("expected warning for invalid env var name")
	}
}

func TestRuntimeValidator_ValidateEnv_ValidName(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	env := map[string]interface{}{
		"VALID_NAME":    "value",
		"_ALSO_VALID":   "value",
		"AnotherValid1": "value",
	}
	rv.validateEnv("field", env, result)

	if result.HasWarnings() {
		t.Errorf("unexpected warnings for valid env names: %v", result.Errors)
	}
}

func TestRuntimeValidator_CheckPlaintextSecrets_GitHubToken(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}
	// Avoid embedding a token-shaped string literal that looks like a real secret in the repo.
	githubPAT := "ghp_" + strings.Repeat("a", 36)
	rv.checkPlaintextSecrets("field", githubPAT, result)

	if !result.HasErrors() {
		t.Error("expected error for GitHub PAT")
	}
}

func TestRuntimeValidator_CheckPlaintextSecrets_GitLabToken(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}
	// Avoid embedding a token-shaped string literal that looks like a real secret in the repo.
	gitlabPAT := "glpat-" + strings.Repeat("a", 20)
	rv.checkPlaintextSecrets("field", gitlabPAT, result)

	if !result.HasErrors() {
		t.Error("expected error for GitLab PAT")
	}
}

func TestRuntimeValidator_CheckPlaintextSecrets_OpenAIKey(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	// OpenAI keys are sk- followed by 48 alphanumeric characters
	// Avoid embedding a token-shaped string literal that looks like a real secret in the repo.
	openAIKey := "sk-" + strings.Repeat("a", 48)
	rv.checkPlaintextSecrets("field", openAIKey, result)

	if !result.HasErrors() {
		t.Error("expected error for OpenAI API key")
	}
}

func TestRuntimeValidator_CheckPlaintextSecrets_SafeValue(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	rv.checkPlaintextSecrets("field", "just-a-normal-value", result)

	if result.HasErrors() {
		t.Error("unexpected error for safe value")
	}
}

func TestRuntimeValidator_ValidateArgs_UnresolvedToken(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home")
	result := &ValidationResult{Target: "test", Valid: true}

	args := []interface{}{"--config", "${repo}/config.yaml"}
	rv.validateArgs("field", args, result)

	if !result.HasWarnings() {
		t.Error("expected warning for unresolved ${repo} token in args")
	}
}

func TestRuntimeValidator_ResolvePath_Tilde(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home/user")
	resolved := rv.resolvePath("~/bin/server")

	expected := "/home/user/bin/server"
	if resolved != expected {
		t.Errorf("resolvePath(~/bin/server) = %q, want %q", resolved, expected)
	}
}

func TestRuntimeValidator_ResolvePath_Home(t *testing.T) {
	rv := NewRuntimeValidator("/repo", "/home/user")
	resolved := rv.resolvePath("$HOME/bin/server")

	expected := "/home/user/bin/server"
	if resolved != expected {
		t.Errorf("resolvePath($HOME/bin/server) = %q, want %q", resolved, expected)
	}
}

// --- Validator Orchestrator Tests ---

func TestValidator_New(t *testing.T) {
	v := New("/repo", "/home")
	if v.RepoRoot != "/repo" {
		t.Errorf("RepoRoot = %q, want /repo", v.RepoRoot)
	}
	if v.HomeDir != "/home" {
		t.Errorf("HomeDir = %q, want /home", v.HomeDir)
	}
}

func TestValidator_ValidateContent_JSON(t *testing.T) {
	v := New("/repo", "/home")

	content := []byte(`{
		"mcpServers": {
			"test": {
				"command": "node",
				"args": ["server.js"]
			}
		}
	}`)

	result := v.ValidateContent("claude", "test.json", content)
	// Should have warnings for relative command, but no errors
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestValidator_ValidateContent_TOML(t *testing.T) {
	v := New("/repo", "/home")

	content := []byte(`
[mcp_servers.test]
command = "node"
args = ["server.js"]
`)

	result := v.ValidateContent("codex", "config.toml", content)
	// Should have warnings for relative command, but no errors
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestValidator_ValidateContent_UnknownTarget(t *testing.T) {
	v := New("/repo", "/home")

	content := []byte(`{"key": "value"}`)
	result := v.ValidateContent("unknown", "test.yaml", content)

	if !result.HasWarnings() {
		t.Error("expected warning for unknown target format")
	}
}

func TestValidator_ValidateFile(t *testing.T) {
	// Create temp file with valid JSON
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "mcp.json")
	content := []byte(`{
		"mcpServers": {
			"test": {
				"command": "node",
				"args": ["server.js"]
			}
		}
	}`)
	if err := os.WriteFile(configFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	v := New("/repo", "/home")
	result, err := v.ValidateFile("claude", configFile)
	if err != nil {
		t.Fatalf("ValidateFile failed: %v", err)
	}
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestValidator_ValidateFile_NotFound(t *testing.T) {
	v := New("/repo", "/home")
	_, err := v.ValidateFile("claude", "/nonexistent/file.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestValidator_ValidateGenerated(t *testing.T) {
	tmpDir := t.TempDir()

	// Create claude config
	claudeDir := filepath.Join(tmpDir, "claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{
		"mcpServers": {
			"test": {
				"command": "node",
				"args": ["server.js"]
			}
		}
	}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "mcp.json"), content, 0644); err != nil {
		t.Fatal(err)
	}

	v := New("/repo", "/home")
	results, err := v.ValidateGenerated(tmpDir, []string{"claude"})
	if err != nil {
		t.Fatalf("ValidateGenerated failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestValidator_ValidateGenerated_SkipsMissing(t *testing.T) {
	tmpDir := t.TempDir()

	v := New("/repo", "/home")
	results, err := v.ValidateGenerated(tmpDir, []string{"claude", "codex"})
	if err != nil {
		t.Fatalf("ValidateGenerated failed: %v", err)
	}
	// Both should be skipped since directories don't exist
	if len(results) != 0 {
		t.Errorf("expected 0 results for missing configs, got %d", len(results))
	}
}

func TestValidator_ValidateDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested config structure
	claudeDir := filepath.Join(tmpDir, "claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := []byte(`{
		"mcpServers": {
			"test": {
				"command": "node"
			}
		}
	}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "mcp.json"), content, 0644); err != nil {
		t.Fatal(err)
	}

	v := New("/repo", "/home")
	results, err := v.ValidateDirectory(tmpDir)
	if err != nil {
		t.Fatalf("ValidateDirectory failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestValidator_getConfigPath(t *testing.T) {
	v := New("/repo", "/home")

	tests := []struct {
		target string
		want   string
	}{
		{"claude", "/output/claude/mcp.json"},
		{"vscode", "/output/vscode/mcp.json"},
		{"antigravity", "/output/antigravity/mcp.json"},
		{"claude_desktop", "/output/claude_desktop/claude_desktop_config.json"},
		{"codex", "/output/codex/config.toml"},
		{"kilocode", "/output/kilocode/config.toml"},
		{"gemini", "/output/gemini/config.toml"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := v.getConfigPath("/output", tt.target)
			if got != tt.want {
				t.Errorf("getConfigPath(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestInferTarget(t *testing.T) {
	tests := []struct {
		parentDir string
		filename  string
		want      string
	}{
		{".claude", "mcp.json", "claude"},
		{"claude", "mcp.json", "claude"},
		{".codex", "config.toml", "codex"},
		{".vscode", "mcp.json", "vscode"},
		{".vscode-mcp", "mcp.json", "vscode"},
		{"claude_desktop", "claude_desktop_config.json", "claude_desktop"},
		{"random", "config.toml", "codex"},
		{"random", "mcp.json", "claude"},
		{"random", "other.yaml", "unknown"},
	}

	for _, tt := range tests {
		name := tt.parentDir + "/" + tt.filename
		t.Run(name, func(t *testing.T) {
			got := inferTarget(tt.parentDir, tt.filename)
			if got != tt.want {
				t.Errorf("inferTarget(%q, %q) = %q, want %q", tt.parentDir, tt.filename, got, tt.want)
			}
		})
	}
}

// --- Helper Function Tests ---

func TestHasErrors(t *testing.T) {
	tests := []struct {
		name    string
		results []*ValidationResult
		want    bool
	}{
		{
			name:    "nil results",
			results: nil,
			want:    false,
		},
		{
			name: "no errors",
			results: []*ValidationResult{
				{Valid: true},
			},
			want: false,
		},
		{
			name: "has errors",
			results: []*ValidationResult{
				{Errors: []ValidationError{{Severity: SeverityError}}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasErrors(tt.results); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSummaryString(t *testing.T) {
	tests := []struct {
		name    string
		results []*ValidationResult
		want    string
	}{
		{
			name:    "all valid",
			results: []*ValidationResult{{Valid: true}},
			want:    "All configs valid",
		},
		{
			name: "warnings only",
			results: []*ValidationResult{
				{Errors: []ValidationError{{Severity: SeverityWarning}}},
			},
			want: "No errors, 1 warning",
		},
		{
			name: "errors and warnings",
			results: []*ValidationResult{
				{Errors: []ValidationError{
					{Severity: SeverityError},
					{Severity: SeverityWarning},
				}},
			},
			want: "1 error, 1 warning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SummaryString(tt.results); got != tt.want {
				t.Errorf("SummaryString() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Integration Test ---

func TestValidator_FullIntegration_JSONConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a realistic JSON config with issues
	content := []byte(`{
		"mcpServers": {
			"valid-server": {
				"command": "node",
				"args": ["server.js"],
				"env": {
					"PORT": "3000"
				}
			},
			"server-with-unresolved": {
				"command": "${repo}/bin/server",
				"args": ["--config", "${HOME}/config.yaml"]
			}
		}
	}`)

	configFile := filepath.Join(tmpDir, "mcp.json")
	if err := os.WriteFile(configFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	v := New("/repo", "/home")
	result, err := v.ValidateFile("claude", configFile)
	if err != nil {
		t.Fatalf("ValidateFile failed: %v", err)
	}

	// Should have warnings but no errors
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if !result.HasWarnings() {
		t.Error("expected warnings for unresolved tokens and relative command")
	}
}

func TestValidator_FullIntegration_TOMLConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a TOML config
	content := []byte(`
[mcp_servers.server1]
command = "python"
args = ["-m", "server"]
timeout = 60
description = "Test server"

[mcp_servers.server1.env]
PYTHONPATH = "/app"
`)

	configFile := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	v := New("/repo", "/home")
	result, err := v.ValidateFile("codex", configFile)
	if err != nil {
		t.Fatalf("ValidateFile failed: %v", err)
	}

	// Should have warnings for relative command only
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

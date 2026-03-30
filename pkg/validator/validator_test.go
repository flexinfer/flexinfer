package validator

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		repoRoot string
		homeDir  string
		wantRepo string
		wantHome string
	}{
		{
			name:     "both paths provided",
			repoRoot: "/tmp/repo",
			homeDir:  "/tmp/home",
			wantRepo: "/tmp/repo",
			wantHome: "/tmp/home",
		},
		{
			name:     "empty homeDir falls back",
			repoRoot: "/tmp/repo",
			homeDir:  "",
			wantRepo: "/tmp/repo",
			// homeDir will be resolved to os.UserHomeDir; just check non-empty.
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := New(tt.repoRoot, tt.homeDir)
			if v == nil {
				t.Fatal("New returned nil")
			}
			if v.RepoRoot != tt.wantRepo {
				t.Errorf("RepoRoot = %q, want %q", v.RepoRoot, tt.wantRepo)
			}
			if tt.homeDir != "" && v.HomeDir != tt.wantHome {
				t.Errorf("HomeDir = %q, want %q", v.HomeDir, tt.wantHome)
			}
			if tt.homeDir == "" && v.HomeDir == "" {
				t.Error("HomeDir should be non-empty when fallback is used")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// HasErrors
// ---------------------------------------------------------------------------

func TestHasErrors(t *testing.T) {
	t.Parallel()

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
			name:    "empty results",
			results: []*ValidationResult{},
			want:    false,
		},
		{
			name: "warnings only",
			results: []*ValidationResult{
				makeResult("test", SeverityWarning),
			},
			want: false,
		},
		{
			name: "errors only",
			results: []*ValidationResult{
				makeResult("test", SeverityError),
			},
			want: true,
		},
		{
			name: "mixed errors and warnings",
			results: []*ValidationResult{
				makeResult("test1", SeverityWarning),
				makeResult("test2", SeverityError),
			},
			want: true,
		},
		{
			name: "multiple clean results",
			results: []*ValidationResult{
				{Target: "a", File: "a.json", Valid: true},
				{Target: "b", File: "b.json", Valid: true},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := HasErrors(tt.results); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SummaryString
// ---------------------------------------------------------------------------

func TestSummaryString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []*ValidationResult
		want    string
	}{
		{
			name:    "no results",
			results: nil,
			want:    "All configs valid",
		},
		{
			name: "all clean",
			results: []*ValidationResult{
				{Target: "a", File: "a.json", Valid: true},
			},
			want: "All configs valid",
		},
		{
			name: "warnings only",
			results: []*ValidationResult{
				makeResult("a", SeverityWarning),
			},
			want: "No errors, 1 warning",
		},
		{
			name: "errors and warnings",
			results: []*ValidationResult{
				makeResult("a", SeverityError),
				makeResult("b", SeverityWarning),
			},
			// Should contain "error" and "warning".
			want: "", // Will check contains instead of exact match.
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SummaryString(tt.results)
			if tt.want != "" {
				if got != tt.want {
					t.Errorf("SummaryString() = %q, want %q", got, tt.want)
				}
			} else {
				// For the mixed case, verify it contains error and warning info.
				if !strings.Contains(got, "error") {
					t.Errorf("SummaryString() = %q, want it to contain 'error'", got)
				}
				if !strings.Contains(got, "warning") {
					t.Errorf("SummaryString() = %q, want it to contain 'warning'", got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UpstreamSchemas
// ---------------------------------------------------------------------------

func TestUpstreamSchemas(t *testing.T) {
	t.Parallel()

	schemas := UpstreamSchemas()
	if len(schemas) == 0 {
		t.Fatal("UpstreamSchemas() returned empty list")
	}

	// Verify we have at least the three expected platforms.
	platforms := map[string]bool{}
	for _, s := range schemas {
		platforms[s.Platform] = true
		if s.Name == "" {
			t.Errorf("schema for %q has empty Name", s.Platform)
		}
		if s.URL == "" {
			t.Errorf("schema for %q has empty URL", s.Platform)
		}
	}

	for _, want := range []string{"claude", "gemini", "codex"} {
		if !platforms[want] {
			t.Errorf("UpstreamSchemas() missing platform %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// GetEmbeddedSchema
// ---------------------------------------------------------------------------

func TestGetEmbeddedSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  string
		wantOK  bool
		wantLen int // Minimum expected length, 0 means skip check.
	}{
		{
			name:    "claude settings schema exists",
			schema:  "claude_settings.json",
			wantOK:  true,
			wantLen: 100,
		},
		{
			name:    "gemini settings schema exists",
			schema:  "gemini_settings.json",
			wantOK:  true,
			wantLen: 100,
		},
		{
			name:    "codex config schema exists",
			schema:  "codex_config.json",
			wantOK:  true,
			wantLen: 100,
		},
		{
			name:    "mcp JSON schema exists",
			schema:  "mcp_json.json",
			wantOK:  true,
			wantLen: 100,
		},
		{
			name:   "non-existent schema",
			schema: "nonexistent.json",
			wantOK: false,
		},
		{
			name:   "empty name",
			schema: "",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, ok := GetEmbeddedSchema(tt.schema)
			if ok != tt.wantOK {
				t.Errorf("GetEmbeddedSchema(%q) ok = %v, want %v", tt.schema, ok, tt.wantOK)
			}
			if tt.wantOK && len(data) < tt.wantLen {
				t.Errorf("GetEmbeddedSchema(%q) returned %d bytes, want >= %d", tt.schema, len(data), tt.wantLen)
			}
			if !tt.wantOK && data != nil {
				t.Errorf("GetEmbeddedSchema(%q) returned non-nil data for missing schema", tt.schema)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateClaudeSettings
// ---------------------------------------------------------------------------

func TestValidateClaudeSettings_Valid(t *testing.T) {
	t.Parallel()

	// A minimal valid Claude settings JSON.
	content := []byte(`{}`)

	result := ValidateClaudeSettings("test.json", content)
	if result == nil {
		t.Fatal("ValidateClaudeSettings returned nil")
	}

	// An empty object should be structurally valid JSON (no required fields
	// at root level in the claude settings schema).
	if result.HasErrors() {
		t.Errorf("ValidateClaudeSettings({}) has errors: %v", result.Errors)
	}
}

func TestValidateClaudeSettings_Invalid(t *testing.T) {
	t.Parallel()

	// Invalid JSON should produce errors.
	content := []byte(`{invalid json}`)

	result := ValidateClaudeSettings("test.json", content)
	if result == nil {
		t.Fatal("ValidateClaudeSettings returned nil")
	}
	if !result.HasErrors() {
		t.Error("ValidateClaudeSettings with invalid JSON should have errors")
	}
}

func TestValidateClaudeSettings_InvalidStructure(t *testing.T) {
	t.Parallel()

	// A JSON value that is valid JSON but violates schema expectations.
	// Use a top-level array which is not a valid settings object.
	content := []byte(`[1, 2, 3]`)

	result := ValidateClaudeSettings("test.json", content)
	if result == nil {
		t.Fatal("ValidateClaudeSettings returned nil")
	}

	// This should at least produce warnings from schema validation.
	if len(result.Errors) == 0 {
		t.Error("ValidateClaudeSettings with non-object JSON should produce errors or warnings")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeResult creates a ValidationResult with a single error or warning.
func makeResult(target string, severity ValidationSeverity) *ValidationResult {
	r := &ValidationResult{
		Target: target,
		File:   target + ".json",
		Valid:  severity != SeverityError,
	}
	if severity == SeverityError {
		r.AddError("TEST_CODE", "field", "test error")
	} else {
		r.AddWarning("TEST_CODE", "field", "test warning")
	}
	return r
}

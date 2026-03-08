package codebase

import (
	"testing"
)

func TestFirstNonEmptyEnv(t *testing.T) {
	t.Setenv("TEST_KEY_A", "")
	t.Setenv("TEST_KEY_B", "value_b")

	got := firstNonEmptyEnv([]string{"TEST_KEY_A", "TEST_KEY_B"}, "default")
	if got != "value_b" {
		t.Errorf("expected value_b, got %q", got)
	}
}

func TestFirstNonEmptyEnv_AllEmpty(t *testing.T) {
	got := firstNonEmptyEnv([]string{"TEST_NONEXISTENT_1", "TEST_NONEXISTENT_2"}, "fallback")
	if got != "fallback" {
		t.Errorf("expected fallback, got %q", got)
	}
}

func TestFirstNonEmptyEnv_FirstSet(t *testing.T) {
	t.Setenv("TEST_FIRST", "first_val")
	got := firstNonEmptyEnv([]string{"TEST_FIRST", "TEST_SECOND"}, "default")
	if got != "first_val" {
		t.Errorf("expected first_val, got %q", got)
	}
}

func TestIntEnv(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	if got := intEnv("TEST_INT", 10); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestIntEnv_Default(t *testing.T) {
	if got := intEnv("TEST_INT_MISSING", 10); got != 10 {
		t.Errorf("expected default 10, got %d", got)
	}
}

func TestIntEnv_Invalid(t *testing.T) {
	t.Setenv("TEST_INT_BAD", "not_a_number")
	if got := intEnv("TEST_INT_BAD", 5); got != 5 {
		t.Errorf("expected default 5 for invalid input, got %d", got)
	}
}

func TestInt64Env(t *testing.T) {
	t.Setenv("TEST_INT64", "4194304")
	if got := int64Env("TEST_INT64", 100); got != 4194304 {
		t.Errorf("expected 4194304, got %d", got)
	}
}

func TestInt64Env_Default(t *testing.T) {
	if got := int64Env("TEST_INT64_MISSING", 2097152); got != 2097152 {
		t.Errorf("expected default 2097152, got %d", got)
	}
}

func TestInt64Env_Invalid(t *testing.T) {
	t.Setenv("TEST_INT64_BAD", "xyz")
	if got := int64Env("TEST_INT64_BAD", 99); got != 99 {
		t.Errorf("expected default 99 for invalid input, got %d", got)
	}
}

func TestBoolEnv(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"y", true},
		{"on", true},
		{"t", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"no", false},
		{"n", false},
		{"off", false},
		{"f", false},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("TEST_BOOL", tc.value)
			got := boolEnv("TEST_BOOL", !tc.expected)
			if got != tc.expected {
				t.Errorf("boolEnv(%q) = %v, want %v", tc.value, got, tc.expected)
			}
		})
	}
}

func TestBoolEnv_Default(t *testing.T) {
	if got := boolEnv("TEST_BOOL_MISSING", true); got != true {
		t.Errorf("expected default true, got %v", got)
	}
	if got := boolEnv("TEST_BOOL_MISSING", false); got != false {
		t.Errorf("expected default false, got %v", got)
	}
}

func TestBoolEnv_UnrecognizedValue(t *testing.T) {
	t.Setenv("TEST_BOOL_WEIRD", "maybe")
	if got := boolEnv("TEST_BOOL_WEIRD", true); got != true {
		t.Errorf("expected default true for unrecognized value, got %v", got)
	}
}

func TestBoolEnv_Whitespace(t *testing.T) {
	t.Setenv("TEST_BOOL_WS", "  true  ")
	if got := boolEnv("TEST_BOOL_WS", false); got != true {
		t.Errorf("expected true with whitespace trimmed, got %v", got)
	}
}

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.EmbedBatchSize != 64 {
		t.Errorf("expected EmbedBatchSize 64, got %d", cfg.EmbedBatchSize)
	}
	if cfg.UpsertBatchSize != 64 {
		t.Errorf("expected UpsertBatchSize 64, got %d", cfg.UpsertBatchSize)
	}
	if cfg.IndexConcurrency != 4 {
		t.Errorf("expected IndexConcurrency 4, got %d", cfg.IndexConcurrency)
	}
	if cfg.ScrollLimit != 256 {
		t.Errorf("expected ScrollLimit 256, got %d", cfg.ScrollLimit)
	}
	if cfg.MaxFileBytes != 2*1024*1024 {
		t.Errorf("expected MaxFileBytes 2MiB, got %d", cfg.MaxFileBytes)
	}
	if cfg.EmbedProvider != "morph" {
		t.Errorf("expected EmbedProvider morph, got %q", cfg.EmbedProvider)
	}
	if cfg.QdrantDistance != "Cosine" {
		t.Errorf("expected QdrantDistance Cosine, got %q", cfg.QdrantDistance)
	}
}

func TestLoadConfigFromEnv_CustomValues(t *testing.T) {
	t.Setenv("CODEBASE_EMBED_BATCH_SIZE", "32")
	t.Setenv("CODEBASE_REPO_ID", "my-repo")
	t.Setenv("CODEBASE_GIT_METADATA", "true")
	t.Setenv("CODEBASE_QDRANT_URL", "http://qdrant:6333/")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.EmbedBatchSize != 32 {
		t.Errorf("expected EmbedBatchSize 32, got %d", cfg.EmbedBatchSize)
	}
	if cfg.RepoIDDefault != "my-repo" {
		t.Errorf("expected RepoIDDefault my-repo, got %q", cfg.RepoIDDefault)
	}
	if !cfg.GitMetadataDefault {
		t.Error("expected GitMetadataDefault true")
	}
	// Trailing slash should be trimmed
	if cfg.QdrantURL != "http://qdrant:6333" {
		t.Errorf("expected trailing slash trimmed, got %q", cfg.QdrantURL)
	}
}

func TestLoadConfigFromEnv_NegativeValuesClamped(t *testing.T) {
	t.Setenv("CODEBASE_EMBED_BATCH_SIZE", "-1")
	t.Setenv("CODEBASE_UPSERT_BATCH_SIZE", "0")
	t.Setenv("CODEBASE_INDEX_CONCURRENCY", "-5")
	t.Setenv("CODEBASE_SCROLL_LIMIT", "0")
	t.Setenv("CODEBASE_MAX_FILE_BYTES", "-100")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.EmbedBatchSize != 64 {
		t.Errorf("expected clamped EmbedBatchSize 64, got %d", cfg.EmbedBatchSize)
	}
	if cfg.UpsertBatchSize != 64 {
		t.Errorf("expected clamped UpsertBatchSize 64, got %d", cfg.UpsertBatchSize)
	}
	if cfg.IndexConcurrency != 4 {
		t.Errorf("expected clamped IndexConcurrency 4, got %d", cfg.IndexConcurrency)
	}
	if cfg.ScrollLimit != 256 {
		t.Errorf("expected clamped ScrollLimit 256, got %d", cfg.ScrollLimit)
	}
	if cfg.MaxFileBytes != 2*1024*1024 {
		t.Errorf("expected clamped MaxFileBytes 2MiB, got %d", cfg.MaxFileBytes)
	}
}

func TestNormalizeRenderFormat(t *testing.T) {
	tests := []struct {
		input    any
		expected string
		wantErr  bool
	}{
		{nil, "none", false},
		{"", "none", false},
		{"none", "none", false},
		{"mermaid", "mermaid", false},
		{"dot", "dot", false},
		{"MERMAID", "mermaid", false},
		{" Dot ", "dot", false},
		{"invalid", "", true},
		{"graphviz", "", true},
	}

	for _, tc := range tests {
		got, err := normalizeRenderFormat(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeRenderFormat(%v): expected error, got %q", tc.input, got)
			}
		} else {
			if err != nil {
				t.Errorf("normalizeRenderFormat(%v): unexpected error: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("normalizeRenderFormat(%v) = %q, want %q", tc.input, got, tc.expected)
			}
		}
	}
}

func TestNormalizeStringSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"nil", nil, nil},
		{"empty", []string{}, nil},
		{"single", []string{"Hello"}, []string{"hello"}},
		{"whitespace", []string{" Go ", " Rust "}, []string{"go", "rust"}},
		{"filters_empty", []string{"go", "", "  ", "rust"}, []string{"go", "rust"}},
		{"lowercases", []string{"GO", "Python", "RUST"}, []string{"go", "python", "rust"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeStringSlice(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.expected))
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("index %d = %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

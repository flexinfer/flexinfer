package codebase

import (
	"testing"
)

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
	if cfg.UpsertWait {
		t.Errorf("expected UpsertWait default false, got true")
	}
}

func TestLoadConfigFromEnv_UpsertWaitOverride(t *testing.T) {
	t.Setenv("CODEBASE_UPSERT_WAIT", "true")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.UpsertWait {
		t.Errorf("expected UpsertWait true when CODEBASE_UPSERT_WAIT=true")
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

func TestLoadConfigFromEnv_EnvChaining(t *testing.T) {
	// Test that fallback env vars work (QDRANT_URL falls back from CODEBASE_QDRANT_URL)
	t.Setenv("QDRANT_URL", "http://shared-qdrant:6333")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.QdrantURL != "http://shared-qdrant:6333" {
		t.Errorf("expected QDRANT_URL fallback, got %q", cfg.QdrantURL)
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

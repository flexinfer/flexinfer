package codebase

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	RepoIDDefault            string
	GitMetadataDefault       bool
	DisableEmbeddingsDefault bool

	QdrantURL        string
	QdrantAPIKey     string
	QdrantCollection string
	QdrantDistance   string

	// EmbedProvider selects the embedding backend: "morph" (default), "flexinfer", "ollama", or "dummy"
	EmbedProvider string
	EmbedAPIKey   string
	EmbedBaseURL  string
	EmbedModel    string

	EmbedBatchSize   int
	UpsertBatchSize  int
	IndexConcurrency int
	ScrollLimit      int

	MaxFileBytes int64

	// Chunker settings for splitting large code chunks
	ChunkMaxTokens     int
	ChunkOverlapTokens int
	ChunkMinTokens     int
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		RepoIDDefault:            os.Getenv("CODEBASE_REPO_ID"),
		GitMetadataDefault:       boolEnv("CODEBASE_GIT_METADATA", false),
		DisableEmbeddingsDefault: boolEnv("CODEBASE_DISABLE_EMBEDDINGS", false),

		QdrantURL: strings.TrimRight(firstNonEmptyEnv(
			[]string{"CODEBASE_QDRANT_URL", "QDRANT_URL"},
			"http://localhost:6333",
		), "/"),
		QdrantAPIKey:     firstNonEmptyEnv([]string{"CODEBASE_QDRANT_API_KEY", "QDRANT_API_KEY"}, ""),
		QdrantCollection: firstNonEmptyEnv([]string{"CODEBASE_QDRANT_COLLECTION"}, "codebase_memory_v1"),
		QdrantDistance:   firstNonEmptyEnv([]string{"CODEBASE_QDRANT_DISTANCE"}, "Cosine"),

		EmbedProvider: strings.ToLower(firstNonEmptyEnv([]string{"CODEBASE_EMBED_PROVIDER"}, "morph")),
		EmbedAPIKey:   firstNonEmptyEnv([]string{"CODEBASE_EMBED_API_KEY", "MORPH_API_KEY", "FLEXINFER_API_KEY", "OPENAI_API_KEY"}, ""),
		EmbedBaseURL: strings.TrimRight(firstNonEmptyEnv(
			[]string{"CODEBASE_EMBED_BASE_URL", "MORPH_BASE_URL", "FLEXINFER_URL", "OPENAI_BASE_URL", "OPENAI_API_BASE", "OLLAMA_BASE_URL"},
			"https://api.morphllm.com/v1",
		), "/"),
		EmbedModel: firstNonEmptyEnv(
			[]string{"CODEBASE_EMBED_MODEL", "MORPH_EMBED_MODEL", "FLEXINFER_EMBED_MODEL", "OPENAI_EMBED_MODEL", "OPENAI_EMBEDDING_MODEL", "OLLAMA_EMBED_MODEL"},
			"morph-embedding-v3",
		),

		EmbedBatchSize:   intEnv("CODEBASE_EMBED_BATCH_SIZE", 64),
		UpsertBatchSize:  intEnv("CODEBASE_UPSERT_BATCH_SIZE", 64),
		IndexConcurrency: intEnv("CODEBASE_INDEX_CONCURRENCY", 4),
		ScrollLimit:      intEnv("CODEBASE_SCROLL_LIMIT", 256),

		MaxFileBytes: int64Env("CODEBASE_MAX_FILE_BYTES", 2*1024*1024), // 2MiB per file by default

		ChunkMaxTokens:     intEnv("CODEBASE_CHUNK_MAX_TOKENS", 2000),
		ChunkOverlapTokens: intEnv("CODEBASE_CHUNK_OVERLAP_TOKENS", 200),
		ChunkMinTokens:     intEnv("CODEBASE_CHUNK_MIN_TOKENS", 50),
	}

	if cfg.EmbedBatchSize <= 0 {
		cfg.EmbedBatchSize = 64
	}
	if cfg.UpsertBatchSize <= 0 {
		cfg.UpsertBatchSize = 64
	}
	if cfg.IndexConcurrency <= 0 {
		cfg.IndexConcurrency = 4
	}
	if cfg.ScrollLimit <= 0 {
		cfg.ScrollLimit = 256
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = 2 * 1024 * 1024
	}

	return cfg, nil
}

func firstNonEmptyEnv(keys []string, def string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return def
}

func intEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func int64Env(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func boolEnv(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "t", "yes", "y", "on":
			return true
		case "0", "false", "f", "no", "n", "off":
			return false
		default:
			return def
		}
	}
	return def
}

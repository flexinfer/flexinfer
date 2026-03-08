package codebase

import (
	"os"
	"strings"

	"github.com/crb2nu/loom/pkg/env"
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
		GitMetadataDefault:       env.Bool("CODEBASE_GIT_METADATA", false),
		DisableEmbeddingsDefault: env.Bool("CODEBASE_DISABLE_EMBEDDINGS", false),

		QdrantURL: strings.TrimRight(env.StringChain(
			[]string{"CODEBASE_QDRANT_URL", "QDRANT_URL"},
			"http://localhost:6333",
		), "/"),
		QdrantAPIKey:     env.StringChain([]string{"CODEBASE_QDRANT_API_KEY", "QDRANT_API_KEY"}, ""),
		QdrantCollection: env.StringChain([]string{"CODEBASE_QDRANT_COLLECTION"}, "codebase_memory_v1"),
		QdrantDistance:   env.StringChain([]string{"CODEBASE_QDRANT_DISTANCE"}, "Cosine"),

		EmbedProvider: strings.ToLower(env.StringChain([]string{"CODEBASE_EMBED_PROVIDER"}, "morph")),
		EmbedAPIKey:   env.StringChain([]string{"CODEBASE_EMBED_API_KEY", "MORPH_API_KEY", "FLEXINFER_API_KEY", "OPENAI_API_KEY"}, ""),
		EmbedBaseURL: strings.TrimRight(env.StringChain(
			[]string{"CODEBASE_EMBED_BASE_URL", "MORPH_BASE_URL", "FLEXINFER_URL", "OPENAI_BASE_URL", "OPENAI_API_BASE", "OLLAMA_BASE_URL"},
			"https://api.morphllm.com/v1",
		), "/"),
		EmbedModel: env.StringChain(
			[]string{"CODEBASE_EMBED_MODEL", "MORPH_EMBED_MODEL", "FLEXINFER_EMBED_MODEL", "OPENAI_EMBED_MODEL", "OPENAI_EMBEDDING_MODEL", "OLLAMA_EMBED_MODEL"},
			"morph-embedding-v3",
		),

		EmbedBatchSize:   env.IntWithZero("CODEBASE_EMBED_BATCH_SIZE", 64),
		UpsertBatchSize:  env.IntWithZero("CODEBASE_UPSERT_BATCH_SIZE", 64),
		IndexConcurrency: env.IntWithZero("CODEBASE_INDEX_CONCURRENCY", 4),
		ScrollLimit:      env.IntWithZero("CODEBASE_SCROLL_LIMIT", 256),

		MaxFileBytes: env.Int64("CODEBASE_MAX_FILE_BYTES", 2*1024*1024), // 2MiB per file by default

		ChunkMaxTokens:     env.IntWithZero("CODEBASE_CHUNK_MAX_TOKENS", 2000),
		ChunkOverlapTokens: env.IntWithZero("CODEBASE_CHUNK_OVERLAP_TOKENS", 200),
		ChunkMinTokens:     env.IntWithZero("CODEBASE_CHUNK_MIN_TOKENS", 50),
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

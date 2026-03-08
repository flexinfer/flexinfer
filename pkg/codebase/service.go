package codebase

import (
	"context"
	"sync"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/codebase/index"
	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
)

// Embedding provider defaults. These are used as fallbacks when the
// configured provider differs from the base config's default values.
const (
	defaultFlexInferURL   = "http://localhost:8080"
	defaultFlexInferModel = "BAAI/bge-large-en-v1.5"
	defaultOllamaURL      = "http://localhost:11434"
	defaultOllamaModel    = "nomic-embed-text"
	defaultMorphBaseURL   = "https://api.morphllm.com/v1"
	defaultMorphModel     = "morph-embedding-v3"
)

type Service struct {
	cfg Config

	qdrant *qdrant.Client
	embed  embed.Embedder

	indexers *index.Registry

	jobsMu sync.RWMutex
	jobs   map[string]*indexJob

	watchMu   sync.RWMutex
	watchJobs map[string]*watchJob
}

type indexJob struct {
	id string

	cancel context.CancelFunc

	status string
	err    string

	stats schema.IndexStats
}

func NewServiceFromEnv() (*Service, error) {
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		return nil, err
	}

	hc := httpclient.NewDefault()

	// Select embedder based on configuration
	var embedder embed.Embedder
	if cfg.DisableEmbeddingsDefault {
		// Use dummy embedder when embeddings are disabled
		embedder = embed.NewDummyEmbedder(1)
	} else {
		switch cfg.EmbedProvider {
		case "flexinfer":
			// FlexInfer TEI backend (OpenAI-compatible)
			baseURL := cfg.EmbedBaseURL
			if baseURL == "" || baseURL == defaultMorphBaseURL {
				baseURL = env.String("FLEXINFER_URL", defaultFlexInferURL) + "/v1"
			}
			model := cfg.EmbedModel
			if model == "" || model == defaultMorphModel {
				model = defaultFlexInferModel
			}
			embedder = embed.NewFlexInferClient(hc, baseURL, cfg.EmbedAPIKey, model)
		case "ollama":
			// Ollama local embeddings
			baseURL := cfg.EmbedBaseURL
			if baseURL == "" || baseURL == defaultMorphBaseURL {
				baseURL = env.String("OLLAMA_BASE_URL", defaultOllamaURL)
			}
			model := cfg.EmbedModel
			if model == "" || model == defaultMorphModel {
				model = defaultOllamaModel
			}
			embedder = embed.NewOllamaClient(hc, baseURL, model)
		case "dummy", "none":
			// Explicit dummy mode
			embedder = embed.NewDummyEmbedder(1)
		default:
			// Default to Morph/OpenAI-compatible API
			embedder = embed.NewMorphClient(hc, cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel)
		}
	}

	svc := &Service{
		cfg:       cfg,
		qdrant:    qdrant.NewClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.QdrantCollection, cfg.QdrantDistance),
		embed:     embedder,
		jobs:      make(map[string]*indexJob),
		watchJobs: make(map[string]*watchJob),
		indexers: index.NewRegistry(
			cfg.MaxFileBytes,
		),
	}

	return svc, nil
}

// NewServiceWithEmbedder creates a service with a custom embedder.
func NewServiceWithEmbedder(cfg Config, embedder embed.Embedder) (*Service, error) {
	hc := httpclient.NewDefault()

	svc := &Service{
		cfg:       cfg,
		qdrant:    qdrant.NewClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.QdrantCollection, cfg.QdrantDistance),
		embed:     embedder,
		jobs:      make(map[string]*indexJob),
		watchJobs: make(map[string]*watchJob),
		indexers: index.NewRegistry(
			cfg.MaxFileBytes,
		),
	}

	return svc, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

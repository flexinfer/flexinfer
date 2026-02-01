package agentcontext

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// Qdrant configuration
	QdrantURL          string
	QdrantAPIKey       string
	ContextCollection  string
	SessionsCollection string
	QdrantDistance     string

	// Embeddings configuration (reuses codebase-memory patterns)
	EmbedAPIKey  string
	EmbedBaseURL string
	EmbedModel   string

	// Batching
	EmbedBatchSize  int
	UpsertBatchSize int
	ScrollLimit     int

	// Defaults
	DefaultAgentID     string
	DefaultNamespace   string
	DefaultVisibility  Visibility
	DefaultTokenBudget int

	// Auto-summarization
	AutoSummarize            bool
	SummarizeEntryThreshold  int
	SummarizeTokenThreshold  int
	SummarizeMinuteThreshold int

	// Handoff configuration
	HandoffMaxTokens       int
	HandoffExpirationHours int

	// Recall enhancements
	DefaultRecencyWeight float64
	TaskPriorityBoost    float64

	// Task/Annotation collections
	TasksCollection       string
	AnnotationsCollection string
	HandoffsCollection    string
	TemplatesCollection   string

	// Persistence collections (Phase 1)
	GraphEntitiesCollection  string
	GraphRelationsCollection string
	WorkflowsCollection      string
	WorkflowDefsCollection   string
	MemoryCollection         string

	// Parallel embedding
	EmbedConcurrency int

	// Semantic deduplication
	DedupeSimilarity float64

	// Trusted sources (Phase 2.5)
	TrustedSources []TrustedSource
}

// TrustedSource defines a trusted source pattern for context weighting
type TrustedSource struct {
	Pattern     string  `json:"pattern"`     // Glob pattern (e.g., "*.md", "src/**/*.go")
	Priority    float64 `json:"priority"`    // 0.0-1.0, higher = more trusted
	Description string  `json:"description"` // Human-readable description
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		QdrantURL: strings.TrimRight(firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_QDRANT_URL", "QDRANT_URL"},
			"http://localhost:6333",
		), "/"),
		QdrantAPIKey:       firstNonEmptyEnv([]string{"AGENT_CONTEXT_QDRANT_API_KEY", "QDRANT_API_KEY"}, ""),
		ContextCollection:  firstNonEmptyEnv([]string{"AGENT_CONTEXT_COLLECTION"}, "agent_context_v1"),
		SessionsCollection: firstNonEmptyEnv([]string{"AGENT_CONTEXT_SESSIONS_COLLECTION"}, "agent_sessions_v1"),
		QdrantDistance:     firstNonEmptyEnv([]string{"AGENT_CONTEXT_QDRANT_DISTANCE"}, "Cosine"),

		EmbedAPIKey: firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_EMBED_API_KEY", "CODEBASE_EMBED_API_KEY", "MORPH_API_KEY", "OPENAI_API_KEY"},
			"",
		),
		EmbedBaseURL: strings.TrimRight(firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_EMBED_BASE_URL", "CODEBASE_EMBED_BASE_URL", "MORPH_BASE_URL", "OPENAI_BASE_URL"},
			"https://api.morphllm.com/v1",
		), "/"),
		EmbedModel: firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_EMBED_MODEL", "CODEBASE_EMBED_MODEL", "MORPH_EMBED_MODEL"},
			"morph-embedding-v3",
		),

		EmbedBatchSize:  intEnv("AGENT_CONTEXT_EMBED_BATCH_SIZE", 64),
		UpsertBatchSize: intEnv("AGENT_CONTEXT_UPSERT_BATCH_SIZE", 64),
		ScrollLimit:     intEnv("AGENT_CONTEXT_SCROLL_LIMIT", 256),

		DefaultAgentID:     os.Getenv("AGENT_CONTEXT_DEFAULT_AGENT_ID"),
		DefaultNamespace:   os.Getenv("AGENT_CONTEXT_DEFAULT_NAMESPACE"),
		DefaultVisibility:  Visibility(firstNonEmptyEnv([]string{"AGENT_CONTEXT_DEFAULT_VISIBILITY"}, "private")),
		DefaultTokenBudget: intEnv("AGENT_CONTEXT_DEFAULT_TOKEN_BUDGET", 4000),

		AutoSummarize:            boolEnv("AGENT_CONTEXT_AUTO_SUMMARIZE", true),
		SummarizeEntryThreshold:  intEnv("AGENT_CONTEXT_SUMMARIZE_ENTRY_THRESHOLD", 50),
		SummarizeTokenThreshold:  intEnv("AGENT_CONTEXT_SUMMARIZE_TOKEN_THRESHOLD", 20000),
		SummarizeMinuteThreshold: intEnv("AGENT_CONTEXT_SUMMARIZE_MINUTE_THRESHOLD", 30),

		HandoffMaxTokens:       intEnv("AGENT_CONTEXT_HANDOFF_MAX_TOKENS", 8000),
		HandoffExpirationHours: intEnv("AGENT_CONTEXT_HANDOFF_EXPIRATION_HOURS", 24),

		DefaultRecencyWeight: floatEnv("AGENT_CONTEXT_DEFAULT_RECENCY_WEIGHT", 0.2),
		TaskPriorityBoost:    floatEnv("AGENT_CONTEXT_TASK_PRIORITY_BOOST", 0.3),

		TasksCollection:       firstNonEmptyEnv([]string{"AGENT_CONTEXT_TASKS_COLLECTION"}, "agent_tasks_v1"),
		AnnotationsCollection: firstNonEmptyEnv([]string{"AGENT_CONTEXT_ANNOTATIONS_COLLECTION"}, "agent_annotations_v1"),
		HandoffsCollection:    firstNonEmptyEnv([]string{"AGENT_CONTEXT_HANDOFFS_COLLECTION"}, "agent_handoffs_v1"),
		TemplatesCollection:   firstNonEmptyEnv([]string{"AGENT_CONTEXT_TEMPLATES_COLLECTION"}, "agent_templates_v1"),

		// Persistence collections
		GraphEntitiesCollection:  firstNonEmptyEnv([]string{"AGENT_CONTEXT_GRAPH_ENTITIES_COLLECTION"}, "agent_graph_entities_v1"),
		GraphRelationsCollection: firstNonEmptyEnv([]string{"AGENT_CONTEXT_GRAPH_RELATIONS_COLLECTION"}, "agent_graph_relations_v1"),
		WorkflowsCollection:      firstNonEmptyEnv([]string{"AGENT_CONTEXT_WORKFLOWS_COLLECTION"}, "agent_workflows_v1"),
		WorkflowDefsCollection:   firstNonEmptyEnv([]string{"AGENT_CONTEXT_WORKFLOW_DEFS_COLLECTION"}, "agent_workflow_defs_v1"),
		MemoryCollection:         firstNonEmptyEnv([]string{"AGENT_CONTEXT_MEMORY_COLLECTION"}, "agent_memory_v1"),

		EmbedConcurrency: intEnv("AGENT_CONTEXT_EMBED_CONCURRENCY", 4),
		DedupeSimilarity: floatEnv("AGENT_CONTEXT_DEDUPE_SIMILARITY", 0.9),
	}

	// Validate visibility
	switch cfg.DefaultVisibility {
	case VisibilityPrivate, VisibilityShared, VisibilityPublic:
		// valid
	default:
		cfg.DefaultVisibility = VisibilityPrivate
	}

	// Apply defaults
	if cfg.EmbedBatchSize <= 0 {
		cfg.EmbedBatchSize = 64
	}
	if cfg.UpsertBatchSize <= 0 {
		cfg.UpsertBatchSize = 64
	}
	if cfg.ScrollLimit <= 0 {
		cfg.ScrollLimit = 256
	}
	if cfg.DefaultTokenBudget <= 0 {
		cfg.DefaultTokenBudget = 4000
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

func floatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

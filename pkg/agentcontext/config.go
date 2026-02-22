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
	// EmbedProvider selects the embedding backend: "morph" (default), "flexinfer", "ollama", or "dummy"
	EmbedProvider string
	EmbedAPIKey   string
	EmbedBaseURL  string
	EmbedModel    string

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

	// Presence registry
	PresenceCollection      string
	PresenceHeartbeatTTL    int // seconds, default 120
	PresenceCleanupInterval int // seconds, default 60

	// File claims
	FileClaimsCollection string

	// Git worktree integration
	WorktreeCollection      string
	GitRepoPath             string
	GitWorktreeBaseDir      string
	GitAutoCleanupWorktrees bool

	// Compaction scheduler
	CompactionEnabled       bool
	CompactionCheckInterval int // seconds, default 300

	// Task reconciler
	TaskReconcilerEnabled            bool
	TaskReconcilerInterval           int // seconds, default 300
	TaskReconcilerCompletedRetention int // hours, default 168 (7 days)
	TaskReconcilerStaleTimeout       int // hours, default 4

	// Session reaper
	SessionReaperEnabled  bool // default: true
	SessionReaperInterval int  // seconds, default: 1800 (30 minutes)
	SessionReaperMaxAge   int  // hours, default: 168 (7 days)

	// Worktree reconciler
	WorktreeReconcilerEnabled       bool
	WorktreeReconcilerInterval      int // seconds, default 300
	WorktreeOrphanGracePeriod       int // minutes, default 30
	WorktreeMaxTTLHours             int // hours, default 0 (unlimited)
	WorktreeArtifactCleanupEnabled  bool
	WorktreeArtifactCleanupPatterns string // comma-separated, default ".next,node_modules,target,dist,.cache,__pycache__,.tox"
	WorktreeDiskScanEnabled         bool
	WorktreeDetectUntracked         bool
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

		EmbedProvider: strings.ToLower(firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_EMBED_PROVIDER", "CODEBASE_EMBED_PROVIDER"},
			"morph",
		)),
		EmbedAPIKey: firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_EMBED_API_KEY", "CODEBASE_EMBED_API_KEY", "MORPH_API_KEY", "FLEXINFER_API_KEY", "OPENAI_API_KEY"},
			"",
		),
		EmbedBaseURL: strings.TrimRight(firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_EMBED_BASE_URL", "CODEBASE_EMBED_BASE_URL", "MORPH_BASE_URL", "FLEXINFER_URL", "OPENAI_BASE_URL"},
			"https://api.morphllm.com/v1",
		), "/"),
		EmbedModel: firstNonEmptyEnv(
			[]string{"AGENT_CONTEXT_EMBED_MODEL", "CODEBASE_EMBED_MODEL", "MORPH_EMBED_MODEL", "FLEXINFER_EMBED_MODEL"},
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

		// Presence registry
		PresenceCollection:      firstNonEmptyEnv([]string{"AGENT_CONTEXT_PRESENCE_COLLECTION"}, "agent_presence_v1"),
		PresenceHeartbeatTTL:    intEnv("AGENT_CONTEXT_PRESENCE_HEARTBEAT_TTL", 120),
		PresenceCleanupInterval: intEnv("AGENT_CONTEXT_PRESENCE_CLEANUP_INTERVAL", 60),

		// File claims
		FileClaimsCollection: firstNonEmptyEnv([]string{"AGENT_CONTEXT_FILE_CLAIMS_COLLECTION"}, "agent_file_claims_v1"),

		// Git worktree
		WorktreeCollection:      firstNonEmptyEnv([]string{"AGENT_CONTEXT_WORKTREE_COLLECTION"}, "agent_worktrees_v1"),
		GitRepoPath:             firstNonEmptyEnv([]string{"AGENT_CONTEXT_GIT_REPO_PATH", "REPO_PATH"}, ""),
		GitWorktreeBaseDir:      firstNonEmptyEnv([]string{"AGENT_CONTEXT_GIT_WORKTREE_BASE_DIR"}, ""),
		GitAutoCleanupWorktrees: boolEnv("AGENT_CONTEXT_GIT_AUTO_CLEANUP_WORKTREES", true),

		// Compaction scheduler
		CompactionEnabled:       boolEnv("AGENT_CONTEXT_COMPACTION_ENABLED", true),
		CompactionCheckInterval: intEnv("AGENT_CONTEXT_COMPACTION_CHECK_INTERVAL", 300),

		// Task reconciler
		TaskReconcilerEnabled:            boolEnv("AGENT_CONTEXT_TASK_RECONCILER_ENABLED", true),
		TaskReconcilerInterval:           intEnv("AGENT_CONTEXT_TASK_RECONCILER_INTERVAL", 300),
		TaskReconcilerCompletedRetention: intEnv("AGENT_CONTEXT_TASK_COMPLETED_RETENTION_HOURS", 168),
		TaskReconcilerStaleTimeout:       intEnv("AGENT_CONTEXT_TASK_STALE_TIMEOUT_HOURS", 4),

		// Session reaper
		SessionReaperEnabled:  boolEnv("AGENT_CONTEXT_SESSION_REAPER_ENABLED", true),
		SessionReaperInterval: intEnv("AGENT_CONTEXT_SESSION_REAPER_INTERVAL", 1800),
		SessionReaperMaxAge:   intEnv("AGENT_CONTEXT_SESSION_REAPER_MAX_AGE_HOURS", 168),

		// Worktree reconciler
		WorktreeReconcilerEnabled:       boolEnv("AGENT_CONTEXT_WORKTREE_RECONCILER_ENABLED", true),
		WorktreeReconcilerInterval:      intEnv("AGENT_CONTEXT_WORKTREE_RECONCILER_INTERVAL", 300),
		WorktreeOrphanGracePeriod:       intEnv("AGENT_CONTEXT_WORKTREE_ORPHAN_GRACE_PERIOD", 30),
		WorktreeMaxTTLHours:             intEnv("AGENT_CONTEXT_WORKTREE_MAX_TTL_HOURS", 0),
		WorktreeArtifactCleanupEnabled:  boolEnv("AGENT_CONTEXT_WORKTREE_ARTIFACT_CLEANUP_ENABLED", true),
		WorktreeArtifactCleanupPatterns: firstNonEmptyEnv([]string{"AGENT_CONTEXT_WORKTREE_ARTIFACT_CLEANUP_PATTERNS"}, ".next,node_modules,target,dist,.cache,__pycache__,.tox"),
		WorktreeDiskScanEnabled:         boolEnv("AGENT_CONTEXT_WORKTREE_DISK_SCAN_ENABLED", true),
		WorktreeDetectUntracked:         boolEnv("AGENT_CONTEXT_WORKTREE_DETECT_UNTRACKED", true),
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

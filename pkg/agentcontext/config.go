package agentcontext

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/env"
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

	// Platform-aware token budgets (M1: seamless integration)
	// JSON map: {"claude-code": 8000, "codex": 4000, "gemini": 6000}
	PlatformBudgets map[string]int

	// Session reaper
	SessionReaperEnabled      bool // default: true
	SessionReaperInterval     int  // seconds, default: 1800 (30 minutes)
	SessionReaperMaxAge       int  // hours, default: 168 (7 days)
	SessionReaperActiveMaxAge int  // hours, default: 24 — reap "active" sessions older than this

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
		QdrantURL: strings.TrimRight(env.StringChain(
			[]string{"AGENT_CONTEXT_QDRANT_URL", "QDRANT_URL"},
			"http://localhost:6333",
		), "/"),
		QdrantAPIKey:       env.StringChain([]string{"AGENT_CONTEXT_QDRANT_API_KEY", "QDRANT_API_KEY"}, ""),
		ContextCollection:  env.StringChain([]string{"AGENT_CONTEXT_COLLECTION"}, "agent_context_v1"),
		SessionsCollection: env.StringChain([]string{"AGENT_CONTEXT_SESSIONS_COLLECTION"}, "agent_sessions_v1"),
		QdrantDistance:     env.StringChain([]string{"AGENT_CONTEXT_QDRANT_DISTANCE"}, "Cosine"),

		EmbedProvider: strings.ToLower(env.StringChain(
			[]string{"AGENT_CONTEXT_EMBED_PROVIDER", "CODEBASE_EMBED_PROVIDER"},
			"morph",
		)),
		EmbedAPIKey: env.StringChain(
			[]string{"AGENT_CONTEXT_EMBED_API_KEY", "CODEBASE_EMBED_API_KEY", "MORPH_API_KEY", "FLEXINFER_API_KEY", "OPENAI_API_KEY"},
			"",
		),
		EmbedBaseURL: strings.TrimRight(env.StringChain(
			[]string{"AGENT_CONTEXT_EMBED_BASE_URL", "CODEBASE_EMBED_BASE_URL", "MORPH_BASE_URL", "FLEXINFER_URL", "OPENAI_BASE_URL"},
			"https://api.morphllm.com/v1",
		), "/"),
		EmbedModel: env.StringChain(
			[]string{"AGENT_CONTEXT_EMBED_MODEL", "CODEBASE_EMBED_MODEL", "MORPH_EMBED_MODEL", "FLEXINFER_EMBED_MODEL"},
			"morph-embedding-v3",
		),

		EmbedBatchSize:  env.IntWithZero("AGENT_CONTEXT_EMBED_BATCH_SIZE", 64),
		UpsertBatchSize: env.IntWithZero("AGENT_CONTEXT_UPSERT_BATCH_SIZE", 64),
		ScrollLimit:     env.IntWithZero("AGENT_CONTEXT_SCROLL_LIMIT", 256),

		DefaultAgentID:     os.Getenv("AGENT_CONTEXT_DEFAULT_AGENT_ID"),
		DefaultNamespace:   os.Getenv("AGENT_CONTEXT_DEFAULT_NAMESPACE"),
		DefaultVisibility:  Visibility(env.StringChain([]string{"AGENT_CONTEXT_DEFAULT_VISIBILITY"}, "private")),
		DefaultTokenBudget: env.IntWithZero("AGENT_CONTEXT_DEFAULT_TOKEN_BUDGET", 4000),

		AutoSummarize:            env.Bool("AGENT_CONTEXT_AUTO_SUMMARIZE", true),
		SummarizeEntryThreshold:  env.IntWithZero("AGENT_CONTEXT_SUMMARIZE_ENTRY_THRESHOLD", 50),
		SummarizeTokenThreshold:  env.IntWithZero("AGENT_CONTEXT_SUMMARIZE_TOKEN_THRESHOLD", 20000),
		SummarizeMinuteThreshold: env.IntWithZero("AGENT_CONTEXT_SUMMARIZE_MINUTE_THRESHOLD", 30),

		HandoffMaxTokens:       env.IntWithZero("AGENT_CONTEXT_HANDOFF_MAX_TOKENS", 8000),
		HandoffExpirationHours: env.IntWithZero("AGENT_CONTEXT_HANDOFF_EXPIRATION_HOURS", 24),

		DefaultRecencyWeight: env.Float("AGENT_CONTEXT_DEFAULT_RECENCY_WEIGHT", 0.2),
		TaskPriorityBoost:    env.Float("AGENT_CONTEXT_TASK_PRIORITY_BOOST", 0.3),

		TasksCollection:       env.StringChain([]string{"AGENT_CONTEXT_TASKS_COLLECTION"}, "agent_tasks_v1"),
		AnnotationsCollection: env.StringChain([]string{"AGENT_CONTEXT_ANNOTATIONS_COLLECTION"}, "agent_annotations_v1"),
		HandoffsCollection:    env.StringChain([]string{"AGENT_CONTEXT_HANDOFFS_COLLECTION"}, "agent_handoffs_v1"),

		// Persistence collections
		GraphEntitiesCollection:  env.StringChain([]string{"AGENT_CONTEXT_GRAPH_ENTITIES_COLLECTION"}, "agent_graph_entities_v1"),
		GraphRelationsCollection: env.StringChain([]string{"AGENT_CONTEXT_GRAPH_RELATIONS_COLLECTION"}, "agent_graph_relations_v1"),
		WorkflowsCollection:      env.StringChain([]string{"AGENT_CONTEXT_WORKFLOWS_COLLECTION"}, "agent_workflows_v1"),
		WorkflowDefsCollection:   env.StringChain([]string{"AGENT_CONTEXT_WORKFLOW_DEFS_COLLECTION"}, "agent_workflow_defs_v1"),
		MemoryCollection:         env.StringChain([]string{"AGENT_CONTEXT_MEMORY_COLLECTION"}, "agent_memory_v1"),

		EmbedConcurrency: env.IntWithZero("AGENT_CONTEXT_EMBED_CONCURRENCY", 4),
		DedupeSimilarity: env.Float("AGENT_CONTEXT_DEDUPE_SIMILARITY", 0.9),

		// Presence registry
		PresenceCollection:      env.StringChain([]string{"AGENT_CONTEXT_PRESENCE_COLLECTION"}, "agent_presence_v1"),
		PresenceHeartbeatTTL:    env.IntWithZero("AGENT_CONTEXT_PRESENCE_HEARTBEAT_TTL", 120),
		PresenceCleanupInterval: env.IntWithZero("AGENT_CONTEXT_PRESENCE_CLEANUP_INTERVAL", 60),

		// File claims
		FileClaimsCollection: env.StringChain([]string{"AGENT_CONTEXT_FILE_CLAIMS_COLLECTION"}, "agent_file_claims_v1"),

		// Git worktree
		WorktreeCollection:      env.StringChain([]string{"AGENT_CONTEXT_WORKTREE_COLLECTION"}, "agent_worktrees_v1"),
		GitRepoPath:             env.StringChain([]string{"AGENT_CONTEXT_GIT_REPO_PATH", "REPO_PATH"}, ""),
		GitWorktreeBaseDir:      env.StringChain([]string{"AGENT_CONTEXT_GIT_WORKTREE_BASE_DIR"}, ""),
		GitAutoCleanupWorktrees: env.Bool("AGENT_CONTEXT_GIT_AUTO_CLEANUP_WORKTREES", true),

		// Compaction scheduler
		CompactionEnabled:       env.Bool("AGENT_CONTEXT_COMPACTION_ENABLED", true),
		CompactionCheckInterval: env.IntWithZero("AGENT_CONTEXT_COMPACTION_CHECK_INTERVAL", 300),

		// Task reconciler
		TaskReconcilerEnabled:            env.Bool("AGENT_CONTEXT_TASK_RECONCILER_ENABLED", true),
		TaskReconcilerInterval:           env.IntWithZero("AGENT_CONTEXT_TASK_RECONCILER_INTERVAL", 300),
		TaskReconcilerCompletedRetention: env.IntWithZero("AGENT_CONTEXT_TASK_COMPLETED_RETENTION_HOURS", 168),
		TaskReconcilerStaleTimeout:       env.IntWithZero("AGENT_CONTEXT_TASK_STALE_TIMEOUT_HOURS", 4),

		// Session reaper
		SessionReaperEnabled:      env.Bool("AGENT_CONTEXT_SESSION_REAPER_ENABLED", true),
		SessionReaperInterval:     env.IntWithZero("AGENT_CONTEXT_SESSION_REAPER_INTERVAL", 1800),
		SessionReaperMaxAge:       env.IntWithZero("AGENT_CONTEXT_SESSION_REAPER_MAX_AGE_HOURS", 168),
		SessionReaperActiveMaxAge: env.IntWithZero("AGENT_CONTEXT_SESSION_REAPER_ACTIVE_MAX_AGE_HOURS", 24),

		// Worktree reconciler
		WorktreeReconcilerEnabled:       env.Bool("AGENT_CONTEXT_WORKTREE_RECONCILER_ENABLED", true),
		WorktreeReconcilerInterval:      env.IntWithZero("AGENT_CONTEXT_WORKTREE_RECONCILER_INTERVAL", 300),
		WorktreeOrphanGracePeriod:       env.IntWithZero("AGENT_CONTEXT_WORKTREE_ORPHAN_GRACE_PERIOD", 30),
		WorktreeMaxTTLHours:             env.IntWithZero("AGENT_CONTEXT_WORKTREE_MAX_TTL_HOURS", 0),
		WorktreeArtifactCleanupEnabled:  env.Bool("AGENT_CONTEXT_WORKTREE_ARTIFACT_CLEANUP_ENABLED", true),
		WorktreeArtifactCleanupPatterns: env.StringChain([]string{"AGENT_CONTEXT_WORKTREE_ARTIFACT_CLEANUP_PATTERNS"}, ".next,node_modules,target,dist,.cache,__pycache__,.tox"),
		WorktreeDiskScanEnabled:         env.Bool("AGENT_CONTEXT_WORKTREE_DISK_SCAN_ENABLED", true),
		WorktreeDetectUntracked:         env.Bool("AGENT_CONTEXT_WORKTREE_DETECT_UNTRACKED", true),
	}

	// Parse platform budgets from env JSON.
	if raw := os.Getenv("AGENT_CONTEXT_PLATFORM_BUDGETS"); raw != "" {
		budgets := make(map[string]int)
		if err := json.Unmarshal([]byte(raw), &budgets); err == nil {
			cfg.PlatformBudgets = budgets
		}
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

// TokenBudgetForPlatform returns the token budget for the given agent type
// (e.g. "claude-code", "codex", "gemini"). Falls back to DefaultTokenBudget
// when the platform is unrecognized or no overrides are configured.
func (c Config) TokenBudgetForPlatform(agentType string) int {
	if agentType != "" && len(c.PlatformBudgets) > 0 {
		if budget, ok := c.PlatformBudgets[agentType]; ok && budget > 0 {
			return budget
		}
	}
	return c.DefaultTokenBudget
}

// =========================================================================
// Reranker config (Slice A1 / F1 — recall reranker wiring)
// Parsed separately via LoadRerankerConfigFromEnv so unrelated callers do
// not have to carry the new struct.
// =========================================================================

// RerankerConfig configures the recall Reranker backend.
type RerankerConfig struct {
	// Kind selects the backend: "off" (default), "flexinfer", or "bge".
	// Parsed from WEAVER_RERANKER.
	Kind RerankerKind

	// Model overrides the backend model name. For "flexinfer"/"bge" this
	// is forwarded as the "model" field on the /v1/rerank request.
	Model string

	// BaseURL is the flexinfer proxy base (no trailing slash). Reused for
	// both the flexinfer and bge backends because bge runs behind the
	// same proxy.
	BaseURL string

	// APIKey is the optional bearer token for the flexinfer proxy.
	APIKey string

	// Timeout caps the rerank HTTP call. Zero uses a conservative default
	// (see backend constructors).
	Timeout time.Duration
}

// LoadRerankerConfigFromEnv parses reranker configuration from the
// environment. Defaults to RerankerKindOff so enabling reranking is an
// explicit opt-in.
//
// Env vars:
//   - WEAVER_RERANKER          : "off" | "flexinfer" | "bge" (default "off")
//   - WEAVER_RERANKER_MODEL    : override model name
//   - WEAVER_RERANKER_TIMEOUT  : Go duration, e.g. "5s"
//   - WEAVER_RERANKER_BASE_URL : flexinfer proxy base URL. Falls back to
//     FLEXINFER_URL, then http://localhost:8080.
//   - WEAVER_RERANKER_API_KEY  : bearer token. Falls back to FLEXINFER_API_KEY.
func LoadRerankerConfigFromEnv() RerankerConfig {
	kind := strings.ToLower(strings.TrimSpace(env.StringChain([]string{"WEAVER_RERANKER"}, "off")))
	return RerankerConfig{
		Kind:  RerankerKind(kind),
		Model: strings.TrimSpace(env.StringChain([]string{"WEAVER_RERANKER_MODEL"}, "")),
		BaseURL: strings.TrimRight(env.StringChain(
			[]string{"WEAVER_RERANKER_BASE_URL", "FLEXINFER_URL"},
			"http://localhost:8080",
		), "/"),
		APIKey:  env.StringChain([]string{"WEAVER_RERANKER_API_KEY", "FLEXINFER_API_KEY"}, ""),
		Timeout: env.Duration("WEAVER_RERANKER_TIMEOUT", 5*time.Second),
	}
}

// =========================================================================
// Auto-handoff config (Slice C1 / F5) — opt-in trigger thresholds parsed
// independently of the main Config so unrelated callers need not carry
// the new struct.
//
// Env vars:
//   - AGENTCONTEXT_HANDOFF_INPUT_TOKEN_HIGH : int, default 160000
//   - AGENTCONTEXT_HANDOFF_COST_USD_HIGH    : float, default 1.50
//   - AGENTCONTEXT_HANDOFF_STALLED          : Go duration, default "8m"
//   - AGENTCONTEXT_HANDOFF_DEBOUNCE         : Go duration, default "10m"
//   - AGENTCONTEXT_HANDOFF_ENABLED          : bool, default false
// =========================================================================

// LoadAutoHandoffConfigFromEnv parses auto-handoff trigger config from
// the environment. Missing or malformed values fall back to the spec
// defaults (§5.F5 of the 2026-04-17 orchestration spec). The returned
// config has Enabled=false unless explicitly set.
func LoadAutoHandoffConfigFromEnv() AutoHandoffConfig {
	cfg := DefaultAutoHandoffConfig()
	if v := env.IntWithZero("AGENTCONTEXT_HANDOFF_INPUT_TOKEN_HIGH", 0); v > 0 {
		cfg.InputTokenHigh = v
	}
	if v := env.Float("AGENTCONTEXT_HANDOFF_COST_USD_HIGH", 0); v > 0 {
		cfg.CostUSDHigh = v
	}
	if v := env.Duration("AGENTCONTEXT_HANDOFF_STALLED", 0); v > 0 {
		cfg.StalledDuration = v
	}
	if v := env.Duration("AGENTCONTEXT_HANDOFF_DEBOUNCE", 0); v > 0 {
		cfg.Debounce = v
	}
	cfg.Enabled = env.Bool("AGENTCONTEXT_HANDOFF_ENABLED", false)
	return cfg
}

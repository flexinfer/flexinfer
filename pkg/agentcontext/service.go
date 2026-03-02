package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/validate"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const sessionsVectorSize = 4

// ServiceOption configures a Service.
type ServiceOption func(*Service)

// WithLogger sets the structured logger for the service.
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) { s.logger = logger }
}

// WithTracer sets the OpenTelemetry TracerProvider for the service.
func WithTracer(tp trace.TracerProvider) ServiceOption {
	return func(s *Service) { s.tracer = tp.Tracer("agentcontext") }
}

type Service struct {
	cfg     Config
	logger  *slog.Logger
	tracer  trace.Tracer
	metrics *Metrics

	qdrant *QdrantRegistry
	embed  embed.Embedder

	vectorSize int

	// Workflow orchestration
	workflowEngine *WorkflowEngine

	// Knowledge graph (with persistence)
	knowledgeGraph          *KnowledgeGraph
	persistedKnowledgeGraph *persistedGraph

	// Memory hierarchy (with persistence)
	memoryHierarchy          *MemoryHierarchy
	persistedMemoryHierarchy *persistedMemoryHierarchy

	// Workflow engine (with persistence)
	persistedWorkflowEngine *persistedWorkflowEngine

	// Domain sub-services
	presence  *PresenceSvc
	claims    *ClaimSvc
	worktrees *WorktreeSvc
	tasks     *TaskSvc
	sess      *SessionSvc

	// Nudge queue — pending nudges per agent, delivered via heartbeat response.
	nudgeMu sync.Mutex
	nudges  map[string][]*Nudge // agentID -> pending nudges

	// Background services
	compactionScheduler *CompactionScheduler
	memoryExporter      *MemoryExporter
	memoryImporter      *MemoryImporter
	bgCancel            context.CancelFunc
}

// Tracer returns the service's OTel tracer. Returns a noop tracer if none was configured.
func (s *Service) Tracer() trace.Tracer {
	return s.tracer
}

// OnPresenceEvent registers a callback invoked when an agent's presence
// state transitions (e.g., active → idle, idle → offline).
func (s *Service) OnPresenceEvent(fn func(eventType string, agentID string, oldStatus, newStatus PresenceStatus)) {
	s.presence.SetOnEvent(fn)
}

// AddNudge enqueues a nudge for the given agent, delivered on next heartbeat.
func (s *Service) AddNudge(agentID string, nudge *Nudge) {
	s.nudgeMu.Lock()
	defer s.nudgeMu.Unlock()
	if s.nudges == nil {
		s.nudges = make(map[string][]*Nudge)
	}
	s.nudges[agentID] = append(s.nudges[agentID], nudge)
}

// DrainNudges returns and clears all pending nudges for the given agent.
func (s *Service) DrainNudges(agentID string) []*Nudge {
	s.nudgeMu.Lock()
	defer s.nudgeMu.Unlock()
	nudges := s.nudges[agentID]
	delete(s.nudges, agentID)
	return nudges
}

// PendingNudgeCount returns the number of pending nudges for the given agent.
func (s *Service) PendingNudgeCount(agentID string) int {
	s.nudgeMu.Lock()
	defer s.nudgeMu.Unlock()
	return len(s.nudges[agentID])
}

func NewServiceFromEnv(opts ...ServiceOption) (*Service, error) {
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		return nil, err
	}

	hc := httpclient.NewDefault()

	// Select embedder based on provider configuration
	var embedder embed.Embedder
	switch cfg.EmbedProvider {
	case "flexinfer":
		baseURL := cfg.EmbedBaseURL
		if baseURL == "" || baseURL == "https://api.morphllm.com/v1" {
			baseURL = firstNonEmptyEnv([]string{"FLEXINFER_URL"}, "http://localhost:8080") + "/v1"
		}
		model := cfg.EmbedModel
		if model == "" || model == "morph-embedding-v3" {
			model = "BAAI/bge-large-en-v1.5"
		}
		embedder = embed.NewFlexInferClient(hc, baseURL, cfg.EmbedAPIKey, model)
	case "ollama":
		baseURL := cfg.EmbedBaseURL
		if baseURL == "" || baseURL == "https://api.morphllm.com/v1" {
			baseURL = "http://localhost:11434"
		}
		model := cfg.EmbedModel
		if model == "" || model == "morph-embedding-v3" {
			model = "nomic-embed-text"
		}
		embedder = embed.NewOllamaClient(hc, baseURL, model)
	case "dummy", "none":
		embedder = embed.NewDummyEmbedder(1)
	default:
		embedder = embed.NewMorphClient(hc, cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel)
	}

	qdrantReg := NewQdrantRegistry(hc, cfg)

	svc := &Service{
		cfg:    cfg,
		qdrant: qdrantReg,
		embed:  embedder,

		nudges: make(map[string][]*Nudge),
	}

	// Best-effort: if the context collection already exists, remember its vector size
	// so we can avoid "unknown vector size" edge-cases on operations like share/summarize.
	if exists, size, err := svc.qdrant.Get(CollContext).GetCollectionVectorSize(context.Background()); err == nil && exists && size > 0 {
		svc.vectorSize = size
	}

	// Initialize workflow engine
	svc.workflowEngine = NewWorkflowEngine(nil) // Tool executor set by daemon

	// Initialize knowledge graph with persistence
	svc.knowledgeGraph = NewKnowledgeGraph()
	svc.persistedKnowledgeGraph = svc.knowledgeGraph.SetPersistence(&GraphPersistenceConfig{
		EntitiesQdrant:  svc.qdrant.Get(CollGraphEntities),
		RelationsQdrant: svc.qdrant.Get(CollGraphRelations),
		EmbedModel:      cfg.EmbedModel,
		VectorSize:      svc.vectorSize,
	})

	// Initialize memory hierarchy with persistence
	svc.memoryHierarchy = NewMemoryHierarchy()
	svc.persistedMemoryHierarchy = svc.memoryHierarchy.SetPersistence(&MemoryPersistenceConfig{
		MemoryQdrant: svc.qdrant.Get(CollMemory),
		EmbedModel:   cfg.EmbedModel,
		VectorSize:   svc.vectorSize,
	})

	// Initialize workflow engine with persistence
	svc.persistedWorkflowEngine = svc.workflowEngine.SetPersistence(&WorkflowPersistenceConfig{
		WorkflowsQdrant:    svc.qdrant.Get(CollWorkflows),
		WorkflowDefsQdrant: svc.qdrant.Get(CollWorkflowDefs),
	})

	// Apply functional options and set defaults.
	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.Default()
	}
	if svc.tracer == nil {
		svc.tracer = noop.NewTracerProvider().Tracer("agentcontext")
	}
	svc.metrics = GetMetrics()

	// Initialize domain sub-services
	svc.presence = NewPresenceSvc(qdrantReg.Get(CollPresence), cfg, svc.logger, svc.metrics)
	svc.claims = NewClaimSvc(qdrantReg.Get(CollFileClaims), svc.logger, svc.metrics)
	svc.worktrees = NewWorktreeSvc(qdrantReg.Get(CollWorktree), cfg, svc.logger, svc.metrics)
	svc.tasks = NewTaskSvc(qdrantReg.Get(CollTasks), svc.embed, cfg, svc.logger, &svc.vectorSize)

	// Wire cross-domain callbacks for presence cleanup
	svc.presence.releaseClaimsForAgent = func(agentID string) {
		svc.claims.ReleaseAllForAgent(agentID)
	}
	svc.presence.orphanWorktrees = svc.orphanWorktreesForAgent
	svc.presence.endSessionsForAgent = svc.endActiveSessionsForAgent
	svc.presence.detectConflicts = func(agentID string, files []string) []map[string]any {
		conflicts := svc.presence.DetectActiveFileConflicts(agentID, files)
		conflicts = append(conflicts, svc.claims.DetectConflicts(agentID, files)...)
		return conflicts
	}

	// Wire worktree ↔ presence callbacks
	svc.worktrees.setPresenceWorktreeID = svc.presence.SetWorktreeID
	svc.worktrees.clearPresenceWorktreeID = svc.presence.ClearWorktreeID

	// Initialize session sub-service
	svc.sess = NewSessionSvc(qdrantReg.Get(CollSessions), cfg, svc.logger, svc.metrics)

	// Wire session cleanup callbacks
	svc.sess.releaseClaimsForAgent = func(agentID string) int {
		return svc.claims.ReleaseAllForAgent(agentID)
	}
	svc.sess.removePresence = svc.presence.Remove
	svc.sess.deletePresenceFromQdrant = func(ctx context.Context, agentID string) error {
		if svc.qdrant.Get(CollPresence) == nil {
			return nil
		}
		return svc.qdrant.Get(CollPresence).DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID)))
	}
	svc.sess.orphanWorktrees = svc.orphanWorktreesForAgent
	svc.sess.markTasksStale = svc.markSessionTasksStale
	svc.sess.enrichResult = svc.enrichSessionStartResult
	svc.sess.generateSummary = svc.generateSummary
	svc.sess.runSummaryAsync = svc.runSessionSummaryAsync
	svc.sess.liveAgentIDs = svc.presence.LiveAgentIDs

	// Wire task callbacks
	svc.tasks.getSession = svc.getSession
	svc.tasks.upsertBatched = svc.upsertPointsBatched

	// Initialize compaction scheduler
	compactionConfig := DefaultCompactionConfig()
	compactionConfig.Enabled = cfg.CompactionEnabled
	if cfg.CompactionCheckInterval > 0 {
		compactionConfig.CheckInterval = time.Duration(cfg.CompactionCheckInterval) * time.Second
	}
	svc.compactionScheduler = NewCompactionScheduler(compactionConfig, svc.memoryHierarchy, nil, svc.logger)
	svc.compactionScheduler.SetPersistence(svc.persistedMemoryHierarchy)

	// Initialize task reconciler
	reconcilerConfig := DefaultTaskReconcilerConfig()
	reconcilerConfig.Enabled = cfg.TaskReconcilerEnabled
	if cfg.TaskReconcilerInterval > 0 {
		reconcilerConfig.CheckInterval = time.Duration(cfg.TaskReconcilerInterval) * time.Second
	}
	if cfg.TaskReconcilerCompletedRetention > 0 {
		reconcilerConfig.CompletedRetention = time.Duration(cfg.TaskReconcilerCompletedRetention) * time.Hour
	}
	if cfg.TaskReconcilerStaleTimeout > 0 {
		reconcilerConfig.StaleTimeout = time.Duration(cfg.TaskReconcilerStaleTimeout) * time.Hour
	}
	svc.tasks.reconciler = NewTaskReconciler(reconcilerConfig, svc.tasks, svc.logger)
	svc.tasks.reconciler.getSession = svc.getSession

	// Initialize worktree reconciler (stored on WorktreeSvc)
	wtReconcilerConfig := DefaultWorktreeReconcilerConfig()
	wtReconcilerConfig.Enabled = cfg.WorktreeReconcilerEnabled
	if cfg.WorktreeReconcilerInterval > 0 {
		wtReconcilerConfig.CheckInterval = time.Duration(cfg.WorktreeReconcilerInterval) * time.Second
	}
	if cfg.WorktreeOrphanGracePeriod > 0 {
		wtReconcilerConfig.OrphanGracePeriod = time.Duration(cfg.WorktreeOrphanGracePeriod) * time.Minute
	}
	wtReconcilerConfig.MaxTTLHours = cfg.WorktreeMaxTTLHours
	wtReconcilerConfig.ArtifactCleanupEnabled = cfg.WorktreeArtifactCleanupEnabled
	if cfg.WorktreeArtifactCleanupPatterns != "" {
		wtReconcilerConfig.ArtifactPatterns = parseArtifactPatterns(cfg.WorktreeArtifactCleanupPatterns)
	}
	wtReconcilerConfig.DiskScanEnabled = cfg.WorktreeDiskScanEnabled
	wtReconcilerConfig.DetectUntracked = cfg.WorktreeDetectUntracked
	svc.worktrees.reconciler = NewWorktreeReconciler(wtReconcilerConfig, svc.worktrees, svc.logger)

	// Initialize memory exporter/importer
	svc.memoryExporter = NewMemoryExporter(svc.memoryHierarchy, svc.knowledgeGraph, svc.workflowEngine)
	svc.memoryImporter = NewMemoryImporter(svc.memoryHierarchy, svc.knowledgeGraph, svc.workflowEngine)

	// Load persisted state on startup (best-effort)
	ctx := context.Background()
	if err := svc.loadPersistedState(ctx); err != nil {
		svc.logger.Warn("failed to load persisted state", "error", err)
	}

	return svc, nil
}

// loadPersistedState loads all persisted data from Qdrant on startup
func (s *Service) loadPersistedState(ctx context.Context) error {
	// Load sessions
	if err := s.sess.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load sessions", "error", err)
	}

	// Load knowledge graph
	if err := s.persistedKnowledgeGraph.LoadGraphFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load knowledge graph", "error", err)
	}
	if err := s.persistedKnowledgeGraph.LoadReasoningChainsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load reasoning chains", "error", err)
	}

	// Load memory hierarchy
	if err := s.persistedMemoryHierarchy.LoadMemoryFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load memory hierarchy", "error", err)
	}

	// Load workflows and definitions
	if err := s.persistedWorkflowEngine.LoadWorkflowsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load workflows", "error", err)
	}
	if err := s.persistedWorkflowEngine.LoadDefinitionsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load workflow definitions", "error", err)
	}

	// Load presence registry
	if err := s.presence.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load presence", "error", err)
	}

	// Load file claims
	if err := s.claims.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load file claims", "error", err)
	}

	// Load worktree assignments
	if err := s.worktrees.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load worktree assignments", "error", err)
	}

	return nil
}

// StartBackgroundServices starts background goroutines (compaction, presence cleanup)
func (s *Service) StartBackgroundServices(ctx context.Context) {
	bgCtx, cancel := context.WithCancel(ctx)
	s.bgCancel = cancel

	s.logger.Info("starting background services",
		"compaction_enabled", s.cfg.CompactionEnabled,
		"task_reconciler_enabled", s.cfg.TaskReconcilerEnabled,
		"worktree_reconciler_enabled", s.cfg.WorktreeReconcilerEnabled,
		"session_reaper_enabled", s.cfg.SessionReaperEnabled,
		"presence_cleanup_interval_s", s.cfg.PresenceCleanupInterval,
	)

	// Start compaction scheduler
	if s.compactionScheduler != nil && s.cfg.CompactionEnabled {
		if err := s.compactionScheduler.Start(bgCtx); err != nil {
			s.logger.Warn("failed to start compaction scheduler", "error", err)
		}
	}

	// Start task reconciler
	if s.cfg.TaskReconcilerEnabled {
		s.tasks.StartReconciler(bgCtx)
	}

	// Start worktree reconciler
	if s.cfg.WorktreeReconcilerEnabled {
		s.worktrees.StartReconciler(bgCtx)
	}

	// Start presence cleanup goroutine
	go s.presence.RunCleanup(bgCtx)

	// Start session reaper
	if s.cfg.SessionReaperEnabled {
		s.logger.Info("starting session reaper",
			"interval_s", s.cfg.SessionReaperInterval,
			"max_age_hours", s.cfg.SessionReaperMaxAge,
		)
		go s.sess.RunReaper(bgCtx)
	}
}

// StopBackgroundServices stops all background goroutines
func (s *Service) StopBackgroundServices() {
	s.logger.Info("stopping background services")
	if s.compactionScheduler != nil {
		s.compactionScheduler.Stop()
	}
	s.tasks.StopReconciler()
	s.worktrees.StopReconciler()
	if s.bgCancel != nil {
		s.bgCancel()
	}
}

// Session Handlers — thin delegation to SessionSvc.

func (s *Service) HandleSessionStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.Start(ctx, args)
}

func (s *Service) HandleSessionEnd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.End(ctx, args)
}

func (s *Service) HandleSessionList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.List(ctx, args)
}

func (s *Service) HandleSessionDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.Delete(ctx, args)
}

func (s *Service) HandleSessionPrune(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.Prune(ctx, args)
}

// enrichSessionStartResult adds coordination info (pending handoffs, active agents).
// Stays on Service because it accesses CollHandoffs (not owned by SessionSvc).
func (s *Service) enrichSessionStartResult(ctx context.Context, result map[string]any, agentID, namespace string) {
	result["active_agents"] = len(s.presence.LiveAgentIDs())

	now := time.Now()
	var pendingHandoffs []map[string]any
	if s.qdrant.Get(CollHandoffs) != nil {
		conds := []any{
			Match("target_agent_id", agentID),
			Match("status", string(HandoffStatusPending)),
		}
		points, err := s.qdrant.Get(CollHandoffs).ScrollPoints(ctx, FilterMust(conds...), 50, false)
		if err == nil {
			for _, p := range points {
				h, err := payloadToHandoff(p.Payload)
				if err != nil || h == nil {
					continue
				}
				if h.ExpiresAt != nil && now.After(*h.ExpiresAt) {
					continue
				}
				pendingHandoffs = append(pendingHandoffs, map[string]any{
					"handoff_id":   h.ID,
					"source_agent": h.SourceAgentID,
					"instructions": h.Instructions,
					"summary":      h.Summary,
					"created_at":   h.CreatedAt.Format(time.RFC3339),
				})
			}
		}
	}
	result["pending_handoffs"] = pendingHandoffs
}

// runSessionSummaryAsync performs end-of-session summarization in background.
// Stays on Service because generateSummary accesses CollContext and embedder.
func (s *Service) runSessionSummaryAsync(session *Session) {
	bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := s.generateSummary(bg, session); err != nil {
		s.logger.Warn("async session summarize failed",
			"session_id", session.ID,
			"agent_id", session.AgentID,
			"error", err,
		)
		return
	}

	session.Status = string(SessionStatusSummarized)
	if err := s.sess.Persist(bg, session); err != nil {
		s.logger.Warn("async session summarize persist failed",
			"session_id", session.ID,
			"agent_id", session.AgentID,
			"error", err,
		)
	}
}

// endActiveSessionsForAgent delegates to SessionSvc.
func (s *Service) endActiveSessionsForAgent(ctx context.Context, agentID string) {
	s.sess.EndActiveForAgent(ctx, agentID)
}

// getSession delegates to SessionSvc.Get.
func (s *Service) getSession(ctx context.Context, sessionID string) (*Session, error) {
	return s.sess.Get(ctx, sessionID)
}

// persistSession delegates to SessionSvc.Persist.
func (s *Service) persistSession(ctx context.Context, session *Session) error {
	return s.sess.Persist(ctx, session)
}

// Context Storage Handlers

func (s *Service) HandleContextAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	entriesRaw := v.RequiredAny("entries")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	entriesArr, ok := entriesRaw.([]any)
	if !ok || len(entriesArr) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entries array is required")), nil
	}

	if strings.TrimSpace(s.cfg.EmbedAPIKey) == "" {
		return mcp.ErrorResult(fmt.Errorf("AGENT_CONTEXT_EMBED_API_KEY (or MORPH_API_KEY / OPENAI_API_KEY) is not set")), nil
	}

	var entries []ContextEntry
	var embedTexts []string

	for _, raw := range entriesArr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		entryType := EntryType(toString(m["entry_type"]))
		title := toString(m["title"])
		content := toString(m["content"])

		if title == "" || content == "" {
			continue
		}

		visibility := Visibility(toString(m["visibility"]))
		if visibility == "" {
			visibility = s.cfg.DefaultVisibility
		}

		ts := time.Now()
		entry := ContextEntry{
			ID:            GenerateID(session.AgentID, sessionID, title+"\n"+content, ts),
			SchemaVersion: SchemaVersion,
			AgentID:       session.AgentID,
			SessionID:     sessionID,
			Namespace:     session.Namespace,
			EntryType:     entryType,
			Timestamp:     ts,
			Title:         title,
			Content:       content,
			ContentHash:   ContentHashFunc(content),
			FilePath:      toString(m["file_path"]),
			LineStart:     toInt(m["line_start"]),
			LineEnd:       toInt(m["line_end"]),
			Tags:          toStringSlice(m["tags"]),
			TokenCount:    EstimateTokens(title + " " + content),
			Visibility:    visibility,
			SharedWith:    toStringSlice(m["shared_with"]),
		}

		if meta, ok := m["metadata"].(map[string]any); ok {
			entry.Metadata = meta
		}

		entries = append(entries, entry)
		embedTexts = append(embedTexts, entry.Title+"\n"+entry.Content)
	}

	if len(entries) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no valid entries provided")), nil
	}

	vectors, err := s.embed.EmbedDocuments(ctx, embedTexts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding entries: %w", err)), nil
	}
	if len(vectors) != len(entries) {
		return mcp.ErrorResult(fmt.Errorf("embedding count mismatch: got %d want %d", len(vectors), len(entries))), nil
	}

	for _, v := range vectors {
		if len(v) > 0 {
			s.vectorSize = len(v)
			break
		}
	}
	if s.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size (empty embeddings)")), nil
	}

	// Ensure collection exists
	if s.vectorSize > 0 {
		if err := s.qdrant.Get(CollContext).EnsureCollection(ctx, s.vectorSize); err != nil {
			return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
		}
	}

	// Upsert to Qdrant
	points := make([]Point, 0, len(entries))
	for i, entry := range entries {
		vector := vectors[i]
		if len(vector) > 0 && len(vector) != s.vectorSize {
			return mcp.ErrorResult(fmt.Errorf("embedding vector size mismatch: got %d want %d", len(vector), s.vectorSize)), nil
		}
		if len(vector) == 0 {
			vector = make([]float64, s.vectorSize)
		}
		points = append(points, Point{
			ID:      entry.ID,
			Vector:  vector,
			Payload: EntryToPayload(entry, s.cfg.EmbedModel),
		})
	}

	if err := s.upsertPointsBatched(ctx, s.qdrant.Get(CollContext), points); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert entries: %w", err)), nil
	}

	// Update session stats
	s.sess.mu.Lock()
	session.EntryCount += len(entries)
	for _, e := range entries {
		session.TotalTokens += e.TokenCount
	}
	s.sess.mu.Unlock()
	// Best-effort persist - don't fail the add operation
	if err := s.persistSession(ctx, session); err != nil {
		// Log to stderr since we can't add to the result at this point
		s.logger.Warn("persist session stats failed", "error", err)
	}

	// Check for auto-summarization
	if s.cfg.AutoSummarize {
		s.maybeAutoSummarize(ctx, session)
	}

	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(entries),
		"entry_ids": ids,
	})
}

func (s *Service) HandleContextGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ids := v.RequiredStringSlice("entry_ids")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	points, err := s.qdrant.Get(CollContext).GetPoints(ctx, ids, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get entries: %w", err)), nil
	}

	var entries []ContextEntry
	for _, p := range points {
		if p.Payload == nil {
			continue
		}
		entry, err := PayloadToEntry(p.Payload)
		if err != nil || entry == nil {
			continue
		}
		entries = append(entries, *entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"entries": entries,
		"count":   len(entries),
	})
}

func (s *Service) HandleContextDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ids := v.RequiredStringSlice("entry_ids")
	confirm := v.Bool("confirm", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm=true required for deletion")), nil
	}

	if err := s.qdrant.Get(CollContext).Delete(ctx, ids); err != nil {
		return mcp.ErrorResult(fmt.Errorf("delete entries: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": len(ids),
	})
}

// Retrieval Handlers

func (s *Service) HandleContextSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	agentID := v.String("agent_id", "")
	sessionID := v.String("session_id", "")
	namespace := v.String("namespace", "")
	entryTypes := v.StringSlice("entry_types")
	tags := v.StringSlice("tags")
	filePath := v.String("file_path", "")
	limit := v.Int("limit", 10)
	includeContent := v.Bool("include_content", true)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build filter
	var conds []any
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	if filePath != "" {
		conds = append(conds, Match("file_path", filePath))
	}
	if len(entryTypes) > 0 {
		conds = append(conds, FilterShould(Matches("entry_type", entryTypes)...))
	}
	if len(tags) > 0 {
		conds = append(conds, MatchAny("tags", tags))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	// Get embedding
	s.metrics.EmbeddingRequests.Add(1)
	vector, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		s.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding query: %w", err)), nil
	}

	searchStart := time.Now()
	results, err := s.qdrant.Get(CollContext).Search(ctx, vector, filter, limit, includeContent)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search: %w", err)), nil
	}
	s.metrics.RecordSearchLatency(time.Since(searchStart).Microseconds())
	s.metrics.RecallRequests.Add(1)

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}

func (s *Service) HandleContextRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	agentID := v.String("agent_id", "")
	sessionID := v.String("session_id", "")
	tokenBudget := v.Int("token_budget", s.cfg.DefaultTokenBudget)
	includeSummaries := v.Bool("include_summaries", true)
	includeDecisions := v.Bool("include_decisions", true)
	fileContext := v.String("file_context", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	opts := RecallOptions{
		Query:            query,
		AgentID:          agentID,
		SessionID:        sessionID,
		TokenBudget:      tokenBudget,
		IncludeSummaries: includeSummaries,
		IncludeDecisions: includeDecisions,
		FileContext:      fileContext,
	}

	entries, err := s.recallContext(ctx, opts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("recall: %w", err)), nil
	}

	s.metrics.RecallRequests.Add(1)
	if len(entries) > 0 {
		s.metrics.RecallHits.Add(1)
	} else {
		s.metrics.RecallMisses.Add(1)
	}

	totalTokens := 0
	for _, e := range entries {
		totalTokens += e.TokenCount
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"entries":      entries,
		"count":        len(entries),
		"total_tokens": totalTokens,
		"token_budget": tokenBudget,
	})
}

// Cross-Agent Handlers

func (s *Service) HandleContextShare(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	entryIDs := v.RequiredStringSlice("entry_ids")
	targetAgents := v.RequiredStringSlice("target_agents")
	visibilityStr := v.String("visibility", string(VisibilityShared))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	visibility := Visibility(visibilityStr)

	// Update entries
	updated := 0
	for _, id := range entryIDs {
		p, err := s.qdrant.Get(CollContext).GetPoint(ctx, id, false)
		if err != nil || p.Payload == nil {
			continue
		}

		entry, err := PayloadToEntry(p.Payload)
		if err != nil || entry == nil {
			continue
		}
		entry.Visibility = visibility
		entry.SharedWith = uniqueStrings(append(entry.SharedWith, targetAgents...))

		payload := map[string]any{
			"visibility":  string(entry.Visibility),
			"shared_with": entry.SharedWith,
		}
		if err := s.qdrant.Get(CollContext).SetPayload(ctx, []string{id}, payload, true); err == nil {
			updated++
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"updated": updated,
	})
}

func (s *Service) HandleContextQueryShared(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	requestingAgent := v.Required("requesting_agent_id")
	sourceAgentID := v.String("source_agent_id", "")
	entryTypes := v.StringSlice("entry_types")
	namespace := v.String("namespace", "")
	limit := v.Int("limit", 10)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build filter for shared/public entries
	conds := []any{
		FilterShould(
			Match("visibility", string(VisibilityPublic)),
			FilterMust(
				Match("visibility", string(VisibilityShared)),
				MatchAny("shared_with", []string{requestingAgent}),
			),
		),
	}

	if sourceAgentID != "" {
		conds = append(conds, Match("agent_id", sourceAgentID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	if len(entryTypes) > 0 {
		conds = append(conds, FilterShould(Matches("entry_type", entryTypes)...))
	}

	filter := FilterMust(conds...)

	s.metrics.EmbeddingRequests.Add(1)
	vector, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		s.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding query: %w", err)), nil
	}

	results, err := s.qdrant.Get(CollContext).Search(ctx, vector, filter, limit, true)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}

// Summarization Handler

func (s *Service) HandleContextSummarize(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	if err := s.generateSummary(ctx, session); err != nil {
		return mcp.ErrorResult(fmt.Errorf("generate summary: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"summarized": true,
	})
}

// Stats Handler

func (s *Service) HandleContextStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	sessionID := v.String("session_id", "")
	namespace := v.String("namespace", "")

	var conds []any
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	count, err := s.qdrant.Get(CollContext).Count(ctx, filter)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("count: %w", err)), nil
	}

	// Get entries to calculate tokens (limited sample)
	entries, _ := s.qdrant.Get(CollContext).Scroll(ctx, filter, 1000)
	totalTokens := 0
	byType := make(map[string]int)
	for _, e := range entries {
		totalTokens += e.TokenCount
		byType[string(e.EntryType)]++
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"entry_count":  count,
		"total_tokens": totalTokens,
		"by_type":      byType,
		"metrics":      s.metrics.Snapshot(),
	})
}

// Codebase Link Handler

func (s *Service) HandleContextLinkCodebase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	filePath := v.Required("file_path")
	repoID := v.String("repo_id", "")
	symbol := v.String("symbol", "")
	note := v.String("note", "")
	tags := v.StringSlice("tags")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	// Create a code_context entry
	content := fmt.Sprintf("File: %s", filePath)
	if symbol != "" {
		content += fmt.Sprintf("\nSymbol: %s", symbol)
	}
	if note != "" {
		content += fmt.Sprintf("\nNote: %s", note)
	}

	entry := ContextEntry{
		ID:            GenerateID(session.AgentID, sessionID, content, time.Now()),
		SchemaVersion: SchemaVersion,
		AgentID:       session.AgentID,
		SessionID:     sessionID,
		Namespace:     session.Namespace,
		EntryType:     EntryTypeCodeContext,
		Timestamp:     time.Now(),
		Title:         fmt.Sprintf("Code: %s", filePath),
		Content:       content,
		ContentHash:   ContentHashFunc(content),
		FilePath:      filePath,
		Tags:          tags,
		TokenCount:    EstimateTokens(content),
		Visibility:    s.cfg.DefaultVisibility,
		Metadata: map[string]any{
			"repo_id": repoID,
			"symbol":  symbol,
		},
	}

	// Generate embedding
	s.metrics.EmbeddingRequests.Add(1)
	vector, err := s.embed.EmbedQuery(ctx, entry.Title+" "+entry.Content)
	if err != nil {
		s.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding: %w", err)), nil
	}
	if len(vector) > 0 {
		s.vectorSize = len(vector)
	}
	if s.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}
	if err := s.qdrant.Get(CollContext).EnsureCollection(ctx, s.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      entry.ID,
		Vector:  vector,
		Payload: EntryToPayload(entry, s.cfg.EmbedModel),
	}

	if err := s.qdrant.Get(CollContext).Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"entry_id": entry.ID,
	})
}

// Internal helpers

func (s *Service) recallContext(ctx context.Context, opts RecallOptions) ([]ContextEntry, error) {
	var results []ContextEntry
	seen := make(map[string]bool)
	remainingBudget := opts.TokenBudget

	// Phase 1: Always include recent decisions (high priority)
	if opts.IncludeDecisions && remainingBudget > 0 {
		decisions, _ := s.getRecentByType(ctx, opts.AgentID, opts.SessionID, EntryTypeDecision, 5)
		for _, d := range decisions {
			if remainingBudget >= d.TokenCount && !seen[d.ID] {
				results = append(results, d)
				seen[d.ID] = true
				remainingBudget -= d.TokenCount
			}
		}
	}

	// Phase 2: Include session summaries
	if opts.IncludeSummaries && remainingBudget > 0 {
		summaries, _ := s.getRecentByType(ctx, opts.AgentID, opts.SessionID, EntryTypeSummary, 3)
		for _, sum := range summaries {
			if remainingBudget >= sum.TokenCount && !seen[sum.ID] {
				results = append(results, sum)
				seen[sum.ID] = true
				remainingBudget -= sum.TokenCount
			}
		}
	}

	// Phase 3: Semantic search for query-relevant context
	if remainingBudget > 500 && opts.Query != "" {
		s.metrics.EmbeddingRequests.Add(1)
		vector, err := s.embed.EmbedQuery(ctx, opts.Query)
		if err != nil {
			s.metrics.EmbeddingErrors.Add(1)
		}
		if err == nil {
			var conds []any
			if opts.AgentID != "" {
				conds = append(conds, Match("agent_id", opts.AgentID))
			}
			if opts.SessionID != "" {
				conds = append(conds, Match("session_id", opts.SessionID))
			}

			var filter map[string]any
			if len(conds) > 0 {
				filter = FilterMust(conds...)
			}

			searchResults, _ := s.qdrant.Get(CollContext).Search(ctx, vector, filter, 20, true)
			for _, sr := range searchResults {
				if remainingBudget >= sr.Entry.TokenCount && !seen[sr.Entry.ID] {
					results = append(results, sr.Entry)
					seen[sr.Entry.ID] = true
					remainingBudget -= sr.Entry.TokenCount
				}
			}
		}
	}

	// Phase 4: File-context boosting
	if opts.FileContext != "" && remainingBudget > 200 {
		fileEntries, _ := s.getEntriesForFile(ctx, opts.AgentID, opts.FileContext, 5)
		for _, fe := range fileEntries {
			if remainingBudget >= fe.TokenCount && !seen[fe.ID] {
				results = append(results, fe)
				seen[fe.ID] = true
				remainingBudget -= fe.TokenCount
			}
		}
	}

	return results, nil
}

func (s *Service) getRecentByType(ctx context.Context, agentID, sessionID string, entryType EntryType, limit int) ([]ContextEntry, error) {
	var conds []any
	conds = append(conds, Match("entry_type", string(entryType)))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}

	entries, err := s.qdrant.Get(CollContext).Scroll(ctx, FilterMust(conds...), limit*2)
	if err != nil {
		return nil, err
	}

	// Sort by timestamp descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (s *Service) getEntriesForFile(ctx context.Context, agentID, filePath string, limit int) ([]ContextEntry, error) {
	var conds []any
	conds = append(conds, Match("file_path", filePath))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	return s.qdrant.Get(CollContext).Scroll(ctx, FilterMust(conds...), limit)
}

func (s *Service) generateSummary(ctx context.Context, session *Session) error {
	// Get all entries for the session
	entries, err := s.qdrant.Get(CollContext).Scroll(ctx, FilterMust(
		Match("session_id", session.ID),
	), 1000)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	// Extract key information
	var findings []string
	var decisions []string
	filesSet := make(map[string]bool)

	for _, e := range entries {
		if e.FilePath != "" {
			filesSet[e.FilePath] = true
		}
		switch e.EntryType {
		case EntryTypeFinding:
			findings = append(findings, e.Title)
		case EntryTypeDecision:
			decisions = append(decisions, e.Title)
		}
	}

	var files []string
	for f := range filesSet {
		files = append(files, f)
	}

	// Build summary content
	var summaryParts []string
	summaryParts = append(summaryParts, fmt.Sprintf("Session: %s", session.ID))
	summaryParts = append(summaryParts, fmt.Sprintf("Agent: %s", session.AgentID))
	summaryParts = append(summaryParts, fmt.Sprintf("Entries: %d", len(entries)))

	if len(findings) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Key findings: %s", strings.Join(findings, "; ")))
	}
	if len(decisions) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Decisions: %s", strings.Join(decisions, "; ")))
	}
	if len(files) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Files: %s", strings.Join(files, ", ")))
	}

	summaryContent := strings.Join(summaryParts, "\n")

	// Create summary entry
	titleSuffix := session.ID
	if len(titleSuffix) > 8 {
		titleSuffix = titleSuffix[:8]
	}
	summaryEntry := ContextEntry{
		ID:            GenerateID(session.AgentID, session.ID, "summary", time.Now()),
		SchemaVersion: SchemaVersion,
		AgentID:       session.AgentID,
		SessionID:     session.ID,
		Namespace:     session.Namespace,
		EntryType:     EntryTypeSummary,
		Timestamp:     time.Now(),
		Title:         fmt.Sprintf("Session Summary: %s", titleSuffix),
		Content:       summaryContent,
		ContentHash:   ContentHashFunc(summaryContent),
		TokenCount:    EstimateTokens(summaryContent),
		Visibility:    s.cfg.DefaultVisibility,
		Metadata: map[string]any{
			"key_findings":  findings,
			"key_decisions": decisions,
			"files_touched": files,
			"entry_count":   len(entries),
		},
	}

	// Generate embedding and store
	s.metrics.EmbeddingRequests.Add(1)
	vector, err := s.embed.EmbedQuery(ctx, summaryEntry.Title+" "+summaryEntry.Content)
	if err != nil {
		s.metrics.EmbeddingErrors.Add(1)
		return err
	}
	if len(vector) > 0 {
		s.vectorSize = len(vector)
	}
	if s.vectorSize <= 0 {
		return fmt.Errorf("unknown vector size")
	}
	if err := s.qdrant.Get(CollContext).EnsureCollection(ctx, s.vectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      summaryEntry.ID,
		Vector:  vector,
		Payload: EntryToPayload(summaryEntry, s.cfg.EmbedModel),
	}

	if err := s.qdrant.Get(CollContext).Upsert(ctx, []Point{point}, true); err != nil {
		return err
	}

	// Update session
	now := time.Now()
	s.sess.mu.Lock()
	session.LastSummaryAt = &now
	s.sess.mu.Unlock()

	return nil
}

func (s *Service) maybeAutoSummarize(ctx context.Context, session *Session) {
	s.sess.mu.RLock()
	entryCount := session.EntryCount
	totalTokens := session.TotalTokens
	lastSummary := session.LastSummaryAt
	s.sess.mu.RUnlock()

	// Check thresholds
	shouldSummarize := false

	// Entry threshold
	if entryCount >= s.cfg.SummarizeEntryThreshold {
		if lastSummary == nil || entryCount >= s.cfg.SummarizeEntryThreshold {
			shouldSummarize = true
		}
	}

	// Token threshold
	if totalTokens >= s.cfg.SummarizeTokenThreshold {
		shouldSummarize = true
	}

	// Time threshold
	if lastSummary != nil {
		minutesSince := int(time.Since(*lastSummary).Minutes())
		if minutesSince >= s.cfg.SummarizeMinuteThreshold && entryCount > 10 {
			shouldSummarize = true
		}
	}

	if shouldSummarize {
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := s.generateSummary(bg, session); err != nil {
				s.logger.Warn("auto-summarize failed",
					"session_id", session.ID,
					"error", err,
				)
			}
		}()
	}
}

func (s *Service) upsertPointsBatched(ctx context.Context, q *QdrantClient, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	batchSize := s.cfg.UpsertBatchSize
	if batchSize <= 0 {
		batchSize = 64
	}
	for i := 0; i < len(points); i += batchSize {
		j := i + batchSize
		if j > len(points) {
			j = len(points)
		}
		if err := q.Upsert(ctx, points[i:j], true); err != nil {
			return err
		}
	}
	return nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

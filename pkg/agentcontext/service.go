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

	contextQdrant     *QdrantClient
	sessionsQdrant    *QdrantClient
	tasksQdrant       *QdrantClient
	annotationsQdrant *QdrantClient
	handoffsQdrant    *QdrantClient
	templatesQdrant   *QdrantClient
	embed             *embed.MorphClient

	// Persistence collections (Phase 1)
	graphEntitiesQdrant  *QdrantClient
	graphRelationsQdrant *QdrantClient
	workflowsQdrant      *QdrantClient
	workflowDefsQdrant   *QdrantClient
	memoryQdrant         *QdrantClient

	// Coordination collections
	presenceQdrant   *QdrantClient
	fileClaimsQdrant *QdrantClient
	worktreeQdrant   *QdrantClient

	sessionsMu sync.RWMutex
	sessions   map[string]*Session

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

	// Agent presence registry
	presenceMu  sync.RWMutex
	presenceMap map[string]*AgentPresence

	// File claims (advisory locks)
	fileClaimsMu sync.RWMutex
	fileClaims   map[string]map[string]*FileClaim // filePath -> agentID -> claim

	// Worktree assignments
	worktreeMu    sync.RWMutex
	worktreeAssns map[string]*WorktreeAssignment

	// Background services
	compactionScheduler *CompactionScheduler
	taskReconciler      *TaskReconciler
	memoryExporter      *MemoryExporter
	memoryImporter      *MemoryImporter
	bgCancel            context.CancelFunc
}

// Tracer returns the service's OTel tracer. Returns a noop tracer if none was configured.
func (s *Service) Tracer() trace.Tracer {
	return s.tracer
}

func NewServiceFromEnv(opts ...ServiceOption) (*Service, error) {
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		return nil, err
	}

	hc := httpclient.NewDefault()

	svc := &Service{
		cfg:               cfg,
		contextQdrant:     NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.ContextCollection, cfg.QdrantDistance),
		sessionsQdrant:    NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.SessionsCollection, cfg.QdrantDistance),
		tasksQdrant:       NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.TasksCollection, cfg.QdrantDistance),
		annotationsQdrant: NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.AnnotationsCollection, cfg.QdrantDistance),
		handoffsQdrant:    NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.HandoffsCollection, cfg.QdrantDistance),
		templatesQdrant:   NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.TemplatesCollection, cfg.QdrantDistance),
		embed:             embed.NewMorphClient(hc, cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel),
		sessions:          make(map[string]*Session),

		// Persistence collections (Phase 1)
		graphEntitiesQdrant:  NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.GraphEntitiesCollection, cfg.QdrantDistance),
		graphRelationsQdrant: NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.GraphRelationsCollection, cfg.QdrantDistance),
		workflowsQdrant:      NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.WorkflowsCollection, cfg.QdrantDistance),
		workflowDefsQdrant:   NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.WorkflowDefsCollection, cfg.QdrantDistance),
		memoryQdrant:         NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.MemoryCollection, cfg.QdrantDistance),

		// Coordination collections
		presenceQdrant:   NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.PresenceCollection, cfg.QdrantDistance),
		fileClaimsQdrant: NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.FileClaimsCollection, cfg.QdrantDistance),
		worktreeQdrant:   NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.WorktreeCollection, cfg.QdrantDistance),

		// In-memory coordination state
		presenceMap:   make(map[string]*AgentPresence),
		fileClaims:    make(map[string]map[string]*FileClaim),
		worktreeAssns: make(map[string]*WorktreeAssignment),
	}

	// Best-effort: if the context collection already exists, remember its vector size
	// so we can avoid "unknown vector size" edge-cases on operations like share/summarize.
	if exists, size, err := svc.contextQdrant.GetCollectionVectorSize(context.Background()); err == nil && exists && size > 0 {
		svc.vectorSize = size
	}

	// Initialize workflow engine
	svc.workflowEngine = NewWorkflowEngine(nil) // Tool executor set by daemon

	// Initialize knowledge graph with persistence
	svc.knowledgeGraph = NewKnowledgeGraph()
	svc.persistedKnowledgeGraph = svc.knowledgeGraph.SetPersistence(&GraphPersistenceConfig{
		EntitiesQdrant:  svc.graphEntitiesQdrant,
		RelationsQdrant: svc.graphRelationsQdrant,
		EmbedModel:      cfg.EmbedModel,
		VectorSize:      svc.vectorSize,
	})

	// Initialize memory hierarchy with persistence
	svc.memoryHierarchy = NewMemoryHierarchy()
	svc.persistedMemoryHierarchy = svc.memoryHierarchy.SetPersistence(&MemoryPersistenceConfig{
		MemoryQdrant: svc.memoryQdrant,
		EmbedModel:   cfg.EmbedModel,
		VectorSize:   svc.vectorSize,
	})

	// Initialize workflow engine with persistence
	svc.persistedWorkflowEngine = svc.workflowEngine.SetPersistence(&WorkflowPersistenceConfig{
		WorkflowsQdrant:    svc.workflowsQdrant,
		WorkflowDefsQdrant: svc.workflowDefsQdrant,
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
	svc.taskReconciler = NewTaskReconciler(reconcilerConfig, svc.tasksQdrant, svc, svc.logger)

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
	if err := s.loadSessionsFromQdrant(ctx); err != nil {
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
	if err := s.loadPresenceFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load presence", "error", err)
	}

	// Load file claims
	if err := s.loadFileClaimsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load file claims", "error", err)
	}

	// Load worktree assignments
	if err := s.loadWorktreeAssignmentsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load worktree assignments", "error", err)
	}

	return nil
}

// loadSessionsFromQdrant loads active sessions from Qdrant into memory
func (s *Service) loadSessionsFromQdrant(ctx context.Context) error {
	points, err := s.sessionsQdrant.ScrollPoints(ctx, FilterMust(Match("status", string(SessionStatusActive))), 500, false)
	if err != nil {
		return err
	}

	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	loaded := 0
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		s.sessions[sess.ID] = sess
		loaded++
	}

	if loaded > 0 {
		s.logger.Info("restored active sessions", "count", loaded)
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
		"presence_cleanup_interval_s", s.cfg.PresenceCleanupInterval,
	)

	// Start compaction scheduler
	if s.compactionScheduler != nil && s.cfg.CompactionEnabled {
		if err := s.compactionScheduler.Start(bgCtx); err != nil {
			s.logger.Warn("failed to start compaction scheduler", "error", err)
		}
	}

	// Start task reconciler
	if s.taskReconciler != nil && s.cfg.TaskReconcilerEnabled {
		s.taskReconciler.Start(bgCtx)
	}

	// Start presence cleanup goroutine
	go s.runPresenceCleanup(bgCtx)
}

// StopBackgroundServices stops all background goroutines
func (s *Service) StopBackgroundServices() {
	s.logger.Info("stopping background services")
	if s.compactionScheduler != nil {
		s.compactionScheduler.Stop()
	}
	if s.taskReconciler != nil {
		s.taskReconciler.Stop()
	}
	if s.bgCancel != nil {
		s.bgCancel()
	}
}

// Session Handlers

func (s *Service) HandleSessionStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", s.cfg.DefaultAgentID)
	namespace := v.String("namespace", s.cfg.DefaultNamespace)
	description := v.String("description", "")
	workingDir := v.String("working_dir", "")
	resumeID := v.String("resume_session_id", "")

	// agent_id is required if no default is configured
	if agentID == "" {
		return mcp.ErrorResult(fmt.Errorf("agent_id is required")), nil
	}

	// Check for resume
	if resumeID != "" {
		existing, err := s.getSession(ctx, resumeID)
		if err != nil || existing == nil {
			return mcp.ErrorResult(fmt.Errorf("session %s not found or cannot be resumed", resumeID)), nil
		}
		if existing.Status != string(SessionStatusActive) {
			existing.Status = string(SessionStatusActive)
			existing.EndedAt = nil
			if err := s.persistSession(ctx, existing); err != nil {
				return mcp.ErrorResult(fmt.Errorf("persist resumed session: %w", err)), nil
			}
		}
		result := map[string]any{
			"ok":         true,
			"session_id": resumeID,
			"resumed":    true,
			"agent_id":   existing.AgentID,
		}
		s.enrichSessionStartResult(ctx, result, existing.AgentID, existing.Namespace)
		return mcp.JSONResult(result)
	}

	// Create new session
	sessionID := GenerateID(agentID, "", time.Now().String(), time.Now())
	session := &Session{
		ID:          sessionID,
		AgentID:     agentID,
		Namespace:   namespace,
		StartedAt:   time.Now(),
		Status:      string(SessionStatusActive),
		Description: description,
		WorkingDir:  workingDir,
	}

	s.sessionsMu.Lock()
	s.sessions[sessionID] = session
	s.sessionsMu.Unlock()

	result := map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"agent_id":   agentID,
		"namespace":  namespace,
		"started_at": session.StartedAt.Format(time.RFC3339),
	}

	// Persist to Qdrant (sessions collection doesn't need vectors)
	if err := s.persistSession(ctx, session); err != nil {
		// Include warning in result but don't fail - session is in memory
		result["_warning"] = fmt.Sprintf("failed to persist session: %v", err)
	}

	s.metrics.SessionsActive.Add(1)
	s.metrics.SessionsTotal.Add(1)

	s.enrichSessionStartResult(ctx, result, agentID, namespace)
	return mcp.JSONResult(result)
}

// enrichSessionStartResult adds coordination info (pending handoffs, active agents) to a session start result.
func (s *Service) enrichSessionStartResult(ctx context.Context, result map[string]any, agentID, namespace string) {
	// Count active agents in same namespace
	activeAgents := 0
	s.presenceMu.RLock()
	now := time.Now()
	for _, p := range s.presenceMap {
		if now.After(p.LastHeartbeat.Add(time.Duration(p.HeartbeatTTL) * time.Second)) {
			continue // expired
		}
		activeAgents++
	}
	s.presenceMu.RUnlock()
	result["active_agents"] = activeAgents

	// Fetch pending handoffs for this agent
	var pendingHandoffs []map[string]any
	if s.handoffsQdrant != nil {
		conds := []any{
			Match("target_agent_id", agentID),
			Match("status", string(HandoffStatusPending)),
		}
		points, err := s.handoffsQdrant.ScrollPoints(ctx, FilterMust(conds...), 50, false)
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

func (s *Service) HandleSessionEnd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	summarize := v.Bool("summarize", true)
	cleanup := v.Bool("cleanup", true)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}
	now := time.Now()
	session.EndedAt = &now
	session.Status = string(SessionStatusEnded)
	s.sessionsMu.Lock()
	s.sessions[sessionID] = session
	s.sessionsMu.Unlock()
	s.metrics.SessionsActive.Add(-1)

	result := map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"ended_at":   now.Format(time.RFC3339),
		"summarized": false,
	}

	// Persist updated session
	if err := s.persistSession(ctx, session); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist session end: %v", err)
	}

	// Optionally generate summary
	if summarize && s.cfg.AutoSummarize {
		if err := s.generateSummary(ctx, session); err != nil {
			result["summary_error"] = err.Error()
		} else {
			result["summarized"] = true
			session.Status = string(SessionStatusSummarized)
			if err := s.persistSession(ctx, session); err != nil {
				result["_persist_error"] = err.Error()
			}
		}
	}

	// Auto-cleanup coordination resources
	if cleanup {
		agentID := session.AgentID
		cleanedUp := map[string]any{}

		// Release all file claims for this agent
		released := s.releaseAllClaimsForAgent(agentID)
		cleanedUp["file_claims_released"] = released

		// Deregister presence
		s.presenceMu.Lock()
		_, hadPresence := s.presenceMap[agentID]
		delete(s.presenceMap, agentID)
		s.presenceMu.Unlock()
		cleanedUp["presence_deregistered"] = hadPresence

		if hadPresence && s.presenceQdrant != nil {
			if err := s.presenceQdrant.DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID))); err != nil {
				s.logger.Warn("failed to delete presence from Qdrant", "agent_id", agentID, "error", err)
			}
		}

		// Orphan worktrees
		s.orphanWorktreesForAgent(agentID)
		cleanedUp["worktrees_orphaned"] = true

		// Mark incomplete session tasks as blocked/stale
		staleTasks := s.markSessionTasksStale(ctx, sessionID)
		cleanedUp["tasks_marked_stale"] = staleTasks

		result["cleanup"] = cleanedUp
	}

	return mcp.JSONResult(result)
}

// markSessionTasksStale marks pending/in_progress tasks for a session as blocked
// with a stale note, so they surface in the reconciler and HUD.
func (s *Service) markSessionTasksStale(ctx context.Context, sessionID string) int {
	filter := FilterMust(
		Match("session_id", sessionID),
		FilterShould(
			Match("status", string(TaskStatusPending)),
			Match("status", string(TaskStatusInProgress)),
		),
	)

	points, err := s.tasksQdrant.ScrollPoints(ctx, filter, 500, false)
	if err != nil || len(points) == 0 {
		return 0
	}

	count := 0
	now := time.Now().Format(time.RFC3339Nano)
	for _, p := range points {
		id := toString(p.Payload["id"])
		if id == "" {
			continue
		}
		payload := map[string]any{
			"status":     string(TaskStatusBlocked),
			"resolution": "session ended — task incomplete",
			"updated_at": now,
		}
		if err := s.tasksQdrant.SetPayload(ctx, []string{id}, payload, false); err != nil {
			s.logger.Warn("failed to mark task stale on session end", "task_id", id, "error", err)
			continue
		}
		count++
	}
	return count
}

func (s *Service) HandleSessionList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	namespace := v.String("namespace", "")
	status := v.String("status", "")
	limit := v.Int("limit", 20)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build filter
	var conds []any
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	if status != "" {
		conds = append(conds, Match("status", status))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}
	points, err := s.sessionsQdrant.ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list sessions: %w", err)), nil
	}

	sessions := make([]Session, 0, len(points))
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		sessions = append(sessions, *sess)
	}

	// Sort by started_at descending
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"sessions": sessions,
		"count":    len(sessions),
	})
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
		if err := s.contextQdrant.EnsureCollection(ctx, s.vectorSize); err != nil {
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

	if err := s.upsertPointsBatched(ctx, s.contextQdrant, points); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert entries: %w", err)), nil
	}

	// Update session stats
	s.sessionsMu.Lock()
	session.EntryCount += len(entries)
	for _, e := range entries {
		session.TotalTokens += e.TokenCount
	}
	s.sessionsMu.Unlock()
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

	points, err := s.contextQdrant.GetPoints(ctx, ids, false)
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

	if err := s.contextQdrant.Delete(ctx, ids); err != nil {
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
	results, err := s.contextQdrant.Search(ctx, vector, filter, limit, includeContent)
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
		p, err := s.contextQdrant.GetPoint(ctx, id, false)
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
		if err := s.contextQdrant.SetPayload(ctx, []string{id}, payload, true); err == nil {
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

	results, err := s.contextQdrant.Search(ctx, vector, filter, limit, true)
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

	count, err := s.contextQdrant.Count(ctx, filter)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("count: %w", err)), nil
	}

	// Get entries to calculate tokens (limited sample)
	entries, _ := s.contextQdrant.Scroll(ctx, filter, 1000)
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
	if err := s.contextQdrant.EnsureCollection(ctx, s.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      entry.ID,
		Vector:  vector,
		Payload: EntryToPayload(entry, s.cfg.EmbedModel),
	}

	if err := s.contextQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"entry_id": entry.ID,
	})
}

// Internal helpers

func (s *Service) persistSession(ctx context.Context, session *Session) error {
	payload := SessionToPayload(*session)

	// For sessions, we use a minimal vector (not for search)
	dummyVector := make([]float64, sessionsVectorSize)

	point := Point{
		ID:      session.ID,
		Vector:  dummyVector,
		Payload: payload,
	}

	// Ensure sessions collection with minimal vector size
	if err := s.sessionsQdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}
	return s.sessionsQdrant.Upsert(ctx, []Point{point}, true)
}

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

			searchResults, _ := s.contextQdrant.Search(ctx, vector, filter, 20, true)
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

	entries, err := s.contextQdrant.Scroll(ctx, FilterMust(conds...), limit*2)
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

	return s.contextQdrant.Scroll(ctx, FilterMust(conds...), limit)
}

func (s *Service) generateSummary(ctx context.Context, session *Session) error {
	// Get all entries for the session
	entries, err := s.contextQdrant.Scroll(ctx, FilterMust(
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
	if err := s.contextQdrant.EnsureCollection(ctx, s.vectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      summaryEntry.ID,
		Vector:  vector,
		Payload: EntryToPayload(summaryEntry, s.cfg.EmbedModel),
	}

	if err := s.contextQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return err
	}

	// Update session
	now := time.Now()
	s.sessionsMu.Lock()
	session.LastSummaryAt = &now
	s.sessionsMu.Unlock()

	return nil
}

func (s *Service) maybeAutoSummarize(ctx context.Context, session *Session) {
	s.sessionsMu.RLock()
	entryCount := session.EntryCount
	totalTokens := session.TotalTokens
	lastSummary := session.LastSummaryAt
	s.sessionsMu.RUnlock()

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

func (s *Service) getSession(ctx context.Context, sessionID string) (*Session, error) {
	s.sessionsMu.RLock()
	if sess, ok := s.sessions[sessionID]; ok {
		s.sessionsMu.RUnlock()
		return sess, nil
	}
	s.sessionsMu.RUnlock()

	p, err := s.sessionsQdrant.GetPoint(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	sess, err := PayloadToSession(p.Payload)
	if err != nil || sess == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	s.sessionsMu.Lock()
	// Re-check: another goroutine may have loaded the same session concurrently
	if existing, ok := s.sessions[sessionID]; ok {
		s.sessionsMu.Unlock()
		return existing, nil
	}
	s.sessions[sessionID] = sess
	s.sessionsMu.Unlock()

	return sess, nil
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

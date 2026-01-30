package agentcontext

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/httpclient"
)

const sessionsVectorSize = 4

type Service struct {
	cfg Config

	contextQdrant     *QdrantClient
	sessionsQdrant    *QdrantClient
	tasksQdrant       *QdrantClient
	annotationsQdrant *QdrantClient
	handoffsQdrant    *QdrantClient
	templatesQdrant   *QdrantClient
	embed             *embed.MorphClient

	sessionsMu sync.RWMutex
	sessions   map[string]*Session

	vectorSize int

	// Workflow orchestration
	workflowEngine *WorkflowEngine

	// Knowledge graph
	knowledgeGraph *KnowledgeGraph

	// Memory hierarchy
	memoryHierarchy *MemoryHierarchy
}

func NewServiceFromEnv() (*Service, error) {
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
	}

	// Best-effort: if the context collection already exists, remember its vector size
	// so we can avoid "unknown vector size" edge-cases on operations like share/summarize.
	if exists, size, err := svc.contextQdrant.GetCollectionVectorSize(context.Background()); err == nil && exists && size > 0 {
		svc.vectorSize = size
	}

	// Initialize workflow engine
	svc.workflowEngine = NewWorkflowEngine(nil) // Tool executor set by daemon

	// Initialize knowledge graph
	svc.knowledgeGraph = NewKnowledgeGraph()

	// Initialize memory hierarchy
	svc.memoryHierarchy = NewMemoryHierarchy()

	return svc, nil
}

// Session Handlers

func (s *Service) HandleSessionStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	agentID := toString(args["agent_id"])
	if agentID == "" {
		agentID = s.cfg.DefaultAgentID
	}
	if agentID == "" {
		return mcp.ErrorResult(fmt.Errorf("agent_id is required")), nil
	}

	namespace := toString(args["namespace"])
	if namespace == "" {
		namespace = s.cfg.DefaultNamespace
	}

	description := toString(args["description"])
	workingDir := toString(args["working_dir"])

	// Check for resume
	if resumeID := toString(args["resume_session_id"]); resumeID != "" {
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
		return mcp.JSONResult(map[string]any{
			"ok":         true,
			"session_id": resumeID,
			"resumed":    true,
			"agent_id":   existing.AgentID,
		})
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

	return mcp.JSONResult(result)
}

func (s *Service) HandleSessionEnd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	summarize := true
	if v, ok := args["summarize"].(bool); ok {
		summarize = v
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

	return mcp.JSONResult(result)
}

func (s *Service) HandleSessionList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	agentID := toString(args["agent_id"])
	if agentID == "" {
		return mcp.ErrorResult(fmt.Errorf("agent_id is required")), nil
	}

	namespace := toString(args["namespace"])
	status := toString(args["status"])
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 20
	}

	// Build filter
	conds := []any{Match("agent_id", agentID)}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	if status != "" {
		conds = append(conds, Match("status", status))
	}

	points, err := s.sessionsQdrant.ScrollPoints(ctx, FilterMust(conds...), limit, false)
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
	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	entriesRaw, ok := args["entries"].([]any)
	if !ok || len(entriesRaw) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entries array is required")), nil
	}

	if strings.TrimSpace(s.cfg.EmbedAPIKey) == "" {
		return mcp.ErrorResult(fmt.Errorf("AGENT_CONTEXT_EMBED_API_KEY (or MORPH_API_KEY / OPENAI_API_KEY) is not set")), nil
	}

	var entries []ContextEntry
	var embedTexts []string

	for _, raw := range entriesRaw {
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
		fmt.Fprintf(os.Stderr, "warning: persist session stats failed: %v\n", err)
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
	idsRaw := args["entry_ids"]
	ids := toStringSlice(idsRaw)
	if len(ids) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entry_ids is required")), nil
	}

	var entries []ContextEntry
	for _, id := range ids {
		p, err := s.contextQdrant.GetPoint(ctx, id, false)
		if err != nil || p.Payload == nil {
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
	idsRaw := args["entry_ids"]
	ids := toStringSlice(idsRaw)
	if len(ids) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entry_ids is required")), nil
	}

	confirm, _ := args["confirm"].(bool)
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
	query := toString(args["query"])
	if query == "" {
		return mcp.ErrorResult(fmt.Errorf("query is required")), nil
	}

	agentID := toString(args["agent_id"])
	sessionID := toString(args["session_id"])
	namespace := toString(args["namespace"])
	entryTypes := toStringSlice(args["entry_types"])
	tags := toStringSlice(args["tags"])
	filePath := toString(args["file_path"])
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 10
	}
	includeContent := true
	if v, ok := args["include_content"].(bool); ok {
		includeContent = v
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
	vector, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding query: %w", err)), nil
	}

	results, err := s.contextQdrant.Search(ctx, vector, filter, limit, includeContent)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}

func (s *Service) HandleContextRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := toString(args["query"])
	if query == "" {
		return mcp.ErrorResult(fmt.Errorf("query is required")), nil
	}

	agentID := toString(args["agent_id"])
	sessionID := toString(args["session_id"])
	tokenBudget := toInt(args["token_budget"])
	if tokenBudget <= 0 {
		tokenBudget = s.cfg.DefaultTokenBudget
	}
	includeSummaries := true
	if v, ok := args["include_summaries"].(bool); ok {
		includeSummaries = v
	}
	includeDecisions := true
	if v, ok := args["include_decisions"].(bool); ok {
		includeDecisions = v
	}
	fileContext := toString(args["file_context"])

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
	entryIDs := toStringSlice(args["entry_ids"])
	if len(entryIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entry_ids is required")), nil
	}

	targetAgents := toStringSlice(args["target_agents"])
	if len(targetAgents) == 0 {
		return mcp.ErrorResult(fmt.Errorf("target_agents is required")), nil
	}

	visibility := Visibility(toString(args["visibility"]))
	if visibility == "" {
		visibility = VisibilityShared
	}

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
	query := toString(args["query"])
	if query == "" {
		return mcp.ErrorResult(fmt.Errorf("query is required")), nil
	}

	requestingAgent := toString(args["requesting_agent_id"])
	if requestingAgent == "" {
		return mcp.ErrorResult(fmt.Errorf("requesting_agent_id is required")), nil
	}

	sourceAgentID := toString(args["source_agent_id"])
	entryTypes := toStringSlice(args["entry_types"])
	namespace := toString(args["namespace"])
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 10
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

	vector, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
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
	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
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
	agentID := toString(args["agent_id"])
	sessionID := toString(args["session_id"])
	namespace := toString(args["namespace"])

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
	})
}

// Codebase Link Handler

func (s *Service) HandleContextLinkCodebase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	filePath := toString(args["file_path"])
	if filePath == "" {
		return mcp.ErrorResult(fmt.Errorf("file_path is required")), nil
	}

	repoID := toString(args["repo_id"])
	symbol := toString(args["symbol"])
	note := toString(args["note"])
	tags := toStringSlice(args["tags"])

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
	vector, err := s.embed.EmbedQuery(ctx, entry.Title+" "+entry.Content)
	if err != nil {
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
		vector, err := s.embed.EmbedQuery(ctx, opts.Query)
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
	vector, err := s.embed.EmbedQuery(ctx, summaryEntry.Title+" "+summaryEntry.Content)
	if err != nil {
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
			_ = s.generateSummary(bg, session)
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

// ============================================================================
// Task Handlers
// ============================================================================

func (s *Service) HandleTaskAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	tasksRaw, ok := args["tasks"].([]any)
	if !ok || len(tasksRaw) == 0 {
		return mcp.ErrorResult(fmt.Errorf("tasks array is required")), nil
	}

	var tasks []Task
	var embedTexts []string
	now := time.Now()

	for _, raw := range tasksRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		title := toString(m["title"])
		if title == "" {
			continue
		}

		priority := TaskPriority(toString(m["priority"]))
		if priority == "" {
			priority = TaskPriorityMedium
		}

		task := Task{
			ID:         GenerateID(session.AgentID, sessionID, title, now),
			SessionID:  sessionID,
			AgentID:    session.AgentID,
			Namespace:  session.Namespace,
			Title:      title,
			Context:    toString(m["context"]),
			Priority:   priority,
			Status:     TaskStatusPending,
			FilePath:   toString(m["file_path"]),
			LineNumber: toInt(m["line_number"]),
			Tags:       toStringSlice(m["tags"]),
			BlockedBy:  toStringSlice(m["blocked_by"]),
			CreatedAt:  now,
			UpdatedAt:  now,
			TokenCount: EstimateTokens(title + " " + toString(m["context"])),
		}

		tasks = append(tasks, task)
		embedTexts = append(embedTexts, task.Title+" "+task.Context)
	}

	if len(tasks) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no valid tasks provided")), nil
	}

	// Generate embeddings
	vectors, err := s.embed.EmbedDocuments(ctx, embedTexts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding tasks: %w", err)), nil
	}
	if len(vectors) != len(tasks) {
		return mcp.ErrorResult(fmt.Errorf("embedding count mismatch")), nil
	}

	for _, v := range vectors {
		if len(v) > 0 {
			s.vectorSize = len(v)
			break
		}
	}
	if s.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}

	if err := s.tasksQdrant.EnsureCollection(ctx, s.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	// Build points
	points := make([]Point, 0, len(tasks))
	for i, task := range tasks {
		vector := vectors[i]
		if len(vector) == 0 {
			vector = make([]float64, s.vectorSize)
		}
		points = append(points, Point{
			ID:      task.ID,
			Vector:  vector,
			Payload: taskToPayload(task),
		})
	}

	if err := s.upsertPointsBatched(ctx, s.tasksQdrant, points); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert tasks: %w", err)), nil
	}

	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(tasks),
		"task_ids": ids,
	})
}

func (s *Service) HandleTaskUpdate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	taskID := toString(args["task_id"])
	if taskID == "" {
		return mcp.ErrorResult(fmt.Errorf("task_id is required")), nil
	}

	status := TaskStatus(toString(args["status"]))
	resolution := toString(args["resolution"])

	// Get existing task
	p, err := s.tasksQdrant.GetPoint(ctx, taskID, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("task %s not found: %w", taskID, err)), nil
	}

	task, err := payloadToTask(p.Payload)
	if err != nil || task == nil {
		return mcp.ErrorResult(fmt.Errorf("invalid task payload")), nil
	}

	// Update fields
	if status != "" {
		task.Status = status
	}
	if resolution != "" {
		task.Resolution = resolution
	}
	task.UpdatedAt = time.Now()

	if status == TaskStatusCompleted {
		now := time.Now()
		task.CompletedAt = &now
	}

	// Update payload
	payload := taskToPayload(*task)
	if err := s.tasksQdrant.SetPayload(ctx, []string{taskID}, payload, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("update task: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (s *Service) HandleTaskList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	statusesRaw := toStringSlice(args["status"])
	includeCompleted := getBool(args["include_completed"], false)
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 50
	}

	// Build filter
	var conds []any
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	// Status filter
	if len(statusesRaw) > 0 {
		conds = append(conds, FilterShould(Matches("status", statusesRaw)...))
	} else if !includeCompleted {
		// Exclude completed by default
		conds = append(conds, FilterShould(
			Match("status", string(TaskStatusPending)),
			Match("status", string(TaskStatusInProgress)),
			Match("status", string(TaskStatusBlocked)),
		))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	points, err := s.tasksQdrant.ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list tasks: %w", err)), nil
	}

	tasks := make([]Task, 0, len(points))
	for _, p := range points {
		task, err := payloadToTask(p.Payload)
		if err != nil || task == nil {
			continue
		}
		tasks = append(tasks, *task)
	}

	// Sort by priority (critical > high > medium > low), then by created_at
	sort.Slice(tasks, func(i, j int) bool {
		pi, pj := priorityRank(tasks[i].Priority), priorityRank(tasks[j].Priority)
		if pi != pj {
			return pi > pj
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"tasks": tasks,
		"count": len(tasks),
	})
}

// ============================================================================
// Annotation Handlers
// ============================================================================

func (s *Service) HandleAnnotationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	filePath := toString(args["file_path"])
	if filePath == "" {
		return mcp.ErrorResult(fmt.Errorf("file_path is required")), nil
	}

	lineStart := toInt(args["line_start"])
	if lineStart <= 0 {
		return mcp.ErrorResult(fmt.Errorf("line_start is required")), nil
	}

	content := toString(args["content"])
	if content == "" {
		return mcp.ErrorResult(fmt.Errorf("content is required")), nil
	}

	annotationType := AnnotationType(toString(args["annotation_type"]))
	if annotationType == "" {
		annotationType = AnnotationTypeNote
	}

	now := time.Now()
	annotation := CodeAnnotation{
		ID:             GenerateID(session.AgentID, sessionID, filePath+content, now),
		SessionID:      sessionID,
		AgentID:        session.AgentID,
		Namespace:      session.Namespace,
		FilePath:       filePath,
		LineStart:      lineStart,
		LineEnd:        toInt(args["line_end"]),
		Symbol:         toString(args["symbol"]),
		RepoID:         toString(args["repo_id"]),
		AnnotationType: annotationType,
		Content:        content,
		CreatedAt:      now,
		UpdatedAt:      now,
		TokenCount:     EstimateTokens(content),
	}

	// Generate embedding
	vector, err := s.embed.EmbedQuery(ctx, annotation.Content)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding: %w", err)), nil
	}
	if len(vector) > 0 {
		s.vectorSize = len(vector)
	}
	if s.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}

	if err := s.annotationsQdrant.EnsureCollection(ctx, s.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      annotation.ID,
		Vector:  vector,
		Payload: annotationToPayload(annotation),
	}

	if err := s.annotationsQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert annotation: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"annotation_id": annotation.ID,
	})
}

func (s *Service) HandleAnnotationsGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	filePath := toString(args["file_path"])
	agentID := toString(args["agent_id"])
	lineStart := toInt(args["line_start"])
	lineEnd := toInt(args["line_end"])
	annotationTypes := toStringSlice(args["annotation_types"])
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 50
	}

	// Build filter
	var conds []any
	if filePath != "" {
		conds = append(conds, Match("file_path", filePath))
	}
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if len(annotationTypes) > 0 {
		conds = append(conds, FilterShould(Matches("annotation_type", annotationTypes)...))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	points, err := s.annotationsQdrant.ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get annotations: %w", err)), nil
	}

	annotations := make([]CodeAnnotation, 0, len(points))
	for _, p := range points {
		ann, err := payloadToAnnotation(p.Payload)
		if err != nil || ann == nil {
			continue
		}
		// Filter by line range if specified
		if lineStart > 0 && ann.LineStart < lineStart {
			continue
		}
		if lineEnd > 0 && ann.LineStart > lineEnd {
			continue
		}
		annotations = append(annotations, *ann)
	}

	// Sort by line number
	sort.Slice(annotations, func(i, j int) bool {
		return annotations[i].LineStart < annotations[j].LineStart
	})

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"annotations": annotations,
		"count":       len(annotations),
	})
}

// ============================================================================
// Handoff Handlers
// ============================================================================

func (s *Service) HandleHandoffCreate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	targetAgentID := toString(args["target_agent_id"])
	if targetAgentID == "" {
		return mcp.ErrorResult(fmt.Errorf("target_agent_id is required")), nil
	}

	handoffType := HandoffType(toString(args["handoff_type"]))
	if handoffType == "" {
		handoffType = HandoffTypeSummaryOnly
	}

	instructions := toString(args["instructions"])
	entryIDs := toStringSlice(args["entry_ids"])
	tokenBudget := toInt(args["token_budget"])
	if tokenBudget <= 0 {
		tokenBudget = s.cfg.HandoffMaxTokens
	}

	now := time.Now()
	handoff := Handoff{
		ID:            GenerateID(session.AgentID, targetAgentID, sessionID, now),
		SourceAgentID: session.AgentID,
		SourceSession: sessionID,
		TargetAgentID: targetAgentID,
		HandoffType:   handoffType,
		Status:        HandoffStatusPending,
		Instructions:  instructions,
		CreatedAt:     now,
	}

	// Set expiration
	if s.cfg.HandoffExpirationHours > 0 {
		expires := now.Add(time.Duration(s.cfg.HandoffExpirationHours) * time.Hour)
		handoff.ExpiresAt = &expires
	}

	// Build handoff content based on type
	var summary strings.Builder
	totalTokens := 0

	switch handoffType {
	case HandoffTypeFull:
		// Get all entries for the session
		entries, _ := s.contextQdrant.Scroll(ctx, FilterMust(Match("session_id", sessionID)), 500)
		for _, e := range entries {
			if totalTokens+e.TokenCount > tokenBudget {
				break
			}
			handoff.EntryIDs = append(handoff.EntryIDs, e.ID)
			totalTokens += e.TokenCount
			summary.WriteString(fmt.Sprintf("- [%s] %s\n", e.EntryType, e.Title))
		}

	case HandoffTypeSelective:
		// Use provided entry IDs
		for _, id := range entryIDs {
			p, err := s.contextQdrant.GetPoint(ctx, id, false)
			if err != nil {
				continue
			}
			entry, _ := PayloadToEntry(p.Payload)
			if entry == nil {
				continue
			}
			if totalTokens+entry.TokenCount > tokenBudget {
				break
			}
			handoff.EntryIDs = append(handoff.EntryIDs, id)
			totalTokens += entry.TokenCount
			summary.WriteString(fmt.Sprintf("- [%s] %s\n", entry.EntryType, entry.Title))
		}

	case HandoffTypeSummaryOnly:
		// Get session summaries and decisions only
		entries, _ := s.contextQdrant.Scroll(ctx, FilterMust(
			Match("session_id", sessionID),
			FilterShould(
				Match("entry_type", string(EntryTypeSummary)),
				Match("entry_type", string(EntryTypeDecision)),
			),
		), 20)
		for _, e := range entries {
			if totalTokens+e.TokenCount > tokenBudget {
				break
			}
			handoff.EntryIDs = append(handoff.EntryIDs, e.ID)
			totalTokens += e.TokenCount
			summary.WriteString(fmt.Sprintf("- [%s] %s\n", e.EntryType, e.Title))
		}
	}

	handoff.Summary = summary.String()
	handoff.TokenCount = totalTokens

	// Store handoff (use dummy vector since not searching by content)
	dummyVector := make([]float64, sessionsVectorSize)
	if err := s.handoffsQdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      handoff.ID,
		Vector:  dummyVector,
		Payload: handoffToPayload(handoff),
	}

	if err := s.handoffsQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("create handoff: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"handoff_id":  handoff.ID,
		"token_count": handoff.TokenCount,
		"entry_count": len(handoff.EntryIDs),
		"summary":     handoff.Summary,
	})
}

func (s *Service) HandleHandoffAccept(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	handoffID := toString(args["handoff_id"])
	if handoffID == "" {
		return mcp.ErrorResult(fmt.Errorf("handoff_id is required")), nil
	}

	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	importEntries := getBool(args["import_entries"], false)

	// Get handoff
	p, err := s.handoffsQdrant.GetPoint(ctx, handoffID, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("handoff %s not found", handoffID)), nil
	}

	handoff, err := payloadToHandoff(p.Payload)
	if err != nil || handoff == nil {
		return mcp.ErrorResult(fmt.Errorf("invalid handoff")), nil
	}

	// Verify target agent
	if handoff.TargetAgentID != session.AgentID {
		return mcp.ErrorResult(fmt.Errorf("handoff is not for this agent")), nil
	}

	// Check expiration
	if handoff.ExpiresAt != nil && time.Now().After(*handoff.ExpiresAt) {
		handoff.Status = HandoffStatusExpired
		s.handoffsQdrant.SetPayload(ctx, []string{handoffID}, map[string]any{"status": string(HandoffStatusExpired)}, true)
		return mcp.ErrorResult(fmt.Errorf("handoff has expired")), nil
	}

	// Check status
	if handoff.Status != HandoffStatusPending {
		return mcp.ErrorResult(fmt.Errorf("handoff already %s", handoff.Status)), nil
	}

	result := map[string]any{
		"ok":           true,
		"handoff_id":   handoffID,
		"source_agent": handoff.SourceAgentID,
		"instructions": handoff.Instructions,
		"summary":      handoff.Summary,
		"token_count":  handoff.TokenCount,
	}

	// Import entries if requested
	if importEntries && len(handoff.EntryIDs) > 0 {
		var importedEntries []ContextEntry
		for _, id := range handoff.EntryIDs {
			ep, err := s.contextQdrant.GetPoint(ctx, id, true)
			if err != nil {
				continue
			}
			entry, _ := PayloadToEntry(ep.Payload)
			if entry == nil {
				continue
			}
			importedEntries = append(importedEntries, *entry)
		}
		result["imported_entries"] = importedEntries
		result["imported_count"] = len(importedEntries)
	}

	// Mark accepted
	now := time.Now()
	handoff.Status = HandoffStatusAccepted
	handoff.AcceptedAt = &now
	s.handoffsQdrant.SetPayload(ctx, []string{handoffID}, map[string]any{
		"status":      string(HandoffStatusAccepted),
		"accepted_at": now.Format(time.RFC3339Nano),
	}, true)

	return mcp.JSONResult(result)
}

// ============================================================================
// Template Handlers
// ============================================================================

func (s *Service) HandleTemplateCreate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name := toString(args["name"])
	if name == "" {
		return mcp.ErrorResult(fmt.Errorf("name is required")), nil
	}

	description := toString(args["description"])
	namespace := toString(args["namespace"])
	fromSessionID := toString(args["from_session_id"])

	// Determine creator
	createdBy := toString(args["created_by"])
	if createdBy == "" {
		createdBy = s.cfg.DefaultAgentID
	}

	now := time.Now()
	template := SessionTemplate{
		ID:          GenerateID(createdBy, name, namespace, now),
		Name:        name,
		Description: description,
		Namespace:   namespace,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Parse entry types to include
	if types := toStringSlice(args["entry_types_to_include"]); len(types) > 0 {
		for _, t := range types {
			template.EntryTypesToInclude = append(template.EntryTypesToInclude, EntryType(t))
		}
	}

	// Copy from existing session if specified
	if fromSessionID != "" {
		entries, _ := s.contextQdrant.Scroll(ctx, FilterMust(Match("session_id", fromSessionID)), 100)
		for _, e := range entries {
			// Filter by entry types if specified
			if len(template.EntryTypesToInclude) > 0 {
				found := false
				for _, t := range template.EntryTypesToInclude {
					if e.EntryType == t {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			template.InitialEntries = append(template.InitialEntries, e)
		}
	}

	// Store template (use dummy vector since not searching by content)
	dummyVector := make([]float64, sessionsVectorSize)
	if err := s.templatesQdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      template.ID,
		Vector:  dummyVector,
		Payload: templateToPayload(template),
	}

	if err := s.templatesQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("create template: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"template_id": template.ID,
		"name":        template.Name,
		"entry_count": len(template.InitialEntries),
	})
}

func (s *Service) HandleTemplateList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	namespace := toString(args["namespace"])
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 50
	}

	var filter map[string]any
	if namespace != "" {
		filter = FilterMust(Match("namespace", namespace))
	}

	points, err := s.templatesQdrant.ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list templates: %w", err)), nil
	}

	templates := make([]map[string]any, 0, len(points))
	for _, p := range points {
		templates = append(templates, map[string]any{
			"id":          p.Payload["id"],
			"name":        p.Payload["name"],
			"description": p.Payload["description"],
			"namespace":   p.Payload["namespace"],
			"created_by":  p.Payload["created_by"],
			"created_at":  p.Payload["created_at"],
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"templates": templates,
		"count":     len(templates),
	})
}

// ============================================================================
// Enhanced Recall Handler
// ============================================================================

func (s *Service) HandleEnhancedRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := toString(args["query"])
	if query == "" {
		return mcp.ErrorResult(fmt.Errorf("query is required")), nil
	}

	opts := EnhancedRecallOptions{
		RecallOptions: RecallOptions{
			Query:            query,
			AgentID:          toString(args["agent_id"]),
			SessionID:        toString(args["session_id"]),
			Namespace:        toString(args["namespace"]),
			TokenBudget:      toInt(args["token_budget"]),
			IncludeSummaries: getBool(args["include_summaries"], true),
			IncludeDecisions: getBool(args["include_decisions"], true),
			FileContext:      toString(args["file_context"]),
		},
		SymbolContext: toString(args["symbol_context"]),
		RecencyWeight: toFloat(args["recency_weight"]),
		IncludeTasks:  getBool(args["include_tasks"], true),
	}

	if opts.TokenBudget <= 0 {
		opts.TokenBudget = s.cfg.DefaultTokenBudget
	}
	if opts.RecencyWeight <= 0 {
		opts.RecencyWeight = s.cfg.DefaultRecencyWeight
	}

	entries, err := s.enhancedRecallContext(ctx, opts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("recall: %w", err)), nil
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
		"token_budget": opts.TokenBudget,
	})
}

func (s *Service) enhancedRecallContext(ctx context.Context, opts EnhancedRecallOptions) ([]ContextEntry, error) {
	var results []ContextEntry
	seen := make(map[string]bool)
	remainingBudget := opts.TokenBudget

	// Phase 1 (NEW): Active tasks - highest priority
	if opts.IncludeTasks && remainingBudget > 0 {
		tasks, _ := s.getActiveTasks(ctx, opts.AgentID, opts.SessionID, 5)
		for _, task := range tasks {
			// Convert task to context entry for unified return type
			entry := ContextEntry{
				ID:         task.ID,
				AgentID:    task.AgentID,
				SessionID:  task.SessionID,
				EntryType:  EntryTypeTask,
				Title:      fmt.Sprintf("[%s] %s", task.Priority, task.Title),
				Content:    task.Context,
				FilePath:   task.FilePath,
				Timestamp:  task.CreatedAt,
				Tags:       task.Tags,
				TokenCount: task.TokenCount,
				Metadata: map[string]any{
					"task_status":   string(task.Status),
					"task_priority": string(task.Priority),
					"blocked_by":    task.BlockedBy,
				},
			}
			if remainingBudget >= entry.TokenCount && !seen[entry.ID] {
				results = append(results, entry)
				seen[entry.ID] = true
				remainingBudget -= entry.TokenCount
			}
		}
	}

	// Phase 2: Recent decisions
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

	// Phase 3: Session summaries
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

	// Phase 4 (NEW): Symbol-context boosting
	if opts.SymbolContext != "" && remainingBudget > 500 {
		symbolEntries, _ := s.getEntriesForSymbol(ctx, opts.AgentID, opts.SymbolContext, 5)
		for _, se := range symbolEntries {
			if remainingBudget >= se.TokenCount && !seen[se.ID] {
				results = append(results, se)
				seen[se.ID] = true
				remainingBudget -= se.TokenCount
			}
		}
	}

	// Phase 5 (ENHANCED): Semantic search with recency weighting
	if remainingBudget > 500 && opts.Query != "" {
		vector, err := s.embed.EmbedQuery(ctx, opts.Query)
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

			searchResults, _ := s.contextQdrant.Search(ctx, vector, filter, 30, true)

			// Apply recency weighting
			now := time.Now()
			for i := range searchResults {
				age := now.Sub(searchResults[i].Entry.Timestamp)
				ageHours := age.Hours()
				// Decay factor: recent entries get boosted
				recencyBoost := 1.0 / (1.0 + (ageHours / 24.0 * opts.RecencyWeight))
				searchResults[i].Score *= (1.0 + recencyBoost*opts.RecencyWeight)
			}

			// Re-sort by adjusted score
			sort.Slice(searchResults, func(i, j int) bool {
				return searchResults[i].Score > searchResults[j].Score
			})

			for _, sr := range searchResults {
				if remainingBudget >= sr.Entry.TokenCount && !seen[sr.Entry.ID] {
					results = append(results, sr.Entry)
					seen[sr.Entry.ID] = true
					remainingBudget -= sr.Entry.TokenCount
				}
			}
		}
	}

	// Phase 6: File-context boosting
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

	// Phase 7 (NEW): Code annotations for current file
	if opts.FileContext != "" && remainingBudget > 100 {
		annotations, _ := s.getAnnotationsForFile(ctx, opts.AgentID, opts.FileContext, 5)
		for _, ann := range annotations {
			entry := ContextEntry{
				ID:         ann.ID,
				AgentID:    ann.AgentID,
				SessionID:  ann.SessionID,
				EntryType:  EntryTypeAnnotation,
				Title:      fmt.Sprintf("[%s] %s:%d", ann.AnnotationType, ann.FilePath, ann.LineStart),
				Content:    ann.Content,
				FilePath:   ann.FilePath,
				LineStart:  ann.LineStart,
				LineEnd:    ann.LineEnd,
				Timestamp:  ann.CreatedAt,
				TokenCount: ann.TokenCount,
			}
			if remainingBudget >= entry.TokenCount && !seen[entry.ID] {
				results = append(results, entry)
				seen[entry.ID] = true
				remainingBudget -= entry.TokenCount
			}
		}
	}

	return results, nil
}

func (s *Service) getActiveTasks(ctx context.Context, agentID, sessionID string, limit int) ([]Task, error) {
	var conds []any
	conds = append(conds, FilterShould(
		Match("status", string(TaskStatusPending)),
		Match("status", string(TaskStatusInProgress)),
		Match("status", string(TaskStatusBlocked)),
	))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}

	points, err := s.tasksQdrant.ScrollPoints(ctx, FilterMust(conds...), limit*2, false)
	if err != nil {
		return nil, err
	}

	tasks := make([]Task, 0, len(points))
	for _, p := range points {
		task, err := payloadToTask(p.Payload)
		if err != nil || task == nil {
			continue
		}
		tasks = append(tasks, *task)
	}

	// Sort by priority
	sort.Slice(tasks, func(i, j int) bool {
		return priorityRank(tasks[i].Priority) > priorityRank(tasks[j].Priority)
	})

	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

func (s *Service) getEntriesForSymbol(ctx context.Context, agentID, symbol string, limit int) ([]ContextEntry, error) {
	// Search for entries mentioning the symbol
	vector, err := s.embed.EmbedQuery(ctx, symbol)
	if err != nil {
		return nil, err
	}

	var filter map[string]any
	if agentID != "" {
		filter = FilterMust(Match("agent_id", agentID))
	}

	results, err := s.contextQdrant.Search(ctx, vector, filter, limit, true)
	if err != nil {
		return nil, err
	}

	entries := make([]ContextEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, r.Entry)
	}
	return entries, nil
}

func (s *Service) getAnnotationsForFile(ctx context.Context, agentID, filePath string, limit int) ([]CodeAnnotation, error) {
	var conds []any
	conds = append(conds, Match("file_path", filePath))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	points, err := s.annotationsQdrant.ScrollPoints(ctx, FilterMust(conds...), limit, false)
	if err != nil {
		return nil, err
	}

	annotations := make([]CodeAnnotation, 0, len(points))
	for _, p := range points {
		ann, err := payloadToAnnotation(p.Payload)
		if err != nil || ann == nil {
			continue
		}
		annotations = append(annotations, *ann)
	}

	return annotations, nil
}

// ============================================================================
// Payload Conversion Helpers
// ============================================================================

func taskToPayload(t Task) map[string]any {
	return map[string]any{
		"id":          t.ID,
		"session_id":  t.SessionID,
		"agent_id":    t.AgentID,
		"namespace":   t.Namespace,
		"title":       t.Title,
		"context":     t.Context,
		"priority":    string(t.Priority),
		"status":      string(t.Status),
		"resolution":  t.Resolution,
		"file_path":   t.FilePath,
		"line_number": t.LineNumber,
		"symbol":      t.Symbol,
		"tags":        t.Tags,
		"blocked_by":  t.BlockedBy,
		"parent_id":   t.ParentID,
		"created_at":  t.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":  t.UpdatedAt.Format(time.RFC3339Nano),
		"token_count": t.TokenCount,
	}
}

func payloadToTask(payload map[string]any) (*Task, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	task := &Task{
		ID:         toString(payload["id"]),
		SessionID:  toString(payload["session_id"]),
		AgentID:    toString(payload["agent_id"]),
		Namespace:  toString(payload["namespace"]),
		Title:      toString(payload["title"]),
		Context:    toString(payload["context"]),
		Priority:   TaskPriority(toString(payload["priority"])),
		Status:     TaskStatus(toString(payload["status"])),
		Resolution: toString(payload["resolution"]),
		FilePath:   toString(payload["file_path"]),
		LineNumber: toInt(payload["line_number"]),
		Symbol:     toString(payload["symbol"]),
		Tags:       toStringSlice(payload["tags"]),
		BlockedBy:  toStringSlice(payload["blocked_by"]),
		ParentID:   toString(payload["parent_id"]),
		TokenCount: toInt(payload["token_count"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			task.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			task.UpdatedAt = t
		}
	}
	if ts := toString(payload["completed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			task.CompletedAt = &t
		}
	}

	return task, nil
}

func annotationToPayload(a CodeAnnotation) map[string]any {
	return map[string]any{
		"id":              a.ID,
		"session_id":      a.SessionID,
		"agent_id":        a.AgentID,
		"namespace":       a.Namespace,
		"file_path":       a.FilePath,
		"line_start":      a.LineStart,
		"line_end":        a.LineEnd,
		"symbol":          a.Symbol,
		"repo_id":         a.RepoID,
		"annotation_type": string(a.AnnotationType),
		"content":         a.Content,
		"created_at":      a.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":      a.UpdatedAt.Format(time.RFC3339Nano),
		"token_count":     a.TokenCount,
	}
}

func payloadToAnnotation(payload map[string]any) (*CodeAnnotation, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	ann := &CodeAnnotation{
		ID:             toString(payload["id"]),
		SessionID:      toString(payload["session_id"]),
		AgentID:        toString(payload["agent_id"]),
		Namespace:      toString(payload["namespace"]),
		FilePath:       toString(payload["file_path"]),
		LineStart:      toInt(payload["line_start"]),
		LineEnd:        toInt(payload["line_end"]),
		Symbol:         toString(payload["symbol"]),
		RepoID:         toString(payload["repo_id"]),
		AnnotationType: AnnotationType(toString(payload["annotation_type"])),
		Content:        toString(payload["content"]),
		TokenCount:     toInt(payload["token_count"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			ann.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			ann.UpdatedAt = t
		}
	}

	return ann, nil
}

func handoffToPayload(h Handoff) map[string]any {
	payload := map[string]any{
		"id":              h.ID,
		"source_agent_id": h.SourceAgentID,
		"source_session":  h.SourceSession,
		"target_agent_id": h.TargetAgentID,
		"handoff_type":    string(h.HandoffType),
		"status":          string(h.Status),
		"instructions":    h.Instructions,
		"summary":         h.Summary,
		"entry_ids":       h.EntryIDs,
		"token_count":     h.TokenCount,
		"created_at":      h.CreatedAt.Format(time.RFC3339Nano),
	}
	if h.AcceptedAt != nil {
		payload["accepted_at"] = h.AcceptedAt.Format(time.RFC3339Nano)
	}
	if h.ExpiresAt != nil {
		payload["expires_at"] = h.ExpiresAt.Format(time.RFC3339Nano)
	}
	return payload
}

func payloadToHandoff(payload map[string]any) (*Handoff, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	h := &Handoff{
		ID:            toString(payload["id"]),
		SourceAgentID: toString(payload["source_agent_id"]),
		SourceSession: toString(payload["source_session"]),
		TargetAgentID: toString(payload["target_agent_id"]),
		HandoffType:   HandoffType(toString(payload["handoff_type"])),
		Status:        HandoffStatus(toString(payload["status"])),
		Instructions:  toString(payload["instructions"]),
		Summary:       toString(payload["summary"]),
		EntryIDs:      toStringSlice(payload["entry_ids"]),
		TokenCount:    toInt(payload["token_count"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			h.CreatedAt = t
		}
	}
	if ts := toString(payload["accepted_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			h.AcceptedAt = &t
		}
	}
	if ts := toString(payload["expires_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			h.ExpiresAt = &t
		}
	}

	return h, nil
}

func templateToPayload(t SessionTemplate) map[string]any {
	entryTypes := make([]string, len(t.EntryTypesToInclude))
	for i, et := range t.EntryTypesToInclude {
		entryTypes[i] = string(et)
	}

	return map[string]any{
		"id":                     t.ID,
		"name":                   t.Name,
		"description":            t.Description,
		"namespace":              t.Namespace,
		"created_by":             t.CreatedBy,
		"entry_types_to_include": entryTypes,
		"initial_entries_count":  len(t.InitialEntries),
		"created_at":             t.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":             t.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// Helper functions

func getBool(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func priorityRank(p TaskPriority) int {
	switch p {
	case TaskPriorityCritical:
		return 4
	case TaskPriorityHigh:
		return 3
	case TaskPriorityMedium:
		return 2
	case TaskPriorityLow:
		return 1
	}
	return 0
}

// =========================================================================
// Workflow Orchestration Handlers
// =========================================================================

// SetToolExecutor sets the callback for executing MCP tools from workflows
func (s *Service) SetToolExecutor(executor ToolExecutor) {
	s.workflowEngine.toolExecutor = executor
}

// GetWorkflowEngine returns the workflow engine for direct access
func (s *Service) GetWorkflowEngine() *WorkflowEngine {
	return s.workflowEngine
}

// HandleWorkflowDefine registers a new workflow definition
func (s *Service) HandleWorkflowDefine(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name := toString(args["name"])
	if name == "" {
		return mcp.ErrorResult(fmt.Errorf("name is required")), nil
	}

	description := toString(args["description"])
	namespace := toString(args["namespace"])
	if namespace == "" {
		namespace = s.cfg.DefaultNamespace
	}
	createdBy := toString(args["created_by"])
	if createdBy == "" {
		createdBy = s.cfg.DefaultAgentID
	}

	// Parse steps
	stepsRaw, ok := args["steps"].([]any)
	if !ok || len(stepsRaw) == 0 {
		return mcp.ErrorResult(fmt.Errorf("steps array is required")), nil
	}

	steps := make([]WorkflowStep, len(stepsRaw))
	for i, stepRaw := range stepsRaw {
		stepMap, ok := stepRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("step %d must be an object", i)), nil
		}

		step := WorkflowStep{
			ID:               toString(stepMap["id"]),
			Name:             toString(stepMap["name"]),
			Description:      toString(stepMap["description"]),
			StepType:         StepType(toString(stepMap["step_type"])),
			ToolName:         toString(stepMap["tool_name"]),
			ServerName:       toString(stepMap["server_name"]),
			RequiresApproval: getBool(stepMap["requires_approval"], false),
			ApprovalMessage:  toString(stepMap["approval_message"]),
			Condition:        toString(stepMap["condition"]),
			MaxRetries:       toInt(stepMap["max_retries"]),
			RetryDelay:       toInt(stepMap["retry_delay_ms"]),
			Timeout:          toInt(stepMap["timeout_seconds"]),
			RollbackStepID:   toString(stepMap["rollback_step_id"]),
			SubflowID:        toString(stepMap["subflow_id"]),
		}

		if step.ID == "" {
			step.ID = fmt.Sprintf("step-%d", i+1)
		}
		if step.StepType == "" {
			step.StepType = StepTypeTool
		}

		// Parse tool args
		if toolArgs, ok := stepMap["tool_args"].(map[string]any); ok {
			step.ToolArgs = toolArgs
		}

		// Parse depends_on
		if deps, ok := stepMap["depends_on"].([]any); ok {
			step.DependsOn = make([]string, len(deps))
			for j, dep := range deps {
				step.DependsOn[j] = toString(dep)
			}
		}

		steps[i] = step
	}

	def := &WorkflowDefinition{
		Name:              name,
		Description:       description,
		Namespace:         namespace,
		CreatedBy:         createdBy,
		Steps:             steps,
		RollbackOnFailure: getBool(args["rollback_on_failure"], false),
		TimeoutSeconds:    toInt(args["timeout_seconds"]),
	}

	// Parse input schema if provided
	if schema, ok := args["input_schema"].(map[string]any); ok {
		def.InputSchema = schema
	}

	if err := s.workflowEngine.RegisterDefinition(def); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"definition_id": def.ID,
		"name":          def.Name,
		"step_count":    len(def.Steps),
	})
}

// HandleWorkflowStart starts a new workflow instance
func (s *Service) HandleWorkflowStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	definitionID := toString(args["definition_id"])
	if definitionID == "" {
		return mcp.ErrorResult(fmt.Errorf("definition_id is required")), nil
	}

	sessionID := toString(args["session_id"])
	if sessionID == "" {
		return mcp.ErrorResult(fmt.Errorf("session_id is required")), nil
	}

	agentID := toString(args["agent_id"])
	if agentID == "" {
		agentID = s.cfg.DefaultAgentID
	}

	// Parse input
	var input map[string]any
	if inputRaw, ok := args["input"].(map[string]any); ok {
		input = inputRaw
	}

	wf, err := s.workflowEngine.StartWorkflow(ctx, definitionID, sessionID, agentID, input)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": wf.ID,
		"name":        wf.Definition.Name,
		"status":      string(wf.Status),
		"total_steps": wf.TotalSteps,
	})
}

// HandleWorkflowStatus gets the status of a workflow
func (s *Service) HandleWorkflowStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	workflowID := toString(args["workflow_id"])
	if workflowID == "" {
		return mcp.ErrorResult(fmt.Errorf("workflow_id is required")), nil
	}

	wf, err := s.workflowEngine.GetWorkflow(workflowID)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	progress := 0.0
	if wf.TotalSteps > 0 {
		progress = float64(wf.CompletedSteps) / float64(wf.TotalSteps)
	}

	// Build step summaries
	stepSummaries := make([]map[string]any, 0, len(wf.StepStates))
	for _, step := range wf.Definition.Steps {
		state := wf.StepStates[step.ID]
		summary := map[string]any{
			"id":     step.ID,
			"name":   step.Name,
			"type":   string(step.StepType),
			"status": string(state.Status),
		}
		if state.Error != "" {
			summary["error"] = state.Error
		}
		if state.ApprovalInfo != nil {
			summary["approval_status"] = string(state.ApprovalInfo.Status)
		}
		stepSummaries = append(stepSummaries, summary)
	}

	result := map[string]any{
		"workflow_id":     wf.ID,
		"name":            wf.Definition.Name,
		"status":          string(wf.Status),
		"current_step":    wf.CurrentStep,
		"progress":        progress,
		"completed_steps": wf.CompletedSteps,
		"total_steps":     wf.TotalSteps,
		"steps":           stepSummaries,
		"created_at":      wf.CreatedAt.Format(time.RFC3339Nano),
	}

	if wf.Error != "" {
		result["error"] = wf.Error
	}
	if wf.StartedAt != nil {
		result["started_at"] = wf.StartedAt.Format(time.RFC3339Nano)
	}
	if wf.CompletedAt != nil {
		result["completed_at"] = wf.CompletedAt.Format(time.RFC3339Nano)
	}

	// Include output if completed
	if wf.Status == WorkflowStatusCompleted && wf.Output != nil {
		result["output"] = wf.Output
	}

	return mcp.JSONResult(result)
}

// HandleWorkflowList lists workflows with filtering
func (s *Service) HandleWorkflowList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	if agentID == "" {
		agentID = s.cfg.DefaultAgentID
	}
	status := WorkflowStatus(toString(args["status"]))

	workflows := s.workflowEngine.ListWorkflows(sessionID, agentID, status)

	results := make([]map[string]any, len(workflows))
	for i, wf := range workflows {
		results[i] = map[string]any{
			"workflow_id":  wf.ID,
			"name":         wf.Name,
			"status":       string(wf.Status),
			"progress":     wf.Progress,
			"current_step": wf.CurrentStep,
			"created_at":   wf.CreatedAt.Format(time.RFC3339Nano),
		}
		if wf.Error != "" {
			results[i]["error"] = wf.Error
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(results),
		"workflows": results,
	})
}

// HandleWorkflowApprove approves a pending step
func (s *Service) HandleWorkflowApprove(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	workflowID := toString(args["workflow_id"])
	if workflowID == "" {
		return mcp.ErrorResult(fmt.Errorf("workflow_id is required")), nil
	}

	stepID := toString(args["step_id"])
	if stepID == "" {
		return mcp.ErrorResult(fmt.Errorf("step_id is required")), nil
	}

	approverID := toString(args["approver_id"])
	if approverID == "" {
		approverID = s.cfg.DefaultAgentID
	}
	comment := toString(args["comment"])

	if err := s.workflowEngine.ApproveStep(workflowID, stepID, approverID, comment); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"step_id":     stepID,
		"approved_by": approverID,
	})
}

// HandleWorkflowReject rejects a pending step
func (s *Service) HandleWorkflowReject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	workflowID := toString(args["workflow_id"])
	if workflowID == "" {
		return mcp.ErrorResult(fmt.Errorf("workflow_id is required")), nil
	}

	stepID := toString(args["step_id"])
	if stepID == "" {
		return mcp.ErrorResult(fmt.Errorf("step_id is required")), nil
	}

	rejecterID := toString(args["rejecter_id"])
	if rejecterID == "" {
		rejecterID = s.cfg.DefaultAgentID
	}
	comment := toString(args["comment"])

	if err := s.workflowEngine.RejectStep(workflowID, stepID, rejecterID, comment); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"step_id":     stepID,
		"rejected_by": rejecterID,
	})
}

// HandleWorkflowCancel cancels a running workflow
func (s *Service) HandleWorkflowCancel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	workflowID := toString(args["workflow_id"])
	if workflowID == "" {
		return mcp.ErrorResult(fmt.Errorf("workflow_id is required")), nil
	}

	reason := toString(args["reason"])
	if reason == "" {
		reason = "cancelled by user"
	}

	if err := s.workflowEngine.CancelWorkflow(workflowID, reason); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"reason":      reason,
	})
}

// HandleWorkflowEvents gets events for a workflow
func (s *Service) HandleWorkflowEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	workflowID := toString(args["workflow_id"])
	if workflowID == "" {
		return mcp.ErrorResult(fmt.Errorf("workflow_id is required")), nil
	}

	events, err := s.workflowEngine.GetEvents(workflowID)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	results := make([]map[string]any, len(events))
	for i, e := range events {
		results[i] = map[string]any{
			"id":         e.ID,
			"event_type": e.EventType,
			"timestamp":  e.Timestamp.Format(time.RFC3339Nano),
		}
		if e.StepID != "" {
			results[i]["step_id"] = e.StepID
		}
		if e.Details != nil {
			results[i]["details"] = e.Details
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"count":       len(results),
		"events":      results,
	})
}

// HandleWorkflowDefinitionList lists workflow definitions
func (s *Service) HandleWorkflowDefinitionList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	namespace := toString(args["namespace"])

	definitions := s.workflowEngine.ListDefinitions(namespace)

	results := make([]map[string]any, len(definitions))
	for i, def := range definitions {
		results[i] = map[string]any{
			"id":          def.ID,
			"name":        def.Name,
			"description": def.Description,
			"namespace":   def.Namespace,
			"step_count":  len(def.Steps),
			"created_by":  def.CreatedBy,
			"created_at":  def.CreatedAt.Format(time.RFC3339Nano),
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"count":       len(results),
		"definitions": results,
	})
}

// =========================================================================
// Knowledge Graph Handlers
// =========================================================================

// GetKnowledgeGraph returns the knowledge graph for direct access
func (s *Service) GetKnowledgeGraph() *KnowledgeGraph {
	return s.knowledgeGraph
}

// HandleEntityAdd adds entities to the knowledge graph
func (s *Service) HandleEntityAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	if agentID == "" {
		agentID = s.cfg.DefaultAgentID
	}

	entitiesRaw, ok := args["entities"].([]any)
	if !ok || len(entitiesRaw) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entities array is required")), nil
	}

	var addedIDs []string
	for i, entityRaw := range entitiesRaw {
		entityMap, ok := entityRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("entity %d must be an object", i)), nil
		}

		entity := &Entity{
			ID:          toString(entityMap["id"]),
			Type:        EntityType(toString(entityMap["type"])),
			Name:        toString(entityMap["name"]),
			Description: toString(entityMap["description"]),
			Namespace:   toString(entityMap["namespace"]),
			FilePath:    toString(entityMap["file_path"]),
			LineStart:   toInt(entityMap["line_start"]),
			LineEnd:     toInt(entityMap["line_end"]),
			Language:    toString(entityMap["language"]),
			Signature:   toString(entityMap["signature"]),
			SessionID:   sessionID,
			AgentID:     agentID,
		}

		if entity.Namespace == "" {
			entity.Namespace = s.cfg.DefaultNamespace
		}

		// Parse properties
		if props, ok := entityMap["properties"].(map[string]any); ok {
			entity.Properties = props
		}

		// Parse tags
		if tags, ok := entityMap["tags"].([]any); ok {
			for _, t := range tags {
				if ts := toString(t); ts != "" {
					entity.Tags = append(entity.Tags, ts)
				}
			}
		}

		if err := s.knowledgeGraph.AddEntity(entity); err != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to add entity %d: %w", i, err)), nil
		}

		addedIDs = append(addedIDs, entity.ID)
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"count":      len(addedIDs),
		"entity_ids": addedIDs,
	})
}

// HandleEntityGet retrieves entities by ID
func (s *Service) HandleEntityGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	entityIDs := toStringSlice(args["entity_ids"])
	if len(entityIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entity_ids is required")), nil
	}

	var entities []map[string]any
	for _, id := range entityIDs {
		entity, err := s.knowledgeGraph.GetEntity(id)
		if err != nil {
			continue // Skip not found
		}
		entities = append(entities, entityToMap(entity))
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(entities),
		"entities": entities,
	})
}

// HandleEntityFind searches for entities
func (s *Service) HandleEntityFind(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	entityType := EntityType(toString(args["type"]))
	namespace := toString(args["namespace"])
	namePattern := toString(args["name_pattern"])
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 50
	}

	entities := s.knowledgeGraph.FindEntities(entityType, namespace, namePattern, limit)

	results := make([]map[string]any, len(entities))
	for i, e := range entities {
		results[i] = entityToMap(e)
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(results),
		"entities": results,
	})
}

// HandleEntityDelete removes entities
func (s *Service) HandleEntityDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	entityIDs := toStringSlice(args["entity_ids"])
	if len(entityIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entity_ids is required")), nil
	}

	confirm := getBool(args["confirm"], false)
	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete entities")), nil
	}

	var deleted []string
	for _, id := range entityIDs {
		if err := s.knowledgeGraph.DeleteEntity(id); err == nil {
			deleted = append(deleted, id)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": deleted,
	})
}

// HandleRelationAdd adds relations to the knowledge graph
func (s *Service) HandleRelationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	if agentID == "" {
		agentID = s.cfg.DefaultAgentID
	}

	relationsRaw, ok := args["relations"].([]any)
	if !ok || len(relationsRaw) == 0 {
		return mcp.ErrorResult(fmt.Errorf("relations array is required")), nil
	}

	var addedIDs []string
	for i, relRaw := range relationsRaw {
		relMap, ok := relRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("relation %d must be an object", i)), nil
		}

		rel := &Relation{
			ID:            toString(relMap["id"]),
			Type:          RelationType(toString(relMap["type"])),
			SourceID:      toString(relMap["source_id"]),
			TargetID:      toString(relMap["target_id"]),
			Weight:        toFloat(relMap["weight"]),
			Bidirectional: getBool(relMap["bidirectional"], false),
			Evidence:      toString(relMap["evidence"]),
			Reasoning:     toString(relMap["reasoning"]),
			SessionID:     sessionID,
			AgentID:       agentID,
		}

		// Parse properties
		if props, ok := relMap["properties"].(map[string]any); ok {
			rel.Properties = props
		}

		if err := s.knowledgeGraph.AddRelation(rel); err != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to add relation %d: %w", i, err)), nil
		}

		addedIDs = append(addedIDs, rel.ID)
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"count":        len(addedIDs),
		"relation_ids": addedIDs,
	})
}

// HandleRelationGet gets relations for an entity
func (s *Service) HandleRelationGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	entityID := toString(args["entity_id"])
	if entityID == "" {
		return mcp.ErrorResult(fmt.Errorf("entity_id is required")), nil
	}

	outgoing := getBool(args["outgoing"], true)
	incoming := getBool(args["incoming"], true)

	var relTypes []RelationType
	if types, ok := args["relation_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				relTypes = append(relTypes, RelationType(ts))
			}
		}
	}

	relations := s.knowledgeGraph.GetEntityRelations(entityID, relTypes, outgoing, incoming)

	results := make([]map[string]any, len(relations))
	for i, r := range relations {
		results[i] = relationToMap(r)
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(results),
		"relations": results,
	})
}

// HandleRelationDelete removes relations
func (s *Service) HandleRelationDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	relationIDs := toStringSlice(args["relation_ids"])
	if len(relationIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("relation_ids is required")), nil
	}

	confirm := getBool(args["confirm"], false)
	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete relations")), nil
	}

	var deleted []string
	for _, id := range relationIDs {
		if err := s.knowledgeGraph.DeleteRelation(id); err == nil {
			deleted = append(deleted, id)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": deleted,
	})
}

// HandleGraphQuery executes a graph query
func (s *Service) HandleGraphQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := GraphQuery{
		Pattern:           toString(args["pattern"]),
		EntityID:          toString(args["entity_id"]),
		Namespace:         toString(args["namespace"]),
		SessionID:         toString(args["session_id"]),
		AgentID:           toString(args["agent_id"]),
		MaxDepth:          toInt(args["max_depth"]),
		Bidirectional:     getBool(args["bidirectional"], false),
		Limit:             toInt(args["limit"]),
		IncludeProperties: getBool(args["include_properties"], true),
	}

	// Parse entity types
	if types, ok := args["source_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				query.SourceTypes = append(query.SourceTypes, EntityType(ts))
			}
		}
	}
	if types, ok := args["target_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				query.TargetTypes = append(query.TargetTypes, EntityType(ts))
			}
		}
	}

	// Parse relation types
	if types, ok := args["relation_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				query.RelationTypes = append(query.RelationTypes, RelationType(ts))
			}
		}
	}

	result, err := s.knowledgeGraph.Query(query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	entities := make([]map[string]any, len(result.Entities))
	for i, e := range result.Entities {
		entities[i] = entityToMap(&e)
	}

	relations := make([]map[string]any, len(result.Relations))
	for i, r := range result.Relations {
		relations[i] = relationToMap(&r)
	}

	paths := make([]map[string]any, len(result.Paths))
	for i, p := range result.Paths {
		paths[i] = map[string]any{
			"nodes":  p.Nodes,
			"edges":  p.Edges,
			"length": p.Length,
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":             true,
		"entity_count":   len(entities),
		"relation_count": len(relations),
		"entities":       entities,
		"relations":      relations,
		"paths":          paths,
	})
}

// HandleFindPath finds a path between two entities
func (s *Service) HandleFindPath(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sourceID := toString(args["source_id"])
	targetID := toString(args["target_id"])
	if sourceID == "" || targetID == "" {
		return mcp.ErrorResult(fmt.Errorf("source_id and target_id are required")), nil
	}

	maxDepth := toInt(args["max_depth"])
	if maxDepth <= 0 {
		maxDepth = 5
	}

	var relTypes []RelationType
	if types, ok := args["relation_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				relTypes = append(relTypes, RelationType(ts))
			}
		}
	}

	path, err := s.knowledgeGraph.FindPath(sourceID, targetID, maxDepth, relTypes)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"path":   path.Nodes,
		"edges":  path.Edges,
		"length": path.Length,
	})
}

// HandleReasoningChainAdd adds a reasoning chain
func (s *Service) HandleReasoningChainAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	if agentID == "" {
		agentID = s.cfg.DefaultAgentID
	}

	query := toString(args["query"])
	if query == "" {
		return mcp.ErrorResult(fmt.Errorf("query is required")), nil
	}

	chain := &ReasoningChain{
		Query:      query,
		Conclusion: toString(args["conclusion"]),
		Confidence: toFloat(args["confidence"]),
		SessionID:  sessionID,
		AgentID:    agentID,
	}

	// Parse steps
	if stepsRaw, ok := args["steps"].([]any); ok {
		for i, stepRaw := range stepsRaw {
			stepMap, ok := stepRaw.(map[string]any)
			if !ok {
				continue
			}
			step := ReasoningStep{
				StepNumber:  i + 1,
				Description: toString(stepMap["description"]),
				Conclusion:  toString(stepMap["conclusion"]),
				Confidence:  toFloat(stepMap["confidence"]),
				EntityIDs:   toStringSlice(stepMap["entity_ids"]),
				RelationIDs: toStringSlice(stepMap["relation_ids"]),
			}
			chain.Steps = append(chain.Steps, step)
		}
	}

	if err := s.knowledgeGraph.AddReasoningChain(chain); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"chain_id": chain.ID,
	})
}

// HandleReasoningChainGet retrieves a reasoning chain
func (s *Service) HandleReasoningChainGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	chainID := toString(args["chain_id"])
	if chainID == "" {
		return mcp.ErrorResult(fmt.Errorf("chain_id is required")), nil
	}

	chain, err := s.knowledgeGraph.GetReasoningChain(chainID)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	steps := make([]map[string]any, len(chain.Steps))
	for i, step := range chain.Steps {
		steps[i] = map[string]any{
			"step_number":  step.StepNumber,
			"description":  step.Description,
			"conclusion":   step.Conclusion,
			"confidence":   step.Confidence,
			"entity_ids":   step.EntityIDs,
			"relation_ids": step.RelationIDs,
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"chain_id":   chain.ID,
		"query":      chain.Query,
		"steps":      steps,
		"conclusion": chain.Conclusion,
		"confidence": chain.Confidence,
		"created_at": chain.CreatedAt.Format(time.RFC3339Nano),
	})
}

// HandleReasoningChainList lists reasoning chains
func (s *Service) HandleReasoningChainList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 50
	}

	chains := s.knowledgeGraph.ListReasoningChains(sessionID, agentID, limit)

	results := make([]map[string]any, len(chains))
	for i, chain := range chains {
		results[i] = map[string]any{
			"chain_id":   chain.ID,
			"query":      chain.Query,
			"step_count": len(chain.Steps),
			"conclusion": chain.Conclusion,
			"confidence": chain.Confidence,
			"created_at": chain.CreatedAt.Format(time.RFC3339Nano),
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(results),
		"chains": results,
	})
}

// HandleGraphStats returns knowledge graph statistics
func (s *Service) HandleGraphStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	stats := s.knowledgeGraph.Stats()

	return mcp.JSONResult(map[string]any{
		"ok":                true,
		"total_entities":    stats.TotalEntities,
		"total_relations":   stats.TotalRelations,
		"entities_by_type":  stats.EntitiesByType,
		"relations_by_type": stats.RelationsByType,
		"namespaces":        stats.Namespaces,
	})
}

// Helper functions for knowledge graph

func entityToMap(e *Entity) map[string]any {
	m := map[string]any{
		"id":          e.ID,
		"type":        string(e.Type),
		"name":        e.Name,
		"description": e.Description,
		"namespace":   e.Namespace,
		"created_at":  e.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":  e.UpdatedAt.Format(time.RFC3339Nano),
	}
	if e.FilePath != "" {
		m["file_path"] = e.FilePath
	}
	if e.LineStart > 0 {
		m["line_start"] = e.LineStart
	}
	if e.LineEnd > 0 {
		m["line_end"] = e.LineEnd
	}
	if e.Language != "" {
		m["language"] = e.Language
	}
	if e.Signature != "" {
		m["signature"] = e.Signature
	}
	if e.Properties != nil {
		m["properties"] = e.Properties
	}
	if len(e.Tags) > 0 {
		m["tags"] = e.Tags
	}
	if e.SessionID != "" {
		m["session_id"] = e.SessionID
	}
	if e.AgentID != "" {
		m["agent_id"] = e.AgentID
	}
	return m
}

func relationToMap(r *Relation) map[string]any {
	m := map[string]any{
		"id":         r.ID,
		"type":       string(r.Type),
		"source_id":  r.SourceID,
		"target_id":  r.TargetID,
		"weight":     r.Weight,
		"created_at": r.CreatedAt.Format(time.RFC3339Nano),
	}
	if r.Bidirectional {
		m["bidirectional"] = true
	}
	if r.Evidence != "" {
		m["evidence"] = r.Evidence
	}
	if r.Reasoning != "" {
		m["reasoning"] = r.Reasoning
	}
	if r.Properties != nil {
		m["properties"] = r.Properties
	}
	if r.SessionID != "" {
		m["session_id"] = r.SessionID
	}
	if r.AgentID != "" {
		m["agent_id"] = r.AgentID
	}
	return m
}

// =========================================================================
// Memory Hierarchy Handlers
// =========================================================================

// GetMemoryHierarchy returns the memory hierarchy for direct access
func (s *Service) GetMemoryHierarchy() *MemoryHierarchy {
	return s.memoryHierarchy
}

// HandleMemoryAdd adds items to the memory hierarchy
func (s *Service) HandleMemoryAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	if agentID == "" {
		agentID = s.cfg.DefaultAgentID
	}
	namespace := toString(args["namespace"])
	if namespace == "" {
		namespace = s.cfg.DefaultNamespace
	}

	itemsRaw, ok := args["items"].([]any)
	if !ok || len(itemsRaw) == 0 {
		return mcp.ErrorResult(fmt.Errorf("items array is required")), nil
	}

	var addedIDs []string
	for i, itemRaw := range itemsRaw {
		itemMap, ok := itemRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("item %d must be an object", i)), nil
		}

		item := &MemoryItem{
			ID:         toString(itemMap["id"]),
			Tier:       MemoryTier(toString(itemMap["tier"])),
			Importance: ImportanceLevel(toString(itemMap["importance"])),
			Title:      toString(itemMap["title"]),
			Content:    toString(itemMap["content"]),
			Category:   toString(itemMap["category"]),
			Namespace:  namespace,
			SessionID:  sessionID,
			AgentID:    agentID,
		}

		// Parse tags
		if tags, ok := itemMap["tags"].([]any); ok {
			for _, t := range tags {
				if ts := toString(t); ts != "" {
					item.Tags = append(item.Tags, ts)
				}
			}
		}

		// Parse metadata
		if metadata, ok := itemMap["metadata"].(map[string]any); ok {
			item.Metadata = metadata
		}

		// Parse related_ids
		if related, ok := itemMap["related_ids"].([]any); ok {
			for _, r := range related {
				if rs := toString(r); rs != "" {
					item.RelatedIDs = append(item.RelatedIDs, rs)
				}
			}
		}

		if err := s.memoryHierarchy.AddItem(item); err != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to add item %d: %w", i, err)), nil
		}

		addedIDs = append(addedIDs, item.ID)
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(addedIDs),
		"item_ids": addedIDs,
	})
}

// HandleMemoryGet retrieves memory items by ID
func (s *Service) HandleMemoryGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	itemIDs := toStringSlice(args["item_ids"])
	if len(itemIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("item_ids is required")), nil
	}

	var items []map[string]any
	for _, id := range itemIDs {
		item, err := s.memoryHierarchy.GetItem(id)
		if err != nil {
			continue // Skip not found
		}
		items = append(items, memoryItemToMap(item))
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": len(items),
		"items": items,
	})
}

// HandleMemoryRecall recalls memories matching criteria
func (s *Service) HandleMemoryRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	req := MemoryRecallRequest{
		Query:         toString(args["query"]),
		Namespace:     toString(args["namespace"]),
		SessionID:     toString(args["session_id"]),
		AgentID:       toString(args["agent_id"]),
		TokenBudget:   toInt(args["token_budget"]),
		Limit:         toInt(args["limit"]),
		MinImportance: toFloat(args["min_importance"]),
	}

	// Parse tiers
	if tiers, ok := args["tiers"].([]any); ok {
		for _, t := range tiers {
			if ts := toString(t); ts != "" {
				req.Tiers = append(req.Tiers, MemoryTier(ts))
			}
		}
	}

	// Parse categories
	req.Categories = toStringSlice(args["categories"])

	// Parse tags
	req.Tags = toStringSlice(args["tags"])

	result, err := s.memoryHierarchy.Recall(req)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	items := make([]map[string]any, len(result.Items))
	for i, item := range result.Items {
		items[i] = memoryItemToMap(&item)
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"count":        len(items),
		"items":        items,
		"by_tier":      result.ByTier,
		"total_tokens": result.TotalTokens,
		"truncated":    result.Truncated,
	})
}

// HandleMemoryDelete deletes memory items
func (s *Service) HandleMemoryDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	itemIDs := toStringSlice(args["item_ids"])
	if len(itemIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("item_ids is required")), nil
	}

	confirm := getBool(args["confirm"], false)
	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete items")), nil
	}

	var deleted []string
	for _, id := range itemIDs {
		if err := s.memoryHierarchy.DeleteItem(id); err == nil {
			deleted = append(deleted, id)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": deleted,
	})
}

// HandleMemoryPromote promotes items to a higher tier
func (s *Service) HandleMemoryPromote(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	itemIDs := toStringSlice(args["item_ids"])
	if len(itemIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("item_ids is required")), nil
	}

	var promoted []string
	var errors []string
	for _, id := range itemIDs {
		if err := s.memoryHierarchy.PromoteItem(id); err == nil {
			promoted = append(promoted, id)
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", id, err))
		}
	}

	result := map[string]any{
		"ok":       true,
		"promoted": promoted,
	}
	if len(errors) > 0 {
		result["errors"] = errors
	}

	return mcp.JSONResult(result)
}

// HandleMemoryDemote demotes items to a lower tier
func (s *Service) HandleMemoryDemote(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	itemIDs := toStringSlice(args["item_ids"])
	if len(itemIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("item_ids is required")), nil
	}

	var demoted []string
	var errors []string
	for _, id := range itemIDs {
		if err := s.memoryHierarchy.DemoteItem(id); err == nil {
			demoted = append(demoted, id)
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", id, err))
		}
	}

	result := map[string]any{
		"ok":      true,
		"demoted": demoted,
	}
	if len(errors) > 0 {
		result["errors"] = errors
	}

	return mcp.JSONResult(result)
}

// HandleMemoryCompress compresses items to reduce token usage
func (s *Service) HandleMemoryCompress(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	// Check if we're compressing specific items or running tier-wide compression
	itemIDs := toStringSlice(args["item_ids"])
	tier := MemoryTier(toString(args["tier"]))

	if len(itemIDs) > 0 {
		// Compress specific items
		var compressed []string
		var errors []string
		for _, id := range itemIDs {
			if err := s.memoryHierarchy.CompressItem(id); err == nil {
				compressed = append(compressed, id)
			} else {
				errors = append(errors, fmt.Sprintf("%s: %v", id, err))
			}
		}

		result := map[string]any{
			"ok":         true,
			"compressed": compressed,
		}
		if len(errors) > 0 {
			result["errors"] = errors
		}
		return mcp.JSONResult(result)
	}

	if tier != "" {
		// Run tier-wide compression
		job, err := s.memoryHierarchy.RunCompression(tier)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(map[string]any{
			"ok":                true,
			"job_id":            job.ID,
			"tier":              job.Tier,
			"item_count":        job.ItemCount,
			"expired_count":     job.ExpiredCount,
			"original_tokens":   job.OriginalTokens,
			"compressed_tokens": job.CompressedTokens,
			"status":            job.Status,
		})
	}

	return mcp.ErrorResult(fmt.Errorf("either item_ids or tier is required")), nil
}

// HandleMemoryMerge merges multiple items into one
func (s *Service) HandleMemoryMerge(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	itemIDs := toStringSlice(args["item_ids"])
	if len(itemIDs) < 2 {
		return mcp.ErrorResult(fmt.Errorf("at least 2 item_ids are required to merge")), nil
	}

	newTitle := toString(args["new_title"])
	if newTitle == "" {
		newTitle = "Merged Memory"
	}

	merged, err := s.memoryHierarchy.MergeItems(itemIDs, newTitle)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":              true,
		"merged_item_id":  merged.ID,
		"merged_item":     memoryItemToMap(merged),
		"source_item_ids": itemIDs,
	})
}

// HandleMemoryStats returns memory hierarchy statistics
func (s *Service) HandleMemoryStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	stats := s.memoryHierarchy.Stats()

	return mcp.JSONResult(map[string]any{
		"ok":                        true,
		"total_items":               stats.TotalItems,
		"total_tokens":              stats.TotalTokens,
		"compression_ratio":         stats.CompressionRatio,
		"items_added_last_24h":      stats.ItemsAddedLast24h,
		"items_compressed_last_24h": stats.ItemsCompressedLast24h,
		"working_memory": map[string]any{
			"item_count":     stats.WorkingMemory.ItemCount,
			"token_count":    stats.WorkingMemory.TokenCount,
			"avg_importance": stats.WorkingMemory.AvgImportance,
			"by_category":    stats.WorkingMemory.ByCategory,
			"by_importance":  stats.WorkingMemory.ByImportance,
		},
		"short_term_memory": map[string]any{
			"item_count":     stats.ShortTermMemory.ItemCount,
			"token_count":    stats.ShortTermMemory.TokenCount,
			"avg_importance": stats.ShortTermMemory.AvgImportance,
			"by_category":    stats.ShortTermMemory.ByCategory,
			"by_importance":  stats.ShortTermMemory.ByImportance,
		},
		"long_term_memory": map[string]any{
			"item_count":     stats.LongTermMemory.ItemCount,
			"token_count":    stats.LongTermMemory.TokenCount,
			"avg_importance": stats.LongTermMemory.AvgImportance,
			"by_category":    stats.LongTermMemory.ByCategory,
			"by_importance":  stats.LongTermMemory.ByImportance,
		},
	})
}

// HandleMemoryPolicyGet returns retention policy for a tier
func (s *Service) HandleMemoryPolicyGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	tier := MemoryTier(toString(args["tier"]))
	if tier == "" {
		return mcp.ErrorResult(fmt.Errorf("tier is required")), nil
	}

	policy := s.memoryHierarchy.GetRetentionPolicy(tier)
	if policy == nil {
		return mcp.ErrorResult(fmt.Errorf("no policy for tier: %s", tier)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok": true,
		"policy": map[string]any{
			"id":                     policy.ID,
			"name":                   policy.Name,
			"tier":                   string(policy.Tier),
			"default_ttl_hours":      policy.DefaultTTL,
			"compress_after_hours":   policy.CompressAfterHours,
			"compression_ratio":      policy.CompressionRatio,
			"merge_threshold":        policy.MergeThreshold,
			"promotion_threshold":    policy.PromotionThreshold,
			"demotion_threshold":     policy.DemotionThreshold,
			"access_count_threshold": policy.AccessCountThreshold,
			"max_items":              policy.MaxItems,
			"max_tokens":             policy.MaxTokens,
			"dedupe_enabled":         policy.DedupeEnabled,
			"dedupe_similarity":      policy.DedupeSimilarity,
		},
	})
}

// HandleMemoryPolicySet updates retention policy for a tier
func (s *Service) HandleMemoryPolicySet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	tier := MemoryTier(toString(args["tier"]))
	if tier == "" {
		return mcp.ErrorResult(fmt.Errorf("tier is required")), nil
	}

	// Get existing policy or create new
	policy := s.memoryHierarchy.GetRetentionPolicy(tier)
	if policy == nil {
		policy = &RetentionPolicy{
			ID:   fmt.Sprintf("custom-%s", tier),
			Tier: tier,
		}
	}

	// Update fields if provided
	if name := toString(args["name"]); name != "" {
		policy.Name = name
	}
	if ttl := toInt(args["default_ttl_hours"]); ttl > 0 {
		policy.DefaultTTL = ttl
	}
	if compress := toInt(args["compress_after_hours"]); compress > 0 {
		policy.CompressAfterHours = compress
	}
	if ratio := toFloat(args["compression_ratio"]); ratio > 0 {
		policy.CompressionRatio = ratio
	}
	if merge := toFloat(args["merge_threshold"]); merge > 0 {
		policy.MergeThreshold = merge
	}
	if promo := toFloat(args["promotion_threshold"]); promo > 0 {
		policy.PromotionThreshold = promo
	}
	if demo := toFloat(args["demotion_threshold"]); demo > 0 {
		policy.DemotionThreshold = demo
	}
	if access := toInt(args["access_count_threshold"]); access > 0 {
		policy.AccessCountThreshold = access
	}
	if maxItems := toInt(args["max_items"]); maxItems > 0 {
		policy.MaxItems = maxItems
	}
	if maxTokens := toInt(args["max_tokens"]); maxTokens > 0 {
		policy.MaxTokens = maxTokens
	}
	if _, ok := args["dedupe_enabled"]; ok {
		policy.DedupeEnabled = getBool(args["dedupe_enabled"], true)
	}
	if sim := toFloat(args["dedupe_similarity"]); sim > 0 {
		policy.DedupeSimilarity = sim
	}

	s.memoryHierarchy.SetRetentionPolicy(policy)

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"tier":    string(tier),
		"message": "Retention policy updated",
	})
}

// Helper function for memory items
func memoryItemToMap(item *MemoryItem) map[string]any {
	m := map[string]any{
		"id":               item.ID,
		"tier":             string(item.Tier),
		"status":           string(item.Status),
		"importance":       string(item.Importance),
		"importance_score": item.ImportanceScore,
		"title":            item.Title,
		"content":          item.Content,
		"category":         item.Category,
		"original_tokens":  item.OriginalTokens,
		"access_count":     item.AccessCount,
		"created_at":       item.CreatedAt.Format(time.RFC3339Nano),
		"last_accessed_at": item.LastAccessedAt.Format(time.RFC3339Nano),
	}

	if item.Summary != "" {
		m["summary"] = item.Summary
		m["compressed_tokens"] = item.CompressedTokens
	}
	if item.Namespace != "" {
		m["namespace"] = item.Namespace
	}
	if item.SessionID != "" {
		m["session_id"] = item.SessionID
	}
	if item.AgentID != "" {
		m["agent_id"] = item.AgentID
	}
	if len(item.Tags) > 0 {
		m["tags"] = item.Tags
	}
	if item.Metadata != nil {
		m["metadata"] = item.Metadata
	}
	if len(item.RelatedIDs) > 0 {
		m["related_ids"] = item.RelatedIDs
	}
	if item.ExpiresAt != nil {
		m["expires_at"] = item.ExpiresAt.Format(time.RFC3339Nano)
	}
	if item.CompressedAt != nil {
		m["compressed_at"] = item.CompressedAt.Format(time.RFC3339Nano)
	}

	return m
}

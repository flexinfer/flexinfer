package agentcontext

import (
	"context"
	"fmt"
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

	// Persist to Qdrant (sessions collection doesn't need vectors)
	if err := s.persistSession(ctx, session); err != nil {
		// Log but don't fail - session is in memory
		fmt.Printf("Warning: failed to persist session: %v\n", err)
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"agent_id":   agentID,
		"namespace":  namespace,
		"started_at": session.StartedAt.Format(time.RFC3339),
	})
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

	// Persist updated session
	if err := s.persistSession(ctx, session); err != nil {
		fmt.Printf("Warning: failed to persist session end: %v\n", err)
	}

	result := map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"ended_at":   now.Format(time.RFC3339),
		"summarized": false,
	}

	// Optionally generate summary
	if summarize && s.cfg.AutoSummarize {
		if err := s.generateSummary(ctx, session); err != nil {
			result["summary_error"] = err.Error()
		} else {
			result["summarized"] = true
			session.Status = string(SessionStatusSummarized)
			s.persistSession(ctx, session)
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
	_ = s.persistSession(ctx, session)

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
				ID:        task.ID,
				AgentID:   task.AgentID,
				SessionID: task.SessionID,
				EntryType: EntryTypeTask,
				Title:     fmt.Sprintf("[%s] %s", task.Priority, task.Title),
				Content:   task.Context,
				FilePath:  task.FilePath,
				Timestamp: task.CreatedAt,
				Tags:      task.Tags,
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
				ID:             ann.ID,
				AgentID:        ann.AgentID,
				SessionID:      ann.SessionID,
				EntryType:      EntryTypeAnnotation,
				Title:          fmt.Sprintf("[%s] %s:%d", ann.AnnotationType, ann.FilePath, ann.LineStart),
				Content:        ann.Content,
				FilePath:       ann.FilePath,
				LineStart:      ann.LineStart,
				LineEnd:        ann.LineEnd,
				Timestamp:      ann.CreatedAt,
				TokenCount:     ann.TokenCount,
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

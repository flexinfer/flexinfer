package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/validate"
)

// ContextSvc manages context entries, annotations, search, and summary generation.
type ContextSvc struct {
	qdrant                   *QdrantRegistry
	embed                    embed.Embedder
	vectorSize               *int // shared mutable — pointer to Service.vectorSize
	cfg                      Config
	logger                   *slog.Logger
	metrics                  *Metrics
	persistedMemoryHierarchy *persistedMemoryHierarchy
	knowledgeGraph           *KnowledgeGraph

	// Cross-domain callbacks (wired by Service).
	getSession     func(ctx context.Context, sessionID string) (*Session, error)
	persistSession func(ctx context.Context, session *Session) error

	// Session state callbacks — SessionSvc owns the mutex.
	addSessionEntryStats  func(session *Session, entries int, tokens int)
	readSessionStats      func(session *Session) (entryCount, totalTokens int, lastSummary *time.Time)
	markSessionSummarized func(session *Session, t time.Time)
}

// NewContextSvc creates a new ContextSvc.
func NewContextSvc(qdrant *QdrantRegistry, embedr embed.Embedder, vectorSize *int, cfg Config, logger *slog.Logger, metrics *Metrics) *ContextSvc {
	return &ContextSvc{
		qdrant:     qdrant,
		embed:      embedr,
		vectorSize: vectorSize,
		cfg:        cfg,
		logger:     logger,
		metrics:    metrics,
	}
}

// --- Context CRUD ---

func (cs *ContextSvc) Add(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	entriesRaw := v.RequiredAny("entries")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := cs.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	entriesArr, ok := entriesRaw.([]any)
	if !ok || len(entriesArr) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entries array is required")), nil
	}

	type parsedEntry struct {
		raw            map[string]any
		durability     Durability
		title          string
		content        string
		mirrorToMemory bool
	}
	var parsed []parsedEntry

	for _, raw := range entriesArr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title := toString(m["title"])
		content := toString(m["content"])
		if title == "" || content == "" {
			continue
		}
		durabilityRaw := strings.TrimSpace(toString(m["durability"]))
		dur := Durability(durabilityRaw)
		if dur == "" {
			dur = DurabilitySession
		}
		parsed = append(parsed, parsedEntry{
			raw:            m,
			durability:     dur,
			title:          title,
			content:        content,
			mirrorToMemory: durabilityRaw == "" && shouldAutoMirrorToMemory(EntryType(strings.TrimSpace(toString(m["entry_type"])))),
		})
	}

	if len(parsed) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no valid entries provided")), nil
	}

	var allIDs []string
	routedCounts := map[string]int{}

	var contextEntries []ContextEntry
	var embedTexts []string
	for _, p := range parsed {
		if p.durability != DurabilitySession {
			continue
		}
		entry := cs.buildContextEntry(session, p.raw, p.title, p.content)
		contextEntries = append(contextEntries, entry)
		embedTexts = append(embedTexts, entry.Title+"\n"+entry.Content)
	}

	if len(contextEntries) > 0 {
		if strings.TrimSpace(cs.cfg.EmbedAPIKey) == "" {
			return mcp.ErrorResult(fmt.Errorf("AGENT_CONTEXT_EMBED_API_KEY (or MORPH_API_KEY / OPENAI_API_KEY) is not set")), nil
		}
		ids, err := cs.storeContextEntries(ctx, contextEntries, embedTexts)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		allIDs = append(allIDs, ids...)
		routedCounts["context"] = len(ids)
	}

	for _, p := range parsed {
		if p.durability != DurabilityPersistent && !p.mirrorToMemory {
			continue
		}
		id, err := cs.routeToMemory(ctx, session, p.raw, p.title, p.content)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("memory store: %w", err)), nil
		}
		if !p.mirrorToMemory {
			allIDs = append(allIDs, id)
		}
		routedCounts["memory"]++
	}

	for _, p := range parsed {
		if p.durability != DurabilityGraph {
			continue
		}
		id, err := cs.routeToGraph(session, p.raw, p.title, p.content)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("graph store: %w", err)), nil
		}
		allIDs = append(allIDs, id)
		routedCounts["graph"]++
	}

	totalTokens := 0
	for _, p := range parsed {
		totalTokens += EstimateTokens(p.title + " " + p.content)
	}
	if cs.addSessionEntryStats != nil {
		cs.addSessionEntryStats(session, len(parsed), totalTokens)
	}
	if cs.persistSession != nil {
		if err := cs.persistSession(ctx, session); err != nil {
			cs.logger.Warn("persist session stats failed", "error", err)
		}
	}

	if cs.cfg.AutoSummarize {
		cs.maybeAutoSummarize(ctx, session)
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(parsed),
		"entry_ids": allIDs,
		"routed":    routedCounts,
	})
}

func shouldAutoMirrorToMemory(entryType EntryType) bool {
	switch entryType {
	case EntryTypeDecision, EntryTypeFinding, EntryTypeQuestion, EntryTypeSummary, EntryTypeError, EntryTypeHandoff:
		return true
	default:
		return false
	}
}

func (cs *ContextSvc) buildContextEntry(session *Session, m map[string]any, title, content string) ContextEntry {
	visibility := Visibility(toString(m["visibility"]))
	if visibility == "" {
		visibility = cs.cfg.DefaultVisibility
	}
	ts := time.Now()
	entry := ContextEntry{
		ID:            GenerateID(session.AgentID, session.ID, title+"\n"+content, ts),
		SchemaVersion: SchemaVersion,
		AgentID:       session.AgentID,
		SessionID:     session.ID,
		Namespace:     session.Namespace,
		EntryType:     EntryType(toString(m["entry_type"])),
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
	return entry
}

func (cs *ContextSvc) storeContextEntries(ctx context.Context, entries []ContextEntry, embedTexts []string) ([]string, error) {
	vectors, err := cs.embed.EmbedDocuments(ctx, embedTexts)
	if err != nil {
		return nil, fmt.Errorf("embedding entries: %w", err)
	}
	if len(vectors) != len(entries) {
		return nil, fmt.Errorf("embedding count mismatch: got %d want %d", len(vectors), len(entries))
	}

	for _, v := range vectors {
		if len(v) > 0 {
			*cs.vectorSize = len(v)
			break
		}
	}
	if *cs.vectorSize <= 0 {
		return nil, fmt.Errorf("unknown vector size (empty embeddings)")
	}

	if err := cs.qdrant.Get(CollContext).EnsureCollection(ctx, *cs.vectorSize); err != nil {
		return nil, fmt.Errorf("ensure collection: %w", err)
	}

	points := make([]Point, 0, len(entries))
	for i, entry := range entries {
		vector := vectors[i]
		if len(vector) > 0 && len(vector) != *cs.vectorSize {
			return nil, fmt.Errorf("embedding vector size mismatch: got %d want %d", len(vector), *cs.vectorSize)
		}
		if len(vector) == 0 {
			vector = make([]float64, *cs.vectorSize)
		}
		points = append(points, Point{
			ID:      entry.ID,
			Vector:  vector,
			Payload: EntryToPayload(entry, cs.cfg.EmbedModel),
		})
	}

	if err := cs.upsertBatched(ctx, cs.qdrant.Get(CollContext), points); err != nil {
		return nil, fmt.Errorf("upsert entries: %w", err)
	}

	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids, nil
}

func (cs *ContextSvc) routeToMemory(ctx context.Context, session *Session, m map[string]any, title, content string) (string, error) {
	if cs.persistedMemoryHierarchy == nil {
		return "", fmt.Errorf("memory hierarchy not available")
	}

	category := toString(m["entry_type"])
	if category == "" {
		category = "finding"
	}

	item := &MemoryItem{
		Tier:       MemoryTierShortTerm,
		Importance: ImportanceLevelMedium,
		Title:      title,
		Content:    content,
		Category:   category,
		Namespace:  session.Namespace,
		SessionID:  session.ID,
		AgentID:    session.AgentID,
		Tags:       toStringSlice(m["tags"]),
	}
	if metadata, ok := m["metadata"].(map[string]any); ok {
		item.Metadata = metadata
	}
	item.OriginalTokens = EstimateTokens(title + " " + content)

	if err := cs.persistedMemoryHierarchy.AddItemWithPersistence(ctx, item, nil); err != nil {
		return "", err
	}

	cs.metrics.ShortTermMemoryItems.Add(1)
	cs.metrics.ShortTermMemoryTokens.Add(int64(item.OriginalTokens))

	return item.ID, nil
}

func (cs *ContextSvc) routeToGraph(session *Session, m map[string]any, title, content string) (string, error) {
	if cs.knowledgeGraph == nil {
		return "", fmt.Errorf("knowledge graph not available")
	}

	entityType := EntityType(toString(m["entry_type"]))
	if entityType == "" {
		entityType = EntityTypeConcept
	}

	entity := &Entity{
		Type:        entityType,
		Name:        title,
		Description: content,
		Namespace:   session.Namespace,
		FilePath:    toString(m["file_path"]),
		LineStart:   toInt(m["line_start"]),
		LineEnd:     toInt(m["line_end"]),
		Language:    toString(m["language"]),
		SessionID:   session.ID,
		AgentID:     session.AgentID,
		Tags:        toStringSlice(m["tags"]),
	}

	if props, ok := m["metadata"].(map[string]any); ok {
		entity.Properties = props
	}

	if err := cs.knowledgeGraph.AddEntity(entity); err != nil {
		return "", err
	}

	cs.metrics.GraphEntitiesAdded.Add(1)
	return entity.ID, nil
}

func (cs *ContextSvc) Get(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ids := v.RequiredStringSlice("entry_ids")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	points, err := cs.qdrant.Get(CollContext).GetPoints(ctx, ids, false)
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

func (cs *ContextSvc) Delete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ids := v.RequiredStringSlice("entry_ids")
	confirm := v.Bool("confirm", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm=true required for deletion")), nil
	}

	if err := cs.qdrant.Get(CollContext).Delete(ctx, ids); err != nil {
		return mcp.ErrorResult(fmt.Errorf("delete entries: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": len(ids),
	})
}

// --- Search & Recall ---

func (cs *ContextSvc) Search(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, query)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding query: %w", err)), nil
	}

	searchStart := time.Now()
	results, err := cs.qdrant.Get(CollContext).Search(ctx, vector, filter, limit, includeContent)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search: %w", err)), nil
	}
	cs.metrics.RecordSearchLatency(time.Since(searchStart).Microseconds())
	cs.metrics.RecallRequests.Add(1)

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}

// --- Sharing ---

func (cs *ContextSvc) Share(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	entryIDs := v.RequiredStringSlice("entry_ids")
	targetAgents := v.RequiredStringSlice("target_agents")
	visibilityStr := v.String("visibility", string(VisibilityShared))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	visibility := Visibility(visibilityStr)

	updated := 0
	for _, id := range entryIDs {
		p, err := cs.qdrant.Get(CollContext).GetPoint(ctx, id, false)
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
		if err := cs.qdrant.Get(CollContext).SetPayload(ctx, []string{id}, payload, true); err == nil {
			updated++
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"updated": updated,
	})
}

func (cs *ContextSvc) QueryShared(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, query)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding query: %w", err)), nil
	}

	results, err := cs.qdrant.Get(CollContext).Search(ctx, vector, filter, limit, true)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}

// --- Summarization ---

func (cs *ContextSvc) Summarize(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := cs.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	if err := cs.GenerateSummary(ctx, session); err != nil {
		return mcp.ErrorResult(fmt.Errorf("generate summary: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"summarized": true,
	})
}

func (cs *ContextSvc) GenerateSummary(ctx context.Context, session *Session) error {
	entries, err := cs.qdrant.Get(CollContext).Scroll(ctx, FilterMust(
		Match("session_id", session.ID),
	), 1000)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

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
		Visibility:    cs.cfg.DefaultVisibility,
		Metadata: map[string]any{
			"key_findings":  findings,
			"key_decisions": decisions,
			"files_touched": files,
			"entry_count":   len(entries),
		},
	}

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, summaryEntry.Title+" "+summaryEntry.Content)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return err
	}
	if len(vector) > 0 {
		*cs.vectorSize = len(vector)
	}
	if *cs.vectorSize <= 0 {
		return fmt.Errorf("unknown vector size")
	}
	if err := cs.qdrant.Get(CollContext).EnsureCollection(ctx, *cs.vectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      summaryEntry.ID,
		Vector:  vector,
		Payload: EntryToPayload(summaryEntry, cs.cfg.EmbedModel),
	}

	if err := cs.qdrant.Get(CollContext).Upsert(ctx, []Point{point}, true); err != nil {
		return err
	}

	// Update session timestamp via callback
	if cs.markSessionSummarized != nil {
		cs.markSessionSummarized(session, time.Now())
	}

	return nil
}

func (cs *ContextSvc) maybeAutoSummarize(ctx context.Context, session *Session) {
	if cs.readSessionStats == nil {
		return
	}

	entryCount, totalTokens, lastSummary := cs.readSessionStats(session)

	shouldSummarize := false

	if entryCount >= cs.cfg.SummarizeEntryThreshold {
		if lastSummary == nil || entryCount >= cs.cfg.SummarizeEntryThreshold {
			shouldSummarize = true
		}
	}

	if totalTokens >= cs.cfg.SummarizeTokenThreshold {
		shouldSummarize = true
	}

	if lastSummary != nil {
		minutesSince := int(time.Since(*lastSummary).Minutes())
		if minutesSince >= cs.cfg.SummarizeMinuteThreshold && entryCount > 10 {
			shouldSummarize = true
		}
	}

	if shouldSummarize {
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := cs.GenerateSummary(bg, session); err != nil {
				cs.logger.Warn("auto-summarize failed",
					"session_id", session.ID,
					"error", err,
				)
			}
		}()
	}
}

// --- Stats ---

func (cs *ContextSvc) Stats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	count, err := cs.qdrant.Get(CollContext).Count(ctx, filter)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("count: %w", err)), nil
	}

	entries, _ := cs.qdrant.Get(CollContext).Scroll(ctx, filter, 1000)
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
		"metrics":      cs.metrics.Snapshot(),
	})
}

// --- Codebase Link ---

func (cs *ContextSvc) LinkCodebase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	session, err := cs.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

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
		Visibility:    cs.cfg.DefaultVisibility,
		Metadata: map[string]any{
			"repo_id": repoID,
			"symbol":  symbol,
		},
	}

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, entry.Title+" "+entry.Content)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding: %w", err)), nil
	}
	if len(vector) > 0 {
		*cs.vectorSize = len(vector)
	}
	if *cs.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}
	if err := cs.qdrant.Get(CollContext).EnsureCollection(ctx, *cs.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      entry.ID,
		Vector:  vector,
		Payload: EntryToPayload(entry, cs.cfg.EmbedModel),
	}

	if err := cs.qdrant.Get(CollContext).Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"entry_id": entry.ID,
	})
}

// --- Annotations ---

func (cs *ContextSvc) AnnotationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	filePath := v.Required("file_path")
	lineStart := v.RequiredInt("line_start")
	content := v.Required("content")
	annotationTypeStr := v.String("annotation_type", string(AnnotationTypeNote))
	lineEnd := v.Int("line_end", 0)
	symbol := v.String("symbol", "")
	repoID := v.String("repo_id", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := cs.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	annotationType := AnnotationType(annotationTypeStr)

	now := time.Now()
	annotation := CodeAnnotation{
		ID:             GenerateID(session.AgentID, sessionID, filePath+content, now),
		SessionID:      sessionID,
		AgentID:        session.AgentID,
		Namespace:      session.Namespace,
		FilePath:       filePath,
		LineStart:      lineStart,
		LineEnd:        lineEnd,
		Symbol:         symbol,
		RepoID:         repoID,
		AnnotationType: annotationType,
		Content:        content,
		CreatedAt:      now,
		UpdatedAt:      now,
		TokenCount:     EstimateTokens(content),
	}

	vector, err := cs.embed.EmbedQuery(ctx, annotation.Content)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding: %w", err)), nil
	}
	if len(vector) > 0 {
		*cs.vectorSize = len(vector)
	}
	if *cs.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}

	if err := cs.qdrant.Get(CollAnnotations).EnsureCollection(ctx, *cs.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      annotation.ID,
		Vector:  vector,
		Payload: annotationToPayload(annotation),
	}

	if err := cs.qdrant.Get(CollAnnotations).Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert annotation: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"annotation_id": annotation.ID,
	})
}

func (cs *ContextSvc) AnnotationsGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	filePath := v.String("file_path", "")
	agentID := v.String("agent_id", "")
	lineStart := v.Int("line_start", 0)
	lineEnd := v.Int("line_end", 0)
	annotationTypes := v.StringSlice("annotation_types")
	limit := v.Int("limit", 50)

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

	points, err := cs.qdrant.Get(CollAnnotations).ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get annotations: %w", err)), nil
	}

	annotations := make([]CodeAnnotation, 0, len(points))
	for _, p := range points {
		ann, err := payloadToAnnotation(p.Payload)
		if err != nil || ann == nil {
			continue
		}
		if lineStart > 0 && ann.LineStart < lineStart {
			continue
		}
		if lineEnd > 0 && ann.LineStart > lineEnd {
			continue
		}
		annotations = append(annotations, *ann)
	}

	sort.Slice(annotations, func(i, j int) bool {
		return annotations[i].LineStart < annotations[j].LineStart
	})

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"annotations": annotations,
		"count":       len(annotations),
	})
}

func (cs *ContextSvc) GetAnnotationsForFile(ctx context.Context, agentID, filePath string, limit int) ([]CodeAnnotation, error) {
	var conds []any
	conds = append(conds, Match("file_path", filePath))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	points, err := cs.qdrant.Get(CollAnnotations).ScrollPoints(ctx, FilterMust(conds...), limit, false)
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

// --- Internal helpers ---

func (cs *ContextSvc) getRecentByType(ctx context.Context, agentID, sessionID string, entryType EntryType, limit int) ([]ContextEntry, error) {
	var conds []any
	conds = append(conds, Match("entry_type", string(entryType)))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}

	entries, err := cs.qdrant.Get(CollContext).Scroll(ctx, FilterMust(conds...), limit*2)
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (cs *ContextSvc) getEntriesForFile(ctx context.Context, agentID, filePath string, limit int) ([]ContextEntry, error) {
	var conds []any
	conds = append(conds, Match("file_path", filePath))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	return cs.qdrant.Get(CollContext).Scroll(ctx, FilterMust(conds...), limit)
}

func (cs *ContextSvc) upsertBatched(ctx context.Context, q *QdrantClient, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	batchSize := cs.cfg.UpsertBatchSize
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

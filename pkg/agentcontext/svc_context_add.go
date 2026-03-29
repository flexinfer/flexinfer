package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

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

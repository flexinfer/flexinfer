// qdrant_convert.go -- Type conversion utilities and Qdrant payload serialization/deserialization.
package agentcontext

import (
	"encoding/json"
	"fmt"
	"time"
)

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func toBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func toMapStringAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// Payload conversion -- ContextEntry

func EntryToPayload(e ContextEntry, embedModel string) map[string]any {
	payload := map[string]any{
		"_record_type":   "entry", // discriminator for shared collection (SIMP-12)
		"id":             e.ID,
		"schema_version": e.SchemaVersion,
		"agent_id":       e.AgentID,
		"session_id":     e.SessionID,
		"namespace":      e.Namespace,
		"entry_type":     string(e.EntryType),
		"timestamp":      e.Timestamp.Format(time.RFC3339Nano),
		"title":          e.Title,
		"content":        e.Content,
		"content_hash":   e.ContentHash,
		"file_path":      e.FilePath,
		"line_start":     e.LineStart,
		"line_end":       e.LineEnd,
		"parent_id":      e.ParentID,
		"related_ids":    e.RelatedIDs,
		"tags":           e.Tags,
		"token_count":    e.TokenCount,
		"visibility":     string(e.Visibility),
		"shared_with":    e.SharedWith,
		"embed_model":    embedModel,
	}
	if e.Metadata != nil {
		payload["metadata"] = e.Metadata
	}
	// Source versioning (Phase 2.1)
	if e.SourceVersion != nil {
		sv := map[string]any{
			"indexed_at": e.SourceVersion.IndexedAt.Format(time.RFC3339Nano),
			"is_stale":   e.SourceVersion.IsStale,
		}
		if e.SourceVersion.CommitHash != "" {
			sv["commit_hash"] = e.SourceVersion.CommitHash
		}
		if !e.SourceVersion.FileMtime.IsZero() {
			sv["file_mtime"] = e.SourceVersion.FileMtime.Format(time.RFC3339Nano)
		}
		payload["source_version"] = sv
	}
	return payload
}

func PayloadToEntry(payload map[string]any) (*ContextEntry, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	entry := &ContextEntry{
		ID:            toString(payload["id"]),
		SchemaVersion: toString(payload["schema_version"]),
		AgentID:       toString(payload["agent_id"]),
		SessionID:     toString(payload["session_id"]),
		Namespace:     toString(payload["namespace"]),
		EntryType:     EntryType(toString(payload["entry_type"])),
		Title:         toString(payload["title"]),
		Content:       toString(payload["content"]),
		ContentHash:   toString(payload["content_hash"]),
		FilePath:      toString(payload["file_path"]),
		LineStart:     toInt(payload["line_start"]),
		LineEnd:       toInt(payload["line_end"]),
		ParentID:      toString(payload["parent_id"]),
		RelatedIDs:    toStringSlice(payload["related_ids"]),
		Tags:          toStringSlice(payload["tags"]),
		TokenCount:    toInt(payload["token_count"]),
		Visibility:    Visibility(toString(payload["visibility"])),
		SharedWith:    toStringSlice(payload["shared_with"]),
	}

	if ts := toString(payload["timestamp"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			entry.Timestamp = t
		}
	}

	if m, ok := payload["metadata"].(map[string]any); ok {
		entry.Metadata = m
	}

	// Parse source version (Phase 2.1)
	if sv, ok := payload["source_version"].(map[string]any); ok {
		entry.SourceVersion = &SourceVersion{
			CommitHash: toString(sv["commit_hash"]),
			IsStale:    toBool(sv["is_stale"]),
		}
		if ts := toString(sv["indexed_at"]); ts != "" {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				entry.SourceVersion.IndexedAt = t
			}
		}
		if ts := toString(sv["file_mtime"]); ts != "" {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				entry.SourceVersion.FileMtime = t
			}
		}
	}

	return entry, nil
}

// Payload conversion -- Session

func SessionToPayload(s Session) map[string]any {
	payload := map[string]any{
		"id":           s.ID,
		"agent_id":     s.AgentID,
		"namespace":    s.Namespace,
		"project":      canonicalProject(s.Project, s.Namespace, s.PipelineRef),
		"started_at":   s.StartedAt.Format(time.RFC3339Nano),
		"status":       s.Status,
		"description":  s.Description,
		"working_dir":  s.WorkingDir,
		"entry_count":  s.EntryCount,
		"total_tokens": s.TotalTokens,
	}
	if s.PipelineRef != nil {
		payload["pipeline_ref"] = pipelineRefToPayload(s.PipelineRef)
	}
	if s.EndedAt != nil {
		payload["ended_at"] = s.EndedAt.Format(time.RFC3339Nano)
	}
	if s.LastSummaryAt != nil {
		payload["last_summary_at"] = s.LastSummaryAt.Format(time.RFC3339Nano)
	}
	return payload
}

func PayloadToSession(payload map[string]any) (*Session, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	session := &Session{
		ID:          toString(payload["id"]),
		AgentID:     toString(payload["agent_id"]),
		Namespace:   toString(payload["namespace"]),
		Project:     toString(payload["project"]),
		Status:      toString(payload["status"]),
		Description: toString(payload["description"]),
		WorkingDir:  toString(payload["working_dir"]),
		EntryCount:  toInt(payload["entry_count"]),
		TotalTokens: toInt(payload["total_tokens"]),
		PipelineRef: pipelineRefFromValue(payload["pipeline_ref"]),
	}
	session.Project = canonicalProject(session.Project, session.Namespace, session.PipelineRef)

	if ts := toString(payload["started_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			session.StartedAt = t
		}
	}
	if ts := toString(payload["ended_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			session.EndedAt = &t
		}
	}
	if ts := toString(payload["last_summary_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			session.LastSummaryAt = &t
		}
	}

	return session, nil
}

// Payload conversion -- Entity

func EntityToPayload(e Entity, embedModel string) map[string]any {
	payload := map[string]any{
		"id":          e.ID,
		"type":        string(e.Type),
		"name":        e.Name,
		"description": e.Description,
		"namespace":   e.Namespace,
		"file_path":   e.FilePath,
		"line_start":  e.LineStart,
		"line_end":    e.LineEnd,
		"language":    e.Language,
		"signature":   e.Signature,
		"session_id":  e.SessionID,
		"agent_id":    e.AgentID,
		"created_at":  e.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":  e.UpdatedAt.Format(time.RFC3339Nano),
		"tags":        e.Tags,
		"embed_model": embedModel,
	}
	if e.Properties != nil {
		payload["properties"] = e.Properties
	}
	return payload
}

func PayloadToEntity(payload map[string]any) (*Entity, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	entity := &Entity{
		ID:          toString(payload["id"]),
		Type:        EntityType(toString(payload["type"])),
		Name:        toString(payload["name"]),
		Description: toString(payload["description"]),
		Namespace:   toString(payload["namespace"]),
		FilePath:    toString(payload["file_path"]),
		LineStart:   toInt(payload["line_start"]),
		LineEnd:     toInt(payload["line_end"]),
		Language:    toString(payload["language"]),
		Signature:   toString(payload["signature"]),
		SessionID:   toString(payload["session_id"]),
		AgentID:     toString(payload["agent_id"]),
		Tags:        toStringSlice(payload["tags"]),
		Properties:  toMapStringAny(payload["properties"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			entity.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			entity.UpdatedAt = t
		}
	}

	return entity, nil
}

// Payload conversion -- Relation

func RelationToPayload(r Relation) map[string]any {
	payload := map[string]any{
		"id":            r.ID,
		"type":          string(r.Type),
		"source_id":     r.SourceID,
		"target_id":     r.TargetID,
		"weight":        r.Weight,
		"bidirectional": r.Bidirectional,
		"evidence":      r.Evidence,
		"reasoning":     r.Reasoning,
		"session_id":    r.SessionID,
		"agent_id":      r.AgentID,
		"created_at":    r.CreatedAt.Format(time.RFC3339Nano),
	}
	if r.Properties != nil {
		payload["properties"] = r.Properties
	}
	return payload
}

func PayloadToRelation(payload map[string]any) (*Relation, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	rel := &Relation{
		ID:            toString(payload["id"]),
		Type:          RelationType(toString(payload["type"])),
		SourceID:      toString(payload["source_id"]),
		TargetID:      toString(payload["target_id"]),
		Weight:        toFloat64(payload["weight"]),
		Bidirectional: toBool(payload["bidirectional"]),
		Evidence:      toString(payload["evidence"]),
		Reasoning:     toString(payload["reasoning"]),
		SessionID:     toString(payload["session_id"]),
		AgentID:       toString(payload["agent_id"]),
		Properties:    toMapStringAny(payload["properties"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			rel.CreatedAt = t
		}
	}

	return rel, nil
}

// Payload conversion -- MemoryItem

func MemoryItemToPayload(m MemoryItem, embedModel string) map[string]any {
	payload := map[string]any{
		"id":                m.ID,
		"tier":              string(m.Tier),
		"status":            string(m.Status),
		"importance":        string(m.Importance),
		"importance_score":  m.ImportanceScore,
		"title":             m.Title,
		"content":           m.Content,
		"summary":           m.Summary,
		"source_entry_id":   m.SourceEntryID,
		"source_type":       string(m.SourceType),
		"category":          m.Category,
		"tags":              m.Tags,
		"namespace":         m.Namespace,
		"session_id":        m.SessionID,
		"agent_id":          m.AgentID,
		"created_at":        m.CreatedAt.Format(time.RFC3339Nano),
		"last_accessed_at":  m.LastAccessedAt.Format(time.RFC3339Nano),
		"access_count":      m.AccessCount,
		"original_tokens":   m.OriginalTokens,
		"compressed_tokens": m.CompressedTokens,
		"related_ids":       m.RelatedIDs,
		"parent_id":         m.ParentID,
		"child_ids":         m.ChildIDs,
		"embed_model":       embedModel,
	}
	if m.ExpiresAt != nil {
		payload["expires_at"] = m.ExpiresAt.Format(time.RFC3339Nano)
	}
	if m.CompressedAt != nil {
		payload["compressed_at"] = m.CompressedAt.Format(time.RFC3339Nano)
	}
	if m.ArchivedAt != nil {
		payload["archived_at"] = m.ArchivedAt.Format(time.RFC3339Nano)
	}
	if m.Metadata != nil {
		payload["metadata"] = m.Metadata
	}
	return payload
}

func PayloadToMemoryItem(payload map[string]any) (*MemoryItem, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	item := &MemoryItem{
		ID:               toString(payload["id"]),
		Tier:             MemoryTier(toString(payload["tier"])),
		Status:           MemoryItemStatus(toString(payload["status"])),
		Importance:       ImportanceLevel(toString(payload["importance"])),
		ImportanceScore:  toFloat64(payload["importance_score"]),
		Title:            toString(payload["title"]),
		Content:          toString(payload["content"]),
		Summary:          toString(payload["summary"]),
		SourceEntryID:    toString(payload["source_entry_id"]),
		SourceType:       EntryType(toString(payload["source_type"])),
		Category:         toString(payload["category"]),
		Tags:             toStringSlice(payload["tags"]),
		Namespace:        toString(payload["namespace"]),
		SessionID:        toString(payload["session_id"]),
		AgentID:          toString(payload["agent_id"]),
		AccessCount:      toInt(payload["access_count"]),
		OriginalTokens:   toInt(payload["original_tokens"]),
		CompressedTokens: toInt(payload["compressed_tokens"]),
		RelatedIDs:       toStringSlice(payload["related_ids"]),
		ParentID:         toString(payload["parent_id"]),
		ChildIDs:         toStringSlice(payload["child_ids"]),
		Metadata:         toMapStringAny(payload["metadata"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.CreatedAt = t
		}
	}
	if ts := toString(payload["last_accessed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.LastAccessedAt = t
		}
	}
	if ts := toString(payload["expires_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.ExpiresAt = &t
		}
	}
	if ts := toString(payload["compressed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.CompressedAt = &t
		}
	}
	if ts := toString(payload["archived_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.ArchivedAt = &t
		}
	}

	return item, nil
}

// Payload conversion -- Workflow

func WorkflowToPayload(wf Workflow) map[string]any {
	payload := map[string]any{
		"id":              wf.ID,
		"definition_id":   wf.DefinitionID,
		"session_id":      wf.SessionID,
		"agent_id":        wf.AgentID,
		"namespace":       wf.Namespace,
		"status":          string(wf.Status),
		"current_step":    wf.CurrentStep,
		"error":           wf.Error,
		"failed_step_id":  wf.FailedStepID,
		"created_at":      wf.CreatedAt.Format(time.RFC3339Nano),
		"total_steps":     wf.TotalSteps,
		"completed_steps": wf.CompletedSteps,
		"failed_steps":    wf.FailedSteps,
	}
	if wf.StartedAt != nil {
		payload["started_at"] = wf.StartedAt.Format(time.RFC3339Nano)
	}
	if wf.CompletedAt != nil {
		payload["completed_at"] = wf.CompletedAt.Format(time.RFC3339Nano)
	}
	if wf.Input != nil {
		payload["input"] = wf.Input
	}
	if wf.Output != nil {
		payload["output"] = wf.Output
	}
	if wf.Context != nil {
		payload["context"] = wf.Context
	}
	// Store definition and step states as JSON
	if defBytes, err := json.Marshal(wf.Definition); err == nil {
		payload["definition_json"] = string(defBytes)
	}
	if statesBytes, err := json.Marshal(wf.StepStates); err == nil {
		payload["step_states_json"] = string(statesBytes)
	}
	return payload
}

func PayloadToWorkflow(payload map[string]any) (*Workflow, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	wf := &Workflow{
		ID:             toString(payload["id"]),
		DefinitionID:   toString(payload["definition_id"]),
		SessionID:      toString(payload["session_id"]),
		AgentID:        toString(payload["agent_id"]),
		Namespace:      toString(payload["namespace"]),
		Status:         WorkflowStatus(toString(payload["status"])),
		CurrentStep:    toString(payload["current_step"]),
		Error:          toString(payload["error"]),
		FailedStepID:   toString(payload["failed_step_id"]),
		TotalSteps:     toInt(payload["total_steps"]),
		CompletedSteps: toInt(payload["completed_steps"]),
		FailedSteps:    toInt(payload["failed_steps"]),
		Input:          toMapStringAny(payload["input"]),
		Output:         toMapStringAny(payload["output"]),
		Context:        toMapStringAny(payload["context"]),
		StepStates:     make(map[string]*WorkflowStep),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			wf.CreatedAt = t
		}
	}
	if ts := toString(payload["started_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			wf.StartedAt = &t
		}
	}
	if ts := toString(payload["completed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			wf.CompletedAt = &t
		}
	}

	// Parse definition from JSON
	if defJSON := toString(payload["definition_json"]); defJSON != "" {
		if err := json.Unmarshal([]byte(defJSON), &wf.Definition); err != nil {
			return nil, fmt.Errorf("parse definition_json: %w", err)
		}
	}

	// Parse step states from JSON
	if statesJSON := toString(payload["step_states_json"]); statesJSON != "" {
		if err := json.Unmarshal([]byte(statesJSON), &wf.StepStates); err != nil {
			return nil, fmt.Errorf("parse step_states_json: %w", err)
		}
	}

	return wf, nil
}

// Payload conversion -- WorkflowDefinition

func WorkflowDefinitionToPayload(def WorkflowDefinition) map[string]any {
	payload := map[string]any{
		"id":                  def.ID,
		"name":                def.Name,
		"description":         def.Description,
		"version":             def.Version,
		"namespace":           def.Namespace,
		"created_by":          def.CreatedBy,
		"timeout_seconds":     def.TimeoutSeconds,
		"rollback_on_failure": def.RollbackOnFailure,
		"created_at":          def.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":          def.UpdatedAt.Format(time.RFC3339Nano),
	}
	// Store steps and input schema as JSON
	if stepsBytes, err := json.Marshal(def.Steps); err == nil {
		payload["steps_json"] = string(stepsBytes)
	}
	if def.InputSchema != nil {
		if schemaBytes, err := json.Marshal(def.InputSchema); err == nil {
			payload["input_schema_json"] = string(schemaBytes)
		}
	}
	return payload
}

func PayloadToWorkflowDefinition(payload map[string]any) (*WorkflowDefinition, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	def := &WorkflowDefinition{
		ID:                toString(payload["id"]),
		Name:              toString(payload["name"]),
		Description:       toString(payload["description"]),
		Version:           toString(payload["version"]),
		Namespace:         toString(payload["namespace"]),
		CreatedBy:         toString(payload["created_by"]),
		TimeoutSeconds:    toInt(payload["timeout_seconds"]),
		RollbackOnFailure: toBool(payload["rollback_on_failure"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			def.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			def.UpdatedAt = t
		}
	}

	// Parse steps from JSON
	if stepsJSON := toString(payload["steps_json"]); stepsJSON != "" {
		if err := json.Unmarshal([]byte(stepsJSON), &def.Steps); err != nil {
			return nil, fmt.Errorf("parse steps_json: %w", err)
		}
	}

	// Parse input schema from JSON
	if schemaJSON := toString(payload["input_schema_json"]); schemaJSON != "" {
		if err := json.Unmarshal([]byte(schemaJSON), &def.InputSchema); err != nil {
			return nil, fmt.Errorf("parse input_schema_json: %w", err)
		}
	}

	return def, nil
}

// Payload conversion -- ReasoningChain

func ReasoningChainToPayload(rc ReasoningChain) map[string]any {
	payload := map[string]any{
		"id":         rc.ID,
		"query":      rc.Query,
		"conclusion": rc.Conclusion,
		"confidence": rc.Confidence,
		"session_id": rc.SessionID,
		"agent_id":   rc.AgentID,
		"created_at": rc.CreatedAt.Format(time.RFC3339Nano),
	}
	if stepsBytes, err := json.Marshal(rc.Steps); err == nil {
		payload["steps_json"] = string(stepsBytes)
	}
	return payload
}

func PayloadToReasoningChain(payload map[string]any) (*ReasoningChain, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	rc := &ReasoningChain{
		ID:         toString(payload["id"]),
		Query:      toString(payload["query"]),
		Conclusion: toString(payload["conclusion"]),
		Confidence: toFloat64(payload["confidence"]),
		SessionID:  toString(payload["session_id"]),
		AgentID:    toString(payload["agent_id"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			rc.CreatedAt = t
		}
	}

	if stepsJSON := toString(payload["steps_json"]); stepsJSON != "" {
		if err := json.Unmarshal([]byte(stepsJSON), &rc.Steps); err != nil {
			return nil, fmt.Errorf("parse steps_json: %w", err)
		}
	}

	return rc, nil
}

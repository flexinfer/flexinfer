package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleHandoffCreate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	targetAgentID := v.Required("target_agent_id")
	handoffTypeStr := v.String("handoff_type", string(HandoffTypeSummaryOnly))
	instructions := v.String("instructions", "")
	entryIDs := v.StringSlice("entry_ids")
	tokenBudget := v.Int("token_budget", s.cfg.HandoffMaxTokens)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	handoffType := HandoffType(handoffTypeStr)

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
	v := validate.NewArgs(args)
	handoffID := v.Required("handoff_id")
	sessionID := v.Required("session_id")
	importEntries := v.Bool("import_entries", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

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

// HandleHandoffInbox lists pending handoffs for an agent
func (s *Service) HandleHandoffInbox(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	includeViewed := v.Bool("include_viewed", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Query handoffs targeted at this agent
	conds := []any{Match("target_agent_id", agentID)}
	if !includeViewed {
		conds = append(conds, Match("status", string(HandoffStatusPending)))
	}

	points, err := s.handoffsQdrant.ScrollPoints(ctx, FilterMust(conds...), 50, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("query inbox: %w", err)), nil
	}

	now := time.Now()
	var handoffs []map[string]any

	for _, p := range points {
		h, err := payloadToHandoff(p.Payload)
		if err != nil || h == nil {
			continue
		}

		// Skip expired
		if h.ExpiresAt != nil && now.After(*h.ExpiresAt) {
			continue
		}

		// Include pending and optionally viewed
		if h.Status != HandoffStatusPending && h.Status != HandoffStatusViewed {
			if !includeViewed {
				continue
			}
		}

		entry := map[string]any{
			"handoff_id":   h.ID,
			"source_agent": h.SourceAgentID,
			"status":       string(h.Status),
			"instructions": h.Instructions,
			"summary":      h.Summary,
			"token_count":  h.TokenCount,
			"entry_count":  len(h.EntryIDs),
			"created_at":   h.CreatedAt.Format(time.RFC3339),
		}
		if h.ExpiresAt != nil {
			entry["expires_at"] = h.ExpiresAt.Format(time.RFC3339)
		}
		handoffs = append(handoffs, entry)

		// Mark as viewed if pending
		if h.Status == HandoffStatusPending {
			viewedAt := now.Format(time.RFC3339Nano)
			if err := s.handoffsQdrant.SetPayload(ctx, []string{h.ID}, map[string]any{
				"status":    string(HandoffStatusViewed),
				"viewed_at": viewedAt,
			}, true); err != nil {
				s.logger.Warn("failed to mark handoff as viewed", "handoff_id", h.ID, "error", err)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"agent_id": agentID,
		"handoffs": handoffs,
		"count":    len(handoffs),
	})
}

// HandleHandoffReject rejects a handoff with a reason
func (s *Service) HandleHandoffReject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	handoffID := v.Required("handoff_id")
	reason := v.String("reason", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get the handoff
	p, err := s.handoffsQdrant.GetPoint(ctx, handoffID, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("handoff %s not found", handoffID)), nil
	}

	h, err := payloadToHandoff(p.Payload)
	if err != nil || h == nil {
		return mcp.ErrorResult(fmt.Errorf("invalid handoff")), nil
	}

	// Only reject if pending or viewed
	if h.Status != HandoffStatusPending && h.Status != HandoffStatusViewed {
		return mcp.ErrorResult(fmt.Errorf("handoff status is %s, cannot reject", h.Status)), nil
	}

	now := time.Now()
	if err := s.handoffsQdrant.SetPayload(ctx, []string{handoffID}, map[string]any{
		"status":          string(HandoffStatusRejected),
		"rejected_at":     now.Format(time.RFC3339Nano),
		"rejected_reason": reason,
	}, true); err != nil {
		s.logger.Warn("failed to persist handoff rejection", "handoff_id", handoffID, "error", err)
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"handoff_id": handoffID,
		"status":     string(HandoffStatusRejected),
		"reason":     reason,
	})
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
	if h.ViewedAt != nil {
		payload["viewed_at"] = h.ViewedAt.Format(time.RFC3339Nano)
	}
	if h.RejectedAt != nil {
		payload["rejected_at"] = h.RejectedAt.Format(time.RFC3339Nano)
	}
	if h.RejectedReason != "" {
		payload["rejected_reason"] = h.RejectedReason
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
	if ts := toString(payload["viewed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			h.ViewedAt = &t
		}
	}
	if ts := toString(payload["rejected_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			h.RejectedAt = &t
		}
	}
	h.RejectedReason = toString(payload["rejected_reason"])

	return h, nil
}

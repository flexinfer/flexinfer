package agentcontext

import (
	"context"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Session handlers — thin delegation to SessionSvc.

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
func (s *Service) runSessionSummaryAsync(session *Session) {
	bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := s.ctxSvc.GenerateSummary(bg, session); err != nil {
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

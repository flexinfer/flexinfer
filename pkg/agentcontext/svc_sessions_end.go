package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// End marks a session as ended and optionally generates a summary.
func (ss *SessionSvc) End(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	summarize := v.Bool("summarize", true)
	summaryAsync := v.Bool("summary_async", false)
	cleanup := v.Bool("cleanup", true)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := ss.Get(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}
	now := time.Now()
	session.EndedAt = &now
	session.Status = string(SessionStatusEnded)
	ss.mu.Lock()
	ss.sessions[sessionID] = session
	ss.mu.Unlock()
	ss.metrics.SessionsActive.Add(-1)

	result := map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"ended_at":   now.Format(time.RFC3339),
		"summarized": false,
	}

	if err := ss.Persist(ctx, session); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist session end: %v", err)
	}

	// Optionally generate summary
	if summarize && ss.cfg.AutoSummarize {
		if summaryAsync {
			result["summary_queued"] = true
			if ss.runSummaryAsync != nil {
				go ss.runSummaryAsync(session)
			}
		} else if ss.generateSummary != nil {
			if err := ss.generateSummary(ctx, session); err != nil {
				result["summary_error"] = err.Error()
			} else {
				result["summarized"] = true
				session.Status = string(SessionStatusSummarized)
				if err := ss.Persist(ctx, session); err != nil {
					result["_persist_error"] = err.Error()
				}
			}
		}
	}

	// Auto-cleanup coordination resources
	if cleanup {
		agentID := session.AgentID
		cleanedUp := map[string]any{}

		if ss.releaseClaimsForAgent != nil {
			released := ss.releaseClaimsForAgent(agentID)
			cleanedUp["file_claims_released"] = released
		}

		if ss.removePresence != nil {
			hadPresence := ss.removePresence(agentID)
			cleanedUp["presence_deregistered"] = hadPresence

			if hadPresence && ss.deletePresenceFromQdrant != nil {
				if err := ss.deletePresenceFromQdrant(ctx, agentID); err != nil {
					ss.logger.Warn("failed to delete presence from Qdrant", "agent_id", agentID, "error", err)
				}
			}
		}

		if ss.orphanWorktrees != nil {
			ss.orphanWorktrees(agentID)
			cleanedUp["worktrees_orphaned"] = true
		}

		if ss.markTasksStale != nil {
			staleTasks := ss.markTasksStale(ctx, sessionID)
			cleanedUp["tasks_marked_stale"] = staleTasks
		}

		result["cleanup"] = cleanedUp
	}

	return mcp.JSONResult(result)
}

// EndActiveForAgent ends all active sessions belonging to the given agent.
func (ss *SessionSvc) EndActiveForAgent(ctx context.Context, agentID string) {
	agentID = strings.TrimSpace(agentID)
	now := time.Now()

	// End in-memory sessions.
	ss.mu.Lock()
	for _, sess := range ss.sessions {
		if strings.TrimSpace(sess.AgentID) == agentID && sess.Status == string(SessionStatusActive) {
			sess.Status = string(SessionStatusEnded)
			sess.EndedAt = &now
		}
	}
	ss.mu.Unlock()

	// End persisted sessions in Qdrant.
	if ss.qdrant == nil {
		return
	}
	filter := FilterMust(
		Match("agent_id", agentID),
		Match("status", "active"),
	)
	points, err := ss.qdrant.ScrollPoints(ctx, filter, 500, false)
	if err != nil || len(points) == 0 {
		return
	}
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		sess.Status = string(SessionStatusEnded)
		sess.EndedAt = &now
		if err := ss.Persist(ctx, sess); err != nil {
			ss.logger.Warn("failed to end stale session for expired agent",
				"session_id", sess.ID, "agent_id", agentID, "error", err)
		}
	}
	ss.logger.Info("ended active sessions for expired agent",
		"agent_id", agentID, "count", len(points))
}

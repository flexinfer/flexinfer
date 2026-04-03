package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// Prune deletes stale sessions matching status and age criteria.
func (ss *SessionSvc) Prune(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	maxAgeHours := v.Int("max_age_hours", 72)
	statusFilter := v.String("status", "ended,summarized")
	dryRun := v.Bool("dry_run", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	pruned, err := ss.PruneSessions(ctx, maxAgeHours, statusFilter, dryRun)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("prune sessions: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"pruned":  pruned,
		"dry_run": dryRun,
	})
}

// PruneSessions deletes sessions matching status and age criteria.
func (ss *SessionSvc) PruneSessions(ctx context.Context, maxAgeHours int, statusFilter string, dryRun bool) (int, error) {
	if ss.qdrant == nil {
		return 0, nil
	}

	statuses := strings.Split(statusFilter, ",")
	for i, st := range statuses {
		statuses[i] = strings.TrimSpace(st)
	}

	cutoff := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)

	var statusConds []any
	for _, st := range statuses {
		if st != "" {
			statusConds = append(statusConds, Match("status", st))
		}
	}
	if len(statusConds) == 0 {
		return 0, nil
	}

	filter := FilterMust(FilterShould(statusConds...))
	points, err := ss.qdrant.ScrollPoints(ctx, filter, 1000, false)
	if err != nil {
		return 0, fmt.Errorf("scroll sessions: %w", err)
	}

	var toDelete []string
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		ts := sess.StartedAt
		if sess.EndedAt != nil {
			ts = *sess.EndedAt
		}
		if ts.Before(cutoff) {
			toDelete = append(toDelete, sess.ID)
		}
	}

	if dryRun || len(toDelete) == 0 {
		return len(toDelete), nil
	}

	if err := ss.qdrant.Delete(ctx, toDelete); err != nil {
		return 0, fmt.Errorf("delete sessions: %w", err)
	}

	ss.mu.Lock()
	for _, id := range toDelete {
		delete(ss.sessions, id)
	}
	ss.mu.Unlock()

	ss.logger.Info("pruned stale sessions", "count", len(toDelete), "max_age_hours", maxAgeHours, "statuses", statusFilter)
	return len(toDelete), nil
}

// EndStale finds active sessions older than maxAgeHours whose agents
// have no current presence, and marks them ended.
func (ss *SessionSvc) EndStale(ctx context.Context, maxAgeHours int) int {
	if ss.qdrant == nil {
		return 0
	}

	cutoff := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)

	filter := FilterMust(Match("status", "active"))
	points, err := ss.qdrant.ScrollPoints(ctx, filter, 1000, false)
	if err != nil {
		ss.logger.Warn("endStaleSessions scroll failed", "error", err)
		return 0
	}

	// Snapshot of agents with live presence.
	var liveAgents map[string]bool
	if ss.liveAgentIDs != nil {
		ids := ss.liveAgentIDs()
		liveAgents = make(map[string]bool, len(ids))
		for _, id := range ids {
			liveAgents[id] = true
		}
	}

	now := time.Now()
	var ended int
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		if liveAgents[sess.AgentID] {
			continue
		}
		if sess.StartedAt.After(cutoff) {
			continue
		}
		sess.Status = string(SessionStatusEnded)
		sess.EndedAt = &now
		if err := ss.Persist(ctx, sess); err != nil {
			ss.logger.Warn("failed to persist stale session end", "session_id", sess.ID, "error", err)
			continue
		}
		ss.mu.Lock()
		if existing, ok := ss.sessions[sess.ID]; ok {
			existing.Status = string(SessionStatusEnded)
			existing.EndedAt = &now
		}
		ss.mu.Unlock()
		ended++
	}
	return ended
}

// RunReaper periodically prunes old sessions and reaps stale active sessions.
func (ss *SessionSvc) RunReaper(ctx context.Context) {
	interval := time.Duration(ss.cfg.SessionReaperInterval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Minute
	}

	// Immediate sweep on startup.
	ss.reaperTick(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ss.reaperTick(ctx)
		}
	}
}

func (ss *SessionSvc) reaperTick(ctx context.Context) {
	statusFilter := "ended,summarized"
	pruned, err := ss.PruneSessions(ctx, ss.cfg.SessionReaperMaxAge, statusFilter, false)
	if err != nil {
		ss.logger.Warn("session reaper failed", "error", err)
	} else if pruned > 0 {
		ss.logger.Info("session reaper completed", "pruned", pruned, "filter", statusFilter)
	}

	activeMaxAge := ss.cfg.SessionReaperActiveMaxAge
	if activeMaxAge <= 0 {
		activeMaxAge = 24
	}
	ended := ss.EndStale(ctx, activeMaxAge)
	if ended > 0 {
		ss.logger.Info("ended stale active sessions", "count", ended, "max_age_hours", activeMaxAge)
	}
}

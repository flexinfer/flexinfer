package agentcontext

import (
	"context"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Git worktree integration: agents request isolated worktrees for parallel work.
// All logic lives in WorktreeSvc; these methods delegate from Service.

func (s *Service) HandleWorktreeAllocate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.worktrees.Allocate(ctx, args)
}

func (s *Service) HandleWorktreeRelease(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.worktrees.Release(ctx, args)
}

func (s *Service) HandleWorktreeList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.worktrees.List(ctx, args)
}

func (s *Service) HandleWorktreeStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.worktrees.Status(ctx, args)
}

func (s *Service) HandleWorktreeCleanup(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.worktrees.Cleanup(ctx, args)
}

func (s *Service) HandleWorktreeReconcile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.worktrees.Reconcile(ctx, args)
}

// orphanWorktreesForAgent delegates to WorktreeSvc.OrphanForAgent.
func (s *Service) orphanWorktreesForAgent(agentID string) {
	s.worktrees.OrphanForAgent(agentID)
}

// Payload converters (used by WorktreeSvc persistence and tests)

func worktreeAssignmentToPayload(a *WorktreeAssignment) map[string]any {
	payload := map[string]any{
		"id":            a.ID,
		"agent_id":      a.AgentID,
		"session_id":    a.SessionID,
		"worktree_path": a.WorktreePath,
		"branch":        a.Branch,
		"base_branch":   a.BaseBranch,
		"purpose":       a.Purpose,
		"status":        string(a.Status),
		"created_at":    a.CreatedAt.Format(time.RFC3339Nano),
	}
	if a.ReleasedAt != nil {
		payload["released_at"] = a.ReleasedAt.Format(time.RFC3339Nano)
	}
	if a.OrphanedAt != nil {
		payload["orphaned_at"] = a.OrphanedAt.Format(time.RFC3339Nano)
	}
	if a.TTL > 0 {
		payload["ttl_hours"] = a.TTL
	}
	if a.DiskUsage > 0 {
		payload["disk_usage_bytes"] = a.DiskUsage
	}
	if a.DiskMeasuredAt != nil {
		payload["disk_measured_at"] = a.DiskMeasuredAt.Format(time.RFC3339Nano)
	}
	return payload
}

func payloadToWorktreeAssignment(payload map[string]any) *WorktreeAssignment {
	if payload == nil {
		return nil
	}
	a := &WorktreeAssignment{
		ID:           toString(payload["id"]),
		AgentID:      toString(payload["agent_id"]),
		SessionID:    toString(payload["session_id"]),
		WorktreePath: toString(payload["worktree_path"]),
		Branch:       toString(payload["branch"]),
		BaseBranch:   toString(payload["base_branch"]),
		Purpose:      toString(payload["purpose"]),
		Status:       WorktreeStatus(toString(payload["status"])),
		TTL:          toInt(payload["ttl_hours"]),
		DiskUsage:    toInt64(payload["disk_usage_bytes"]),
	}
	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			a.CreatedAt = t
		}
	}
	if ts := toString(payload["released_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			a.ReleasedAt = &t
		}
	}
	if ts := toString(payload["orphaned_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			a.OrphanedAt = &t
		}
	}
	if ts := toString(payload["disk_measured_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			a.DiskMeasuredAt = &t
		}
	}
	return a
}

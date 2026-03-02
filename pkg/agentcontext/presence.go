package agentcontext

import (
	"context"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Presence registry: agents register, heartbeat to stay alive, discover each other.
// This file contains thin delegation methods on Service that forward to PresenceSvc,
// plus payload conversion functions shared by both layers.

// HandlePresenceRegister announces an agent is active
func (s *Service) HandlePresenceRegister(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.presence.Register(ctx, args)
}

// HandlePresenceHeartbeat keeps an agent alive and updates state
func (s *Service) HandlePresenceHeartbeat(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.presence.Heartbeat(ctx, args)
}

// HandlePresenceList discovers active agents
func (s *Service) HandlePresenceList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.presence.List(ctx, args)
}

// HandlePresenceDeregister cleans up an agent's presence
func (s *Service) HandlePresenceDeregister(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.presence.Deregister(ctx, args)
}

// detectFileConflicts checks if any files overlap with other agents' active files or claims
func (s *Service) detectFileConflicts(agentID string, files []string) []map[string]any {
	conflicts := s.presence.DetectActiveFileConflicts(agentID, files)
	conflicts = append(conflicts, s.claims.DetectConflicts(agentID, files)...)
	return conflicts
}

// Payload converters

func presenceToPayload(p *AgentPresence) map[string]any {
	payload := map[string]any{
		"id":             p.ID,
		"agent_id":       p.AgentID,
		"session_id":     p.SessionID,
		"status":         string(p.Status),
		"description":    p.Description,
		"current_task":   p.CurrentTask,
		"active_files":   p.ActiveFiles,
		"working_dir":    p.WorkingDir,
		"branch":         p.Branch,
		"worktree_id":    p.WorktreeID,
		"agent_type":     p.AgentType,
		"pr_url":         p.PRUrl,
		"last_heartbeat": p.LastHeartbeat.Format(time.RFC3339Nano),
		"heartbeat_ttl":  p.HeartbeatTTL,
		"registered_at":  p.RegisteredAt.Format(time.RFC3339Nano),
	}
	if p.Metadata != nil {
		payload["metadata"] = p.Metadata
	}
	return payload
}

func payloadToPresence(payload map[string]any) *AgentPresence {
	if payload == nil {
		return nil
	}
	p := &AgentPresence{
		ID:           toString(payload["id"]),
		AgentID:      toString(payload["agent_id"]),
		SessionID:    toString(payload["session_id"]),
		Status:       PresenceStatus(toString(payload["status"])),
		Description:  toString(payload["description"]),
		CurrentTask:  toString(payload["current_task"]),
		ActiveFiles:  toStringSlice(payload["active_files"]),
		WorkingDir:   toString(payload["working_dir"]),
		Branch:       toString(payload["branch"]),
		WorktreeID:   toString(payload["worktree_id"]),
		AgentType:    toString(payload["agent_type"]),
		PRUrl:        toString(payload["pr_url"]),
		HeartbeatTTL: toInt(payload["heartbeat_ttl"]),
	}
	if ts := toString(payload["last_heartbeat"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			p.LastHeartbeat = t
		}
	}
	if ts := toString(payload["registered_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			p.RegisteredAt = t
		}
	}
	if m, ok := payload["metadata"].(map[string]any); ok {
		p.Metadata = m
	}
	return p
}

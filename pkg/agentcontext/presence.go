package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// Presence registry: agents register, heartbeat to stay alive, discover each other.

// HandlePresenceRegister announces an agent is active
func (s *Service) HandlePresenceRegister(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	sessionID := v.String("session_id", "")
	agentType := v.String("agent_type", "")
	description := v.String("description", "")
	ttl := v.Int("heartbeat_ttl_seconds", s.cfg.PresenceHeartbeatTTL)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Enforce minimum TTL floor to prevent misconfigured instant-expiry
	if ttl < 30 {
		ttl = 30
	}

	now := time.Now()
	presence := &AgentPresence{
		ID:            GenerateID(agentID, "presence", "", now),
		AgentID:       agentID,
		SessionID:     sessionID,
		Status:        PresenceStatusActive,
		Description:   description,
		AgentType:     agentType,
		LastHeartbeat: now,
		HeartbeatTTL:  ttl,
		RegisteredAt:  now,
	}

	s.presenceMu.Lock()
	s.presenceMap[agentID] = presence
	s.presenceMu.Unlock()

	result := map[string]any{
		"ok":            true,
		"presence_id":   presence.ID,
		"agent_id":      agentID,
		"status":        string(presence.Status),
		"heartbeat_ttl": ttl,
		"registered_at": now.Format(time.RFC3339),
	}

	// Persist to Qdrant (non-fatal)
	if err := s.persistPresence(ctx, presence); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist presence: %v", err)
	}

	return mcp.JSONResult(result)
}

// HandlePresenceHeartbeat keeps an agent alive and updates state
func (s *Service) HandlePresenceHeartbeat(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	activeFiles := v.StringSlice("active_files")
	currentTask := v.String("current_task", "")
	branch := v.String("branch", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.presenceMu.Lock()
	presence, ok := s.presenceMap[agentID]
	if !ok {
		s.presenceMu.Unlock()
		return mcp.ErrorResult(fmt.Errorf("agent %s not registered; call agent_presence_register first", agentID)), nil
	}

	presence.LastHeartbeat = time.Now()
	presence.ActiveFiles = activeFiles
	presence.CurrentTask = currentTask
	if branch != "" {
		presence.Branch = branch
	}
	s.presenceMu.Unlock()

	// Check for file conflicts against other agents' active files
	conflicts := s.detectFileConflicts(agentID, activeFiles)

	result := map[string]any{
		"ok":             true,
		"agent_id":       agentID,
		"last_heartbeat": presence.LastHeartbeat.Format(time.RFC3339),
	}

	// Persist update (non-fatal)
	if err := s.persistPresence(ctx, presence); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist heartbeat: %v", err)
	}

	if len(conflicts) > 0 {
		result["has_conflicts"] = true
		result["conflicts"] = conflicts
	}

	return mcp.JSONResult(result)
}

// HandlePresenceList discovers active agents
func (s *Service) HandlePresenceList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	includeOffline := v.Bool("include_offline", false)
	namespace := v.String("namespace", "")
	_ = namespace // reserved for future filtering

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.presenceMu.RLock()
	defer s.presenceMu.RUnlock()

	var agents []map[string]any
	now := time.Now()

	for _, p := range s.presenceMap {
		isExpired := now.After(p.LastHeartbeat.Add(time.Duration(p.HeartbeatTTL) * time.Second))

		if isExpired && !includeOffline {
			continue
		}

		status := p.Status
		if isExpired {
			status = PresenceStatusOffline
		}

		entry := map[string]any{
			"agent_id":       p.AgentID,
			"status":         string(status),
			"agent_type":     p.AgentType,
			"description":    p.Description,
			"current_task":   p.CurrentTask,
			"active_files":   p.ActiveFiles,
			"branch":         p.Branch,
			"worktree_id":    p.WorktreeID,
			"last_heartbeat": p.LastHeartbeat.Format(time.RFC3339),
			"registered_at":  p.RegisteredAt.Format(time.RFC3339),
		}
		if p.SessionID != "" {
			entry["session_id"] = p.SessionID
		}
		agents = append(agents, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"agents": agents,
		"count":  len(agents),
	})
}

// HandlePresenceDeregister cleans up an agent's presence
func (s *Service) HandlePresenceDeregister(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	releaseClaims := v.Bool("release_claims", true)
	releaseWorktrees := v.Bool("release_worktrees", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.presenceMu.Lock()
	_, existed := s.presenceMap[agentID]
	delete(s.presenceMap, agentID)
	s.presenceMu.Unlock()

	if !existed {
		return mcp.ErrorResult(fmt.Errorf("agent %s not registered", agentID)), nil
	}

	// Release file claims if requested
	if releaseClaims {
		s.releaseAllClaimsForAgent(agentID)
	}

	// Mark worktrees as orphaned if requested
	if releaseWorktrees {
		s.orphanWorktreesForAgent(agentID)
	}

	result := map[string]any{
		"ok":       true,
		"agent_id": agentID,
	}

	// Delete from Qdrant (non-fatal)
	if err := s.presenceQdrant.DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID))); err != nil {
		result["_warning"] = fmt.Sprintf("failed to delete presence from store: %v", err)
	}

	return mcp.JSONResult(result)
}

// detectFileConflicts checks if any files overlap with other agents' active files or claims
func (s *Service) detectFileConflicts(agentID string, files []string) []map[string]any {
	if len(files) == 0 {
		return nil
	}

	var conflicts []map[string]any

	// Check against other agents' active files
	s.presenceMu.RLock()
	for otherID, p := range s.presenceMap {
		if otherID == agentID {
			continue
		}
		now := time.Now()
		if now.After(p.LastHeartbeat.Add(time.Duration(p.HeartbeatTTL) * time.Second)) {
			continue // expired
		}
		for _, f := range files {
			for _, of := range p.ActiveFiles {
				if f == of {
					conflicts = append(conflicts, map[string]any{
						"file":       f,
						"agent_id":   otherID,
						"agent_type": p.AgentType,
						"source":     "active_files",
					})
				}
			}
		}
	}
	s.presenceMu.RUnlock()

	// Check against file claims
	s.fileClaimsMu.RLock()
	for _, f := range files {
		if agents, ok := s.fileClaims[f]; ok {
			for claimAgent, claim := range agents {
				if claimAgent == agentID {
					continue
				}
				if claim.ExpiresAt != nil && time.Now().After(*claim.ExpiresAt) {
					continue
				}
				conflicts = append(conflicts, map[string]any{
					"file":       f,
					"agent_id":   claimAgent,
					"claim_type": string(claim.ClaimType),
					"source":     "file_claim",
				})
			}
		}
	}
	s.fileClaimsMu.RUnlock()

	return conflicts
}

// runPresenceCleanup periodically sweeps expired presence entries and releases their claims
func (s *Service) runPresenceCleanup(ctx context.Context) {
	interval := time.Duration(s.cfg.PresenceCleanupInterval) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupExpiredPresence(ctx)
		}
	}
}

// cleanupExpiredPresence removes expired presence entries and auto-releases resources
func (s *Service) cleanupExpiredPresence(ctx context.Context) {
	now := time.Now()
	var expired []string

	s.presenceMu.RLock()
	for agentID, p := range s.presenceMap {
		if now.After(p.LastHeartbeat.Add(time.Duration(p.HeartbeatTTL) * time.Second)) {
			expired = append(expired, agentID)
		}
	}
	s.presenceMu.RUnlock()

	for _, agentID := range expired {
		s.presenceMu.Lock()
		delete(s.presenceMap, agentID)
		s.presenceMu.Unlock()

		// Auto-release file claims
		s.releaseAllClaimsForAgent(agentID)

		// Auto-orphan worktrees
		if s.cfg.GitAutoCleanupWorktrees {
			s.orphanWorktreesForAgent(agentID)
		}

		// Clean from Qdrant
		_ = s.presenceQdrant.DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID)))
	}
}

// persistPresence stores presence to Qdrant
func (s *Service) persistPresence(ctx context.Context, p *AgentPresence) error {
	if err := s.presenceQdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      p.ID,
		Vector:  make([]float64, sessionsVectorSize),
		Payload: presenceToPayload(p),
	}

	return s.presenceQdrant.Upsert(ctx, []Point{point}, true)
}

// loadPresenceFromQdrant loads presence registry from Qdrant on startup
func (s *Service) loadPresenceFromQdrant(ctx context.Context) error {
	points, err := s.presenceQdrant.ScrollPoints(ctx, nil, 500, false)
	if err != nil {
		return err
	}

	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()

	for _, p := range points {
		presence := payloadToPresence(p.Payload)
		if presence == nil {
			continue
		}
		s.presenceMap[presence.AgentID] = presence
	}
	return nil
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

package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// PresenceSvc manages agent presence registration, heartbeats, and cleanup.
type PresenceSvc struct {
	mu  sync.RWMutex
	reg map[string]*AgentPresence

	qdrant  *QdrantClient
	cfg     Config
	logger  *slog.Logger
	metrics *Metrics

	// Callback for state transitions (wired by Service for SSE broadcast).
	onEvent func(eventType string, agentID string, oldStatus, newStatus PresenceStatus)

	// Cross-domain callbacks (wired by Service).
	releaseClaimsForAgent func(agentID string)
	orphanWorktrees       func(agentID string)
	endSessionsForAgent   func(ctx context.Context, agentID string)
	detectConflicts       func(agentID string, files []string) []map[string]any
}

// NewPresenceSvc creates a new PresenceSvc.
func NewPresenceSvc(qdrant *QdrantClient, cfg Config, logger *slog.Logger, metrics *Metrics) *PresenceSvc {
	return &PresenceSvc{
		reg:     make(map[string]*AgentPresence),
		qdrant:  qdrant,
		cfg:     cfg,
		logger:  logger,
		metrics: metrics,
	}
}

// SetOnEvent registers a callback for presence state transitions.
func (p *PresenceSvc) SetOnEvent(fn func(eventType string, agentID string, oldStatus, newStatus PresenceStatus)) {
	p.onEvent = fn
}

// Register announces an agent is active.
func (p *PresenceSvc) Register(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	sessionID := v.String("session_id", "")
	agentType := v.String("agent_type", "")
	description := v.String("description", "")
	ttl := v.Int("heartbeat_ttl_seconds", p.cfg.PresenceHeartbeatTTL)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

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

	p.mu.Lock()
	p.reg[agentID] = presence
	p.mu.Unlock()

	result := map[string]any{
		"ok":            true,
		"presence_id":   presence.ID,
		"agent_id":      agentID,
		"status":        string(presence.Status),
		"heartbeat_ttl": ttl,
		"registered_at": now.Format(time.RFC3339),
	}

	if err := p.persist(ctx, presence); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist presence: %v", err)
	}

	return mcp.JSONResult(result)
}

// Heartbeat keeps an agent alive and updates state.
// If the agent is not registered, it auto-registers with a minimal presence
// entry so heartbeats never fail just because the initial registration was
// missed (e.g. due to a flaky daemon transport).
func (p *PresenceSvc) Heartbeat(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	activeFiles := v.StringSlice("active_files")
	currentTask := v.String("current_task", "")
	branch := v.String("branch", "")
	statusRaw := v.String("status", "")
	agentType := v.String("agent_type", "")
	sessionID := v.String("session_id", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	autoRegistered := false
	p.mu.Lock()
	presence, ok := p.reg[agentID]
	if !ok {
		// Auto-register: create a minimal presence entry so heartbeats
		// succeed even when the initial registration was lost.
		now := time.Now()
		status := PresenceStatusActive
		if statusRaw != "" {
			switch PresenceStatus(statusRaw) {
			case PresenceStatusActive, PresenceStatusIdle:
				status = PresenceStatus(statusRaw)
			}
		}
		presence = &AgentPresence{
			ID:            GenerateID(agentID, "presence", "", now),
			AgentID:       agentID,
			SessionID:     sessionID,
			Status:        status,
			AgentType:     agentType,
			LastHeartbeat: now,
			HeartbeatTTL:  p.cfg.PresenceHeartbeatTTL,
			RegisteredAt:  now,
		}
		if presence.HeartbeatTTL < 30 {
			presence.HeartbeatTTL = 30
		}
		p.reg[agentID] = presence
		autoRegistered = true
		p.logger.Info("auto-registered agent on heartbeat", "agent_id", agentID, "agent_type", agentType)
	}

	presence.LastHeartbeat = time.Now()
	presence.ActiveFiles = activeFiles
	presence.CurrentTask = currentTask
	if branch != "" {
		presence.Branch = branch
	}
	if prURL := v.String("pr_url", ""); prURL != "" {
		presence.PRUrl = prURL
	}
	if agentType != "" && presence.AgentType == "" {
		presence.AgentType = agentType
	}
	if sessionID != "" && presence.SessionID == "" {
		presence.SessionID = sessionID
	}
	if statusRaw != "" && !autoRegistered {
		switch PresenceStatus(statusRaw) {
		case PresenceStatusActive, PresenceStatusIdle:
			presence.Status = PresenceStatus(statusRaw)
		default:
			p.mu.Unlock()
			return mcp.ErrorResult(fmt.Errorf("invalid status %q (must be 'active' or 'idle')", statusRaw)), nil
		}
	}
	p.mu.Unlock()

	var conflicts []map[string]any
	if p.detectConflicts != nil {
		conflicts = p.detectConflicts(agentID, activeFiles)
	}

	result := map[string]any{
		"ok":             true,
		"agent_id":       agentID,
		"last_heartbeat": presence.LastHeartbeat.Format(time.RFC3339),
	}
	if autoRegistered {
		result["auto_registered"] = true
	}

	if err := p.persist(ctx, presence); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist heartbeat: %v", err)
	}

	if len(conflicts) > 0 {
		result["has_conflicts"] = true
		result["conflicts"] = conflicts
	}

	return mcp.JSONResult(result)
}

// List discovers active agents.
func (p *PresenceSvc) List(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	includeOffline := v.Bool("include_offline", false)
	namespace := v.String("namespace", "")
	_ = namespace

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	agents := make([]map[string]any, 0)
	now := time.Now()

	for _, pr := range p.reg {
		isExpired := now.After(pr.LastHeartbeat.Add(time.Duration(pr.HeartbeatTTL) * time.Second))
		if isExpired && !includeOffline {
			continue
		}

		status := pr.Status
		if isExpired {
			status = PresenceStatusOffline
		}

		activeFiles := pr.ActiveFiles
		if activeFiles == nil {
			activeFiles = []string{}
		}

		entry := map[string]any{
			"agent_id":       pr.AgentID,
			"status":         string(status),
			"agent_type":     pr.AgentType,
			"description":    pr.Description,
			"current_task":   pr.CurrentTask,
			"active_files":   activeFiles,
			"branch":         pr.Branch,
			"pr_url":         pr.PRUrl,
			"worktree_id":    pr.WorktreeID,
			"last_heartbeat": pr.LastHeartbeat.Format(time.RFC3339),
			"registered_at":  pr.RegisteredAt.Format(time.RFC3339),
		}
		if pr.SessionID != "" {
			entry["session_id"] = pr.SessionID
		}
		agents = append(agents, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"agents": agents,
		"count":  len(agents),
	})
}

// Deregister cleans up an agent's presence.
func (p *PresenceSvc) Deregister(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	releaseClaims := v.Bool("release_claims", true)
	releaseWorktrees := v.Bool("release_worktrees", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	p.mu.Lock()
	_, existed := p.reg[agentID]
	delete(p.reg, agentID)
	p.mu.Unlock()

	if !existed {
		return mcp.ErrorResult(fmt.Errorf("agent %s not registered", agentID)), nil
	}

	if releaseClaims && p.releaseClaimsForAgent != nil {
		p.releaseClaimsForAgent(agentID)
	}
	if releaseWorktrees && p.orphanWorktrees != nil {
		p.orphanWorktrees(agentID)
	}

	result := map[string]any{
		"ok":       true,
		"agent_id": agentID,
	}

	if p.qdrant != nil {
		if err := p.qdrant.DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID))); err != nil {
			result["_warning"] = fmt.Sprintf("failed to delete presence from store: %v", err)
		}
	}

	return mcp.JSONResult(result)
}

// RunCleanup periodically sweeps expired presence entries.
func (p *PresenceSvc) RunCleanup(ctx context.Context) {
	interval := time.Duration(p.cfg.PresenceCleanupInterval) * time.Second
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
			p.cleanupExpired(ctx)
		}
	}
}

// cleanupExpired implements the presence state machine:
//
//	active → idle    (no heartbeat for 1× TTL)
//	idle   → offline (no heartbeat for 2× TTL)
//	offline→ expired (no heartbeat for 3× TTL, then remove)
func (p *PresenceSvc) cleanupExpired(ctx context.Context) {
	now := time.Now()

	type transition struct {
		agentID   string
		oldStatus PresenceStatus
		newStatus PresenceStatus
	}

	var transitions []transition
	var expired []string

	p.mu.Lock()
	for agentID, pr := range p.reg {
		ttl := time.Duration(pr.HeartbeatTTL) * time.Second
		elapsed := now.Sub(pr.LastHeartbeat)

		switch {
		case elapsed >= 3*ttl:
			if pr.Status != PresenceStatusExpired {
				transitions = append(transitions, transition{agentID, pr.Status, PresenceStatusExpired})
			}
			expired = append(expired, agentID)
		case elapsed >= 2*ttl:
			if pr.Status != PresenceStatusOffline {
				transitions = append(transitions, transition{agentID, pr.Status, PresenceStatusOffline})
				pr.Status = PresenceStatusOffline
			}
		case elapsed >= ttl:
			if pr.Status == PresenceStatusActive {
				transitions = append(transitions, transition{agentID, pr.Status, PresenceStatusIdle})
				pr.Status = PresenceStatusIdle
			}
		}
	}

	for _, agentID := range expired {
		delete(p.reg, agentID)
	}
	p.mu.Unlock()

	for _, t := range transitions {
		eventType := "agent.presence." + string(t.newStatus)
		p.logger.Info("presence state transition",
			"agent_id", t.agentID,
			"from", string(t.oldStatus),
			"to", string(t.newStatus))
		if p.onEvent != nil {
			p.onEvent(eventType, t.agentID, t.oldStatus, t.newStatus)
		}
	}

	for _, agentID := range expired {
		if p.releaseClaimsForAgent != nil {
			p.releaseClaimsForAgent(agentID)
		}
		if p.cfg.GitAutoCleanupWorktrees && p.orphanWorktrees != nil {
			p.orphanWorktrees(agentID)
		}
		if p.qdrant != nil {
			if err := p.qdrant.DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID))); err != nil {
				p.logger.Warn("failed to delete expired presence from Qdrant", "agent_id", agentID, "error", err)
			}
		}
		if p.endSessionsForAgent != nil {
			p.endSessionsForAgent(ctx, agentID)
		}
	}
}

// LiveAgentIDs returns agent IDs that have a non-expired heartbeat.
func (p *PresenceSvc) LiveAgentIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now()
	var ids []string
	for agentID, pr := range p.reg {
		if !now.After(pr.LastHeartbeat.Add(time.Duration(pr.HeartbeatTTL) * time.Second)) {
			ids = append(ids, agentID)
		}
	}
	return ids
}

// IsAgentStale returns true when the agent has no presence registered or
// its last heartbeat has expired beyond HeartbeatTTL.
func (p *PresenceSvc) IsAgentStale(agentID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	pr, ok := p.reg[agentID]
	if !ok {
		return true // no presence = stale
	}
	return time.Now().After(pr.LastHeartbeat.Add(time.Duration(pr.HeartbeatTTL) * time.Second))
}

// Get returns the presence entry for an agent, or nil.
func (p *PresenceSvc) Get(agentID string) *AgentPresence {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.reg[agentID]
}

// SetWorktreeID updates the worktree ID for an agent's presence.
func (p *PresenceSvc) SetWorktreeID(agentID, worktreeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pr, ok := p.reg[agentID]; ok {
		pr.WorktreeID = worktreeID
	}
}

// ClearWorktreeID clears the worktree ID for an agent if it matches the given ID.
func (p *PresenceSvc) ClearWorktreeID(agentID, worktreeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pr, ok := p.reg[agentID]; ok {
		if pr.WorktreeID == worktreeID {
			pr.WorktreeID = ""
		}
	}
}

func (p *PresenceSvc) persist(ctx context.Context, pr *AgentPresence) error {
	if p.qdrant == nil {
		return nil
	}
	if err := p.qdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      pr.ID,
		Vector:  make([]float64, sessionsVectorSize),
		Payload: presenceToPayload(pr),
	}

	return p.qdrant.Upsert(ctx, []Point{point}, true)
}

// DetectActiveFileConflicts checks if any files overlap with other agents' active files.
func (p *PresenceSvc) DetectActiveFileConflicts(agentID string, files []string) []map[string]any {
	if len(files) == 0 {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var conflicts []map[string]any
	now := time.Now()
	for otherID, pr := range p.reg {
		if otherID == agentID {
			continue
		}
		if now.After(pr.LastHeartbeat.Add(time.Duration(pr.HeartbeatTTL) * time.Second)) {
			continue // expired
		}
		for _, f := range files {
			for _, of := range pr.ActiveFiles {
				if f == of {
					conflicts = append(conflicts, map[string]any{
						"file":       f,
						"agent_id":   otherID,
						"agent_type": pr.AgentType,
						"source":     "active_files",
					})
				}
			}
		}
	}

	return conflicts
}

// Remove removes an agent from the presence registry. Returns whether it existed.
func (p *PresenceSvc) Remove(agentID string) bool {
	p.mu.Lock()
	_, existed := p.reg[agentID]
	delete(p.reg, agentID)
	p.mu.Unlock()
	return existed
}

// LoadFromQdrant loads presence registry from Qdrant on startup.
func (p *PresenceSvc) LoadFromQdrant(ctx context.Context) error {
	if p.qdrant == nil {
		return nil
	}
	points, err := p.qdrant.ScrollPoints(ctx, nil, 500, false)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, pt := range points {
		presence := payloadToPresence(pt.Payload)
		if presence == nil {
			continue
		}
		p.reg[presence.AgentID] = presence
	}
	return nil
}

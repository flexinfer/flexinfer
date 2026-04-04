package shuttle

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Bridge defines the subset of AgentBridge methods the engine needs.
type Bridge interface {
	Sessions() ([]bridge.SessionInfo, error)
	AllTasks() ([]bridge.TaskInfo, error)
	PresenceList(includeOffline bool) ([]bridge.PresenceInfo, error)
	FileClaimList(agentID string) ([]bridge.FileClaimInfo, error)
}

// Engine evaluates dispatch policies, scores agents, and produces
// recommendations and preflight conflict checks.
type Engine struct {
	mu     sync.RWMutex
	policy PolicyConfig
	logger *slog.Logger
}

// NewEngine creates an Engine with default policies.
func NewEngine(logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		policy: DefaultPolicyConfig(),
		logger: logger.With("component", "shuttle-engine"),
	}
}

// GetPolicy returns the current policy configuration.
func (e *Engine) GetPolicy() PolicyConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policy
}

// UpdatePolicy replaces the active policy configuration.
func (e *Engine) UpdatePolicy(cfg PolicyConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policy = cfg
	e.logger.Info("policy updated",
		"max_tasks", cfg.Load.MaxConcurrentTasks,
		"token_cap", cfg.Load.TokenBudgetCeiling,
		"auto_dispatch", cfg.Dispatch.Enabled,
	)
}

// EvaluateDispatch scores each agent for a given task and returns the
// best agent ID with a human-readable reason.
func (e *Engine) EvaluateDispatch(taskID, taskTitle string, agents []CapacityInfo) (string, string) {
	e.mu.RLock()
	policy := e.policy
	e.mu.RUnlock()

	var bestAgent string
	var bestScore float64
	var bestReason string

	for _, agent := range agents {
		if !isAvailable(agent) {
			continue
		}
		if agent.ActiveTasks >= policy.Load.MaxConcurrentTasks {
			continue
		}
		if policy.Load.TokenBudgetCeiling > 0 && agent.TokensUsed >= policy.Load.TokenBudgetCeiling {
			continue
		}

		score := scoreAgent(agent, policy)
		if score > bestScore || bestAgent == "" {
			bestAgent = agent.AgentID
			bestScore = score
			bestReason = buildReason(agent, score, policy)
		}
	}

	if bestAgent == "" {
		return "", "no available agents"
	}
	return bestAgent, bestReason
}

// scoreAgent computes a dispatch fitness score for an agent.
// Higher is better. The score favours agents with more available slots,
// lower utilization, and idle status when PreferIdleAgents is enabled.
func scoreAgent(agent CapacityInfo, policy PolicyConfig) float64 {
	// Base: available capacity ratio (0..1).
	maxTasks := policy.Load.MaxConcurrentTasks
	if maxTasks <= 0 {
		maxTasks = 5
	}
	available := maxTasks - agent.ActiveTasks
	if available < 0 {
		available = 0
	}
	score := float64(available) / float64(maxTasks)

	// Penalise high utilization.
	score *= (1.0 - agent.Utilization*0.5)

	// Bonus for idle agents.
	if policy.Dispatch.Enabled && strings.EqualFold(agent.Status, "idle") {
		score += 0.3
	}

	return score
}

func buildReason(agent CapacityInfo, score float64, policy PolicyConfig) string {
	parts := []string{}
	parts = append(parts, fmt.Sprintf("score=%.2f", score))
	parts = append(parts, fmt.Sprintf("slots=%d", policy.Load.MaxConcurrentTasks-agent.ActiveTasks))
	parts = append(parts, fmt.Sprintf("util=%.0f%%", agent.Utilization*100))
	if policy.Dispatch.Enabled && strings.EqualFold(agent.Status, "idle") {
		parts = append(parts, "idle_bonus")
	}
	return strings.Join(parts, ", ")
}

func isAvailable(agent CapacityInfo) bool {
	status := strings.ToLower(strings.TrimSpace(agent.Status))
	return status == "active" || status == "idle"
}

// BuildCapacities derives CapacityInfo for each agent from raw bridge data.
func (e *Engine) BuildCapacities(
	sessions []bridge.SessionInfo,
	tasks []bridge.TaskInfo,
	presence []bridge.PresenceInfo,
) []CapacityInfo {
	e.mu.RLock()
	policy := e.policy
	e.mu.RUnlock()

	// Count active tasks per agent.
	agentTasks := map[string]int{}
	for _, task := range tasks {
		agentID := taskAgentID(task, sessions)
		if agentID == "" {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if status == "pending" || status == "in_progress" {
			agentTasks[agentID]++
		}
	}

	// Sum tokens per agent from sessions.
	agentTokens := map[string]int{}
	for _, session := range sessions {
		if session.AgentID == "" || strings.ToLower(session.Status) != "active" {
			continue
		}
		agentTokens[session.AgentID] += session.TotalTokens
	}

	capacities := make([]CapacityInfo, 0, len(presence))
	for _, agent := range presence {
		activeTasks := agentTasks[agent.AgentID]
		tokensUsed := agentTokens[agent.AgentID]
		maxTasks := policy.Load.MaxConcurrentTasks
		tokenBudget := policy.Load.TokenBudgetCeiling
		available := maxTasks - activeTasks
		if available < 0 {
			available = 0
		}
		var utilization float64
		if maxTasks > 0 {
			utilization = float64(activeTasks) / float64(maxTasks)
		}

		info := CapacityInfo{
			AgentID:        agent.AgentID,
			Status:         normalizeStatus(agent.Status),
			ActiveTasks:    activeTasks,
			MaxTasks:       maxTasks,
			TokensUsed:     tokensUsed,
			TokenBudget:    tokenBudget,
			Utilization:    utilization,
			AvailableSlots: available,
		}
		if strings.EqualFold(agent.Status, "idle") && agent.LastHeartbeat != "" {
			info.IdleSince = agent.LastHeartbeat
		}
		capacities = append(capacities, info)
	}
	return capacities
}

// BuildRecommendations matches pending tasks to the best available agents.
func (e *Engine) BuildRecommendations(
	tasks []bridge.TaskInfo,
	capacities []CapacityInfo,
) []DispatchRecommendation {
	var recommendations []DispatchRecommendation
	for _, task := range tasks {
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if status != "pending" {
			continue
		}
		agentID, reason := e.EvaluateDispatch(task.ID, task.Title, capacities)
		if agentID == "" {
			continue
		}
		recommendations = append(recommendations, DispatchRecommendation{
			TaskID:           task.ID,
			TaskTitle:        task.Title,
			RecommendedAgent: agentID,
			Score:            0, // will be recomputed inline
			Reason:           reason,
		})
	}
	return recommendations
}

// PreflightCheck identifies file claim conflicts for a given agent and file path.
func (e *Engine) PreflightCheck(
	agentID, filePath string,
	claims []bridge.FileClaimInfo,
) []ConflictWarning {
	var warnings []ConflictWarning
	normalizedPath := strings.TrimSpace(filePath)
	normalizedAgent := strings.TrimSpace(agentID)
	if normalizedPath == "" || normalizedAgent == "" {
		return warnings
	}

	for _, claim := range claims {
		claimPath := strings.TrimSpace(claim.FilePath)
		claimAgent := strings.TrimSpace(claim.AgentID)
		if claimPath == "" || claimAgent == "" {
			continue
		}
		if claimAgent == normalizedAgent {
			continue
		}
		if claimPath == normalizedPath {
			warnings = append(warnings, ConflictWarning{
				FilePath:     claimPath,
				HeldBy:       claimAgent,
				ConflictType: "file_claim",
			})
		}
	}
	return warnings
}

// taskAgentID resolves the owning agent for a task, falling back to the
// session's agent if the task itself does not carry an agent_id.
func taskAgentID(task bridge.TaskInfo, sessions []bridge.SessionInfo) string {
	if id := strings.TrimSpace(task.AgentID); id != "" {
		return id
	}
	sessionID := strings.TrimSpace(task.SessionID)
	if sessionID == "" {
		return ""
	}
	for _, s := range sessions {
		if s.ID == sessionID {
			return strings.TrimSpace(s.AgentID)
		}
	}
	return ""
}

func normalizeStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return "offline"
	}
	return s
}

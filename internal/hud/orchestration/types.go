// Package orchestration implements the orchestration policy engine for
// auto-dispatch, load balancing, and conflict preflight across agents.
package orchestration

import "time"

// CapacityInfo describes an agent's current capacity and utilization.
type CapacityInfo struct {
	AgentID        string  `json:"agent_id"`
	Status         string  `json:"status"`
	ActiveTasks    int     `json:"active_tasks"`
	MaxTasks       int     `json:"max_tasks"`
	TokensUsed     int     `json:"tokens_used"`
	TokenBudget    int     `json:"token_budget"`
	Utilization    float64 `json:"utilization"`
	AvailableSlots int     `json:"available_slots"`
	IdleSince      string  `json:"idle_since,omitempty"`
}

// DispatchRecommendation pairs a pending task with the best agent to handle it.
type DispatchRecommendation struct {
	TaskID           string  `json:"task_id"`
	TaskTitle        string  `json:"task_title"`
	RecommendedAgent string  `json:"recommended_agent"`
	Score            float64 `json:"score"`
	Reason           string  `json:"reason"`
}

// ConflictWarning signals a file-level conflict risk before dispatch.
type ConflictWarning struct {
	FilePath     string `json:"file_path"`
	HeldBy       string `json:"held_by"`
	ConflictType string `json:"conflict_type"`
}

// OrchestrationSnapshot is the aggregated orchestration state served to the frontend.
type OrchestrationSnapshot struct {
	Capacities      []CapacityInfo           `json:"capacities"`
	Recommendations []DispatchRecommendation `json:"recommendations"`
	PendingTasks    int                      `json:"pending_tasks"`
	ActiveAgents    int                      `json:"active_agents"`
	SystemLoad      float64                  `json:"system_load"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

// DispatchPolicy, PolicyConfig, and DefaultPolicyConfig are defined in policy.go.

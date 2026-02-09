package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// WorkflowPlan is the LLM-generated workflow decomposition.
type WorkflowPlan struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Steps       []WorkflowPlanStep `json:"steps"`
}

// WorkflowPlanStep is a single step in the planned workflow.
type WorkflowPlanStep struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"` // "tool", "approval", "gate"
	Description string         `json:"description"`
	DependsOn   []string       `json:"depends_on,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
}

// Planner handles LLM-powered workflow DAG generation.
type Planner struct {
	client *FlexInferClient
	agent  *bridge.AgentBridge
	config Config
	logger *slog.Logger
}

// NewPlanner creates a Planner.
func NewPlanner(client *FlexInferClient, agent *bridge.AgentBridge, cfg Config, logger *slog.Logger) *Planner {
	return &Planner{
		client: client,
		agent:  agent,
		config: cfg,
		logger: logger.With("subsystem", "planner"),
	}
}

// PlanFromGoal decomposes a natural language goal into a workflow DAG.
func (p *Planner) PlanFromGoal(ctx context.Context, goal, namespace string) (*WorkflowPlan, error) {
	model := p.config.PlannerModel
	if model == "" {
		model = p.config.DefaultModel
	}

	raw, err := p.client.CompleteSimple(ctx, model, promptWorkflowPlan, goal, 800)
	if err != nil {
		return nil, fmt.Errorf("plan workflow: %w", err)
	}

	raw = stripCodeFence(raw)
	var plan WorkflowPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, fmt.Errorf("parse workflow plan: %w", err)
	}

	if plan.Name == "" {
		return nil, fmt.Errorf("empty workflow name in plan")
	}
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("no steps in workflow plan")
	}

	return &plan, nil
}

// RegisterPlan converts a WorkflowPlan into a workflow definition and registers
// it via the agent bridge.
func (p *Planner) RegisterPlan(ctx context.Context, plan *WorkflowPlan, namespace string) (string, error) {
	// Convert plan steps to the format expected by WorkflowDefine.
	steps := make([]map[string]any, len(plan.Steps))
	for i, s := range plan.Steps {
		step := map[string]any{
			"id":   s.ID,
			"name": s.Name,
			"type": s.Type,
		}
		if s.Description != "" {
			step["description"] = s.Description
		}
		if len(s.DependsOn) > 0 {
			step["depends_on"] = s.DependsOn
		}
		if len(s.Config) > 0 {
			step["config"] = s.Config
		}
		steps[i] = step
	}

	args := map[string]any{
		"name":  plan.Name,
		"steps": steps,
	}
	if plan.Description != "" {
		args["description"] = plan.Description
	}
	if namespace != "" {
		args["namespace"] = namespace
	}

	result, err := p.agent.WorkflowDefine(args)
	if err != nil {
		return "", fmt.Errorf("register workflow: %w", err)
	}

	return result.DefinitionID, nil
}

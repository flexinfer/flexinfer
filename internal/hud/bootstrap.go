package hud

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// workflowYAML is the subset of a workflow YAML file needed for registration.
type workflowYAML struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Steps       []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	StepType string `yaml:"step_type"`
}

// bootstrapWorkflowDefinitions reads .agents/workflows/*.yaml from the working
// directory and registers each as a workflow definition via the agent bridge.
// This is idempotent — duplicate definitions are silently skipped.
func (a *App) bootstrapWorkflowDefinitions() {
	pattern := filepath.Join(".agents", "workflows", "*.yaml")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return
	}

	loaded := 0
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			a.logger.Debug("bootstrap: skip unreadable workflow file", "path", path, "error", err)
			continue
		}

		var wf workflowYAML
		if err := yaml.Unmarshal(data, &wf); err != nil {
			a.logger.Debug("bootstrap: skip unparseable workflow file", "path", path, "error", err)
			continue
		}
		if wf.Name == "" || len(wf.Steps) == 0 {
			continue
		}

		// Build the steps slice for the define call.
		steps := make([]map[string]any, len(wf.Steps))
		for i, s := range wf.Steps {
			steps[i] = map[string]any{
				"id":        s.ID,
				"name":      s.Name,
				"step_type": s.StepType,
			}
		}

		args := map[string]any{
			"name":        wf.Name,
			"description": wf.Description,
			"steps":       steps,
		}

		if _, err := a.agent.WorkflowDefine(args); err != nil {
			a.logger.Debug("bootstrap: failed to register workflow definition",
				"name", wf.Name, "path", path, "error", err)
			continue
		}
		loaded++
	}

	if loaded > 0 {
		a.logger.Info("workflow definitions bootstrapped from YAML", "count", loaded)
	}
}

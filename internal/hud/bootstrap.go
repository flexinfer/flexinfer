package hud

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// bootstrapWorkflowDefinitions reads .agents/workflows/*.yaml from the working
// directory and registers each as a workflow definition via the agent bridge.
// This is idempotent — duplicate name+namespace definitions update in-place.
func (a *App) bootstrapWorkflowDefinitions() {
	patternYAML := filepath.Join(".agents", "workflows", "*.yaml")
	patternYML := filepath.Join(".agents", "workflows", "*.yml")
	matchesYAML, err := filepath.Glob(patternYAML)
	if err != nil {
		return
	}
	matchesYML, err := filepath.Glob(patternYML)
	if err != nil {
		return
	}

	matches := append(matchesYAML, matchesYML...)
	if len(matches) == 0 {
		return
	}

	loaded := 0
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			a.logger.Debug("bootstrap: skip unreadable workflow file", "path", path, "error", err)
			continue
		}

		// Keep the full payload to avoid dropping step attributes such as
		// tool_name/server_name/depends_on/requires_approval/etc.
		var body map[string]any
		if err := yaml.Unmarshal(data, &body); err != nil {
			a.logger.Debug("bootstrap: skip unparseable workflow file", "path", path, "error", err)
			continue
		}
		name, _ := body["name"].(string)
		steps, _ := body["steps"].([]any)
		if name == "" || len(steps) == 0 {
			continue
		}
		if _, err := a.agent.WorkflowDefine(body); err != nil {
			a.logger.Debug("bootstrap: failed to register workflow definition",
				"name", name, "path", path, "error", err)
			continue
		}
		loaded++
	}

	if loaded > 0 {
		a.logger.Info("workflow definitions bootstrapped from YAML", "count", loaded)
	}
}

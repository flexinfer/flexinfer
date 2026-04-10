package sandbox

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// handleSandbox returns devbox sandbox summary from the sandbox monitor.
// Returns {"available": false} if mcp-devbox is not running.
func (d *SandboxDomain) handleSandbox(w http.ResponseWriter, _ *http.Request) {
	snap := d.deps.SandboxSnapshot()
	if snap == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{
			"available":     false,
			"status":        "offline",
			"reason":        "mcp-devbox is not running or not connected to the daemon",
			"hint":          "Start the devbox service, then return to Labs to provision or inspect sandboxes.",
			"start_command": "loom start devbox",
		})
		return
	}
	snap["available"] = true
	d.deps.WriteJSON(w, http.StatusOK, snap)
}

// handleSandboxPolicy serves the sandbox policy from .sandbox-policy.json.
// Searches cwd and common profile directories for the policy file.
func (d *SandboxDomain) handleSandboxPolicy(w http.ResponseWriter, _ *http.Request) {
	if cached, ok := d.deps.CacheGet("sandbox_policy"); ok {
		d.deps.WriteJSON(w, http.StatusOK, cached)
		return
	}

	// Search well-known locations for the policy file.
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, ".sandbox-policy.json"),
		filepath.Join(cwd, ".claude", ".sandbox-policy.json"),
		filepath.Join(cwd, ".codex", ".sandbox-policy.json"),
		filepath.Join(cwd, ".gemini", ".sandbox-policy.json"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var policy map[string]any
		if err := json.Unmarshal(data, &policy); err != nil {
			continue
		}
		d.deps.CacheSet("sandbox_policy", policy, 60*time.Second)
		d.deps.WriteJSON(w, http.StatusOK, policy)
		return
	}

	// No policy found -- return empty.
	empty := map[string]any{"configured": false}
	d.deps.CacheSet("sandbox_policy", empty, 30*time.Second)
	d.deps.WriteJSON(w, http.StatusOK, empty)
}

// handleSandboxStart triggers devbox_build for a project via the daemon.
// POST /api/sandbox/start
func (d *SandboxDomain) handleSandboxStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		AgentID string `json:"agent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Project == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "project is required", nil)
		return
	}

	parsed, err := d.deps.DoSandboxStart(body.Project, body.AgentID)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to start sandbox", err)
		return
	}
	if parsed == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"project": body.Project,
			"message": "sandbox start requested",
		})
		return
	}
	parsed["ok"] = true
	if _, ok := parsed["project"]; !ok {
		parsed["project"] = body.Project
	}
	if _, ok := parsed["message"]; !ok {
		parsed["message"] = "sandbox start requested"
	}
	d.deps.WriteJSON(w, http.StatusOK, parsed)
}

// handleSandboxStop stops a running sandbox container for a project.
// POST /api/sandbox/stop
func (d *SandboxDomain) handleSandboxStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Project == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "project is required", nil)
		return
	}

	if err := d.deps.DoSandboxStop(body.Project); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to stop sandbox", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"project": body.Project,
		"message": "sandbox stop requested",
	})
}

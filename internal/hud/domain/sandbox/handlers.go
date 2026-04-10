package sandbox

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// handleSandboxCapabilities reports which devbox actions the HUD can drive.
func (d *SandboxDomain) handleSandboxCapabilities(w http.ResponseWriter, _ *http.Request) {
	snap := d.deps.SandboxSnapshot()
	payload := map[string]any{
		"available":         snap != nil,
		"backend":           "",
		"auth_required":     true,
		"supported_actions": []string{"build", "stop", "exec_async", "exec_poll"},
		"notes": map[string]any{
			"async_exec":           true,
			"polling_required":     true,
			"streaming_output":     false,
			"telemetry_source":     "devbox_summary + async exec polling",
			"sandbox_event_source": "hud.sandbox.event",
		},
	}
	if snap == nil {
		d.deps.WriteJSON(w, http.StatusOK, payload)
		return
	}
	if backend, _ := snap["backend"].(string); backend != "" {
		payload["backend"] = backend
	}
	if projects, ok := snap["projects"].([]string); ok {
		payload["projects"] = projects
	}
	if projects, ok := snap["projects"].([]any); ok {
		payload["project_count"] = len(projects)
	}
	d.deps.WriteJSON(w, http.StatusOK, payload)
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
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

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
	d.deps.BroadcastAgentEvent("hud.sandbox.event", map[string]any{
		"type":      "start",
		"project":   body.Project,
		"detail":    parsed["message"],
		"timestamp": time.Now().Format(time.RFC3339),
		"build_id":  parsed["build_id"],
	})
	d.deps.WriteJSON(w, http.StatusOK, parsed)
}

// handleSandboxStop stops a running sandbox container for a project.
// POST /api/sandbox/stop
func (d *SandboxDomain) handleSandboxStop(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

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
	d.deps.BroadcastAgentEvent("hud.sandbox.event", map[string]any{
		"type":      "stop",
		"project":   body.Project,
		"detail":    "sandbox stop requested",
		"timestamp": time.Now().Format(time.RFC3339),
	})
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"project": body.Project,
		"message": "sandbox stop requested",
	})
}

// handleSandboxExec starts an async command inside a project sandbox.
func (d *SandboxDomain) handleSandboxExec(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	var body struct {
		Project string `json:"project"`
		Command string `json:"command"`
		Timeout string `json:"timeout,omitempty"`
		AgentID string `json:"agent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if strings.TrimSpace(body.Project) == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "project is required", nil)
		return
	}
	if strings.TrimSpace(body.Command) == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "command is required", nil)
		return
	}

	parsed, err := d.deps.DoSandboxExecAsync(body.Project, body.Command, body.Timeout, body.AgentID)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to start sandbox exec", err)
		return
	}
	if parsed == nil {
		parsed = map[string]any{
			"project": body.Project,
			"command": body.Command,
			"status":  "running",
		}
	}
	parsed["ok"] = true
	if _, ok := parsed["project"]; !ok {
		parsed["project"] = body.Project
	}
	if _, ok := parsed["command"]; !ok {
		parsed["command"] = body.Command
	}
	d.deps.BroadcastAgentEvent("hud.sandbox.event", map[string]any{
		"type":      "exec",
		"project":   body.Project,
		"detail":    "queued " + body.Command,
		"timestamp": time.Now().Format(time.RFC3339),
		"exec_id":   parsed["exec_id"],
	})
	d.deps.WriteJSON(w, http.StatusAccepted, parsed)
}

// handleSandboxExecPoll returns the latest async exec status for an exec_id.
func (d *SandboxDomain) handleSandboxExecPoll(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	execID := r.PathValue("exec_id")
	if execID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "exec_id is required", nil)
		return
	}

	parsed, err := d.deps.DoSandboxExecPoll(execID)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to poll sandbox exec", err)
		return
	}
	if parsed == nil {
		d.deps.WriteError(w, http.StatusBadGateway, "sandbox exec returned no data", nil)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, parsed)
}

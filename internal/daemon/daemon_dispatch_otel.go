package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// handleRBACConfig returns RBAC configuration and recent denied calls.
func (d *Daemon) handleRBACConfig(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.rbac == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled":       false,
			"audit_enabled": d.audit != nil,
		})
	}

	cfg := d.rbac.Config()
	recentDenied := d.recentDeniedSnapshot()
	result := map[string]any{
		"enabled":        true,
		"audit_enabled":  d.audit != nil,
		"default_policy": cfg.DefaultPolicy,
		"global_deny":    cfg.GlobalDeny,
		"denied_count":   len(recentDenied),
	}

	var roles []map[string]any
	for name, role := range cfg.Roles {
		roles = append(roles, map[string]any{
			"name":  name,
			"allow": role.Allow,
			"deny":  role.Deny,
		})
	}
	result["roles"] = roles

	var bindings []map[string]any
	for _, b := range cfg.Bindings {
		bindings = append(bindings, map[string]any{
			"agent_id":   b.AgentID,
			"agent_type": b.AgentType,
			"role":       b.Role,
		})
	}
	result["bindings"] = bindings

	var rateLimits []map[string]any
	for _, rl := range cfg.RateLimits {
		rateLimits = append(rateLimits, map[string]any{
			"agent_id":            rl.AgentID,
			"tool":                rl.Tool,
			"requests_per_minute": rl.RequestsPerMinute,
		})
	}
	result["rate_limits"] = rateLimits

	result["recent_denied"] = recentDenied

	return mcp.NewResponse(msg.ID, result)
}

// handleRBACSimulate evaluates an RBAC decision for a provided request tuple.
func (d *Daemon) handleRBACSimulate(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		AgentID   string `json:"agent_id,omitempty"`
		AgentType string `json:"agent_type,omitempty"`
		Server    string `json:"server"`
		Tool      string `json:"tool"`
		DryRun    bool   `json:"dry_run,omitempty"`
	}
	if len(msg.Params) > 0 {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if params.Server == "" || params.Tool == "" {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "server and tool are required"), nil
	}

	if d.rbac == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
			"decision": AccessDecision{
				Allowed:    true,
				AgentID:    params.AgentID,
				Server:     params.Server,
				Tool:       params.Tool,
				Reason:     "rbac disabled",
				ReasonCode: "rbac_disabled",
				DryRun:     params.DryRun,
			},
		})
	}

	var decision AccessDecision
	if params.DryRun {
		decision = d.rbac.Simulate(params.AgentID, params.AgentType, params.Server, params.Tool)
	} else {
		decision = d.rbac.Check(params.AgentID, params.AgentType, params.Server, params.Tool)
	}

	return mcp.NewResponse(msg.ID, map[string]any{
		"enabled":  true,
		"decision": decision,
	})
}

// handleOTelStatus returns observability configuration status.
func (d *Daemon) handleOTelStatus(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	// Fall back to file config if env var is not set.
	if endpoint == "" && d.fileCfg.OTel.Endpoint != "" {
		endpoint = d.fileCfg.OTel.Endpoint
	}
	logFormat := os.Getenv("MCP_LOG_FORMAT")
	if logFormat == "" {
		logFormat = "text"
	}

	tracedServers, totalServers := d.computeTracedServerCoverage()
	coverage := formatCoverage(tracedServers, totalServers)

	runtimeSurfaces := runtimeTraceSurfaces()
	runtimeTraceCount := 0
	for _, enabled := range runtimeSurfaces {
		if enabled {
			runtimeTraceCount++
		}
	}
	runtimeTraceCoverage := formatCoverage(runtimeTraceCount, len(runtimeSurfaces))

	result := map[string]any{
		"otlp_endpoint":             endpoint,
		"otlp_configured":           endpoint != "",
		"log_format":                logFormat,
		"json_logs_enabled":         logFormat == "json",
		"traced_servers":            tracedServers,
		"total_servers":             totalServers,
		"trace_coverage":            coverage,
		"runtime_otlp_configured":   d.otelRuntimeState.Configured,
		"runtime_otlp_enabled":      d.otelRuntimeState.Enabled,
		"runtime_otlp_endpoint":     d.otelRuntimeState.Endpoint,
		"runtime_otlp_protocol":     d.otelRuntimeState.Protocol,
		"runtime_otlp_service_name": d.otelRuntimeState.ServiceName,
		"runtime_otlp_sample_rate":  d.otelRuntimeState.SampleRate,
		"runtime_otlp_error":        d.otelRuntimeState.InitError,
		"runtime_trace_surfaces":    runtimeSurfaces,
		"runtime_trace_coverage":    runtimeTraceCoverage,
	}
	return mcp.NewResponse(msg.ID, result)
}

func runtimeTraceSurfaces() map[string]bool {
	return map[string]bool{
		"rpc_dispatch":                true,
		"server_connect_and_spawn":    true,
		"server_restart_lifecycle":    true,
		"client_connection_lifecycle": true,
		"transport_recovery_events":   true,
	}
}

func (d *Daemon) computeTracedServerCoverage() (traced, total int) {
	if d.registry == nil {
		return 0, 0
	}
	total = len(d.registry.Servers)
	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}
		spec, err := d.registry.GetServerSpec(server.Name, d.cfg.Target)
		if err != nil || spec == nil {
			continue
		}
		if isMCPServerCommand(spec.Command, spec.Args) {
			traced++
		}
	}
	return traced, total
}

func isMCPServerCommand(command string, args []any) bool {
	if isMCPServerToken(command) {
		return true
	}
	for _, arg := range args {
		if isMCPServerToken(fmt.Sprint(arg)) {
			return true
		}
	}
	return false
}

func isMCPServerToken(token string) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "cmd/mcp-") {
		return true
	}
	base := filepath.Base(trimmed)
	return strings.HasPrefix(base, "mcp-")
}

func formatCoverage(numerator, denominator int) string {
	if denominator <= 0 {
		return "100%"
	}
	pct := float64(numerator) / float64(denominator) * 100
	return fmt.Sprintf("%.0f%%", pct)
}

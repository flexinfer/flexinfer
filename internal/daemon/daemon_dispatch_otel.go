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

type observabilityStatus struct {
	OTLPEndpoint           string          `json:"otlp_endpoint"`
	OTLPConfigured         bool            `json:"otlp_configured"`
	LogFormat              string          `json:"log_format"`
	JSONLogsEnabled        bool            `json:"json_logs_enabled"`
	TracedServers          int             `json:"traced_servers"`
	TotalServers           int             `json:"total_servers"`
	TraceCoverage          string          `json:"trace_coverage"`
	RuntimeOTLPConfigured  bool            `json:"runtime_otlp_configured"`
	RuntimeOTLPEnabled     bool            `json:"runtime_otlp_enabled"`
	RuntimeOTLPEndpoint    string          `json:"runtime_otlp_endpoint"`
	RuntimeOTLPProtocol    string          `json:"runtime_otlp_protocol"`
	RuntimeOTLPServiceName string          `json:"runtime_otlp_service_name"`
	RuntimeOTLPSampleRate  float64         `json:"runtime_otlp_sample_rate"`
	RuntimeOTLPError       string          `json:"runtime_otlp_error,omitempty"`
	RuntimeMeterEnabled    bool            `json:"runtime_meter_enabled"`
	RuntimeTraceSurfaces   map[string]bool `json:"runtime_trace_surfaces"`
	RuntimeTraceCoverage   string          `json:"runtime_trace_coverage"`
	Warnings               []string        `json:"warnings,omitempty"`
}

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
	return mcp.NewResponse(msg.ID, d.currentObservabilityStatus())
}

func (d *Daemon) currentObservabilityStatus() observabilityStatus {
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

	result := observabilityStatus{
		OTLPEndpoint:           endpoint,
		OTLPConfigured:         endpoint != "",
		LogFormat:              logFormat,
		JSONLogsEnabled:        logFormat == "json",
		TracedServers:          tracedServers,
		TotalServers:           totalServers,
		TraceCoverage:          coverage,
		RuntimeOTLPConfigured:  d.otelRuntimeState.Configured,
		RuntimeOTLPEnabled:     d.otelRuntimeState.Enabled,
		RuntimeOTLPEndpoint:    d.otelRuntimeState.Endpoint,
		RuntimeOTLPProtocol:    d.otelRuntimeState.Protocol,
		RuntimeOTLPServiceName: d.otelRuntimeState.ServiceName,
		RuntimeOTLPSampleRate:  d.otelRuntimeState.SampleRate,
		RuntimeOTLPError:       d.otelRuntimeState.InitError,
		RuntimeMeterEnabled:    d.otelMetrics != nil,
		RuntimeTraceSurfaces:   runtimeSurfaces,
		RuntimeTraceCoverage:   runtimeTraceCoverage,
	}

	if !result.OTLPConfigured {
		result.Warnings = append(result.Warnings, "otlp endpoint not configured")
	}
	if !result.JSONLogsEnabled {
		result.Warnings = append(result.Warnings, "json logging disabled")
	}
	if result.RuntimeOTLPError != "" {
		result.Warnings = append(result.Warnings, "otel runtime init error: "+result.RuntimeOTLPError)
	}
	return result
}

func runtimeTraceSurfaces() map[string]bool {
	return map[string]bool{
		"rpc_dispatch":                true,
		"server_connect_and_spawn":    true,
		"server_restart_lifecycle":    true,
		"client_connection_lifecycle": true,
		"transport_recovery_events":   true,
		"daemon_otel_metrics":         true,
		"session_lifecycle":           true,
		"hub_keepalive":               true,
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

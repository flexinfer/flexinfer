// Package status holds the canonical platform-status DTOs surfaced by
// `loom status` and the HUD landing surface.
//
// These types are lifted from cmd/loom/status.go and become the canonical
// definitions during EPIC 2 (#66). The original unexported types in
// cmd/loom can migrate onto these in a later slice (S3/S4) without changing
// JSON output.
package status

// PlatformStatus aggregates daemon, agent, and HUD status into one struct.
// It is the unified DTO returned by `loom status --json` and consumed by the
// HUD landing surface.
type PlatformStatus struct {
	Daemon        DaemonStatus               `json:"daemon"`
	Agents        AgentStatus                `json:"agents"`
	Sessions      SessionCount               `json:"sessions"`
	Pipelines     PipelineStatus             `json:"pipelines"`
	HUD           HUDStatus                  `json:"hud"`
	Health        *DaemonHealthSnapshot      `json:"health,omitempty"`
	Observability *DaemonObservabilityStatus `json:"observability,omitempty"`
	Healthy       bool                       `json:"healthy"`
}

// DaemonStatus summarizes daemon-level state surfaced in PlatformStatus.
type DaemonStatus struct {
	Running             bool     `json:"running"`
	Servers             int      `json:"servers"`
	ActiveConns         int      `json:"active_conns"`
	IdleConns           int      `json:"idle_conns"`
	ActiveRPCs          int64    `json:"active_rpcs"`
	ActiveProxySessions int      `json:"active_proxy_sessions"`
	DaemonEpoch         int64    `json:"daemon_epoch"`
	DrainReady          bool     `json:"drain_ready"`
	Draining            bool     `json:"draining"`
	Processes           []string `json:"processes,omitempty"`
}

// AgentStatus summarizes agent counts by liveness bucket.
type AgentStatus struct {
	Active  int `json:"active"`
	Idle    int `json:"idle"`
	Offline int `json:"offline"`
	Total   int `json:"total"`
}

// SessionCount summarizes active vs total session counts.
type SessionCount struct {
	Active int `json:"active"`
	Total  int `json:"total"`
}

// PipelineStatus summarizes recent CI pipeline activity surfaced in
// PlatformStatus. Available is false when pipeline data is unreachable.
type PipelineStatus struct {
	Available    bool   `json:"available"`
	Running      int    `json:"running"`
	Passed       int    `json:"passed"`
	Failed       int    `json:"failed"`
	Pending      int    `json:"pending"`
	LastActivity string `json:"last_activity,omitempty"`
}

// HUDStatus summarizes HUD reachability.
type HUDStatus struct {
	Reachable bool `json:"reachable"`
}

// DaemonHealthSnapshot is the aggregate per-server health view surfaced in
// PlatformStatus.
type DaemonHealthSnapshot struct {
	Servers         map[string]DaemonHealthServer `json:"servers,omitempty"`
	DegradedServers []string                      `json:"degraded_servers,omitempty"`
}

// DaemonHealthServer is one server's health entry inside DaemonHealthSnapshot.
type DaemonHealthServer struct {
	Healthy           bool    `json:"healthy"`
	Ready             bool    `json:"ready"`
	ConsecutiveFails  int     `json:"consecutive_fails"`
	TotalChecks       int     `json:"total_checks"`
	TotalFailures     int     `json:"total_failures"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	LastError         string  `json:"last_error,omitempty"`
	RestartCount      int     `json:"restart_count"`
	LastCheck         string  `json:"last_check,omitempty"`
	LastHealthy       string  `json:"last_healthy,omitempty"`
	LastRestart       string  `json:"last_restart,omitempty"`
	AutoRestartFailed bool    `json:"auto_restart_failed,omitempty"`
	LastDeepProbe     string  `json:"last_deep_probe,omitempty"`
}

// DaemonObservabilityStatus surfaces the daemon's OTel/log observability
// configuration inside PlatformStatus.
type DaemonObservabilityStatus struct {
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

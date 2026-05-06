// Package health holds the per-server health DTOs surfaced by the daemon's
// loom/health RPC. These are lifted from internal/hud/bridge/daemon.go and
// continue to be re-exported there as type aliases during EPIC 2 (#66).
package health

// HealthEntry describes the health of one endpoint (local or hub).
type HealthEntry struct {
	Healthy      bool    `json:"healthy"`
	ConsecFails  int     `json:"consecFails"`
	AvgLatencyMs float64 `json:"avgLatencyMs"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
}

// HealthDivergence represents a disagreement between the health monitor and
// the router for a single server.
type HealthDivergence struct {
	MonitorHealthy  bool   `json:"monitor_healthy"`
	RouterAvailable bool   `json:"router_available"`
	Reason          string `json:"reason"`
}

// HealthDivergenceEntry is a top-level divergence summary entry, one per
// disagreeing server, returned alongside the per-server map.
type HealthDivergenceEntry struct {
	Server string `json:"server"`
	Reason string `json:"reason"`
}

// ServerHealth contains local and hub health plus the target.
type ServerHealth struct {
	Local      HealthEntry       `json:"local"`
	Hub        HealthEntry       `json:"hub"`
	Monitor    *HealthEntry      `json:"monitor,omitempty"`
	Target     string            `json:"target"`
	Transport  string            `json:"transport,omitempty"` // ws, stdio, sse, ssh, or unavailable
	Divergence *HealthDivergence `json:"divergence,omitempty"`
}

// HealthResult holds the response from loom/health.
type HealthResult struct {
	Servers    map[string]ServerHealth `json:"servers"`
	Divergence []HealthDivergenceEntry `json:"divergence,omitempty"`
}

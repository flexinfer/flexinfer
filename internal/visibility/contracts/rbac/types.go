// Package rbac holds minimal RBAC visibility DTOs for the HUD/CLI surfaces.
//
// This package is intentionally a scaffold: the rich RBAC config DTOs in
// internal/hud/bridge/daemon.go (RBACConfigResult, RBACRoleInfo, etc.)
// remain canonical for now. Later EPIC 2 (#66) slices will lift the full
// shape here. For now we expose a tiny aggregate Snapshot that mirrors what
// the CLI/HUD surfaces actually need at a glance.
package rbac

import "time"

// Snapshot is a glance-level summary of the daemon's RBAC posture for the
// HUD landing page and `loom status`.
type Snapshot struct {
	PolicyVersion  string   `json:"policy_version,omitempty"`
	DeniedCount24h int      `json:"denied_count_24h"`
	AuditEnabled   bool     `json:"audit_enabled"`
	SimulationMode bool     `json:"simulation_mode"`
	RecentDenials  []Denial `json:"recent_denials,omitempty"`
}

// Denial describes a recently denied tool call surfaced in Snapshot.
type Denial struct {
	Time     time.Time `json:"time"`
	Actor    string    `json:"actor"`
	Resource string    `json:"resource"`
	Reason   string    `json:"reason"`
}

// Package catalog holds minimal MCP catalog/server inventory DTOs for the
// HUD/CLI surfaces.
//
// This package is intentionally a scaffold: the richer ServerInfo/ServersResult
// DTOs in internal/hud/bridge/daemon.go remain canonical for now. Later
// EPIC 2 (#66) slices will lift the full shape here.
package catalog

import "time"

// Status is a glance-level summary of the MCP server catalog surfaced by
// `loom status` and the HUD landing page.
type Status struct {
	Servers      []Entry   `json:"servers"`
	LastSyncTime time.Time `json:"last_sync_time"`
}

// Entry is one server in the catalog Status view.
type Entry struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	LastError   string `json:"last_error,omitempty"`
	Description string `json:"description,omitempty"`
}

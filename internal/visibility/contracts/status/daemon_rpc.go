package status

// DaemonRPCStatus is the response body from the daemon's loom/status RPC.
// It is a thin per-call snapshot of daemon liveness used by the HUD/CLI to
// build the richer PlatformStatus aggregate. Lifted from
// internal/hud/bridge/daemon.go (StatusResult); the bridge type continues
// to alias this during EPIC 2 (#66) so callers see no change.
type DaemonRPCStatus struct {
	Running     bool     `json:"running"`
	Servers     int      `json:"servers"`
	ActiveConns int      `json:"activeConns"`
	IdleConns   int      `json:"idleConns"`
	Processes   []string `json:"processes"`
}

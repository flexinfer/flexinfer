package hud

import (
	"context"

	"github.com/crb2nu/loom/internal/tui"
)

// tuiRun launches the bubbletea terminal UI dashboard with shared monitors.
// This is called from Run() when Config.TUI is true. The TUI reads from the
// HUD's monitors instead of creating its own daemon connection.
func tuiRun(deps tui.Deps, ctx context.Context) error {
	return tui.RunWithDeps(deps, ctx)
}

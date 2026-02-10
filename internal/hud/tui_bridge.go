package hud

import (
	"github.com/crb2nu/loom/internal/tui"
)

// tuiRun launches the bubbletea terminal UI dashboard.
// This is called from Run() when Config.TUI is true.
func tuiRun(socketPath string) error {
	return tui.Run(socketPath)
}

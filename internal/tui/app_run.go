package tui

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// RunWithDeps starts the TUI dashboard using externally-owned monitors.
// This is called when the HUD and TUI co-host: the HUD owns the daemon
// connection and monitors, so the TUI reads from shared cached snapshots.
func RunWithDeps(deps Deps, ctx context.Context) error {
	logger := newTUILogger().With("component", "tui")
	client := NewClientFromDeps(deps, logger)
	// No Start/Stop — monitors are externally managed.

	restoreStderr := redirectStderr()
	defer restoreStderr()

	model := New(client)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}

// Run starts the TUI dashboard. This is the main entry point called from the CLI.
// The provided context enables clean shutdown on external signals (SIGINT, SIGTERM, SIGHUP).
func Run(socketPath string, ctx context.Context) error {
	logger := newTUILogger().With("component", "tui")

	client, err := NewClient(socketPath, logger)
	if err != nil {
		return fmt.Errorf("create TUI client: %w", err)
	}
	client.Start()
	defer func() {
		// Timeout client.Stop() to avoid hanging if the daemon is unresponsive.
		done := make(chan struct{})
		go func() {
			client.Stop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}()

	// Redirect stderr and the standard log package to the TUI log file so
	// that daemon reconnection warnings, net package diagnostics, and any
	// other stray writes don't bleed through the alt-screen.
	restoreStderr := redirectStderr()
	defer restoreStderr()

	model := New(client)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}

// redirectStderr duplicates os.Stderr to the TUI log file so stray writes
// (net package warnings, mcp-go library output, runtime diagnostics) don't
// corrupt the bubbletea alt-screen.  Returns a function that restores the
// original stderr.
func redirectStderr() (restore func()) {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "loom", "logs")
	_ = os.MkdirAll(logDir, 0755)

	logFile, err := os.OpenFile(
		filepath.Join(logDir, "tui.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644,
	)
	if err != nil {
		return func() {}
	}

	// Save original stderr fd so we can restore it later.
	origFd, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		logFile.Close()
		return func() {}
	}

	// Point stderr fd at the log file.
	_ = syscall.Dup2(int(logFile.Fd()), int(os.Stderr.Fd()))

	// Also redirect Go's standard log package.
	prevLogOutput := log.Writer()
	log.SetOutput(logFile)

	return func() {
		_ = syscall.Dup2(origFd, int(os.Stderr.Fd()))
		_ = syscall.Close(origFd)
		log.SetOutput(prevLogOutput)
		logFile.Close()
	}
}

func newTUILogger() *slog.Logger {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "loom", "logs")
	_ = os.MkdirAll(logDir, 0755)

	// Never write logs to stderr/stdout while bubbletea is running; it corrupts the UI.
	f, err := os.OpenFile(filepath.Join(logDir, "tui.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{}))
}

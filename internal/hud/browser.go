package hud

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// newHUDTUILogger creates a logger that writes to ~/.config/loom/logs/tui.log.
// Used in TUI mode so HUD log output doesn't corrupt the alt-screen.
func newHUDTUILogger() *slog.Logger {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "loom", "logs")
	_ = os.MkdirAll(logDir, 0755)

	f, err := os.OpenFile(filepath.Join(logDir, "tui.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{}))
}

// openBrowser attempts to open a URL in the default browser.
func openBrowser(url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cancel()
		return
	}
	go func() {
		defer cancel()
		_ = cmd.Run()
	}()
}

func browserURL(scheme, bindAddr string, addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return scheme + "://" + addr.String()
	}
	return scheme + "://" + net.JoinHostPort(browserHost(bindAddr, host), port)
}

func browserHost(bindAddr, listenHost string) string {
	host := strings.Trim(strings.TrimSpace(bindAddr), "[]")
	if host == "" {
		host = strings.Trim(strings.TrimSpace(listenHost), "[]")
	}

	switch host {
	case "", "0.0.0.0", "::", "*":
		return "127.0.0.1"
	case "localhost", "127.0.0.1", "::1":
		return host
	default:
		return host
	}
}

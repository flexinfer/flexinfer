// Package portforward manages kubectl port-forward subprocesses for MCP servers
// that need to access Kubernetes services.
package portforward

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Config describes a kubectl port-forward target.
type Config struct {
	// Namespace is the Kubernetes namespace (e.g., "monitoring").
	Namespace string
	// Service is the service name (e.g., "svc/kube-prometheus-stack-grafana").
	Service string
	// LocalPort is the local port to forward to.
	LocalPort int
	// RemotePort is the remote service port.
	RemotePort int
	// HostPrefixes are additional hostname prefixes that indicate
	// port forwarding is needed (beyond the standard .svc.cluster.local check).
	HostPrefixes []string
}

// PortForwarder manages a single kubectl port-forward subprocess.
type PortForwarder struct {
	config  Config
	enabled bool

	mu  sync.Mutex
	cmd *exec.Cmd
}

// New creates a PortForwarder. If enabled is false, EnsureRunning is a no-op.
func New(cfg Config, enabled bool) *PortForwarder {
	return &PortForwarder{
		config:  cfg,
		enabled: enabled,
	}
}

// EnsureRunning checks if port forwarding is needed for the given serviceURL
// and starts it if necessary. Returns the effective URL to use for requests:
// either the original serviceURL or a localhost URL if port forwarding is active.
func (pf *PortForwarder) EnsureRunning(serviceURL string) string {
	if !pf.enabled {
		return serviceURL
	}

	u, err := url.Parse(serviceURL)
	if err != nil {
		return serviceURL
	}

	if !pf.needsPortForward(u.Hostname()) {
		return serviceURL
	}

	pf.mu.Lock()
	defer pf.mu.Unlock()

	// Already running?
	if pf.isRunningLocked() {
		return pf.localURL(u)
	}

	// Start port-forward
	mapping := fmt.Sprintf("%d:%d", pf.config.LocalPort, pf.config.RemotePort)
	cmd := exec.Command("kubectl", "-n", pf.config.Namespace, "port-forward", pf.config.Service, mapping) //nolint:noctx // background port-forward managed separately
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return serviceURL
	}

	pf.cmd = cmd

	go func(cmd *exec.Cmd) {
		_ = cmd.Wait()
		pf.mu.Lock()
		if pf.cmd == cmd {
			pf.cmd = nil
		}
		pf.mu.Unlock()
	}(cmd)

	// Give it a moment to establish
	time.Sleep(500 * time.Millisecond)

	return pf.localURL(u)
}

// Cleanup kills the port-forward process if running.
func (pf *PortForwarder) Cleanup() {
	pf.mu.Lock()
	cmd := pf.cmd
	pf.cmd = nil
	pf.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// IsRunning returns true if the port-forward process is still alive.
func (pf *PortForwarder) IsRunning() bool {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return pf.isRunningLocked()
}

func (pf *PortForwarder) isRunningLocked() bool {
	if pf.cmd == nil || pf.cmd.Process == nil {
		return false
	}
	return pf.cmd.Process.Signal(syscall.Signal(0)) == nil
}

func (pf *PortForwarder) localURL(original *url.URL) string {
	u := *original
	u.Host = fmt.Sprintf("127.0.0.1:%d", pf.config.LocalPort)
	u.Scheme = "http"
	return u.String()
}

func (pf *PortForwarder) needsPortForward(host string) bool {
	return NeedsPortForward(host, pf.config.HostPrefixes)
}

// NeedsPortForward returns true if the given hostname looks like a
// Kubernetes in-cluster service name. It checks for standard suffixes
// (.svc.cluster.local, .svc) and any additional host prefixes.
func NeedsPortForward(host string, extraPrefixes []string) bool {
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}
	if strings.HasSuffix(host, ".svc.cluster.local") || strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".cluster.local") {
		return true
	}
	for _, prefix := range extraPrefixes {
		if strings.HasPrefix(host, prefix) {
			return true
		}
	}
	// Heuristic: a single-label host (no dots) is likely an in-cluster DNS name.
	return !strings.Contains(host, ".")
}

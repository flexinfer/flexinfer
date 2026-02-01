// Package daemon provides the TunnelManager for SSH tunnel lifecycle management.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"

	"github.com/crb2nu/loom/pkg/tunnel"
)

// TunnelState represents the state of an SSH tunnel.
type TunnelState string

const (
	TunnelStateDisconnected TunnelState = "disconnected"
	TunnelStateConnecting   TunnelState = "connecting"
	TunnelStateConnected    TunnelState = "connected"
	TunnelStateReconnecting TunnelState = "reconnecting"
	TunnelStateFailed       TunnelState = "failed"
)

// TunnelStatus holds the current status of a tunnel.
type TunnelStatus struct {
	ServerName     string        `json:"serverName"`
	State          TunnelState   `json:"state"`
	LocalAddr      string        `json:"localAddr,omitempty"`
	RemoteHost     string        `json:"remoteHost,omitempty"`
	ConnectedAt    time.Time     `json:"connectedAt,omitempty"`
	LastError      string        `json:"lastError,omitempty"`
	ReconnectCount int           `json:"reconnectCount"`
	Uptime         time.Duration `json:"uptime,omitempty"`
}

// TunnelManagerConfig configures the TunnelManager.
type TunnelManagerConfig struct {
	// ConnectTimeout is the timeout for establishing a tunnel.
	ConnectTimeout time.Duration

	// ReconnectInterval is the base interval between reconnection attempts.
	ReconnectInterval time.Duration

	// MaxReconnectInterval is the maximum backoff for reconnection.
	MaxReconnectInterval time.Duration

	// MaxReconnectAttempts is the max attempts before giving up (0 = infinite).
	MaxReconnectAttempts int
}

// DefaultTunnelManagerConfig returns sensible defaults.
func DefaultTunnelManagerConfig() TunnelManagerConfig {
	return TunnelManagerConfig{
		ConnectTimeout:       30 * time.Second,
		ReconnectInterval:    5 * time.Second,
		MaxReconnectInterval: 5 * time.Minute,
		MaxReconnectAttempts: 0, // Infinite
	}
}

// managedTunnel holds a tunnel and its metadata.
type managedTunnel struct {
	serverName string
	sshSpec    *registry.SSHSpec
	tunnel     *tunnel.SSHTunnel
	listener   net.Listener
	localAddr  string
	remoteAddr string

	status   TunnelStatus
	statusMu sync.RWMutex

	cancel context.CancelFunc
}

func (m *managedTunnel) getStatus() TunnelStatus {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	status := m.status
	if m.status.State == TunnelStateConnected && !m.status.ConnectedAt.IsZero() {
		status.Uptime = time.Since(m.status.ConnectedAt)
	}
	return status
}

func (m *managedTunnel) setState(state TunnelState, err error) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.status.State = state
	if err != nil {
		m.status.LastError = err.Error()
	}
	if state == TunnelStateConnected {
		m.status.ConnectedAt = time.Now()
		m.status.LastError = ""
	}
}

func (m *managedTunnel) incrementReconnect() {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.status.ReconnectCount++
}

// TunnelManager manages SSH tunnels for remote MCP servers.
type TunnelManager struct {
	cfg      TunnelManagerConfig
	logger   *slog.Logger
	tunnels  map[string]*managedTunnel
	tunnelMu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewTunnelManager creates a new TunnelManager.
func NewTunnelManager(cfg TunnelManagerConfig, logger *slog.Logger) *TunnelManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &TunnelManager{
		cfg:     cfg,
		logger:  logger,
		tunnels: make(map[string]*managedTunnel),
	}
}

// Start starts the tunnel manager.
func (tm *TunnelManager) Start(ctx context.Context) {
	tm.ctx, tm.cancel = context.WithCancel(ctx)
	tm.logger.Info("tunnel manager started")
}

// Stop stops all tunnels and the manager.
func (tm *TunnelManager) Stop() {
	if tm.cancel != nil {
		tm.cancel()
	}

	tm.tunnelMu.Lock()
	for name, mt := range tm.tunnels {
		tm.logger.Info("stopping tunnel", "server", name)
		if mt.cancel != nil {
			mt.cancel()
		}
	}
	tm.tunnelMu.Unlock()

	tm.wg.Wait()
	tm.logger.Info("tunnel manager stopped")
}

// AddTunnel adds and starts a tunnel for a server with SSH configuration.
func (tm *TunnelManager) AddTunnel(serverName string, sshSpec *registry.SSHSpec, localPort int, remoteAddr string) error {
	if sshSpec == nil {
		return fmt.Errorf("SSH spec is nil")
	}
	if sshSpec.Host == "" {
		return fmt.Errorf("SSH host is required")
	}

	tm.tunnelMu.Lock()
	if _, exists := tm.tunnels[serverName]; exists {
		tm.tunnelMu.Unlock()
		return fmt.Errorf("tunnel already exists for server: %s", serverName)
	}
	tm.tunnelMu.Unlock()

	// Convert registry.SSHSpec to tunnel.SSHConfig
	sshCfg := tm.specToConfig(sshSpec)

	// Create local address
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)

	// Create managed tunnel
	mt := &managedTunnel{
		serverName: serverName,
		sshSpec:    sshSpec,
		tunnel:     tunnel.NewSSHTunnel(sshCfg),
		localAddr:  localAddr,
		remoteAddr: remoteAddr,
		status: TunnelStatus{
			ServerName: serverName,
			State:      TunnelStateDisconnected,
			LocalAddr:  localAddr,
			RemoteHost: sshSpec.Host,
		},
	}

	// Create tunnel context
	tunnelCtx, tunnelCancel := context.WithCancel(tm.ctx)
	mt.cancel = tunnelCancel

	tm.tunnelMu.Lock()
	tm.tunnels[serverName] = mt
	tm.tunnelMu.Unlock()

	// Start tunnel in background
	tm.wg.Add(1)
	go tm.runTunnel(tunnelCtx, mt)

	return nil
}

// RemoveTunnel stops and removes a tunnel.
func (tm *TunnelManager) RemoveTunnel(serverName string) error {
	tm.tunnelMu.Lock()
	mt, exists := tm.tunnels[serverName]
	if !exists {
		tm.tunnelMu.Unlock()
		return fmt.Errorf("tunnel not found: %s", serverName)
	}
	delete(tm.tunnels, serverName)
	tm.tunnelMu.Unlock()

	if mt.cancel != nil {
		mt.cancel()
	}

	return nil
}

// GetStatus returns the status of a specific tunnel.
func (tm *TunnelManager) GetStatus(serverName string) (*TunnelStatus, error) {
	tm.tunnelMu.RLock()
	mt, exists := tm.tunnels[serverName]
	tm.tunnelMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("tunnel not found: %s", serverName)
	}

	status := mt.getStatus()
	return &status, nil
}

// GetAllStatuses returns status for all tunnels.
func (tm *TunnelManager) GetAllStatuses() map[string]*TunnelStatus {
	tm.tunnelMu.RLock()
	defer tm.tunnelMu.RUnlock()

	result := make(map[string]*TunnelStatus, len(tm.tunnels))
	for name, mt := range tm.tunnels {
		status := mt.getStatus()
		result[name] = &status
	}
	return result
}

// GetLocalAddr returns the local address for a tunnel, if connected.
func (tm *TunnelManager) GetLocalAddr(serverName string) (string, error) {
	tm.tunnelMu.RLock()
	mt, exists := tm.tunnels[serverName]
	tm.tunnelMu.RUnlock()

	if !exists {
		return "", fmt.Errorf("tunnel not found: %s", serverName)
	}

	status := mt.getStatus()
	if status.State != TunnelStateConnected {
		return "", fmt.Errorf("tunnel not connected: %s", status.State)
	}

	return mt.localAddr, nil
}

// runTunnel manages the lifecycle of a single tunnel with reconnection.
func (tm *TunnelManager) runTunnel(ctx context.Context, mt *managedTunnel) {
	defer tm.wg.Done()

	backoff := tm.cfg.ReconnectInterval

	for {
		select {
		case <-ctx.Done():
			tm.closeTunnel(mt)
			return
		default:
		}

		// Attempt connection
		mt.setState(TunnelStateConnecting, nil)
		tm.logger.Info("connecting tunnel", "server", mt.serverName, "host", mt.sshSpec.Host)

		err := tm.connectTunnel(ctx, mt)
		if err != nil {
			mt.setState(TunnelStateFailed, err)
			tm.logger.Error("tunnel connection failed", "server", mt.serverName, "error", err)

			// Check if we should retry
			if tm.cfg.MaxReconnectAttempts > 0 {
				mt.statusMu.RLock()
				attempts := mt.status.ReconnectCount
				mt.statusMu.RUnlock()
				if attempts >= tm.cfg.MaxReconnectAttempts {
					tm.logger.Error("max reconnect attempts reached", "server", mt.serverName)
					return
				}
			}

			// Wait with backoff before retrying
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				mt.incrementReconnect()
				mt.setState(TunnelStateReconnecting, nil)
				// Increase backoff
				backoff = backoff * 2
				if backoff > tm.cfg.MaxReconnectInterval {
					backoff = tm.cfg.MaxReconnectInterval
				}
				continue
			}
		}

		// Connection successful
		mt.setState(TunnelStateConnected, nil)
		tm.logger.Info("tunnel connected", "server", mt.serverName, "local", mt.localAddr)

		// Wait for context cancellation or connection loss
		// The tunnel will be monitored by keepalive, we just wait here
		<-ctx.Done()
		tm.closeTunnel(mt)
		return
	}
}

// connectTunnel establishes the SSH tunnel and local port forward.
func (tm *TunnelManager) connectTunnel(ctx context.Context, mt *managedTunnel) error {
	// Create timeout context for connection
	connectCtx, cancel := context.WithTimeout(ctx, tm.cfg.ConnectTimeout)
	defer cancel()

	// Connect SSH
	if err := mt.tunnel.Connect(connectCtx); err != nil {
		return fmt.Errorf("SSH connect: %w", err)
	}

	// Set up local port forward
	listener, err := mt.tunnel.ForwardLocalPort(ctx, mt.localAddr, mt.remoteAddr)
	if err != nil {
		mt.tunnel.Close()
		return fmt.Errorf("port forward: %w", err)
	}
	mt.listener = listener

	return nil
}

// closeTunnel closes the tunnel and listener.
func (tm *TunnelManager) closeTunnel(mt *managedTunnel) {
	if mt.listener != nil {
		mt.listener.Close()
		mt.listener = nil
	}
	if mt.tunnel != nil {
		mt.tunnel.Close()
	}
	mt.setState(TunnelStateDisconnected, nil)
	tm.logger.Info("tunnel closed", "server", mt.serverName)
}

// specToConfig converts registry.SSHSpec to tunnel.SSHConfig.
func (tm *TunnelManager) specToConfig(spec *registry.SSHSpec) tunnel.SSHConfig {
	cfg := tunnel.DefaultSSHConfig()
	cfg.Host = spec.Host
	cfg.User = spec.User
	cfg.KeyFile = spec.KeyFile
	cfg.KnownHostsFile = spec.KnownHostsFile

	if spec.UseAgent != nil {
		cfg.UseAgent = *spec.UseAgent
	}
	if spec.StrictHostKeyChecking != nil {
		cfg.StrictHostKeyChecking = *spec.StrictHostKeyChecking
	}
	if spec.ConnectTimeout > 0 {
		cfg.ConnectTimeout = time.Duration(spec.ConnectTimeout) * time.Second
	}

	return cfg
}

// TunnelCount returns the number of managed tunnels.
func (tm *TunnelManager) TunnelCount() int {
	tm.tunnelMu.RLock()
	defer tm.tunnelMu.RUnlock()
	return len(tm.tunnels)
}

// ConnectedCount returns the number of connected tunnels.
func (tm *TunnelManager) ConnectedCount() int {
	tm.tunnelMu.RLock()
	defer tm.tunnelMu.RUnlock()
	count := 0
	for _, mt := range tm.tunnels {
		if mt.getStatus().State == TunnelStateConnected {
			count++
		}
	}
	return count
}

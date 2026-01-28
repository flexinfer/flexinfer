// Package tunnel provides secure tunneling for remote MCP server connections.
package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHConfig configures an SSH tunnel connection.
type SSHConfig struct {
	// Host is the SSH server address (host:port)
	Host string

	// User is the SSH username
	User string

	// KeyFile is the path to the private key file (optional if using agent)
	KeyFile string

	// KeyPassphrase is the passphrase for encrypted keys (optional)
	KeyPassphrase string

	// UseAgent enables SSH agent authentication
	UseAgent bool

	// KnownHostsFile is the path to known_hosts file (optional, uses default if empty)
	KnownHostsFile string

	// StrictHostKeyChecking enables host key verification (default: true)
	StrictHostKeyChecking bool

	// ConnectTimeout is the connection timeout (default: 30s)
	ConnectTimeout time.Duration

	// KeepAliveInterval is the keepalive interval (default: 30s, 0 disables)
	KeepAliveInterval time.Duration
}

// DefaultSSHConfig returns a config with sensible defaults.
func DefaultSSHConfig() SSHConfig {
	return SSHConfig{
		UseAgent:              true,
		StrictHostKeyChecking: true,
		ConnectTimeout:        30 * time.Second,
		KeepAliveInterval:     30 * time.Second,
	}
}

// SSHTunnel manages an SSH connection that can spawn remote processes.
type SSHTunnel struct {
	cfg    SSHConfig
	client *ssh.Client
	mu     sync.Mutex
}

// NewSSHTunnel creates a new SSH tunnel manager.
func NewSSHTunnel(cfg SSHConfig) *SSHTunnel {
	return &SSHTunnel{cfg: cfg}
}

// Connect establishes the SSH connection.
func (t *SSHTunnel) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.client != nil {
		return nil // Already connected
	}

	authMethods, err := t.buildAuthMethods()
	if err != nil {
		return fmt.Errorf("build auth methods: %w", err)
	}

	hostKeyCallback, err := t.buildHostKeyCallback()
	if err != nil {
		return fmt.Errorf("build host key callback: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            t.cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         t.cfg.ConnectTimeout,
	}

	host := t.cfg.Host
	if !strings.Contains(host, ":") {
		host = host + ":22"
	}

	// Use context for connection timeout
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("dial %s: %w", host, err)
	}

	// Perform SSH handshake
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, host, config)
	if err != nil {
		conn.Close()
		return fmt.Errorf("ssh handshake: %w", err)
	}

	t.client = ssh.NewClient(sshConn, chans, reqs)

	// Start keepalive if configured
	if t.cfg.KeepAliveInterval > 0 {
		go t.keepAlive(ctx)
	}

	return nil
}

// Close closes the SSH connection.
func (t *SSHTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.client != nil {
		err := t.client.Close()
		t.client = nil
		return err
	}
	return nil
}

// SpawnProcess starts a remote process and returns its stdin/stdout.
// The command is executed on the remote host via SSH.
func (t *SSHTunnel) SpawnProcess(ctx context.Context, command string) (io.WriteCloser, io.ReadCloser, error) {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()

	if client == nil {
		return nil, nil, fmt.Errorf("ssh tunnel not connected")
	}

	session, err := client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("create session: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Capture stderr for debugging
	session.Stderr = os.Stderr

	if err := session.Start(command); err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("start command: %w", err)
	}

	// Wrap stdin to close session when done
	wrappedStdin := &sessionWriter{
		WriteCloser: stdin,
		session:     session,
	}

	return wrappedStdin, io.NopCloser(stdout), nil
}

// ForwardLocalPort creates a local port forward to a remote address.
// Returns the local listener address.
func (t *SSHTunnel) ForwardLocalPort(ctx context.Context, localAddr, remoteAddr string) (net.Listener, error) {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()

	if client == nil {
		return nil, fmt.Errorf("ssh tunnel not connected")
	}

	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return nil, fmt.Errorf("listen local: %w", err)
	}

	go func() {
		for {
			localConn, err := listener.Accept()
			if err != nil {
				return // Listener closed
			}

			remoteConn, err := client.Dial("tcp", remoteAddr)
			if err != nil {
				localConn.Close()
				continue
			}

			// Bidirectional copy
			go func() {
				defer localConn.Close()
				defer remoteConn.Close()
				go io.Copy(localConn, remoteConn)
				io.Copy(remoteConn, localConn)
			}()
		}
	}()

	return listener, nil
}

func (t *SSHTunnel) buildAuthMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try SSH agent first
	if t.cfg.UseAgent {
		if agentConn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
			agentClient := agent.NewClient(agentConn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	// Try key file
	if t.cfg.KeyFile != "" {
		keyPath := expandPath(t.cfg.KeyFile)
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read key file: %w", err)
		}

		var signer ssh.Signer
		if t.cfg.KeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(t.cfg.KeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyData)
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// Try default key locations
	if len(methods) == 0 {
		for _, keyName := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
			keyPath := filepath.Join(os.Getenv("HOME"), ".ssh", keyName)
			if keyData, err := os.ReadFile(keyPath); err == nil {
				if signer, err := ssh.ParsePrivateKey(keyData); err == nil {
					methods = append(methods, ssh.PublicKeys(signer))
					break
				}
			}
		}
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no authentication methods available")
	}

	return methods, nil
}

func (t *SSHTunnel) buildHostKeyCallback() (ssh.HostKeyCallback, error) {
	if !t.cfg.StrictHostKeyChecking {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	knownHostsPath := t.cfg.KnownHostsFile
	if knownHostsPath == "" {
		knownHostsPath = filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
	}
	knownHostsPath = expandPath(knownHostsPath)

	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		// If known_hosts doesn't exist, create permissive callback that logs
		if os.IsNotExist(err) {
			return ssh.InsecureIgnoreHostKey(), nil
		}
		return nil, fmt.Errorf("parse known_hosts: %w", err)
	}

	return callback, nil
}

func (t *SSHTunnel) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(t.cfg.KeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.mu.Lock()
			client := t.client
			t.mu.Unlock()

			if client == nil {
				return
			}

			// Send keepalive request
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				return // Connection lost
			}
		}
	}
}

// sessionWriter wraps a write closer and closes the session on close.
type sessionWriter struct {
	io.WriteCloser
	session *ssh.Session
}

func (w *sessionWriter) Close() error {
	w.WriteCloser.Close()
	return w.session.Close()
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		return filepath.Join(home, path[2:])
	}
	return path
}

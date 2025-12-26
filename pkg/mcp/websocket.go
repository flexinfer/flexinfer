package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketTransport implements MCP transport over WebSocket.
type WebSocketTransport struct {
	conn        *websocket.Conn
	serverName  string
	profile     string
	initialized bool
	mu          sync.Mutex
	readMu      sync.Mutex
}

// HubClientConfig configures the hub WebSocket client.
type HubClientConfig struct {
	URL                  string
	Profile              string
	CFAccessClientID     string
	CFAccessClientSecret string
	ConnectTimeout       time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
}

// NewWebSocketTransport creates a WebSocket transport to the hub.
func NewWebSocketTransport(ctx context.Context, cfg HubClientConfig, serverName string) (*WebSocketTransport, error) {
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}

	// Build URL with server query param
	url := cfg.URL
	if serverName != "" {
		url = fmt.Sprintf("%s?server=%s", cfg.URL, serverName)
	}
	if cfg.Profile != "" {
		if serverName != "" {
			url = fmt.Sprintf("%s&profile=%s", url, cfg.Profile)
		} else {
			url = fmt.Sprintf("%s?profile=%s", url, cfg.Profile)
		}
	}

	// Build headers
	header := http.Header{}
	if cfg.CFAccessClientID != "" && cfg.CFAccessClientSecret != "" {
		header.Set("CF-Access-Client-Id", cfg.CFAccessClientID)
		header.Set("CF-Access-Client-Secret", cfg.CFAccessClientSecret)
	}

	// Create dialer with timeout
	dialer := websocket.Dialer{
		HandshakeTimeout: cfg.ConnectTimeout,
	}

	conn, _, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		return nil, fmt.Errorf("connect to hub: %w", err)
	}

	return &WebSocketTransport{
		conn:       conn,
		serverName: serverName,
		profile:    cfg.Profile,
	}, nil
}

// Send sends a message over WebSocket.
func (t *WebSocketTransport) Send(ctx context.Context, msg *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	if err := t.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("write message: %w", err)
	}

	return nil
}

// Recv receives a message from WebSocket.
func (t *WebSocketTransport) Recv(ctx context.Context) (*Message, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()

	_, data, err := t.conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}

	return &msg, nil
}

// Close closes the WebSocket connection.
func (t *WebSocketTransport) Close() error {
	return t.conn.Close()
}

// Initialize performs the MCP initialization handshake.
func (t *WebSocketTransport) Initialize(ctx context.Context) error {
	if t.initialized {
		return nil
	}

	// Send initialize request
	initReq, err := NewRequest(1, "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    Capabilities{},
		ClientInfo:      ClientInfo{Name: "loom-hub-client", Version: "0.2.0"},
	})
	if err != nil {
		return fmt.Errorf("create init request: %w", err)
	}

	if err := t.Send(ctx, initReq); err != nil {
		return fmt.Errorf("send init: %w", err)
	}

	// Receive init response
	resp, err := t.Recv(ctx)
	if err != nil {
		return fmt.Errorf("recv init: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("init error: %s", resp.Error.Message)
	}

	// Send initialized notification
	initNotif := &Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if err := t.Send(ctx, initNotif); err != nil {
		return fmt.Errorf("send initialized: %w", err)
	}

	t.initialized = true
	return nil
}

// IsInitialized returns whether the transport has been initialized.
func (t *WebSocketTransport) IsInitialized() bool {
	return t.initialized
}

// Ping sends a ping message to check connection health.
func (t *WebSocketTransport) Ping(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
}

// HubClient manages connections to the MCP hub.
type HubClient struct {
	cfg        HubClientConfig
	conns      map[string]*WebSocketTransport
	mu         sync.Mutex
	maxRetries int
}

// NewHubClient creates a new hub client.
func NewHubClient(cfg HubClientConfig) *HubClient {
	maxRetries := 3
	return &HubClient{
		cfg:        cfg,
		conns:      make(map[string]*WebSocketTransport),
		maxRetries: maxRetries,
	}
}

// GetConnection returns a connection for a server, creating and initializing one if needed.
func (c *HubClient) GetConnection(ctx context.Context, serverName string) (*WebSocketTransport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check for existing initialized connection
	if conn, ok := c.conns[serverName]; ok {
		if conn.IsInitialized() {
			return conn, nil
		}
		// Connection exists but not initialized, close and recreate
		conn.Close()
		delete(c.conns, serverName)
	}

	// Create new connection with retries
	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		conn, err := NewWebSocketTransport(ctx, c.cfg, serverName)
		if err != nil {
			lastErr = fmt.Errorf("connect attempt %d: %w", attempt+1, err)
			continue
		}

		// Initialize MCP protocol
		if err := conn.Initialize(ctx); err != nil {
			conn.Close()
			lastErr = fmt.Errorf("init attempt %d: %w", attempt+1, err)
			continue
		}

		c.conns[serverName] = conn
		return conn, nil
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", c.maxRetries, lastErr)
}

// Reconnect forces a reconnection for a specific server.
func (c *HubClient) Reconnect(ctx context.Context, serverName string) (*WebSocketTransport, error) {
	c.mu.Lock()
	if conn, ok := c.conns[serverName]; ok {
		conn.Close()
		delete(c.conns, serverName)
	}
	c.mu.Unlock()

	return c.GetConnection(ctx, serverName)
}

// CloseConnection closes a specific server connection.
func (c *HubClient) CloseConnection(serverName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if conn, ok := c.conns[serverName]; ok {
		conn.Close()
		delete(c.conns, serverName)
	}
}

// Close closes all connections.
func (c *HubClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for name, conn := range c.conns {
		conn.Close()
		delete(c.conns, name)
	}
	return nil
}

// Dial implements the pool.DialFunc interface for hub connections.
func (c *HubClient) Dial(ctx context.Context, serverName string) (Transport, error) {
	return c.GetConnection(ctx, serverName)
}

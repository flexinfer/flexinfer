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
	conn       *websocket.Conn
	serverName string
	profile    string
	mu         sync.Mutex
	readMu     sync.Mutex
}

// HubClientConfig configures the hub WebSocket client.
type HubClientConfig struct {
	URL                   string
	Profile               string
	CFAccessClientID      string
	CFAccessClientSecret  string
	ConnectTimeout        time.Duration
	ReadTimeout           time.Duration
	WriteTimeout          time.Duration
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

// HubClient manages connections to the MCP hub.
type HubClient struct {
	cfg    HubClientConfig
	conns  map[string]*WebSocketTransport
	mu     sync.Mutex
}

// NewHubClient creates a new hub client.
func NewHubClient(cfg HubClientConfig) *HubClient {
	return &HubClient{
		cfg:   cfg,
		conns: make(map[string]*WebSocketTransport),
	}
}

// GetConnection returns a connection for a server, creating one if needed.
func (c *HubClient) GetConnection(ctx context.Context, serverName string) (*WebSocketTransport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check for existing connection
	if conn, ok := c.conns[serverName]; ok {
		return conn, nil
	}

	// Create new connection
	conn, err := NewWebSocketTransport(ctx, c.cfg, serverName)
	if err != nil {
		return nil, err
	}

	c.conns[serverName] = conn
	return conn, nil
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

// Package pool provides connection pooling for MCP servers.
package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/mcp"
)

// Conn represents a pooled connection to an MCP server.
type Conn struct {
	ServerName string
	Transport  mcp.Transport
	CreatedAt  time.Time
	LastUsed   time.Time
	Healthy    bool
}

// Stats provides pool statistics.
type Stats struct {
	TotalConns  int
	ActiveConns int
	IdleConns   int
	Hits        int64
	Misses      int64
}

// Pool manages a pool of MCP server connections.
type Pool struct {
	maxIdle     int
	maxOpen     int
	idleTimeout time.Duration
	dialFunc    DialFunc
	mu          sync.Mutex
	conns       map[string][]*Conn
	activeCount map[string]int
	stats       Stats
	closed      bool
}

// DialFunc is a function that creates a new connection to a server.
type DialFunc func(ctx context.Context, serverName string) (mcp.Transport, error)

// Config configures the connection pool.
type Config struct {
	MaxIdle     int           // Maximum idle connections per server (default: 2)
	MaxOpen     int           // Maximum open connections per server (default: 10)
	IdleTimeout time.Duration // Idle connection timeout (default: 5m)
	DialFunc    DialFunc      // Function to dial new connections
}

// New creates a new connection pool.
func New(cfg Config) *Pool {
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = 2
	}
	if cfg.MaxOpen <= 0 {
		cfg.MaxOpen = 10
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}

	p := &Pool{
		maxIdle:     cfg.MaxIdle,
		maxOpen:     cfg.MaxOpen,
		idleTimeout: cfg.IdleTimeout,
		dialFunc:    cfg.DialFunc,
		conns:       make(map[string][]*Conn),
		activeCount: make(map[string]int),
	}

	// Start idle connection reaper
	go p.reapLoop()

	return p
}

// Get retrieves a connection from the pool, or creates a new one.
func (p *Pool) Get(ctx context.Context, serverName string) (*Conn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool is closed")
	}

	// Check for idle connection
	if conns := p.conns[serverName]; len(conns) > 0 {
		conn := conns[len(conns)-1]
		p.conns[serverName] = conns[:len(conns)-1]
		p.activeCount[serverName]++
		p.stats.Hits++
		p.stats.IdleConns--
		p.stats.ActiveConns++
		p.mu.Unlock()

		conn.LastUsed = time.Now()
		return conn, nil
	}

	// Check if we can create a new connection
	if p.activeCount[serverName] >= p.maxOpen {
		p.mu.Unlock()
		return nil, fmt.Errorf("max connections reached for %s", serverName)
	}

	p.activeCount[serverName]++
	p.stats.Misses++
	p.stats.TotalConns++
	p.stats.ActiveConns++
	p.mu.Unlock()

	// Create new connection
	transport, err := p.dialFunc(ctx, serverName)
	if err != nil {
		p.mu.Lock()
		p.activeCount[serverName]--
		p.stats.ActiveConns--
		p.mu.Unlock()
		return nil, fmt.Errorf("dial %s: %w", serverName, err)
	}

	return &Conn{
		ServerName: serverName,
		Transport:  transport,
		CreatedAt:  time.Now(),
		LastUsed:   time.Now(),
		Healthy:    true,
	}, nil
}

// Put returns a connection to the pool.
func (p *Pool) Put(conn *Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || !conn.Healthy {
		p.activeCount[conn.ServerName]--
		p.stats.ActiveConns--
		conn.Transport.Close()
		return
	}

	// Check if we have room in idle pool
	if len(p.conns[conn.ServerName]) >= p.maxIdle {
		p.activeCount[conn.ServerName]--
		p.stats.ActiveConns--
		conn.Transport.Close()
		return
	}

	conn.LastUsed = time.Now()
	p.conns[conn.ServerName] = append(p.conns[conn.ServerName], conn)
	p.activeCount[conn.ServerName]--
	p.stats.ActiveConns--
	p.stats.IdleConns++
}

// Close closes the pool and all connections.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	for _, conns := range p.conns {
		for _, conn := range conns {
			conn.Transport.Close()
		}
	}
	p.conns = nil
	return nil
}

// Stats returns pool statistics.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// reapLoop periodically closes idle connections.
func (p *Pool) reapLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}

		now := time.Now()
		for serverName, conns := range p.conns {
			var keep []*Conn
			for _, conn := range conns {
				if now.Sub(conn.LastUsed) > p.idleTimeout {
					conn.Transport.Close()
					p.stats.IdleConns--
				} else {
					keep = append(keep, conn)
				}
			}
			p.conns[serverName] = keep
		}
		p.mu.Unlock()
	}
}

// WarmUp pre-establishes connections to the specified servers.
func (p *Pool) WarmUp(ctx context.Context, servers []string) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(servers))

	for _, server := range servers {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			conn, err := p.Get(ctx, s)
			if err != nil {
				errCh <- fmt.Errorf("warm up %s: %w", s, err)
				return
			}
			p.Put(conn)
		}(server)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("warm up failed: %v", errs)
	}
	return nil
}

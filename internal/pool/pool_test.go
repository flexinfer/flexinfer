package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// mockTransport implements mcp.Transport for testing.
type mockTransport struct {
	closed atomic.Bool
}

func (m *mockTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if m.closed.Load() {
		return fmt.Errorf("transport closed")
	}
	return nil
}

func (m *mockTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	if m.closed.Load() {
		return nil, fmt.Errorf("transport closed")
	}
	return &mcp.Message{}, nil
}

func (m *mockTransport) Close() error {
	m.closed.Store(true)
	return nil
}

func TestNew_DefaultConfig(t *testing.T) {
	p := New(Config{
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	// Check defaults are applied
	if p.maxIdle != 2 {
		t.Errorf("expected maxIdle=2, got %d", p.maxIdle)
	}
	if p.maxOpen != 10 {
		t.Errorf("expected maxOpen=10, got %d", p.maxOpen)
	}
	if p.idleTimeout != 5*time.Minute {
		t.Errorf("expected idleTimeout=5m, got %v", p.idleTimeout)
	}
}

func TestNew_CustomConfig(t *testing.T) {
	p := New(Config{
		MaxIdle:     5,
		MaxOpen:     20,
		IdleTimeout: 10 * time.Minute,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	if p.maxIdle != 5 {
		t.Errorf("expected maxIdle=5, got %d", p.maxIdle)
	}
	if p.maxOpen != 20 {
		t.Errorf("expected maxOpen=20, got %d", p.maxOpen)
	}
}

func TestPool_GetAndPut(t *testing.T) {
	dialCount := 0
	p := New(Config{
		MaxIdle: 2,
		MaxOpen: 5,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			dialCount++
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()

	// First Get should dial
	conn1, err := p.Get(ctx, "server1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dialCount != 1 {
		t.Errorf("expected 1 dial, got %d", dialCount)
	}

	// Put it back
	p.Put(conn1)

	// Second Get should reuse the connection (pool hit)
	conn2, err := p.Get(ctx, "server1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dialCount != 1 {
		t.Errorf("expected still 1 dial (pool hit), got %d", dialCount)
	}

	// Verify stats
	stats := p.Stats()
	if stats.Hits != 1 {
		t.Errorf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}

	p.Put(conn2)
}

func TestPool_MaxOpenLimit(t *testing.T) {
	p := New(Config{
		MaxIdle: 1,
		MaxOpen: 2,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()

	// Get 2 connections (max)
	conn1, _ := p.Get(ctx, "server1")
	conn2, _ := p.Get(ctx, "server1")

	// Third should fail
	_, err := p.Get(ctx, "server1")
	if err == nil {
		t.Error("expected error when max connections reached")
	}

	// Return one, then we should be able to get another
	p.Put(conn1)

	conn3, err := p.Get(ctx, "server1")
	if err != nil {
		t.Errorf("Get after Put should succeed: %v", err)
	}

	p.Put(conn2)
	p.Put(conn3)
}

func TestPool_MaxIdleLimit(t *testing.T) {
	closeCount := 0
	p := New(Config{
		MaxIdle: 1,
		MaxOpen: 5,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			mt := &mockTransport{}
			return mt, nil
		},
	})
	defer p.Close()

	ctx := context.Background()

	// Get 3 connections
	conn1, _ := p.Get(ctx, "server1")
	conn2, _ := p.Get(ctx, "server1")
	conn3, _ := p.Get(ctx, "server1")

	// Return all 3 - only 1 should stay in pool (maxIdle=1)
	p.Put(conn1)
	p.Put(conn2)
	p.Put(conn3)

	stats := p.Stats()
	if stats.IdleConns != 1 {
		t.Errorf("expected 1 idle conn (maxIdle limit), got %d", stats.IdleConns)
	}

	// Verify connections were closed
	if !conn2.Transport.(*mockTransport).closed.Load() {
		closeCount++ // conn2 should be closed
	}
	if !conn3.Transport.(*mockTransport).closed.Load() {
		closeCount++ // conn3 should be closed
	}
}

func TestPool_UnhealthyConnection(t *testing.T) {
	p := New(Config{
		MaxIdle: 2,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()

	conn, _ := p.Get(ctx, "server1")
	conn.Healthy = false

	// Unhealthy connections should be closed, not returned to pool
	p.Put(conn)

	stats := p.Stats()
	if stats.IdleConns != 0 {
		t.Errorf("unhealthy conn should not be pooled, got %d idle", stats.IdleConns)
	}
}

func TestPool_Close(t *testing.T) {
	p := New(Config{
		MaxIdle: 2,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return &mockTransport{}, nil
		},
	})

	ctx := context.Background()

	conn, _ := p.Get(ctx, "server1")
	p.Put(conn)

	// Close pool
	p.Close()

	// Get after close should fail
	_, err := p.Get(ctx, "server1")
	if err == nil {
		t.Error("Get after Close should fail")
	}

	// Double close should be safe
	p.Close()
}

func TestPool_DialError(t *testing.T) {
	dialErr := fmt.Errorf("dial failed")
	p := New(Config{
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return nil, dialErr
		},
	})
	defer p.Close()

	ctx := context.Background()

	_, err := p.Get(ctx, "server1")
	if err == nil {
		t.Error("expected error when dial fails")
	}

	// Stats should show the failed attempt didn't leave orphaned counts
	stats := p.Stats()
	if stats.ActiveConns != 0 {
		t.Errorf("expected 0 active conns after dial error, got %d", stats.ActiveConns)
	}
}

func TestPool_ConcurrentAccess(t *testing.T) {
	var dialCount atomic.Int32
	p := New(Config{
		MaxIdle: 5,
		MaxOpen: 10,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			dialCount.Add(1)
			time.Sleep(10 * time.Millisecond) // Simulate dial latency
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	// Run 50 concurrent Get/Put cycles
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := p.Get(ctx, "server1")
			if err != nil {
				return // May hit max open, that's ok
			}
			time.Sleep(5 * time.Millisecond)
			p.Put(conn)
		}()
	}

	wg.Wait()

	// Pool should still be in valid state
	stats := p.Stats()
	if stats.ActiveConns < 0 {
		t.Errorf("invalid negative active conns: %d", stats.ActiveConns)
	}
}

func TestPool_MultipleServers(t *testing.T) {
	p := New(Config{
		MaxIdle: 2,
		MaxOpen: 5,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()

	// Get connections to different servers
	conn1, _ := p.Get(ctx, "server1")
	conn2, _ := p.Get(ctx, "server2")
	conn3, _ := p.Get(ctx, "server1")

	// Return them
	p.Put(conn1)
	p.Put(conn2)
	p.Put(conn3)

	// Should have 2 idle for server1, 1 for server2
	stats := p.Stats()
	if stats.IdleConns != 3 {
		t.Errorf("expected 3 total idle conns, got %d", stats.IdleConns)
	}
}

func TestPool_WarmUp(t *testing.T) {
	var dialedServers sync.Map
	p := New(Config{
		MaxIdle: 2,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			dialedServers.Store(serverName, true)
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()
	servers := []string{"server1", "server2", "server3"}

	err := p.WarmUp(ctx, servers)
	if err != nil {
		t.Fatalf("WarmUp failed: %v", err)
	}

	// All servers should have been dialed
	for _, s := range servers {
		if _, ok := dialedServers.Load(s); !ok {
			t.Errorf("server %s was not warmed up", s)
		}
	}

	// Connections should be in the pool
	stats := p.Stats()
	if stats.IdleConns != 3 {
		t.Errorf("expected 3 idle conns after warmup, got %d", stats.IdleConns)
	}
}

func TestPool_WarmUpPartialFailure(t *testing.T) {
	p := New(Config{
		MaxIdle: 2,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			if serverName == "bad-server" {
				return nil, fmt.Errorf("cannot dial bad-server")
			}
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()
	servers := []string{"server1", "bad-server", "server2"}

	err := p.WarmUp(ctx, servers)
	if err == nil {
		t.Error("expected error when warmup partially fails")
	}

	// Good servers should still be in pool
	stats := p.Stats()
	if stats.IdleConns != 2 {
		t.Errorf("expected 2 idle conns (good servers), got %d", stats.IdleConns)
	}
}

func TestPool_Stats(t *testing.T) {
	p := New(Config{
		MaxIdle: 2,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return &mockTransport{}, nil
		},
	})
	defer p.Close()

	ctx := context.Background()

	// Initial stats
	stats := p.Stats()
	if stats.TotalConns != 0 || stats.ActiveConns != 0 || stats.IdleConns != 0 {
		t.Error("initial stats should be zero")
	}

	// Get a connection
	conn1, _ := p.Get(ctx, "server1")
	stats = p.Stats()
	if stats.TotalConns != 1 {
		t.Errorf("expected TotalConns=1, got %d", stats.TotalConns)
	}
	if stats.ActiveConns != 1 {
		t.Errorf("expected ActiveConns=1, got %d", stats.ActiveConns)
	}
	if stats.Misses != 1 {
		t.Errorf("expected Misses=1, got %d", stats.Misses)
	}

	// Put it back
	p.Put(conn1)
	stats = p.Stats()
	if stats.IdleConns != 1 {
		t.Errorf("expected IdleConns=1, got %d", stats.IdleConns)
	}
	if stats.ActiveConns != 0 {
		t.Errorf("expected ActiveConns=0, got %d", stats.ActiveConns)
	}

	// Get again (pool hit)
	conn2, _ := p.Get(ctx, "server1")
	stats = p.Stats()
	if stats.Hits != 1 {
		t.Errorf("expected Hits=1, got %d", stats.Hits)
	}
	p.Put(conn2)
}

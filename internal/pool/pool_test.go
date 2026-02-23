package pool

import (
	"context"
	"sync"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

type fakePoolTransport struct {
	id     int
	closed bool
	mu     sync.Mutex
}

func (f *fakePoolTransport) Send(_ context.Context, _ *mcp.Message) error { return nil }
func (f *fakePoolTransport) Recv(_ context.Context) (*mcp.Message, error) {
	return &mcp.Message{JSONRPC: mcp.JSONRPCVersion}, nil
}
func (f *fakePoolTransport) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func TestNew(t *testing.T) {
	t.Parallel()

	p := New(Config{
		MaxOpen: 5,
		MaxIdle: 2,
	})

	if p == nil {
		t.Fatal("expected non-nil pool")
	}

	stats := p.Stats()
	if stats.ActiveConns != 0 {
		t.Errorf("expected 0 active, got %d", stats.ActiveConns)
	}
	if stats.IdleConns != 0 {
		t.Errorf("expected 0 idle, got %d", stats.IdleConns)
	}
	if stats.TotalConns != 0 {
		t.Errorf("expected 0 total, got %d", stats.TotalConns)
	}
}

// TestPool_GetPutCycle verifies basic get/put lifecycle.
func TestPool_GetPutCycle(t *testing.T) {
	dialCount := 0
	p := New(Config{
		MaxIdle:     2,
		MaxOpen:     5,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			dialCount++
			return &fakePoolTransport{id: dialCount}, nil
		},
	})
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	conn, err := p.Get(ctx, "test-server")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if dialCount != 1 {
		t.Fatalf("dialCount = %d, want 1", dialCount)
	}

	p.Put(conn)

	stats := p.Stats()
	if stats.IdleConns != 1 {
		t.Fatalf("idle = %d, want 1", stats.IdleConns)
	}

	// Second Get should reuse the idle connection.
	conn2, err := p.Get(ctx, "test-server")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if dialCount != 1 {
		t.Fatalf("dialCount = %d, want 1 (should reuse)", dialCount)
	}
	p.Put(conn2)
}

// TestPool_MaxOpenEnforced verifies that the pool rejects Get when MaxOpen is reached.
func TestPool_MaxOpenEnforced(t *testing.T) {
	p := New(Config{
		MaxIdle:     1,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakePoolTransport{}, nil
		},
	})
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	conn1, err := p.Get(ctx, "srv")
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}
	conn2, err := p.Get(ctx, "srv")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}

	// Third Get should fail (maxOpen=2, both active).
	_, err = p.Get(ctx, "srv")
	if err == nil {
		t.Fatal("expected error when MaxOpen reached")
	}

	p.Put(conn1)
	p.Put(conn2)
}

// TestPool_UnhealthyConnectionsDropped verifies that unhealthy connections
// are discarded on Put and not returned on subsequent Get.
func TestPool_UnhealthyConnectionsDropped(t *testing.T) {
	dialCount := 0
	p := New(Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			dialCount++
			return &fakePoolTransport{id: dialCount}, nil
		},
	})
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	conn, err := p.Get(ctx, "srv")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Mark unhealthy and return to pool.
	conn.Healthy = false
	p.Put(conn)

	// Pool should have 0 idle connections (unhealthy was dropped).
	stats := p.Stats()
	if stats.IdleConns != 0 {
		t.Fatalf("idle = %d, want 0 (unhealthy dropped)", stats.IdleConns)
	}

	// Next Get should dial a new connection.
	conn2, err := p.Get(ctx, "srv")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if dialCount != 2 {
		t.Fatalf("dialCount = %d, want 2 (fresh dial after unhealthy drop)", dialCount)
	}
	p.Put(conn2)
}

// TestPool_ClearServer removes all idle connections for a server.
func TestPool_ClearServer(t *testing.T) {
	p := New(Config{
		MaxIdle:     5,
		MaxOpen:     5,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakePoolTransport{}, nil
		},
	})
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	var conns []*Conn
	for i := 0; i < 3; i++ {
		c, _ := p.Get(ctx, "srv")
		conns = append(conns, c)
	}
	for _, c := range conns {
		p.Put(c)
	}

	stats := p.Stats()
	if stats.IdleConns == 0 {
		t.Fatal("expected idle connections before clear")
	}

	p.ClearServer("srv")

	stats = p.Stats()
	if stats.IdleConns != 0 {
		t.Fatalf("idle = %d, want 0 after ClearServer", stats.IdleConns)
	}
}

// TestPool_ConcurrentGetPut exercises the pool under concurrent access.
// Uses MaxOpen large enough to handle all concurrent goroutines.
func TestPool_ConcurrentGetPut(t *testing.T) {
	const concurrency = 20
	p := New(Config{
		MaxIdle:     5,
		MaxOpen:     concurrency,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakePoolTransport{}, nil
		},
	})
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := p.Get(ctx, "srv")
			if err != nil {
				errs <- err
				return
			}
			// Simulate some work.
			time.Sleep(time.Millisecond)
			p.Put(conn)
			errs <- nil
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Get/Put error: %v", err)
		}
	}
}

// TestPool_DialFuncReturnsSameTransport_DocumentedBehavior documents that when
// a DialFunc returns the same transport instance for a given server, all pool
// connections share that transport. This is the architectural limitation that
// makes the daemon's per-server callLock essential for stdio servers.
func TestPool_DialFuncReturnsSameTransport_DocumentedBehavior(t *testing.T) {
	// Simulate the procMgr.Dial() behavior: returns the same transport.
	sharedTransport := &fakePoolTransport{id: 1}
	p := New(Config{
		MaxIdle:     2,
		MaxOpen:     5,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return sharedTransport, nil
		},
	})
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	conn1, _ := p.Get(ctx, "srv")
	conn2, _ := p.Get(ctx, "srv")

	// Both connections reference the same underlying transport.
	if conn1.Transport != conn2.Transport {
		t.Fatal("expected connections to share transport when DialFunc returns the same instance")
	}

	p.Put(conn1)
	p.Put(conn2)
}

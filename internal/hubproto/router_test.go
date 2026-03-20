package hubproto

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRouterDispatch(t *testing.T) {
	r := NewRouter()

	called := false
	r.Register(DomainMCP, func(ctx context.Context, env Envelope) (*Envelope, error) {
		called = true
		return &Envelope{
			Domain:    DomainMCP,
			Method:    "response",
			RequestID: env.RequestID,
			Timestamp: time.Now().UTC(),
		}, nil
	})

	env := Envelope{
		Domain:    DomainMCP,
		Method:    "tools/call",
		RequestID: "req-1",
		Timestamp: time.Now().UTC(),
	}

	resp, err := r.Dispatch(context.Background(), env)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.RequestID != "req-1" {
		t.Errorf("response request_id: got %q, want %q", resp.RequestID, "req-1")
	}
}

func TestRouterUnknownDomain(t *testing.T) {
	r := NewRouter()

	env := Envelope{
		Domain:    Domain("nonexistent"),
		Method:    "test",
		Timestamp: time.Now().UTC(),
	}

	_, err := r.Dispatch(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for unknown domain")
	}
	if !strings.Contains(err.Error(), "no handler registered") {
		t.Errorf("error message: got %q, want it to contain 'no handler registered'", err.Error())
	}
}

func TestRouterRegisterReplace(t *testing.T) {
	r := NewRouter()

	var which string
	r.Register(DomainControl, func(ctx context.Context, env Envelope) (*Envelope, error) {
		which = "first"
		return nil, nil
	})
	r.Register(DomainControl, func(ctx context.Context, env Envelope) (*Envelope, error) {
		which = "second"
		return nil, nil
	})

	env := Envelope{
		Domain:    DomainControl,
		Method:    "ping",
		Timestamp: time.Now().UTC(),
	}

	_, err := r.Dispatch(context.Background(), env)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if which != "second" {
		t.Errorf("expected second handler to be called, got %q", which)
	}
}

func TestRouterHandlerError(t *testing.T) {
	r := NewRouter()

	expectedErr := errors.New("handler failed")
	r.Register(DomainAgent, func(ctx context.Context, env Envelope) (*Envelope, error) {
		return nil, expectedErr
	})

	env := Envelope{
		Domain:    DomainAgent,
		Method:    "session/start",
		Timestamp: time.Now().UTC(),
	}

	_, err := r.Dispatch(context.Background(), env)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected handler error, got %v", err)
	}
}

func TestRouterNilResponse(t *testing.T) {
	r := NewRouter()

	r.Register(DomainDevbox, func(ctx context.Context, env Envelope) (*Envelope, error) {
		return nil, nil
	})

	env := Envelope{
		Domain:    DomainDevbox,
		Method:    "exec",
		Timestamp: time.Now().UTC(),
	}

	resp, err := r.Dispatch(context.Background(), env)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if resp != nil {
		t.Error("expected nil response for notification-style handler")
	}
}

func TestRouterHasHandler(t *testing.T) {
	r := NewRouter()

	if r.HasHandler(DomainSpawn) {
		t.Error("expected HasHandler to return false before registration")
	}

	r.Register(DomainSpawn, func(ctx context.Context, env Envelope) (*Envelope, error) {
		return nil, nil
	})

	if !r.HasHandler(DomainSpawn) {
		t.Error("expected HasHandler to return true after registration")
	}
}

func TestRouterConcurrentDispatch(t *testing.T) {
	r := NewRouter()

	var count int64
	var mu sync.Mutex
	r.Register(DomainMCP, func(ctx context.Context, env Envelope) (*Envelope, error) {
		mu.Lock()
		count++
		mu.Unlock()
		return nil, nil
	})

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			env := Envelope{
				Domain:    DomainMCP,
				Method:    "tools/call",
				Timestamp: time.Now().UTC(),
			}
			_, _ = r.Dispatch(context.Background(), env)
		}()
	}
	wg.Wait()

	mu.Lock()
	if count != n {
		t.Errorf("expected %d calls, got %d", n, count)
	}
	mu.Unlock()
}

func TestRouterContextCancellation(t *testing.T) {
	r := NewRouter()

	r.Register(DomainControl, func(ctx context.Context, env Envelope) (*Envelope, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return nil, errors.New("timeout in handler")
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	env := Envelope{
		Domain:    DomainControl,
		Method:    "long-op",
		Timestamp: time.Now().UTC(),
	}

	_, err := r.Dispatch(ctx, env)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

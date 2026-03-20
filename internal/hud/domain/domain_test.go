package domain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubDomain is a minimal Domain implementation for testing.
type stubDomain struct {
	name       string
	started    bool
	stopped    bool
	startErr   error
	stopErr    error
	registered bool
}

func (d *stubDomain) Name() string { return d.name }

func (d *stubDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	d.registered = true
	mux.HandleFunc("GET /test/"+d.name, mw(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func (d *stubDomain) Start(_ context.Context) error {
	if d.startErr != nil {
		return d.startErr
	}
	d.started = true
	return nil
}

func (d *stubDomain) Stop() error {
	d.stopped = true
	return d.stopErr
}

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(reg.Domains()) != 0 {
		t.Fatalf("expected 0 domains, got %d", len(reg.Domains()))
	}
}

func TestRegister(t *testing.T) {
	reg := NewRegistry()
	d := &stubDomain{name: "test"}
	reg.Register(d)

	names := reg.Domains()
	if len(names) != 1 || names[0] != "test" {
		t.Fatalf("expected [test], got %v", names)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubDomain{name: "dup"})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	reg.Register(&stubDomain{name: "dup"})
}

func TestGet(t *testing.T) {
	reg := NewRegistry()
	d := &stubDomain{name: "fleet"}
	reg.Register(d)

	got, ok := reg.Get("fleet")
	if !ok {
		t.Fatal("expected to find domain 'fleet'")
	}
	if got.Name() != "fleet" {
		t.Fatalf("expected name 'fleet', got %q", got.Name())
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Fatal("expected not to find domain 'nonexistent'")
	}
}

func TestRegisterAll(t *testing.T) {
	reg := NewRegistry()
	d1 := &stubDomain{name: "alpha"}
	d2 := &stubDomain{name: "beta"}
	reg.Register(d1)
	reg.Register(d2)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	reg.RegisterAll(mux, mw)

	if !d1.registered || !d2.registered {
		t.Fatal("expected both domains to be registered")
	}

	// Verify routes are reachable.
	for _, name := range []string{"alpha", "beta"} {
		req := httptest.NewRequest(http.MethodGet, "/test/"+name, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("route /test/%s returned %d, expected 200", name, rec.Code)
		}
	}
}

func TestStartAllSuccess(t *testing.T) {
	reg := NewRegistry()
	d1 := &stubDomain{name: "a"}
	d2 := &stubDomain{name: "b"}
	reg.Register(d1)
	reg.Register(d2)

	if err := reg.StartAll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d1.started || !d2.started {
		t.Fatal("expected both domains to be started")
	}
}

func TestStartAllRollback(t *testing.T) {
	reg := NewRegistry()
	d1 := &stubDomain{name: "ok"}
	d2 := &stubDomain{name: "fail", startErr: errors.New("boot failure")}
	reg.Register(d1)
	reg.Register(d2)

	err := reg.StartAll(context.Background())
	if err == nil {
		t.Fatal("expected error from StartAll")
	}
	if !d1.stopped {
		t.Fatal("expected d1 to be rolled back (stopped)")
	}
	if d2.started {
		t.Fatal("d2 should not have been started")
	}
}

func TestStopAll(t *testing.T) {
	reg := NewRegistry()
	d1 := &stubDomain{name: "x"}
	d2 := &stubDomain{name: "y"}
	reg.Register(d1)
	reg.Register(d2)

	_ = reg.StartAll(context.Background())
	if err := reg.StopAll(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d1.stopped || !d2.stopped {
		t.Fatal("expected both domains to be stopped")
	}
}

func TestStopAllReturnsFirstError(t *testing.T) {
	reg := NewRegistry()
	d1 := &stubDomain{name: "a", stopErr: errors.New("stop-a")}
	d2 := &stubDomain{name: "b", stopErr: errors.New("stop-b")}
	reg.Register(d1)
	reg.Register(d2)

	err := reg.StopAll()
	if err == nil {
		t.Fatal("expected error from StopAll")
	}
	// StopAll iterates in reverse, so d2 stops first.
	if err.Error() != "stop-b" {
		t.Fatalf("expected first error 'stop-b', got %q", err.Error())
	}
	// Both should still be stopped.
	if !d1.stopped || !d2.stopped {
		t.Fatal("expected both domains to attempt stop")
	}
}

func TestDomainOrderPreserved(t *testing.T) {
	reg := NewRegistry()
	names := []string{"fleet", "spawn", "mobile", "coordinator"}
	for _, n := range names {
		reg.Register(&stubDomain{name: n})
	}

	got := reg.Domains()
	if len(got) != len(names) {
		t.Fatalf("expected %d domains, got %d", len(names), len(got))
	}
	for i, n := range names {
		if got[i] != n {
			t.Fatalf("domain[%d]: expected %q, got %q", i, n, got[i])
		}
	}
}

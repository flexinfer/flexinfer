package domain

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubDomain is a minimal Domain implementation for testing.
type stubDomain struct {
	name       string
	registered bool
}

func (d *stubDomain) Name() string { return d.name }

func (d *stubDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	d.registered = true
	mux.HandleFunc("GET /test/"+d.name, mw(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
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

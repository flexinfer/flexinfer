package pool

import (
	"testing"
)

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

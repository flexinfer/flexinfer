package coordinator

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_StartsClosedAllowsRequests(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)

	if cb.State() != StateClosed {
		t.Fatalf("expected closed, got %s", cb.State())
	}
	if !cb.IsAvailable() {
		t.Fatal("expected available")
	}

	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestCircuitBreaker_OpensAfterThresholdFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)
	testErr := errors.New("fail")

	for i := 0; i < 3; i++ {
		_ = cb.Execute(func() error { return testErr })
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected open, got %s", cb.State())
	}
	if cb.IsAvailable() {
		t.Fatal("expected not available")
	}

	// Requests should be rejected.
	err := cb.Execute(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	testErr := errors.New("fail")

	_ = cb.Execute(func() error { return testErr })
	_ = cb.Execute(func() error { return testErr })

	if cb.State() != StateOpen {
		t.Fatalf("expected open, got %s", cb.State())
	}

	// Wait for reset timeout.
	time.Sleep(60 * time.Millisecond)

	if !cb.IsAvailable() {
		t.Fatal("expected available after timeout")
	}

	// Successful probe should close the circuit.
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if cb.State() != StateClosed {
		t.Fatalf("expected closed after successful probe, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailGoesBackToOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	testErr := errors.New("fail")

	_ = cb.Execute(func() error { return testErr })
	_ = cb.Execute(func() error { return testErr })

	time.Sleep(60 * time.Millisecond)

	// Failed probe should go back to open.
	_ = cb.Execute(func() error { return testErr })
	if cb.State() != StateOpen {
		t.Fatalf("expected open after failed probe, got %s", cb.State())
	}
}

func TestCircuitBreaker_ResetClearsState(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Hour)
	testErr := errors.New("fail")

	_ = cb.Execute(func() error { return testErr })
	_ = cb.Execute(func() error { return testErr })

	if cb.State() != StateOpen {
		t.Fatalf("expected open, got %s", cb.State())
	}

	cb.Reset()

	if cb.State() != StateClosed {
		t.Fatalf("expected closed after reset, got %s", cb.State())
	}
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures, got %d", cb.Failures())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Second)
	testErr := errors.New("fail")

	_ = cb.Execute(func() error { return testErr })
	_ = cb.Execute(func() error { return testErr })
	// Two failures, one more would open.

	// Success resets count.
	_ = cb.Execute(func() error { return nil })
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures after success, got %d", cb.Failures())
	}

	// Now we need 3 more failures to open.
	_ = cb.Execute(func() error { return testErr })
	_ = cb.Execute(func() error { return testErr })
	if cb.State() != StateClosed {
		t.Fatalf("expected still closed with 2 failures, got %s", cb.State())
	}
}

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state CircuitState
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
		{CircuitState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

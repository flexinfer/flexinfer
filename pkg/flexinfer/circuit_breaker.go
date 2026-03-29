package flexinfer

import (
	"errors"
	"sync"
	"time"
)

// CircuitState represents the current state of the circuit breaker.
type CircuitState int

const (
	// StateClosed allows requests through normally.
	StateClosed CircuitState = iota
	// StateOpen blocks all requests after threshold failures.
	StateOpen
	// StateHalfOpen allows one test request to probe recovery.
	StateHalfOpen
)

// String returns the state name.
func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitBreaker implements the circuit breaker pattern for external calls.
type CircuitBreaker struct {
	mu            sync.Mutex
	state         CircuitState
	failures      int
	threshold     int
	resetTimeout  time.Duration
	lastFailure   time.Time
	halfOpenAllow bool // true when one request is allowed in half-open
}

// NewCircuitBreaker creates a CircuitBreaker that opens after threshold
// consecutive failures and resets to half-open after resetTimeout.
func NewCircuitBreaker(threshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:        StateClosed,
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}
}

// Execute runs fn if the circuit allows it. On success, the circuit
// resets to closed. On failure, the failure count increments and the
// circuit may open.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if !cb.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()
		if cb.failures >= cb.threshold {
			cb.state = StateOpen
		}
		// If we were half-open and the probe failed, go back to open.
		if cb.state == StateHalfOpen {
			cb.state = StateOpen
		}
		return err
	}

	// Success -- reset to closed.
	cb.failures = 0
	cb.state = StateClosed
	cb.halfOpenAllow = false
	return nil
}

// allowRequest checks whether a request should be permitted.
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if reset timeout has elapsed.
		if time.Since(cb.lastFailure) >= cb.resetTimeout {
			cb.state = StateHalfOpen
			cb.halfOpenAllow = true
			return true
		}
		return false
	case StateHalfOpen:
		// Allow exactly one probe request.
		if cb.halfOpenAllow {
			cb.halfOpenAllow = false
			return true
		}
		return false
	default:
		return false
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// IsAvailable reports whether the circuit will currently allow a request.
func (cb *CircuitBreaker) IsAvailable() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		return time.Since(cb.lastFailure) >= cb.resetTimeout
	case StateHalfOpen:
		return cb.halfOpenAllow
	default:
		return false
	}
}

// Failures returns the current consecutive failure count.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failures
}

// Reset forces the circuit breaker back to the closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failures = 0
	cb.halfOpenAllow = false
}

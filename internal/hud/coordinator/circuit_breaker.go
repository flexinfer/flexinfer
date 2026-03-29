package coordinator

// This file re-exports types from pkg/flexinfer so existing coordinator
// code continues to compile without import changes.

import (
	"github.com/crb2nu/loom/pkg/flexinfer"
)

// Type aliases for circuit breaker.
type (
	CircuitBreaker = flexinfer.CircuitBreaker
	CircuitState   = flexinfer.CircuitState
)

// State constants.
const (
	StateClosed   = flexinfer.StateClosed
	StateOpen     = flexinfer.StateOpen
	StateHalfOpen = flexinfer.StateHalfOpen
)

// ErrCircuitOpen is re-exported from pkg/flexinfer.
var ErrCircuitOpen = flexinfer.ErrCircuitOpen

// NewCircuitBreaker delegates to flexinfer.NewCircuitBreaker.
var NewCircuitBreaker = flexinfer.NewCircuitBreaker

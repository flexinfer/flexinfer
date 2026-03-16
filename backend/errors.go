package backend

import "errors"

// ErrUnknownBackend is returned when a backend name cannot be resolved
// to a registered implementation.
var ErrUnknownBackend = errors.New("unknown backend")

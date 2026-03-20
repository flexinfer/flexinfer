package sandbox

import "errors"

// ErrStreamClosed is returned when writing to a closed StreamWriter.
var ErrStreamClosed = errors.New("stream writer closed")

// ErrNotSupported is returned when an operation is not supported by the backend.
var ErrNotSupported = errors.New("operation not supported")

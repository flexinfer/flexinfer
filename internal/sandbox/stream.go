package sandbox

import (
	"sync"
	"time"
)

// StreamWriter implements io.Writer and sends each Write call as an ExecChunk
// to the provided channel. It is safe for concurrent use.
type StreamWriter struct {
	stream string // "stdout" or "stderr"
	ch     chan<- ExecChunk
	mu     sync.Mutex
	closed bool
}

// NewStreamWriter creates a StreamWriter that sends chunks to ch with the
// given stream name ("stdout" or "stderr").
func NewStreamWriter(stream string, ch chan<- ExecChunk) *StreamWriter {
	return &StreamWriter{
		stream: stream,
		ch:     ch,
	}
}

// Write sends the data as an ExecChunk. It never returns an error unless
// the writer has been closed.
func (w *StreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrStreamClosed
	}

	if len(p) == 0 {
		return 0, nil
	}

	// Copy data to avoid referencing the caller's buffer after return.
	data := make([]byte, len(p))
	copy(data, p)

	w.ch <- ExecChunk{
		Stream:    w.stream,
		Data:      string(data),
		Timestamp: time.Now(),
	}

	return len(p), nil
}

// Close marks the writer as closed. Subsequent Write calls return ErrStreamClosed.
func (w *StreamWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

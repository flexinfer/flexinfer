package codexwatch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// Publisher is the minimal surface the tailer needs from
// pkg/eventpub.HTTPPublisher (or any drop-in). Defined locally so tests
// can inject an in-memory recorder.
type Publisher interface {
	Publish(eventType string, payload any)
}

// tailerConfig governs one file's tailing loop. Zero values are sensible
// defaults; the watcher passes a shared config to every tailer it owns.
type tailerConfig struct {
	pollInterval time.Duration // how often to re-stat + re-read; default 500ms
	idleTimeout  time.Duration // how long without growth before emitting session.end; default 30min
	maxLifetime  time.Duration // hard ceiling on a single tailer; default 4h
	startAtEnd   bool          // skip existing content on first open (true for --from=now)
}

// tailer reads one Codex session JSONL file append-only, mapping each
// new line to canonical events and pushing them to the publisher.
// Lifetime is bounded by both ctx and maxLifetime.
type tailer struct {
	path       string
	cfg        tailerConfig
	publisher  Publisher
	logger     *slog.Logger
	state      *SessionState
	offset     int64 // bytes consumed so far; checkpointed by watcher (future)
	pending    []byte
	emittedEnd atomic.Bool // ensure session.end fires at most once per tailer
}

// newTailer constructs a tailer. cfg is passed by value so callers can
// override defaults without mutating shared config.
func newTailer(path string, cfg tailerConfig, publisher Publisher, logger *slog.Logger) *tailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &tailer{
		path:      path,
		cfg:       cfg,
		publisher: publisher,
		logger:    logger,
		state:     &SessionState{},
	}
}

// Run blocks until ctx is cancelled, maxLifetime elapses, the file is
// archived/removed, or the file goes idle past idleTimeout. Always emits
// a single session.end on exit (with appropriate reason) once a
// session_meta record has been observed.
func (t *tailer) Run(ctx context.Context) {
	deadline := time.Now().Add(t.cfg.maxLifetime)
	ticker := time.NewTicker(t.cfg.pollInterval)
	defer ticker.Stop()

	// First open: optionally seek to end (--from=now).
	if t.cfg.startAtEnd {
		if fi, err := os.Stat(t.path); err == nil {
			t.offset = fi.Size()
		}
	}

	var lastGrowth = time.Now()
	for {
		select {
		case <-ctx.Done():
			t.emitEnd("shutdown")
			return
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			t.emitEnd("max_lifetime")
			return
		}
		grew, gone, err := t.readNew()
		if err != nil {
			t.logger.Debug("codexwatch: tail read", "path", t.path, "error", err)
			continue
		}
		if gone {
			t.emitEnd("archived")
			return
		}
		if grew {
			lastGrowth = time.Now()
			continue
		}
		if time.Since(lastGrowth) > t.cfg.idleTimeout {
			t.emitEnd("idle")
			return
		}
	}
}

// readNew opens the file, seeks to t.offset, drains all complete lines,
// and dispatches them through MapRecord + publisher. Returns whether the
// file grew this poll, whether the file has been removed, and any
// non-fatal error.
//
// Partial trailing lines (no \n) are buffered in t.pending and joined
// with the next read; this is the standard JSONL-tail recovery pattern
// and prevents losing the last partial record after a process restart
// would otherwise truncate the buffer.
func (t *tailer) readNew() (grew bool, gone bool, err error) {
	f, err := os.Open(t.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, true, nil
		}
		return false, false, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return false, false, err
	}
	size := fi.Size()
	if size < t.offset {
		// File was truncated or rotated under us. Reset rather than
		// trying to reconcile — the Codex.app does not rewrite
		// existing session files in place, so this branch is mostly
		// defensive against a sysadmin rotating logs.
		t.offset = 0
		t.pending = nil
	}
	if size == t.offset {
		return false, false, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return false, false, err
	}

	r := bufio.NewReaderSize(f, 64*1024)
	buf := bytes.NewBuffer(t.pending)
	for {
		chunk, err := r.ReadBytes('\n')
		if len(chunk) > 0 {
			buf.Write(chunk)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, false, err
		}
	}
	t.offset = size

	data := buf.Bytes()
	// Trim trailing partial line back into pending; everything else is
	// flushed through MapRecord.
	if nl := bytes.LastIndexByte(data, '\n'); nl >= 0 {
		complete := data[:nl]
		t.pending = append([]byte(nil), data[nl+1:]...)
		t.dispatchLines(complete)
	} else {
		t.pending = append([]byte(nil), data...)
	}
	return true, false, nil
}

// dispatchLines splits complete on \n boundaries and runs each non-empty
// line through MapRecord. Errors are logged at debug and skipped to keep
// the tailer alive across the inevitable schema drift in future Codex
// versions.
func (t *tailer) dispatchLines(complete []byte) {
	for {
		nl := bytes.IndexByte(complete, '\n')
		var line []byte
		if nl < 0 {
			line = complete
			complete = nil
		} else {
			line = complete[:nl]
			complete = complete[nl+1:]
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if complete == nil {
				return
			}
			continue
		}
		events, err := MapRecord(line, t.state)
		if err != nil {
			t.logger.Debug("codexwatch: map record", "path", t.path, "error", err)
		}
		for _, ev := range events {
			t.publisher.Publish(ev.Type, ev.Payload)
		}
		if complete == nil {
			return
		}
	}
}

// emitEnd publishes a synthetic session.end event exactly once per
// tailer. No-op when the tailer never observed a session_meta record
// (the file may have been empty or already archived when discovered).
func (t *tailer) emitEnd(reason string) {
	if t.state == nil || t.state.SessionID == "" {
		return
	}
	if !t.emittedEnd.CompareAndSwap(false, true) {
		return
	}
	ev := MakeSessionEnd(t.state, reason, time.Now())
	if ev.Type == "" {
		return
	}
	t.publisher.Publish(ev.Type, ev.Payload)
}

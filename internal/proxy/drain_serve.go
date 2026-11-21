package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/flexinfer/flexinfer/pkg/validation"
)

// A request drained from the cold-start queue is served on the queue
// processor's goroutine, not on the HTTP handler goroutine that waits for it.
// net/http recovers a panic only on the handler goroutine, so a panic while
// serving a drained request would otherwise end the whole proxy process.
// httputil.ReverseProxy panics with http.ErrAbortHandler when copying the
// response body fails and the request came from an http.Server (a client that
// left mid-stream, an upstream reset after the headers), and
// forwardWithRetry and trackAndServeWithDemand recover and re-panic after
// their accounting. serveDrained recovers it on the drain goroutine and hands
// the outcome to the waiting handler (QueuedRequest.finish).

// serveDrained serves qr on the drain goroutine. A panic is recovered here:
// http.ErrAbortHandler quietly, anything else logged with the lane and a
// stack. trackAndServe's own deferred accounting (connection count, demand
// heartbeat, QoS seat, request metrics) runs while the panic unwinds. When no
// response byte went out the client gets a 500; when the response was cut
// short qr.aborted tells the waiting handler to abort its connection. No
// second response is written after the headers.
func (p *Proxy) serveDrained(qr *QueuedRequest) {
	w := &sentTracker{ResponseWriter: qr.w}
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		abort := v == http.ErrAbortHandler //nolint:errorlint // net/http compares the sentinel the same way
		if abort {
			slog.Debug("drained request aborted", "model", qr.modelName)
		} else {
			slog.Error("panic serving a request drained from the cold-start queue",
				"model", qr.modelName, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
		}
		if w.sent || abort {
			qr.aborted = true
			return
		}
		validation.WriteInternalError(qr.w, "Internal error serving the request")
	}()
	p.trackAndServe(w, withColdStartDrain(qr.r), qr.modelName, time.Now())
}

// errDrainedResponseCut is what a waiter outside an http.Server (a job's
// forward) returns when its drained response was cut short.
var errDrainedResponseCut = errors.New("response to a request drained from the cold-start queue was cut short")

// finish is the waiting handler's result once the drain closed qr.done. A
// response the drain cut short is aborted the way net/http aborts one on the
// handler goroutine: by panicking with http.ErrAbortHandler, which the server
// recovers and answers by closing the connection, so the client sees a broken
// response rather than a short one that looks complete. Outside an
// http.Server nothing would recover that panic, so the waiter returns an error
// instead.
func (qr *QueuedRequest) finish() error {
	if qr.aborted {
		if qr.r.Context().Value(http.ServerContextKey) != nil {
			panic(http.ErrAbortHandler)
		}
		return errDrainedResponseCut
	}
	return qr.err
}

// sentTracker records whether any part of the response reached the client's
// writer. It unwraps for http.ResponseController so flushing and hijacking
// keep working.
type sentTracker struct {
	http.ResponseWriter
	sent bool
}

func (w *sentTracker) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *sentTracker) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	if status >= 200 {
		w.sent = true
	}
}

func (w *sentTracker) Write(b []byte) (int, error) {
	// The first Write commits the headers even if it fails part way.
	w.sent = true
	return w.ResponseWriter.Write(b)
}

func (w *sentTracker) Flush() {
	w.sent = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

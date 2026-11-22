package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// panicWriter panics with value on its first WriteHeader (beforeHeaders) or
// its first Write (after the headers went out), then behaves normally.
type panicWriter struct {
	*httptest.ResponseRecorder
	value         any
	beforeHeaders bool
	once          sync.Once
}

func (w *panicWriter) WriteHeader(status int) {
	if w.beforeHeaders {
		w.once.Do(func() { panic(w.value) })
	}
	w.ResponseRecorder.WriteHeader(status)
}

func (w *panicWriter) Write(b []byte) (int, error) {
	if !w.beforeHeaders {
		w.once.Do(func() { panic(w.value) })
	}
	return w.ResponseRecorder.Write(b)
}

// resetUpstream answers 200 with a first chunk and then drops the connection
// when the request carries X-Test: reset, which makes httputil.ReverseProxy
// fail the body copy; otherwise it answers 200 "ok".
func resetUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "reset" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type waiterResult struct {
	err      error
	panicked any
}

// queueWaiter runs handleColdStart for req on its own goroutine, as an HTTP
// handler would, and reports what it returned or panicked with.
func queueWaiter(p *Proxy, w http.ResponseWriter, req *http.Request, model string) <-chan waiterResult {
	out := make(chan waiterResult, 1)
	go func() {
		var res waiterResult
		defer func() {
			res.panicked = recover()
			out <- res
		}()
		res.err = p.handleColdStart(req.Context(), w, req, model, time.Now())
	}()
	return out
}

// A request drained from the cold-start queue is served on the queue
// processor's goroutine, which has no recover of its own. A panic while
// serving it must not take the proxy down: http.ErrAbortHandler (the reverse
// proxy's signal for a failed body copy) and any other panic are recovered,
// the drain goes on with the next request, and the waiting handler aborts its
// connection when the response was cut short.
func TestDrainSurvivesAPanicWhileServing(t *testing.T) {
	serverCtx := func(r *http.Request) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), http.ServerContextKey, &http.Server{}))
	}
	for _, tc := range []struct {
		name       string
		first      func(t *testing.T) (http.ResponseWriter, *http.Request)
		wantAbort  bool // the waiting handler aborts its connection
		wantStatus int  // status of the first response when not aborted
	}{
		{name: "upstream reset after the headers", wantAbort: true, first: func(*testing.T) (http.ResponseWriter, *http.Request) {
			r := serverCtx(chatReq("stream-lane"))
			r.Header.Set("X-Test", "reset")
			return httptest.NewRecorder(), r
		}},
		{name: "ordinary panic after the headers", wantAbort: true, first: func(*testing.T) (http.ResponseWriter, *http.Request) {
			return &panicWriter{ResponseRecorder: httptest.NewRecorder(), value: "boom"}, serverCtx(chatReq("stream-lane"))
		}},
		{name: "ordinary panic before the headers", wantStatus: http.StatusInternalServerError, first: func(*testing.T) (http.ResponseWriter, *http.Request) {
			return &panicWriter{ResponseRecorder: httptest.NewRecorder(), value: "boom", beforeHeaders: true}, serverCtx(chatReq("stream-lane"))
		}},
		{name: "ordinary panic outside an HTTP server", wantStatus: http.StatusOK, first: func(*testing.T) (http.ResponseWriter, *http.Request) {
			// A job's forward: no server to abort a connection, the
			// waiter returns and the job runner sees what was written.
			return &panicWriter{ResponseRecorder: httptest.NewRecorder(), value: "boom"}, chatReq("stream-lane")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "stream-lane"
			lane := &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: model, Namespace: "default"},
				Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b"},
				Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseLoading},
			}
			p := newGateTestProxy(t, &touchCounter{}, lane)
			p.queueTimeout = time.Hour
			p.directLoadTargets.Store(model, resetUpstream(t).URL)
			q := stalledQueue(p, model, 4)

			firstW, firstReq := tc.first(t)
			first := queueWaiter(p, firstW, firstReq, model)
			require.Eventually(t, func() bool { return queuedLen(q) == 1 }, 5*time.Second, 5*time.Millisecond)
			secondW := httptest.NewRecorder()
			second := queueWaiter(p, secondW, serverCtx(chatReq(model)), model)
			require.Eventually(t, func() bool { return queuedLen(q) == 2 }, 5*time.Second, 5*time.Millisecond)

			drained := make(chan struct{})
			go func() {
				defer close(drained)
				p.drainQueue(q)
			}()
			select {
			case <-drained:
			case <-time.After(10 * time.Second):
				t.Fatal("drain did not finish")
			}

			res := <-first
			if tc.wantAbort {
				assert.Equal(t, http.ErrAbortHandler, res.panicked, "the handler aborts the cut-short response")
			} else {
				assert.Nil(t, res.panicked)
				assert.Equal(t, tc.wantStatus, firstW.(interface{ Result() *http.Response }).Result().StatusCode)
				if tc.wantStatus == http.StatusOK {
					assert.ErrorIs(t, res.err, errDrainedResponseCut, "a waiter outside a server learns the response was cut short")
				}
			}
			res2 := <-second
			assert.Nil(t, res2.panicked)
			assert.NoError(t, res2.err)
			assert.Equal(t, http.StatusOK, secondW.Code, "the drain went on with the next request")
			assert.Equal(t, "ok", secondW.Body.String())
			assert.Zero(t, promtestutil.ToFloat64(queueDepth.WithLabelValues(model)))
			assert.Zero(t, p.GetActiveConnections(model), "in-flight accounting was released")
		})
	}
}

func queuedLen(q *RequestQueue) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// A job's forward runs the handler on the job goroutine, where no server
// recovers a panic; serveJobForward does.
func TestServeJobForwardRecoversAPanic(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	assert.True(t, serveJobForward(boom, httptest.NewRecorder(), chatReq("lane"), "job-1"))
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	assert.False(t, serveJobForward(ok, httptest.NewRecorder(), chatReq("lane"), "job-1"))
}

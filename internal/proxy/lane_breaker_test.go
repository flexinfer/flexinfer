package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLaneBreakerSilentUpstreamAndRecovery(t *testing.T) {
	t.Setenv(envMaxForwardAttempts, "1")
	var healthy atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Proxy{ctx: ctx, laneConfig: Config{LaneBreakerThreshold: 3, LaneFirstByteBudget: 10 * time.Millisecond, LaneBreakerCooldown: 100 * time.Millisecond}}
	for i := 0; i < 3; i++ {
		p.directLoadTargets.Store("silent", upstream.URL)
		w := httptest.NewRecorder()
		p.forwardWithRetry(w, httptest.NewRequest("POST", "/v1/chat/completions", nil), "silent", "silent", 8000, nil, nil)
		if w.Code != 504 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	start := time.Now()
	w := httptest.NewRecorder()
	if !p.rejectLane(w, httptest.NewRequest("POST", "/v1/chat/completions", nil), "silent", "adapter", nil) || w.Code != 503 || !strings.Contains(w.Body.String(), "lane_unresponsive") || w.Header().Get("Retry-After") == "" {
		t.Fatalf("gate: %v", w)
	}
	if time.Since(start) >= 10*time.Millisecond {
		t.Fatal("open gate did not fail fast")
	}
	healthy.Store(true)
	deadline := time.Now().Add(time.Second)
	for {
		b := p.lane("silent")
		b.mu.Lock()
		open := b.open
		b.mu.Unlock()
		if !open {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe did not close breaker")
		}
		time.Sleep(time.Millisecond)
	}
	if testutil.ToFloat64(laneBreakerOpen.WithLabelValues("silent")) != 0 {
		t.Fatal("open gauge")
	}
	p.directLoadTargets.Store("silent", upstream.URL)
	next := httptest.NewRecorder()
	p.forwardWithRetry(next, httptest.NewRequest("POST", "/v1/chat/completions", nil), "silent", "silent", 8000, nil, nil)
	if next.Code != 200 || !strings.Contains(next.Body.String(), "ok") {
		t.Fatal("recovered lane did not forward")
	}
}

func TestLaneProbePayloads(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/embeddings"} {
		t.Run(path, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path {
					t.Errorf("path %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != "base" {
					t.Error(body)
				}
				if path == "/v1/embeddings" {
					if body["input"] != "Hi" || body["messages"] != nil {
						t.Error(body)
					}
				} else if body["max_tokens"] != float64(1) || body["messages"] == nil {
					t.Error(body)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			if err := probeLane(context.Background(), srv.URL, path, "base"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLaneFirstByteStopsBudgetAndPreservesStreaming(t *testing.T) {
	// Scale 120 seconds to 120ms and a 30-second prefill to 30ms.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("last"))
	}))
	defer srv.Close()
	p := &Proxy{laneConfig: Config{LaneBreakerThreshold: 1, LaneFirstByteBudget: 120 * time.Millisecond}}
	p.directLoadTargets.Store("slow", srv.URL)
	w := httptest.NewRecorder()
	p.forwardWithRetry(w, httptest.NewRequest("POST", "/v1/chat/completions", nil), "slow", "slow", 8000, nil, nil)
	if w.Body.String() != "firstlast" || !w.Flushed {
		t.Fatalf("stream: %v", w)
	}
	b := p.lane("slow")
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures != 0 || b.open {
		t.Fatal("progressing stream tripped breaker")
	}
}

func TestResponseOutcomeAndFlush(t *testing.T) {
	for _, tc := range []struct {
		status int
		cancel bool
		body   string
		want   string
	}{{0, true, "", "timeout"}, {502, false, "error", "upstream_error"}, {200, false, "data", "success"}} {
		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		w := &outcomeWriter{ResponseWriter: rec}
		if tc.status != 0 {
			w.WriteHeader(tc.status)
		}
		if tc.body != "" {
			_, _ = io.WriteString(w, tc.body)
			w.Flush()
			if !rec.Flushed {
				t.Fatal("flush lost")
			}
		}
		if tc.cancel {
			cancel()
		}
		if got := responseOutcome(w, r); got != tc.want {
			t.Fatalf("%s != %s", got, tc.want)
		}
		cancel()
	}
}

func TestLaneFailuresResetDisableAndBudgetOverride(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Proxy{ctx: ctx, laneConfig: Config{LaneBreakerThreshold: 3}}
	p.recordLane("reset", false, errors.New("timeout"))
	p.recordLane("reset", true, nil)
	p.rejectLane(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "reset", "reset", map[string]string{"flexinfer.ai/lane-first-byte-budget": "4m"})
	b := p.lane("reset")
	b.mu.Lock()
	if b.failures != 0 || b.budget != 4*time.Minute {
		t.Fatal("reset/override")
	}
	b.mu.Unlock()
	p.laneConfig.LaneBreakerThreshold = 0
	p.recordLane("disabled", false, errors.New("timeout"))
	if _, ok := p.lanes.Load("disabled"); ok {
		t.Fatal("disabled breaker created state")
	}
}

func TestLaneClientCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	owner, stop := context.WithCancel(context.Background())
	defer stop()
	p := &Proxy{ctx: owner, laneConfig: Config{LaneBreakerThreshold: 1}}
	p.directLoadTargets.Store("cancelled", srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	w := &outcomeWriter{ResponseWriter: rec}
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	p.forwardWithRetry(w, req, "cancelled", "cancelled", 8000, nil, nil)
	if responseOutcome(w, req) != "timeout" {
		t.Fatal("cancellation counted as success")
	}
	b := p.lane("cancelled")
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		t.Fatal("cancellation did not trip")
	}
}

func TestLaneProbeFailureBackoffCap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Proxy{ctx: ctx, laneConfig: Config{LaneBreakerThreshold: 1, LaneBreakerCooldown: 10 * time.Millisecond, LaneBreakerMaxCooldown: 25 * time.Millisecond}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	b := p.lane("backoff")
	b.target = srv.URL
	p.recordLane("backoff", false, errors.New("timeout"))
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		capped := b.cooldown == 25*time.Millisecond && b.open
		b.mu.Unlock()
		if capped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backoff did not reach cap")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTrackAndServeOutcomeCounters(t *testing.T) {
	t.Setenv(envMaxForwardAttempts, "1")
	for _, mode := range []string{"timeout", "upstream_error", "success"} {
		t.Run(mode, func(t *testing.T) {
			p := setupTestProxy(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "timeout" {
					_, _ = io.Copy(io.Discard, r.Body)
					<-r.Context().Done()
					return
				}
				if mode == "upstream_error" {
					w.WriteHeader(502)
				} else {
					w.WriteHeader(200)
				}
				_, _ = w.Write([]byte("data"))
				w.(http.Flusher).Flush()
			}))
			defer srv.Close()
			name := "outcome-" + mode
			p.directLoadTargets.Store(name, srv.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+name+`"}`)).WithContext(ctx)
			before := testutil.ToFloat64(requestsTotal.WithLabelValues(name, mode))
			successes := testutil.ToFloat64(requestsTotal.WithLabelValues(name, "success"))
			rec := httptest.NewRecorder()
			p.trackAndServeBackground(rec, req, name, time.Now())
			if got := testutil.ToFloat64(requestsTotal.WithLabelValues(name, mode)); got != before+1 {
				t.Fatalf("counter %s: %v", mode, got)
			}
			if mode != "success" && testutil.ToFloat64(requestsTotal.WithLabelValues(name, "success")) != successes {
				t.Fatal("failed request counted as success")
			}
			if mode == "success" && !rec.Flushed {
				t.Fatal("stream flush lost")
			}
		})
	}
}

func TestLaneInterruptedStreamReleasesPodConnection(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.laneConfig = Config{LaneBreakerThreshold: 3}
	var target string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := p.getPodConnections(target); got != 1 {
			t.Errorf("active pod connections = %d, want 1", got)
		}
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()
	name := "broken-stream"
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.namespace, Annotations: map[string]string{AnnotationRouting: "least-loaded"}}}
	if err := p.client.Create(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	target = strings.TrimPrefix(srv.URL, "http://")
	p.router.UpdateEndpoints(name, []string{target})
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	// ReverseProxy only propagates stream failures in a server context.
	req = req.WithContext(context.WithValue(req.Context(), http.ServerContextKey, &http.Server{}))
	func() {
		defer func() {
			if got := recover(); got != http.ErrAbortHandler {
				t.Fatalf("abort = %v", got)
			}
		}()
		p.forwardWithRetry(httptest.NewRecorder(), req, name, name, 8000, nil, nil)
	}()
	if got := p.getPodConnections(target); got != 0 {
		t.Fatalf("leaked pod connections: %d", got)
	}
}

func TestLaneFirstByteBudgetExpiryReleasesPodConnection(t *testing.T) {
	t.Setenv(envMaxForwardAttempts, "1")
	p := setupTestProxyWithRouting(t)
	p.laneConfig = Config{LaneBreakerThreshold: 3, LaneFirstByteBudget: 20 * time.Millisecond}
	var target string
	var inFlight atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Store(p.getPodConnections(target))
		<-r.Context().Done()
	}))
	defer srv.Close()
	name := "hanging-pod"
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.namespace, Annotations: map[string]string{AnnotationRouting: "least-loaded"}}}
	if err := p.client.Create(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	target = strings.TrimPrefix(srv.URL, "http://")
	p.router.UpdateEndpoints(name, []string{target})
	w := httptest.NewRecorder()
	p.forwardWithRetry(w, httptest.NewRequest("POST", "/v1/chat/completions", nil), name, name, 8000, nil, nil)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", w.Code)
	}
	if got := inFlight.Load(); got != 1 {
		t.Fatalf("pod connections during request = %d, want 1", got)
	}
	if got := p.getPodConnections(target); got != 0 {
		t.Fatalf("leaked pod connections after budget expiry: %d", got)
	}
}

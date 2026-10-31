package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
)

// Swap-safe replay (D6-a of
// .loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.6).
//
// A multi-model runtime drains a lane before it unloads or reloads it: the
// lane's ingress answers new requests with 503 "model is draining" while the
// accepted ones finish, then the backend stops and its port closes. A request
// that lands in that window fails although the lane, or its declared
// equivalent, can serve it a few seconds later. For idempotent, non-streaming
// paths (embeddings, rerank, non-streaming transcription) the proxy holds back
// a 502/503/504 and, when the failure coincides with a swap of the lane,
// replays the request once:
//
//   - on the flexinfer.ai/unavailable-reroute-to lane straight away when the
//     lane is parked in its shared group and that lane is Ready with a closed
//     breaker (a parked lane with no equivalent fails fast, as today);
//   - otherwise on the same lane once it is Ready and its health endpoint
//     answers, falling back to the reroute lane when the wait runs out.
//
// The wait is bounded by FLEXINFER_PROXY_SWAP_REPLAY_WAIT, by what is left of
// the lane's first-byte budget, and by the client's own context. Every other
// status, every failure without a swap signal, and every streamed body pass
// through untouched. FLEXINFER_PROXY_SWAP_REPLAY=false turns replay off.
const (
	headerSwapReplayed = "X-FlexInfer-Replayed"

	envSwapReplay     = "FLEXINFER_PROXY_SWAP_REPLAY"
	envSwapReplayWait = "FLEXINFER_PROXY_SWAP_REPLAY_WAIT"

	defaultSwapReplayWait = 30 * time.Second
	swapReplayPoll        = 250 * time.Millisecond
	// swapRecentWindow is how long after a park or a (re)load a failure still
	// counts as part of that swap: a stale direct target or a client that
	// raced the new endpoints.
	swapRecentWindow = time.Minute

	// runtimeDrainMarker is the body the runtime's per-model ingress answers
	// with while it drains (internal/runtime/ingress.go).
	runtimeDrainMarker = "model is draining"

	// annotationSwapRerouteTo is the equivalent lane a swapped-out request is
	// replayed on. It is the same annotation the proxy's unavailable reroute
	// reads at admission (G1, !1204): one declaration of the equivalent serves
	// both the requests that arrive while the lane is down and the ones it
	// drops on the way down.
	annotationSwapRerouteTo = "flexinfer.ai/unavailable-reroute-to"
)

// Swap signals, as logged and counted.
const (
	swapDraining   = "draining"
	swapNotReady   = "not_ready"
	swapParked     = "parked"
	swapRecentLoad = "recent_swap"
)

// Replay outcomes for flexinfer_proxy_replays_total.
const (
	replaySameLane = "same_lane"
	replayReroute  = "reroute"
	replayFailed   = "failed"
	replayNoLane   = "no_ready_lane"
)

type swapReplayCtxKey struct{}

// withSwapReplayed marks a request as already replayed once, so the replay
// can never be held back and replayed again.
func withSwapReplayed(ctx context.Context) context.Context {
	return context.WithValue(ctx, swapReplayCtxKey{}, true)
}

func swapReplayedFrom(ctx context.Context) bool {
	v, _ := ctx.Value(swapReplayCtxKey{}).(bool)
	return v
}

func swapReplayEnabled() bool {
	v := strings.TrimSpace(os.Getenv(envSwapReplay))
	if v == "" {
		return true
	}
	on, err := strconv.ParseBool(v)
	return err != nil || on
}

func swapReplayWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(envSwapReplayWait)); err == nil && d >= 0 {
		return d
	}
	return defaultSwapReplayWait
}

// swapReplayPath reports whether path is idempotent: replaying it cannot
// change what the caller gets or what the lane stores.
func swapReplayPath(path string) bool {
	switch path {
	case "/v1/embeddings", "/v1/rerank", "/rerank", "/v1/score", "/v1/audio/transcriptions":
		return true
	}
	return false
}

// swapReplayEligible reports whether a request may be held back for replay:
// an idempotent POST with a buffered body that does not ask for a stream and
// has not been replayed already.
func swapReplayEligible(r *http.Request, body []byte) bool {
	if r.Method != http.MethodPost || !swapReplayPath(r.URL.Path) || swapReplayedFrom(r.Context()) {
		return false
	}
	if !swapReplayEnabled() {
		return false
	}
	return !requestStreams(r.Header.Get("Content-Type"), body)
}

// requestStreams reports whether a request body asks for a streamed
// response: "stream": true in JSON, or a stream=true form field in a
// multipart transcription upload.
func requestStreams(contentType string, body []byte) bool {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	if mediaType == "multipart/form-data" {
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				return false
			}
			if part.FormName() != "stream" {
				continue
			}
			v, _ := io.ReadAll(io.LimitReader(part, 16))
			on, _ := strconv.ParseBool(strings.TrimSpace(string(v)))
			return on
		}
	}
	_, stream := parseRequestForUsageLog(body)
	return stream
}

// swapReplayStatus reports whether a held-back status may be a swap: the
// runtime's drain answer (503), a dead or closed backend (502), or no first
// byte from a lane that is going away (504).
func swapReplayStatus(code int) bool {
	return code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

// laneParked reports whether m sits outside the Active set of its shared
// group: it comes back only when the election admits it again.
func laneParked(m *aiv1alpha2.Model) bool {
	if m == nil {
		return false
	}
	sg := m.Status.SharedGroup
	return sg != nil && sg.State != "" && sg.State != "Active"
}

// swapSignal reports why a failed request coincides with a swap of m, or ""
// when nothing says the lane is swapping (a real upstream error, which is
// returned to the caller as is).
func swapSignal(m *aiv1alpha2.Model, body []byte, now time.Time) string {
	if laneParked(m) {
		return swapParked
	}
	if bytes.Contains(body, []byte(runtimeDrainMarker)) {
		return swapDraining
	}
	if m == nil {
		return ""
	}
	if m.Status.Phase != aiv1alpha2.ModelPhaseReady {
		return swapNotReady
	}
	if sg := m.Status.SharedGroup; sg != nil && sg.PreemptedAt != nil && now.Sub(sg.PreemptedAt.Time) < swapRecentWindow {
		return swapRecentLoad
	}
	for _, c := range m.Status.Conditions {
		if c.Type == aiv1alpha2.ConditionModelReady && c.Status == metav1.ConditionTrue && now.Sub(c.LastTransitionTime.Time) < swapRecentWindow {
			return swapRecentLoad
		}
	}
	return ""
}

// swapCaptureWriter holds back a 502/503/504 so the proxy can decide to
// replay it; any other status is forwarded to the client as it arrives.
type swapCaptureWriter struct {
	w       http.ResponseWriter
	header  http.Header
	status  int
	capture bool
	buf     bytes.Buffer
}

func newSwapCaptureWriter(w http.ResponseWriter) *swapCaptureWriter {
	return &swapCaptureWriter{w: w, header: w.Header().Clone()}
}

func (c *swapCaptureWriter) Header() http.Header { return c.header }

func (c *swapCaptureWriter) WriteHeader(code int) {
	if code < http.StatusOK {
		c.copyHeader()
		c.w.WriteHeader(code)
		return
	}
	if c.status != 0 {
		return
	}
	c.status = code
	if swapReplayStatus(code) {
		c.capture = true
		return
	}
	c.copyHeader()
	c.w.WriteHeader(code)
}

func (c *swapCaptureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.WriteHeader(http.StatusOK)
	}
	if !c.capture {
		return c.w.Write(b)
	}
	if c.buf.Len()+len(b) > maxCapturedErrorBytes {
		// Too large to be a drain or gateway error; stop holding it back.
		if err := c.flushCaptured(); err != nil {
			return 0, err
		}
		return c.w.Write(b)
	}
	return c.buf.Write(b)
}

func (c *swapCaptureWriter) Flush() {
	if c.capture {
		return
	}
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *swapCaptureWriter) Unwrap() http.ResponseWriter { return c.w }

func (c *swapCaptureWriter) captured() bool { return c.capture }

// flushCaptured sends the held-back failure to the client unchanged.
func (c *swapCaptureWriter) flushCaptured() error {
	if !c.capture {
		return nil
	}
	c.capture = false
	c.copyHeader()
	c.w.WriteHeader(c.status)
	_, err := c.w.Write(c.buf.Bytes())
	c.buf.Reset()
	return err
}

func (c *swapCaptureWriter) copyHeader() {
	dst := c.w.Header()
	for k, v := range c.header {
		dst[k] = v
	}
}

// statusRecorder notes the status a replay wrote, for the outcome metric.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 && code >= http.StatusOK {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// swapRerouteTarget returns m's Ready equivalent for path, or "" when m names
// none, names itself, or the equivalent cannot serve now (not Ready, does not
// serve path, or its breaker is open). Never cold-starts the equivalent: on a
// shared GPU that could preempt its leader.
func (p *Proxy) swapRerouteTarget(ctx context.Context, m *aiv1alpha2.Model, path string) string {
	if m == nil {
		return ""
	}
	target := strings.TrimSpace(m.Annotations[annotationSwapRerouteTo])
	if target == "" {
		return ""
	}
	if p.resolver != nil {
		target = p.resolver.ResolveModelAlias(ctx, target)
	}
	if target == m.Name {
		return ""
	}
	tm, err := p.getModel(ctx, target)
	if err != nil || tm.Status.Phase != aiv1alpha2.ModelPhaseReady || !modelServesPath(tm, path) {
		return ""
	}
	if p.laneConfig.LaneBreakerThreshold > 0 {
		if open, _, _ := p.prepareLane(target, tm.Annotations); open {
			return ""
		}
	}
	return target
}

// laneServingAgain reports whether the lane m is Ready and Active again and
// its health endpoint answers below 500 on the target a forward would use
// (the direct-load fast path, else the Service). A draining ingress answers
// 503 on every path, /health included; a stopped backend refuses the
// connection.
func (p *Proxy) laneServingAgain(ctx context.Context, m *aiv1alpha2.Model, port int32) bool {
	if m == nil || m.Status.Phase != aiv1alpha2.ModelPhaseReady || laneParked(m) {
		return false
	}
	target := k8surl.ServiceURL(m.Name, p.namespace, port, true)
	if dt, ok := p.directLoadTargets.Load(m.Name); ok {
		target = dt
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, strings.TrimRight(target, "/")+laneHealthPath, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	return resp.StatusCode < http.StatusInternalServerError
}

// swapReplayDeadline is the latest time a same-lane replay may start: the
// configured wait, cut by what is left of the lane's first-byte budget since
// the request started and by the client's own deadline.
func (p *Proxy) swapReplayDeadline(ctx context.Context, lane string, started time.Time) time.Time {
	deadline := time.Now().Add(swapReplayWait())
	_, _, budget := p.laneDurations()
	b := p.lane(lane)
	b.mu.Lock()
	if b.budget > 0 {
		budget = b.budget
	}
	b.mu.Unlock()
	if end := started.Add(budget); end.Before(deadline) {
		deadline = end
	}
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return deadline
}

// swapReplayLane picks where a swapped-out request is replayed: the
// equivalent at once for a parked lane (it returns only when the election
// admits it again), else the same lane as soon as it serves again, else the
// equivalent once the wait runs out. "" means no lane can serve within the
// bound.
func (p *Proxy) swapReplayLane(ctx context.Context, lane, path string, port int32, started time.Time) string {
	deadline := p.swapReplayDeadline(ctx, lane, started)
	for {
		m, _ := p.getModel(ctx, lane)
		if laneParked(m) {
			return p.swapRerouteTarget(ctx, m, path)
		}
		if p.laneServingAgain(ctx, m, port) {
			return lane
		}
		remaining := time.Until(deadline)
		if ctx.Err() != nil || remaining <= 0 {
			return p.swapRerouteTarget(ctx, m, path)
		}
		select {
		case <-ctx.Done():
		case <-time.After(min(swapReplayPoll, remaining)):
		}
	}
}

// finishSwapReplay either replays a held-back swap failure once or releases
// it to the client. originalBody is the client's request body before model
// rewriting, so the lane that serves the replay applies its own.
func (p *Proxy) finishSwapReplay(
	cw *swapCaptureWriter,
	w http.ResponseWriter,
	r *http.Request,
	lane string,
	port int32,
	originalBody []byte,
	started time.Time,
) {
	if !cw.captured() {
		return
	}
	ctx := r.Context()
	if ctx.Err() != nil {
		_ = cw.flushCaptured()
		return
	}
	m, _ := p.getModel(ctx, lane)
	signal := swapSignal(m, cw.buf.Bytes(), time.Now())
	if signal == "" {
		_ = cw.flushCaptured()
		return
	}
	served := p.swapReplayLane(ctx, lane, r.URL.Path, port, started)
	if served == "" || ctx.Err() != nil {
		slog.Warn("swap replay: no lane can serve within the bound; returning the original failure",
			append([]any{"model", lane, "signal", signal, "status", cw.status}, callerIdentityFrom(r).logAttrs()...)...)
		swapReplaysTotal.WithLabelValues(lane, replayNoLane).Inc()
		_ = cw.flushCaptured()
		return
	}

	outcome := replaySameLane
	if served != lane {
		outcome = replayReroute
		p.updateLastAccess(ctx, served)
	}
	slog.Info("swap replay",
		append([]any{
			"event", "swap_replay",
			"model", lane,
			"served_by", served,
			"signal", signal,
			"status", cw.status,
			"path", r.URL.Path,
			"waited_ms", time.Since(started).Milliseconds(),
		}, callerIdentityFrom(r).logAttrs()...)...)

	w.Header().Set(headerSwapReplayed, lane+"->"+served)
	replay := r.Clone(withSwapReplayed(ctx))
	replay.Body = io.NopCloser(bytes.NewReader(originalBody))
	replay.ContentLength = int64(len(originalBody))
	rec := &statusRecorder{ResponseWriter: w}
	p.serveProxy(rec, replay, served)
	if rec.status == 0 || rec.status >= http.StatusInternalServerError {
		outcome = replayFailed
	}
	swapReplaysTotal.WithLabelValues(lane, outcome).Inc()
}

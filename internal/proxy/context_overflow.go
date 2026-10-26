package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Context-overflow reroute (CC-6a-3 of docs/planning/context-bounded-admission-spec.md).
//
// A Model annotated with flexinfer.ai/context-overflow-to names a sibling lane
// with a larger context window. When the Model's runtime rejects a completion
// because the prompt does not fit (vLLM's 400 "maximum context length" /
// "maximum model length"), the proxy replays the original request once to that
// sibling instead of returning the 400. The runtime's own check decides, so the
// proxy never estimates tokens, and every caller — proxy-direct clients and
// LiteLLM, which forwards through this proxy — gets the same behaviour.
//
// Only 400 responses are held back; every other status, and every streamed
// body, passes straight through. vLLM validates the prompt before it starts a
// stream, so a context rejection always arrives as a complete JSON 400.
const annotationContextOverflowTo = "flexinfer.ai/context-overflow-to"

// headerContextOverflow tells the caller which lane served the request.
const headerContextOverflow = "X-FlexInfer-Context-Overflow"

// maxCapturedErrorBytes bounds the held-back 400 body. Runtime errors are a few
// hundred bytes; anything larger is passed through untouched.
const maxCapturedErrorBytes = 64 << 10

type contextOverflowCtxKey struct{}

// withContextOverflow marks a request as already rerouted once, so a target
// that itself names an overflow lane cannot chain or loop.
func withContextOverflow(ctx context.Context, from string) context.Context {
	return context.WithValue(ctx, contextOverflowCtxKey{}, from)
}

func contextOverflowedFrom(ctx context.Context) string {
	from, _ := ctx.Value(contextOverflowCtxKey{}).(string)
	return from
}

// contextOverflowTarget returns the Model to reroute over-length prompts to, or
// "" when the Model has no overflow lane (the default: no behaviour change).
func (p *Proxy) contextOverflowTarget(ctx context.Context, modelName string) string {
	if p.client == nil {
		return ""
	}
	m, err := p.getModel(ctx, modelName)
	if err != nil {
		return ""
	}
	target := strings.TrimSpace(m.Annotations[annotationContextOverflowTo])
	if target == "" {
		return ""
	}
	if p.resolver != nil {
		target = p.resolver.ResolveModelAlias(ctx, target)
	}
	if target == modelName {
		return ""
	}
	return target
}

// isContextLengthError reports whether a runtime 400 body is a prompt-length
// rejection. vLLM 0.32 phrases it as "This model's maximum context length is
// N tokens…" (renderer), "…longer than the maximum model length of N" (input
// processor) or "Input length (N) exceeds model's maximum context length (N)"
// (max_tokens resolution); max_tokens alone never errors, vLLM trims it.
func isContextLengthError(body []byte) bool {
	lower := bytes.ToLower(body)
	return bytes.Contains(lower, []byte("maximum context length")) ||
		bytes.Contains(lower, []byte("maximum model length"))
}

// overflowCaptureWriter holds back a 400 response so the proxy can decide to
// reroute it; any other status is forwarded to the client as it arrives.
type overflowCaptureWriter struct {
	w       http.ResponseWriter
	header  http.Header
	status  int
	capture bool
	buf     bytes.Buffer
}

func newOverflowCaptureWriter(w http.ResponseWriter) *overflowCaptureWriter {
	return &overflowCaptureWriter{w: w, header: w.Header().Clone()}
}

func (c *overflowCaptureWriter) Header() http.Header { return c.header }

func (c *overflowCaptureWriter) WriteHeader(code int) {
	if code < http.StatusOK {
		// Informational responses never decide the outcome; pass them on.
		c.copyHeader()
		c.w.WriteHeader(code)
		return
	}
	if c.status != 0 {
		return
	}
	c.status = code
	if code == http.StatusBadRequest {
		c.capture = true
		return
	}
	c.copyHeader()
	c.w.WriteHeader(code)
}

func (c *overflowCaptureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.WriteHeader(http.StatusOK)
	}
	if !c.capture {
		return c.w.Write(b)
	}
	if c.buf.Len()+len(b) > maxCapturedErrorBytes {
		// Too large to be a runtime error; stop holding it back.
		if err := c.flushCaptured(); err != nil {
			return 0, err
		}
		return c.w.Write(b)
	}
	return c.buf.Write(b)
}

// Flush forwards streaming flushes only once the response is passing through.
func (c *overflowCaptureWriter) Flush() {
	if c.capture {
		return
	}
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *overflowCaptureWriter) Unwrap() http.ResponseWriter { return c.w }

// captured reports whether a 400 is still being held back.
func (c *overflowCaptureWriter) captured() bool { return c.capture }

// flushCaptured sends the held-back 400 to the client unchanged.
func (c *overflowCaptureWriter) flushCaptured() error {
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

func (c *overflowCaptureWriter) copyHeader() {
	dst := c.w.Header()
	for k, v := range c.header {
		dst[k] = v
	}
}

// finishContextOverflow either reroutes a held-back context-length 400 to the
// overflow lane or releases it to the client. originalBody is the client's
// request body before model rewriting and max_tokens clamping, so the overflow
// lane applies its own.
func (p *Proxy) finishContextOverflow(
	cw *overflowCaptureWriter,
	w http.ResponseWriter,
	r *http.Request,
	modelName, target string,
	originalBody []byte,
) {
	if !cw.captured() {
		return
	}
	ctx := r.Context()
	if !isContextLengthError(cw.buf.Bytes()) {
		_ = cw.flushCaptured()
		return
	}
	tm, err := p.getModel(ctx, target)
	if err != nil || tm.Status.Phase != aiv1alpha2.ModelPhaseReady {
		// Never cold-start the overflow lane from here: on a shared GPU that
		// could preempt its leader. The client gets the original 400.
		phase := ""
		if tm != nil {
			phase = string(tm.Status.Phase)
		}
		slog.Warn("context overflow lane unavailable; returning the original 400",
			"model", modelName, "target", target, "phase", phase, "error", err)
		contextOverflowTotal.WithLabelValues(modelName, target, "target_unavailable").Inc()
		_ = cw.flushCaptured()
		return
	}
	// The replay is forwarded under the target's lane: load its first-byte
	// budget (flexinfer.ai/lane-first-byte-budget, sized for a long prefill)
	// and stay off a lane whose breaker is open, so over-length prompts can
	// neither time out on the default budget nor keep a tripped lane busy.
	if p.laneConfig.LaneBreakerThreshold > 0 {
		if open, _, _ := p.prepareLane(target, tm.Annotations); open {
			slog.Warn("context overflow lane breaker open; returning the original 400",
				"model", modelName, "target", target)
			contextOverflowTotal.WithLabelValues(modelName, target, "target_unavailable").Inc()
			_ = cw.flushCaptured()
			return
		}
	}

	slog.Info("context overflow reroute",
		append([]any{
			"event", "context_overflow",
			"model", modelName,
			"target", target,
			"path", r.URL.Path,
		}, callerIdentityFrom(r).logAttrs()...)...)
	contextOverflowTotal.WithLabelValues(modelName, target, "rerouted").Inc()

	// The overflow lane sets its own clamp header if it clamps.
	w.Header().Del("X-FlexInfer-MaxTokens-Clamped")
	w.Header().Set(headerContextOverflow, modelName+"->"+target)
	replay := r.Clone(withContextOverflow(ctx, modelName))
	replay.Body = io.NopCloser(bytes.NewReader(originalBody))
	replay.ContentLength = int64(len(originalBody))
	p.updateLastAccess(ctx, target)
	p.serveProxy(w, replay, target)
}

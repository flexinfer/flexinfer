package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
	"github.com/flexinfer/flexinfer/pkg/validation"
)

// Self-healing upstream forwarding.
//
// The proxy caches a per-model fast-path target in p.directLoadTargets
// ("http://<podIP>:<port>") for models served by the multi-subprocess runtime.
// That map is populated at startup (recoverDirectLoadTargets) and on activation
// (tryDirectRuntimeLoad) but was never invalidated and is not periodically
// refreshed. So when a runtime pod restarted (new pod IP / per-model port), the
// stale entry pinned a dead address and every request dialed it and 502'd until
// the proxy itself restarted or the model re-activated.
//
// forwardWithRetry closes that gap: on a connection-level (dial) failure it
// drops the stale direct-load target so subsequent requests re-resolve, and it
// retries the in-flight request once against a freshly resolved target. Retrying
// is safe because httputil.ReverseProxy only invokes its ErrorHandler for
// RoundTrip (pre-response) errors — at that point nothing has been written to
// the client, so the buffered request body can be replayed without corrupting a
// partially-sent response.

const (
	defaultMaxForwardAttempts = 2
	upstreamRetryBackoff      = 50 * time.Millisecond
	maxForwardAttemptsCap     = 3
	// envMaxForwardAttempts tunes (or disables, with "1") the upstream retry.
	envMaxForwardAttempts = "FLEXINFER_PROXY_UPSTREAM_MAX_ATTEMPTS"
)

// forwardResult carries the upstream transport error (if any) from the
// ReverseProxy ErrorHandler back to forwardWithRetry. A non-nil err means the
// RoundTrip failed before any response byte was written to the client.
type forwardResult struct {
	err error
}

type forwardResultCtxKey struct{}

func withForwardResult(ctx context.Context, fr *forwardResult) context.Context {
	return context.WithValue(ctx, forwardResultCtxKey{}, fr)
}

func forwardResultFromContext(ctx context.Context) *forwardResult {
	fr, _ := ctx.Value(forwardResultCtxKey{}).(*forwardResult)
	return fr
}

// maxForwardAttempts returns the total number of forward attempts (>=1).
// Configurable via FLEXINFER_PROXY_UPSTREAM_MAX_ATTEMPTS, clamped to [1, 3].
// "1" disables retry (single attempt, the legacy behavior).
func maxForwardAttempts() int {
	v := os.Getenv(envMaxForwardAttempts)
	if v == "" {
		return defaultMaxForwardAttempts
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultMaxForwardAttempts
	}
	if n > maxForwardAttemptsCap {
		return maxForwardAttemptsCap
	}
	return n
}

// classifyUpstreamErr reports whether an upstream forwarding error is a
// connection-establishment failure worth retrying against a fresh target, plus
// a short reason label for metrics. These are exactly the symptoms of a moved /
// restarting backend pod (stale fast-path target, rolling runtime).
func classifyUpstreamErr(err error) (reason string, retryable bool) {
	if err == nil {
		return "", false
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "conn_refused", true
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "host_unreachable", true
	case errors.Is(err, syscall.ECONNRESET):
		return "conn_reset", true
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof", true
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timeout", true
	}
	return "other", false
}

// resolveTargetURL picks the upstream target for a request. On the first attempt
// it honors strategy routing and the direct-load fast path. On retry
// (skipDirect=true) it bypasses both and goes straight to the Service DNS name,
// which load-balances across the model's Ready endpoints via kube-proxy — so a
// retry never re-selects the same dead pod that just failed.
//
// fromDirect reports that the returned target came from the direct-load cache,
// which is the only target class that can go stale without invalidation.
func (p *Proxy) resolveTargetURL(ctx context.Context, r *http.Request, resolvedModel string, port int32, bodyBytes []byte, skipDirect bool) (targetURL string, targetPod string, fromDirect bool) {
	if !skipDirect {
		strategy := p.getRoutingStrategy(ctx, resolvedModel)
		if strategy != routing.StrategyDefault && p.router != nil {
			decision := p.router.RouteWithDecision(resolvedModel, strategy, r, bodyBytes, p.getPodConnections)
			targetPod = decision.Target
			if targetPod != "" {
				targetURL = fmt.Sprintf("http://%s", targetPod)
			}
			p.recordRoutingObservability(resolvedModel, strategy, decision, targetPod)
		}
		if targetURL == "" {
			if dt, ok := p.directLoadTargets.Load(resolvedModel); ok {
				return dt, "", true
			}
		}
	}
	if targetURL == "" {
		targetURL = k8surl.ServiceURL(resolvedModel, p.namespace, port, true)
	}
	return targetURL, targetPod, false
}

// forwardWithRetry forwards the request to the resolved upstream, self-healing
// stale direct-load targets and retrying once on a dial-class failure.
//
//   - routeBody is the original request body used for routing decisions
//     (strategy routing keys on the original payload, before model rewriting).
//   - forwardBody is the final body sent upstream (after model-name rewrite +
//     max_tokens clamp). It is replayed on each attempt. Either may be nil.
func (p *Proxy) forwardWithRetry(w http.ResponseWriter, r *http.Request, modelName, resolvedModel string, port int32, routeBody, forwardBody []byte) {
	ctx := r.Context()
	maxAttempts := maxForwardAttempts()

	maxTokensForLog, streamForLog := parseRequestForUsageLog(forwardBody)
	startedAt := time.Now()
	userAgent := r.Header.Get("User-Agent")
	wantPrefix := prefixHitOptIn(r.Header.Get(headerWantPrefixHit))
	path := r.URL.Path

	for attempt := 0; attempt < maxAttempts; attempt++ {
		targetURL, targetPod, fromDirect := p.resolveTargetURL(ctx, r, resolvedModel, port, routeBody, attempt > 0)

		// Replay the buffered body for this attempt.
		if len(forwardBody) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(forwardBody))
			r.ContentLength = int64(len(forwardBody))
		}

		fr := &forwardResult{}
		areq := r.WithContext(withForwardResult(withUsageLogCtx(ctx, &usageLogCtx{
			model:         modelName,
			resolvedModel: resolvedModel,
			path:          path,
			maxTokens:     maxTokensForLog,
			stream:        streamForLog,
			userAgent:     userAgent,
			startedAt:     startedAt,
			wantPrefixHit: wantPrefix,
			targetURL:     targetURL,
		}), fr))

		rp, ok := p.loadOrCreateProxy(targetURL)
		if !ok {
			validation.WriteInternalError(w, "Internal error routing request")
			return
		}

		if targetPod != "" {
			p.incrementPodConnections(targetPod)
		}
		if attempt > 0 {
			slog.Debug("retrying upstream after dial failure",
				"model", resolvedModel, "attempt", attempt, "target", targetURL)
		}
		rp.ServeHTTP(w, areq)
		if targetPod != "" {
			p.decrementPodConnections(targetPod)
		}

		// fr.err is set only by the ReverseProxy ErrorHandler, which fires only
		// on a pre-response RoundTrip error. Nil => the response was forwarded
		// (success or a real upstream HTTP status) and was written to the client.
		if fr.err == nil {
			return
		}

		// A stale fast-path target can pin a dead pod indefinitely; drop it so
		// future requests (and our own retry) re-resolve via Service DNS.
		if fromDirect {
			p.directLoadTargets.Delete(resolvedModel)
			slog.Warn("invalidated stale direct-load target after upstream dial failure",
				"model", resolvedModel, "target", targetURL, "error", fr.err)
		}

		reason, retryable := classifyUpstreamErr(fr.err)
		if !retryable || attempt == maxAttempts-1 {
			slog.Warn("upstream forward failed",
				"model", resolvedModel, "target", targetURL,
				"attempt", attempt, "retryable", retryable, "error", fr.err)
			break
		}
		upstreamRetriesTotal.WithLabelValues(resolvedModel, reason).Inc()
		time.Sleep(upstreamRetryBackoff)
	}

	// All attempts failed at the transport layer; the ErrorHandler suppressed
	// its own write, so nothing has been sent to the client yet.
	validation.WriteError(w, http.StatusBadGateway, "upstream unavailable", "upstream_error", "bad_gateway")
}

package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// codebaseAnswerUpstreamPath is the path the codebase-answer sibling service
// serves. The proxy exposes it as /v1/rag (the platform front-door route) and
// rewrites the upstream path to this.
const codebaseAnswerUpstreamPath = "/v1/answer"

// handleCodebaseAnswer reverse-proxies POST /v1/rag to the codebase-answer
// read-path service (FLEXINFER_CODEBASE_ANSWER_UPSTREAM), exposing
// retrieval-augmented codebase Q&A (embed -> qdrant -> rerank -> generate)
// through the platform front door. Like /diarize and /v1/audio/speech, the
// sibling is a hand-written Deployment routed by static path, not a Model CR.
// The sibling serves POST /v1/answer, so the upstream path is rewritten
// /v1/rag -> /v1/answer.
//
// Returns 503 when the upstream is unconfigured. RAG answers are low-traffic
// (interactive agent queries), so a per-request ReverseProxy is fine — no pooling.
func (p *Proxy) handleCodebaseAnswer(w http.ResponseWriter, r *http.Request) {
	if p.codebaseAnswerUpstream == "" {
		http.Error(w, "codebase-answer upstream not configured (FLEXINFER_CODEBASE_ANSWER_UPSTREAM unset)", http.StatusServiceUnavailable)
		return
	}
	target, err := url.Parse(p.codebaseAnswerUpstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		slog.Error("invalid codebase-answer upstream URL", "upstream", p.codebaseAnswerUpstream, "error", err)
		http.Error(w, "codebase-answer upstream misconfigured", http.StatusInternalServerError)
		return
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	defaultDirector := rp.Director
	rp.Director = func(req *http.Request) {
		defaultDirector(req)
		req.URL.Path = codebaseAnswerUpstreamPath // expose /v1/rag, forward to sibling /v1/answer
	}
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		slog.Error("codebase-answer upstream error", "upstream", p.codebaseAnswerUpstream, "error", err)
		http.Error(w, "codebase-answer upstream unavailable", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

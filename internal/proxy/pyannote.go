package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// handleDiarize reverse-proxies POST /diarize to the pyannote diarization
// sibling service (FLEXINFER_PYANNOTE_UPSTREAM), giving ICC a single base URL
// for both ASR (/v1/audio/transcriptions, served by the Whisper Model CR) and
// speaker diarization (this route). The pyannote service is a hand-written
// Deployment on gfx906, not a Model CR, so it has no OpenAI `model` field and
// is routed by static path prefix rather than the model resolver.
//
// Returns 503 when the upstream is unconfigured. Diarization is low-traffic
// (post-meeting batch), so a per-request ReverseProxy is fine — no pooling.
func (p *Proxy) handleDiarize(w http.ResponseWriter, r *http.Request) {
	if p.pyannoteUpstream == "" {
		http.Error(w, "diarization upstream not configured (FLEXINFER_PYANNOTE_UPSTREAM unset)", http.StatusServiceUnavailable)
		return
	}
	target, err := url.Parse(p.pyannoteUpstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		slog.Error("invalid pyannote upstream URL", "upstream", p.pyannoteUpstream, "error", err)
		http.Error(w, "diarization upstream misconfigured", http.StatusInternalServerError)
		return
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		slog.Error("pyannote upstream error", "upstream", p.pyannoteUpstream, "error", err)
		http.Error(w, "diarization upstream unavailable", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// handleSpeech reverse-proxies POST /v1/audio/speech to the Kokoro TTS sibling
// service (FLEXINFER_KOKORO_UPSTREAM), completing the voice stack: ASR
// (/v1/audio/transcriptions, Whisper Model CR), diarization (/diarize, pyannote
// Deployment), and now text-to-speech under one base URL. Kokoro is a
// pre-built OpenAI-compatible FastAPI Deployment (remsky/Kokoro-FastAPI), not a
// Model CR, so — like /diarize — it is routed by static path rather than the
// model resolver (its `model: "kokoro"` body field is not a flexinfer Model).
//
// Returns 503 when the upstream is unconfigured. The 2026-06-02 kill-test
// measured RTF ~0.12 on CPU, so a per-request ReverseProxy is fine — no pooling.
func (p *Proxy) handleSpeech(w http.ResponseWriter, r *http.Request) {
	if p.kokoroUpstream == "" {
		http.Error(w, "TTS upstream not configured (FLEXINFER_KOKORO_UPSTREAM unset)", http.StatusServiceUnavailable)
		return
	}
	target, err := url.Parse(p.kokoroUpstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		slog.Error("invalid kokoro upstream URL", "upstream", p.kokoroUpstream, "error", err)
		http.Error(w, "TTS upstream misconfigured", http.StatusInternalServerError)
		return
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		slog.Error("kokoro upstream error", "upstream", p.kokoroUpstream, "error", err)
		http.Error(w, "TTS upstream unavailable", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

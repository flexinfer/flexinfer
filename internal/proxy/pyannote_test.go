package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleDiarize_NotConfigured(t *testing.T) {
	p := setupTestProxy(t)
	p.pyannoteUpstream = ""

	req := httptest.NewRequest(http.MethodPost, "/diarize", nil)
	rec := httptest.NewRecorder()
	p.handleDiarize(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleDiarize_InvalidUpstream(t *testing.T) {
	p := setupTestProxy(t)
	p.pyannoteUpstream = "not-a-url"

	req := httptest.NewRequest(http.MethodPost, "/diarize", nil)
	rec := httptest.NewRecorder()
	p.handleDiarize(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleDiarize_ForwardsToUpstream(t *testing.T) {
	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"segments":[{"start":0,"end":12.97,"speaker":"SPEAKER_00"}],"num_speakers":2}`))
	}))
	defer upstream.Close()

	p := setupTestProxy(t)
	p.pyannoteUpstream = upstream.URL

	req := httptest.NewRequest(http.MethodPost, "/diarize", strings.NewReader("fake-audio-bytes"))
	rec := httptest.NewRecorder()
	p.handleDiarize(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/diarize", gotPath)
	assert.Equal(t, "fake-audio-bytes", gotBody)
	assert.Contains(t, rec.Body.String(), "SPEAKER_00")
	assert.Contains(t, rec.Body.String(), "num_speakers")
}

func TestHandleDiarize_UpstreamDown(t *testing.T) {
	p := setupTestProxy(t)
	// Valid URL, but nothing is listening → ReverseProxy ErrorHandler → 502.
	p.pyannoteUpstream = "http://127.0.0.1:1"

	req := httptest.NewRequest(http.MethodPost, "/diarize", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	p.handleDiarize(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
}

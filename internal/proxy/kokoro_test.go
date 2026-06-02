package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSpeech_NotConfigured(t *testing.T) {
	p := setupTestProxy(t)
	p.kokoroUpstream = ""

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	rec := httptest.NewRecorder()
	p.handleSpeech(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleSpeech_InvalidUpstream(t *testing.T) {
	p := setupTestProxy(t)
	p.kokoroUpstream = "not-a-url"

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	rec := httptest.NewRecorder()
	p.handleSpeech(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleSpeech_ForwardsToUpstream(t *testing.T) {
	var gotPath, gotBody, gotContentType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFFfake-wav-bytes"))
	}))
	defer upstream.Close()

	p := setupTestProxy(t)
	p.kokoroUpstream = upstream.URL

	body := `{"model":"kokoro","voice":"af_heart","input":"hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleSpeech(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/v1/audio/speech", gotPath)
	assert.Equal(t, body, gotBody)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "audio/wav", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "fake-wav-bytes")
}

func TestHandleSpeech_UpstreamDown(t *testing.T) {
	p := setupTestProxy(t)
	// Valid URL, but nothing is listening → ReverseProxy ErrorHandler → 502.
	p.kokoroUpstream = "http://127.0.0.1:1"

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	p.handleSpeech(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
}

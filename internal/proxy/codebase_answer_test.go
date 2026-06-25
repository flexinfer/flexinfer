package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleCodebaseAnswer_NotConfigured(t *testing.T) {
	p := setupTestProxy(t)
	p.codebaseAnswerUpstream = ""

	req := httptest.NewRequest(http.MethodPost, "/v1/rag", nil)
	rec := httptest.NewRecorder()
	p.handleCodebaseAnswer(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleCodebaseAnswer_InvalidUpstream(t *testing.T) {
	p := setupTestProxy(t)
	p.codebaseAnswerUpstream = "not-a-url"

	req := httptest.NewRequest(http.MethodPost, "/v1/rag", nil)
	rec := httptest.NewRecorder()
	p.handleCodebaseAnswer(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleCodebaseAnswer_ForwardsToUpstream(t *testing.T) {
	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"answer":"42","citations":[{"path":"pkg/x.go","score":0.8}]}`))
	}))
	defer upstream.Close()

	p := setupTestProxy(t)
	p.codebaseAnswerUpstream = upstream.URL

	req := httptest.NewRequest(http.MethodPost, "/v1/rag", strings.NewReader(`{"query":"q"}`))
	rec := httptest.NewRecorder()
	p.handleCodebaseAnswer(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// /v1/rag must be rewritten to the sibling's /v1/answer path.
	assert.Equal(t, "/v1/answer", gotPath)
	assert.Equal(t, `{"query":"q"}`, gotBody)
	assert.Contains(t, rec.Body.String(), "citations")
}

func TestHandleCodebaseAnswer_UpstreamDown(t *testing.T) {
	p := setupTestProxy(t)
	// Valid URL, but nothing is listening → ReverseProxy ErrorHandler → 502.
	p.codebaseAnswerUpstream = "http://127.0.0.1:1"

	req := httptest.NewRequest(http.MethodPost, "/v1/rag", strings.NewReader(`{"query":"q"}`))
	rec := httptest.NewRecorder()
	p.handleCodebaseAnswer(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
}

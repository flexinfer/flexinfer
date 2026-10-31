package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forwardedBody sends one request through handleRequest and returns the body
// the lane received.
func forwardedBody(t *testing.T, p *Proxy, model, path, body string, headers map[string]string) map[string]any {
	t.Helper()
	got := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	p.directLoadTargets.Store(model, server.URL)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.handleRequest(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var fields map[string]any
	require.NoError(t, json.Unmarshal(<-got, &fields))
	return fields
}

func TestPriorityLaneSetsVLLMPriorityFromClass(t *testing.T) {
	p, model := qosLaneTestProxy(t, nil, `{"maxNumSeqs":2,"schedulingPolicy":"priority"}`)
	chat := `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"priority":-5}`

	fields := forwardedBody(t, p, model.Name, "/v1/chat/completions", chat, map[string]string{headerQoS: "background"})
	assert.EqualValues(t, 10, fields["priority"])
	assert.NotNil(t, fields["messages"], "the rest of the body is kept")

	fields = forwardedBody(t, p, model.Name, "/v1/chat/completions", chat, nil)
	assert.EqualValues(t, 0, fields["priority"], "an interactive caller cannot promote itself with a negative priority")

	fields = forwardedBody(t, p, model.Name, "/v1/completions",
		`{"model":"test-model","prompt":"hi"}`, map[string]string{headerQoS: "batch"})
	assert.EqualValues(t, 20, fields["priority"])

	fields = forwardedBody(t, p, model.Name, "/v1/embeddings",
		`{"model":"test-model","input":"hi"}`, map[string]string{headerQoS: "background"})
	assert.NotContains(t, fields, "priority", "only completion requests carry the priority")
}

func TestNonPriorityLanesForwardBodyUnchanged(t *testing.T) {
	for name, tc := range map[string]struct {
		annotations map[string]string
		config      string
	}{
		"fcfs lane":            {nil, `{"maxNumSeqs":2}`},
		"proxy-only lane":      {map[string]string{annotationQoS: "proxy"}, `{"maxNumSeqs":2}`},
		"priority lane off":    {map[string]string{annotationQoS: "off"}, `{"maxNumSeqs":2,"schedulingPolicy":"priority"}`},
		"explicit fcfs policy": {nil, `{"maxNumSeqs":2,"schedulingPolicy":"fcfs"}`},
	} {
		t.Run(name, func(t *testing.T) {
			p, model := qosLaneTestProxy(t, tc.annotations, tc.config)
			fields := forwardedBody(t, p, model.Name, "/v1/chat/completions",
				`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`,
				map[string]string{headerQoS: "background"})
			assert.NotContains(t, fields, "priority", "vLLM rejects a non-zero priority without priority scheduling")
		})
	}
}

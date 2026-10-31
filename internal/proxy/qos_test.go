package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func qosTestPolicy(t *testing.T, yaml string) *qosResolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qos.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	q := newQoSResolver(path)
	require.NotNil(t, q.current())
	return q
}

func TestParseQoSClass(t *testing.T) {
	for in, want := range map[string]qosClass{
		"interactive": qosInteractive,
		"Background":  qosBackground,
		" BATCH ":     qosBatch,
	} {
		got, ok := parseQoSClass(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "realtime", "bg"} {
		_, ok := parseQoSClass(in)
		assert.False(t, ok, in)
	}
}

func TestQoSClassVLLMPriority(t *testing.T) {
	assert.Equal(t, 0, qosInteractive.vllmPriority())
	assert.Equal(t, 10, qosBackground.vllmPriority())
	assert.Equal(t, 20, qosBatch.vllmPriority())
}

func TestSplitQoSAliasSuffix(t *testing.T) {
	base, class, ok := splitQoSAliasSuffix("workhorse-background")
	assert.True(t, ok)
	assert.Equal(t, "workhorse", base)
	assert.Equal(t, qosBackground, class)

	base, class, ok = splitQoSAliasSuffix("workhorse-batch")
	assert.True(t, ok)
	assert.Equal(t, "workhorse", base)
	assert.Equal(t, qosBatch, class)

	for _, name := range []string{"workhorse", "-background", "workhorse-interactive", "background"} {
		_, _, ok := splitQoSAliasSuffix(name)
		assert.False(t, ok, name)
	}
}

func TestParseQoSPolicy(t *testing.T) {
	key := sha256Hex("sk-mills")
	p, err := parseQoSPolicy([]byte("callers:\n  Loom-Mills: background\napiKeys:\n  \"sha256:" + strings.ToUpper(key) + "\": batch\n"))
	require.NoError(t, err)
	assert.Equal(t, qosBackground, p.callers["loom-mills"])
	assert.Equal(t, qosBatch, p.apiKeys[key])

	empty, err := parseQoSPolicy(nil)
	require.NoError(t, err)
	assert.Empty(t, empty.callers)

	_, err = parseQoSPolicy([]byte("callers:\n  loom-mills: urgent\n"))
	assert.ErrorContains(t, err, "unknown qos class")
	_, err = parseQoSPolicy([]byte("apiKeys:\n  sk-plaintext: background\n"))
	assert.ErrorContains(t, err, "sha256")
}

func TestQoSResolverReloadKeepsPreviousOnBadEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qos.yaml")
	q := newQoSResolver(path)
	require.NotNil(t, q.current(), "a missing file is an empty policy")
	assert.Empty(t, q.current().callers)

	require.NoError(t, os.WriteFile(path, []byte("callers:\n  loom-mills: background\n"), 0o600))
	q.reload()
	assert.Equal(t, qosBackground, q.current().callers["loom-mills"])

	require.NoError(t, os.WriteFile(path, []byte("callers:\n  loom-mills: nope\n"), 0o600))
	q.reload()
	assert.Equal(t, qosBackground, q.current().callers["loom-mills"], "a bad edit keeps the previous map")

	require.NoError(t, os.Remove(path))
	q.reload()
	assert.Empty(t, q.current().callers, "removing the file clears the map")

	var nilResolver *qosResolver
	assert.Nil(t, nilResolver.current())
	nilResolver.reload()
}

func TestQoSCallerName(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("User-Agent", "Loom-Mills/1.4 (go1.24)")
	assert.Equal(t, "loom-mills", qosCallerName(r))
	r.Header.Set(headerCaller, "HUD")
	assert.Equal(t, "hud", qosCallerName(r))
	r = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Del("User-Agent")
	assert.Equal(t, "", qosCallerName(r))
}

func TestResolveQoSPrecedence(t *testing.T) {
	q := qosTestPolicy(t, "callers:\n  loom-mills: background\n  hud: interactive\napiKeys:\n  \"sha256:"+sha256Hex("sk-batch")+"\": batch\n")
	p := &Proxy{qos: q}

	newReq := func(headers map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	cases := []struct {
		name       string
		headers    map[string]string
		alias      qosClass
		wantClass  qosClass
		wantSource qosSource
	}{
		{"header beats everything", map[string]string{headerQoS: "batch", "User-Agent": "loom-mills/1", "Authorization": "Bearer sk-batch"}, qosBackground, qosBatch, qosSourceHeader},
		{"api key beats caller", map[string]string{"User-Agent": "hud/2", "Authorization": "Bearer sk-batch"}, "", qosBatch, qosSourceAPIKey},
		{"caller map", map[string]string{"User-Agent": "loom-mills/1"}, "", qosBackground, qosSourceCaller},
		{"caller header beats user agent", map[string]string{headerCaller: "loom-mills", "User-Agent": "hud/2"}, "", qosBackground, qosSourceCaller},
		{"caller map beats alias", map[string]string{"User-Agent": "hud/2"}, qosBackground, qosInteractive, qosSourceCaller},
		{"alias suffix", map[string]string{"User-Agent": "python-httpx/0.27"}, qosBackground, qosBackground, qosSourceAlias},
		{"unknown header falls through", map[string]string{headerQoS: "urgent"}, "", qosInteractive, qosSourceDefault},
		{"default", nil, "", qosInteractive, qosSourceDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newReq(tc.headers)
			d := p.resolveQoS(r, tc.alias)
			assert.Equal(t, tc.wantClass, d.class)
			assert.Equal(t, tc.wantSource, d.source)
			assert.Empty(t, r.Header.Get(headerQoS), "class header is stripped")
			assert.Empty(t, r.Header.Get(headerCaller), "caller header is stripped")
		})
	}

	// A proxy without a policy (struct-literal tests, unset path) still
	// resolves header, alias and default.
	bare := &Proxy{}
	assert.Equal(t, qosBackground, bare.resolveQoS(newReq(map[string]string{headerQoS: "background"}), "").class)
	assert.Equal(t, qosSourceDefault, bare.resolveQoS(newReq(map[string]string{"User-Agent": "loom-mills/1"}), "").source)
}

func TestStripQoSAliasSuffixHonoursExistingNames(t *testing.T) {
	p, _ := workloadClassTestProxy(t, aiv1alpha2.ModelPhaseReady)
	ctx := context.Background()

	name, class := p.stripQoSAliasSuffix(ctx, "test-model-background")
	assert.Equal(t, "test-model", name)
	assert.Equal(t, qosBackground, class)

	name, class = p.stripQoSAliasSuffix(ctx, "test-model")
	assert.Equal(t, "test-model", name)
	assert.Empty(t, class)

	// A Model whose own name ends in a QoS suffix keeps routing to itself.
	batchModel := &aiv1alpha2.Model{}
	batchModel.Name, batchModel.Namespace = "embed-batch", "default"
	batchModel.Spec.Backend, batchModel.Spec.Source = "vllm", "HF://org/embed"
	require.NoError(t, p.client.Create(ctx, batchModel))
	name, class = p.stripQoSAliasSuffix(ctx, "embed-batch")
	assert.Equal(t, "embed-batch", name)
	assert.Empty(t, class)
}

func TestHandleRequestLabelsQoSWithoutEnforcement(t *testing.T) {
	p, model := workloadClassTestProxy(t, aiv1alpha2.ModelPhaseReady)
	upstream := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream <- r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	p.directLoadTargets.Store(model.Name, server.URL)

	counter := qosDecisionsTotal.WithLabelValues("background", model.Name, qosOutcomeUnenforced)
	before := testutil.ToFloat64(counter)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test-model-background","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRequest(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	got := <-upstream
	assert.Empty(t, got.Header.Get(headerQoS))
	assert.Equal(t, before+1, testutil.ToFloat64(counter), "the alias suffix resolves background; the lane does not enforce it")

	// An explicit header on the base name resolves the same class.
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerQoS, "background")
	rec = httptest.NewRecorder()
	p.handleRequest(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	got = <-upstream
	assert.Empty(t, got.Header.Get(headerQoS), "the class header never reaches the model server")
	assert.Equal(t, before+2, testutil.ToFloat64(counter))
}

func TestLogUpstreamUsageObservesQoSTTFTForStreams(t *testing.T) {
	RegisterMetrics()
	newResp := func(stream bool) *http.Response {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		lc := &usageLogCtx{
			resolvedModel: "qos-ttft-lane",
			path:          "/v1/chat/completions",
			stream:        stream,
			startedAt:     time.Now().Add(-1500 * time.Millisecond),
			qosClass:      string(qosBackground),
			qosSource:     string(qosSourceCaller),
			caller:        "loom-mills",
		}
		req = req.WithContext(withUsageLogCtx(context.Background(), lc))
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: req}
		resp.Header.Set("Content-Type", "application/json")
		return resp
	}
	count := func() uint64 {
		m := &dto.Metric{}
		require.NoError(t, qosTTFT.WithLabelValues("background", "qos-ttft-lane").(prometheus.Histogram).Write(m))
		return m.GetHistogram().GetSampleCount()
	}

	before := count()
	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(newResp(true)))
	assert.Equal(t, before+1, count(), "a streamed completion records TTFT for its class")
	require.NoError(t, p.logUpstreamUsage(newResp(false)))
	assert.Equal(t, before+1, count(), "a non-streamed completion has no TTFT")
}

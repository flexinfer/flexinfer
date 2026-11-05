package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/modelmeta"
)

// schemaRequest is a chat request whose json_schema declares its properties
// in a deliberate, non-alphabetical order, including a property named "model"
// (with a string example) ahead of the top-level model key, and an HTML
// character in a pattern. Every byte outside the edited top-level values must
// reach the lane unchanged.
const schemaRequest = `{"response_format":{"type":"json_schema","json_schema":{"name":"car","strict":true,` +
	`"schema":{"type":"object","properties":{"reasoning":{"type":"string"},"model":{"type":"string","example":"gpt-4o"},` +
	`"brand":{"type":"string","pattern":"^[A-Z][a-z]+ <&>$"},"answer":{"type":"integer"}},` +
	`"required":["reasoning","model","brand","answer"],"additionalProperties":false}}},` +
	`"tools":[{"type":"function","function":{"name":"pick","parameters":{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"number"}}}}}],` +
	"\n  \"model\" : \"workhorse\",\n" +
	`"max_tokens":999999,"messages":[{"role":"user","content":"describe a car"}]}`

func rawField(t *testing.T, body []byte, key string) string {
	t.Helper()
	raw, ok := topLevelValue(body, key)
	require.True(t, ok, "top-level %q missing in %s", key, body)
	return string(raw)
}

func TestTopLevelFieldSpans(t *testing.T) {
	body := []byte(schemaRequest)
	require.Equal(t, `"workhorse"`, rawField(t, body, "model"))
	require.Equal(t, `999999`, rawField(t, body, "max_tokens"))
	require.True(t, strings.HasPrefix(rawField(t, body, "response_format"), `{"type":"json_schema"`))

	_, _, found, ok := topLevelField(body, "priority")
	require.True(t, ok)
	require.False(t, found)

	// Last occurrence wins, as encoding/json and vLLM's json.loads read it.
	dup := []byte(`{"model":"first","model":"second"}`)
	require.Equal(t, `"second"`, rawField(t, dup, "model"))

	for _, bad := range []string{``, `[1,2]`, `{"model":"x"`, `{"model":"x"} trailing`, `"model"`} {
		_, _, _, ok := topLevelField([]byte(bad), "model")
		require.False(t, ok, "%q must not parse as one object", bad)
	}
}

func TestSetTopLevelField(t *testing.T) {
	cases := []struct{ name, body, key, value, want string }{
		{"replace", `{"a":1,"model":"x","b":[1]}`, "model", `"y"`, `{"a":1,"model":"y","b":[1]}`},
		{"replace keeps whitespace", "{ \"model\" :  \"x\" }", "model", `"y"`, "{ \"model\" :  \"y\" }"},
		{"insert", `{"a":1}`, "priority", `10`, `{"a":1,"priority":10}`},
		{"insert before trailing whitespace", "{\"a\":1\n}\n", "priority", `10`, "{\"a\":1,\"priority\":10\n}\n"},
		{"insert into empty object", `{ }`, "priority", `0`, `{"priority":0 }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := setTopLevelField([]byte(tc.body), tc.key, []byte(tc.value))
			require.True(t, ok)
			require.Equal(t, tc.want, string(got))
			require.True(t, json.Valid(got))
		})
	}
	got, ok := setTopLevelField([]byte(`[1]`), "priority", []byte(`1`))
	require.False(t, ok)
	require.Equal(t, `[1]`, string(got))
}

// bytesOutside returns body with the [start,end) span of key's value removed,
// so two bodies can be compared on everything except that one value.
func bytesOutside(t *testing.T, body []byte, key string) string {
	t.Helper()
	start, end, found, ok := topLevelField(body, key)
	require.True(t, ok && found, "top-level %q missing", key)
	return string(body[:start]) + string(body[end:])
}

func TestRewriteModelLeavesNestedModelAndSchemaOrderAlone(t *testing.T) {
	p := &Proxy{}
	in := []byte(schemaRequest)
	out := p.rewriteModelInBody(in, "Qwen/Qwen3.8-27B")
	require.Equal(t, `"Qwen/Qwen3.8-27B"`, rawField(t, out, "model"))
	require.Equal(t, bytesOutside(t, in, "model"), bytesOutside(t, out, "model"),
		"only the top-level model value may change")
	require.Contains(t, string(out), `"model":{"type":"string","example":"gpt-4o"}`)

	// A nested string "model" ahead of the top-level key is not the target.
	nested := []byte(`{"metadata":{"model":"keep-me"},"model":"alias"}`)
	require.Equal(t, `{"metadata":{"model":"keep-me"},"model":"served"}`, string(p.rewriteModelInBody(nested, "served")))

	// No top-level model: the body is returned as sent, never re-encoded.
	noModel := []byte(`{"metadata":{"model":"keep-me"},"z":1,"a":2}`)
	require.Equal(t, string(noModel), string(p.rewriteModelInBody(noModel, "served")))
}

func TestClampMaxTokensLeavesSchemaBytesAlone(t *testing.T) {
	in := []byte(schemaRequest)
	out, orig, to := clampMaxTokensInBody(in, modelmeta.TokenLimits{ContextWindow: 32768}, 512)
	require.Equal(t, 999999, orig)
	require.Equal(t, 32768-512, to)
	require.Equal(t, `32256`, rawField(t, out, "max_tokens"))
	require.Equal(t, bytesOutside(t, in, "max_tokens"), bytesOutside(t, out, "max_tokens"))
}

func TestQoSPriorityLeavesSchemaBytesAlone(t *testing.T) {
	p, model := qosLaneTestProxy(t, nil, `{"maxNumSeqs":2,"schedulingPolicy":"priority"}`)
	in := []byte(schemaRequest)
	ctx := withQoSDecision(context.Background(), &qosDecision{class: qosBackground})
	out := p.applyQoSPriority(ctx, model.Name, "/v1/chat/completions", in)
	require.Equal(t, `10`, rawField(t, out, "priority"))
	require.Equal(t, strings.TrimSuffix(string(in), "}"), strings.TrimSuffix(bytesOutside(t, out, "priority"), `,"priority":}`))
}

func TestRequestConstraint(t *testing.T) {
	cases := []struct{ name, path, body, want string }{
		{"json_schema", "/v1/chat/completions", schemaRequest, "response_format.json_schema"},
		{"json_object", "/v1/chat/completions", `{"response_format":{"type":"json_object"}}`, "response_format.json_object"},
		{"structural_tag", "/v1/chat/completions", `{"response_format":{"type":"structural_tag","format":{}}}`, "response_format.structural_tag"},
		{"text is unconstrained", "/v1/chat/completions", `{"response_format":{"type":"text"}}`, ""},
		{"structured_outputs json", "/v1/completions", `{"structured_outputs":{"json":{"type":"object"}}}`, "structured_outputs.json"},
		{"structured_outputs choice", "/v1/chat/completions", `{"structured_outputs":{"choice":["a","b"]}}`, "structured_outputs.choice"},
		{"structured_outputs regex", "/v1/chat/completions", `{"structured_outputs":{"regex":"\\d+","json":null}}`, "structured_outputs.regex"},
		{"structured_outputs grammar", "/v1/chat/completions", `{"structured_outputs":{"grammar":"root ::= \"a\""}}`, "structured_outputs.grammar"},
		{"responses text.format", "/v1/responses", `{"text":{"format":{"type":"json_schema","name":"x","schema":{}}}}`, "text.format.json_schema"},
		{"required tool_choice", "/v1/chat/completions", `{"tool_choice":"required","tools":[]}`, "tool_choice.required"},
		{"named tool_choice", "/v1/chat/completions", `{"tool_choice":{"type":"function","function":{"name":"f"}}}`, "tool_choice.function"},
		{"auto tool_choice", "/v1/chat/completions", `{"tool_choice":"auto"}`, ""},
		{"plain chat", "/v1/chat/completions", `{"messages":[]}`, ""},
		{"embeddings are never tagged", "/v1/embeddings", `{"response_format":{"type":"json_object"}}`, ""},
		{"malformed", "/v1/chat/completions", `{"response_format":`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, requestConstraint(tc.path, []byte(tc.body)))
		})
	}
}

func TestLogUpstreamUsageTagsSchemaConstrainedRequests(t *testing.T) {
	RegisterMetrics()
	const model = "constrained-tag-lane"
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	mkResp := func(constraint string, status int) *http.Response {
		req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		require.NoError(t, err)
		req = req.WithContext(withUsageLogCtx(context.Background(), &usageLogCtx{
			resolvedModel: model, path: "/v1/chat/completions", startedAt: time.Now(), constraint: constraint,
		}))
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}
	}
	counter := func(code string) float64 {
		return promtestutil.ToFloat64(schemaConstrainedRequestsTotal.WithLabelValues(model, "response_format.json_schema", code))
	}
	before2xx, before4xx := counter("2xx"), counter("4xx")

	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(mkResp("response_format.json_schema", http.StatusOK)))
	require.NoError(t, p.logUpstreamUsage(mkResp("response_format.json_schema", http.StatusBadRequest)))
	require.NoError(t, p.logUpstreamUsage(mkResp("", http.StatusOK)))

	require.Equal(t, before2xx+1, counter("2xx"))
	require.Equal(t, before4xx+1, counter("4xx"))
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], "schema_constrained=true constraint=response_format.json_schema")
	require.NotContains(t, lines[2], "schema_constrained")
}

// TestContextOverflowRerouteForwardsSchemaUnchanged sends a schema-constrained
// request to a lane that overflows. The reroute target must receive the
// response_format, tools and messages bytes exactly as the client sent them,
// with only the top-level model renamed to the target's served name.
func TestContextOverflowRerouteForwardsSchemaUnchanged(t *testing.T) {
	f := newOverflowFixture(t, http.StatusBadRequest, vllmInputContextError, overflowToBig, nil, aiv1alpha2.ModelPhaseReady)
	var got []byte
	f.big.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	})
	body := strings.Replace(schemaRequest, `"workhorse"`, `"small"`, 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.p.trackAndServe(rec, req, "small", time.Now())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, int32(1), f.smallHits.Load())
	require.NotEmpty(t, got, "the overflow target never received the request")
	sent := []byte(body)
	for _, key := range []string{"response_format", "tools", "messages", "max_tokens"} {
		require.Equal(t, rawField(t, sent, key), rawField(t, got, key), "%s changed on reroute", key)
	}
	require.Equal(t, `"big-served"`, rawField(t, got, "model"))
}

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/flexinfer/flexinfer/internal/agentloop"
)

// fakeLane is an OpenAI-compatible chat endpoint plus vLLM-style /metrics.
// It answers each tool-call turn with a valid call for the scenario's tool,
// unless inject names a failure for that request, and moves its counters as
// a lane decoding at constrainedTPS / unguidedTPS with the given pos-0 rates.
type fakeLane struct {
	mu sync.Mutex

	constrainedTPS, unguidedTPS   float64
	constrainedPos0, unguidedPos0 float64
	noDrafts                      bool
	parityDriftAfter              int // parity calls after which answers change; 0 = never
	inject                        func(n int, constrained bool, mode string) string

	gen, requests, decodeSeconds float64
	drafts, draftTokens, pos0Acc float64
	parityCalls                  int
	constrainedCalls             int
	requestsSeen                 []fakeRequest
}

type fakeRequest struct {
	Messages          []agentloop.Message `json:"messages"`
	Tools             []agentloop.ToolDef `json:"tools"`
	ToolChoice        json.RawMessage     `json:"tool_choice"`
	ResponseFormat    json.RawMessage     `json:"response_format"`
	StructuredOutputs json.RawMessage     `json:"structured_outputs"`
	MaxTokens         int                 `json:"max_tokens"`
	Temperature       float64             `json:"temperature"`
	raw               []byte
}

func (r fakeRequest) constrained() bool {
	return len(r.ResponseFormat) > 0 || len(r.StructuredOutputs) > 0 ||
		(len(r.ToolChoice) > 0 && string(r.ToolChoice) != `"auto"`)
}

func (r fakeRequest) mode() string {
	switch {
	case len(r.Tools) > 0 && string(r.ToolChoice) == `"required"`:
		return tccModeRequired
	case len(r.Tools) > 0 && strings.HasPrefix(string(r.ToolChoice), "{"):
		return tccModeNamed
	case len(r.Tools) > 0:
		return "tools-auto"
	case len(r.ResponseFormat) > 0:
		return tccModeJSONSchema
	case len(r.StructuredOutputs) > 0:
		return tccModeStructuredOutputs
	}
	return "plain"
}

var fakePromptTool = func() map[string]string {
	m := map[string]string{}
	for _, sc := range tccScenarios(600) {
		m[sc.Prompt] = sc.Tool
	}
	return m
}()

var fakeValidArgs = map[string]string{
	"get_weather":           `{"city":"Lisbon","unit":"celsius"}`,
	"create_calendar_event": `{"title":"Retro","date":"2026-11-03","start_time":"09:00","duration_minutes":30,"attendees":["Ana"]}`,
	"search_documents":      `{"query":"gpu reset","source":"runbooks","top_k":5}`,
	"transfer_inventory":    `{"sku":"ABC-0042","quantity":12,"from_warehouse":"austin","to_warehouse":"reno"}`,
	"set_thermostat":        `{"zone":"office","temperature_c":21.5,"mode":"auto"}`,
	"lookup_order":          `{"order_id":"ORD-123456","include_items":true}`,
}

func newFakeLane() *fakeLane {
	return &fakeLane{constrainedTPS: 64, unguidedTPS: 66, constrainedPos0: 0.82, unguidedPos0: 0.81}
}

func (f *fakeLane) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", f.metrics)
	mux.HandleFunc("/v1/chat/completions", f.chat)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeLane) metrics(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = fmt.Fprintf(w, "# HELP vllm:generation_tokens_total x\n# TYPE vllm:generation_tokens_total counter\n")
	_, _ = fmt.Fprintf(w, "vllm:generation_tokens_total{engine=\"0\",model_name=\"m\"} %g\n", f.gen)
	_, _ = fmt.Fprintf(w, "vllm:request_success_total{finished_reason=\"stop\",model_name=\"m\"} %g\n", f.requests)
	_, _ = fmt.Fprintf(w, "vllm:request_decode_time_seconds_sum{model_name=\"m\"} %g\n", f.decodeSeconds)
	_, _ = fmt.Fprintf(w, "vllm:request_decode_time_seconds_count{model_name=\"m\"} %g\n", f.requests)
	if !f.noDrafts {
		_, _ = fmt.Fprintf(w, "vllm:spec_decode_num_drafts_total{model_name=\"m\"} %g\n", f.drafts)
		_, _ = fmt.Fprintf(w, "vllm:spec_decode_num_draft_tokens_total{model_name=\"m\"} %g\n", f.draftTokens)
		_, _ = fmt.Fprintf(w, "vllm:spec_decode_num_accepted_tokens_total{model_name=\"m\"} %g\n", f.pos0Acc*1.5)
		_, _ = fmt.Fprintf(w, "vllm:spec_decode_num_accepted_tokens_per_pos_total{model_name=\"m\",position=\"0\"} %g\n", f.pos0Acc)
		_, _ = fmt.Fprintf(w, "vllm:spec_decode_num_accepted_tokens_per_pos_total{model_name=\"m\",position=\"1\"} %g\n", f.pos0Acc/2)
	}
}

func (f *fakeLane) chat(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req fakeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.raw = raw
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestsSeen = append(f.requestsSeen, req)

	last := req.Messages[len(req.Messages)-1].Content
	const completion = 20
	f.gen += completion
	f.requests++
	tps, pos0 := f.unguidedTPS, f.unguidedPos0
	if req.constrained() {
		tps, pos0 = f.constrainedTPS, f.constrainedPos0
	}
	f.decodeSeconds += (completion - 1) / tps
	f.drafts += 10
	f.draftTokens += 20
	f.pos0Acc += 10 * pos0

	tool, isTurn := fakePromptTool[last]
	if !isTurn {
		f.parityCalls++
		answer := "greedy answer for " + last[:20]
		if f.parityDriftAfter > 0 && f.parityCalls > f.parityDriftAfter {
			answer += " (drifted)"
		}
		writeFakeChat(w, map[string]any{"role": "assistant", "content": answer}, "stop", completion)
		return
	}

	failure := ""
	if req.constrained() {
		f.constrainedCalls++
		if f.inject != nil {
			failure = f.inject(f.constrainedCalls, true, req.mode())
		}
	} else if f.inject != nil {
		failure = f.inject(0, false, req.mode())
	}
	args, name, finish := fakeValidArgs[tool], tool, "tool_calls"
	switch failure {
	case "http500":
		http.Error(w, `{"error":{"message":"engine dead"}}`, http.StatusInternalServerError)
		return
	case tccInvalidJSON:
		args = `{"city": "Lisb`
	case tccSchemaViolation:
		args = `{"unexpected":true}`
	case tccWrongToolName:
		name = "get_wether"
	case tccTruncated:
		finish = "length"
	case "prose":
		writeFakeChat(w, map[string]any{"role": "assistant", "content": "Sure, I can help with that."}, "stop", completion)
		return
	}
	if len(req.Tools) == 0 {
		content := fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, args)
		if finish == "tool_calls" {
			finish = "stop"
		}
		writeFakeChat(w, map[string]any{"role": "assistant", "content": content}, finish, completion)
		return
	}
	writeFakeChat(w, map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{
		"id": fmt.Sprintf("chatcmpl-tool-%d", len(f.requestsSeen)), "type": "function",
		"function": map[string]any{"name": name, "arguments": args},
	}}}, finish, completion)
}

func writeFakeChat(w http.ResponseWriter, msg map[string]any, finish string, completion int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 400, "completion_tokens": completion},
	})
}

func fakeParityHash(prompt string) string {
	sum := sha256.Sum256([]byte("greedy answer for " + prompt[:20]))
	return hex.EncodeToString(sum[:])[:16]
}

func runTCCForTest(t *testing.T, args ...string) (int, tccReport, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code, handled := runSubcommand(context.Background(), append([]string{"tool_call_conformance"}, args...), &stdout, &stderr)
	require.True(t, handled)
	var report tccReport
	if code != 2 {
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &report), "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	return code, report, stderr.String()
}

func checkStatus(r tccReport, name string) tccCheck {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return tccCheck{}
}

// matrixCells splits a matrix row on unescaped " | " separators.
func matrixCells(t *testing.T, row string) []string {
	t.Helper()
	require.True(t, strings.HasPrefix(row, "| ") && strings.HasSuffix(row, " |"), row)
	inner := row[2 : len(row)-2]
	var cells []string
	start := 0
	for i := 0; i+3 <= len(inner); i++ {
		if inner[i:i+3] == " | " && (i == 0 || inner[i-1] != '\\') {
			cells = append(cells, inner[start:i])
			start = i + 3
			i += 2
		}
	}
	return append(cells, inner[start:])
}

func TestTCCPassesAgainstAConformingLane(t *testing.T) {
	lane := newFakeLane()
	srv := lane.serve(t)
	baseline := "parity_code=" + fakeParityHash(tccParityPrompts[0].Prompt)[:8]
	code, report, stderr := runTCCForTest(t, "--url", srv.URL, "--model", "m", "--turns", "24",
		"--session-turns", "3", "--warmup-turns", "4", "--parity-baseline", baseline,
		"--image", "registry.harbor.lan/flexinfer/runtime@sha256:b06b06fe", "--context-length", "32768")

	require.Equal(t, 0, code, stderr)
	require.True(t, report.Pass)
	require.Equal(t, "PASS", report.Verdict)
	require.Equal(t, srv.URL+"/metrics", report.MetricsURL)
	for _, name := range []string{"parse_success", "decode_vs_unguided", "pos0_acceptance", "greedy_parity"} {
		require.Equal(t, "pass", checkStatus(report, name).Status, "%s: %+v", name, checkStatus(report, name))
	}
	c := report.Constrained
	require.Equal(t, 24, c.Turns)
	require.Equal(t, 24, c.OK)
	require.Equal(t, 1.0, c.ExpectedToolAccuracy)
	for _, mode := range tccAllModes {
		require.Equal(t, tccModeStats{Turns: 6, OK: 6}, c.ByMode[mode], mode)
	}
	require.NotNil(t, c.Engine)
	require.InDelta(t, 64, *c.Engine.DecodeTokensPerSecond, 0.01)
	require.InDelta(t, 0.82, *c.Engine.Pos0Acceptance, 0.0001)
	require.InDelta(t, 66, *report.Unguided.Engine.DecodeTokensPerSecond, 0.01)
	require.Equal(t, 24, report.Unguided.Turns)
	require.Contains(t, checkStatus(report, "decode_vs_unguided").Value, "0.970")

	// The lane saw every constraint shape, and the unguided arm none.
	shapes := map[string]int{}
	lane.mu.Lock()
	for _, req := range lane.requestsSeen {
		key := req.mode()
		if key == tccModeNamed {
			var choice struct {
				Function struct{ Name string } `json:"function"`
			}
			require.NoError(t, json.Unmarshal(req.ToolChoice, &choice))
			require.Equal(t, fakePromptTool[req.Messages[len(req.Messages)-1].Content], choice.Function.Name, "named tool_choice must force the scenario's tool")
		}
		if key == tccModeJSONSchema {
			require.Contains(t, string(req.ResponseFormat), `"type":"json_schema"`)
			require.Contains(t, string(req.ResponseFormat), `"strict":true`)
			require.Contains(t, string(req.ResponseFormat), `"anyOf":[`)
		}
		if key == tccModeStructuredOutputs {
			require.True(t, strings.HasPrefix(string(req.StructuredOutputs), `{"json":{"anyOf":[`))
		}
		require.Zero(t, req.Temperature)
		shapes[key]++
	}
	lane.mu.Unlock()
	// 24 measured + 4 warm-up turns per arm; tool modes in both arms, the
	// unguided tool modes as tools-auto and the unguided JSON modes as plain.
	require.Equal(t, 7, shapes[tccModeRequired])
	require.Equal(t, 7, shapes[tccModeNamed])
	require.Equal(t, 7, shapes[tccModeJSONSchema])
	require.Equal(t, 7, shapes[tccModeStructuredOutputs])
	require.Equal(t, 14, shapes["tools-auto"])
	require.Equal(t, 14+2*len(tccParityPrompts), shapes["plain"])

	cells := matrixCells(t, report.MatrixRow)
	require.Len(t, cells, 13, report.MatrixRow)
	require.Contains(t, cells[0], "gfx1100-structured-outputs-")
	require.Equal(t, "32768", cells[1])
	require.Equal(t, "none", cells[8])
	require.Contains(t, cells[9], "flexinfer-bench tool_call_conformance --url")
	require.Contains(t, cells[12], "`promote`")
}

func TestTCCParseFailureTaxonomy(t *testing.T) {
	lane := newFakeLane()
	// Constrained call numbers include the 4 warm-up turns.
	lane.inject = func(n int, constrained bool, mode string) string {
		if !constrained {
			if mode == "tools-auto" {
				return "prose"
			}
			return ""
		}
		return map[int]string{
			5: tccInvalidJSON, 6: tccSchemaViolation, 7: tccWrongToolName, 8: tccTruncated, 9: "http500", 10: "prose",
		}[n]
	}
	srv := lane.serve(t)
	code, report, _ := runTCCForTest(t, "--url", srv.URL, "--model", "m", "--turns", "12", "--session-turns", "2", "--warmup-turns", "4")

	require.Equal(t, 1, code)
	require.False(t, report.Pass)
	c := report.Constrained
	require.Equal(t, 6, c.OK)
	require.Equal(t, map[string]int{
		tccInvalidJSON: 2, tccSchemaViolation: 1, tccWrongToolName: 1, tccTruncated: 1, tccRequestError: 1,
	}, c.Taxonomy)
	require.Len(t, c.Failures, 6)
	require.Equal(t, "fail", checkStatus(report, "parse_success").Status)
	require.Contains(t, checkStatus(report, "parse_success").Detail, "invalid_json 2")
	// The unguided tool turns (sessions required, named, ..., required, named:
	// 8 of 12 turns) came back as prose: the baseline's own taxonomy.
	require.Equal(t, 8, report.Unguided.Taxonomy[tccInvalidJSON])
	cells := matrixCells(t, report.MatrixRow)
	require.Contains(t, cells[8], "parse_success")
	require.Equal(t, "`fail`", cells[12])
}

func TestTCCDecodeAndAcceptanceFailures(t *testing.T) {
	lane := newFakeLane()
	lane.constrainedTPS, lane.constrainedPos0 = 50, 0.6
	srv := lane.serve(t)
	code, report, _ := runTCCForTest(t, "--url", srv.URL, "--model", "m", "--turns", "8", "--warmup-turns", "0")

	require.Equal(t, 1, code)
	require.Equal(t, "pass", checkStatus(report, "parse_success").Status)
	require.Equal(t, "fail", checkStatus(report, "decode_vs_unguided").Status)
	require.Contains(t, checkStatus(report, "decode_vs_unguided").Value, "0.758")
	require.Equal(t, "fail", checkStatus(report, "pos0_acceptance").Status)
	require.Equal(t, "0.6000", checkStatus(report, "pos0_acceptance").Value)
	require.Equal(t, "unguided 0.8100", checkStatus(report, "pos0_acceptance").Detail)
}

func TestTCCParityDriftFails(t *testing.T) {
	lane := newFakeLane()
	lane.parityDriftAfter = len(tccParityPrompts)
	srv := lane.serve(t)
	code, report, _ := runTCCForTest(t, "--url", srv.URL, "--model", "m", "--turns", "4", "--warmup-turns", "0",
		"--parity-baseline", "parity_code=deadbeef")

	require.Equal(t, 1, code)
	parity := checkStatus(report, "greedy_parity")
	require.Equal(t, "fail", parity.Status)
	require.Contains(t, parity.Detail, "parity_code")
	require.Contains(t, parity.Detail, "baseline deadbeef")
	require.Contains(t, parity.Detail, "parity_list")
}

func TestTCCUnmeasuredEngineFails(t *testing.T) {
	lane := newFakeLane()
	srv := lane.serve(t)

	// Through a proxy path there is no default metrics URL.
	code, report, _ := runTCCForTest(t, "--url", srv.URL+"/model/m", "--model", "m", "--turns", "2", "--warmup-turns", "0")
	require.Equal(t, 1, code)
	require.Equal(t, "fail", checkStatus(report, "decode_vs_unguided").Status)
	require.Contains(t, checkStatus(report, "decode_vs_unguided").Detail, "unmeasured")
	require.Contains(t, checkStatus(report, "pos0_acceptance").Detail, "unmeasured")

	// A lane without speculative decoding: pos-0 fails unless declared n/a.
	lane.noDrafts = true
	code, report, _ = runTCCForTest(t, "--url", srv.URL, "--model", "m", "--turns", "2", "--warmup-turns", "0")
	require.Equal(t, 1, code)
	require.Contains(t, checkStatus(report, "pos0_acceptance").Detail, "no speculative drafts")
	code, report, _ = runTCCForTest(t, "--url", srv.URL, "--model", "m", "--turns", "2", "--warmup-turns", "0", "--expect-spec-decode=false")
	require.Equal(t, 0, code)
	require.Equal(t, "n/a", checkStatus(report, "pos0_acceptance").Status)
}

func TestTCCFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "m"},
		{"--url", "localhost:8000", "--model", "m"},
		{"--url", "http://x", "--model", "m", "--modes", "bogus"},
		{"--url", "http://x", "--model", "m", "--parity-baseline", "parity_code"},
		{"--url", "http://x", "--model", "m", "--turns", "0"},
	} {
		code, _, _ := runTCCForTest(t, args...)
		require.Equal(t, 2, code, "%v", args)
	}
}

func TestClassifyTCCTurn(t *testing.T) {
	weather := tccScenario{Tool: "get_weather"}
	call := func(name, args string) agentloop.TurnResult {
		return agentloop.TurnResult{FinishReason: "tool_calls", Message: agentloop.Message{ToolCalls: []agentloop.ToolCall{
			{ID: "1", Type: "function", Function: agentloop.FunctionCall{Name: name, Arguments: args}},
		}}}
	}
	content := func(s string) agentloop.TurnResult {
		return agentloop.TurnResult{FinishReason: "stop", Message: agentloop.Message{Content: s}}
	}
	cases := []struct {
		name        string
		mode        string
		constrained bool
		res         agentloop.TurnResult
		err         error
		want        string
		detail      string
	}{
		{"ok", tccModeRequired, true, call("get_weather", `{"city":"Oslo","unit":"celsius"}`), nil, tccOK, ""},
		{"request error", tccModeRequired, true, agentloop.TurnResult{}, fmt.Errorf("chat: status 500"), tccRequestError, "status 500"},
		{"truncated wins over invalid JSON", tccModeRequired, true, agentloop.TurnResult{FinishReason: "length"}, nil, tccTruncated, "length"},
		{"unknown tool", tccModeRequired, true, call("get_wether", `{}`), nil, tccWrongToolName, "not offered"},
		{"named mismatch", tccModeNamed, true, call("lookup_order", `{"order_id":"ORD-123456","include_items":true}`), nil, tccWrongToolName, "forced"},
		{"unguided named picks another valid tool", tccModeNamed, false, call("lookup_order", `{"order_id":"ORD-123456","include_items":true}`), nil, tccOK, ""},
		{"arguments not JSON", tccModeRequired, true, call("get_weather", `{"city":`), nil, tccInvalidJSON, "not JSON"},
		{"missing required", tccModeRequired, true, call("get_weather", `{"city":"Oslo"}`), nil, tccSchemaViolation, `missing required "unit"`},
		{"enum", tccModeRequired, true, call("get_weather", `{"city":"Oslo","unit":"kelvin"}`), nil, tccSchemaViolation, "not one of"},
		{"extra property", tccModeRequired, true, call("get_weather", `{"city":"Oslo","unit":"celsius","x":1}`), nil, tccSchemaViolation, "unexpected property"},
		{"integer", tccModeRequired, true, call("search_documents", `{"query":"q","source":"wiki","top_k":2.5}`), nil, tccSchemaViolation, "want integer"},
		{"maximum", tccModeRequired, true, call("search_documents", `{"query":"q","source":"wiki","top_k":50}`), nil, tccSchemaViolation, "above maximum"},
		{"pattern", tccModeRequired, true, call("lookup_order", `{"order_id":"123","include_items":true}`), nil, tccSchemaViolation, "does not match"},
		{"array items", tccModeRequired, true, call("create_calendar_event", `{"title":"t","date":"2026-11-01","start_time":"09:00","duration_minutes":30,"attendees":[""]}`), nil, tccSchemaViolation, "attendees[0]"},
		{"prose instead of a call", tccModeRequired, true, content("Sure!"), nil, tccInvalidJSON, "not JSON"},
		{"empty reply", tccModeRequired, true, content(""), nil, tccInvalidJSON, "no content"},
		{"call in content", tccModeRequired, true, content(`{"name":"get_weather","arguments":{}}`), nil, tccSchemaViolation, "not tool_calls"},
		{"envelope ok", tccModeJSONSchema, true, content(`{"name":"get_weather","arguments":{"city":"Oslo","unit":"fahrenheit"}}`), nil, tccOK, ""},
		{"envelope fenced", tccModeJSONSchema, false, content("```json\n{}\n```"), nil, tccInvalidJSON, "not JSON"},
		{"envelope extra key", tccModeStructuredOutputs, true, content(`{"name":"get_weather","arguments":{"city":"Oslo","unit":"celsius"},"why":"x"}`), nil, tccSchemaViolation, "unexpected key"},
		{"envelope array", tccModeJSONSchema, true, content(`[]`), nil, tccSchemaViolation, "not an object"},
		{"envelope unknown tool", tccModeJSONSchema, true, content(`{"name":"nope","arguments":{}}`), nil, tccWrongToolName, "not offered"},
		{"envelope bad arguments", tccModeJSONSchema, true, content(`{"name":"set_thermostat","arguments":{"zone":"attic","temperature_c":20,"mode":"heat"}}`), nil, tccSchemaViolation, "arguments.zone"},
		{"envelope trailing text", tccModeJSONSchema, true, content(`{"name":"get_weather","arguments":{}} thanks`), nil, tccInvalidJSON, "not JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTCCTurn(tc.mode, tc.constrained, weather, tc.res, tc.err)
			require.Equal(t, tc.want, got.Class, "detail: %s", got.Detail)
			if tc.detail != "" {
				require.Contains(t, got.Detail, tc.detail)
			}
		})
	}
}

func TestTCCCatalogueIsSelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range tccTools {
		require.False(t, seen[tool.Name], "duplicate tool %s", tool.Name)
		seen[tool.Name] = true
		require.NoError(t, checkTCCSchemaKeywords(tool.schema(), tool.Name))
		args, err := decodeJSONNumber([]byte(fakeValidArgs[tool.Name]))
		require.NoError(t, err)
		require.NoError(t, validateTCCSchema(tool.schema(), args, tool.Name), "fixture args for %s", tool.Name)
	}
	require.True(t, json.Valid([]byte(tccEnvelopeSchema())))
	for _, d := range tccToolDefs() {
		require.True(t, json.Valid(d.Function.Parameters))
	}
	prompts := map[string]bool{}
	for _, sc := range tccScenarios(200) {
		require.True(t, seen[sc.Tool])
		require.NotEmpty(t, sc.Prompt)
		prompts[sc.Prompt] = true
	}
	require.Greater(t, len(prompts), 120, "200 turns should not collapse onto a few prompts")
	require.Equal(t, "parity_code", tccParityPrompts[0].Name)
}

func TestParseEngineMetricsAndDelta(t *testing.T) {
	before := parseEngineMetrics(`# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{engine="0"} 100
vllm:generation_tokens_total{engine="1"} 50
vllm:request_success_total{finished_reason="stop"} 4
vllm:request_success_total{finished_reason="length"} 1
vllm:inter_token_latency_seconds_sum 2
vllm:spec_decode_num_drafts_total 40
vllm:spec_decode_num_draft_tokens_total 80
vllm:spec_decode_num_accepted_tokens_total 60
vllm:spec_decode_num_accepted_tokens_per_pos_total{position="0"} 30
vllm:spec_decode_num_accepted_tokens_per_pos_total{position="1"} 30
vllm:num_preemptions_total 0
`)
	require.Equal(t, 150.0, before.values[vllmMetricGenerationTokens])
	require.Equal(t, 5.0, before.values[vllmMetricRequestSuccess])
	after := parseEngineMetrics(`vllm:generation_tokens_total 1155
vllm:request_success_total 15
vllm:inter_token_latency_seconds_sum 17.5
vllm:spec_decode_num_drafts_total 440
vllm:spec_decode_num_draft_tokens_total 880
vllm:spec_decode_num_accepted_tokens_total 660
vllm:spec_decode_num_accepted_tokens_per_pos_total{position="0"} 350
vllm:spec_decode_num_accepted_tokens_per_pos_total{position="1"} 310
vllm:num_preemptions_total 1
`)
	d := diffEngine(before, after)
	require.Equal(t, "vllm:inter_token_latency_seconds_sum", d.DecodeSource)
	require.InDelta(t, (1005.0-10.0)/15.5, *d.DecodeTokensPerSecond, 0.001)
	require.InDelta(t, 0.8, *d.Pos0Acceptance, 1e-9)
	require.InDelta(t, 0.75, *d.AcceptanceRate, 1e-9)
	require.InDelta(t, 2.5, *d.MeanAcceptanceLength, 1e-9)
	require.Equal(t, 1.0, d.Preemptions)
}

func TestRunSubcommandDispatch(t *testing.T) {
	var out, errOut bytes.Buffer
	_, handled := runSubcommand(context.Background(), []string{"-model", "x"}, &out, &errOut)
	require.False(t, handled, "flag invocations stay with the benchmark")
	_, handled = runSubcommand(context.Background(), nil, &out, &errOut)
	require.False(t, handled)

	code, handled := runSubcommand(context.Background(), []string{"help"}, &out, &errOut)
	require.True(t, handled)
	require.Zero(t, code)
	require.Contains(t, out.String(), "tool_call_conformance")

	code, handled = runSubcommand(context.Background(), []string{"tool_call_conformance", "-h"}, &out, &errOut)
	require.True(t, handled)
	require.Zero(t, code)
	require.Contains(t, errOut.String(), "-metrics-url")
}

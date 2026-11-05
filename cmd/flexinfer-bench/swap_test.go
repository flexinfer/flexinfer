package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rt "github.com/flexinfer/flexinfer/internal/runtime"
)

// fakeSwapClock advances only when the profiler sleeps, so every health poll
// is exactly one poll interval apart.
type fakeSwapClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeSwapClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeSwapClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
	return nil
}

// fakeSwapRuntime is the runtime API (load/unload/health/status/models) and
// an OpenAI server for the loaded text lane, on one httptest server. Each
// health poll of a loading model advances it one step through the lane's
// script; the last step is Ready.
type fakeSwapRuntime struct {
	mu        sync.Mutex
	scripts   map[string][]string // lane -> substage per poll ("" = Spawn)
	loaded    map[string]int      // lane -> polls seen
	busyOnce  map[string]bool     // next load answers 503 once (VRAM still held)
	loads     []string
	unloads   []string
	answer    func(prompt string) string
	vramUsed  int64
	failLoads map[string]bool
}

func newFakeSwapRuntime() *fakeSwapRuntime {
	return &fakeSwapRuntime{
		scripts: map[string][]string{
			"warm":  {"", "LoadingWeights", "LoadingWeights", "LoadingWeights", "Compiling", "Compiling", "ProfilingKV", "Compiling", "Ready"},
			"image": {"", "", "Ready"},
		},
		loaded:    map[string]int{},
		busyOnce:  map[string]bool{},
		failLoads: map[string]bool{},
		answer:    func(prompt string) string { return "answer to: " + prompt[:min(len(prompt), 24)] },
	}
}

func (f *fakeSwapRuntime) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/models/{name}/load", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		name := r.PathValue("name")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["backend"] == nil {
			writeJSONForTest(w, http.StatusBadRequest, map[string]string{"error": "backend is required"})
			return
		}
		if f.busyOnce[name] {
			f.busyOnce[name] = false
			writeJSONForTest(w, http.StatusServiceUnavailable, map[string]string{"error": "insufficient VRAM"})
			return
		}
		f.loaded[name] = 0
		f.loads = append(f.loads, name)
		writeJSONForTest(w, http.StatusAccepted, map[string]string{"status": "loading", "model": name})
	})
	mux.HandleFunc("DELETE /api/v1/models/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		name := r.PathValue("name")
		if _, ok := f.loaded[name]; !ok {
			writeJSONForTest(w, http.StatusNotFound, map[string]string{"error": "not loaded"})
			return
		}
		delete(f.loaded, name)
		f.unloads = append(f.unloads, name)
		writeJSONForTest(w, http.StatusOK, map[string]string{"status": "unloaded"})
	})
	mux.HandleFunc("GET /api/v1/models/{name}/health", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		name := r.PathValue("name")
		polls, ok := f.loaded[name]
		if !ok {
			writeJSONForTest(w, http.StatusNotFound, map[string]string{"error": "not loaded"})
			return
		}
		f.loaded[name] = polls + 1
		script := f.scripts[name]
		if f.failLoads[name] && polls >= 1 {
			writeJSONForTest(w, http.StatusServiceUnavailable, map[string]any{"state": "Failed", "error": "engine died"})
			return
		}
		if polls < len(script) {
			writeJSONForTest(w, http.StatusServiceUnavailable, map[string]any{"state": "Loading", "port": 8100, "loadingSubstage": script[polls]})
			return
		}
		writeJSONForTest(w, http.StatusOK, map[string]any{"state": "Ready", "port": 8100, "kvCacheTokens": 132561, "kvMaxConcurrency": "2.02"})
	})
	mux.HandleFunc("GET /api/v1/models", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		models := []map[string]string{{"name": "snac-decode-gfx1100"}}
		for name := range f.loaded {
			models = append(models, map[string]string{"name": name})
		}
		writeJSONForTest(w, http.StatusOK, map[string]any{"models": models})
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSONForTest(w, http.StatusOK, map[string]any{"gpuArch": "gfx1100", "gpu": map[string]int64{"vramUsedMB": f.vramUsed}})
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSONForTest(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "qwen38-27b-xtx-warm-canary"}}})
	})
	mux.HandleFunc("POST /tokenize", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		// ~1 token per 4 bytes, close enough for sizing.
		writeJSONForTest(w, http.StatusOK, map[string]int{"count": len(req.Messages[0].Content) / 4})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages  []struct{ Content string } `json:"messages"`
			CacheSalt string                     `json:"cache_salt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.CacheSalt == "" {
			http.Error(w, "test expects a cache_salt", http.StatusBadRequest)
			return
		}
		prompt := req.Messages[0].Content
		if strings.HasPrefix(prompt, "Session ") {
			prompt = prompt[strings.Index(prompt, "\n")+1:] // the nonce line
		}
		w.Header().Set("Content-Type", "text/event-stream")
		words := strings.Fields(f.answer(prompt))
		for i, word := range words {
			text := word
			if i > 0 {
				text = " " + word
			}
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", text)
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d}}\n\n", len(prompt)/4, len(words))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})
	return mux
}

func writeJSONForTest(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSwapPayload(t *testing.T, dir, name, backendName string) string {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	payload := fmt.Sprintf(`{"backend":%q,"model":"/models/flexinfer-system/%s","config":{"maxModelLen":65536},"specHash":"abc123"}`, backendName, name)
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o644))
	return path
}

func newTestSwapProfiler(t *testing.T, f *fakeSwapRuntime, args ...string) (*swapProfiler, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	all := append([]string{"--runtime-url", srv.URL, "--inference-url", srv.URL, "--poll-interval", "1s", "--decode-contexts", "512"}, args...)
	opts, err := parseSwapFlags(all, &bytes.Buffer{})
	require.NoError(t, err)
	n := 0
	salt := func() string { n++; return fmt.Sprintf("salt%d", n) }
	return &swapProfiler{opts: opts, http: srv.Client(), clock: &fakeSwapClock{t: time.Unix(0, 0)}, salt: salt}, srv
}

// TestSwapSubstagesMatchTheRuntime keeps the profiler's substage names on the
// runtime's (internal/runtime/load_progress.go).
func TestSwapSubstagesMatchTheRuntime(t *testing.T) {
	assert.Equal(t, rt.LoadSubstageLoadingWeights, swapSubstageLoadingWeights)
	assert.Equal(t, rt.LoadSubstageCompiling, swapSubstageCompiling)
	assert.Equal(t, rt.LoadSubstageProfilingKV, swapSubstageProfilingKV)
	assert.Equal(t, rt.LoadSubstageReady, swapSubstageReady)
}

func TestSwapSubstageDurationsSumRepeatedVisits(t *testing.T) {
	marks := []swapMark{
		{AtS: 0, State: "Loading", Substage: swapSubstageSpawn},
		{AtS: 4, State: "Loading", Substage: swapSubstageLoadingWeights},
		{AtS: 40, State: "Loading", Substage: swapSubstageCompiling},
		{AtS: 50, State: "Loading", Substage: swapSubstageProfilingKV},
		{AtS: 60, State: "Loading", Substage: swapSubstageCompiling},
		{AtS: 75, State: "Loading", Substage: swapSubstageReady},
		{AtS: 77, State: "Ready"},
	}
	got := swapSubstageDurations(marks, 77)
	assert.Equal(t, map[string]float64{"Spawn": 4, "LoadingWeights": 36, "Compiling": 25, "ProfilingKV": 10, "Ready": 2}, got)
}

func TestSwapProfilerTwoLanesAgainstFakeRuntime(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSwapRuntime()
	f.busyOnce["image"] = true // first image swap-in waits for the warm lane's VRAM
	p, _ := newTestSwapProfiler(t, f,
		"--lane", "warm="+writeSwapPayload(t, dir, "warm", "vllm"),
		"--lane", "image="+writeSwapPayload(t, dir, "image", "diffusers"),
		"--cycles", "2")

	report := p.run(context.Background())
	require.Empty(t, report.Error)

	// warmup warm, then image, warm per cycle: 2 swaps each direction.
	assert.Equal(t, []string{"warm", "image", "warm", "image", "warm"}, f.loads)
	require.Len(t, report.Loads, 5)
	assert.True(t, report.Loads[0].Warmup)
	assert.Equal(t, "warm", report.Loads[1].From)
	assert.Equal(t, []string{"snac-decode-gfx1100"}, report.OtherModels)

	// One poll per second: the script's polls are the substage durations.
	warm := report.Loads[2]
	assert.Equal(t, "warm", warm.Lane)
	assert.Equal(t, 9.0, warm.LoadS)
	assert.Equal(t, map[string]float64{"Spawn": 1, "LoadingWeights": 3, "Compiling": 3, "ProfilingKV": 1, "Ready": 1}, warm.Substages)
	assert.Equal(t, []string{"Spawn", "LoadingWeights", "Compiling", "ProfilingKV", "Compiling", "Ready", ""}, timelineSubstages(warm.Timeline))
	assert.Equal(t, int64(132561), warm.KVCacheTokens)
	assert.Equal(t, "qwen38-27b-xtx-warm-canary", warm.ServedModel)
	require.Len(t, warm.Probes, 4) // three parity prompts + decode@512
	assert.Equal(t, "decode@512", warm.Probes[3].Name)

	image := report.Loads[1]
	assert.Equal(t, 1.0, image.AdmitWaitS)
	assert.Equal(t, 1, image.AdmitRetries)
	assert.Equal(t, map[string]float64{"Spawn": 2, "Ready": 1}, image.Substages)
	assert.Empty(t, image.Probes, "non-text lanes are timed, not probed")

	s := report.Summary["warm"]
	assert.Equal(t, 2, s.Loads)
	assert.Equal(t, "LoadingWeights", s.Dominant)
	for _, pp := range swapParityPrompts {
		assert.Len(t, s.Hashes[pp.Name], 1, "%s: greedy hash must be stable across swaps", pp.Name)
	}
	// No baseline: the hash and decode bars stay open, so at best conditional.
	assert.Equal(t, "conditional", report.Verdict)

	row := swapMatrixRow(report)
	assert.Equal(t, 14, strings.Count(row, " | ")+2, "13 cells: %s", row)
	assert.Contains(t, row, "dominant LoadingWeights")
	assert.Contains(t, row, "`conditional`")
}

func TestSwapProfilerSingleLaneReloads(t *testing.T) {
	f := newFakeSwapRuntime()
	p, _ := newTestSwapProfiler(t, f, "--lane", "image="+writeSwapPayload(t, t.TempDir(), "image", "diffusers"), "--cycles", "3")
	report := p.run(context.Background())
	require.Empty(t, report.Error)
	assert.Equal(t, []string{"image", "image", "image", "image"}, f.loads)
	assert.Equal(t, []string{"image", "image", "image"}, f.unloads)
	assert.Equal(t, 3, report.Summary["image"].Loads)
	assert.Equal(t, "pass", barStatus(report, "image", "load_p50"))
	// The host-log and warm-lane bars are outside the profiler.
	assert.Equal(t, "conditional", report.Verdict)
}

func TestSwapProfilerStopsOnAFailedLoad(t *testing.T) {
	f := newFakeSwapRuntime()
	f.failLoads["warm"] = true
	p, _ := newTestSwapProfiler(t, f, "--lane", "warm="+writeSwapPayload(t, t.TempDir(), "warm", "vllm"))
	report := p.run(context.Background())
	assert.Contains(t, report.Error, "engine died")
	assert.Equal(t, "fail", report.Verdict)
	assert.Len(t, report.Loads, 1)
}

func TestSwapBaselineComparison(t *testing.T) {
	dir := t.TempDir()
	f := newFakeSwapRuntime()
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	baseOpts, err := parseSwapFlags([]string{"--probe-url", srv.URL, "--cycles", "2", "--decode-contexts", "512"}, &bytes.Buffer{})
	require.NoError(t, err)
	n := 0
	base := &swapProfiler{opts: baseOpts, http: srv.Client(), clock: &fakeSwapClock{t: time.Unix(0, 0)}, salt: func() string { n++; return fmt.Sprint(n) }}
	baseline := base.run(context.Background())
	require.Empty(t, baseline.Error)
	assert.Equal(t, "baseline", baseline.Kind)
	require.Len(t, baseline.Probes, 2)
	baselinePath := filepath.Join(dir, "baseline.json")
	data, err := json.Marshal(baseline)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(baselinePath, data, 0o644))

	// Same engine output: the hash bar passes.
	p, _ := newTestSwapProfiler(t, f, "--lane", "warm="+writeSwapPayload(t, dir, "warm", "vllm"), "--cycles", "1", "--baseline", baselinePath)
	report := p.run(context.Background())
	require.Empty(t, report.Error)
	assert.Equal(t, "pass", barStatus(report, "warm", "greedy_hash"))

	// A different greedy answer in-pod fails it.
	f.answer = func(prompt string) string { return "other answer to: " + prompt[:min(len(prompt), 24)] }
	p, _ = newTestSwapProfiler(t, f, "--lane", "warm="+writeSwapPayload(t, dir, "warm", "vllm"), "--cycles", "1", "--baseline", baselinePath)
	report = p.run(context.Background())
	assert.Equal(t, "fail", barStatus(report, "warm", "greedy_hash"))
	assert.Equal(t, "fail", report.Verdict)
}

func TestSwapDecodeBarUsesTheTolerance(t *testing.T) {
	p := &swapProfiler{opts: swapOptions{Bars: swapBars{TextLoadP50: time.Minute, TextLoadP95: time.Minute, DecodeTolerance: 0.03}}}
	lane := swapLane{Name: "warm", Text: true}
	hashes := map[string][]string{"parity_code": {"a"}, "parity_explain": {"b"}, "parity_list": {"c"}}
	baseline := &swapReport{Kind: "baseline", Summary: map[string]swapLaneSummary{
		"baseline": {Hashes: hashes, DecodeP50TPS: map[string]float64{"decode@2048": 40.0}},
	}}
	within := swapLaneSummary{Loads: 5, Hashes: hashes, DecodeP50TPS: map[string]float64{"decode@2048": 38.9}}
	outside := swapLaneSummary{Loads: 5, Hashes: hashes, DecodeP50TPS: map[string]float64{"decode@2048": 38.7}}
	assert.Equal(t, "pass", findBar(p.laneBars(lane, within, baseline), "decode").Status)
	assert.Equal(t, "fail", findBar(p.laneBars(lane, outside, baseline), "decode").Status)
}

func TestReadSwapChatStreamMeasuresDecode(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":" world"}}]}`,
		`data: {"choices":[{"delta":{"content":"  "},"finish_reason":"stop"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":11}}`,
		`data: [DONE]`,
	}, "\n\n")
	start := time.Unix(100, 0)
	ticks := []time.Time{start.Add(500 * time.Millisecond), start.Add(1500 * time.Millisecond), start.Add(2500 * time.Millisecond)}
	i := 0
	now := func() time.Time { t := ticks[i]; i++; return t }
	pr, first := readSwapChatStream(strings.NewReader(stream), "p", start, now)
	require.Empty(t, pr.Error)
	assert.Equal(t, 0.5, pr.TTFTS)
	assert.Equal(t, ticks[0], first)
	require.NotNil(t, pr.DecodeTPS)
	assert.Equal(t, 5.0, *pr.DecodeTPS) // (11-1) tokens over 2 s
	// sha256("Hello world")[:16], as verify-vllm-mtp.py hashes the stripped answer.
	assert.Equal(t, "64ec88ca00b268e5", pr.SHA)
	assert.Equal(t, "stop", pr.Finish)
}

func TestSwapInferenceBase(t *testing.T) {
	got, err := swapInferenceBase("", "http://10.42.0.137:8080/", 8101)
	require.NoError(t, err)
	assert.Equal(t, "http://10.42.0.137:8101", got)
	got, err = swapInferenceBase("http://flexinfer-runtime-gfx1100:{port}/", "http://x:8080", 8101)
	require.NoError(t, err)
	assert.Equal(t, "http://flexinfer-runtime-gfx1100:8101", got)
	_, err = swapInferenceBase("", "http://10.42.0.137:8080", 0)
	assert.Error(t, err)
}

// A Model-manifest lane renders the payload the controller would send.
func TestSwapLaneFromModelManifest(t *testing.T) {
	gpuProfile := filepath.Join("..", "..", "deploy", "gpuprofiles", "gfx1100.yaml")
	p := &swapProfiler{opts: swapOptions{
		GPUProfile: gpuProfile,
		Lanes:      []swapLaneFlag{{Name: "warm", Source: filepath.Join("..", "..", "deploy", "models", "qwen38-27b-xtx-warm-canary.yaml")}},
	}}
	lanes, err := p.resolveLanes()
	require.NoError(t, err)
	require.Len(t, lanes, 1)
	lane := lanes[0]
	assert.Equal(t, "vllm", lane.Backend)
	assert.True(t, lane.Text)
	assert.Equal(t, int64(65536), lane.Context)
	assert.NotEmpty(t, lane.SpecHash)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(lane.payload, &payload))
	assert.True(t, strings.HasPrefix(fmt.Sprint(payload["modelPath"]), "/models/flexinfer-system/qwen38-27b-xtx-warm-canary"), payload["modelPath"])
}

// The report says why the controller would keep a lane off the runtime path.
func TestSwapLaneFromManifestWarnsWhenTheControllerWouldNotLoadItInPod(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "lane.yaml")
	require.NoError(t, os.WriteFile(manifest, []byte(`apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: dedicated-lane
spec:
  backend: diffusers
  source: HF://org/model
  config:
    dedicatedDeployment: true
`), 0o644))
	p := &swapProfiler{opts: swapOptions{
		GPUProfile: filepath.Join("..", "..", "deploy", "gpuprofiles", "gfx1100.yaml"),
		Lanes:      []swapLaneFlag{{Name: "lane", Source: manifest}},
	}}
	lanes, err := p.resolveLanes()
	require.NoError(t, err)
	require.Len(t, lanes[0].Warnings, 1)
	assert.Contains(t, lanes[0].Warnings[0], "dedicatedDeployment")
}

func TestParseSwapFlagsRequiresATarget(t *testing.T) {
	_, err := parseSwapFlags([]string{"--lane", "a=b.json"}, &bytes.Buffer{})
	assert.Error(t, err)
	_, err = parseSwapFlags([]string{"--runtime-url", "http://x:8080", "--lane", "nosource"}, &bytes.Buffer{})
	assert.Error(t, err)
	_, err = parseSwapFlags([]string{"--probe-url", "http://x:8000", "--lane", "a=b.json"}, &bytes.Buffer{})
	assert.Error(t, err)
	opts, err := parseSwapFlags([]string{"--probe-url", "http://x:8000", "--decode-contexts", ""}, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Empty(t, opts.DecodeContexts)
}

func TestDispatchSubcommand(t *testing.T) {
	_, ok := dispatchSubcommand([]string{"--model", "x"})
	assert.False(t, ok, "flag-only invocations keep the classic benchmark")
	code, ok := dispatchSubcommand([]string{"swap", "--runtime-url", "http://x:8080"})
	assert.True(t, ok)
	assert.Equal(t, 2, code)
}

func timelineSubstages(marks []swapMark) []string {
	out := make([]string, 0, len(marks))
	for _, m := range marks {
		out = append(out, m.Substage)
	}
	return out
}

func barStatus(r *swapReport, lane, bar string) string {
	for _, b := range r.Bars {
		if b.Lane == lane && b.Bar == bar {
			return b.Status
		}
	}
	return ""
}

func findBar(bars []swapBarResult, name string) swapBarResult {
	for _, b := range bars {
		if b.Bar == name {
			return b
		}
	}
	return swapBarResult{}
}

func TestRunSwapWritesReportAndRow(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(newFakeSwapRuntime().handler())
	defer srv.Close()
	out, rowOut := filepath.Join(dir, "swap.json"), filepath.Join(dir, "row.md")
	code := runSwap([]string{
		"--runtime-url", srv.URL, "--inference-url", srv.URL, "--poll-interval", "1ms", "--cycles", "1",
		"--decode-contexts", "", "--lane", "warm=" + writeSwapPayload(t, dir, "warm", "vllm"),
		"--row-id", "gfx1100-xtx-inpod-swap-test", "--out", out, "--row-out", rowOut,
	})
	assert.Equal(t, 0, code)
	report, err := os.ReadFile(out)
	require.NoError(t, err)
	var r swapReport
	require.NoError(t, json.Unmarshal(report, &r))
	assert.Equal(t, "conditional", r.Verdict)
	assert.Len(t, r.Loads, 2)
	row, err := os.ReadFile(rowOut)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(row), "| `gfx1100-xtx-inpod-swap-test` — KT-SWAP"), string(row))
	assert.Contains(t, string(row), "flexinfer-bench swap --runtime-url")
}

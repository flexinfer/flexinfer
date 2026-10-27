package runtime

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A vLLM 0.32 boot as the runtime streams it: tqdm redraws the shard bar with
// '\r', and the profile lines precede graph capture and the API server.
const vllmBootOutput = "INFO 10-27 14:02:11 [gpu_model_runner.py:1801] Starting to load model /models/flexinfer-system/workhorse...\n" +
	"Loading safetensors checkpoint shards:   0% Completed | 0/4 [00:00<?, ?it/s]\r" +
	"Loading safetensors checkpoint shards:  25% Completed | 1/4 [00:21<01:03, 21.10s/it]\r" +
	"Loading safetensors checkpoint shards:  50% Completed | 2/4 [00:40<00:40, 20.02s/it]\n" +
	"INFO 10-27 14:03:52 [backends.py:530] Dynamo bytecode transform time: 9.81 s\n" +
	"INFO 10-27 14:04:30 [monitor.py:34] torch.compile takes 37.4 s in total\n" +
	"INFO 10-27 14:04:41 [gpu_worker.py:298] Memory profiling takes 10.2 seconds\n" +
	"INFO 10-27 14:04:41 [kv_cache_utils.py:1087] GPU KV cache size: 140,753 tokens\n" +
	"INFO 10-27 14:04:41 [kv_cache_utils.py:1091] Maximum concurrency for 65,536 tokens per request: 2.15x\n" +
	"Capturing CUDA graphs (mixed prefill-decode, PIECEWISE): 100%|██████████| 67/67\n" +
	"INFO 10-27 14:05:20 [api_server.py:1881] Starting vLLM API server 0 on http://0.0.0.0:8000\n" +
	"INFO:     Application startup complete.\n"

func TestLoadProgressFollowsVLLMBoot(t *testing.T) {
	progress := &loadProgress{}
	var seen []string
	observe := func(line string) {
		progress.observe(line)
		substage, message, _, _ := progress.snapshot()
		if len(seen) == 0 || seen[len(seen)-1] != substage+": "+message {
			seen = append(seen, substage+": "+message)
		}
	}
	streamLogs(io.NopCloser(strings.NewReader(vllmBootOutput)), logr.Discard(), "stdout", observe)

	assert.Equal(t, []string{
		"LoadingWeights: loading weights",
		"LoadingWeights: loading weights (1/4 shards, 21.10s/it)",
		"LoadingWeights: loading weights (2/4 shards, 20.02s/it)",
		"Compiling: compiling kernels / capturing graphs",
		"ProfilingKV: profiling KV cache",
		"ProfilingKV: profiled KV cache (140753 tokens)",
		"ProfilingKV: profiled KV cache (140753 tokens, 2.15x concurrency)",
		"Compiling: compiling kernels / capturing graphs",
		"Ready: backend HTTP server up, awaiting readiness probe",
	}, seen, "each carriage-return update of the shard bar must surface on its own")

	substage, _, tokens, concurrency := progress.snapshot()
	assert.Equal(t, LoadSubstageReady, substage)
	assert.Equal(t, int64(140753), tokens)
	assert.Equal(t, "2.15", concurrency)

	// Nothing moves the substage back from Ready, and a stopped tracker ignores
	// request logs while keeping the profile.
	progress.observe("Loading safetensors checkpoint shards:  50% Completed | 2/4 [00:40<00:40, 20.02s/it]")
	substage, _, _, _ = progress.snapshot()
	assert.Equal(t, LoadSubstageReady, substage)
	fresh := &loadProgress{}
	fresh.stop()
	fresh.observe("GPU KV cache size: 10 tokens")
	_, _, tokens, _ = fresh.snapshot()
	assert.Zero(t, tokens)
}

func TestScanLinesOrCRKeepsTerminators(t *testing.T) {
	var tokens []string
	data := []byte("a\rb\r\nc\nd")
	for len(data) > 0 {
		advance, token, err := scanLinesOrCR(data, true)
		require.NoError(t, err)
		tokens = append(tokens, string(token))
		data = data[advance:]
	}
	assert.Equal(t, []string{"a\r", "b\r", "\n", "c\n", "d"}, tokens)
}

// captureSink records logged messages.
type captureSink struct{ msgs *[]string }

func (c captureSink) Init(logr.RuntimeInfo)               {}
func (c captureSink) Enabled(int) bool                    { return true }
func (c captureSink) Info(_ int, msg string, _ ...any)    { *c.msgs = append(*c.msgs, msg) }
func (c captureSink) Error(_ error, msg string, _ ...any) { *c.msgs = append(*c.msgs, msg) }
func (c captureSink) WithValues(...any) logr.LogSink      { return c }
func (c captureSink) WithName(string) logr.LogSink        { return c }

// Splitting at '\r' for the tracker must not change what is logged: one entry
// per '\n'-terminated line, redraws included, as before.
func TestStreamLogsStillLogsWholeLines(t *testing.T) {
	var logged, observed []string
	logger := logr.New(captureSink{msgs: &logged})
	streamLogs(io.NopCloser(strings.NewReader("shards 25%\rshards 50%\rshards 100%\nnext line\r\nlast")), logger, "stderr",
		func(s string) { observed = append(observed, s) })
	assert.Equal(t, []string{"shards 25%\rshards 50%\rshards 100%", "next line", "last"}, logged)
	assert.Equal(t, []string{"shards 25%", "shards 50%", "shards 100%", "next line", "last"}, observed)
}

func TestModelHealthReportsLoadProgressAndKVProfile(t *testing.T) {
	m := NewManager(ManagerConfig{GPUVendor: backend.GPUVendorCPU})
	profiled := &loadProgress{}
	profiled.observe("INFO [kv_cache_utils.py:1087] GPU KV cache size: 140,753 tokens")
	profiled.observe("INFO [kv_cache_utils.py:1091] Maximum concurrency for 65,536 tokens per request: 2.15x")
	m.models["lane"] = &LoadedModel{Name: "lane", Backend: backend.NameVLLM, State: ModelStateLoading, progress: profiled}
	m.models["llama"] = &LoadedModel{Name: "llama", Backend: backend.NameLlamaCpp, State: ModelStateLoading, progress: &loadProgress{}}
	api := NewServer(m)
	health := func(name string) map[string]any {
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/models/"+name+"/health", nil))
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body
	}

	body := health("lane")
	assert.Equal(t, LoadSubstageProfilingKV, body["loadingSubstage"])
	assert.Equal(t, "profiled KV cache (140753 tokens, 2.15x concurrency)", body["loadingMessage"])
	assert.Equal(t, 140753.0, body["kvCacheTokens"])
	assert.Equal(t, "2.15", body["kvMaxConcurrency"])

	body = health("llama")
	assert.NotContains(t, body, "kvCacheTokens", "an unknown pool must not read as an empty one")

	m.mu.Lock()
	m.models["lane"].State = ModelStateReady
	m.mu.Unlock()
	body = health("lane")
	assert.Equal(t, "", body["loadingSubstage"], "substage is reported only while Loading")
	assert.Equal(t, 140753.0, body["kvCacheTokens"], "the profile stays readable for the KV-pool recheck")
}

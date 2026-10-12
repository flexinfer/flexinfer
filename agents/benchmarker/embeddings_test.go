package benchmarker

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRun_Embeddings_BatchesAndPersistsInputThroughput(t *testing.T) {
	t.Parallel()
	for _, backend := range []string{"llamacpp", "llama.cpp", "llama-cpp", "vllm"} {
		t.Run(backend, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			clock := &fakeClock{t: time.Unix(0, 0)}
			var batches [][]string
			b := NewBenchmarkerWithClient(client, "default", "http://proxy", "gemma", backend, Options{
				Task: TaskEmbeddings, EmbeddingInputs: []string{"query: a", "passage: b", "passage: c"},
				BatchSize: 2, WarmupIterations: 1, Iterations: 2, MinDuration: time.Millisecond,
			})
			b.now = clock.Now
			b.store = NewConfigMapStore(client)
			b.httpClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/model/gemma/health":
					return httpResponse(http.StatusOK, "ok"), nil
				case "/model/gemma/v1/embeddings":
					assert.Equal(t, http.MethodPost, req.Method)
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					var body struct {
						Model    string   `json:"model"`
						Input    []string `json:"input"`
						Encoding string   `json:"encoding_format"`
					}
					require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
					assert.Equal(t, "embeddinggemma-2", body.Model)
					assert.Equal(t, "float", body.Encoding)
					batches = append(batches, body.Input)
					return httpResponse(http.StatusOK, `{"data":[{"index":0,"embedding":[0.6,0.8]},{"index":1,"embedding":[1,0]}],"usage":{"prompt_tokens":7}}`), nil
				default:
					t.Fatalf("unexpected request to %s", req.URL.Path)
					return nil, nil
				}
			})
			require.NoError(t, b.Run(context.Background(), "embeddinggemma-2", "gemma-bench"))
			assert.Equal(t, [][]string{{"query: a", "passage: b"}, {"passage: c", "query: a"}, {"passage: b", "passage: c"}}, batches)
			cm, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), "gemma-bench", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, "embeddings", cm.Data["task"])
			assert.Equal(t, "0", cm.Data["completionTokens"])
			assert.Equal(t, "14", cm.Data["promptTokens"])
			assert.Equal(t, "70", cm.Data["tokensPerSecond"])
			assert.Equal(t, "4", cm.Data["documents"])
			assert.Equal(t, "20", cm.Data["documentsPerSecond"])
			assert.Equal(t, "2", cm.Data["batchSize"])
			assert.Equal(t, "2", cm.Data["samples"])
		})
	}
}

func TestEmbeddings_RejectsInvalidResponse(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, body, want string }{
		{"missing usage", `{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[0,1]}]}`, "usage.prompt_tokens"},
		{"missing prompt tokens", `{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[0,1]}],"usage":{}}`, "usage.prompt_tokens"},
		{"zero prompt tokens", `{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[0,1]}],"usage":{"prompt_tokens":0}}`, "usage.prompt_tokens"},
		{"missing vectors", `{"data":[],"usage":{"prompt_tokens":7}}`, "response count"},
		{"count mismatch", `{"data":[{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":7}}`, "response count"},
		{"missing index", `{"data":[{"embedding":[1,0]},{"index":1,"embedding":[0,1]}],"usage":{"prompt_tokens":7}}`, "response index"},
		{"out of order", `{"data":[{"index":1,"embedding":[1,0]},{"index":0,"embedding":[0,1]}],"usage":{"prompt_tokens":7}}`, "response index"},
		{"duplicate index", `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}],"usage":{"prompt_tokens":7}}`, "response index"},
		{"empty vector", `{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[]}],"usage":{"prompt_tokens":7}}`, "invalid dimensions"},
		{"dimension mismatch", `{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1]}],"usage":{"prompt_tokens":7}}`, "invalid dimensions"},
		{"zero vector", `{"data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[0,1]}],"usage":{"prompt_tokens":7}}`, "zero vector"},
		{"null value", `{"data":[{"index":0,"embedding":[1,null]},{"index":1,"embedding":[0,1]}],"usage":{"prompt_tokens":7}}`, "null value"},
		{"nonfinite numeric overflow", `{"data":[{"index":0,"embedding":[1e400,0]},{"index":1,"embedding":[0,1]}],"usage":{"prompt_tokens":7}}`, "decode embeddings response"},
		{"nonfinite literal", `{"data":[{"index":0,"embedding":[NaN,0]}]}`, "decode embeddings response"},
		{"base64 instead of float", `{"data":[{"index":0,"embedding":"abcd"}]}`, "decode embeddings response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := &fakeClock{t: time.Unix(0, 0)}
			b := &Benchmarker{proxyURL: "http://proxy", modelName: "gemma", now: clock.Now,
				httpClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return httpResponse(http.StatusOK, tt.body), nil })}}
			_, err := b.embedOnce(context.Background(), "gemma", []string{"a", "b"})
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestEmbeddings_RejectsChangingDimensions(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Unix(0, 0)}
	calls := 0
	b := &Benchmarker{proxyURL: "http://proxy", modelName: "gemma", backendType: "llamacpp", now: clock.Now,
		opts: Options{Task: TaskEmbeddings, BatchSize: 1, Iterations: 2, MinDuration: time.Millisecond},
		httpClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return httpResponse(http.StatusOK, `{"data":[{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":2}}`), nil
			}
			return httpResponse(http.StatusOK, `{"data":[{"index":0,"embedding":[1,0,0]}],"usage":{"prompt_tokens":2}}`), nil
		})}}
	_, err := b.runBenchmark(context.Background(), "gemma")
	require.ErrorContains(t, err, "dimensions changed between requests")
}

func TestEmbeddings_OptionsAndTaskValidation(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 128, (Options{}).withDefaults().BatchSize)
	assert.Equal(t, TaskGeneration, (Options{}).withDefaults().Task)
	assert.Equal(t, 1, (Options{Task: TaskEmbeddings}).withDefaults().BatchSize)
	for _, value := range []string{"", "generation", "embeddings"} {
		_, err := ParseTask(value)
		require.NoError(t, err)
	}
	_, err := ParseTask("embedding")
	require.ErrorContains(t, err, "unsupported benchmark task")
	for _, opts := range []Options{
		{Task: Task("invalid")},
		{Task: TaskGeneration, EmbeddingInputs: []string{"a"}},
		{Task: TaskEmbeddings, EmbeddingInputs: []string{" "}},
		{Task: TaskEmbeddings, Prompt: " "},
	} {
		require.Error(t, opts.validate("vllm"))
	}
	b := &Benchmarker{backendType: "ollama", opts: Options{Task: TaskEmbeddings}}
	_, err = b.RunAndReturn(context.Background(), "gemma")
	require.ErrorContains(t, err, "not supported for backend")
	_, err = NewBenchmarker("vllm", Options{Task: Task("invalid")}, "")
	require.ErrorContains(t, err, "unsupported benchmark task")
}

func TestEmbeddings_PropagatesHTTPFailure(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := &Benchmarker{proxyURL: "http://proxy", modelName: "gemma", now: clock.Now,
		httpClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return httpResponse(http.StatusBadRequest, "model is not an embedding model"), nil
		})}}
	_, err := b.embedOnce(context.Background(), "gemma", []string{"a"})
	require.ErrorContains(t, err, "status 400")
	require.ErrorContains(t, err, "model is not an embedding model")
}

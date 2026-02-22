package benchmarker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time {
	c.t = c.t.Add(100 * time.Millisecond)
	return c.t
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func httpResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func stripProxyModelPrefix(path, modelName string) string {
	prefix := "/model/" + modelName
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return "/"
	}
	return rest
}

func TestRun_Ollama_UpsertsConfigMapAndComputesTPS(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()

	clock := &fakeClock{t: time.Unix(0, 0)}
	var generateCalls int
	model := "test-model"
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch stripProxyModelPrefix(r.URL.Path, model) {
			case "/api/tags":
				return httpResponse(http.StatusOK, `{"models":[]}`), nil
			case "/api/pull":
				return httpResponse(http.StatusOK, `{"status":"success"}`), nil
			case "/api/generate":
				generateCalls++
				// Simulate Ollama streaming: two token chunks + final done chunk with eval stats.
				stream := `{"response":"hello","done":false}` + "\n" +
					`{"response":"world","done":false}` + "\n" +
					`{"response":"","done":true,"eval_count":10,"eval_duration":1000000000}` + "\n"
				resp := httpResponse(http.StatusOK, stream)
				resp.Header.Set("Content-Type", "application/json")
				return resp, nil
			default:
				return httpResponse(http.StatusNotFound, "not found"), nil
			}
		}),
	}

	configMapName := "test-cm"

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"flexinfer.ai/gpu.vendor": "NVIDIA",
				"flexinfer.ai/gpu.arch":   "sm_89",
				"flexinfer.ai/gpu.vram":   "24Gi",
				"flexinfer.ai/gpu.count":  "1",
				"flexinfer.ai/gpu.int4":   "true",
			},
		},
	}
	_, err := clientset.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
	require.NoError(t, err)

	// Ensure upsert path is exercised.
	_, err = clientset.CoreV1().ConfigMaps("default").Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: "default"},
		Data:       map[string]string{"tokensPerSecond": "0"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	b := &Benchmarker{
		kubeClient:  clientset,
		namespace:   "default",
		proxyURL:    "http://backend",
		modelName:   model,
		backendType: "ollama",
		opts: Options{
			WarmupIterations: 1,
			MinDuration:      1 * time.Millisecond,
			Iterations:       5,
			BatchSize:        16,
		}.withDefaults(),
		httpClient: httpClient,
		now:        clock.Now,
		nodeName:   "node1",
		resultsCM:  defaultBenchmarkResultsConfigMap,
		store:      NewConfigMapStore(clientset),
	}

	err = b.Run(context.Background(), model, configMapName)
	require.NoError(t, err)

	cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), configMapName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, model, cm.Data["model"])
	assert.Equal(t, "ollama", cm.Data["backend"])
	assert.Equal(t, "10", cm.Data["tokensPerSecond"])
	assert.Equal(t, "50", cm.Data["completionTokens"])
	assert.Equal(t, "5", cm.Data["durationSeconds"])
	assert.Equal(t, "5", cm.Data["samples"])
	assert.NotEmpty(t, cm.Data["timestamp"])
	assert.GreaterOrEqual(t, generateCalls, 6, "expected warmup + at least 5 measurement calls")

	global, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), defaultBenchmarkResultsConfigMap, metav1.GetOptions{})
	require.NoError(t, err)
	deviceClass := deviceClassFromNode(node)
	key := benchmarkKey("ollama", model, deviceClass)
	assert.Equal(t, "10", global.Data[key])
}

func TestRun_VLLM_ComputesTPS(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()

	clock := &fakeClock{t: time.Unix(0, 0)}
	model := "test-model"
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch stripProxyModelPrefix(r.URL.Path, model) {
			case "/health":
				return httpResponse(http.StatusOK, "ok"), nil
			case "/v1/completions":
				// Simulate SSE streaming with include_usage in the final chunk.
				stream := "data: " + `{"choices":[{"text":"hi"}]}` + "\n\n" +
					"data: " + `{"choices":[{"text":"there"}]}` + "\n\n" +
					"data: " + `{"usage":{"completion_tokens":50},"choices":[]}` + "\n\n" +
					"data: [DONE]\n\n"
				resp := httpResponse(http.StatusOK, stream)
				resp.Header.Set("Content-Type", "text/event-stream")
				return resp, nil
			default:
				return httpResponse(http.StatusNotFound, "not found"), nil
			}
		}),
	}

	configMapName := "test-cm"

	b := &Benchmarker{
		kubeClient:  clientset,
		namespace:   "default",
		proxyURL:    "http://backend",
		modelName:   model,
		backendType: "vllm",
		opts: Options{
			WarmupIterations: 0,
			MinDuration:      1 * time.Millisecond,
			Iterations:       5,
			BatchSize:        16,
		}.withDefaults(),
		httpClient: httpClient,
		now:        clock.Now,
		store:      NewConfigMapStore(clientset),
	}

	err := b.Run(context.Background(), model, configMapName)
	require.NoError(t, err)

	cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), configMapName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, "vllm", cm.Data["backend"])
	// Streaming timing uses first->last token window (fake clock advances on each chunk with text).
	assert.Equal(t, "500", cm.Data["tokensPerSecond"])
	assert.Equal(t, "250", cm.Data["completionTokens"])
	assert.Equal(t, "0.5", cm.Data["durationSeconds"])
	assert.Equal(t, "5", cm.Data["samples"])
}

func TestRun_VLLM_ServerTimingViaMetrics(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()

	clock := &fakeClock{t: time.Unix(0, 0)}
	var metricsCalls int
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch stripProxyModelPrefix(r.URL.Path, "test-model") {
			case "/health":
				return httpResponse(http.StatusOK, "ok"), nil
			case "/metrics":
				metricsCalls++
				if metricsCalls == 1 {
					return httpResponse(http.StatusOK, "vllm:request_latency_seconds_sum 5.0\nvllm:request_latency_seconds_count 10\n"), nil
				}
				return httpResponse(http.StatusOK, "vllm:request_latency_seconds_sum 5.1\nvllm:request_latency_seconds_count 11\n"), nil
			case "/v1/completions":
				resp := httpResponse(http.StatusOK, `{"usage":{"completion_tokens":50}}`)
				resp.Header.Set("Content-Type", "application/json")
				return resp, nil
			default:
				return httpResponse(http.StatusNotFound, "not found"), nil
			}
		}),
	}

	b := &Benchmarker{
		kubeClient:  clientset,
		namespace:   "default",
		proxyURL:    "http://backend",
		modelName:   "test-model",
		backendType: "vllm",
		opts: Options{
			WarmupIterations: 0,
			MinDuration:      1 * time.Millisecond,
			Iterations:       1,
			BatchSize:        16,
		}.withDefaults(),
		httpClient: httpClient,
		now:        clock.Now,
		store:      NewConfigMapStore(clientset),
	}

	err := b.Run(context.Background(), "test-model", "test-cm")
	require.NoError(t, err)

	cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), "test-cm", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, "vllm", cm.Data["backend"])
	// 50 tokens / 0.1s server-side latency delta = 500 TPS.
	tps, err := strconv.ParseFloat(cm.Data["tokensPerSecond"], 64)
	require.NoError(t, err)
	assert.InDelta(t, 500.0, tps, 1e-4)
	assert.Equal(t, "50", cm.Data["completionTokens"])
	dur, err := strconv.ParseFloat(cm.Data["durationSeconds"], 64)
	require.NoError(t, err)
	assert.InDelta(t, 0.1, dur, 1e-6)
	assert.Equal(t, "1", cm.Data["samples"])
}

func TestRun_OpenAICompatibleBackends_ComputesTPS(t *testing.T) {
	t.Parallel()

	for _, backendType := range []string{"llamacpp", "mlc-llm"} {
		t.Run(backendType, func(t *testing.T) {
			t.Parallel()

			clientset := fake.NewSimpleClientset()
			clock := &fakeClock{t: time.Unix(0, 0)}

			httpClient := &http.Client{
				Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					switch stripProxyModelPrefix(r.URL.Path, "test-model") {
					case "/health":
						return httpResponse(http.StatusOK, "ok"), nil
					case "/v1/completions":
						stream := "data: " + `{"choices":[{"text":"hi"}]}` + "\n\n" +
							"data: " + `{"choices":[{"text":"there"}]}` + "\n\n" +
							"data: " + `{"usage":{"completion_tokens":50},"choices":[]}` + "\n\n" +
							"data: [DONE]\n\n"
						resp := httpResponse(http.StatusOK, stream)
						resp.Header.Set("Content-Type", "text/event-stream")
						return resp, nil
					default:
						return httpResponse(http.StatusNotFound, "not found"), nil
					}
				}),
			}

			b := &Benchmarker{
				kubeClient:  clientset,
				namespace:   "default",
				proxyURL:    "http://backend",
				modelName:   "test-model",
				backendType: backendType,
				opts: Options{
					WarmupIterations: 0,
					MinDuration:      1 * time.Millisecond,
					Iterations:       1,
					BatchSize:        16,
				}.withDefaults(),
				httpClient: httpClient,
				now:        clock.Now,
				store:      NewConfigMapStore(clientset),
			}

			cmName := "test-cm-" + backendType
			err := b.Run(context.Background(), "test-model", cmName)
			require.NoError(t, err)

			cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), cmName, metav1.GetOptions{})
			require.NoError(t, err)

			tps, err := strconv.ParseFloat(cm.Data["tokensPerSecond"], 64)
			require.NoError(t, err)
			assert.InDelta(t, 500.0, tps, 1e-3)
			assert.Equal(t, backendType, cm.Data["backend"])
		})
	}
}

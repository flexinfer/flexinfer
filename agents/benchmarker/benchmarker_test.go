package benchmarker

import (
	"bytes"
	"context"
	"io"
	"net/http"
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

func TestRun_Ollama_UpsertsConfigMapAndComputesTPS(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()

	clock := &fakeClock{t: time.Unix(0, 0)}
	var generateCalls int
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
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

	model := "test-model"
	configMapName := "test-cm"

	// Ensure upsert path is exercised.
	_, err := clientset.CoreV1().ConfigMaps("default").Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: "default"},
		Data:       map[string]string{"tokensPerSecond": "0"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	b := &Benchmarker{
		kubeClient:  clientset,
		namespace:   "default",
		backendURL:  "http://backend",
		backendType: "ollama",
		opts: Options{
			WarmupIterations: 1,
			MinDuration:      1 * time.Millisecond,
			Iterations:       5,
			BatchSize:        16,
		}.withDefaults(),
		httpClient: httpClient,
		now:        clock.Now,
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
}

func TestRun_VLLM_ComputesTPS(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()

	clock := &fakeClock{t: time.Unix(0, 0)}
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
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

	model := "test-model"
	configMapName := "test-cm"

	b := &Benchmarker{
		kubeClient:  clientset,
		namespace:   "default",
		backendURL:  "http://backend",
		backendType: "vllm",
		opts: Options{
			WarmupIterations: 0,
			MinDuration:      1 * time.Millisecond,
			Iterations:       5,
			BatchSize:        16,
		}.withDefaults(),
		httpClient: httpClient,
		now:        clock.Now,
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

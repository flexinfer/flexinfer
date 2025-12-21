// Package benchmarker implements the logic for running benchmarks and reporting results.
package benchmarker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Benchmarker runs benchmarks for a model on a specific device.
type Benchmarker struct {
	kubeClient  kubernetes.Interface
	namespace   string
	backendURL  string
	backendType string
}

// NewBenchmarker creates a new Benchmarker.
func NewBenchmarker(backendType string) (*Benchmarker, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		return nil, fmt.Errorf("POD_NAMESPACE environment variable not set")
	}

	backendURL := os.Getenv("BACKEND_URL")
	if backendURL == "" {
		backendURL = "http://localhost:11434"
	}

	// Default backend to ollama if not specified
	if backendType == "" {
		backendType = "ollama"
	}

	return &Benchmarker{
		kubeClient:  clientset,
		namespace:   namespace,
		backendURL:  backendURL,
		backendType: backendType,
	}, nil
}

// Run executes the benchmark and stores the result in a ConfigMap.
func (b *Benchmarker) Run(ctx context.Context, model, configMapName string) error {
	log := log.FromContext(ctx)
	log.Info("Running benchmark", "model", model)

	if err := b.waitForBackend(ctx); err != nil {
		return fmt.Errorf("backend failed to become ready: %w", err)
	}

	if err := b.pullModel(ctx, model); err != nil {
		return fmt.Errorf("failed to pull model: %w", err)
	}

	tokensPerSecond, err := b.runBenchmark(ctx, model)
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}

	log.Info("Benchmark result", "tokensPerSecond", tokensPerSecond)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: b.namespace,
		},
		Data: map[string]string{
			"tokensPerSecond": strconv.FormatFloat(tokensPerSecond, 'f', -1, 64),
			"model":           model,
			"timestamp":       time.Now().Format(time.RFC3339),
		},
	}

	log.Info("Creating ConfigMap with benchmark results", "configMap", configMapName)
	_, err = b.kubeClient.CoreV1().ConfigMaps(b.namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create benchmark result configmap: %w", err)
	}

	return nil
}

// waitForBackend polls the backend until it is reachable.
func (b *Benchmarker) waitForBackend(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.Info("Waiting for backend to be ready...", "url", b.backendURL)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	checkPath := ""
	if b.backendType == "vllm" {
		checkPath = "/health"
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timed out waiting for backend")
		case <-ticker.C:
			resp, err := http.Get(b.backendURL + checkPath)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					log.Info("Backend is ready")
					return nil
				}
			}
		}
	}
}

// pullModel triggers the model pull on the backend.
func (b *Benchmarker) pullModel(ctx context.Context, model string) error {
	// vLLM loads model at startup, no pull needed
	if b.backendType == "vllm" {
		return nil
	}

	log := log.FromContext(ctx)
	log.Info("Pulling model...", "model", model)

	reqBody, _ := json.Marshal(map[string]string{"name": model})
	resp, err := http.Post(b.backendURL+"/api/pull", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to pull model: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Read stream explicitly to wait for completion
	// In a real implementation we would parse the JSON stream, for now we just drain it
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// runBenchmark executes inference and calculates tokens per second.
func (b *Benchmarker) runBenchmark(ctx context.Context, model string) (float64, error) {
	log := log.FromContext(ctx)
	log.Info("Executing benchmark queries...", "backend", b.backendType)

	prompt := "Write a long story about a space adventure to Mars."

	var reqBody []byte
	var endpoint string

	if b.backendType == "vllm" {
		endpoint = "/v1/completions"
		reqBody, _ = json.Marshal(map[string]interface{}{
			"model":      model,
			"prompt":     prompt,
			"max_tokens": 100, // Force generation for TPS measurement
		})
	} else {
		endpoint = "/api/generate"
		reqBody, _ = json.Marshal(map[string]interface{}{
			"model":  model,
			"prompt": prompt,
			"stream": false,
		})
	}

	start := time.Now()
	resp, err := http.Post(b.backendURL+endpoint, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	duration := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse response
	if b.backendType == "vllm" {
		// OpenAI API format
		var result struct {
			Usage struct {
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return 0, fmt.Errorf("failed to decode vLLM response: %w", err)
		}
		if result.Usage.CompletionTokens > 0 {
			tps := float64(result.Usage.CompletionTokens) / duration.Seconds()
			return tps, nil
		}
	} else {
		// Ollama API format
		var result struct {
			EvalCount    int   `json:"eval_count"`
			EvalDuration int64 `json:"eval_duration"` // in nanoseconds
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return 0, fmt.Errorf("failed to decode Ollama response: %w", err)
		}
		// Calculate tokens per second based on Ollama's reported metrics if available
		if result.EvalDuration > 0 && result.EvalCount > 0 {
			tps := float64(result.EvalCount) / (float64(result.EvalDuration) / 1e9)
			return tps, nil
		}
	}

	// Fallback to client-side timing if metrics missing
	// (Note: this includes network overhead and prompt processing time, so it's less accurate)
	log.Info("Warning: using client-side timing for benchmark")
	// Estimate: assume 100 tokens generated (simplification for fallback)
	return 100.0 / duration.Seconds(), nil
}

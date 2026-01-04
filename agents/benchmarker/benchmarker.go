// Package benchmarker implements the logic for running benchmarks and reporting results.
package benchmarker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Options struct {
	WarmupIterations int
	MinDuration      time.Duration
	Iterations       int
	BatchSize        int
	Prompt           string
	RequestTimeout   time.Duration
}

func (o Options) withDefaults() Options {
	// Allow explicitly disabling warmup with 0.
	if o.WarmupIterations < 0 {
		o.WarmupIterations = 2
	}
	if o.MinDuration <= 0 {
		o.MinDuration = 30 * time.Second
	}
	if o.Iterations <= 0 {
		o.Iterations = 5
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 128
	}
	if o.Prompt == "" {
		o.Prompt = "Write a long story about a space adventure to Mars."
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 3 * time.Minute
	}
	return o
}

// Benchmarker runs benchmarks for a model on a specific device.
type Benchmarker struct {
	kubeClient  kubernetes.Interface
	namespace   string
	backendURL  string
	backendType string
	opts        Options
	httpClient  *http.Client
	now         func() time.Time
}

// NewBenchmarker creates a new Benchmarker.
func NewBenchmarker(backendType string, opts Options) (*Benchmarker, error) {
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
		opts:        opts.withDefaults(),
		httpClient:  &http.Client{},
		now:         time.Now,
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

	result, err := b.runBenchmark(ctx, model)
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}

	log.Info(
		"Benchmark result",
		"tokensPerSecond", result.TokensPerSecond,
		"tokens", result.CompletionTokens,
		"duration", result.Duration,
		"samples", result.Samples,
	)

	now := b.now().UTC().Format(time.RFC3339)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: b.namespace,
		},
		Data: map[string]string{
			"tokensPerSecond":  strconv.FormatFloat(result.TokensPerSecond, 'f', -1, 64),
			"model":            model,
			"backend":          b.backendType,
			"warmupIterations": strconv.Itoa(b.opts.WarmupIterations),
			"iterations":       strconv.Itoa(b.opts.Iterations),
			"batchSize":        strconv.Itoa(b.opts.BatchSize),
			"minDuration":      b.opts.MinDuration.String(),
			"completionTokens": strconv.Itoa(result.CompletionTokens),
			"durationSeconds":  strconv.FormatFloat(result.Duration.Seconds(), 'f', -1, 64),
			"samples":          strconv.Itoa(result.Samples),
			"timestamp":        now,
		},
	}

	log.Info("Upserting ConfigMap with benchmark results", "configMap", configMapName)
	_, err = b.kubeClient.CoreV1().ConfigMaps(b.namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil && apierrors.IsAlreadyExists(err) {
		existing, getErr := b.kubeClient.CoreV1().ConfigMaps(b.namespace).Get(ctx, configMapName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing benchmark result configmap: %w", getErr)
		}
		existing.Data = cm.Data
		_, err = b.kubeClient.CoreV1().ConfigMaps(b.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("failed to upsert benchmark result configmap: %w", err)
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

	checkPath := b.backendReadinessPath()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.backendURL+checkPath, nil)
		if err != nil {
			return err
		}
		resp, err := b.httpClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.Info("Backend is ready")
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timed out waiting for backend")
		case <-ticker.C:
			continue
		}
	}
}

func (b *Benchmarker) backendReadinessPath() string {
	switch b.backendType {
	case "vllm":
		return "/health"
	default:
		// Ollama and most backends return 200 on /api/tags when ready.
		return "/api/tags"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.backendURL+"/api/pull", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
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

type benchmarkResult struct {
	TokensPerSecond   float64
	CompletionTokens  int
	Duration          time.Duration
	Samples           int
	UsedBackendTiming bool
}

// runBenchmark executes inference and calculates tokens per second.
func (b *Benchmarker) runBenchmark(ctx context.Context, model string) (benchmarkResult, error) {
	log := log.FromContext(ctx)
	log.Info("Executing benchmark queries...", "backend", b.backendType)

	opts := b.opts.withDefaults()

	if opts.WarmupIterations > 0 {
		log.Info("Running warmup", "iterations", opts.WarmupIterations)
		for i := 0; i < opts.WarmupIterations; i++ {
			_, _, _, err := b.generateOnce(ctx, model, opts.Prompt, opts.BatchSize)
			if err != nil {
				return benchmarkResult{}, fmt.Errorf("warmup iteration %d failed: %w", i+1, err)
			}
		}
	}

	log.Info("Running measurement", "minDuration", opts.MinDuration.String(), "batchSize", opts.BatchSize, "iterations", opts.Iterations)

	minSamples := opts.Iterations
	if minSamples < 1 {
		minSamples = 1
	}
	start := b.now()
	var totalTokens int
	var totalTime time.Duration
	var samples int
	usedBackendTiming := true

	for {
		tokens, duration, usedBackend, err := b.generateOnce(ctx, model, opts.Prompt, opts.BatchSize)
		if err != nil {
			return benchmarkResult{}, err
		}
		if tokens <= 0 || duration <= 0 {
			return benchmarkResult{}, fmt.Errorf("invalid benchmark sample: tokens=%d duration=%s", tokens, duration)
		}
		samples++
		totalTokens += tokens
		totalTime += duration
		if !usedBackend {
			usedBackendTiming = false
		}

		elapsedWall := b.now().Sub(start)
		if samples >= minSamples && elapsedWall >= opts.MinDuration {
			break
		}
	}

	if totalTokens <= 0 || totalTime <= 0 {
		return benchmarkResult{}, fmt.Errorf("invalid benchmark totals: tokens=%d duration=%s", totalTokens, totalTime)
	}

	tps := float64(totalTokens) / totalTime.Seconds()
	log.Info("Benchmark completed", "tps", tps, "tokens", totalTokens, "duration", totalTime, "samples", samples, "usedBackendTiming", usedBackendTiming)
	return benchmarkResult{
		TokensPerSecond:   tps,
		CompletionTokens:  totalTokens,
		Duration:          totalTime,
		Samples:           samples,
		UsedBackendTiming: usedBackendTiming,
	}, nil
}

func (b *Benchmarker) generateOnce(ctx context.Context, model, prompt string, maxTokens int) (tokens int, duration time.Duration, usedBackendTiming bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, b.opts.withDefaults().RequestTimeout)
	defer cancel()

	switch b.backendType {
	case "vllm":
		return b.generateOnceVLLM(ctx, model, prompt, maxTokens)
	default:
		return b.generateOnceOllama(ctx, model, prompt)
	}
}

type streamSample struct {
	tokens          int
	generationTime  time.Duration
	firstTokenAfter time.Duration
	wallTime        time.Duration
}

func (b *Benchmarker) generateOnceVLLM(ctx context.Context, model, prompt string, maxTokens int) (tokens int, duration time.Duration, usedBackendTiming bool, err error) {
	if tokens, duration, ok, err := b.generateOnceVLLMServerTiming(ctx, model, prompt, maxTokens); err != nil {
		return 0, 0, false, err
	} else if ok {
		return tokens, duration, true, nil
	}

	// Prefer streaming so we can measure first-token latency and decode throughput more accurately.
	if sample, ok, err := b.generateOnceVLLMStream(ctx, model, prompt, maxTokens); err != nil {
		return 0, 0, false, err
	} else if ok {
		if sample.tokens > 0 && sample.generationTime > 0 {
			return sample.tokens, sample.generationTime, false, nil
		}
		// Fall back to wall time if streaming didn't yield usable timings.
		if sample.tokens > 0 && sample.wallTime > 0 {
			return sample.tokens, sample.wallTime, false, nil
		}
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"prompt":     prompt,
		"max_tokens": maxTokens,
		"stream":     false,
	})

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.backendURL+"/v1/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, false, fmt.Errorf("failed to decode vLLM response: %w", err)
	}

	duration = b.now().Sub(start)
	return result.Usage.CompletionTokens, duration, false, nil
}

func (b *Benchmarker) generateOnceVLLMServerTiming(ctx context.Context, model, prompt string, maxTokens int) (tokens int, duration time.Duration, ok bool, err error) {
	metrics0, ok, err := b.getVLLMServerTimingSnapshot(ctx)
	if err != nil || !ok {
		return 0, 0, false, err
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"prompt":     prompt,
		"max_tokens": maxTokens,
		"stream":     false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.backendURL+"/v1/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, false, fmt.Errorf("failed to decode vLLM response: %w", err)
	}

	metrics1, ok, err := b.getVLLMServerTimingSnapshot(ctx)
	if err != nil || !ok {
		return 0, 0, false, err
	}

	dCount := metrics1.count - metrics0.count
	dSum := metrics1.sumSeconds - metrics0.sumSeconds
	if dCount <= 0 || dSum <= 0 {
		return 0, 0, false, nil
	}

	avgSeconds := dSum / float64(dCount)
	return result.Usage.CompletionTokens, time.Duration(avgSeconds * float64(time.Second)), true, nil
}

type vllmTimingSnapshot struct {
	sumSeconds float64
	count      int64
}

func (b *Benchmarker) getVLLMServerTimingSnapshot(ctx context.Context) (vllmTimingSnapshot, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.backendURL+"/metrics", nil)
	if err != nil {
		return vllmTimingSnapshot{}, false, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return vllmTimingSnapshot{}, false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return vllmTimingSnapshot{}, false, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return vllmTimingSnapshot{}, false, err
	}

	sum, count, ok := extractPromHistogramSumCount(string(body), []string{
		"vllm:request_generation_latency_seconds",
		"vllm:request_latency_seconds",
		"vllm_request_generation_latency_seconds",
		"vllm_request_latency_seconds",
	})
	if !ok {
		return vllmTimingSnapshot{}, false, nil
	}
	return vllmTimingSnapshot{sumSeconds: sum, count: count}, true, nil
}

func extractPromHistogramSumCount(metrics string, bases []string) (sumSeconds float64, count int64, ok bool) {
	for _, base := range bases {
		sumName := base + "_sum"
		countName := base + "_count"
		sum, sumOK := sumPromMetric(metrics, sumName)
		cnt, cntOK := sumPromMetric(metrics, countName)
		if sumOK && cntOK && cnt > 0 {
			return sum, int64(cnt), true
		}
	}
	return 0, 0, false
}

func sumPromMetric(metrics, name string) (float64, bool) {
	var total float64
	var found bool
	for _, line := range strings.Split(metrics, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// <name>{...} <value> OR <name> <value>
		if !strings.HasPrefix(line, name) {
			continue
		}
		// Ensure exact metric match (next char must start labels or value).
		if len(line) > len(name) {
			next := line[len(name)]
			if next != '{' && next != ' ' && next != '\t' {
				continue
			}
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, name))
		if rest == "" {
			continue
		}
		// Drop labels if present.
		if strings.HasPrefix(rest, "{") {
			if idx := strings.Index(rest, "}"); idx >= 0 {
				rest = strings.TrimSpace(rest[idx+1:])
			} else {
				continue
			}
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		total += v
		found = true
	}
	return total, found
}

func (b *Benchmarker) generateOnceVLLMStream(ctx context.Context, model, prompt string, maxTokens int) (streamSample, bool, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"prompt":     prompt,
		"max_tokens": maxTokens,
		"stream":     true,
		"stream_options": map[string]interface{}{
			"include_usage": true,
		},
	})

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.backendURL+"/v1/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return streamSample{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return streamSample{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return streamSample{}, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	type chunk struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
		Usage *struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage,omitempty"`
	}

	reader := bufio.NewReader(resp.Body)

	var firstTokenAt time.Time
	var lastTokenAt time.Time
	var totalTokens int

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return streamSample{}, false, err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var c chunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			// If streaming isn't supported, vLLM may return non-SSE.
			return streamSample{}, false, nil
		}

		if c.Usage != nil && c.Usage.CompletionTokens > 0 {
			totalTokens = c.Usage.CompletionTokens
		}

		hasText := false
		for _, choice := range c.Choices {
			if choice.Text != "" {
				hasText = true
				break
			}
		}
		if hasText {
			now := b.now()
			if firstTokenAt.IsZero() {
				firstTokenAt = now
			}
			lastTokenAt = now
		}
	}

	wall := b.now().Sub(start)
	if totalTokens <= 0 {
		return streamSample{wallTime: wall}, true, nil
	}
	if firstTokenAt.IsZero() || lastTokenAt.IsZero() || !lastTokenAt.After(firstTokenAt) {
		return streamSample{tokens: totalTokens, wallTime: wall}, true, nil
	}

	return streamSample{
		tokens:          totalTokens,
		generationTime:  lastTokenAt.Sub(firstTokenAt),
		firstTokenAfter: firstTokenAt.Sub(start),
		wallTime:        wall,
	}, true, nil
}

func (b *Benchmarker) generateOnceOllama(ctx context.Context, model, prompt string) (tokens int, duration time.Duration, usedBackendTiming bool, err error) {
	// Prefer streaming so we can observe first-token latency while still using server-side eval_duration when available.
	if sample, ok, err := b.generateOnceOllamaStream(ctx, model, prompt); err != nil {
		return 0, 0, false, err
	} else if ok {
		if sample.tokens > 0 && sample.generationTime > 0 {
			return sample.tokens, sample.generationTime, true, nil
		}
		if sample.tokens > 0 && sample.wallTime > 0 {
			return sample.tokens, sample.wallTime, false, nil
		}
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	})

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.backendURL+"/api/generate", bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		EvalCount    int   `json:"eval_count"`
		EvalDuration int64 `json:"eval_duration"` // in nanoseconds
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, false, fmt.Errorf("failed to decode Ollama response: %w", err)
	}

	// Prefer backend-reported eval_duration (excludes prompt processing and network), with a wall-clock fallback.
	if result.EvalCount > 0 && result.EvalDuration > 0 {
		return result.EvalCount, time.Duration(result.EvalDuration), true, nil
	}

	duration = b.now().Sub(start)
	return result.EvalCount, duration, false, nil
}

func (b *Benchmarker) generateOnceOllamaStream(ctx context.Context, model, prompt string) (streamSample, bool, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": true,
	})

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.backendURL+"/api/generate", bytes.NewBuffer(reqBody))
	if err != nil {
		return streamSample{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return streamSample{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return streamSample{}, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	decoder := json.NewDecoder(resp.Body)
	var firstTokenAt time.Time
	var lastTokenAt time.Time
	var finalEvalCount int
	var finalEvalDuration time.Duration

	for {
		var chunk struct {
			Response     string `json:"response"`
			Done         bool   `json:"done"`
			EvalCount    int    `json:"eval_count"`
			EvalDuration int64  `json:"eval_duration"`
		}
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return streamSample{}, false, err
		}
		if chunk.Response != "" {
			now := b.now()
			if firstTokenAt.IsZero() {
				firstTokenAt = now
			}
			lastTokenAt = now
		}
		if chunk.Done {
			finalEvalCount = chunk.EvalCount
			if chunk.EvalDuration > 0 {
				finalEvalDuration = time.Duration(chunk.EvalDuration)
			}
		}
	}

	wall := b.now().Sub(start)
	if finalEvalCount <= 0 {
		return streamSample{wallTime: wall}, true, nil
	}

	s := streamSample{
		tokens:   finalEvalCount,
		wallTime: wall,
	}
	if finalEvalDuration > 0 {
		s.generationTime = finalEvalDuration
	}
	if !firstTokenAt.IsZero() {
		s.firstTokenAfter = firstTokenAt.Sub(start)
	}
	if !lastTokenAt.IsZero() && !firstTokenAt.IsZero() && lastTokenAt.After(firstTokenAt) {
		// This is "observed decode window", useful if eval_duration is absent.
		_ = lastTokenAt
	}
	return s, true, nil
}

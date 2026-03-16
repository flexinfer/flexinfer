// Package benchmarker implements the logic for running benchmarks and reporting results.
package benchmarker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const defaultBenchmarkResultsConfigMap = benchmarkconfig.DefaultBenchmarkResultsConfigMap

type Options struct {
	WarmupIterations int
	MinDuration      time.Duration
	Iterations       int
	BatchSize        int
	Prompt           string
	RequestTimeout   time.Duration
	ModelName        string        // ModelDeployment name for proxy routing
	ColdStartTimeout time.Duration // Timeout waiting for cold start (model scale-up)
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
	if o.ColdStartTimeout <= 0 {
		o.ColdStartTimeout = 5 * time.Minute
	}
	return o
}

// Benchmarker runs benchmarks for a model on a specific device.
type Benchmarker struct {
	kubeClient  kubernetes.Interface
	namespace   string
	proxyURL    string // Base proxy URL (e.g., http://flexinfer-proxy.flexinfer.svc:80)
	modelName   string // ModelDeployment name for proxy routing
	backendType string
	opts        Options
	httpClient  *http.Client
	now         func() time.Time
	nodeName    string
	resultsCM   string
	store       ResultStore
}

// NewBenchmarker creates a new Benchmarker.
func NewBenchmarker(backendType string, opts Options, dsn string) (*Benchmarker, error) {
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

	// Use proxy URL for routing through the FlexInfer proxy
	proxyURL := benchmarkconfig.ProxyURL()

	// Model name for proxy routing (falls back to option if env not set)
	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = opts.ModelName
	}

	// Default backend to ollama if not specified
	if backendType == "" {
		backendType = "ollama"
	}
	if backendType == "llama.cpp" {
		backendType = "llamacpp"
	}
	if backendType == "mlc" {
		backendType = "mlc-llm"
	}

	var stores []ResultStore
	stores = append(stores, NewConfigMapStore(clientset))
	if dsn != "" {
		pgStore, err := NewPostgresStore(dsn, clientset)
		if err != nil {
			return nil, fmt.Errorf("failed to create postgres store: %w", err)
		}
		stores = append(stores, pgStore)
	}

	return &Benchmarker{
		kubeClient:  clientset,
		namespace:   namespace,
		proxyURL:    proxyURL,
		modelName:   modelName,
		backendType: backendType,
		opts:        opts.withDefaults(),
		httpClient:  &http.Client{},
		now:         time.Now,
		nodeName:    os.Getenv("NODE_NAME"),
		resultsCM:   benchmarkconfig.GlobalResultsConfigMapName(),
		store:       NewMultiStore(stores...),
	}, nil
}

// NewBenchmarkerWithClient creates a Benchmarker with an injected kubernetes client and explicit config.
// This is useful for callers that already have a K8s client (e.g., CLI commands, autotune).
func NewBenchmarkerWithClient(kubeClient kubernetes.Interface, namespace, proxyURL, modelName, backendType string, opts Options) *Benchmarker {
	if backendType == "" {
		backendType = "ollama"
	}
	if backendType == "llama.cpp" {
		backendType = "llamacpp"
	}
	if backendType == "mlc" {
		backendType = "mlc-llm"
	}
	return &Benchmarker{
		kubeClient:  kubeClient,
		namespace:   namespace,
		proxyURL:    proxyURL,
		modelName:   modelName,
		backendType: backendType,
		opts:        opts.withDefaults(),
		httpClient:  &http.Client{},
		now:         time.Now,
	}
}

// buildModelURL returns the URL for a specific path routed through the proxy.
// Format: {proxyURL}/model/{modelName}/{path}
func (b *Benchmarker) buildModelURL(path string) string {
	// Strip leading slash from path to avoid double slashes
	path = strings.TrimPrefix(path, "/")
	return fmt.Sprintf("%s/model/%s/%s", b.proxyURL, b.modelName, path)
}

// RunAndReturn executes the benchmark and returns the result without persisting it.
// This is useful for callers that need the result for further processing (e.g., autotune).
func (b *Benchmarker) RunAndReturn(ctx context.Context, model string) (*BenchmarkRecord, error) {
	log := log.FromContext(ctx)
	log.Info("Running benchmark",
		"model", model,
		"modelName", b.modelName,
		"proxyURL", b.proxyURL,
		"backend", b.backendType)

	if err := b.waitForBackend(ctx); err != nil {
		return nil, fmt.Errorf("model failed to become ready: %w", err)
	}

	result, err := b.runBenchmark(ctx, model)
	if err != nil {
		return nil, fmt.Errorf("benchmark failed: %w", err)
	}

	log.Info(
		"Benchmark result",
		"tokensPerSecond", result.TokensPerSecond,
		"tokens", result.CompletionTokens,
		"duration", result.Duration,
		"samples", result.Samples,
	)

	record := &BenchmarkRecord{
		ModelName:        model,
		Backend:          b.backendType,
		NodeName:         b.nodeName,
		Namespace:        b.namespace,
		TokensPerSecond:  result.TokensPerSecond,
		CompletionTokens: result.CompletionTokens,
		Duration:         result.Duration,
		Samples:          result.Samples,
		BatchSize:        b.opts.BatchSize,
		Iterations:       b.opts.Iterations,
		WarmupIterations: b.opts.WarmupIterations,
		MinDuration:      b.opts.MinDuration,
		Timestamp:        b.now(),
	}

	return record, nil
}

// Run executes the benchmark and stores the result in a ConfigMap.
func (b *Benchmarker) Run(ctx context.Context, model, configMapName string) error {
	record, err := b.RunAndReturn(ctx, model)
	if err != nil {
		return err
	}

	record.ConfigMapName = configMapName
	record.GlobalConfigMap = b.resultsCM

	if b.store != nil {
		if err := b.store.Save(ctx, *record); err != nil {
			return fmt.Errorf("failed to save benchmark result: %w", err)
		}
	}

	// Emit benchmark result as Prometheus metrics for scraping.
	b.emitBenchmarkMetrics(ctx, record)

	return nil
}

// emitBenchmarkMetrics publishes benchmark results to Prometheus gauges.
func (b *Benchmarker) emitBenchmarkMetrics(ctx context.Context, record *BenchmarkRecord) {
	vendor, arch := "", ""
	if b.nodeName != "" {
		node, err := b.kubeClient.CoreV1().Nodes().Get(ctx, b.nodeName, metav1.GetOptions{})
		if err == nil {
			vendor = node.Labels["flexinfer.ai/gpu.vendor"]
			arch = node.Labels["flexinfer.ai/gpu.arch"]
		}
	}
	metrics.BenchmarkTokensPerSecond.WithLabelValues(
		record.ModelName, record.Backend, vendor, arch,
	).Set(record.TokensPerSecond)
}

func deviceClassFromNode(node *corev1.Node) string {
	labels := node.Labels
	return fmt.Sprintf(
		"vendor=%s,arch=%s,vram=%s,count=%s,int4=%s",
		labels["flexinfer.ai/gpu.vendor"],
		labels["flexinfer.ai/gpu.arch"],
		labels["flexinfer.ai/gpu.vram"],
		labels["flexinfer.ai/gpu.count"],
		labels["flexinfer.ai/gpu.int4"],
	)
}

func benchmarkKey(backend, model, deviceClass string) string {
	sum := sha256.Sum256([]byte(backend + "|" + model + "|" + deviceClass))
	return "bench_" + hex.EncodeToString(sum[:16])
}

// waitForBackend polls the backend through the proxy until it is reachable.
// This may trigger cold start (GPUGroup model swap, scale-up) which can take several minutes.
func (b *Benchmarker) waitForBackend(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.Info("Waiting for model to be ready through proxy...",
		"proxyURL", b.proxyURL,
		"modelName", b.modelName,
		"coldStartTimeout", b.opts.ColdStartTimeout)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Use cold start timeout (default 5 minutes) since this may trigger scale-up
	timeout := time.After(b.opts.ColdStartTimeout)

	checkPaths := b.backendReadinessPaths()

	for {
		for _, checkPath := range checkPaths {
			// Build URL through proxy: /model/{modelName}/{path}
			url := b.buildModelURL(checkPath)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := b.httpClient.Do(req)
			if err != nil {
				log.V(1).Info("Proxy request failed (model may be starting)", "error", err.Error())
				continue
			}
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				log.Error(err, "Failed to drain backend readiness response body", "path", checkPath)
			}
			if err := resp.Body.Close(); err != nil {
				log.Error(err, "Failed to close backend readiness response body", "path", checkPath)
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.Info("Model is ready through proxy", "path", checkPath)
				return nil
			}
			// 502/503/504 typically mean model is still starting (cold start)
			if resp.StatusCode == http.StatusBadGateway ||
				resp.StatusCode == http.StatusServiceUnavailable ||
				resp.StatusCode == http.StatusGatewayTimeout {
				log.V(1).Info("Model not ready yet (cold start in progress)", "status", resp.StatusCode)
				continue
			}
			// 404 may mean model doesn't exist
			if resp.StatusCode == http.StatusNotFound {
				log.Info("Model not found through proxy, waiting...", "status", resp.StatusCode)
				continue
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timed out waiting for model to become ready (waited %s)", b.opts.ColdStartTimeout)
		case <-ticker.C:
			continue
		}
	}
}

func (b *Benchmarker) backendReadinessPaths() []string {
	switch b.backendType {
	case "vllm":
		return []string{"/health", "/v1/models"}
	case "mlc-llm", "mlc":
		return []string{"/health", "/v1/models"}
	case "llamacpp", "llama.cpp":
		return []string{"/health", "/v1/models"}
	case "tei":
		// Text Embeddings Inference uses /health for readiness checks
		return []string{"/health", "/info"}
	case "comfyui":
		// ComfyUI uses /api/system_stats for health checks
		return []string{"/api/system_stats", "/"}
	case "diffusers":
		// Diffusers API server uses /health
		return []string{"/health", "/v1/models"}
	default:
		// Ollama and most backends return 200 on /api/tags when ready.
		return []string{"/api/tags"}
	}
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
	case "vllm", "mlc-llm", "mlc", "llamacpp", "llama.cpp":
		return b.generateOnceVLLM(ctx, model, prompt, maxTokens)
	case "tei":
		return b.generateOnceTEI(ctx, prompt)
	case "comfyui":
		return b.generateOnceComfyUI(ctx, model)
	case "diffusers":
		return b.generateOnceDiffusers(ctx, model)
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

func (b *Benchmarker) closeResponseBody(ctx context.Context, resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if err := resp.Body.Close(); err != nil {
		log.FromContext(ctx).V(1).Info("failed to close http response body", "error", err)
	}
}

func (b *Benchmarker) readResponseBodyBestEffort(ctx context.Context, resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.FromContext(ctx).V(1).Info("failed to read http response body", "error", err)
		return fmt.Sprintf("<failed to read body: %v>", err)
	}
	return string(body)
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

	reqBody, err := json.Marshal(map[string]interface{}{
		"model":      model,
		"prompt":     prompt,
		"max_tokens": maxTokens,
		"stream":     false,
	})
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to marshal vLLM request: %w", err)
	}

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/v1/completions"), bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return 0, 0, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, body)
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

	reqBody, err := json.Marshal(map[string]interface{}{
		"model":      model,
		"prompt":     prompt,
		"max_tokens": maxTokens,
		"stream":     false,
	})
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to marshal vLLM request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/v1/completions"), bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return 0, 0, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, body)
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
	logger := log.FromContext(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.buildModelURL("/metrics"), nil)
	if err != nil {
		return vllmTimingSnapshot{}, false, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return vllmTimingSnapshot{}, false, nil
	}
	defer b.closeResponseBody(ctx, resp)
	if resp.StatusCode != http.StatusOK {
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			logger.Error(err, "Failed to drain vLLM metrics response body", "status", resp.StatusCode)
		}
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
	reqBody, err := json.Marshal(map[string]interface{}{
		"model":      model,
		"prompt":     prompt,
		"max_tokens": maxTokens,
		"stream":     true,
		"stream_options": map[string]interface{}{
			"include_usage": true,
		},
	})
	if err != nil {
		return streamSample{}, false, fmt.Errorf("failed to marshal vLLM stream request: %w", err)
	}

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/v1/completions"), bytes.NewBuffer(reqBody))
	if err != nil {
		return streamSample{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return streamSample{}, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return streamSample{}, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, body)
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

	reqBody, err := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	})
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to marshal Ollama request: %w", err)
	}

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/api/generate"), bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return 0, 0, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, body)
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
	reqBody, err := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": true,
	})
	if err != nil {
		return streamSample{}, false, fmt.Errorf("failed to marshal Ollama stream request: %w", err)
	}

	start := b.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/api/generate"), bytes.NewBuffer(reqBody))
	if err != nil {
		return streamSample{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return streamSample{}, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return streamSample{}, false, fmt.Errorf("inference failed: status %d, body: %s", resp.StatusCode, body)
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
	}
	return s, true, nil
}

// generateOnceComfyUI generates a single image via the ComfyUI API.
// Returns 1 "token" per image so tokens_per_second becomes images_per_second.
func (b *Benchmarker) generateOnceComfyUI(ctx context.Context, model string) (tokens int, duration time.Duration, usedBackendTiming bool, err error) {
	start := b.now()

	// Minimal ComfyUI workflow: empty latent → KSampler → VAE decode
	workflow := map[string]interface{}{
		"prompt": map[string]interface{}{
			"1": map[string]interface{}{
				"class_type": "EmptyLatentImage",
				"inputs":     map[string]interface{}{"width": 512, "height": 512, "batch_size": 1},
			},
		},
	}

	reqBody, err := json.Marshal(workflow)
	if err != nil {
		return 0, 0, false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/api/prompt"), bytes.NewReader(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return 0, 0, false, fmt.Errorf("ComfyUI generation failed: status %d, body: %s", resp.StatusCode, body)
	}

	// Drain response body
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, 0, false, err
	}

	duration = b.now().Sub(start)
	// 1 "token" = 1 image generated. tokens_per_second becomes images_per_second.
	return 1, duration, false, nil
}

// generateOnceDiffusers generates a single image via the OpenAI-compatible images API.
// Returns 1 "token" per image so tokens_per_second becomes images_per_second.
func (b *Benchmarker) generateOnceDiffusers(ctx context.Context, model string) (tokens int, duration time.Duration, usedBackendTiming bool, err error) {
	start := b.now()

	reqBody, err := json.Marshal(map[string]interface{}{
		"model":           model,
		"prompt":          "A solid blue square",
		"n":               1,
		"size":            "512x512",
		"response_format": "b64_json",
	})
	if err != nil {
		return 0, 0, false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/v1/images/generations"), bytes.NewReader(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return 0, 0, false, fmt.Errorf("diffusers generation failed: status %d, body: %s", resp.StatusCode, body)
	}

	// Drain response body (contains base64 image data)
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, 0, false, err
	}

	duration = b.now().Sub(start)
	// 1 "token" = 1 image generated. tokens_per_second becomes images_per_second.
	return 1, duration, false, nil
}

// generateOnceTEI performs an embedding request to Text Embeddings Inference.
// For embeddings, we measure the time to generate embeddings for the input text.
// Returns "tokens" as the number of input tokens processed.
func (b *Benchmarker) generateOnceTEI(ctx context.Context, prompt string) (tokens int, duration time.Duration, usedBackendTiming bool, err error) {
	start := b.now()

	// TEI uses POST /embed for embeddings
	reqBody, err := json.Marshal(map[string]interface{}{
		"inputs": prompt,
	})
	if err != nil {
		return 0, 0, false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/embed"), bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer b.closeResponseBody(ctx, resp)

	if resp.StatusCode != http.StatusOK {
		body := b.readResponseBodyBestEffort(ctx, resp)
		return 0, 0, false, fmt.Errorf("TEI embed request failed: status %d, body: %s", resp.StatusCode, body)
	}

	// Read response body to ensure request is complete
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, false, err
	}

	duration = b.now().Sub(start)

	// For embeddings, we report "tokens" as a proxy for throughput
	// Use word count as a rough approximation of tokens processed
	wordCount := len(strings.Fields(prompt))
	if wordCount == 0 {
		wordCount = 1
	}

	return wordCount, duration, false, nil
}

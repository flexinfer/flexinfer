package benchmarker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// Task identifies the inference work measured by a benchmark.
type Task string

const (
	TaskGeneration Task = "generation"
	TaskEmbeddings Task = "embeddings"
)

func ParseTask(value string) (Task, error) {
	switch Task(value) {
	case "", TaskGeneration:
		return TaskGeneration, nil
	case TaskEmbeddings:
		return TaskEmbeddings, nil
	default:
		return "", fmt.Errorf("unsupported benchmark task %q (use generation or embeddings)", value)
	}
}

func (o Options) validate(backend string) error {
	task, err := ParseTask(string(o.Task))
	if err != nil {
		return err
	}
	if task != TaskEmbeddings {
		if len(o.EmbeddingInputs) > 0 {
			return fmt.Errorf("embedding inputs require the embeddings task")
		}
		return nil
	}
	switch backend {
	case "vllm", "llamacpp", "llama.cpp", "llama-cpp":
	default:
		return fmt.Errorf("embeddings benchmark is not supported for backend %q (use llama-cpp or vllm)", backend)
	}
	for i, input := range o.EmbeddingInputs {
		if strings.TrimSpace(input) == "" {
			return fmt.Errorf("embedding input %d is empty", i)
		}
	}
	if len(o.EmbeddingInputs) == 0 && strings.TrimSpace(o.withDefaults().Prompt) == "" {
		return fmt.Errorf("embedding prompt is empty")
	}
	return nil
}

type embeddingSample struct {
	promptTokens int
	dimensions   int
	duration     time.Duration
}

// runEmbeddingsBenchmark measures input tokens and documents over request wall
// time. It deliberately does not use decode metrics or estimate tokens from text.
func (b *Benchmarker) runEmbeddingsBenchmark(ctx context.Context, model string) (benchmarkResult, error) {
	opts := b.opts.withDefaults()
	inputs := opts.EmbeddingInputs
	if len(inputs) == 0 {
		inputs = []string{opts.Prompt}
	}
	nextInput, dimensions := 0, 0
	measure := func() (embeddingSample, error) {
		batch := make([]string, opts.BatchSize)
		for i := range batch {
			batch[i] = inputs[nextInput]
			nextInput = (nextInput + 1) % len(inputs)
		}
		sample, err := b.embedOnce(ctx, model, batch)
		if err != nil {
			return embeddingSample{}, err
		}
		if dimensions != 0 && sample.dimensions != dimensions {
			return embeddingSample{}, fmt.Errorf("embedding dimensions changed between requests: got %d, want %d", sample.dimensions, dimensions)
		}
		dimensions = sample.dimensions
		return sample, nil
	}
	for i := 0; i < opts.WarmupIterations; i++ {
		if _, err := measure(); err != nil {
			return benchmarkResult{}, fmt.Errorf("warmup iteration %d failed: %w", i+1, err)
		}
	}

	start := b.now()
	result := benchmarkResult{}
	for {
		sample, err := measure()
		if err != nil {
			return benchmarkResult{}, err
		}
		if sample.duration <= 0 {
			return benchmarkResult{}, fmt.Errorf("invalid embedding benchmark duration: %s", sample.duration)
		}
		result.PromptTokens += sample.promptTokens
		result.Documents += opts.BatchSize
		result.Duration += sample.duration
		result.Samples++
		if result.Samples >= opts.Iterations && b.now().Sub(start) >= opts.MinDuration {
			break
		}
	}
	result.TokensPerSecond = float64(result.PromptTokens) / result.Duration.Seconds()
	result.DocumentsPerSecond = float64(result.Documents) / result.Duration.Seconds()
	return result, nil
}

func (b *Benchmarker) embedOnce(ctx context.Context, model string, inputs []string) (embeddingSample, error) {
	ctx, cancel := context.WithTimeout(ctx, b.opts.withDefaults().RequestTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"model": model, "input": inputs, "encoding_format": "float",
	})
	if err != nil {
		return embeddingSample{}, fmt.Errorf("failed to marshal embeddings request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.buildModelURL("/v1/embeddings"), bytes.NewReader(body))
	if err != nil {
		return embeddingSample{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := b.now()
	resp, err := b.do(req)
	if err != nil {
		return embeddingSample{}, err
	}
	defer b.closeResponseBody(ctx, resp)
	if resp.StatusCode != http.StatusOK {
		return embeddingSample{}, fmt.Errorf("embeddings inference failed: status %d, body: %s", resp.StatusCode, b.readResponseBodyBestEffort(ctx, resp))
	}
	var result struct {
		Data []struct {
			Index     *int       `json:"index"`
			Embedding []*float64 `json:"embedding"`
		} `json:"data"`
		Usage *struct {
			PromptTokens *int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return embeddingSample{}, fmt.Errorf("failed to decode embeddings response: %w", err)
	}
	duration := b.now().Sub(start)
	if len(result.Data) != len(inputs) || len(result.Data) == 0 {
		return embeddingSample{}, fmt.Errorf("embedding response count %d does not match input count %d", len(result.Data), len(inputs))
	}
	dimensions := len(result.Data[0].Embedding)
	for i, item := range result.Data {
		if item.Index == nil || *item.Index != i {
			return embeddingSample{}, fmt.Errorf("embedding response index at position %d must be %d", i, i)
		}
		if dimensions == 0 || len(item.Embedding) != dimensions {
			return embeddingSample{}, fmt.Errorf("embedding at index %d has invalid dimensions %d (expected %d)", i, len(item.Embedding), dimensions)
		}
		nonzero := false
		for _, value := range item.Embedding {
			if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
				return embeddingSample{}, fmt.Errorf("embedding at index %d contains a non-finite or null value", i)
			}
			nonzero = nonzero || *value != 0
		}
		if !nonzero {
			return embeddingSample{}, fmt.Errorf("embedding at index %d is a zero vector", i)
		}
	}
	if result.Usage == nil || result.Usage.PromptTokens == nil || *result.Usage.PromptTokens <= 0 {
		return embeddingSample{}, fmt.Errorf("embeddings response requires positive usage.prompt_tokens to measure input throughput; token estimates are not used")
	}
	return embeddingSample{promptTokens: *result.Usage.PromptTokens, dimensions: dimensions, duration: duration}, nil
}

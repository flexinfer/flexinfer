package backend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// VLLMStallSample aggregates all labeled engine series.
//
// KVUsage is the KV-cache usage gauge (vllm:kv_cache_usage_perc, or the older
// vllm:gpu_cache_usage_perc). vLLM counts prompt tokens only when a request's
// prefill completes, so a chunked prefill longer than the stall window moves
// neither token counter. The scheduler publishes KV usage every engine step,
// and each prefill chunk allocates blocks, so the gauge moves while the
// prefill runs (2026-11-21: a 120K-token prefill on the XTX held both
// counters flat for five minutes while the gauge rose 0.17 -> 0.74). A hung
// engine does not step, so the gauge freezes with the counters. The gauge is
// optional: an engine that does not export it is judged on the counters
// alone, as before.
type VLLMStallSample struct {
	Running, Waiting, Prompt, Generation float64
	KVUsage                              float64
	HasKVUsage                           bool
}

func (s VLLMStallSample) String() string {
	out := fmt.Sprintf("running=%g waiting=%g prompt_tokens=%g generation_tokens=%g", s.Running, s.Waiting, s.Prompt, s.Generation)
	if s.HasKVUsage {
		out += fmt.Sprintf(" kv_cache_usage=%g", s.KVUsage)
	}
	return out
}

// vllmKVUsageMetrics are the KV-cache usage gauge names, newest first. vLLM
// renamed vllm:gpu_cache_usage_perc to vllm:kv_cache_usage_perc; an engine
// exports one of them.
var vllmKVUsageMetrics = []string{"vllm:kv_cache_usage_perc", "vllm:gpu_cache_usage_perc"}

func ParseVLLMStallSample(text string) (VLLMStallSample, error) {
	names := append([]string{"vllm:num_requests_running", "vllm:num_requests_waiting", "vllm:prompt_tokens_total", "vllm:generation_tokens_total"}, vllmKVUsageMetrics...)
	values := make([]float64, len(names))
	seen := make([]bool, len(names))
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		end := strings.IndexAny(line, "{ \t")
		if end < 0 {
			continue
		}
		name := line[:end]
		for i, n := range names {
			if name != n {
				continue
			}
			rest := line[end:]
			if strings.HasPrefix(rest, "{") {
				j := strings.LastIndex(rest, "}")
				if j < 0 {
					return VLLMStallSample{}, fmt.Errorf("invalid metric %s", n)
				}
				rest = rest[j+1:]
			}
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				return VLLMStallSample{}, fmt.Errorf("missing value %s", n)
			}
			v, err := strconv.ParseFloat(fields[0], 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				if i >= 4 {
					// A bad optional gauge is ignored, never a reason to
					// reset the detector on every scrape.
					break
				}
				return VLLMStallSample{}, fmt.Errorf("invalid value %s", n)
			}
			values[i] += v
			seen[i] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return VLLMStallSample{}, err
	}
	for i, ok := range seen[:4] {
		if !ok {
			return VLLMStallSample{}, fmt.Errorf("missing metric %s", names[i])
		}
	}
	sample := VLLMStallSample{Running: values[0], Waiting: values[1], Prompt: values[2], Generation: values[3]}
	for i := 4; i < len(names); i++ {
		if seen[i] {
			sample.KVUsage, sample.HasKVUsage = values[i], true
			break
		}
	}
	return sample, nil
}

// StallDetector requires continuous outstanding work without progress: no
// change in either token counter or, when the engine exports it, in the
// KV-cache usage gauge (see VLLMStallSample).
// Reset on scrape errors to avoid declaring a stall across an observation gap.
type StallDetector struct {
	Window   time.Duration
	last     VLLMStallSample
	since    time.Time
	observed bool
}

func (d *StallDetector) Reset() { d.observed = false; d.since = time.Time{} }
func (d *StallDetector) Observe(s VLLMStallSample, now time.Time) bool {
	window := d.Window
	if window <= 0 {
		window = 5 * time.Minute
	}
	kvMoved := s.HasKVUsage != d.last.HasKVUsage || s.KVUsage != d.last.KVUsage
	if !d.observed || s.Running+s.Waiting == 0 || d.last.Running+d.last.Waiting == 0 || s.Prompt != d.last.Prompt || s.Generation != d.last.Generation || kvMoved || now.Before(d.since) {
		d.since = now
	}
	d.last = s
	d.observed = true
	return s.Running+s.Waiting > 0 && now.Sub(d.since) >= window
}
func StallWindowForSpec(spec *ModelSpec) time.Duration {
	if spec != nil {
		if w := spec.ConfigDuration("stallWindow", 0); w > 0 {
			return w
		}
	}
	return 5 * time.Minute
}

// ReadVLLMStallSample bounds both the HTTP request and response size.
func ReadVLLMStallSample(ctx context.Context, url string) (VLLMStallSample, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return VLLMStallSample{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return VLLMStallSample{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return VLLMStallSample{}, fmt.Errorf("metrics status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return VLLMStallSample{}, err
	}
	if len(body) > 4<<20 {
		return VLLMStallSample{}, fmt.Errorf("metrics response exceeds 4 MiB")
	}
	return ParseVLLMStallSample(string(body))
}

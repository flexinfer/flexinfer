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
type VLLMStallSample struct{ Running, Waiting, Prompt, Generation float64 }

func (s VLLMStallSample) String() string {
	return fmt.Sprintf("running=%g waiting=%g prompt_tokens=%g generation_tokens=%g", s.Running, s.Waiting, s.Prompt, s.Generation)
}
func ParseVLLMStallSample(text string) (VLLMStallSample, error) {
	names := []string{"vllm:num_requests_running", "vllm:num_requests_waiting", "vllm:prompt_tokens_total", "vllm:generation_tokens_total"}
	values := make([]float64, 4)
	seen := make([]bool, 4)
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
				return VLLMStallSample{}, fmt.Errorf("invalid value %s", n)
			}
			values[i] += v
			seen[i] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return VLLMStallSample{}, err
	}
	for i, ok := range seen {
		if !ok {
			return VLLMStallSample{}, fmt.Errorf("missing metric %s", names[i])
		}
	}
	return VLLMStallSample{values[0], values[1], values[2], values[3]}, nil
}

// StallDetector requires continuous outstanding work without token progress.
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
	if !d.observed || s.Running+s.Waiting == 0 || d.last.Running+d.last.Waiting == 0 || s.Prompt != d.last.Prompt || s.Generation != d.last.Generation || now.Before(d.since) {
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

package gauntlet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
)

// ProbeRequest describes a single coherence/latency probe against an
// OpenAI-compatible /v1/completions endpoint.
type ProbeRequest struct {
	Model     string
	Prompt    string
	MaxTokens int
}

// Probe sends one streaming completion request to completionsURL and returns a
// Sample with the captured text, time-to-first-token, completion tokens, and a
// rough decode throughput. Transport/parse failures populate Sample.Err with
// Served=false rather than returning an error; a non-nil error is reserved for
// caller-side programming mistakes (e.g. a malformed request).
func Probe(ctx context.Context, client *http.Client, completionsURL string, pr ProbeRequest, now func() time.Time) (Sample, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	if pr.MaxTokens <= 0 {
		pr.MaxTokens = 64
	}

	reqBody, err := json.Marshal(map[string]any{
		"model":          pr.Model,
		"prompt":         pr.Prompt,
		"max_tokens":     pr.MaxTokens,
		"stream":         true,
		"temperature":    0,
		"stream_options": map[string]any{"include_usage": true},
	})
	if err != nil {
		return Sample{}, fmt.Errorf("marshal probe request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, completionsURL, bytes.NewReader(reqBody))
	if err != nil {
		return Sample{}, fmt.Errorf("build probe request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	benchmarkconfig.ApplyWorkloadClass(req)

	start := now()
	resp, err := client.Do(req)
	if err != nil {
		return Sample{Served: false, Err: fmt.Sprintf("request failed: %v", err)}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Sample{Served: false, Err: fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}, nil
	}

	var (
		text         strings.Builder
		firstTokenAt time.Time
		lastTokenAt  time.Time
		usageTokens  int
	)

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if t, usage, ok := parseSSEChunk(line); ok {
				if t != "" {
					if firstTokenAt.IsZero() {
						firstTokenAt = now()
					}
					lastTokenAt = now()
					text.WriteString(t)
				}
				if usage > 0 {
					usageTokens = usage
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return Sample{Served: false, Err: fmt.Sprintf("read stream: %v", err)}, nil
		}
	}

	out := text.String()
	tokens := usageTokens
	if tokens == 0 {
		tokens = approxTokens(out)
	}

	s := Sample{
		Served:           true,
		CompletionText:   out,
		CompletionTokens: tokens,
	}
	if !firstTokenAt.IsZero() {
		s.TTFT = firstTokenAt.Sub(start)
	}
	if !firstTokenAt.IsZero() && lastTokenAt.After(firstTokenAt) && tokens > 0 {
		decode := lastTokenAt.Sub(firstTokenAt).Seconds()
		if decode > 0 {
			s.TokensPerSecond = float64(tokens) / decode
		}
	}
	if out == "" {
		s.Served = false
		s.Err = "empty completion"
	}
	return s, nil
}

// parseSSEChunk extracts generated text and (when present) the usage token count
// from one SSE line of an OpenAI-compatible completions stream. Non-data lines
// and the [DONE] sentinel return ok=false.
func parseSSEChunk(line string) (text string, usageTokens int, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data:") {
		return "", 0, false
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "[DONE]" {
		return "", 0, false
	}
	var c struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
		Usage *struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage,omitempty"`
	}
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return "", 0, false
	}
	var b strings.Builder
	for _, ch := range c.Choices {
		b.WriteString(ch.Text)
	}
	if c.Usage != nil {
		usageTokens = c.Usage.CompletionTokens
	}
	return b.String(), usageTokens, true
}

// approxTokens is a whitespace-word fallback when the backend omits usage.
func approxTokens(text string) int {
	n := len(strings.Fields(text))
	if n == 0 && strings.TrimSpace(text) != "" {
		return 1
	}
	return n
}

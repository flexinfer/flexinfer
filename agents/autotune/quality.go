package autotune

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// quality.go provides the production QualityFunc for the Goodhart guard: a
// workload-stratified throughput canary. It measures decode tok/s separately for
// a prompt-copy ("lookup") class and an open-ended generation ("novel") class.
// n-gram speculative decoding accelerates lookup but regresses novel; measuring
// the novel class is the true objective the guard protects (kill-test 2026-06-26,
// .loom/killtest-autotune-goodhart-2026-06-26.md). Chat-completions (not raw
// completions) is required so instruct models honor their template instead of
// degenerating into fast trivial tokens that would mask the regression.

type qualityWorkload struct {
	name      string
	prompt    string
	maxTokens int
}

const novelQualityPrompt = "Write an original reflective essay of about 300 words on how a " +
	"person's perception of the passage of time changes as they grow older. Use vivid, varied " +
	"language. Do not use lists or headings. Do not repeat sentences."

// qualityInventoryDoc builds a structured record the lookup workload asks the
// model to reproduce verbatim — maximizing n-gram prompt-lookup acceptance.
func qualityInventoryDoc() string {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "SKU-%03d: qty %d, bin %c%d, status ACTIVE\n", i, (i*7)%53, 'A'+rune(i%26), i%10)
	}
	return b.String()
}

func defaultQualityWorkloads() []qualityWorkload {
	return []qualityWorkload{
		{
			name:      "lookup",
			prompt:    "Reproduce the following inventory record EXACTLY, line by line, with no commentary, preamble, or extra text:\n\n" + qualityInventoryDoc(),
			maxTokens: 256,
		},
		{name: "novel", prompt: novelQualityPrompt, maxTokens: 384},
	}
}

// NewWorkloadQualityFunc returns a QualityFunc that probes an OpenAI-compatible
// chat-completions endpoint and reports per-workload-class decode tok/s (median
// of repeats). chatURL is the full .../v1/chat/completions URL. A nil client uses
// http.DefaultClient; repeats < 1 is treated as 1.
func NewWorkloadQualityFunc(client *http.Client, chatURL, model string, repeats int) QualityFunc {
	if client == nil {
		client = http.DefaultClient
	}
	if repeats < 1 {
		repeats = 1
	}
	workloads := defaultQualityWorkloads()
	return func(ctx context.Context) (map[string]float64, error) {
		out := make(map[string]float64, len(workloads))
		for _, w := range workloads {
			runs := make([]float64, 0, repeats)
			for i := 0; i < repeats; i++ {
				tps, err := probeChatDecodeTPS(ctx, client, chatURL, model, w.prompt, w.maxTokens)
				if err != nil {
					return nil, fmt.Errorf("quality probe class %q: %w", w.name, err)
				}
				runs = append(runs, tps)
			}
			out[w.name] = medianFloat(runs)
		}
		return out, nil
	}
}

// probeChatDecodeTPS streams one chat completion and returns decode throughput
// (completion tokens / first-to-last-token seconds).
func probeChatDecodeTPS(ctx context.Context, client *http.Client, chatURL, model, prompt string, maxTokens int) (float64, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model":          model,
		"messages":       []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens":     maxTokens,
		"stream":         true,
		"temperature":    0,
		"stream_options": map[string]any{"include_usage": true},
	})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL, bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var firstTok, lastTok time.Time
	var usageTokens, chunkTokens int
	reader := bufio.NewReader(resp.Body)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			if content, usage, ok := parseChatSSE(line); ok {
				if content != "" {
					now := time.Now()
					if firstTok.IsZero() {
						firstTok = now
					}
					lastTok = now
					chunkTokens++
				}
				if usage > 0 {
					usageTokens = usage
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return 0, readErr
		}
	}

	tokens := usageTokens
	if tokens == 0 {
		tokens = chunkTokens // fallback when the backend omits usage
	}
	if firstTok.IsZero() || !lastTok.After(firstTok) || tokens == 0 {
		return 0, nil
	}
	return float64(tokens) / lastTok.Sub(firstTok).Seconds(), nil
}

// parseChatSSE extracts delta content and (when present) usage from one SSE line
// of an OpenAI-compatible chat-completions stream.
func parseChatSSE(line string) (content string, usageTokens int, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return "", 0, false
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "[DONE]" {
		return "", 0, false
	}
	var c struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
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
		b.WriteString(ch.Delta.Content)
	}
	if c.Usage != nil {
		usageTokens = c.Usage.CompletionTokens
	}
	return b.String(), usageTokens, true
}

func medianFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

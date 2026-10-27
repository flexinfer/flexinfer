package runtime

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/flexinfer/flexinfer/backend"
)

// Load substages the runtime reports in model health while a backend loads,
// read from the backend output it already streams. The controller maps them
// onto Model.status.loadingSubstage.
const (
	LoadSubstageLoadingWeights = "LoadingWeights"
	LoadSubstageCompiling      = "Compiling"
	LoadSubstageProfilingKV    = "ProfilingKV"
	LoadSubstageReady          = "Ready"
)

// loadProgress follows one backend instance's output: the latest load
// substage with a one-line message, and the vLLM KV-cache profile. Lines
// arrive from the stdout and stderr streams concurrently.
type loadProgress struct {
	mu            sync.Mutex
	substage      string
	message       string
	kvTokens      int64
	kvConcurrency string
	// stopped ends classification once the instance left Loading, so request
	// logs on a serving backend cost nothing.
	stopped bool
}

// observe classifies one output line (or one carriage-return progress update).
// The latest signal wins, except that nothing moves the substage back from
// Ready: vLLM compiles, profiles the KV cache and captures graphs in that
// order, and the message changes at each step, which is what advances
// LoadingProgressAt.
func (p *loadProgress) observe(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.substage == LoadSubstageReady {
		return
	}
	if n, ok := backend.ParseVLLMKVTokensLine(line); ok {
		p.kvTokens = n
		p.set(LoadSubstageProfilingKV, fmt.Sprintf("profiled KV cache (%d tokens)", n))
		return
	}
	if c, ok := backend.ParseVLLMKVConcurrencyLine(line); ok {
		p.kvConcurrency = c
		p.set(LoadSubstageProfilingKV, fmt.Sprintf("profiled KV cache (%d tokens, %sx concurrency)", p.kvTokens, c))
		return
	}
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "uvicorn running on "), strings.Contains(lower, "application startup complete"):
		p.set(LoadSubstageReady, "backend HTTP server up, awaiting readiness probe")
	case strings.Contains(lower, "capturing cuda graph"), strings.Contains(lower, "capturing hip graph"),
		strings.Contains(lower, "capturing cudagraph"), strings.Contains(lower, "compiling a graph"),
		strings.Contains(lower, "torch.compile") && strings.Contains(lower, "compil"):
		p.set(LoadSubstageCompiling, "compiling kernels / capturing graphs")
	case strings.Contains(lower, "memory profiling"):
		p.set(LoadSubstageProfilingKV, "profiling KV cache")
	case strings.Contains(lower, "loading safetensors checkpoint shards"):
		if cur, total, rate, ok := backend.ParseVLLMShardProgress(line); ok {
			p.set(LoadSubstageLoadingWeights, fmt.Sprintf("loading weights (%d/%d shards, %s)", cur, total, rate))
		} else {
			p.set(LoadSubstageLoadingWeights, "loading weights")
		}
	case strings.Contains(lower, "starting to load model"):
		p.set(LoadSubstageLoadingWeights, "loading weights")
	}
}

func (p *loadProgress) set(substage, message string) {
	p.substage = substage
	p.message = message
}

// stop freezes the substage; the KV profile stays readable.
func (p *loadProgress) stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
}

func (p *loadProgress) snapshot() (substage, message string, kvTokens int64, kvConcurrency string) {
	if p == nil {
		return "", "", 0, ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.substage, p.message, p.kvTokens, p.kvConcurrency
}

// scanLinesOrCR splits backend output at '\n' and at '\r', keeping the
// terminator on the token. tqdm (vLLM's shard progress) redraws one line with
// '\r' and only ends it with '\n' when the bar finishes, so splitting at '\n'
// alone would surface weight-loading progress only after it was over.
func scanLinesOrCR(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

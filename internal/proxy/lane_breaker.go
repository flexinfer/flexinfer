package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/flexinfer/flexinfer/pkg/validation"
)

// A lane is shared by its base model and all LoRA adapters. Its probe bypasses
// the public handler so it cannot recurse through admission or the breaker.
type laneBreaker struct {
	mu            sync.Mutex
	failures      int
	open          bool
	until         time.Time
	cooldown      time.Duration
	lastSuccess   time.Time
	budget        time.Duration
	path          string
	target        string
	upstreamModel string
}

func (p *Proxy) lane(name string) *laneBreaker {
	b, _ := p.lanes.LoadOrStore(name, &laneBreaker{lastSuccess: time.Now()})
	return b
}
func (p *Proxy) laneDurations() (time.Duration, time.Duration, time.Duration) {
	c, max, budget := p.laneConfig.LaneBreakerCooldown, p.laneConfig.LaneBreakerMaxCooldown, p.laneConfig.LaneFirstByteBudget
	if c == 0 {
		c = time.Minute
	}
	if max == 0 {
		max = 10 * time.Minute
	}
	if budget == 0 {
		budget = 120 * time.Second
	}
	return c, max, budget
}

// prepareLane refreshes a lane's first-byte budget and probe path from its
// Model annotations and reports whether its breaker is open. Every path that
// forwards to a lane must call it first, or the lane keeps whatever budget the
// last direct request left (the default after a proxy restart).
func (p *Proxy) prepareLane(name string, annotations map[string]string) (open bool, until, last time.Time) {
	b := p.lane(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _, b.budget = p.laneDurations()
	if v, err := time.ParseDuration(annotations["flexinfer.ai/lane-first-byte-budget"]); err == nil && v > 0 {
		b.budget = v
	}
	b.path = laneProbePath(annotations["flexinfer.ai/serve-paths"])
	return b.open, b.until, b.lastSuccess
}

// laneHealthPath is the recovery probe for lanes that serve neither chat nor
// embeddings (images, audio, rerank, ...). Every backend server answers GET
// /health once it is up; a chat completion answers 404 there, which would hold
// the breaker open for as long as the lane lives.
const laneHealthPath = "/health"

// laneProbePath picks the request the breaker's recovery probe sends from the
// Model's flexinfer.ai/serve-paths annotation. A lane that serves chat or
// completions, or declares nothing, gets a one-token chat completion; an
// embeddings-only lane gets an embeddings call; any other lane is probed on
// its health endpoint.
func laneProbePath(servePaths string) string {
	s := strings.TrimSpace(servePaths)
	switch {
	case s == "", strings.Contains(s, "completions"):
		return "/v1/chat/completions"
	case strings.Contains(s, "embeddings"):
		return "/v1/embeddings"
	default:
		return laneHealthPath
	}
}

func (p *Proxy) rejectLane(w http.ResponseWriter, r *http.Request, name, requested string, annotations map[string]string) bool {
	if p.laneConfig.LaneBreakerThreshold == 0 {
		return false
	}
	open, until, last := p.prepareLane(name, annotations)
	if !open {
		return false
	}
	retry := int(math.Ceil(time.Until(until).Seconds()))
	if retry < 1 {
		retry = 1
	}
	p.writeLaneError(w, p.peekModel(r.Context(), name), laneError{
		status:     http.StatusServiceUnavailable,
		code:       validation.CodeLaneUnresponsive,
		message:    fmt.Sprintf("model %s has not completed a request since %s; breaker open, retry after %ds", requested, last.UTC().Format(time.RFC3339), retry),
		retryAfter: retry,
	})
	requestsTotal.WithLabelValues(requested, "lane_unresponsive").Inc()
	return true
}
func (p *Proxy) recordLane(name string, success bool, err error) {
	if p.laneConfig.LaneBreakerThreshold == 0 {
		return
	}
	b := p.lane(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	// In-flight requests from before opening cannot close a breaker; only its
	// single scheduled probe can do that.
	if b.open {
		return
	}
	if success {
		b.failures = 0
		b.lastSuccess = time.Now()
		return
	}
	if err == nil {
		return
	}
	b.failures++
	if b.failures < p.laneConfig.LaneBreakerThreshold {
		return
	}
	b.cooldown, _, _ = p.laneDurations()
	p.openLaneLocked(name, b, err)
}
func (p *Proxy) openLaneLocked(name string, b *laneBreaker, err error) {
	b.open = true
	b.until = time.Now().Add(b.cooldown)
	laneBreakerOpen.WithLabelValues(name).Set(1)
	laneBreakerTrips.WithLabelValues(name).Inc()
	slog.Warn("lane breaker opened", "model", name, "consecutive_failures", b.failures, "last_error", err)
	go p.probeLaneAfter(name, b, b.cooldown)
}
func (p *Proxy) probeLaneAfter(name string, b *laneBreaker, delay time.Duration) {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	b.mu.Lock()
	target, path, model := b.target, b.path, b.upstreamModel
	b.mu.Unlock()
	if p.client != nil {
		if backend := p.getBackendModelName(ctx, name); backend != "" {
			model = backend
		}
	}
	err := probeLane(ctx, target, path, model)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.open = false
		b.failures = 0
		b.cooldown = 0
		b.lastSuccess = time.Now()
		laneBreakerOpen.WithLabelValues(name).Set(0)
		slog.Info("lane breaker closed", "model", name)
		return
	}
	_, max, _ := p.laneDurations()
	b.cooldown *= 2
	if b.cooldown > max {
		b.cooldown = max
	}
	p.openLaneLocked(name, b, err)
}
func probeLane(parent context.Context, target, path, model string) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	var req *http.Request
	var err error
	if path == laneHealthPath {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(target, "/")+laneHealthPath, nil)
	} else {
		payload := map[string]any{"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "Hi"}}}
		if path == "/v1/embeddings" {
			payload = map[string]any{"model": model, "input": "Hi"}
		} else {
			path = "/v1/chat/completions"
		}
		body, _ := json.Marshal(payload)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target, "/")+path, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("probe status %d", resp.StatusCode)
	}
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return err
	}
	if n == 0 && path != laneHealthPath {
		return fmt.Errorf("empty probe response")
	}
	return nil
}

// Peek before ReverseProxy writes headers. The timer covers both response
// headers and body stalls; once a byte arrives it is stopped for long streams.
type firstByteTransport struct {
	base  http.RoundTripper
	stop  func() bool
	first *bool
}
type replayBody struct {
	io.Reader
	io.Closer
}

func (t firstByteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	var first [1]byte
	n, err := io.ReadFull(resp.Body, first[:])
	if n > 0 {
		if t.first != nil {
			*t.first = true
		}
		t.stop()
		resp.Body = replayBody{io.MultiReader(bytes.NewReader(first[:n]), resp.Body), resp.Body}
		return resp, nil
	}
	if err == io.EOF {
		t.stop()
		return resp, nil
	}
	_ = resp.Body.Close()
	if err == nil {
		err = io.ErrNoProgress
	}
	return nil, err
}

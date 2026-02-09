package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// SSEBroadcaster is the interface used to send events to browser clients.
type SSEBroadcaster interface {
	Broadcast(event bridge.SSEEvent)
}

// CoordinatorStatus holds the aggregate status of the coordinator.
type CoordinatorStatus struct {
	Enabled         bool         `json:"enabled"`
	Healthy         bool         `json:"healthy"`
	Model           string       `json:"model"`
	CircuitState    string       `json:"circuit_state"`
	Failures        int          `json:"failures"`
	Subsystems      SubsystemMap `json:"subsystems"`
	LastPoll        string       `json:"last_poll,omitempty"`
	PollInterval    string       `json:"poll_interval"`
	AvailableModels []string     `json:"available_models,omitempty"`
}

// SubsystemMap reports enabled/disabled state of each subsystem.
type SubsystemMap struct {
	Summarizer bool `json:"summarizer"`
	Compressor bool `json:"compressor"`
	Triager    bool `json:"triager"`
	Extractor  bool `json:"extractor"`
	Planner    bool `json:"planner"`
}

// Coordinator orchestrates LLM-powered agent context operations.
type Coordinator struct {
	config Config
	client *FlexInferClient
	agent  *bridge.AgentBridge
	sse    SSEBroadcaster
	logger *slog.Logger

	// Sub-components.
	summarizer *Summarizer
	compressor *Compressor
	triager    *Triager
	extractor  *Extractor
	planner    *Planner

	// Concurrency control.
	sem chan struct{} // Semaphore limiting concurrent LLM calls.

	// State.
	mu       sync.RWMutex
	healthy  bool
	lastPoll time.Time
	models   []string // Cached model IDs.

	// Lifecycle.
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewCoordinator creates a Coordinator. Returns nil if the config is disabled.
func NewCoordinator(cfg Config, agent *bridge.AgentBridge, sse SSEBroadcaster, logger *slog.Logger) *Coordinator {
	if !cfg.Enabled() {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "coordinator")

	breaker := NewCircuitBreaker(cfg.CircuitBreakerThreshold, cfg.CircuitBreakerReset)
	client := NewFlexInferClient(cfg.FlexInferURL, cfg.FlexInferKey, breaker, logger)

	c := &Coordinator{
		config: cfg,
		client: client,
		agent:  agent,
		sse:    sse,
		logger: logger,
		sem:    make(chan struct{}, cfg.MaxConcurrentLLM),
		stopCh: make(chan struct{}),
	}

	// Initialize subsystems based on feature toggles.
	if cfg.EnableSummarizer {
		c.summarizer = NewSummarizer(client, agent, cfg, logger)
	}
	if cfg.EnableCompressor {
		c.compressor = NewCompressor(client, agent, cfg, logger)
	}
	if cfg.EnableTriager {
		c.triager = NewTriager(client, agent, cfg, logger)
	}
	if cfg.EnableExtractor {
		c.extractor = NewExtractor(client, agent, cfg, logger)
	}
	if cfg.EnablePlanner {
		c.planner = NewPlanner(client, agent, cfg, logger)
	}

	return c
}

// Start performs an initial health check and begins the background poll loop.
// Returns an error only if the initial health check fails (coordinator will
// be nil and the HUD continues without it).
func (c *Coordinator) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.client.HealthCheck(ctx); err != nil {
		c.logger.Warn("coordinator: FlexInfer unreachable at startup, disabling", "error", err)
		return err
	}

	// Cache available models.
	if models, err := c.client.Models(ctx); err == nil {
		c.mu.Lock()
		c.healthy = true
		c.models = make([]string, len(models))
		for i, m := range models {
			c.models[i] = m.ID
		}
		c.mu.Unlock()
	}

	c.logger.Info("coordinator started",
		"url", c.config.FlexInferURL,
		"model", c.config.DefaultModel,
		"poll_interval", c.config.PollInterval,
	)

	c.broadcastEvent("coordinator.health", map[string]any{
		"flexinfer_healthy": true,
		"circuit_state":     c.client.breaker.State().String(),
		"model":             c.config.DefaultModel,
	})

	c.wg.Add(1)
	go c.pollLoop()

	return nil
}

// Stop signals the poll loop to exit and waits for completion.
func (c *Coordinator) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		c.wg.Wait()
		c.logger.Info("coordinator stopped")
	})
}

// Status returns the current coordinator status.
func (c *Coordinator) Status() CoordinatorStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var lastPoll string
	if !c.lastPoll.IsZero() {
		lastPoll = c.lastPoll.Format(time.RFC3339)
	}

	return CoordinatorStatus{
		Enabled:      true,
		Healthy:      c.healthy,
		Model:        c.config.DefaultModel,
		CircuitState: c.client.breaker.State().String(),
		Failures:     c.client.breaker.Failures(),
		Subsystems: SubsystemMap{
			Summarizer: c.config.EnableSummarizer,
			Compressor: c.config.EnableCompressor,
			Triager:    c.config.EnableTriager,
			Extractor:  c.config.EnableExtractor,
			Planner:    c.config.EnablePlanner,
		},
		LastPoll:        lastPoll,
		PollInterval:    c.config.PollInterval.String(),
		AvailableModels: c.models,
	}
}

// OnSessionEnd is called when an agent session ends. It triggers async
// summarization if the summarizer is enabled.
func (c *Coordinator) OnSessionEnd(sessionID, agentID string) {
	if c.summarizer == nil {
		return
	}
	go func() {
		if !c.acquireSem() {
			return
		}
		defer c.releaseSem()

		ctx, cancel := context.WithTimeout(context.Background(), c.config.DefaultTimeout)
		defer cancel()

		c.broadcastEvent("coordinator.summarize.start", map[string]any{
			"session_id": sessionID,
		})

		result, err := c.summarizer.SummarizeSession(ctx, sessionID)
		if err != nil {
			c.logger.Warn("summarize session failed", "session_id", sessionID, "error", err)
			return
		}

		c.broadcastEvent("coordinator.summarize.complete", map[string]any{
			"session_id":      sessionID,
			"summary_preview": truncate(result.Summary, 200),
		})
	}()
}

// SummarizeSession performs on-demand summarization (for API calls).
func (c *Coordinator) SummarizeSession(ctx context.Context, sessionID string) (*SessionSummaryResult, error) {
	if c.summarizer == nil {
		return nil, fmt.Errorf("summarizer is disabled")
	}
	return c.summarizer.SummarizeSession(ctx, sessionID)
}

// RunCompression performs on-demand memory compression (for API calls).
func (c *Coordinator) RunCompression(ctx context.Context) (*CompactionResult, error) {
	if c.compressor == nil {
		return nil, fmt.Errorf("compressor is disabled")
	}
	return c.compressor.RunCompactionCycle(ctx)
}

// PlanWorkflow generates a workflow from a natural language goal.
func (c *Coordinator) PlanWorkflow(ctx context.Context, goal, namespace string) (*WorkflowPlan, error) {
	if c.planner == nil {
		return nil, fmt.Errorf("planner is disabled")
	}
	return c.planner.PlanFromGoal(ctx, goal, namespace)
}

// RegisterPlan converts a WorkflowPlan into a workflow definition and registers it.
func (c *Coordinator) RegisterPlan(ctx context.Context, plan *WorkflowPlan, namespace string) (string, error) {
	if c.planner == nil {
		return "", fmt.Errorf("planner is disabled")
	}
	return c.planner.RegisterPlan(ctx, plan, namespace)
}

// pollLoop runs periodic sweeps at the configured interval.
func (c *Coordinator) pollLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			c.logger.Debug("coordinator poll loop stopped")
			return
		case <-ticker.C:
			c.poll()
		}
	}
}

// poll executes one sweep cycle.
func (c *Coordinator) poll() {
	ctx, cancel := context.WithTimeout(context.Background(), c.config.DefaultTimeout)
	defer cancel()

	c.mu.Lock()
	c.lastPoll = time.Now()
	c.mu.Unlock()

	// Health check — update model list and broadcast state changes.
	prevHealthy := c.isHealthy()
	if err := c.client.HealthCheck(ctx); err != nil {
		c.mu.Lock()
		c.healthy = false
		c.mu.Unlock()
		if prevHealthy {
			c.broadcastEvent("coordinator.health", map[string]any{
				"flexinfer_healthy": false,
				"circuit_state":     c.client.breaker.State().String(),
			})
		}
		return
	}
	c.mu.Lock()
	c.healthy = true
	c.mu.Unlock()
	if !prevHealthy {
		c.broadcastEvent("coordinator.health", map[string]any{
			"flexinfer_healthy": true,
			"circuit_state":     c.client.breaker.State().String(),
			"model":             c.config.DefaultModel,
		})
	}

	// Sweep ended sessions for unsummarized ones.
	if c.summarizer != nil {
		if !c.acquireSem() {
			return
		}
		count, err := c.summarizer.SweepEndedSessions(ctx)
		c.releaseSem()
		if err != nil {
			c.logger.Debug("sweep ended sessions error", "error", err)
		} else if count > 0 {
			c.logger.Info("swept ended sessions", "summarized", count)
		}
	}

	// Triage recent entries.
	if c.triager != nil {
		if !c.acquireSem() {
			return
		}
		result, err := c.triager.TriageRecent(ctx)
		c.releaseSem()
		if err != nil {
			c.logger.Debug("triage recent error", "error", err)
		} else if result != nil && result.Count > 0 {
			c.broadcastEvent("coordinator.triage.complete", map[string]any{
				"count":    result.Count,
				"critical": result.Critical,
				"high":     result.High,
			})
		}
	}

	// Extract entities from recent entries.
	if c.extractor != nil {
		if !c.acquireSem() {
			return
		}
		result, err := c.extractor.ExtractRecent(ctx)
		c.releaseSem()
		if err != nil {
			c.logger.Debug("extract recent error", "error", err)
		} else if result != nil && (result.EntitiesAdded > 0 || result.RelationsAdded > 0) {
			c.broadcastEvent("coordinator.extract.complete", map[string]any{
				"entities_added":  result.EntitiesAdded,
				"relations_added": result.RelationsAdded,
			})
		}
	}

	// Run compaction if memory is over threshold.
	if c.compressor != nil {
		if !c.acquireSem() {
			return
		}
		result, err := c.compressor.RunCompactionCycle(ctx)
		c.releaseSem()
		if err != nil {
			c.logger.Debug("compaction cycle error", "error", err)
		} else if result != nil && result.CompressedCount > 0 {
			c.broadcastEvent("coordinator.compress.complete", map[string]any{
				"tier":             result.Tier,
				"compressed_count": result.CompressedCount,
				"tokens_saved":     result.TokensSaved,
			})
		}
	}
}

// selectModel checks if the preferred model is available; falls back if not.
func (c *Coordinator) selectModel(preferred string) string {
	c.mu.RLock()
	models := c.models
	c.mu.RUnlock()

	if preferred == "" {
		preferred = c.config.DefaultModel
	}

	// Check preferred model.
	for _, m := range models {
		if m == preferred {
			return preferred
		}
	}

	// Try fallback.
	if c.config.FallbackModel != "" {
		for _, m := range models {
			if m == c.config.FallbackModel {
				return c.config.FallbackModel
			}
		}
	}

	// Return preferred anyway — FlexInfer may accept it.
	return preferred
}

// broadcastEvent sends a coordinator SSE event to browser clients.
func (c *Coordinator) broadcastEvent(eventType string, payload any) {
	if c.sse == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	c.sse.Broadcast(bridge.SSEEvent{
		ID:        fmt.Sprintf("%s-%d", eventType, time.Now().UnixMilli()),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// acquireSem tries to acquire the LLM semaphore without blocking.
// Returns false if the semaphore is full.
func (c *Coordinator) acquireSem() bool {
	select {
	case c.sem <- struct{}{}:
		return true
	default:
		c.logger.Debug("coordinator: LLM semaphore full, skipping")
		return false
	}
}

// releaseSem releases one slot in the LLM semaphore.
func (c *Coordinator) releaseSem() {
	<-c.sem
}

// isHealthy reports current health status.
func (c *Coordinator) isHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.healthy
}

// truncate returns s truncated to maxLen characters with "..." appended.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

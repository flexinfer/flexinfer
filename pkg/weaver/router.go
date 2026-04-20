package weaver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/openairesponses"
)

// tokensPerIteration is the rough estimate of tokens consumed per orchestration
// loop iteration, used to derive max iterations from a token budget.
const tokensPerIteration = 512

// QueryRequest defines parameters for an orchestrated query.
type QueryRequest struct {
	Query     string   `json:"query"`
	Domains   []string `json:"domains,omitempty"`
	MaxTokens int      `json:"max_tokens,omitempty"`
	Identity  openairesponses.ExecutionIdentity
	// ParentSessionID is the caller's proxy session ID (from the daemon
	// SessionManager). When the Router dispatches to a SpawnBridge, it
	// flows into the spawn pod as LOOM_PARENT_SESSION_ID so downstream
	// CLI hooks can stitch the spawn's agent-context session to the
	// originating proxy session. Optional; empty when the caller has no
	// proxy session (e.g., internal daemon-initiated queries).
	ParentSessionID string `json:"parent_session_id,omitempty"`
}

// DomainResult holds the output from a single domain's subagent.
type DomainResult struct {
	Domain     string `json:"domain"`
	Answer     string `json:"answer"`
	Tokens     int    `json:"tokens"`
	LatencyMs  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
	Iterations int    `json:"iterations"`
}

// QueryResult holds the aggregated output from an orchestrated query.
type QueryResult struct {
	Answer        string         `json:"answer"`
	DomainResults []DomainResult `json:"domain_results"`
	TotalTokens   int            `json:"total_tokens"`
	LatencyMs     int64          `json:"latency_ms"`
	DomainsUsed   []string       `json:"domains_used"`
}

const maxHistoryEntries = 100

// QueryHistoryEntry records a completed orchestrated query for HUD display.
type QueryHistoryEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	QueryID     string    `json:"query_id"`
	Query       string    `json:"query"`
	Domains     []string  `json:"domains"`
	Status      string    `json:"status"`
	LatencyMs   int64     `json:"latency_ms"`
	TotalTokens int       `json:"total_tokens"`
}

// Router orchestrates multi-domain queries using local FlexInfer models,
// with optional dispatch to real headless-agent pods via SpawnBridge.
type Router struct {
	cfg         Config
	client      *flexinfer.Client
	executor    *DaemonToolExecutor
	lister      ToolLister
	registry    *DomainRegistry
	metrics     *Metrics
	tracer      trace.Tracer
	logger      *slog.Logger
	spawnBridge SpawnBridge

	historyMu sync.Mutex
	history   []QueryHistoryEntry
}

// NewRouter creates a Router with the given dependencies.
func NewRouter(cfg Config, client *flexinfer.Client, executor *DaemonToolExecutor, lister ToolLister, logger *slog.Logger) *Router {
	reg := NewDomainRegistry()
	for _, d := range DefaultDomains() {
		reg.Register(d)
	}

	return &Router{
		cfg:         cfg,
		client:      client,
		executor:    executor,
		lister:      lister,
		registry:    reg,
		logger:      logger.With("component", "weaver-router"),
		spawnBridge: NoopSpawnBridge{},
	}
}

// SetSpawnBridge installs a SpawnBridge used to dispatch SubAgents whose
// Backend is non-flexinfer. Passing nil reverts to the NoopSpawnBridge
// default, which fails fast with ErrSpawnBridgeNotConfigured.
func (r *Router) SetSpawnBridge(b SpawnBridge) {
	if b == nil {
		r.spawnBridge = NoopSpawnBridge{}
		return
	}
	r.spawnBridge = b
}

// SetMetrics sets the Prometheus metrics collector.
func (r *Router) SetMetrics(m *Metrics) {
	r.metrics = m
}

// SetTracer sets the OpenTelemetry tracer for trace span instrumentation.
func (r *Router) SetTracer(t trace.Tracer) {
	r.tracer = t
}

// Registry returns the domain registry for external registration.
func (r *Router) Registry() *DomainRegistry {
	return r.registry
}

// MetricsSummary returns lifetime metrics for HUD display.
func (r *Router) MetricsSummary() map[string]any {
	if r.metrics == nil {
		return nil
	}
	return r.metrics.Summary()
}

// Query executes an orchestrated query, optionally classifying the domain first.
func (r *Router) Query(ctx context.Context, req QueryRequest) (QueryResult, error) {
	start := time.Now()
	queryID := uuid.New().String()[:8]
	qlog := r.logger.With("query_id", queryID)

	if r.tracer != nil {
		var span trace.Span
		ctx, span = r.tracer.Start(ctx, "weaver.query",
			trace.WithAttributes(
				attribute.String("query", req.Query),
				attribute.String("query_id", queryID),
			),
		)
		defer func() {
			span.SetAttributes(attribute.Int("domain_count", len(req.Domains)))
			span.End()
		}()
	}

	if err := r.cfg.RequireEnabled(); err != nil {
		return QueryResult{}, err
	}

	domains := req.Domains
	if len(domains) == 0 {
		classified, err := r.classify(ctx, req.Query, qlog)
		if err != nil {
			r.recordQuery(start, "error")
			r.recordHistory(QueryHistoryEntry{
				Timestamp: start, QueryID: queryID, Query: req.Query, Domains: domains,
				Status: "error", LatencyMs: time.Since(start).Milliseconds(),
			})
			return QueryResult{}, fmt.Errorf("classify: %w", err)
		}
		domains = classified
	}

	if len(domains) == 0 {
		// F10: auto-compose fallback for unmatched compound queries.
		if result, ok, err := r.TryAutoCompose(ctx, req.Query); ok {
			if err != nil {
				r.recordQuery(start, "error")
				r.recordHistory(QueryHistoryEntry{
					Timestamp: start, QueryID: queryID, Query: req.Query,
					Status: "error", LatencyMs: time.Since(start).Milliseconds(),
				})
				return QueryResult{}, err
			}
			result.LatencyMs = time.Since(start).Milliseconds()
			r.recordQuery(start, "ok")
			r.recordHistory(QueryHistoryEntry{
				Timestamp: start, QueryID: queryID, Query: req.Query,
				Domains: result.DomainsUsed, Status: "ok",
				LatencyMs: result.LatencyMs, TotalTokens: result.TotalTokens,
			})
			return result, nil
		}
		r.recordQuery(start, "no_match")
		r.recordHistory(QueryHistoryEntry{
			Timestamp: start, QueryID: queryID, Query: req.Query,
			Status: "no_match", LatencyMs: time.Since(start).Milliseconds(),
		})
		return QueryResult{
			Answer:    "No matching domains found for this query.",
			LatencyMs: time.Since(start).Milliseconds(),
		}, nil
	}

	result, err := r.dispatch(ctx, domains, req, queryID, qlog)
	if err != nil {
		r.recordQuery(start, "error")
		r.recordHistory(QueryHistoryEntry{
			Timestamp: start, QueryID: queryID, Query: req.Query, Domains: domains,
			Status: "error", LatencyMs: time.Since(start).Milliseconds(),
		})
		return QueryResult{}, err
	}

	result.LatencyMs = time.Since(start).Milliseconds()
	r.recordQuery(start, "ok")
	r.recordHistory(QueryHistoryEntry{
		Timestamp: start, QueryID: queryID, Query: req.Query, Domains: domains,
		Status: "ok", LatencyMs: result.LatencyMs, TotalTokens: result.TotalTokens,
	})
	return result, nil
}

// Gather executes an orchestrated query against specified domains (no classification).
func (r *Router) Gather(ctx context.Context, domains []string, query string, identity openairesponses.ExecutionIdentity) (QueryResult, error) {
	return r.Query(ctx, QueryRequest{
		Query:    query,
		Domains:  domains,
		Identity: identity,
	})
}

// Status returns the current weaver status.
func (r *Router) Status() map[string]any {
	domains := r.registry.List()
	return map[string]any{
		"enabled":        r.cfg.Enabled,
		"router_model":   r.cfg.RouterModel,
		"subagent_model": r.cfg.SubagentModel,
		"domains":        domains,
		"max_iterations": r.cfg.MaxIterations,
		"max_concurrent": r.cfg.MaxConcurrent,
	}
}

// classify uses the router model to determine which domains to query.
func (r *Router) classify(ctx context.Context, query string, logger *slog.Logger) ([]string, error) {
	var classifySpan trace.Span
	if r.tracer != nil {
		ctx, classifySpan = r.tracer.Start(ctx, "weaver.classify",
			trace.WithAttributes(attribute.String("query", query)),
		)
		defer classifySpan.End()
	}

	allDomains := r.registry.List()
	if len(allDomains) == 0 {
		return nil, nil
	}

	prompt := routerClassifyPrompt(allDomains)
	resp, err := r.client.CompleteSimple(ctx, r.cfg.RouterModel, prompt, r.applyModelPrefix(r.cfg.RouterModel, query), 256)
	if err != nil {
		return nil, fmt.Errorf("classification LLM call: %w", err)
	}

	var result struct {
		Domains []string `json:"domains"`
	}
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		logger.Warn("failed to parse classification response", "response", resp, "error", err)
		return nil, nil
	}

	// Validate domains exist in the registry.
	var valid []string
	for _, d := range result.Domains {
		if _, ok := r.registry.Get(d); ok {
			valid = append(valid, d)
		}
	}

	if classifySpan != nil {
		classifySpan.SetAttributes(attribute.StringSlice("classified_domains", valid))
	}

	logger.Debug("classified query", "query", query, "domains", valid)
	return valid, nil
}

// dispatch runs subagents for the specified domains in parallel.
func (r *Router) dispatch(ctx context.Context, domains []string, req QueryRequest, queryID string, logger *slog.Logger) (QueryResult, error) {
	if r.tracer != nil {
		var span trace.Span
		ctx, span = r.tracer.Start(ctx, "weaver.dispatch",
			trace.WithAttributes(
				attribute.StringSlice("domains", domains),
				attribute.Int("concurrent_limit", r.cfg.MaxConcurrent),
			),
		)
		defer span.End()
	}

	sem := make(chan struct{}, r.cfg.MaxConcurrent)

	results := make([]DomainResult, len(domains))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, domainName := range domains {
		agent, ok := r.registry.Get(domainName)
		if !ok {
			results[i] = DomainResult{
				Domain: domainName,
				Error:  "domain not found",
			}
			continue
		}

		wg.Add(1)
		go func(idx int, agent SubAgent) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			subCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
			defer cancel()

			dr := r.runSubAgent(subCtx, agent, req, queryID, logger)

			mu.Lock()
			results[idx] = dr
			mu.Unlock()
		}(i, agent)
	}

	wg.Wait()

	// Aggregate results.
	var totalTokens int
	var domainsUsed []string
	for _, dr := range results {
		totalTokens += dr.Tokens
		if dr.Error == "" {
			domainsUsed = append(domainsUsed, dr.Domain)
		}
	}

	// Synthesize if multiple domains returned results.
	answer := r.synthesize(ctx, results, req.Query, logger)

	return QueryResult{
		Answer:        answer,
		DomainResults: results,
		TotalTokens:   totalTokens,
		DomainsUsed:   domainsUsed,
	}, nil
}

// runSubAgent executes the orchestration loop for a single domain.
func (r *Router) runSubAgent(ctx context.Context, agent SubAgent, req QueryRequest, queryID string, logger *slog.Logger) DomainResult {
	start := time.Now()
	domain := agent.Name

	// Non-flexinfer backends route to a SpawnBridge that creates real
	// headless-agent pods. The bridge returns a BridgeResult we fold into
	// the standard DomainResult shape so callers downstream can treat
	// real-agent and FlexInfer-agent outputs uniformly.
	if !agent.IsFlexInferBackend() {
		return r.runSubAgentViaBridge(ctx, agent, req, queryID, start, logger)
	}

	model := r.cfg.SubagentModel
	if agent.Model != "" {
		model = agent.Model
	}

	if r.tracer != nil {
		var span trace.Span
		ctx, span = r.tracer.Start(ctx, "weaver.subagent",
			trace.WithAttributes(
				attribute.String("domain", domain),
				attribute.String("model", model),
			),
		)
		defer span.End()
	}

	maxIter := r.cfg.MaxIterations
	if agent.TokenBudget > 0 {
		estimatedIter := agent.TokenBudget / tokensPerIteration
		if estimatedIter > 0 && estimatedIter < maxIter {
			maxIter = estimatedIter
		}
		logger.Debug("token budget adjusted iterations",
			"domain", domain,
			"budget", agent.TokenBudget,
			"max_iter", maxIter,
		)
	}

	if r.tracer != nil {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.Int("max_iterations", maxIter))
	}

	adapter := NewSubAgentAdapter(agent, r.lister)
	systemPrompt := subAgentSystemPrompt(agent)

	responsesClient := NewFlexInferResponsesClient(r.client, r.cfg.ModelBehaviors, r.cfg.HTTPTimeout, r.logger)
	var telemetry openairesponses.TelemetrySink
	if r.metrics != nil {
		telemetry = NewSubagentTelemetry(domain, r.metrics)
	}

	orch := &openairesponses.Orchestrator{
		Config: openairesponses.Config{
			Enabled:           true,
			RequestTimeout:    r.cfg.Timeout,
			MaxLoopIterations: maxIter,
		},
		Client:    responsesClient,
		Adapter:   adapter,
		Executor:  r.executor,
		Telemetry: telemetry,
	}

	turnReq := openairesponses.TurnRequest{
		Model: model,
		Input: req.Query,
		Meta: map[string]string{
			"system_prompt": systemPrompt,
			"query":         req.Query,
		},
		Context: openairesponses.ContextStrategy{
			Mode: openairesponses.ContextModeStateless,
		},
	}

	loopResult, err := orch.Run(ctx, turnReq, req.Identity)
	latencyMs := time.Since(start).Milliseconds()

	if r.metrics != nil {
		r.metrics.SubagentDuration.WithLabelValues(domain).Observe(time.Since(start).Seconds())
	}

	if err != nil {
		logger.Warn("subagent failed", "domain", domain, "error", err, "latency_ms", latencyMs)
		return DomainResult{
			Domain:    domain,
			Error:     err.Error(),
			LatencyMs: latencyMs,
		}
	}

	return DomainResult{
		Domain:     domain,
		Answer:     loopResult.Final.OutputText,
		Tokens:     loopResult.TotalPromptTokens + loopResult.TotalCompletionTokens,
		LatencyMs:  latencyMs,
		Iterations: loopResult.Iterations,
	}
}

// runSubAgentViaBridge dispatches a SubAgent to the configured SpawnBridge
// (real headless Claude/Codex/Gemini pod) and folds the BridgeResult into
// the standard DomainResult shape. Safety gate: domains that declared a
// non-flexinfer backend without RequiresSpawn=true are rejected here so a
// misconfigured YAML never causes surprise pod creation; the daemon-side
// authorization layer is still responsible for gating by caller scope
// (ScopeAgentSpawn).
func (r *Router) runSubAgentViaBridge(
	ctx context.Context,
	agent SubAgent,
	req QueryRequest,
	queryID string,
	start time.Time,
	logger *slog.Logger,
) DomainResult {
	domain := agent.Name
	sublog := logger.With("domain", domain, "backend", agent.Backend)

	if r.tracer != nil {
		var span trace.Span
		ctx, span = r.tracer.Start(ctx, "weaver.subagent.bridge",
			trace.WithAttributes(
				attribute.String("domain", domain),
				attribute.String("backend", agent.Backend),
				attribute.String("query_id", queryID),
			),
		)
		defer span.End()
	}

	if err := agent.Validate(); err != nil {
		sublog.Warn("weaver: subagent validation failed", "error", err)
		return DomainResult{
			Domain:    domain,
			Error:     err.Error(),
			LatencyMs: time.Since(start).Milliseconds(),
		}
	}

	bridge := r.spawnBridge
	if bridge == nil {
		bridge = NoopSpawnBridge{}
	}

	result, err := bridge.Dispatch(ctx, agent, req.Query, req.ParentSessionID, queryID)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		sublog.Warn("weaver: spawn bridge dispatch failed", "error", err, "latency_ms", latencyMs)
		return DomainResult{
			Domain:    domain,
			Error:     err.Error(),
			LatencyMs: latencyMs,
		}
	}

	sublog.Debug("weaver: spawn bridge dispatch ok",
		"spawn_id", result.SpawnID,
		"stop_reason", result.StopReason,
		"tool_calls", result.ToolCalls,
		"total_cost_usd", result.TotalCostUSD,
		"latency_ms", latencyMs,
	)

	return DomainResult{
		Domain:    domain,
		Answer:    result.LastMessage,
		Tokens:    result.Tokens,
		LatencyMs: latencyMs,
	}
}

// synthesize combines results from multiple domains into a single answer.
func (r *Router) synthesize(ctx context.Context, results []DomainResult, query string, logger *slog.Logger) string {
	if r.tracer != nil {
		var span trace.Span
		ctx, span = r.tracer.Start(ctx, "weaver.synthesize",
			trace.WithAttributes(attribute.Int("result_count", len(results))),
		)
		defer span.End()
	}

	// If only one domain with a result, return it directly.
	var successResults []DomainResult
	for _, dr := range results {
		if dr.Error == "" && dr.Answer != "" {
			successResults = append(successResults, dr)
		}
	}

	if len(successResults) == 0 {
		return "No results from any domain."
	}
	if len(successResults) == 1 {
		return successResults[0].Answer
	}

	// Multiple domains: use the router model to synthesize.
	var input string
	for _, dr := range successResults {
		input += fmt.Sprintf("## %s\n%s\n\n", dr.Domain, dr.Answer)
	}

	prompt := routerSynthesizePrompt()
	userMsg := fmt.Sprintf("Original query: %s\n\nDomain results:\n%s", query, input)

	synthCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	synth, err := r.client.CompleteSimple(synthCtx, r.cfg.RouterModel, prompt, r.applyModelPrefix(r.cfg.RouterModel, userMsg), 1024)
	if err != nil {
		logger.Warn("synthesis failed, returning concatenated results", "error", err)
		return input
	}
	return synth
}

// History returns a copy of the query history buffer (most recent last).
func (r *Router) History() []QueryHistoryEntry {
	r.historyMu.Lock()
	defer r.historyMu.Unlock()
	out := make([]QueryHistoryEntry, len(r.history))
	copy(out, r.history)
	return out
}

// recordHistory appends a query history entry, evicting old entries when
// the buffer exceeds maxHistoryEntries.
func (r *Router) recordHistory(entry QueryHistoryEntry) {
	r.historyMu.Lock()
	defer r.historyMu.Unlock()
	r.history = append(r.history, entry)
	if len(r.history) > maxHistoryEntries {
		r.history = r.history[len(r.history)-maxHistoryEntries:]
	}
	// Update lifetime metrics.
	if r.metrics != nil {
		r.metrics.RecordQuery(entry.Status, entry.LatencyMs, entry.TotalTokens)
	}
}

// applyModelPrefix prepends any model-specific prefix to a user message.
// Used for classify() and synthesize() calls that go through CompleteSimple
// (which no longer applies model-specific prefixes itself).
func (r *Router) applyModelPrefix(model, msg string) string {
	if b, ok := FindModelBehavior(r.cfg.ModelBehaviors, model); ok && b.UserMessagePrefix != "" {
		return b.UserMessagePrefix + msg
	}
	return msg
}

func (r *Router) recordQuery(start time.Time, status string) {
	if r.metrics == nil {
		return
	}
	r.metrics.QueriesTotal.WithLabelValues(status).Inc()
	r.metrics.QueryDuration.WithLabelValues(status).Observe(time.Since(start).Seconds())
}

// F10 auto-compose wiring (append-only).

// autoComposeMetrics is an optional sink for auto-compose outcome counters.
// Kept as a router-level field via accessor to remain append-only.
var routerAutoComposeMetrics = make(map[*Router]*AutoComposeMetrics)
var routerAutoComposeMu sync.Mutex

// SetAutoComposeMetrics attaches an AutoComposeMetrics sink to the router.
// Safe to call once at startup; subsequent calls replace the sink.
func (r *Router) SetAutoComposeMetrics(m *AutoComposeMetrics) {
	routerAutoComposeMu.Lock()
	defer routerAutoComposeMu.Unlock()
	routerAutoComposeMetrics[r] = m
}

func (r *Router) autoComposeMetrics() *AutoComposeMetrics {
	routerAutoComposeMu.Lock()
	defer routerAutoComposeMu.Unlock()
	return routerAutoComposeMetrics[r]
}

// TryAutoCompose runs the auto-compose fallback if enabled.
// Returns (result, true, nil) when auto-compose ran and produced a result
// (possibly empty if no domains scored). Returns (_, false, nil) when
// auto-compose is disabled. A dispatch error yields (_, true, err).
func (r *Router) TryAutoCompose(ctx context.Context, query string) (QueryResult, bool, error) {
	if !AutoComposeEnabled() {
		return QueryResult{}, false, nil
	}

	picked := selectDomains(r.registry.List(), query, AutoComposeMaxDomains())
	if len(picked) == 0 {
		if m := r.autoComposeMetrics(); m != nil {
			m.RecordOutcome("empty")
		}
		return QueryResult{}, true, nil
	}

	// Refused bookkeeping: count any domains skipped because of Write=true.
	refused := 0
	for _, d := range r.registry.List() {
		if d.Write {
			refused++
		}
	}
	if m := r.autoComposeMetrics(); m != nil && refused > 0 {
		m.RecordOutcome("refused")
	}

	// Auto-compose path has no dedicated query ID; generate a short tag so
	// any spawn-bridge dispatches emitted under auto-compose still have a
	// stable correlation identifier for HUD display.
	acQueryID := "ac-" + uuid.New().String()[:6]
	result, err := r.dispatch(ctx, picked, QueryRequest{Query: query, Domains: picked}, acQueryID, r.logger.With("auto_compose", true))
	if err != nil {
		return QueryResult{}, true, err
	}

	if m := r.autoComposeMetrics(); m != nil {
		m.RecordOutcome("success")
		m.RecordDomainsUsed(len(picked))
	}
	return result, true, nil
}

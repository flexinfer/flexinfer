package weaver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	Query       string    `json:"query"`
	Domains     []string  `json:"domains"`
	Status      string    `json:"status"`
	LatencyMs   int64     `json:"latency_ms"`
	TotalTokens int       `json:"total_tokens"`
}

// Router orchestrates multi-domain queries using local FlexInfer models.
type Router struct {
	cfg      Config
	client   *flexinfer.Client
	executor *DaemonToolExecutor
	lister   ToolLister
	registry *DomainRegistry
	metrics  *Metrics
	tracer   trace.Tracer
	logger   *slog.Logger

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
		cfg:      cfg,
		client:   client,
		executor: executor,
		lister:   lister,
		registry: reg,
		logger:   logger.With("component", "weaver-router"),
	}
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

// Query executes an orchestrated query, optionally classifying the domain first.
func (r *Router) Query(ctx context.Context, req QueryRequest) (QueryResult, error) {
	start := time.Now()

	if r.tracer != nil {
		var span trace.Span
		ctx, span = r.tracer.Start(ctx, "weaver.query",
			trace.WithAttributes(attribute.String("query", req.Query)),
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
		classified, err := r.classify(ctx, req.Query)
		if err != nil {
			r.recordQuery(start, "error")
			r.recordHistory(QueryHistoryEntry{
				Timestamp: start, Query: req.Query, Domains: domains,
				Status: "error", LatencyMs: time.Since(start).Milliseconds(),
			})
			return QueryResult{}, fmt.Errorf("classify: %w", err)
		}
		domains = classified
	}

	if len(domains) == 0 {
		r.recordQuery(start, "no_match")
		r.recordHistory(QueryHistoryEntry{
			Timestamp: start, Query: req.Query,
			Status: "no_match", LatencyMs: time.Since(start).Milliseconds(),
		})
		return QueryResult{
			Answer:    "No matching domains found for this query.",
			LatencyMs: time.Since(start).Milliseconds(),
		}, nil
	}

	result, err := r.dispatch(ctx, domains, req)
	if err != nil {
		r.recordQuery(start, "error")
		r.recordHistory(QueryHistoryEntry{
			Timestamp: start, Query: req.Query, Domains: domains,
			Status: "error", LatencyMs: time.Since(start).Milliseconds(),
		})
		return QueryResult{}, err
	}

	result.LatencyMs = time.Since(start).Milliseconds()
	r.recordQuery(start, "ok")
	r.recordHistory(QueryHistoryEntry{
		Timestamp: start, Query: req.Query, Domains: domains,
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
	domains := r.registry.Names()
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
func (r *Router) classify(ctx context.Context, query string) ([]string, error) {
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
		r.logger.Warn("failed to parse classification response", "response", resp, "error", err)
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

	r.logger.Debug("classified query", "query", query, "domains", valid)
	return valid, nil
}

// dispatch runs subagents for the specified domains in parallel.
func (r *Router) dispatch(ctx context.Context, domains []string, req QueryRequest) (QueryResult, error) {
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

			dr := r.runSubAgent(subCtx, agent, req)

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
	answer := r.synthesize(ctx, results, req.Query)

	return QueryResult{
		Answer:        answer,
		DomainResults: results,
		TotalTokens:   totalTokens,
		DomainsUsed:   domainsUsed,
	}, nil
}

// runSubAgent executes the orchestration loop for a single domain.
func (r *Router) runSubAgent(ctx context.Context, agent SubAgent, req QueryRequest) DomainResult {
	start := time.Now()
	domain := agent.Name

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
		r.logger.Debug("token budget adjusted iterations",
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

	responsesClient := NewFlexInferResponsesClient(r.client, r.cfg.ModelBehaviors, r.logger)
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
		r.logger.Warn("subagent failed", "domain", domain, "error", err, "latency_ms", latencyMs)
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

// synthesize combines results from multiple domains into a single answer.
func (r *Router) synthesize(ctx context.Context, results []DomainResult, query string) string {
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

	synth, err := r.client.CompleteSimple(ctx, r.cfg.RouterModel, prompt, r.applyModelPrefix(r.cfg.RouterModel, userMsg), 1024)
	if err != nil {
		r.logger.Warn("synthesis failed, returning concatenated results", "error", err)
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

package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// WeaverHTTPConfig is the connection config for the routed weaver
// dispatch. The endpoint is the loom HUD's POST /api/weaver/query (see
// internal/hud/domain/weaver/handlers.go), which proxies to the
// daemon's loom/weaver/query JSON-RPC. The Mills operator runs as a
// pod inside the cluster and reaches the HUD via its ClusterIP
// service.
//
// Token is optional: the GET /api/weaver/* surface and the new POST
// share the HUD's withCORS middleware, which does not require a bearer
// today (mobile_operator tokens are restricted to /api/mobile/v1).
// Future hardening can require a token; the field is plumbed now to
// keep the wire format stable.
type WeaverHTTPConfig struct {
	BaseURL string        // e.g. http://mobile-hud.loom-hub.svc.cluster.local
	Token   string        // optional bearer
	Timeout time.Duration // per-request timeout, defaults to 60s
	AgentID string        // forwarded as agent_id; identifies the caller
}

// WeaverHTTPDelegator is the production WeaverDelegator that issues a
// routed multi-domain weaver query against the HUD's HTTP surface. It
// satisfies the WeaverDelegator interface in flexinfer.go.
//
// The wire shape is documented in handleQuery (request) and
// pkg/weaver.QueryResult (response). On success, the HUD body is
// mapped onto pipeline.WeaverResponse:
//
//	Notes        ← QueryResult.Answer
//	Citation     ← {domains_used, total_tokens, latency_ms, weaver_endpoint}
//	SpawnID      ← "weaver-router" (the multi-domain answer doesn't
//	               have a single backing model id; the citation carries
//	               the domain breakdown)
//	CostUSD      ← 0 (cost is not reported by the router today; will be
//	               populated when QueryResult grows a CostUSD field)
//	LogTail      ← short summary of per-domain answer counts so
//	               operators can spot empty-domain regressions in logs.
type WeaverHTTPDelegator struct {
	cfg    WeaverHTTPConfig
	http   *httpclient.Client
	logger *slog.Logger
}

// NewWeaverHTTPDelegator validates the config and returns a ready
// delegator. BaseURL is required; everything else has sane defaults.
func NewWeaverHTTPDelegator(cfg WeaverHTTPConfig) (*WeaverHTTPDelegator, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("weaver delegator: BaseURL required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	hcfg := httpclient.DefaultConfig()
	hcfg.Timeout = cfg.Timeout
	c := httpclient.New(hcfg)
	return &WeaverHTTPDelegator{
		cfg:    cfg,
		http:   c,
		logger: slog.Default().With("component", "mills-weaver-delegator"),
	}, nil
}

// SetTransport is for tests: overrides the underlying RoundTripper so
// test cases can serve canned responses without binding a port.
func (d *WeaverHTTPDelegator) SetTransport(rt http.RoundTripper) {
	d.http.HTTP().Transport = rt
}

// SetLogger overrides the default slog logger.
func (d *WeaverHTTPDelegator) SetLogger(l *slog.Logger) {
	if l != nil {
		d.logger = l
	}
}

// queryRequestBody is the shape POST /api/weaver/query consumes. Mirror
// of the daemon's handleWeaverQuery params.
type queryRequestBody struct {
	Query           string `json:"query"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
}

// queryResultBody mirrors pkg/weaver.QueryResult. Kept as a local typed
// struct so we don't pull pkg/weaver into the mills client side (the
// router code drags in OpenAI types, instrumentation, and the spawn
// bridge — none of which the operator needs).
type queryResultBody struct {
	Answer        string                  `json:"answer"`
	DomainResults []queryDomainResultBody `json:"domain_results"`
	TotalTokens   int                     `json:"total_tokens"`
	LatencyMs     int64                   `json:"latency_ms"`
	DomainsUsed   []string                `json:"domains_used"`
}

type queryDomainResultBody struct {
	Domain    string `json:"domain"`
	Answer    string `json:"answer"`
	Tokens    int    `json:"tokens"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// Delegate satisfies WeaverDelegator. Returns a non-nil error on
// transport failures, non-2xx status codes, and invalid JSON; the
// WeaverClient logic in flexinfer.go falls back to the legacy chat in
// "on" mode and records a delegate_error in "shadow" mode.
//
// The session_id surfaced to the HUD is derived from req.RunID (the
// pipeline_runs.id) so weaver query history can be joined to the
// originating pipeline run by the agent-context recorder.
func (d *WeaverHTTPDelegator) Delegate(ctx context.Context, req pipeline.WeaverRequest) (pipeline.WeaverResponse, error) {
	if d == nil || d.http == nil {
		return pipeline.WeaverResponse{}, errors.New("weaver delegator: not configured")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return pipeline.WeaverResponse{}, errors.New("weaver delegator: empty prompt")
	}

	body := queryRequestBody{
		Query:           req.Prompt,
		AgentID:         d.cfg.AgentID,
		SessionID:       req.RunID,
		ParentSessionID: req.RunID,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return pipeline.WeaverResponse{}, fmt.Errorf("weaver delegator: marshal: %w", err)
	}

	url := strings.TrimRight(d.cfg.BaseURL, "/") + "/api/weaver/query"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return pipeline.WeaverResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if d.cfg.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+d.cfg.Token)
	}

	resp, err := d.http.Do(httpReq)
	if err != nil {
		return pipeline.WeaverResponse{}, fmt.Errorf("weaver delegator: POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Read a bounded snippet so a giant error page can't pin the
		// caller. The HUD's WriteError emits a small JSON envelope; 4
		// KB is plenty.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return pipeline.WeaverResponse{}, fmt.Errorf(
			"weaver delegator: POST status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(snippet)),
		)
	}

	var result queryResultBody
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return pipeline.WeaverResponse{}, fmt.Errorf("weaver delegator: decode: %w", err)
	}

	return pipeline.WeaverResponse{
		SpawnID: "weaver-router",
		Notes:   result.Answer,
		LogTail: summarizeDomainResults(result.DomainResults),
		Citation: map[string]any{
			"domains_used":    result.DomainsUsed,
			"domain_results":  result.DomainResults,
			"total_tokens":    result.TotalTokens,
			"latency_ms":      result.LatencyMs,
			"weaver_endpoint": url,
			"backlog_id":      req.BacklogID,
			"run_id":          req.RunID,
		},
	}, nil
}

// summarizeDomainResults builds a short one-line summary the operator
// can grep in logs without parsing JSON. Format:
//
//	"weaver: 3 domains (codebase=210t, ci-pipeline=180t, cluster-ops=0t err='router off')"
func summarizeDomainResults(results []queryDomainResultBody) string {
	if len(results) == 0 {
		return "weaver: 0 domains"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "weaver: %d domains (", len(results))
	for i, r := range results {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%dt", r.Domain, r.Tokens)
		if r.Error != "" {
			fmt.Fprintf(&b, " err=%q", r.Error)
		}
	}
	b.WriteString(")")
	return b.String()
}

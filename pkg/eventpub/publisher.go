// Package eventpub provides an HTTP-based implementation of the
// pkg/agentcontext.Publisher contract. Out-of-process MCP servers
// (mcp-agent-context, mcp-codebase-memory, future siblings) use it to
// republish their lifecycle events into the loom daemon EventBus, which
// then fans them out via the /events SSE endpoint.
//
// Publish is best-effort and non-blocking: failures log but do not propagate
// so a slow or down daemon never stalls a tool handler on the hot path.
package eventpub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// publishTimeout caps each POST to keep tool handlers fast even when the
// daemon is slow. Events are advisory; dropping a few is preferable to
// blocking the caller.
const publishTimeout = 2 * time.Second

// HTTPPublisher posts {type, data} JSON envelopes to a loom daemon's
// /events/publish endpoint. Safe for concurrent use.
type HTTPPublisher struct {
	url        string
	adminToken string
	client     *http.Client
	logger     *slog.Logger
}

// NewHTTPPublisher constructs a publisher targeting the given daemon URL
// (typically discovered via the LOOM_DAEMON_HTTP_URL env var). The url should
// not include the /events/publish path; the publisher appends it. adminToken
// is forwarded as a Bearer header when non-empty; the daemon decides whether
// to require it. logger nil falls back to slog.Default().
func NewHTTPPublisher(url, adminToken string, logger *slog.Logger) *HTTPPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &HTTPPublisher{
		url:        strings.TrimRight(url, "/"),
		adminToken: adminToken,
		client:     &http.Client{Timeout: publishTimeout},
		logger:     logger,
	}
}

// envelope is the JSON body shape consumed by daemon.HandleEventsPublish.
type envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Publish satisfies the agentcontext.Publisher interface. Errors are logged
// at debug level so a chronically-down daemon does not flood logs.
func (p *HTTPPublisher) Publish(eventType string, payload any) {
	body, err := json.Marshal(envelope{Type: eventType, Data: payload})
	if err != nil {
		p.logger.Debug("eventpub: marshal failed", "type", eventType, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/events/publish", bytes.NewReader(body))
	if err != nil {
		p.logger.Debug("eventpub: request build failed", "type", eventType, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if p.adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.adminToken)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Debug("eventpub: POST failed", "type", eventType, "url", p.url, "error", err)
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		p.logger.Debug("eventpub: daemon refused event",
			"type", eventType, "status", resp.StatusCode)
		return
	}
}

// URL returns the configured daemon URL (without the /events/publish suffix).
// Useful for tests + diagnostic output.
func (p *HTTPPublisher) URL() string { return p.url }

// Ping does a single best-effort POST of a synthetic envelope to probe
// connectivity. Returns nil on 2xx, error otherwise. Not called in production
// hot paths; intended for startup self-check + tests.
func (p *HTTPPublisher) Ping(ctx context.Context) error {
	body, _ := json.Marshal(envelope{Type: "eventpub.ping", Data: map[string]string{"src": "eventpub.Ping"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/events/publish", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.adminToken)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("eventpub: daemon refused ping with status %d", resp.StatusCode)
	}
	return nil
}

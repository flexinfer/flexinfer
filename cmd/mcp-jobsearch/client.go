package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/strutil"
)

type requestOptions struct {
	Method           string
	Path             string
	Query            map[string]string
	Payload          any
	MaxBytes         int
	ExpectedStatuses []int
}

type jobsearchResponse struct {
	StatusCode  int
	ContentType string
	Headers     map[string]any
	Data        any
	Truncated   bool
}

type apiCaller interface {
	Request(ctx context.Context, opts requestOptions) (*jobsearchResponse, error)
}

type jobsearchClient struct {
	baseURL               string
	token                 string
	cfAccessClientID      string
	cfAccessClientSecret  string
	hasCloudflareAccess   bool
	httpClient            *httpclient.Client
	logger                *slog.Logger
	maxResponseBytes      int
	defaultTimeoutSeconds int
}

func newJobsearchClientFromEnv(logger *slog.Logger) (*jobsearchClient, error) {
	baseURL := strings.TrimSpace(env.String("JOBSEARCH_API_URL", ""))
	if baseURL == "" {
		return nil, mcperror.NotConfigured("JOBSEARCH_API_URL", "set JOBSEARCH_API_URL environment variable")
	}

	token := strings.TrimSpace(env.StringWithFallbacks("JOBSEARCH_API_TOKEN", "JOBSEARCH_BEARER_TOKEN"))
	if token == "" {
		return nil, mcperror.NotConfigured("JOBSEARCH_API_TOKEN", "set JOBSEARCH_API_TOKEN or JOBSEARCH_BEARER_TOKEN")
	}

	timeoutSeconds := env.Int("JOBSEARCH_TIMEOUT_SECONDS", 30)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	maxResponseBytes := env.Int("JOBSEARCH_MAX_RESPONSE_BYTES", 2*1024*1024)
	if maxResponseBytes <= 0 {
		maxResponseBytes = 2 * 1024 * 1024
	}

	cfID := strings.TrimSpace(env.StringWithFallbacks("JOBSEARCH_CF_ACCESS_CLIENT_ID", "CF_ACCESS_CLIENT_ID"))
	cfSecret := strings.TrimSpace(env.StringWithFallbacks("JOBSEARCH_CF_ACCESS_CLIENT_SECRET", "CF_ACCESS_CLIENT_SECRET"))

	hasCF := cfID != "" && cfSecret != ""
	if (cfID == "") != (cfSecret == "") {
		logger.Warn("partial Cloudflare Access configuration detected; headers will not be injected", "has_client_id", cfID != "", "has_client_secret", cfSecret != "")
	}

	hc := httpclient.New(httpclient.Config{
		Timeout:          time.Duration(timeoutSeconds) * time.Second,
		MaxRetries:       2,
		RetryBaseDelay:   150 * time.Millisecond,
		RetryMaxDelay:    2 * time.Second,
		MaxResponseBytes: maxResponseBytes,
	})

	return &jobsearchClient{
		baseURL:               strings.TrimSuffix(baseURL, "/"),
		token:                 token,
		cfAccessClientID:      cfID,
		cfAccessClientSecret:  cfSecret,
		hasCloudflareAccess:   hasCF,
		httpClient:            hc,
		logger:                logger,
		maxResponseBytes:      maxResponseBytes,
		defaultTimeoutSeconds: timeoutSeconds,
	}, nil
}

func (c *jobsearchClient) Request(ctx context.Context, opts requestOptions) (*jobsearchResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	if method == "" {
		method = http.MethodGet
	}

	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return nil, mcperror.InvalidParam("path", "must not be empty")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, mcperror.InvalidParam("path", fmt.Sprintf("invalid URL path: %v", err))
	}
	if len(opts.Query) > 0 {
		q := u.Query()
		for k, v := range opts.Query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	var reqBody []byte
	if opts.Payload != nil {
		reqBody, err = json.Marshal(opts.Payload)
		if err != nil {
			return nil, mcperror.InvalidParam("payload", fmt.Sprintf("must be JSON-serializable: %v", err))
		}
	}

	var bodyReader *bytes.Reader
	if len(reqBody) > 0 {
		bodyReader = bytes.NewReader(reqBody)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return nil, mcperror.WrapAPI("JobSearch", err)
	}

	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "mcp-jobsearch/"+version)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if len(reqBody) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.hasCloudflareAccess {
		req.Header.Set("CF-Access-Client-Id", c.cfAccessClientID)
		req.Header.Set("CF-Access-Client-Secret", c.cfAccessClientSecret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, mcperror.WrapAPI("JobSearch", err)
	}
	defer resp.Body.Close()

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = c.maxResponseBytes
	}
	body, truncated, err := httpclient.ReadBodyWithLimit(resp.Body, maxBytes)
	if err != nil {
		return nil, mcperror.WrapAPI("JobSearch", err)
	}

	if !isExpectedStatus(resp.StatusCode, opts.ExpectedStatuses) {
		return nil, mcperror.APIError("JobSearch", resp.StatusCode, strutil.TruncateNoEllipsis(string(body), 8192))
	}

	parsed := parseResponseBody(resp.Header.Get("Content-Type"), body)

	return &jobsearchResponse{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Headers:     collectUsefulHeaders(resp.Header),
		Data:        parsed,
		Truncated:   truncated,
	}, nil
}

func isExpectedStatus(statusCode int, expected []int) bool {
	if len(expected) == 0 {
		return statusCode >= 200 && statusCode < 300
	}
	for _, code := range expected {
		if statusCode == code {
			return true
		}
	}
	return false
}

func collectUsefulHeaders(h http.Header) map[string]any {
	keys := []string{
		"Content-Type",
		"X-Total",
		"X-Total-Pages",
		"X-Page",
		"X-Per-Page",
		"Retry-After",
	}
	out := map[string]any{}
	for _, key := range keys {
		if v := h.Get(key); v != "" {
			out[strings.ToLower(strings.ReplaceAll(key, "-", "_"))] = v
		}
	}
	return out
}

func parseResponseBody(contentType string, body []byte) any {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return map[string]any{}
	}

	looksJSON := strings.Contains(strings.ToLower(contentType), "json") || (trimmed[0] == '{' || trimmed[0] == '[')
	if looksJSON {
		var parsed any
		if err := json.Unmarshal(trimmed, &parsed); err == nil {
			return parsed
		}
	}

	return string(trimmed)
}

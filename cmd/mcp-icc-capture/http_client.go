package main

import (
	"log/slog"
	"strings"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
)

// iccClient holds configuration for talking to the ICC backend via
// HMAC-signed HTTP. This slice ships the struct + config wiring only —
// no methods land yet so we don't trigger unused-symbol lints. A
// future slice (different session) adds ensureConfigured / post /
// signed-POST helpers as soon as the first network-backed tool needs
// them. We keep the struct here so the next slice can dependency-inject
// a real or fake client without a wider refactor.
type iccClient struct {
	baseURL    string
	apiKey     string // HMAC key id
	apiSecret  string // HMAC shared secret
	httpClient *httpclient.Client
	logger     *slog.Logger
}

// newICCClient reads ICC_* env vars and returns a configured client. It
// never fails — missing config is fine for tools that don't talk to
// ICC (none currently do; see package doc).
func newICCClient(logger *slog.Logger) *iccClient {
	return &iccClient{
		baseURL:    strings.TrimSpace(env.String("ICC_API_URL", "")),
		apiKey:     strings.TrimSpace(env.StringWithFallbacks("ICC_API_KEY_ID", "ICC_API_KEY")),
		apiSecret:  strings.TrimSpace(env.StringWithFallbacks("ICC_API_SECRET", "ICC_API_KEY_SECRET")),
		httpClient: httpclient.NewDefault(),
		logger:     logger,
	}
}

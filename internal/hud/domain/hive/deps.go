// Package hive registers HUD-side proxy routes that forward /api/hive/*
// to the in-cluster loom-hive-operator's REST surface. The HUD does not
// embed the operator; it only proxies on behalf of the browser so the
// admin bearer token never leaves the HUD process.
//
// The proxy is graceful-degrading: when LOOM_HIVE_OPERATOR_URL is unset
// every route returns HTTP 503 with a clear "operator not configured"
// body so the frontend can render an empty-state instead of crashing.
package hive

import (
	"log/slog"
	"net/http"
)

// Deps exposes the subset of App capabilities the hive proxy needs. The
// hud.App satisfies this via accessors in domain_adapters.go.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	RequireAdminToken(w http.ResponseWriter, r *http.Request) bool
	Logger() *slog.Logger
	HiveConfig() Config
}

// Config carries the hive-specific runtime config: the operator REST URL
// (cluster-internal, e.g. http://loom-hive-operator.loom-hive.svc.cluster.local:8090)
// and the admin bearer token used for mutations.
//
// Both come from env at HUD boot:
//
//	LOOM_HIVE_OPERATOR_URL   — base URL, no trailing /api/hive
//	LOOM_HIVE_OPERATOR_TOKEN — bearer for POST routes (reads are open)
//
// When BaseURL is empty the proxy is disabled and every handler returns
// 503 — this is the steady state on developer laptops where the cluster
// operator isn't reachable.
type Config struct {
	BaseURL    string
	AdminToken string
}

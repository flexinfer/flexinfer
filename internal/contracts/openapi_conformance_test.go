// OpenAPI conformance tests.
//
// These tests validate that the hand-authored OpenAPI 3.0.3 spec at
// docs/api/openapi.yaml stays in sync with the Go types in
// internal/visibility/contracts. They do NOT touch a live HUD listener; the
// approach is fixture-vs-spec only. A representative fixture is built from
// the canonical Go type, marshaled to JSON, and validated against the
// response schema kin-openapi resolves for the matching path.
//
// /sessions and /tasks are intentionally skipped at the per-path level: their
// canonical types still live in internal/hud/bridge during S1 and the spec
// describes them with additionalProperties=true. They will be tightened in a
// later EPIC 2 (#66) slice when the lift completes. See docs/api/openapi.yaml
// for the matching note.
package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/stretchr/testify/require"

	catalogctr "github.com/crb2nu/loom/internal/visibility/contracts/catalog"
	costctr "github.com/crb2nu/loom/internal/visibility/contracts/cost"
	healthctr "github.com/crb2nu/loom/internal/visibility/contracts/health"
	presencectr "github.com/crb2nu/loom/internal/visibility/contracts/presence"
	rbacctr "github.com/crb2nu/loom/internal/visibility/contracts/rbac"
	statusctr "github.com/crb2nu/loom/internal/visibility/contracts/status"
)

const openapiSpecPath = "../../docs/api/openapi.yaml"

// loadSpec loads and validates the hand-authored spec. Failure here is a hard
// stop because every per-path test depends on a parseable spec.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	abs, err := filepath.Abs(openapiSpecPath)
	require.NoError(t, err, "resolve spec path")
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	spec, err := loader.LoadFromFile(abs)
	require.NoError(t, err, "load openapi.yaml")
	require.NoError(t, spec.Validate(loader.Context), "validate openapi.yaml")
	return spec
}

// validateResponse marshals the fixture, builds a fake request/response pair,
// and runs the kin-openapi response validator against the resolved route.
func validateResponse(t *testing.T, spec *openapi3.T, path string, fixture any) {
	t.Helper()

	body, err := json.Marshal(fixture)
	require.NoError(t, err, "marshal fixture for %s", path)

	router, err := gorillamux.NewRouter(spec)
	require.NoError(t, err, "build router")

	ctx := context.Background()
	url := "http://localhost:5052/api" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err, "build request")

	route, pathParams, err := router.FindRoute(req)
	require.NoError(t, err, "find route for %s", path)

	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
		Options: &openapi3filter.Options{
			IncludeResponseStatus: true,
		},
	}

	require.NoError(
		t,
		openapi3filter.ValidateResponse(ctx, respInput),
		"validate %s response against spec", path,
	)
}

// TestOpenAPISpecLoads is the load-and-validate gate. If this fails everything
// else is moot, so it runs first and isolates parse errors from schema drift.
func TestOpenAPISpecLoads(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)

	// Sanity: every documented path must resolve.
	wantPaths := []string{
		"/status", "/health", "/cost", "/rbac", "/catalog",
		"/sessions", "/tasks", "/presence", "/events",
	}
	for _, p := range wantPaths {
		require.NotNilf(t, spec.Paths.Value(p), "missing path %s in spec", p)
	}
}

func TestOpenAPIConformance_Status(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	fixture := statusctr.PlatformStatus{
		Daemon: statusctr.DaemonStatus{
			Running:             true,
			Servers:             3,
			ActiveConns:         2,
			IdleConns:           1,
			ActiveRPCs:          7,
			ActiveProxySessions: 0,
			DaemonEpoch:         1714000000,
			DrainReady:          false,
			Draining:            false,
			Processes:           []string{"daemon"},
		},
		Agents:    statusctr.AgentStatus{Active: 1, Idle: 0, Offline: 0, Total: 1},
		Sessions:  statusctr.SessionCount{Active: 1, Total: 4},
		Pipelines: statusctr.PipelineStatus{Available: true, Running: 0, Passed: 5, Failed: 0, Pending: 0},
		HUD:       statusctr.HUDStatus{Reachable: true},
		Healthy:   true,
	}
	validateResponse(t, spec, "/status", fixture)
}

func TestOpenAPIConformance_Health(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	fixture := healthctr.HealthResult{
		Servers: map[string]healthctr.ServerHealth{
			"loom-core": {
				Local:     healthctr.HealthEntry{Healthy: true, ConsecFails: 0, AvgLatencyMs: 4.2},
				Hub:       healthctr.HealthEntry{Healthy: true, ConsecFails: 0, AvgLatencyMs: 5.1},
				Target:    "ws://localhost:5051",
				Transport: "ws",
			},
		},
	}
	validateResponse(t, spec, "/health", fixture)
}

func TestOpenAPIConformance_Cost(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	fixture := costctr.CostStatsResult{
		Enabled:   true,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Totals: costctr.CostTotals{
			CallCount:     42,
			ErrorCount:    1,
			DeniedCount:   0,
			CachedCount:   3,
			TotalDuration: 1234,
		},
	}
	validateResponse(t, spec, "/cost", fixture)
}

func TestOpenAPIConformance_RBAC(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	fixture := rbacctr.Snapshot{
		PolicyVersion:  "v1",
		DeniedCount24h: 0,
		AuditEnabled:   true,
		SimulationMode: false,
	}
	validateResponse(t, spec, "/rbac", fixture)
}

func TestOpenAPIConformance_Catalog(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	fixture := catalogctr.Status{
		Servers: []catalogctr.Entry{
			{Name: "loom-core", Enabled: true, Description: "core MCP"},
		},
		LastSyncTime: time.Now().UTC(),
	}
	validateResponse(t, spec, "/catalog", fixture)
}

func TestOpenAPIConformance_Presence(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	fixture := []presencectr.PresenceInfo{
		{
			AgentID:       "claude-code",
			Status:        "active",
			AgentType:     "claude-code",
			Description:   "openapi conformance",
			CurrentTask:   "writing tests",
			ActiveFiles:   []string{"openapi_conformance_test.go"},
			Branch:        "docs/unify-2c-openapi-spec",
			WorktreeID:    "wt-1",
			LastHeartbeat: time.Now().UTC().Format(time.RFC3339),
			RegisteredAt:  time.Now().UTC().Format(time.RFC3339),
		},
	}
	validateResponse(t, spec, "/presence", fixture)
}

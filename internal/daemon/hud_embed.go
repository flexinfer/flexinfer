package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/spawn"
)

// startEmbeddedHUD initializes and starts the embedded HUD application,
// mounting its routes on the given HTTP mux. The HUD uses a LocalCaller
// that dispatches directly to d.handleMessage (in-process, no socket).
func (d *Daemon) startEmbeddedHUD(ctx context.Context, mux *http.ServeMux) error {
	cfg := d.fileCfg.EmbeddedHUD

	// Build the HUD config from the embedded HUD config section.
	// Environment variables are checked as fallbacks for K8s deployment
	// where secrets are injected as env vars.
	hudCfg := hud.Config{
		RegistryPath:                      firstNonEmpty(d.cfg.RegistryPath, os.Getenv("LOOM_REGISTRY_PATH"), os.Getenv("LOOM_REGISTRY")),
		AdminToken:                        firstNonEmpty(cfg.AdminToken, os.Getenv("LOOM_HUD_ADMIN_TOKEN"), os.Getenv("HUD_ADMIN_TOKEN")),
		MobileOperatorToken:               firstNonEmpty(cfg.MobileOperatorToken, os.Getenv("HUD_MOBILE_OPERATOR_TOKEN")),
		MobileOperatorScopes:              firstNonEmpty(cfg.MobileOperatorScopes, os.Getenv("HUD_MOBILE_OPERATOR_SCOPES")),
		SpawnEnabled:                      cfg.SpawnEnabled || os.Getenv("SPAWN_ENABLED") == "true",
		SpawnNamespace:                    firstNonEmpty(cfg.SpawnNamespace, os.Getenv("SPAWN_NAMESPACE")),
		SpawnRegistry:                     firstNonEmpty(cfg.SpawnRegistry, os.Getenv("SPAWN_REGISTRY")),
		SpawnSyncMode:                     firstNonEmpty(cfg.SpawnSyncMode, os.Getenv("SPAWN_SYNC_MODE")),
		SpawnGitBaseURL:                   firstNonEmpty(cfg.SpawnGitBaseURL, os.Getenv("SPAWN_GIT_BASE_URL")),
		SpawnGitSecret:                    firstNonEmpty(cfg.SpawnGitSecret, os.Getenv("SPAWN_GIT_SECRET")),
		SpawnGitCloneImage:                firstNonEmpty(cfg.SpawnGitCloneImage, os.Getenv("SPAWN_GIT_CLONE_IMAGE")),
		SpawnProjects:                     firstNonEmpty(cfg.SpawnProjects, os.Getenv("SPAWN_PROJECTS")),
		SpawnDefaultCPU:                   firstPositiveFloat(cfg.SpawnDefaultCPU, os.Getenv("SPAWN_DEFAULT_CPU")),
		SpawnDefaultMemory:                firstPositiveInt(cfg.SpawnDefaultMemory, os.Getenv("SPAWN_DEFAULT_MEMORY_MB")),
		SpawnMaxConcurrent:                firstPositiveInt(cfg.SpawnMaxConcurrent, os.Getenv("SPAWN_MAX_CONCURRENT")),
		SpawnMaxConcurrentBuilds:          firstPositiveInt(cfg.SpawnMaxConcurrentBuilds, os.Getenv("SPAWN_MAX_CONCURRENT_BUILDS")),
		SpawnBuildCPURequest:              firstNonEmpty(cfg.SpawnBuildCPURequest, os.Getenv("SPAWN_BUILD_CPU_REQUEST")),
		SpawnBuildCPULimit:                firstNonEmpty(cfg.SpawnBuildCPULimit, os.Getenv("SPAWN_BUILD_CPU_LIMIT")),
		SpawnBuildMemoryRequest:           firstNonEmpty(cfg.SpawnBuildMemoryRequest, os.Getenv("SPAWN_BUILD_MEMORY_REQUEST")),
		SpawnBuildMemoryLimit:             firstNonEmpty(cfg.SpawnBuildMemoryLimit, os.Getenv("SPAWN_BUILD_MEMORY_LIMIT")),
		SpawnBuildEphemeralStorageRequest: firstNonEmpty(cfg.SpawnBuildEphemeralStorageRequest, os.Getenv("SPAWN_BUILD_EPHEMERAL_STORAGE_REQUEST")),
		SpawnBuildEphemeralStorageLimit:   firstNonEmpty(cfg.SpawnBuildEphemeralStorageLimit, os.Getenv("SPAWN_BUILD_EPHEMERAL_STORAGE_LIMIT")),
		SpawnBuildAvoidNodes:              firstNonEmpty(cfg.SpawnBuildAvoidNodes, os.Getenv("SPAWN_BUILD_AVOID_NODES")),
		PipelineProjects:                  firstNonEmpty(cfg.PipelineProjects, os.Getenv("HUD_PIPELINE_PROJECTS")),
		BindAddress:                       firstNonEmpty(cfg.BindAddress, os.Getenv("HUD_BIND_ADDRESS")),
		FlexInferURL:                      firstNonEmpty(cfg.FlexInferURL, os.Getenv("FLEXINFER_URL")),
		FlexInferKey:                      firstNonEmpty(cfg.FlexInferKey, os.Getenv("FLEXINFER_API_KEY")),
		CoordinatorModel:                  firstNonEmpty(cfg.CoordinatorModel, os.Getenv("COORDINATOR_MODEL")),
		MillsOperatorURL:                  os.Getenv("LOOM_MILLS_OPERATOR_URL"),
		MillsOperatorToken:                os.Getenv("LOOM_MILLS_OPERATOR_TOKEN"),

		// Inbound webhook intake (GitLab/GitHub CI failure routing).
		// File config wins; envs are fallbacks for K8s secret injection.
		// `WEBHOOK_INBOUND_ENABLED=true` flips the gate; secrets are HMAC
		// verifiers, not bearer tokens, and are required when the
		// originating system signs its requests (always on for GitLab,
		// recommended for GitHub).
		WebhookInboundEnabled: cfg.WebhookInboundEnabled || envBoolTrue(os.Getenv("WEBHOOK_INBOUND_ENABLED")),
		WebhookGitLabSecret:   firstNonEmpty(cfg.WebhookGitLabSecret, os.Getenv("WEBHOOK_GITLAB_SECRET")),
		WebhookGitHubSecret:   firstNonEmpty(cfg.WebhookGitHubSecret, os.Getenv("WEBHOOK_GITHUB_SECRET")),
	}

	// Default mobile operator scopes when token is set.
	if hudCfg.MobileOperatorToken != "" && hudCfg.MobileOperatorScopes == "" {
		hudCfg.MobileOperatorScopes = "mobile:read,mobile:session:create,mobile:session:end,mobile:push"
	}

	// Create an in-process caller that dispatches directly to the daemon.
	caller := bridge.NewLocalCaller(d.handleMessage)

	logger := d.logger.With("component", "embedded-hud")
	app, err := hud.NewApp(hudCfg, caller, logger)
	if err != nil {
		return err
	}

	if err := app.StartMonitors(ctx); err != nil {
		return err
	}
	app.RegisterRoutes(mux)
	d.hudApp = app

	// Refresh snapshots in the background so slow monitor warm-up does not block
	// route registration and the HUD HTTP listener from becoming probeable.
	go app.RefreshMonitors()

	// Wire the weaver→spawn bridge: when the HUD's spawn orchestrator is
	// enabled AND the weaver router is up, let weaver dispatch SubAgents
	// with non-flexinfer Backend values to real headless-agent pods.
	// Safe to call when either side is missing — SetSpawnBridge(nil)
	// reverts to the noop default, and we only call it when both are set.
	if sp := app.SpawnOrchestrator(); sp != nil && d.weaver != nil {
		bridge := NewDaemonSpawnBridge(sp, d.logger, 0)
		d.weaver.SetSpawnBridge(bridge)
		logger.Info("weaver spawn bridge wired",
			"router_configured", true,
			"spawn_enabled", true)
	}

	// Sessions endpoint: joins proxy sessions (daemon) with spawns
	// (HUD) via parent_session_id metadata for HUD observability.
	mux.HandleFunc("GET /api/hud/sessions", d.handleHUDSessions)

	logger.Info("embedded HUD started",
		"spawn", hudCfg.SpawnEnabled,
		"mobile", hudCfg.MobileOperatorToken != "",
		"coordinator", hudCfg.FlexInferURL != "")

	return nil
}

// hudSessionsResponse is the shape of GET /api/hud/sessions.
type hudSessionsResponse struct {
	DaemonEpoch int64                `json:"daemon_epoch"`
	Draining    bool                 `json:"draining"`
	Sessions    []hudSessionWithKids `json:"sessions"`
}

// hudSessionWithKids folds a proxy session together with the spawns that
// were launched under it (joined by LOOM_PARENT_SESSION_ID, set in
// Request.ParentSessionID by Slice 1). Lets the HUD render a single row
// per session with a nested list of its children.
type hudSessionWithKids struct {
	ID              string               `json:"id"`
	AgentHint       string               `json:"agent_hint,omitempty"`
	PresenceAgentID string               `json:"presence_agent_id,omitempty"`
	DaemonEpoch     int64                `json:"daemon_epoch"`
	State           string               `json:"state"`
	CreatedAt       time.Time            `json:"created_at"`
	LastSeenAt      time.Time            `json:"last_seen_at"`
	LeaseExpires    time.Time            `json:"lease_expires"`
	Spawns          []hudSessionSpawnRef `json:"spawns,omitempty"`
}

type hudSessionSpawnRef struct {
	SpawnID       string `json:"spawn_id"`
	AgentID       string `json:"agent_id"`
	AgentType     string `json:"agent_type"`
	Status        string `json:"status"`
	WeaverQueryID string `json:"weaver_query_id,omitempty"`
	WeaverDomain  string `json:"weaver_domain,omitempty"`
}

// handleHUDSessions renders the current proxy-session fleet with their
// spawns joined in. Read-only, no scope enforcement beyond what the
// surrounding auth middleware already applies to /api/hud/*.
func (d *Daemon) handleHUDSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if d.sessions == nil {
		_ = json.NewEncoder(w).Encode(hudSessionsResponse{DaemonEpoch: d.daemonEpoch})
		return
	}

	var spawns []*spawn.State
	if d.hudApp != nil {
		if sp := d.hudApp.SpawnOrchestrator(); sp != nil {
			spawns = sp.ListSpawns()
		}
	}

	// Group spawns by parent session id (empty-string bucket excluded below).
	spawnsByParent := make(map[string][]hudSessionSpawnRef, len(spawns))
	for _, s := range spawns {
		parent := s.Request.ParentSessionID
		if parent == "" {
			continue
		}
		spawnsByParent[parent] = append(spawnsByParent[parent], hudSessionSpawnRef{
			SpawnID:       s.SpawnID,
			AgentID:       s.AgentID,
			AgentType:     s.Request.AgentType,
			Status:        string(s.Status),
			WeaverQueryID: s.Request.Metadata["weaver_query_id"],
			WeaverDomain:  s.Request.Metadata["weaver_domain"],
		})
	}

	snap := d.sessions.Snapshot()
	out := hudSessionsResponse{
		DaemonEpoch: d.daemonEpoch,
		Draining:    d.draining.Load(),
		Sessions:    make([]hudSessionWithKids, 0, len(snap)),
	}
	for _, sess := range snap {
		out.Sessions = append(out.Sessions, hudSessionWithKids{
			ID:              sess.ID,
			AgentHint:       sess.ClientInfo.AgentHint,
			PresenceAgentID: sess.ClientInfo.PresenceAgentID,
			DaemonEpoch:     sess.DaemonEpoch,
			State:           string(sess.State),
			CreatedAt:       sess.CreatedAt,
			LastSeenAt:      sess.LastSeenAt,
			LeaseExpires:    sess.LeaseExpires,
			Spawns:          spawnsByParent[sess.ID],
		})
	}

	if err := json.NewEncoder(w).Encode(out); err != nil {
		d.logger.Warn("failed to encode /api/hud/sessions response", "error", err)
	}
}

// EnableEmbeddedHUD sets the embedded HUD enabled flag on the daemon's file
// config. Call this after New() but before Start() to enable the HUD via
// CLI flag (--hud-port) without requiring a config file change.
func (d *Daemon) EnableEmbeddedHUD() {
	d.fileCfg.EmbeddedHUD.Enabled = true
}

// firstNonEmpty returns the first non-empty string from the arguments.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// envBoolTrue interprets a string from os.Getenv as a permissive boolean.
// Returns true for "1", "true", "yes", "on" (case-insensitive); false for
// anything else (including empty). Mirrors the relaxed semantics of the
// existing SPAWN_ENABLED check in startEmbeddedHUD so operators don't have
// to remember which env vars are strict-equal vs lenient.
func envBoolTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func firstPositiveInt(configured int, envValue string) int {
	if configured > 0 {
		return configured
	}
	if v, err := strconv.Atoi(strings.TrimSpace(envValue)); err == nil && v > 0 {
		return v
	}
	return 0
}

func firstPositiveFloat(configured float64, envValue string) float64 {
	if configured > 0 {
		return configured
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(envValue), 64); err == nil && v > 0 {
		return v
	}
	return 0
}

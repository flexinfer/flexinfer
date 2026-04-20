package daemon

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/internal/hud/bridge"
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
		RegistryPath:         firstNonEmpty(d.cfg.RegistryPath, os.Getenv("LOOM_REGISTRY_PATH"), os.Getenv("LOOM_REGISTRY")),
		MobileOperatorToken:  firstNonEmpty(cfg.MobileOperatorToken, os.Getenv("HUD_MOBILE_OPERATOR_TOKEN")),
		MobileOperatorScopes: firstNonEmpty(cfg.MobileOperatorScopes, os.Getenv("HUD_MOBILE_OPERATOR_SCOPES")),
		SpawnEnabled:         cfg.SpawnEnabled || os.Getenv("SPAWN_ENABLED") == "true",
		SpawnNamespace:       firstNonEmpty(cfg.SpawnNamespace, os.Getenv("SPAWN_NAMESPACE")),
		SpawnRegistry:        firstNonEmpty(cfg.SpawnRegistry, os.Getenv("SPAWN_REGISTRY")),
		SpawnSyncMode:        firstNonEmpty(cfg.SpawnSyncMode, os.Getenv("SPAWN_SYNC_MODE")),
		SpawnGitBaseURL:      firstNonEmpty(cfg.SpawnGitBaseURL, os.Getenv("SPAWN_GIT_BASE_URL")),
		SpawnGitSecret:       firstNonEmpty(cfg.SpawnGitSecret, os.Getenv("SPAWN_GIT_SECRET")),
		SpawnProjects:        firstNonEmpty(cfg.SpawnProjects, os.Getenv("SPAWN_PROJECTS")),
		PipelineProjects:     firstNonEmpty(cfg.PipelineProjects, os.Getenv("HUD_PIPELINE_PROJECTS")),
		BindAddress:          firstNonEmpty(cfg.BindAddress, os.Getenv("HUD_BIND_ADDRESS")),
		FlexInferURL:         firstNonEmpty(cfg.FlexInferURL, os.Getenv("FLEXINFER_URL")),
		FlexInferKey:         firstNonEmpty(cfg.FlexInferKey, os.Getenv("FLEXINFER_API_KEY")),
		CoordinatorModel:     firstNonEmpty(cfg.CoordinatorModel, os.Getenv("COORDINATOR_MODEL")),
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

	logger.Info("embedded HUD started",
		"spawn", hudCfg.SpawnEnabled,
		"mobile", hudCfg.MobileOperatorToken != "",
		"coordinator", hudCfg.FlexInferURL != "")

	return nil
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

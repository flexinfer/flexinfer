package daemon

import (
	"context"
	"os"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/weaver"
)

// startEmbeddedWeaver initializes the weaver router. Non-fatal on error
// (the daemon continues without weaver).
func (d *Daemon) startEmbeddedWeaver(ctx context.Context) error {
	cfg := weaver.LoadConfigFromEnv()
	if !cfg.Enabled {
		d.logger.Debug("weaver disabled")
		return nil
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	// Resolve FlexInfer connection from embedded HUD config or env.
	flexURL := firstNonEmpty(d.fileCfg.EmbeddedHUD.FlexInferURL, os.Getenv("FLEXINFER_URL"))
	flexKey := firstNonEmpty(d.fileCfg.EmbeddedHUD.FlexInferKey, os.Getenv("FLEXINFER_API_KEY"))
	if flexURL == "" {
		d.logger.Warn("weaver: FLEXINFER_URL not set, disabling")
		return nil
	}

	breaker := flexinfer.NewCircuitBreaker(5, cfg.Timeout)
	client := flexinfer.NewClient(flexURL, flexKey, cfg.Timeout, breaker, d.logger)

	// Verify FlexInfer is reachable (non-blocking; don't fail daemon start).
	if err := client.HealthCheck(ctx); err != nil {
		d.logger.Warn("weaver: FlexInfer health check failed, proceeding anyway", "error", err)
	}

	// Load model behaviors from YAML (overrides defaults in cfg).
	if bs, err := weaver.LoadBehaviorsFromFile(weaver.DefaultBehaviorsPath()); err != nil {
		d.logger.Warn("weaver: failed to load behaviors YAML", "error", err)
	} else if bs != nil {
		for k, v := range bs {
			cfg.ModelBehaviors[k] = v
		}
		d.logger.Debug("weaver: loaded behaviors from YAML", "count", len(bs))
	}

	// Create in-process caller for tool dispatch through daemon pipeline.
	caller := bridge.NewLocalCaller(d.handleMessage)
	executor := weaver.NewDaemonToolExecutor(caller, cfg.Timeout)

	// Create tool lister that reads from daemon's tool cache.
	lister := &daemonToolLister{daemon: d}

	router := weaver.NewRouter(cfg, client, executor, lister, d.logger)

	// Load YAML domain overrides.
	yamlPath := weaver.DefaultDomainsPath()
	if err := weaver.MergeDomainsIntoRegistry(router.Registry(), yamlPath); err != nil {
		d.logger.Warn("weaver: failed to load YAML domains", "path", yamlPath, "error", err)
	}

	// Wire OTel tracer if the daemon has one configured.
	if t := d.daemonTracer(); t != nil {
		router.SetTracer(t)
	}

	d.weaver = router

	// Install the agent-context recorder so completed Query calls
	// emit `weaver.query.complete` on the daemon EventBus. The HUD
	// SSE hub forwards these to subscribers (HUD, iOS, spectator)
	// and any future agent-context bridge can consume them to write
	// `weaver_query` entries on the originating session. See
	// .loom/111 (AC-002) and pkg/agentcontext/schema.go
	// (EntryTypeWeaverQuery).
	router.SetQueryRecorder(newWeaverContextRecorder(d.logger, d.eventBus))

	// Validate domain tool references against available tools.
	if warnings := router.Registry().ValidateTools(lister); len(warnings) > 0 {
		for _, w := range warnings {
			d.logger.Warn("weaver: " + w)
		}
	}

	// Run a non-blocking model preflight. Failure is logged but never
	// blocks daemon startup — operators see degraded state in
	// /api/weaver/status instead.
	pre := runWeaverPreflight(ctx, client, cfg, router.Registry())
	d.weaverPreflight.Set(pre)
	if pre.Degraded {
		d.logger.Warn("weaver: model preflight degraded",
			"missing_models", pre.MissingModels,
			"catalog_size", pre.CatalogSize,
			"catalog_error", pre.CatalogError,
		)
	} else {
		d.logger.Info("weaver: model preflight ok",
			"ready_models", pre.ReadyModels,
			"catalog_size", pre.CatalogSize,
		)
	}

	d.logger.Info("weaver started",
		"router_model", cfg.RouterModel,
		"subagent_model", cfg.SubagentModel,
		"domains", router.Registry().Names(),
		"max_concurrent", cfg.MaxConcurrent,
	)
	return nil
}

// daemonToolLister implements weaver.ToolLister by reading from the
// daemon's aggregated tool cache.
type daemonToolLister struct {
	daemon *Daemon
}

func (l *daemonToolLister) ListTools() ([]weaver.ToolInfo, error) {
	l.daemon.toolCache.mu.RLock()
	defer l.daemon.toolCache.mu.RUnlock()

	tools := make([]weaver.ToolInfo, 0, len(l.daemon.toolCache.tools))
	for _, t := range l.daemon.toolCache.tools {
		schema := make(map[string]any)
		if t.InputSchema.Properties != nil {
			schema["type"] = t.InputSchema.Type
			schema["properties"] = t.InputSchema.Properties
			if len(t.InputSchema.Required) > 0 {
				schema["required"] = t.InputSchema.Required
			}
		}
		tools = append(tools, weaver.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return tools, nil
}

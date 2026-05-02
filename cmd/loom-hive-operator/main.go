// loom-hive-operator is the always-on cluster control plane for Loom Hive.
// It owns the canonical SQLite store, runs the council scheduler + pipeline
// reconciler, evaluates policy gates, and exposes the REST + MCP surface that
// the Mac-side `loom hive` CLI and the HUD consume. See .loom/91-… Phase 1.
//
// This binary is the home of the slow lights-on processes the operator's
// laptop cannot host: scheduled council runs, the per-backlog-item DAG
// reconciler, OAuth refresh integration, and the budget enforcer. Mac
// clients are read-mostly callers over HTTPS.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/clients"
	"github.com/crb2nu/loom/pkg/hive/council"
	"github.com/crb2nu/loom/pkg/hive/eval"
	"github.com/crb2nu/loom/pkg/hive/gates"
	"github.com/crb2nu/loom/pkg/hive/pipeline"
	"github.com/crb2nu/loom/pkg/hive/runner"
	"github.com/crb2nu/loom/pkg/hive/squads"
	"github.com/crb2nu/loom/pkg/hive/store"
)

var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "loom-hive-operator: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cfg := DefaultConfig()
	cfg.ApplyEnv()

	cmd := &cobra.Command{
		Use:           "loom-hive-operator",
		Short:         "Loom Hive cluster operator (council + pipeline)",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return run(cfg)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&cfg.DBPath, "db-path", cfg.DBPath, "Path to the canonical SQLite database")
	flags.StringVar(&cfg.PolicyPath, "policy-path", cfg.PolicyPath, "Path to the YAML policy file")
	flags.StringVar(&cfg.SquadsPath, "squads-path", cfg.SquadsPath, "Directory containing squad manifest YAMLs (missing dir is non-fatal)")
	flags.StringVar(&cfg.HTTPAddr, "listen", cfg.HTTPAddr, "Bind address for the REST + MCP listener (empty disables)")
	flags.StringVar(&cfg.MetricsAddr, "metrics-addr", cfg.MetricsAddr, "Bind address for /healthz, /readyz, /metrics (empty disables)")
	flags.StringVar(&cfg.RepoRoot, "repo-root", cfg.RepoRoot, "Path to the loom-core checkout the council writes into")
	flags.BoolVar(&cfg.Debug, "debug", cfg.Debug, "Enable verbose logging")
	return cmd
}

// run is the top-level lifecycle: prepare deps → start listeners → block on
// signal → graceful shutdown. Lifecycle is ordered so the readyz probe only
// flips to 200 after migrations + initial policy load complete.
func run(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	logger := newLogger(cfg.Debug)
	logger.Info("loom-hive-operator booting",
		"version", version,
		"db_path", cfg.DBPath,
		"policy_path", cfg.PolicyPath,
		"http_addr", cfg.HTTPAddr,
		"metrics_addr", cfg.MetricsAddr,
	)

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return fmt.Errorf("ensure db dir: %w", err)
	}

	// Read the admin token from env once. setAdminToken is atomic so a
	// future K8s Secret rotation path can swap it in without a restart.
	loadAdminTokenFromEnv()

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st, err := store.Open(rootCtx, store.Options{Path: cfg.DBPath})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			logger.Warn("store close", "error", cerr)
		}
	}()
	logger.Info("store opened, migrations applied")

	pm, err := hive.NewPolicyManager(rootCtx, cfg.PolicyPath, hive.PolicyManagerOptions{
		OnError: func(e error) { logger.Warn("policy reload failed", "error", e) },
	})
	if err != nil {
		return fmt.Errorf("policy manager: %w", err)
	}
	defer func() {
		if cerr := pm.Close(); cerr != nil {
			logger.Warn("policy manager close", "error", cerr)
		}
	}()
	pm.Subscribe(func(_, n *hive.Policy) {
		logger.Info("policy reloaded",
			"version", n.Version,
			"enabled", n.IsEnabled(),
			"council_max_usd_per_day", n.Budgets.Council.MaxUSDPerDay,
			"pipeline_max_concurrent_runs", n.Budgets.Pipeline.MaxConcurrentRuns,
		)
	})

	budget := hive.NewBudget(pm, hive.NewStoreBudgetReader(st))

	// Squad loader (Phase 2 slice 2.4). Reflects squad manifests from
	// cfg.SquadsPath into the canonical store and watches the dir via
	// fsnotify. A missing dir is non-fatal: the squads endpoints return
	// empty results until manifests are mounted.
	squadsLoader := buildSquadsLoader(rootCtx, cfg, st, logger)
	if squadsLoader != nil {
		defer func() {
			if cerr := squadsLoader.Close(); cerr != nil {
				logger.Warn("squads loader close", "error", cerr)
			}
		}()
	}

	// Council runner. Reviewers + editor are FakeReviewer + FakeEditor
	// for slice 3.7 — they make /api/hive/council/{run,dryrun} produce
	// real artifacts + sidecar + eval row + backlog mutations end to
	// end without a live agent. Production wiring swaps in spawn-backed
	// implementations in a follow-up slice once the spawn integration
	// for FlexInfer + Claude/Codex headless is wired.
	councilRunner := buildCouncilRunner(st, pm, budget, cfg.RepoRoot, logger)

	op := newOperator(st, pm, budget, logger).withRunner(councilRunner).withSquadsLoader(squadsLoader)
	op.markReady()
	logger.Info("operator ready", "policy_enabled", pm.Current().IsEnabled())

	httpSrv := httpServer(cfg.HTTPAddr, op.httpMux())
	metricsSrv := httpServer(cfg.MetricsAddr, op.metricsMux())

	// Gate registry: deterministic gates always; LLM-judged gates only
	// when FlexInfer is configured.
	gateRegistry := gates.Default()
	flexClient := buildFlexInferClient(cfg, logger)
	if flexClient != nil {
		gates.RegisterLLMGates(gateRegistry, clients.NewRubricJudge(flexClient))
		logger.Info("LLM-judged gates enabled (FlexInfer)")
	} else {
		logger.Warn("LLM-judged gates disabled; spec_conformance + pr_self_review skipped (set FLEXINFER_PROXY_URL)")
	}

	// MCP hub client: shared by Devbox, Handoff, and Worktree wrappers.
	// Nil hub means stage workers + escalator handoff fall back to
	// stubs. The operator establishes a persistent agent-context session
	// so handoff + worktree-allocate calls have a stable source session
	// id; defer cleanup so a clean shutdown ends the session row.
	hubClient, sessionID := establishHubAndSession(rootCtx, cfg, logger)
	if hubClient != nil {
		defer endOperatorSession(hubClient, sessionID, logger)
	}

	// Worker dispatcher: real clients where configured, NoOpDispatcher
	// for stages whose backing service isn't wired yet. The operator
	// logs each gap so it's obvious which surfaces are stub vs production.
	dispatcher := buildDispatcher(cfg, flexClient, hubClient, logger)

	pipelineRunner := pipeline.New(st, gateRegistry, dispatcher, pm)
	pipelineRunner.Logger = logger
	attributor := eval.NewOutcomeAttributor(st)
	pipelineRunner.OnMerged = attributor.OnMerged

	// Escalator: GitLab for issues, MCP hub for handoff. Either may be
	// disabled independently; the escalator runs whichever it has.
	gitlabClient := buildGitLabClient(cfg, logger)
	var handoffClient pipeline.HandoffClient
	if hubClient != nil && sessionID != "" {
		handoffClient = clients.NewHandoffClient(hubClient, sessionID)
		logger.Info("escalator handoff enabled (mcp-agent-context)")
	} else {
		logger.Warn("escalator handoff disabled (no MCP hub or operator session)")
	}
	if gitlabClient != nil || handoffClient != nil {
		escalator := pipeline.NewEscalator(st, gitlabClient, handoffClient)
		escalator.Logger = logger
		pipelineRunner.Escalator = escalator
		logger.Info("escalator enabled", "issue", gitlabClient != nil, "handoff", handoffClient != nil)
	} else {
		logger.Warn("escalator disabled; failures will transition to escalated state without issue/handoff publication")
	}

	// Pipeline starter routes fan-out items through the integrator when
	// the worktree allocator + branch merger are both available.
	var integrator *pipeline.Integrator
	if hubClient != nil && sessionID != "" && cfg.RepoRoot != "" {
		alloc := clients.NewWorktreeAllocator(hubClient, "loom-hive-operator", sessionID, cfg.RepoRoot)
		merger := clients.NewGitBranchMerger(cfg.RepoRoot)
		integrator = pipeline.NewIntegrator(st, pipelineRunner, alloc, merger)
		integrator.Logger = logger
		// Inherit the pipeline runner's MaxConcurrentRuns budget for the
		// integrator's parallel fan-out cap so a single backlog item
		// can't blow through the daily run budget.
		if max := pm.Current().Budgets.Pipeline.MaxConcurrentRuns; max > 0 {
			integrator.MaxParallel = max
		}
		// Hook the same Escalator the runner uses so a fan-out parent
		// that escalates publishes a failure record + handoff.
		integrator.Escalator = pipelineRunner.Escalator
		logger.Info("integrator enabled (worktree allocator + git branch merger)")
	} else {
		logger.Warn("integrator disabled; multi-slice items will run via Runner only (no fan-out)")
	}
	starter := pipeline.NewRunnerStarter(pipelineRunner, integrator)
	starter.Logger = logger

	// Reconciler / scheduler. The reconciler hands queued items to the
	// pipeline starter (which spawns goroutines that drive the DAG and
	// fire OnMerged → eval Loop B per merge).
	reconciler := hive.NewReconciler(st, pm, budget, starter)
	reconciler.Logger = logger
	scheduler := hive.NewScheduler(reconciler)
	scheduler.Logger = logger

	// Eval Loop C — weekly cross-run consistency check (default Sunday
	// 06:00 UTC). Runs alongside the reconciler scheduler in the same
	// errgroup so a panic in either takes the whole operator down for a
	// supervised restart, not a silent stuck loop.
	crossRunChecker := &eval.CrossRunChecker{Store: st, Logger: logger}
	crossRunSched := eval.NewCrossRunScheduler(crossRunChecker)
	crossRunSched.Logger = logger
	logger.Info("eval Loop C scheduler armed",
		"weekday", crossRunSched.Weekday.String(), "hour_utc", crossRunSched.Hour)

	g, gctx := errgroup.WithContext(rootCtx)
	g.Go(func() error { return runListener(gctx, "http", httpSrv, logger) })
	g.Go(func() error { return runListener(gctx, "metrics", metricsSrv, logger) })
	g.Go(func() error { return scheduler.Run(gctx) })
	g.Go(func() error { return crossRunSched.Run(gctx) })

	err = g.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("loom-hive-operator stopped")
	return nil
}

// newLogger returns a slog.Logger writing JSON to stderr — the format Loki
// expects from cluster pods.
func newLogger(debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// buildCouncilRunner wires the slice 3.7 fakes — FakeReviewer for every
// configured lens, FakeEditor as the synthesis agent, and the deterministic
// rubric with no LLM judge — into a runner. Production wiring swaps the
// fakes for spawn-backed implementations in a follow-up slice once the
// FlexInfer + Claude/Codex headless integration ships.
//
// Returns nil + a structured log line if the policy doesn't configure any
// reviewer ensemble. The operator continues to serve read-only endpoints
// in that case; council POSTs respond 503.
func buildCouncilRunner(
	st *store.Store,
	pm *hive.PolicyManager,
	budget *hive.Budget,
	repoRoot string,
	logger *slog.Logger,
) *runner.Runner {
	policy := pm.Current()
	lenses := council.LensesFromPolicy(policy)
	if len(lenses) == 0 {
		logger.Warn("no council reviewers configured; council POST endpoints will return 503")
		return nil
	}
	reviewers := map[string]council.Reviewer{}
	for _, l := range lenses {
		reviewers[l.Name] = &council.FakeReviewer{
			Notes:   "fake reviewer (slice 3.7)",
			CostUSD: 0.05,
		}
	}
	dispatcher := &council.Dispatcher{Reviewers: reviewers}
	editor := &council.FakeEditor{
		Backend: policy.Council.Ensemble.Editor.Backend,
		Model:   policy.Council.Ensemble.Editor.Model,
		CostUSD: 0.42,
		Notes:   "FakeEditor (slice 3.7); production swap-in pending",
	}
	writer := &council.ArtifactWriter{RepoRoot: repoRoot}
	mutator := &council.BacklogMutator{Store: st}
	judge := &eval.Judge{Criteria: eval.DefaultRubric(&eval.FakeLLMJudge{Score: 1.0})}

	return &runner.Runner{
		Store:     st,
		Policy:    pm,
		Budget:    budget,
		Reviewers: dispatcher,
		Editor:    editor,
		Writer:    writer,
		Mutator:   mutator,
		Judge:     judge,
		RepoRoot:  repoRoot,
		Logger:    logger,
	}
}

// buildSquadsLoader instantiates the squads manifest loader pointing at
// cfg.SquadsPath. Missing dir is non-fatal: a warn log fires and the
// operator boots without a loader (squad endpoints return empty results
// until manifests are mounted). Other errors (fsnotify failure) also
// log + return nil so a busted watcher doesn't block boot.
func buildSquadsLoader(ctx context.Context, cfg Config, st *store.Store, logger *slog.Logger) *squads.Loader {
	if strings.TrimSpace(cfg.SquadsPath) == "" {
		logger.Warn("squads loader disabled (squads-path empty)")
		return nil
	}
	if _, err := os.Stat(cfg.SquadsPath); err != nil {
		logger.Warn("squads loader skipped: path not present",
			"squads_path", cfg.SquadsPath, "error", err)
		return nil
	}
	loader, err := squads.NewLoader(ctx, cfg.SquadsPath, st, squads.LoaderOptions{
		OnError: func(e error) { logger.Warn("squads reload error", "error", e) },
		Logger:  logger,
	})
	if err != nil {
		logger.Warn("squads loader init failed; squad endpoints will return empty",
			"error", err)
		return nil
	}
	logger.Info("squads loader running", "squads_path", cfg.SquadsPath,
		"loaded", len(loader.Current()))
	return loader
}

// buildFlexInferClient returns a configured FlexInfer client when
// FLEXINFER_PROXY_URL is set, or nil + a warn log otherwise. The nil
// path lets the operator boot in "policy disabled" mode without LLM
// dependencies for local dev.
func buildFlexInferClient(cfg Config, logger *slog.Logger) *clients.FlexInferClient {
	if cfg.FlexInferProxyURL == "" {
		return nil
	}
	c, err := clients.NewFlexInferClient(clients.FlexInferConfig{
		ProxyURL:    cfg.FlexInferProxyURL,
		Token:       cfg.FlexInferToken,
		JudgeModel:  cfg.FlexInferJudgeModel,
		WeaverModel: cfg.FlexInferWeaverModel,
	})
	if err != nil {
		logger.Error("flexinfer client init failed; LLM gates + research stage will skip", "error", err)
		return nil
	}
	return c
}

// buildGitLabClient returns a configured GitLab client when
// GITLAB_API_URL + GITLAB_TOKEN + GITLAB_PROJECT are all set, otherwise
// nil + a warn log so the operator boots without it.
func buildGitLabClient(cfg Config, logger *slog.Logger) *clients.GitLabClient {
	if cfg.GitLabAPIURL == "" || cfg.GitLabToken == "" || cfg.GitLabProject == "" {
		return nil
	}
	c, err := clients.NewGitLabClient(clients.GitLabConfig{
		APIURL:  cfg.GitLabAPIURL,
		Token:   cfg.GitLabToken,
		Project: cfg.GitLabProject,
	})
	if err != nil {
		logger.Error("gitlab client init failed; mr/ci/merge/cleanup stages will stub", "error", err)
		return nil
	}
	return c
}

// buildDispatcher wires the per-stage worker dispatcher. Real clients
// are used where configured; stages whose backing service isn't bridged
// fall back to the NoOp output so the runner still drives the DAG to
// done in a smoke-test sense. Each gap is logged at startup so
// production deployments can see exactly what's still stub.
//
// Wired stages (when env-configured):
//   - WeaverWorker (research): FlexInfer proxy
//   - GitLabWorker (mr/ci_watch/merge/cleanup): GitLab REST API
//   - DevboxWorker (tests): mcp-devbox via MCP hub
//   - SpawnWorker (plan_slice/implement/pr_self_review): HUD mobile API
func buildDispatcher(cfg Config, flex *clients.FlexInferClient, hub *clients.MCPHubClient, logger *slog.Logger) pipeline.WorkerDispatcher {
	noop := &pipeline.NoOpDispatcher{}
	gitlab := buildGitLabClient(cfg, logger)
	spawn := buildHUDSpawnClient(cfg, logger)

	routes := map[string]pipeline.Worker{}
	if flex != nil {
		routes["research"] = &pipeline.WeaverWorker{Client: clients.NewWeaverClient(flex)}
		logger.Info("research stage wired to FlexInfer (WeaverClient)")
	} else {
		logger.Warn("research stage stub: NoOpDispatcher (FLEXINFER_PROXY_URL unset)")
	}
	if gitlab != nil {
		gw := &pipeline.GitLabWorker{Client: gitlab}
		routes["mr"] = gw
		routes["ci_watch"] = gw
		routes["merge"] = gw
		routes["cleanup"] = gw
		logger.Info("mr/ci_watch/merge/cleanup stages wired to GitLab")
	} else {
		logger.Warn("mr/ci_watch/merge/cleanup stages stub: NoOpDispatcher (GITLAB_API_URL/TOKEN/PROJECT unset)")
	}

	project := cfg.GitLabProject
	if project == "" {
		project = "loom-core"
	}
	if hub != nil {
		routes["tests"] = &pipeline.DevboxWorker{
			Client:  clients.NewDevboxClient(hub),
			Project: project,
			AgentID: "loom-hive-operator",
		}
		logger.Info("tests stage wired to devbox via MCP hub")
	} else {
		logger.Warn("tests stage stub: NoOpDispatcher (LOOM_MCP_HUB_URL unset)")
	}

	if spawn != nil {
		// All three Claude/Codex-backed stages share the spawn client.
		// Per-stage Model + PromptFor closures select agent type and
		// prompt body; production deployments register richer prompt
		// builders here once spec doc loaders ship.
		routes["plan_slice"] = &pipeline.SpawnWorker{
			Client:    spawn,
			Model:     "claude-code",
			Project:   project,
			Namespace: "loom-hive",
			PromptFor: stagePromptFor("plan_slice"),
		}
		routes["implement"] = &pipeline.SpawnWorker{
			Client:        spawn,
			Model:         "claude-code",
			Project:       project,
			Namespace:     "loom-hive",
			PromptFor:     stagePromptFor("implement"),
			NeedsWorktree: true,
		}
		routes["pr_self_review"] = &pipeline.SpawnWorker{
			Client:    spawn,
			Model:     "claude-code",
			Project:   project,
			Namespace: "loom-hive",
			PromptFor: stagePromptFor("pr_self_review"),
		}
		logger.Info("plan_slice/implement/pr_self_review stages wired to HUD spawn API")
	} else {
		logger.Warn("plan_slice/implement/pr_self_review stages stub: NoOpDispatcher (LOOM_HUD_URL+LOOM_HUD_TOKEN unset)")
	}

	return &fallbackDispatcher{routes: routes, fallback: noop}
}

// stagePromptFor returns a default per-stage prompt builder. Production
// deployments override this with spec-doc-aware closures; the default
// gives each stage a terse but pointed prompt that the runner's
// JobContext fills with item title + slice scope.
func stagePromptFor(stage string) func(jc pipeline.JobContext) string {
	templates := map[string]string{
		"plan_slice":     "Plan implementation slices for backlog item %s (%q). Output a numbered list of independent slices with files touched and test strategy per slice.",
		"implement":      "Implement backlog item %s (%q). Write code + tests in the allocated worktree. Commit with conventional commit format.",
		"pr_self_review": "Review your own diff for backlog item %s (%q) before opening a merge request. Score on the pr_self_review_v1 rubric and fix anything below 0.8.",
	}
	tmpl := templates[stage]
	if tmpl == "" {
		tmpl = "Run stage %s for item %s (%q)."
	}
	return func(jc pipeline.JobContext) string {
		title := ""
		id := ""
		if jc.Item != nil {
			id = jc.Item.ID
			title = jc.Item.Title
		}
		if stage == "" {
			return fmt.Sprintf(tmpl, jc.Stage.ID, id, title)
		}
		return fmt.Sprintf(tmpl, id, title)
	}
}

// buildHUDSpawnClient returns a configured SpawnClient when LOOM_HUD_URL
// and LOOM_HUD_TOKEN are both set. Nil otherwise + warn log so the
// operator boots without it.
func buildHUDSpawnClient(cfg Config, logger *slog.Logger) *clients.HUDSpawnClient {
	if cfg.HUDBaseURL == "" || cfg.HUDToken == "" {
		return nil
	}
	c, err := clients.NewHUDSpawnClient(clients.HUDSpawnConfig{
		BaseURL: cfg.HUDBaseURL,
		Token:   cfg.HUDToken,
	})
	if err != nil {
		logger.Error("HUD spawn client init failed; spawn-driven stages disabled", "error", err)
		return nil
	}
	return c
}

// fallbackDispatcher routes stages with a real worker through that
// worker, and stages without one through the NoOp fallback. It's a
// thin variant of pipeline.Dispatcher with a guaranteed fallback so
// unmapped stages never error during the bring-up window.
type fallbackDispatcher struct {
	routes   map[string]pipeline.Worker
	fallback pipeline.WorkerDispatcher
}

func (d *fallbackDispatcher) Dispatch(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem, stage pipeline.Stage, prior map[string]pipeline.StageOutput) (pipeline.StageOutput, error) {
	if w, ok := d.routes[stage.ID]; ok {
		jc := pipeline.JobContext{
			Run:    run,
			Item:   item,
			Stage:  stage,
			Prior:  prior,
			Budget: item.Budget,
			Env:    pipeline.BuildHiveEnv(run, item, stage),
		}
		return w.Run(ctx, jc)
	}
	return d.fallback.Dispatch(ctx, run, item, stage, prior)
}

// establishHubAndSession constructs the MCP hub client (when
// LOOM_MCP_HUB_URL is set) and registers a long-lived agent-context
// session for the operator. The session id is the SourceSessionID
// passed to HandoffClient + WorktreeAllocator so handoff packages and
// worktree-allocate calls have a consistent source.
//
// Returns (nil, "") and a warn log when the hub is unconfigured or the
// session-start call fails — the operator still boots, just without
// hub-backed clients.
func establishHubAndSession(ctx context.Context, cfg Config, logger *slog.Logger) (*clients.MCPHubClient, string) {
	hubCfg, ok := clients.MCPHubConfigFromEnv(os.Getenv)
	if !ok {
		logger.Warn("MCP hub disabled (set LOOM_MCP_HUB_URL); devbox/handoff/worktree clients fall back to stubs")
		return nil, ""
	}
	hub, err := clients.NewMCPHubClient(hubCfg)
	if err != nil {
		logger.Error("MCP hub init failed; devbox/handoff/worktree clients disabled", "error", err)
		return nil, ""
	}
	logger.Info("MCP hub configured", "url", hubCfg.HubURL, "profile", hubCfg.Profile)

	// Establish a persistent operator session so handoff + worktree
	// calls have a stable source. Best-effort: a session-start failure
	// is logged but doesn't block boot — the affected clients fall
	// back to stub behavior.
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, err := hub.CallTool(startCtx, clients.AgentContextServerName, "agent_session_start", map[string]any{
		"namespace":   "loom-hive",
		"agent_id":    "loom-hive-operator",
		"agent_type":  "operator",
		"description": "loom-hive-operator persistent session (boot " + time.Now().UTC().Format(time.RFC3339) + ")",
	})
	if err != nil {
		logger.Error("agent_session_start failed; handoff + worktree clients disabled", "error", err)
		return hub, ""
	}
	sessionID := extractSessionID(body)
	if sessionID == "" {
		logger.Warn("agent_session_start returned empty session_id; handoff + worktree clients disabled", "body_tail", truncateForLog(body, 200))
		return hub, ""
	}
	logger.Info("operator session established", "session_id", sessionID)
	return hub, sessionID
}

// endOperatorSession is the deferred cleanup that ends the operator's
// agent-context session on shutdown. Best-effort: errors are logged.
func endOperatorSession(hub *clients.MCPHubClient, sessionID string, logger *slog.Logger) {
	if hub == nil || sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := hub.CallTool(ctx, clients.AgentContextServerName, "agent_session_end", map[string]any{
		"session_id": sessionID,
		"summarize":  false,
	}); err != nil {
		logger.Warn("agent_session_end on shutdown failed", "error", err)
		return
	}
	logger.Info("operator session ended", "session_id", sessionID)
}

// extractSessionID pulls session_id out of the agent_session_start
// response body. The mcp-agent-context tool emits its result via the
// active LOOM_MCP_OUTPUT_FORMAT — usually TOON (yaml-like) in
// production, JSON when override env is set. Try JSON first; fall back
// to a line-by-line scan for "session_id: <value>" which works for
// both TOON and JSON-pretty formats.
func extractSessionID(body string) string {
	if body == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err == nil {
		if v, ok := parsed["session_id"].(string); ok {
			return v
		}
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "session_id:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, "session_id:"))
		val = strings.Trim(val, "\"' ")
		if val != "" && val != "null" {
			return val
		}
	}
	return ""
}

// truncateForLog clips a string to n characters for safe slog output.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

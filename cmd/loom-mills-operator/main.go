// loom-mills-operator is the always-on cluster control plane for Loom Mills.
// It owns the canonical SQLite store, runs the council scheduler + pipeline
// reconciler, evaluates policy gates, and exposes the REST + MCP surface that
// the Mac-side `loom mills` CLI and the HUD consume. See .loom/91-… Phase 1.
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
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/audit"
	"github.com/crb2nu/loom/pkg/mills/clients"
	"github.com/crb2nu/loom/pkg/mills/council"
	"github.com/crb2nu/loom/pkg/mills/eval"
	"github.com/crb2nu/loom/pkg/mills/gates"
	"github.com/crb2nu/loom/pkg/mills/pipeline"
	"github.com/crb2nu/loom/pkg/mills/runner"
	"github.com/crb2nu/loom/pkg/mills/squads"
	"github.com/crb2nu/loom/pkg/mills/store"
)

var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "loom-mills-operator: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cfg := DefaultConfig()
	cfg.ApplyEnv()

	cmd := &cobra.Command{
		Use:           "loom-mills-operator",
		Short:         "Loom Mills cluster operator (council + pipeline)",
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
	logger.Info("loom-mills-operator booting",
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

	if err := ensureRepoRoot(rootCtx, cfg, logger); err != nil {
		logger.Warn("repo root bootstrap failed; autonomy readiness will report repo_root red", "repo_root", cfg.RepoRoot, "error", err)
	}

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

	pm, err := mills.NewPolicyManager(rootCtx, cfg.PolicyPath, mills.PolicyManagerOptions{
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
	pm.Subscribe(func(_, n *mills.Policy) {
		logger.Info("policy reloaded",
			"version", n.Version,
			"enabled", n.IsEnabled(),
			"council_max_usd_per_day", n.Budgets.Council.MaxUSDPerDay,
			"pipeline_max_concurrent_runs", n.Budgets.Pipeline.MaxConcurrentRuns,
		)
	})

	budget := mills.NewBudget(pm, mills.NewStoreBudgetReader(st))

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

	// Gate registry: deterministic gates always; LLM-judged gates only
	// when FlexInfer is configured.
	gateRegistry := gates.Default()
	flexClient := buildFlexInferClient(cfg, logger)
	capabilities := newCapabilityWiring(cfg)
	capabilities.FlexInferConfigured = strings.TrimSpace(cfg.FlexInferProxyURL) != ""
	capabilities.FlexInferReady = flexClient != nil
	if flexClient != nil {
		gates.RegisterLLMGates(gateRegistry, clients.NewRubricJudge(flexClient))
		logger.Info("LLM-judged gates enabled (FlexInfer)")
	} else {
		logger.Warn("LLM-judged gates disabled; spec_conformance + pr_self_review skipped (set FLEXINFER_PROXY_URL)")
	}

	// Council runner. In production it uses FlexInfer-backed reviewers,
	// editor, and artifact judge. Local/degraded runs keep the deterministic
	// fakes so handlers can still be exercised, but autonomy readiness reports
	// the fake fallback as a blocker.
	councilRunner, councilUsesFakeAgents := buildCouncilRunner(st, pm, budget, cfg.RepoRoot, flexClient, logger)
	capabilities.CouncilConfigured = councilRunner != nil
	capabilities.CouncilUsesFakeAgents = councilUsesFakeAgents

	op := newOperator(st, pm, budget, logger).
		withRunner(councilRunner).
		withSquadsLoader(squadsLoader)
	// Audit subsystem is attached below after the pipeline runner +
	// FlexInfer client are ready; handlers read the fields at request
	// time so late attachment is fine.

	httpSrv := httpServer(cfg.HTTPAddr, op.httpMux())
	metricsSrv := httpServer(cfg.MetricsAddr, op.metricsMux())

	// MCP hub client: shared by Devbox, Handoff, and Worktree wrappers.
	// Nil hub means stage workers + escalator handoff fall back to
	// stubs. The operator establishes a persistent agent-context session
	// so handoff + worktree-allocate calls have a stable source session
	// id; defer cleanup so a clean shutdown ends the session row.
	hubClient, sessionID := establishHubAndSession(rootCtx, cfg, logger)
	operatorSession := &operatorSessionRef{}
	operatorSession.Set(sessionID)
	capabilities.MCPHubConfigured = strings.TrimSpace(os.Getenv("LOOM_MCP_HUB_URL")) != ""
	capabilities.MCPHubSessionReady = hubClient != nil && sessionID != ""
	if hubClient != nil {
		defer func() { endOperatorSession(hubClient, operatorSession.SessionID(), logger) }()
	}

	// Worker dispatcher: real clients where configured, NoOpDispatcher
	// for stages whose backing service isn't wired yet. The operator
	// logs each gap so it's obvious which surfaces are stub vs production.
	dispatcher, realStages := buildDispatcher(cfg, flexClient, hubClient, st, logger)
	capabilities.DispatcherRealStages = realStages
	capabilities.BranchContractReady = true
	capabilities.BranchContractSource = "pkg/mills/pipeline/branch_contract.go"
	capabilities.HUDSpawnConfigured = strings.TrimSpace(cfg.HUDBaseURL) != "" && strings.TrimSpace(cfg.HUDToken) != ""
	capabilities.HUDSpawnReady = realStages["plan_slice"] && realStages["implement"] && realStages["pr_self_review"]

	pipelineRunner := pipeline.New(st, gateRegistry, dispatcher, pm)
	pipelineRunner.Logger = logger
	attributor := eval.NewOutcomeAttributor(st)

	// Squad outcome recorder (Phase 2 v2.0 reconciler integration).
	// Reads the squad attribution event the reconciler emits at routing
	// time and writes a squad_outcomes row when a run merges. Wired
	// alongside attributor.OnMerged via a small composite hook so both
	// fire on every successful merge.
	squadRecorder := squads.NewOutcomeRecorder(st)
	squadRecorder.Logger = logger
	mergedHooks := []pipelineMergedHook{attributor.OnMerged, squadRecorder.OnMerged}

	// Audit subsystem (Phase 3). Activates only when FlexInfer is
	// configured AND the operator can reach the canonical store +
	// council runner. Without it the audit endpoints serve canonical
	// rows but the dispatcher / trigger fire-paths short-circuit.
	auditDispatcher, auditWorker, auditTriggers, auditPolicy := buildAuditSubsystem(
		flexClient, councilRunner, st, cfg.RepoRoot, logger,
	)
	if auditTriggers != nil {
		mergedHooks = append(mergedHooks, auditTriggers.OnPipelineMerged)
		logger.Info("audit triggers enabled (council + pipeline)")
	} else {
		logger.Info("audit triggers disabled (FLEXINFER_PROXY_URL or council runner missing)")
	}
	pipelineRunner.OnMerged = chainPipelineMerged(mergedHooks...)
	op.withAudit(auditDispatcher, auditWorker, auditTriggers, auditPolicy)

	// Escalator: GitLab for issues, MCP hub for handoff. Either may be
	// disabled independently; the escalator runs whichever it has.
	gitlabClient := buildGitLabClient(cfg, logger)
	capabilities.GitLabConfigured = strings.TrimSpace(cfg.GitLabAPIURL) != "" && strings.TrimSpace(cfg.GitLabToken) != "" && strings.TrimSpace(cfg.GitLabProject) != ""
	capabilities.GitLabReady = gitlabClient != nil
	var handoffClient pipeline.HandoffClient
	if hubClient != nil {
		handoff := clients.NewHandoffClient(hubClient, sessionID)
		handoff.SourceSessionIDFunc = operatorSession.SessionID
		handoffClient = handoff
		logger.Info("escalator handoff configured (mcp-agent-context)")
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

	// Audit follow-up writer (Phase 3 slice 3.6). When the audit
	// subsystem and a GitLab client are both wired, low-survival
	// findings auto-open advisory issues. Without GitLab, audits still
	// land in the canonical store + HUD; the follow-up step is a no-op.
	if auditWorker != nil && gitlabClient != nil {
		followup := audit.NewFollowup(gitlabClient)
		followup.Logger = logger
		auditWorker.OnRecorded = followup.OnRecorded
		logger.Info("audit follow-up writer enabled",
			"threshold", followup.Threshold)
	} else if auditWorker != nil {
		logger.Info("audit follow-up writer disabled (no GitLab client)")
	}

	// Pipeline starter routes fan-out items through the integrator when
	// the worktree allocator + branch merger are both available.
	var integrator *pipeline.Integrator
	if hubClient != nil && cfg.RepoRoot != "" {
		alloc := clients.NewWorktreeAllocator(hubClient, "loom-mills-operator", sessionID, cfg.RepoRoot)
		alloc.SourceSessionIDFunc = operatorSession.SessionID
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
	kpiWriter := mills.NewKPIWriter(st, pm)
	kpiWriter.Logger = logger
	capabilities.KPIWriterReady = true
	capabilities.KPIWriterSource = "pkg/mills/kpi_writer.go"
	op.setCapabilities(capabilities)
	op.markReady()
	logger.Info("operator ready",
		"policy_enabled", pm.Current().IsEnabled(),
		"autonomy_ready", op.capabilityReport(rootCtx).AutonomyReady,
	)

	// Reconciler / scheduler. The reconciler hands queued items to the
	// pipeline starter (which spawns goroutines that drive the DAG and
	// fire OnMerged → eval Loop B per merge).
	reconciler := mills.NewReconciler(st, pm, budget, starter)
	reconciler.Logger = logger
	reconciler.AutonomyGate = func(ctx context.Context) (bool, []string) {
		report := op.capabilityReport(ctx)
		return report.AutonomyReady, report.AutonomyBlockers
	}
	if squadsLoader != nil {
		// Wire the squad router into the reconciler so each tick attributes
		// the chosen squad via a "reconciler.squad_routed" event keyed on
		// the new run id. squadRecorder.OnMerged then reads it back at merge
		// time. Adapter glues squads.Decision → mills.SquadDecision without
		// pulling pkg/mills/squads into pkg/mills (no import cycle).
		router := squads.NewRouter(squadsLoader, st)
		reconciler.SquadRouter = &squadRouterAdapter{router: router}
		logger.Info("squad routing enabled", "min_confidence", router.MinConfidence)
	} else {
		logger.Info("squad routing disabled (no squads loader)")
	}
	op.withReconciler(reconciler)
	terminalSync, err := reconciler.SyncTerminalBacklogs(rootCtx)
	if err != nil {
		logger.Warn("pipeline startup backlog terminal sync failed", "error", err)
	} else if terminalSync.Inspected > 0 {
		logger.Info("pipeline startup backlog terminal sync complete",
			"inspected", terminalSync.Inspected, "updated", terminalSync.Updated,
			"skipped", terminalSync.Skipped, "errored", terminalSync.Errored)
	}
	resumed, err := reconciler.ResumeInFlightRuns(rootCtx)
	if err != nil {
		logger.Warn("pipeline startup resume failed", "error", err)
	} else if resumed.Inspected > 0 {
		logger.Info("pipeline startup resume complete",
			"inspected", resumed.Inspected, "resumed", resumed.Resumed, "errored", resumed.Errored)
	}
	scheduler := mills.NewScheduler(reconciler)
	scheduler.Logger = logger
	scheduler.KPIRecorder = kpiWriter

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
	if hubClient != nil {
		g.Go(func() error {
			runOperatorSessionMaintainer(gctx, hubClient, operatorSession, op, logger, 30*time.Second)
			return nil
		})
	}
	if auditWorker != nil {
		g.Go(func() error {
			auditWorker.Run(gctx)
			return nil
		})
		// Stop the worker on shutdown so Run() unblocks before Wait()
		// returns. defer Stop here (after errgroup setup) so a panic
		// in any sibling goroutine still triggers a clean drain.
		defer auditWorker.Stop()
	}

	err = g.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("loom-mills-operator stopped")
	return nil
}

type operatorSessionRef struct {
	mu        sync.RWMutex
	sessionID string
}

func (r *operatorSessionRef) SessionID() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessionID
}

func (r *operatorSessionRef) Set(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = sessionID
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

// buildCouncilRunner wires the configured council ensemble into a runner. When
// FlexInfer is configured, reviewers + editor + judge all use real local-tier
// model calls. Without FlexInfer, local/degraded deployments keep the
// deterministic fake fallback so dry-run/handler smoke tests still work, while
// autonomy readiness reports the fake participants as a blocker.
//
// Returns nil + a structured log line if the policy doesn't configure any
// reviewer ensemble. The operator continues to serve read-only endpoints
// in that case; council POSTs respond 503.
func buildCouncilRunner(
	st *store.Store,
	pm *mills.PolicyManager,
	budget *mills.Budget,
	repoRoot string,
	flexClient *clients.FlexInferClient,
	logger *slog.Logger,
) (*runner.Runner, bool) {
	policy := pm.Current()
	lenses := council.LensesFromPolicy(policy)
	if len(lenses) == 0 {
		logger.Warn("no council reviewers configured; council POST endpoints will return 503")
		return nil, false
	}

	usesFakeAgents := flexClient == nil
	reviewers := map[string]council.Reviewer{}
	var editor council.Editor
	var judge *eval.Judge
	if flexClient != nil {
		for _, l := range lenses {
			reviewers[l.Name] = &clients.FlexInferCouncilReviewer{
				Client: flexClient,
			}
		}
		editor = &clients.FlexInferCouncilEditor{
			Client:  flexClient,
			Backend: "flexinfer",
			Model:   policy.Council.Ensemble.Editor.Model,
		}
		judge = &eval.Judge{Criteria: eval.DefaultRubric(&clients.FlexInferEvalJudge{Client: flexClient})}
		logger.Info("council participants wired to FlexInfer-backed reviewers/editor/judge")
	} else {
		for _, l := range lenses {
			reviewers[l.Name] = &council.FakeReviewer{
				Notes:   "fake reviewer fallback; set FLEXINFER_PROXY_URL for production council participants",
				CostUSD: 0.05,
			}
		}
		editor = &council.FakeEditor{
			Backend: policy.Council.Ensemble.Editor.Backend,
			Model:   policy.Council.Ensemble.Editor.Model,
			CostUSD: 0.42,
			Notes:   "FakeEditor fallback; set FLEXINFER_PROXY_URL for production council participants",
		}
		judge = &eval.Judge{Criteria: eval.DefaultRubric(&eval.FakeLLMJudge{Score: 1.0})}
		logger.Warn("council participants using fake fallback; autonomy readiness will fail closed")
	}
	dispatcher := &council.Dispatcher{Reviewers: reviewers}
	writer := &council.ArtifactWriter{RepoRoot: repoRoot}
	mutator := &council.BacklogMutator{Store: st}

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
	}, usesFakeAgents
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
		Timeout:     cfg.FlexInferTimeout,
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
//   - WeaverWorker (research): FlexInfer proxy. When
//     MILLS_RESEARCH_VIA_WEAVER=shadow|on AND a weaver URL is
//     configured, the worker also calls the routed multi-domain
//     dispatch via WeaverHTTPDelegator and (in shadow mode) records
//     the diff to pipeline_runs.research_diff via PipelineDAO.
//   - GitLabWorker (mr/ci_watch/merge/cleanup): GitLab REST API
//   - DevboxWorker (tests): mcp-devbox via MCP hub
//   - SpawnWorker (plan_slice/implement/pr_self_review): HUD mobile API
func buildDispatcher(cfg Config, flex *clients.FlexInferClient, hub *clients.MCPHubClient, st *store.Store, logger *slog.Logger) (pipeline.WorkerDispatcher, map[string]bool) {
	noop := &pipeline.NoOpDispatcher{}
	gitlab := buildGitLabClient(cfg, logger)
	spawn := buildHUDSpawnClient(cfg, logger)

	routes := map[string]pipeline.Worker{}
	realStages := newCapabilityWiring(cfg).DispatcherRealStages
	if flex != nil {
		wc := clients.NewWeaverClient(flex)
		// Mode is read at construction time from MILLS_RESEARCH_VIA_
		// WEAVER. When non-default, attach the delegator + recorder
		// so shadow/on can actually do something. Mode==off ignores
		// both, so wiring them unconditionally would be wasteful.
		if wc.Mode != clients.ResearchModeOff {
			attachWeaverDelegation(wc, cfg, st, logger)
		}
		routes["research"] = &pipeline.WeaverWorker{Client: wc, PromptFor: stagePromptFor("research")}
		realStages["research"] = true
		logger.Info("research stage wired to FlexInfer (WeaverClient)",
			"research_mode", string(wc.Mode))
	} else {
		logger.Warn("research stage stub: NoOpDispatcher (FLEXINFER_PROXY_URL unset)")
	}
	if gitlab != nil {
		gw := &pipeline.GitLabWorker{Client: gitlab}
		routes["mr"] = gw
		routes["ci_watch"] = gw
		routes["merge"] = gw
		routes["cleanup"] = gw
		realStages["mr"] = true
		realStages["ci_watch"] = true
		realStages["merge"] = true
		realStages["cleanup"] = true
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
			AgentID: "loom-mills-operator",
		}
		realStages["tests"] = true
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
			Namespace: "loom-mills",
			PromptFor: stagePromptFor("plan_slice"),
		}
		routes["implement"] = &pipeline.SpawnWorker{
			Client:        spawn,
			Model:         "claude-code",
			Project:       project,
			Namespace:     "loom-mills",
			PromptFor:     stagePromptFor("implement"),
			NeedsWorktree: true,
		}
		routes["pr_self_review"] = &pipeline.SpawnWorker{
			Client:    spawn,
			Model:     "claude-code",
			Project:   project,
			Namespace: "loom-mills",
			PromptFor: stagePromptFor("pr_self_review"),
		}
		realStages["plan_slice"] = true
		realStages["implement"] = true
		realStages["pr_self_review"] = true
		logger.Info("plan_slice/implement/pr_self_review stages wired to HUD spawn API")
	} else {
		logger.Warn("plan_slice/implement/pr_self_review stages stub: NoOpDispatcher (LOOM_HUD_URL+LOOM_HUD_TOKEN unset)")
	}

	return &fallbackDispatcher{routes: routes, fallback: noop}, realStages
}

// stagePromptFor returns a default per-stage prompt builder. Production
// deployments override this with spec-doc-aware closures; the default
// gives each stage a terse but pointed prompt that the runner's
// JobContext fills with item title + slice scope.
func stagePromptFor(stage string) func(jc pipeline.JobContext) string {
	templates := map[string]string{
		"plan_slice":     "Plan implementation slices for backlog item %s (%q). Output a numbered list of independent slices with files touched and test strategy per slice.",
		"research":       "Research backlog item %s (%q). Summarize relevant code paths, prior decisions, test constraints, and rollout risks for the implementation worker.",
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
			return fmt.Sprintf("%s\n\n%s", fmt.Sprintf(tmpl, jc.Stage.ID, id, title), backlogPromptContext(jc.Item))
		}
		return fmt.Sprintf("%s\n\n%s", fmt.Sprintf(tmpl, id, title), backlogPromptContext(jc.Item))
	}
}

func backlogPromptContext(item *store.BacklogItem) string {
	if item == nil {
		return "Backlog context: unavailable."
	}
	var b strings.Builder
	b.WriteString("Backlog context:\n")
	if len(item.Labels) > 0 {
		fmt.Fprintf(&b, "- Labels: %s\n", strings.Join(item.Labels, ", "))
	}
	if item.SpecDoc != "" {
		fmt.Fprintf(&b, "- Spec: %s\n", item.SpecDoc)
	}
	if item.SpecAnchor != "" {
		fmt.Fprintf(&b, "- Spec anchor: %s\n", item.SpecAnchor)
	}
	if len(item.Success.Tests) > 0 {
		fmt.Fprintf(&b, "- Required tests: %s\n", strings.Join(item.Success.Tests, "; "))
	}
	if len(item.Success.Metrics) > 0 {
		fmt.Fprintf(&b, "- Required metrics: %s\n", strings.Join(item.Success.Metrics, "; "))
	}
	if item.Success.ManualCheck != "" {
		fmt.Fprintf(&b, "- Manual check: %s\n", item.Success.ManualCheck)
	}
	if len(item.Slices) > 0 {
		b.WriteString("- Slice scope:\n")
		for _, s := range item.Slices {
			fmt.Fprintf(&b, "  - %s", s.Name)
			if len(s.Files) > 0 {
				fmt.Fprintf(&b, " files=%s", strings.Join(s.Files, ", "))
			}
			if len(s.Tests) > 0 {
				fmt.Fprintf(&b, " tests=%s", strings.Join(s.Tests, "; "))
			}
			b.WriteByte('\n')
		}
	}
	if len(item.Policy.ProtectedPathsTouched) > 0 {
		fmt.Fprintf(&b, "- Predeclared protected paths: %s\n", strings.Join(item.Policy.ProtectedPathsTouched, ", "))
	}
	return strings.TrimSpace(b.String())
}

// attachWeaverDelegation wires the routed weaver delegator + research
// diff recorder onto wc when MILLS_RESEARCH_VIA_WEAVER is "shadow" or
// "on". Falls back gracefully — every missing piece is a warn log, not
// a startup failure, so the operator can still serve the legacy
// FlexInfer chat path.
//
// Resolution order for the weaver URL:
//  1. LOOM_WEAVER_URL (cfg.WeaverURL)
//  2. LOOM_HUD_URL    (cfg.HUDBaseURL) — same loomd hosts both today
//
// Without a URL, the WeaverClient remains in shadow/on mode but
// without a delegator; flexinfer.go falls back to legacy + records a
// "delegator not configured" diff entry in shadow mode. That's
// intentional: the env knob is the source of truth for "we want the
// shadow signal," and the operator log surfaces the missing URL so
// operators can fix it without flipping the knob back.
func attachWeaverDelegation(wc *clients.WeaverClient, cfg Config, st *store.Store, logger *slog.Logger) {
	weaverURL := strings.TrimSpace(cfg.WeaverURL)
	if weaverURL == "" {
		weaverURL = strings.TrimSpace(cfg.HUDBaseURL)
	}
	if weaverURL == "" {
		logger.Warn("weaver delegation requested but no URL configured",
			"mode", string(wc.Mode),
			"hint", "set LOOM_WEAVER_URL or LOOM_HUD_URL")
		return
	}
	delegator, err := clients.NewWeaverHTTPDelegator(clients.WeaverHTTPConfig{
		BaseURL: weaverURL,
		Token:   cfg.WeaverToken,
		AgentID: "loom-mills-operator",
	})
	if err != nil {
		logger.Warn("weaver delegator init failed; falling back to legacy chat",
			"error", err, "weaver_url", weaverURL)
		return
	}
	wc.Delegator = delegator

	// The recorder is only useful in shadow mode (the diff comparison).
	// On mode delegates fully so there's no diff to record. Wiring it
	// for both modes is harmless but the log noise is cleaner this way.
	if wc.Mode == clients.ResearchModeShadow {
		if st == nil || st.Pipeline == nil {
			logger.Warn("research diff recorder disabled: store unavailable")
		} else {
			wc.DiffRecorder = clients.NewPipelineDAOResearchDiffRecorder(st.Pipeline, logger)
			logger.Info("research diff recorder wired (shadow mode → pipeline_runs.research_diff)")
		}
	}

	logger.Info("weaver delegation wired",
		"mode", string(wc.Mode),
		"weaver_url", weaverURL,
		"recorder_enabled", wc.DiffRecorder != nil,
		"token_set", cfg.WeaverToken != "")
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
			Run:           run,
			Item:          item,
			Stage:         stage,
			Prior:         prior,
			ResumeSpawnID: pipeline.ResumeSpawnIDFromContext(ctx),
			Budget:        item.Budget,
			Env:           pipeline.BuildMillsEnv(run, item, stage),
		}
		return w.Run(ctx, jc)
	}
	return d.fallback.Dispatch(ctx, run, item, stage, prior)
}

type agentContextCaller interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error)
}

// establishHubAndSession constructs the MCP hub client (when
// LOOM_MCP_HUB_URL is set) and tries to register a long-lived
// agent-context session for the operator. The session id is the
// SourceSessionID passed to HandoffClient + WorktreeAllocator so handoff
// packages and worktree-allocate calls have a consistent source.
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

	sessionID, err := startOperatorSession(ctx, hub)
	if err != nil {
		logger.Error("agent_session_start failed; handoff + worktree clients will retry", "error", err)
		return hub, ""
	}
	logger.Info("operator session established", "session_id", sessionID)
	return hub, sessionID
}

func startOperatorSession(ctx context.Context, caller agentContextCaller) (string, error) {
	if caller == nil {
		return "", errors.New("agent_context caller not configured")
	}
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, err := caller.CallTool(startCtx, clients.AgentContextServerName, "agent_session_start", map[string]any{
		"namespace":   "loom-mills",
		"agent_id":    "loom-mills-operator",
		"agent_type":  "operator",
		"description": "loom-mills-operator persistent session (boot " + time.Now().UTC().Format(time.RFC3339) + ")",
	})
	if err != nil {
		return "", err
	}
	sessionID := extractSessionID(body)
	if sessionID == "" {
		return "", fmt.Errorf("agent_session_start returned empty session_id; body_tail=%s", truncateForLog(body, 200))
	}
	return sessionID, nil
}

func runOperatorSessionMaintainer(ctx context.Context, caller agentContextCaller, ref *operatorSessionRef, op *operator, logger *slog.Logger, retryEvery time.Duration) {
	if caller == nil || ref == nil {
		return
	}
	if retryEvery <= 0 {
		retryEvery = 30 * time.Second
	}
	try := func() {
		if ref.SessionID() != "" {
			return
		}
		sessionID, err := startOperatorSession(ctx, caller)
		if err != nil {
			logger.Warn("agent_session_start retry failed", "error", err)
			return
		}
		ref.Set(sessionID)
		if op != nil {
			op.setMCPHubSessionReady(true)
		}
		logger.Info("operator session established after retry", "session_id", sessionID)
	}

	try()
	ticker := time.NewTicker(retryEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			try()
		}
	}
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

// squadRouterAdapter glues the squads-package Router (which returns the
// rich squads.Decision) onto the slimmer mills.SquadRouter contract. The
// indirection keeps pkg/mills free of an import on pkg/mills/squads — the
// operator owns the type translation here.
type squadRouterAdapter struct {
	router *squads.Router
}

func (a *squadRouterAdapter) Pick(ctx context.Context, item *store.BacklogItem) (mills.SquadDecision, error) {
	if a == nil || a.router == nil {
		return mills.SquadDecision{SquadName: squads.FallbackName}, nil
	}
	d, err := a.router.Pick(ctx, item)
	if err != nil {
		return mills.SquadDecision{}, err
	}
	return mills.SquadDecision{
		SquadName:  d.SquadName,
		PathClass:  d.PathClass,
		Confidence: d.Confidence,
		SampleSize: d.SampleSize,
		Reason:     d.Reason,
	}, nil
}

// pipelineMergedHook is the on-merge callback shape pipeline.Runner
// fires after a successful merge. Aliased so the operator can build a
// chain of N hooks from a slice without a wall of identical signatures.
type pipelineMergedHook = func(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error

// chainPipelineMerged composes N hooks. Each hook fires even if a
// previous one errored; the returned error is the FIRST non-nil error
// so the runner's logging surfaces the upstream cause without losing
// downstream signals.
func chainPipelineMerged(hooks ...pipelineMergedHook) pipelineMergedHook {
	return func(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
		var firstErr error
		for _, h := range hooks {
			if h == nil {
				continue
			}
			if err := h(ctx, run, item); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
}

// buildAuditSubsystem stands up the Phase 3 audit dispatcher, queue
// worker, and trigger plumbing when FlexInfer is configured. Returns
// (nil, nil, nil, nil) when any required dependency is missing — the
// operator boots with the audit endpoints in degraded mode.
//
// PoolPolicy defaults are conservative: bulk = Llama 4 70B + Qwen 3 32B
// (both on FlexInfer), escalation = Claude Opus + Codex GPT-5 backed by
// the same flexinfer reviewer (proxy routes by model id). v2.1 will
// load the pool from policy.yaml so operators can rotate without a
// restart.
func buildAuditSubsystem(
	flex *clients.FlexInferClient,
	councilRunner *runner.Runner,
	st *store.Store,
	repoRoot string,
	logger *slog.Logger,
) (*audit.Dispatcher, *audit.QueueWorker, *audit.Triggers, *audit.PoolPolicy) {
	if flex == nil {
		return nil, nil, nil, nil
	}
	reviewer := clients.NewFlexInferAuditReviewer(flex)
	if reviewer == nil {
		return nil, nil, nil, nil
	}
	rubric, err := audit.LoadRubric()
	if err != nil {
		logger.Warn("audit: rubric load failed; subsystem disabled", "error", err)
		return nil, nil, nil, nil
	}
	dispatcher := audit.New(map[string]audit.Reviewer{reviewer.Backend(): reviewer}, rubric)
	dispatcher.Logger = logger

	// Pool defaults align with FlexInfer models actually deployed on the
	// canonical cluster. Prior values (`llama-4-70b-instruct`,
	// `qwen-3-32b`) were absent from /v1/models and 404'd on first audit
	// dispatch. See services/loom-core/.loom/111-product-spec-weaver-
	// qwen3-integration-2026-05-08.md (MW-004); the gitops policy
	// ConfigMap is updated in lockstep at platform/gitops/k3s/mills/
	// configmap-policy.yaml. Escalation entries retain the `flexinfer`
	// backend tag because audit.PoolMember has no driver field today;
	// the policy.AuditPool YAML mirror keeps the per-driver split for
	// the eventual spawn-backend wiring (v2.1).
	policy := &audit.PoolPolicy{
		Bulk: []audit.PoolMember{
			{Backend: "flexinfer", Model: "qwen3-8b"},
			{Backend: "flexinfer", Model: "qwen3-14b-abliterated"},
		},
		Escalation: []audit.PoolMember{
			{Backend: "flexinfer", Model: "claude-opus-4-7"},
			{Backend: "flexinfer", Model: "codex-gpt5"},
		},
	}
	worker := audit.NewQueueWorker(dispatcher, st.Audit, *policy, audit.QueueOptions{
		Capacity: 64,
		Logger:   logger,
	})
	triggers := &audit.Triggers{
		Worker:              worker,
		LoadCouncilArtifact: audit.LoadCouncilArtifactFromFS(repoRoot),
		LoadMergedDiff:      stubMergedDiffLoader(),
		Logger:              logger,
	}
	if councilRunner != nil {
		// Wire the council runner's post-commit hook so successful
		// council artifacts auto-enqueue an audit job. Pipeline merges
		// are wired separately via the pipelineRunner.OnMerged chain.
		councilRunner.OnArtifactsCommitted = triggers.OnArtifactsCommitted
		logger.Info("audit: council post-commit trigger wired")
	}
	logger.Info("audit subsystem enabled",
		"bulk", len(policy.Bulk),
		"escalation", len(policy.Escalation),
	)
	return dispatcher, worker, triggers, policy
}

// stubMergedDiffLoader is a v2.0 placeholder: returns a brief metadata
// summary derived from the run + item state. v2.1 will fetch the real
// unified diff via mcp-gitlab so the rubric scores actual code rather
// than commit metadata. The audit row still produces today; the rubric
// just has less to work with.
func stubMergedDiffLoader() func(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) (string, error) {
	return func(_ context.Context, run *store.PipelineRun, item *store.BacklogItem) (string, error) {
		if run == nil {
			return "", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# Pipeline merge %s\n", run.ID)
		if item != nil {
			fmt.Fprintf(&b, "Backlog: %s — %s\n", item.ID, item.Title)
			if len(item.Slices) > 0 {
				b.WriteString("\n## Slices\n")
				for _, sl := range item.Slices {
					fmt.Fprintf(&b, "- %s — files: %v\n", sl.Name, sl.Files)
				}
			}
		}
		if run.MRIID != nil {
			fmt.Fprintf(&b, "\nMR iid: %d\n", *run.MRIID)
		}
		fmt.Fprintf(&b, "\nCost: $%.2f, attempts: %d\n", run.CostUSD, run.Attempts)
		return b.String(), nil
	}
}

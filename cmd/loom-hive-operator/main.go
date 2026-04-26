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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/clients"
	"github.com/crb2nu/loom/pkg/hive/council"
	"github.com/crb2nu/loom/pkg/hive/eval"
	"github.com/crb2nu/loom/pkg/hive/gates"
	"github.com/crb2nu/loom/pkg/hive/pipeline"
	"github.com/crb2nu/loom/pkg/hive/runner"
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

	// Council runner. Reviewers + editor are FakeReviewer + FakeEditor
	// for slice 3.7 — they make /api/hive/council/{run,dryrun} produce
	// real artifacts + sidecar + eval row + backlog mutations end to
	// end without a live agent. Production wiring swaps in spawn-backed
	// implementations in a follow-up slice once the spawn integration
	// for FlexInfer + Claude/Codex headless is wired.
	councilRunner := buildCouncilRunner(st, pm, budget, cfg.RepoRoot, logger)

	op := newOperator(st, pm, budget, logger).withRunner(councilRunner)
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

	// Worker dispatcher: real clients where configured, NoOpDispatcher
	// for stages whose backing service isn't wired yet (spawn, devbox,
	// agent-context handoff/worktree). The operator logs each gap so
	// it's obvious which surfaces are stub vs production.
	dispatcher := buildDispatcher(cfg, flexClient, logger)

	pipelineRunner := pipeline.New(st, gateRegistry, dispatcher, pm)
	pipelineRunner.Logger = logger
	attributor := eval.NewOutcomeAttributor(st)
	pipelineRunner.OnMerged = attributor.OnMerged

	// Escalator: only enabled when GitLab is configured (issues + handoff
	// posting need a real backend). Without it the runner still
	// transitions to escalated state, just without the human-handoff
	// publication.
	if gitlabClient := buildGitLabClient(cfg, logger); gitlabClient != nil {
		escalator := pipeline.NewEscalator(st, gitlabClient, nil)
		escalator.Logger = logger
		pipelineRunner.Escalator = escalator
		logger.Info("escalator enabled (GitLab issues)")
	} else {
		logger.Warn("escalator disabled; failures will transition to escalated state without issue/handoff (set GITLAB_API_URL+GITLAB_TOKEN+GITLAB_PROJECT)")
	}

	// Pipeline starter routes fan-out items through the integrator
	// when one is configured. The integrator's WorktreeAllocator and
	// BranchMerger remain stubs until the agent-context bridge ships,
	// so for now single-slice items are the only ones that fully
	// execute.
	starter := pipeline.NewRunnerStarter(pipelineRunner, nil)
	starter.Logger = logger

	// Reconciler / scheduler. The reconciler hands queued items to the
	// pipeline starter (which spawns goroutines that drive the DAG and
	// fire OnMerged → eval Loop B per merge).
	reconciler := hive.NewReconciler(st, pm, budget, starter)
	reconciler.Logger = logger
	scheduler := hive.NewScheduler(reconciler)
	scheduler.Logger = logger

	g, gctx := errgroup.WithContext(rootCtx)
	g.Go(func() error { return runListener(gctx, "http", httpSrv, logger) })
	g.Go(func() error { return runListener(gctx, "metrics", metricsSrv, logger) })
	g.Go(func() error { return scheduler.Run(gctx) })

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
// Stub gaps as of this slice (need follow-up work):
//   - SpawnClient:   spawn lives in HUD process; no in-cluster HTTP API.
//   - DevboxClient:  mcp-devbox is stdio-only.
//   - HandoffClient: mcp-agent-context is stdio-only.
//   - WorktreeAllocator: same as HandoffClient.
//
// Until those bridges land, the routes table omits real clients for
// plan_slice/implement/pr_self_review/tests and falls back to the
// NoOpDispatcher's deterministic outputs.
func buildDispatcher(cfg Config, flex *clients.FlexInferClient, logger *slog.Logger) pipeline.WorkerDispatcher {
	noop := &pipeline.NoOpDispatcher{}
	gitlab := buildGitLabClient(cfg, logger)

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
	logger.Warn("plan_slice/implement/pr_self_review/tests stages stub: NoOpDispatcher (spawn/devbox bridges not yet shipped)")

	return &fallbackDispatcher{routes: routes, fallback: noop}
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

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/audit"
	"github.com/crb2nu/loom/pkg/hive/gates"
	"github.com/crb2nu/loom/pkg/hive/runner"
	"github.com/crb2nu/loom/pkg/hive/squads"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// operator owns the state shared between HTTP handlers — the canonical
// store, the policy manager, the budget enforcer, and (slice 3.7+) the
// council runner that orchestrates an end-to-end planning pass.
type operator struct {
	store          *store.Store
	policy         *hive.PolicyManager
	budget         *hive.Budget
	runner         *runner.Runner        // optional; nil disables /api/hive/council/{run,dryrun}
	regressionGate *gates.RegressionGate // optional; nil makes the alerts webhook return 503
	squadsLoader   *squads.Loader        // optional; nil makes squad endpoints return empty / 404

	// Audit (Phase 3). Set by main.go when FlexInfer is configured + a
	// reviewer is registered; nil leaves the read endpoints serving
	// canonical-store rows and the admin /run endpoint returning 503.
	auditDispatcher *audit.Dispatcher
	auditWorker     *audit.QueueWorker
	auditTriggers   *audit.Triggers
	auditPolicy     *audit.PoolPolicy

	logger *slog.Logger

	ready atomic.Bool
}

func newOperator(st *store.Store, pm *hive.PolicyManager, b *hive.Budget, logger *slog.Logger) *operator {
	return &operator{
		store:  st,
		policy: pm,
		budget: b,
		logger: logger,
		// Default regression gate: same store + policy + default 30min
		// window. Tests that want to skip the gate clear this field.
		regressionGate: &gates.RegressionGate{Store: st, Policy: pm},
	}
}

// withRunner attaches a council runner. Operators that don't want
// council functionality (e.g. the stub used by handler tests) leave
// runner unset and the council POST endpoints respond 503.
func (o *operator) withRunner(r *runner.Runner) *operator {
	o.runner = r
	return o
}

// withSquadsLoader attaches a squads.Loader. nil leaves the loader unset
// so the squad endpoints return empty list / 404 — the operator still
// boots cleanly when no squad manifests are mounted.
func (o *operator) withSquadsLoader(l *squads.Loader) *operator {
	o.squadsLoader = l
	return o
}

// withAudit attaches the Phase 3 audit subsystem: dispatcher, queue
// worker, triggers, and the pool policy used when admin re-runs default
// to the policy ensemble. Any nil leaves the audit endpoints in their
// degraded state (read-only via canonical store; /run returns 503).
func (o *operator) withAudit(d *audit.Dispatcher, w *audit.QueueWorker, t *audit.Triggers, p *audit.PoolPolicy) *operator {
	o.auditDispatcher = d
	o.auditWorker = w
	o.auditTriggers = t
	o.auditPolicy = p
	return o
}

// markReady flips the readyz response from 503 to 200. Called once startup
// completes so Kubernetes only routes traffic after migrations + initial
// policy load are done.
func (o *operator) markReady() { o.ready.Store(true) }

// httpMux returns the REST + MCP listener mux. Read-only routes are
// open; mutating routes are wrapped in requireAdmin so they reject
// every caller when LOOM_HIVE_ADMIN_TOKEN is unset and require a Bearer
// match when it isn't.
func (o *operator) httpMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Status / policy / KPIs (read-only).
	mux.HandleFunc("GET /api/hive/status", o.handleStatusFull)
	mux.HandleFunc("GET /api/hive/policy", o.handlePolicy)
	mux.HandleFunc("GET /api/hive/kpis", o.handleKPIs)

	// Council.
	mux.HandleFunc("GET /api/hive/council/runs", o.handleCouncilRunsList)
	mux.HandleFunc("GET /api/hive/council/runs/{id}", o.handleCouncilRunGet)
	mux.HandleFunc("POST /api/hive/council/run", requireAdmin(o.handleCouncilRun))
	mux.HandleFunc("POST /api/hive/council/dryrun", requireAdmin(o.handleCouncilDryrun))

	// Pipeline.
	mux.HandleFunc("GET /api/hive/pipeline/runs", o.handlePipelineRunsList)
	mux.HandleFunc("GET /api/hive/pipeline/runs/{id}", o.handlePipelineRunGet)
	mux.HandleFunc("POST /api/hive/pipeline/runs/{backlog_id}/start", requireAdmin(o.handlePipelineStart))
	mux.HandleFunc("POST /api/hive/pipeline/runs/{id}/pause", requireAdmin(o.handlePipelinePause))
	mux.HandleFunc("POST /api/hive/pipeline/runs/{id}/resume", requireAdmin(o.handlePipelineResume))
	mux.HandleFunc("POST /api/hive/pipeline/runs/{id}/escalate", requireAdmin(o.handlePipelineEscalate))

	// Backlog.
	mux.HandleFunc("GET /api/hive/backlog", o.handleBacklogList)
	mux.HandleFunc("GET /api/hive/backlog/{id}", o.handleBacklogGet)
	mux.HandleFunc("POST /api/hive/backlog", requireAdmin(o.handleBacklogCreate))
	mux.HandleFunc("POST /api/hive/backlog/sync", requireAdmin(o.handleBacklogSync))

	// Squads (Phase 2 slice 2.4). Read endpoints are open; route-test is
	// admin-gated because it loads + executes the live router which is
	// otherwise an internal call surface.
	mux.HandleFunc("GET /api/hive/squads", o.handleSquadsList)
	mux.HandleFunc("GET /api/hive/squads/{name}", o.handleSquadGet)
	mux.HandleFunc("GET /api/hive/squads/{name}/memory", o.handleSquadMemory)
	mux.HandleFunc("GET /api/hive/squads/{name}/outcomes", o.handleSquadOutcomes)
	mux.HandleFunc("POST /api/hive/squads/{name}/route-test", requireAdmin(o.handleSquadRouteTest))

	// Eval.
	mux.HandleFunc("GET /api/hive/eval/scores", o.handleEvalScores)
	mux.HandleFunc("POST /api/hive/eval/run-cross", requireAdmin(o.handleEvalRunCross))

	// Audit (Phase 3 slice 3.4). Read endpoints serve canonical-store
	// rows even when the dispatcher isn't wired (HUD never sees a 503
	// on a poll). The admin /run endpoint requires the dispatcher +
	// queue worker; without them it returns 503 with a clear message.
	mux.HandleFunc("GET /api/hive/audit/findings", o.handleAuditFindings)
	mux.HandleFunc("GET /api/hive/audit/findings/{id}", o.handleAuditFindingDetails)
	mux.HandleFunc("POST /api/hive/audit/run", requireAdmin(o.handleAuditRun))

	// Regression gate (slice 6.3): Alertmanager webhook target. Admin-
	// gated so a misconfigured external pushes can't bump our metric.
	mux.HandleFunc("POST /api/hive/alerts/regression", requireAdmin(o.handleRegressionAlert))

	// Anything else under /api/hive returns 404 with a clear message; the
	// catch-all "/" stays 501 so unprefixed paths don't get mistaken for
	// missing API routes.
	mux.HandleFunc("/api/hive/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "operator REST root; see /api/hive/status", http.StatusNotFound)
	})
	return mux
}

// metricsMux returns the lifecycle listener mux: /healthz, /readyz, /metrics.
// Kept on a separate listener so health probes don't queue behind real
// traffic and so a misbehaving handler can't break liveness.
func (o *operator) metricsMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", o.handleHealth)
	mux.HandleFunc("/readyz", o.handleReady)
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

func (o *operator) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if err := o.store.DB().Ping(); err != nil {
		http.Error(w, "db unreachable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (o *operator) handleReady(w http.ResponseWriter, _ *http.Request) {
	if !o.ready.Load() {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

// (handleStatus stub removed — slice 2.4 wires handleStatusFull which
// pulls real values from the canonical store.)

// httpServer constructs an http.Server with sensible timeouts.
func httpServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

// runListener starts an HTTP listener and blocks until ctx cancels or the
// server returns an unrecoverable error.
func runListener(ctx context.Context, label string, srv *http.Server, logger *slog.Logger) error {
	if srv.Addr == "" {
		return nil
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listener starting", "label", label, "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("%s: %w", label, err)
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

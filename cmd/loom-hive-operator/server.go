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
	"github.com/crb2nu/loom/pkg/hive/runner"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// operator owns the state shared between HTTP handlers — the canonical
// store, the policy manager, the budget enforcer, and (slice 3.7+) the
// council runner that orchestrates an end-to-end planning pass.
type operator struct {
	store  *store.Store
	policy *hive.PolicyManager
	budget *hive.Budget
	runner *runner.Runner // optional; nil disables /api/hive/council/{run,dryrun}
	logger *slog.Logger

	ready atomic.Bool
}

func newOperator(st *store.Store, pm *hive.PolicyManager, b *hive.Budget, logger *slog.Logger) *operator {
	return &operator{store: st, policy: pm, budget: b, logger: logger}
}

// withRunner attaches a council runner. Operators that don't want
// council functionality (e.g. the stub used by handler tests) leave
// runner unset and the council POST endpoints respond 503.
func (o *operator) withRunner(r *runner.Runner) *operator {
	o.runner = r
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
	mux.HandleFunc("POST /api/hive/backlog/sync", requireAdmin(o.handleBacklogSync))

	// Eval.
	mux.HandleFunc("GET /api/hive/eval/scores", o.handleEvalScores)
	mux.HandleFunc("POST /api/hive/eval/run-cross", requireAdmin(o.handleEvalRunCross))

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

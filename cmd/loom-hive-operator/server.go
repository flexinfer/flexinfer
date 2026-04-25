package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// operator owns the state shared between HTTP handlers — the canonical store,
// the policy manager, and the budget enforcer. Slice 2.4 replaces the
// placeholder REST handlers with the full surface; this slice exposes only
// the lifecycle endpoints (healthz / readyz / metrics) plus a stub status
// endpoint so smoke tests can confirm wiring.
type operator struct {
	store  *store.Store
	policy *hive.PolicyManager
	budget *hive.Budget
	logger *slog.Logger

	ready atomic.Bool
}

func newOperator(st *store.Store, pm *hive.PolicyManager, b *hive.Budget, logger *slog.Logger) *operator {
	return &operator{store: st, policy: pm, budget: b, logger: logger}
}

// markReady flips the readyz response from 503 to 200. Called once startup
// completes so Kubernetes only routes traffic after migrations + initial
// policy load are done.
func (o *operator) markReady() { o.ready.Store(true) }

// httpMux returns the REST + MCP listener mux. Currently exposes a tiny stub
// surface; slice 2.4 wires the full /api/hive/* routes.
func (o *operator) httpMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/hive/status", o.handleStatus)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented (slice 2.4)", http.StatusNotImplemented)
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

// handleStatus is a placeholder that proves the server is wired end-to-end.
// Slice 2.4 replaces it with the full /api/hive/status contract from the
// product spec (budgets remaining, queue depth, last council run, etc.).
func (o *operator) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]any{
		"db_ok":                o.store != nil,
		"policy_enabled":       o.policy.Current().IsEnabled(),
		"policy_version":       o.policy.Current().Version,
		"queue_depth":          nil, // slice 2.4
		"last_council_at":      nil, // slice 2.4
		"active_pipeline_runs": nil, // slice 2.4
		"slice":                "1.2-skeleton",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

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

package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/crb2nu/loom/pkg/mills/gates"
)

// alertmanagerPayload is the subset of the v4 Alertmanager webhook
// payload the regression gate consumes. Alertmanager sends every
// notification as a batch; we iterate over Alerts. Fields we don't
// use (groupKey, externalURL, commonLabels) are omitted but tolerated
// by encoding/json's default ignore-unknown behavior.
type alertmanagerPayload struct {
	Version string                  `json:"version"`
	Status  string                  `json:"status"` // "firing" | "resolved"
	Alerts  []alertmanagerAlertJSON `json:"alerts"`
}

type alertmanagerAlertJSON struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
}

// regressionResponse is what the handler returns to the webhook caller.
// One entry per alert correlated, regardless of whether the gate found
// any merge to attribute.
type regressionResponse struct {
	Processed int                       `json:"processed"`
	Results   []*gates.RegressionResult `json:"results"`
}

// handleRegressionAlert is the Alertmanager webhook entry point. The
// gate path is admin-only — Alertmanager is configured with a Bearer
// token in its receiver config so the same `requireAdmin` middleware
// gates this just like the rest of the mutating routes.
//
// We always return 200 on a successfully decoded payload even when the
// gate finds no correlated merges; Alertmanager would otherwise retry
// indefinitely on any non-2xx status, which would re-bump our metric.
func (o *operator) handleRegressionAlert(w http.ResponseWriter, r *http.Request) {
	if o.regressionGate == nil {
		// Operator booted without a regression gate (e.g. handler-only
		// test fixture). Surface a clear 503 so Alertmanager backs off.
		http.Error(w, "regression gate not configured", http.StatusServiceUnavailable)
		return
	}
	defer r.Body.Close()
	var payload alertmanagerPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid alertmanager payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	resp := regressionResponse{Results: make([]*gates.RegressionResult, 0, len(payload.Alerts))}
	for _, a := range payload.Alerts {
		ev := gates.AlertEvent{
			Name:        a.Labels["alertname"],
			Severity:    a.Labels["severity"],
			StartsAt:    a.StartsAt,
			Status:      firstNonEmpty(a.Status, payload.Status),
			Labels:      a.Labels,
			Annotations: a.Annotations,
		}
		out, err := o.regressionGate.OnAlert(r.Context(), ev)
		if err != nil {
			// Don't fail the whole batch on one error — log + continue
			// so a transient store hiccup doesn't drop the rest.
			if o.logger != nil {
				o.logger.Warn("regression gate: OnAlert failed",
					"alert", ev.Name, "error", err)
			}
			continue
		}
		resp.Results = append(resp.Results, out)
		resp.Processed++
	}
	writeJSON(w, http.StatusOK, resp)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

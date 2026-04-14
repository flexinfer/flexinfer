package daemon

import (
	"context"
	"encoding/json"
	"slices"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type auditTraceSummary struct {
	Count   int     `json:"count"`
	Errors  int     `json:"errors"`
	Denied  int     `json:"denied"`
	Cached  int     `json:"cached"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	Slowest int64   `json:"slowest_ms"`
}

type auditTraceResult struct {
	Enabled bool              `json:"enabled"`
	Path    string            `json:"path,omitempty"`
	Count   int               `json:"count"`
	Limit   int               `json:"limit"`
	Summary auditTraceSummary `json:"summary"`
	Traces  []AuditEntry      `json:"traces"`
}

func (d *Daemon) handleAuditTraces(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		Limit int `json:"limit,omitempty"`
	}
	if len(msg.Params) > 0 {
		_ = json.Unmarshal(msg.Params, &params)
	}
	_ = ctx

	limit := params.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}

	if d.audit == nil || d.audit.Path() == "" {
		return mcp.NewResponse(msg.ID, auditTraceResult{
			Enabled: false,
			Limit:   limit,
			Traces:  []AuditEntry{},
		})
	}

	entries, err := d.audit.ReadEntries(AuditReadOptions{Limit: limit})
	if err != nil {
		return nil, err
	}
	slices.Reverse(entries)

	result := auditTraceResult{
		Enabled: true,
		Path:    d.audit.Path(),
		Count:   len(entries),
		Limit:   limit,
		Summary: summarizeAuditTraceEntries(entries),
		Traces:  entries,
	}
	if result.Traces == nil {
		result.Traces = []AuditEntry{}
	}
	return mcp.NewResponse(msg.ID, result)
}

func summarizeAuditTraceEntries(entries []AuditEntry) auditTraceSummary {
	if len(entries) == 0 {
		return auditTraceSummary{}
	}

	durations := make([]float64, 0, len(entries))
	summary := auditTraceSummary{Count: len(entries)}
	for _, entry := range entries {
		durations = append(durations, float64(entry.DurationMs))
		switch entry.Status {
		case "error":
			summary.Errors++
		case "denied":
			summary.Denied++
		}
		if entry.Cached {
			summary.Cached++
		}
		if entry.DurationMs > summary.Slowest {
			summary.Slowest = entry.DurationMs
		}
	}

	slices.Sort(durations)
	summary.P50Ms = percentileDuration(durations, 0.50)
	summary.P95Ms = percentileDuration(durations, 0.95)
	return summary
}

func percentileDuration(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return values[0]
	}
	if quantile <= 0 {
		return values[0]
	}
	if quantile >= 1 {
		return values[len(values)-1]
	}
	pos := quantile * float64(len(values)-1)
	lower := int(pos)
	upper := lower + 1
	if upper >= len(values) {
		return values[len(values)-1]
	}
	frac := pos - float64(lower)
	return values[lower] + frac*(values[upper]-values[lower])
}

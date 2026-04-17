package agentcontext

import (
	"context"
	"log/slog"
	"strings"
)

// =========================================================================
// Recall Reranker (Slice A1)
//
// The Reranker re-orders candidate ContextEntry items returned by hybrid
// recall to push the most relevant entries to the top. Backends pluggable
// via the WEAVER_RERANKER env var:
//
//   - "off" (default)      -> NoopReranker, returns entries unchanged.
//   - "flexinfer"          -> FlexInferReranker, calls the flexinfer
//                             /v1/rerank proxy endpoint.
//   - "bge"                -> BGEReranker, cross-encoder via the same
//                             flexinfer proxy with model bge-reranker-v2-m3.
//
// Integration notes:
//   - This slice wires ONLY the abstraction, config, metrics, and an
//     opt-in ApplyReranker helper on *Service. It is not yet called from
//     HandleUnifiedRecall / enhancedRecallContext — that happens in a
//     follow-up slice once A1+A2+A3 land to avoid merge conflicts.
//   - On any error (timeout, network, bad response), backends MUST return
//     the entries unchanged and set Metadata["rerank_status"] on the
//     returned slice elements so callers can audit behaviour.
// =========================================================================

// RerankerKind is the selected reranker backend identifier.
type RerankerKind string

const (
	// RerankerKindOff disables reranking. Default for the initial rollout.
	RerankerKindOff RerankerKind = "off"
	// RerankerKindFlexInfer uses the flexinfer /v1/rerank proxy endpoint.
	RerankerKindFlexInfer RerankerKind = "flexinfer"
	// RerankerKindBGE uses a BGE cross-encoder via the flexinfer proxy
	// with the model name bge-reranker-v2-m3.
	RerankerKindBGE RerankerKind = "bge"
)

// Reranker re-orders ContextEntry results so the most relevant entries
// surface first. Implementations MUST NOT mutate ordering when an error
// (including ctx timeout) occurs — return the original slice unchanged and
// let the caller observe status via entry Metadata / Metrics counters.
type Reranker interface {
	// Backend returns a short identifier used for metrics labels/logging
	// (e.g. "off", "flexinfer", "bge").
	Backend() string

	// Rerank returns entries reordered by relevance to query. The returned
	// slice length MUST equal len(entries). On timeout / error the
	// implementation MUST return the entries unchanged (same order) and a
	// nil error, annotating entries with Metadata["rerank_status"] so
	// callers can observe degraded behaviour without failing the recall.
	Rerank(ctx context.Context, query string, entries []ContextEntry) ([]ContextEntry, error)
}

// NewReranker returns a Reranker for the given kind. Unknown or empty
// kinds fall through to RerankerKindOff (NoopReranker). The logger may be
// nil — implementations route through slog.Default() in that case.
func NewReranker(kind RerankerKind, cfg RerankerConfig, logger *slog.Logger) Reranker {
	if logger == nil {
		logger = slog.Default()
	}

	switch RerankerKind(strings.ToLower(string(kind))) {
	case RerankerKindFlexInfer:
		return newFlexInferReranker(cfg, logger)
	case RerankerKindBGE:
		return newBGEReranker(cfg, logger)
	case RerankerKindOff, "":
		return NoopReranker{}
	default:
		logger.Warn("unknown reranker kind; falling back to off", "kind", string(kind))
		return NoopReranker{}
	}
}

// NoopReranker is the zero-cost default — returns entries unchanged and
// performs no network I/O. It is the only backend considered safe when
// WEAVER_RERANKER=off.
type NoopReranker struct{}

// Backend implements Reranker.
func (NoopReranker) Backend() string { return string(RerankerKindOff) }

// Rerank implements Reranker. Always returns entries unchanged.
func (NoopReranker) Rerank(_ context.Context, _ string, entries []ContextEntry) ([]ContextEntry, error) {
	return entries, nil
}

// annotateRerankStatus sets Metadata["rerank_status"] on every entry in the
// supplied slice. Used by backends to mark degraded responses (timeout /
// error) so downstream callers can audit or trace the behaviour.
func annotateRerankStatus(entries []ContextEntry, status string) []ContextEntry {
	if status == "" || len(entries) == 0 {
		return entries
	}
	for i := range entries {
		if entries[i].Metadata == nil {
			entries[i].Metadata = map[string]any{}
		}
		entries[i].Metadata["rerank_status"] = status
	}
	return entries
}

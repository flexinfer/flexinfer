package agentcontext

import (
	"context"
	"log/slog"
)

// =========================================================================
// BGE reranker (CPU cross-encoder)
//
// Implemented as a thin wrapper around FlexInferReranker: the same proxy
// endpoint (/v1/rerank) is used, with the model name pinned to
// bge-reranker-v2-m3 per the spec. Routing this through the flexinfer
// proxy keeps a single transport + auth path and reuses the timeout /
// metric plumbing.
//
// Tradeoff: we intentionally do NOT ship a standalone ONNX runtime or
// HuggingFace loader in this slice. A CPU-local cross-encoder inside the
// Go process is out of scope for F1 — the flexinfer sidecar already runs
// bge-reranker-v2-m3 and is reachable over HTTP. If/when local inference
// is desired, this file is the seam to add it.
// =========================================================================

// BGEReranker reranks via the flexinfer proxy pinned to
// bge-reranker-v2-m3.
type BGEReranker struct {
	inner *FlexInferReranker
}

// newBGEReranker builds a BGEReranker. If RerankerConfig.Model is empty we
// pin to bge-reranker-v2-m3 (the production CPU cross-encoder used in the
// flexinfer stack).
func newBGEReranker(cfg RerankerConfig, logger *slog.Logger) *BGEReranker {
	if cfg.Model == "" {
		cfg.Model = "bge-reranker-v2-m3"
	}
	inner := newFlexInferReranker(cfg, logger)
	inner.backendTag = string(RerankerKindBGE)
	inner.logger = logger.With("component", "agentcontext-reranker", "backend", "bge")
	return &BGEReranker{inner: inner}
}

// Backend implements Reranker.
func (r *BGEReranker) Backend() string { return string(RerankerKindBGE) }

// Rerank implements Reranker.
func (r *BGEReranker) Rerank(ctx context.Context, query string, entries []ContextEntry) ([]ContextEntry, error) {
	return r.inner.Rerank(ctx, query, entries)
}

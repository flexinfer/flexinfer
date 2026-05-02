package clients

import (
	"context"
	"errors"
)

// FlexInferAuditReviewer adapts *FlexInferClient onto the audit.Reviewer
// contract. It's a trivial wrapper — the real plumbing (HTTP, retries,
// cost estimation) lives in FlexInferClient.Chat — so we keep the type
// alongside the other adapters in this package and the audit package
// stays free of an HTTP dependency.
//
// The default backend name is "flexinfer"; the audit.Dispatcher matches
// PoolMember.Backend to this string when registering reviewers. Users
// who want to label different FlexInfer model pools differently (e.g.
// "flexinfer-bulk" vs. "flexinfer-frontier") can construct multiple
// reviewers with different BackendName values pointing at the same
// underlying client.
type FlexInferAuditReviewer struct {
	Client *FlexInferClient

	// BackendName overrides the default "flexinfer" identifier. Only
	// matters when the operator registers more than one FlexInfer pool;
	// production today uses the default.
	BackendName string

	// MaxTokens caps each audit completion. 0 → 2048, large enough for
	// the ≤12-finding rubric without runaway output.
	MaxTokens int
}

// NewFlexInferAuditReviewer wires a reviewer for the audit.Dispatcher.
// Returns nil when the underlying client is nil so the operator can
// skip registration cleanly when FLEXINFER_PROXY_URL isn't set.
func NewFlexInferAuditReviewer(c *FlexInferClient) *FlexInferAuditReviewer {
	if c == nil {
		return nil
	}
	return &FlexInferAuditReviewer{Client: c, BackendName: "flexinfer", MaxTokens: 2048}
}

// Backend identifies the PoolMember.Backend value this reviewer handles.
func (r *FlexInferAuditReviewer) Backend() string {
	if r == nil || r.BackendName == "" {
		return "flexinfer"
	}
	return r.BackendName
}

// Review issues a single completion against the configured FlexInfer
// proxy. The auditor's structured response is returned verbatim — the
// audit.Dispatcher parses it and folds findings into the row.
//
// maxCostUSD is currently advisory: the underlying chat call is bounded
// by MaxTokens, and the budget enforcer pre-checks before this call. We
// surface the parameter on the interface so a future per-call cap can
// land without a contract change.
func (r *FlexInferAuditReviewer) Review(ctx context.Context, model, prompt string, _ float64) (string, float64, error) {
	if r == nil || r.Client == nil {
		return "", 0, errors.New("clients: flexinfer audit reviewer not configured")
	}
	maxTokens := r.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	return r.Client.Chat(ctx, model, prompt, maxTokens)
}

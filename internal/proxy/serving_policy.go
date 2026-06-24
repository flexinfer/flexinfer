package proxy

import (
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

// litellmExplicitlyDisabled reports whether a Model opted OUT of the servable
// fleet via spec.litellm.enabled=false. Such "parked" models are hidden from
// /v1/models, are not resolvable by alias, and are never cold-started by name —
// so a stale client or prober hitting one cannot build a queue or trigger
// shared-GPU preemption for a model we do not intend to serve.
//
// litellm.enabled defaults to true, so a nil Enabled (or a nil LiteLLM block)
// means the model IS part of the fleet. Only an explicit false parks it.
func litellmExplicitlyDisabled(m *aiv1alpha2.Model) bool {
	return m != nil && m.Spec.LiteLLM != nil &&
		m.Spec.LiteLLM.Enabled != nil && !*m.Spec.LiteLLM.Enabled
}

// parkedBehindPrimary reports whether a Model is statically un-promotable on its
// shared GPU: the controller's election determined a warm-pinned/warm-primary
// leader of higher priority holds the single slot and never idles, so this
// member can never win the demand-swap. The election encodes that by prefixing
// SharedGroupStatus.PreemptedBy with PreemptedByPrimaryPrefix ("primary/").
//
// A request for such a model must NOT cold-start it: the controller parks the
// backend the instant it spawns (signal: terminated), so every attempt is a
// guaranteed 10-25m timeout plus GPU-runtime churn. The proxy fast-fails (503)
// instead — no queue, no cold-start, no demand-touch. The prefix is cleared the
// moment the member becomes promotable (leader vacates or priorities change), so
// the gate self-heals. The bare PreemptedBy name (no prefix) means an ordinary
// transient preemption that demand CAN promote, so it is left to cold-start.
func parkedBehindPrimary(m *aiv1alpha2.Model) bool {
	return m != nil && m.Status.SharedGroup != nil &&
		strings.HasPrefix(m.Status.SharedGroup.PreemptedBy, aiv1alpha2.PreemptedByPrimaryPrefix)
}

// modelServesPath reports whether a Model serves the given request path.
//
// A Model may declare the inference endpoints it serves via the
// flexinfer.ai/serve-paths annotation (comma-separated path prefixes, e.g.
// "/v1/audio/transcriptions" for an ASR-only model). When set, a request whose
// path matches none of the prefixes is rejected at the edge WITHOUT serving,
// cold-starting, or touching demand — this stops e.g. chat-completion probes
// from warming an audio-only model (whisper) and preempting its shared GPU.
//
// When the annotation is absent or empty the model serves all paths (the
// default), preserving existing behavior for every other model.
func modelServesPath(m *aiv1alpha2.Model, path string) bool {
	if m == nil {
		return true
	}
	raw := m.Annotations[constants.AnnotationServePaths]
	if strings.TrimSpace(raw) == "" {
		return true
	}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

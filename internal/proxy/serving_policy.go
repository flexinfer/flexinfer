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

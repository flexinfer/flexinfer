package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Unavailable reroute (G1 of .loom/10-research-voice-stack-and-gpu-placement-2026-10-30.md §8).
//
// A Model annotated with flexinfer.ai/unavailable-reroute-to names an
// equivalent lane, usually the same weights on another card. When a request
// names the Model while it cannot serve and waiting would not help — it is
// Failed (GPU-degraded or missing node, failed load), or parked on its shared
// GPU behind a holder its demand cannot beat — and the equivalent is Ready
// with a closed breaker, the proxy serves the request once on the equivalent
// instead of fast-failing or queueing for a cold start. In every other case,
// including a target that is not Ready, the request takes its normal path
// unchanged, so the Model's own cold start still records the demand that
// failover-for stand-ins inherit (controllers/model_shared_gpu.go).
//
// Mills and LiteLLM's alias rewrite address lanes by concrete name, so the
// label-group fallback never sees their requests; this annotation is what
// moves them. The reroute happens once: the target is served directly and its
// own unavailable-reroute-to is never consulted.
const annotationUnavailableRerouteTo = "flexinfer.ai/unavailable-reroute-to"

// headerUnavailableReroute tells the caller which lane served the request.
const headerUnavailableReroute = "X-FlexInfer-Unavailable-Reroute"

// Reasons a Model cannot serve now, as logged and counted.
const (
	unavailableFailed    = "failed"
	unavailableParked    = "parked"
	unavailablePreempted = "preempted"
)

// unavailableReason reports why m cannot serve without waiting on something
// its demand cannot move, or "" when its own path (cold start) is the right
// one:
//   - failed: Phase Failed;
//   - parked: its shared-group PreemptedBy carries a park sentinel —
//     primary/<leader> (statically behind a warm leader), gpu-lease/<owner>
//     or gaming/<session> — none of which demand can lift (Model names never
//     contain "/");
//   - preempted: queued behind a Model of strictly higher gpu.priority, which
//     keeps the card until it idles out.
//
// An equal- or lower-priority holder yields to m's demand, so that case keeps
// the cold start.
func (p *Proxy) unavailableReason(ctx context.Context, m *aiv1alpha2.Model) string {
	if m.Status.Phase == aiv1alpha2.ModelPhaseFailed {
		return unavailableFailed
	}
	sg := m.Status.SharedGroup
	if sg == nil || sg.State == "Active" || sg.PreemptedBy == "" {
		return ""
	}
	if strings.Contains(sg.PreemptedBy, "/") {
		return unavailableParked
	}
	holder, err := p.getModel(ctx, sg.PreemptedBy)
	if err != nil {
		return ""
	}
	if holder.Spec.GetPriority() > m.Spec.GetPriority() {
		return unavailablePreempted
	}
	return ""
}

// unavailableRerouteTarget returns m's equivalent lane, or "" when m names
// none (the default: no behaviour change) or names itself.
func (p *Proxy) unavailableRerouteTarget(ctx context.Context, m *aiv1alpha2.Model) string {
	target := strings.TrimSpace(m.Annotations[annotationUnavailableRerouteTo])
	if target == "" {
		return ""
	}
	if p.resolver != nil {
		target = p.resolver.ResolveModelAlias(ctx, target)
	}
	if target == m.Name {
		return ""
	}
	return target
}

// rerouteUnavailable serves a request for m once on its
// flexinfer.ai/unavailable-reroute-to lane when m cannot serve now and that
// lane can. It reports whether it served the request; false leaves the
// request untouched for the normal path.
func (p *Proxy) rerouteUnavailable(w http.ResponseWriter, r *http.Request, m *aiv1alpha2.Model, start time.Time) bool {
	ctx := r.Context()
	target := p.unavailableRerouteTarget(ctx, m)
	if target == "" {
		return false
	}
	reason := p.unavailableReason(ctx, m)
	if reason == "" {
		return false
	}
	tm, err := p.getModel(ctx, target)
	if err != nil || tm.Status.Phase != aiv1alpha2.ModelPhaseReady || !modelServesPath(tm, r.URL.Path) {
		// Never cold-start the equivalent from here: on a shared GPU that
		// could preempt its leader. A failover-for stand-in is promoted by
		// the election from the demand this request leaves on m instead.
		phase := ""
		if tm != nil {
			phase = string(tm.Status.Phase)
		}
		slog.Debug("unavailable reroute target not servable; taking the normal path",
			"model", m.Name, "target", target, "reason", reason, "phase", phase, "error", err)
		unavailableRerouteTotal.WithLabelValues(m.Name, target, reason, "target_unavailable").Inc()
		return false
	}
	// Serve under the target's lane, as the context-overflow replay does:
	// load its first-byte budget and stay off it while its breaker is open.
	if p.laneConfig.LaneBreakerThreshold > 0 {
		if open, _, _ := p.prepareLane(target, tm.Annotations); open {
			slog.Debug("unavailable reroute target breaker open; taking the normal path",
				"model", m.Name, "target", target, "reason", reason)
			unavailableRerouteTotal.WithLabelValues(m.Name, target, reason, "target_unavailable").Inc()
			return false
		}
	}

	slog.Info("unavailable reroute",
		append([]any{
			"event", "unavailable_reroute",
			"model", m.Name,
			"target", target,
			"reason", reason,
			"path", r.URL.Path,
		}, callerIdentityFrom(r).logAttrs()...)...)
	unavailableRerouteTotal.WithLabelValues(m.Name, target, reason, "rerouted").Inc()

	w.Header().Set(headerUnavailableReroute, m.Name+"->"+target)
	p.trackAndServe(w, r, target, start)
	return true
}

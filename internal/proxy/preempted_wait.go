package proxy

import (
	"context"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/gpu"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Preempted wait: how long a synchronous request may sit in the cold-start
// queue of a shared-GPU lane that cannot win its card right now.
//
// The queue holds a request for up to the lane's coldStartTimeout (25 min on
// the XTX warm lane). That is right for a lane that is loading, and wrong for
// a lane a higher-priority peer holds the card against: no amount of demand
// makes the election hand it the card, so the request waits until the peer
// idles out, long after any client gave up (KT-JOBS bar 2,
// .loom/60-validation-matrix.md row proxy-async-jobs-2026-10-31). Such a
// request waits at most the preempted wait, then gets the same typed 503 a
// Prefer: wait=N caller gets, with code model_preempted. Its demand stamp is
// written on arrival as before, so the lane still comes back once the card is
// free.

// annotationPreemptedWait overrides the preempted wait for one lane, as a Go
// duration ("45s").
const annotationPreemptedWait = "flexinfer.ai/preempted-wait"

// defaultPreemptedWait is the preempted wait when neither
// FLEXINFER_PROXY_PREEMPTED_WAIT nor the lane annotation sets one. It sits
// under the 30 s bar KT-JOBS holds every client connection to.
const defaultPreemptedWait = 20 * time.Second

// preemptedRecheckInterval is how often a request that passed the preempted
// wait without a refusal re-reads the lanes while its lane still waits for the
// card. Lane status moves through short intermediate states during a handover
// (a released holder still Active, an attribution naming a peer that already
// left), so one check at the bound can land in one of them.
const preemptedRecheckInterval = 5 * time.Second

// preemptedWait is the bound for lane m: its annotation when it parses to a
// positive duration, else the proxy setting, else defaultPreemptedWait.
func (p *Proxy) preemptedWait(m *aiv1alpha2.Model) time.Duration {
	if m != nil {
		if v, err := time.ParseDuration(strings.TrimSpace(m.Annotations[annotationPreemptedWait])); err == nil && v > 0 {
			return v
		}
	}
	if p.laneConfig.PreemptedWait > 0 {
		return p.laneConfig.PreemptedWait
	}
	return defaultPreemptedWait
}

// Election timings the proxy mirrors to read the cached Model status the way
// the controller's shared-GPU election does. They must agree with the
// controller constants of the same meaning: activationDemandWindow
// (controllers/constants.go) and sharedDemandWindow
// (controllers/model_shared_gpu.go).
const (
	electionActivationDemandWindow = 30 * time.Minute
	electionSharedDemandWindow     = 2 * time.Minute
	// electionReleaseGrace mirrors releaseGrace in
	// controllers/model_release.go.
	electionReleaseGrace = 45 * time.Second
)

// warmPolicyPrimary is the spec.config.warmPolicy value of the lane that
// reclaims its shared GPU from an idle holder (isWarmPrimaryModel in
// controllers/model_helpers.go).
const warmPolicyPrimary = "primary"

// losingCardTo names the peer that holds m's shared GPU against m, or returns
// "" when m may still win the card. It reads only the informer cache, like the
// rest of the request path.
//
// m cannot win while all of these hold:
//   - m is a shared-group member that the election set aside: phase
//     Preempted, or status.sharedGroup.state Preempted or Queued;
//   - status.sharedGroup.preemptedBy names a plain Model (no park-class
//     prefix such as primary/ or gpu-lease/), and that Model is in the same
//     group with state Active, so it holds the card or is loading onto it;
//   - that Model's gpu.priority is strictly higher than m's;
//   - that Model has not handed the card back: it carries no standing
//     release (holderReleaseStanding), or it is a holder a release does not
//     scale off the card (releaseFreesCard); when it has handed the card
//     back, m still loses to a higher-priority member that wants the card
//     (outrankingContender), and that member is named instead;
//   - when m is a warm primary (spec.config.warmPolicy: primary) and that
//     Model is not one, that Model's demand is still inside its activity
//     window.
//
// The election hands the card to a demanded lane only when it outranks the
// holder, or ties it while the holder is idle (chooseSharedGroupLeader in
// controllers/model_shared_gpu.go). A strictly lower lane therefore waits for
// the holder to idle out or be released, whatever its own demand. Two
// handovers do come on a short clock, and m keeps the full wait for both:
//
//   - A scale-to-zero holder with a standing release gives the card up after the
//     controller's 45 s releaseGrace (the job runner wrote releasedAt after
//     its last job on it). m is about to come back only when no other member
//     outranks it for the card; when the runner's next job is for another,
//     higher-priority lane, the card goes there (KT-JOBS 2026-11-20: two
//     windows of requests held to the client's 120 s timeout, one after each
//     release that handed the card to the next job's lane).
//   - A warm primary reclaims the card from a holder whose demand is older
//     than the holder's activity window (2 min, or its idleTimeout when that
//     is shorter and the holder scales to zero), even from a higher-priority
//     holder that is not a warm primary itself; the swap cooldown delays it
//     by at most 5 min more. While the
//     holder's demand is fresh (a job or a client keeps stamping it) the
//     primary cannot reclaim, and that can last as long as the holder is
//     used, so the bound still applies then. An on-demand lane (the XTX warm
//     lane) has no such reclaim: it returns only when the holder idles out
//     or is released.
//
// An equal priority lane can win on demand, so it keeps the full wait. A
// failover stand-in (flexinfer.ai/failover-for) and a force-promoted lane are
// ranked differently by the election and also keep the full wait. Every case
// this does not recognise keeps the full wait.
func (p *Proxy) losingCardTo(ctx context.Context, m *aiv1alpha2.Model) string {
	if m == nil || !m.Spec.IsShared() || m.Spec.IsForcePromoted() {
		return ""
	}
	if strings.TrimSpace(m.Annotations[constants.AnnotationFailoverFor]) != "" {
		return ""
	}
	sg := m.Status.SharedGroup
	if sg == nil {
		return ""
	}
	if m.Status.Phase != aiv1alpha2.ModelPhasePreempted && sg.State != "Preempted" && sg.State != "Queued" {
		return ""
	}
	holderName := strings.TrimSpace(sg.PreemptedBy)
	if holderName == "" || strings.Contains(holderName, "/") || holderName == m.Name {
		return ""
	}
	holder := p.peekModel(ctx, holderName)
	if holder == nil || holder.Status.SharedGroup == nil || holder.Status.SharedGroup.State != "Active" {
		return ""
	}
	if holder.Spec.GPU == nil || holder.Spec.GPU.Shared != m.Spec.GPU.Shared {
		return ""
	}
	if holder.Spec.GetPriority() <= m.Spec.GetPriority() {
		return ""
	}
	now := time.Now()
	if releaseFreesCard(holder) && holderReleaseStanding(holder, now) {
		// The holder is handing the card back, but the election gives it to
		// the highest-priority lane that wants it, which need not be m: the
		// job runner releases a lane after its last job on that lane and
		// then forwards the group's next job to another creative lane.
		return p.outrankingContender(ctx, m, holder, now)
	}
	// chooseSharedGroupLeader reclaims for a warm primary only from a Ready
	// holder that is not itself a warm primary (!isWarmPrimaryModel).
	if isWarmPrimaryLane(m) && !isWarmPrimaryLane(holder) && !holderDemandFresh(holder, now) {
		return ""
	}
	return holderName
}

// outrankingContender names a member of m's shared group, other than m and
// the releasing holder, that the election would give the card to ahead of m
// once the holder is gone, or returns "" when there is none and m may win.
//
// It counts only a member that waits for the card itself and that the
// election would rank as a wanter ahead of m (sharedModelWantsToRun and
// demandTime in controllers/model_shared_gpu.go and
// controllers/model_activation.go):
//   - it is not Active: a member already on the card is either the next
//     holder (then m's attribution moves to it and the next check names it)
//     or, in a multi-model group, a co-resident that m may join;
//   - it can take demand (not Failed, its cache ready when it has one);
//   - its gpu.priority is strictly higher than m's (an equal one can lose to
//     m on demand);
//   - its demand is inside its activity window, and a release of its own has
//     not outlived the controller's grace (modelReleased withdraws demand);
//   - it is not a failover stand-in, which ranks as the Model it stands in
//     for, a rank the proxy does not resolve.
//
// A multi-model group admits every wanter that fits, highest priority first,
// so a higher-priority wanter need not keep m off the card; no contender is
// named there and m keeps the full wait. The group is multi-model when the
// GPUProfile for the architecture in m's nodeSelector sets
// features.multiModel (sharedGroupMultiModel in
// controllers/model_shared_gpu.go reads the same label and flag), or when a
// member besides the holder is Active, which only a multi-model group allows
// for long.
//
// A warm primary m reclaims the card from an idle holder that is not a warm
// primary, ahead of a lower-ranked wanter (chooseSharedGroupLeader): a
// released holder's demand is withdrawn after the grace, so it is idle. For
// such an m only a contender that ranks at or above the holder can still
// take the card first, through the demand path.
//
// A job forwarded to a lane that is not ready waits in that lane's cold-start
// queue, whose first step stamps the lane's lastActiveTime, so the next job's
// lane shows fresh demand within moments of the release that precedes it.
// Demand older than the 2 min window (the election ranks some wanters on a
// 5 min window when nothing is Ready) is not counted: that errs toward a long
// wait, never toward a 503 the election would not back.
func (p *Proxy) outrankingContender(ctx context.Context, m, holder *aiv1alpha2.Model, now time.Time) string {
	if p.client == nil {
		return ""
	}
	var list aiv1alpha2.ModelList
	if err := p.client.List(ctx, &list, client.InNamespace(m.Namespace)); err != nil {
		return ""
	}
	if p.groupIsMultiModel(ctx, m) {
		return ""
	}
	group := m.Spec.GPU.Shared
	best, bestPriority := "", m.Spec.GetPriority()
	if isWarmPrimaryLane(m) && !isWarmPrimaryLane(holder) {
		// Strictly above means at or above the holder's priority.
		bestPriority = max(bestPriority, holder.Spec.GetPriority()-1)
	}
	for i := range list.Items {
		c := &list.Items[i]
		if c.Name == m.Name || c.Name == holder.Name {
			continue
		}
		if c.Spec.GPU == nil || c.Spec.GPU.Shared != group {
			continue
		}
		if c.Status.SharedGroup != nil && c.Status.SharedGroup.State == "Active" {
			// Two members on the card at once: a multi-model group.
			return ""
		}
		if strings.TrimSpace(c.Annotations[constants.AnnotationFailoverFor]) != "" {
			continue
		}
		if c.Status.Phase == aiv1alpha2.ModelPhaseFailed || (c.Status.Cache != nil && !c.Status.Cache.Ready) {
			continue
		}
		if contenderReleased(c, now) {
			continue
		}
		if prio := c.Spec.GetPriority(); prio > bestPriority && holderDemandFresh(c, now) {
			best, bestPriority = c.Name, prio
		}
	}
	return best
}

// groupIsMultiModel reports whether the GPUProfile for the architecture in
// m's nodeSelector declares a multi-model runtime, as the election resolves
// it. A selector without an architecture label resolves to single-slot there
// too.
func (p *Proxy) groupIsMultiModel(ctx context.Context, m *aiv1alpha2.Model) bool {
	arch := gpu.ArchFromLabels(m.Spec.NodeSelector)
	if arch == "" {
		return false
	}
	profile := p.lookupGPUProfile(ctx, arch)
	return profile != nil && profile.Features.MultiModel
}

// contenderReleased mirrors modelReleased in controllers/model_release.go: a
// standing release that has outlived the grace withdraws the member's demand.
func contenderReleased(m *aiv1alpha2.Model, now time.Time) bool {
	return holderReleaseStanding(m, now) && now.Sub(m.Status.ReleasedAt.Time) >= electionReleaseGrace
}

// releaseFreesCard reports whether a standing release on m makes the
// controller scale m off the card. The release acts through desiredReplicas
// (controllers/model_helpers.go): a Model that is not serverless keeps one
// replica before the release is looked at, and a released serverless Model
// scales to minReplicas, which shouldKeepWarmPrimary raises to 1 for a warm
// primary. Only a serverless, scale-to-zero, non-primary holder gives the
// card back, the same condition sharedActivityWindow uses for its idle
// window.
func releaseFreesCard(m *aiv1alpha2.Model) bool {
	return m.Spec.IsServerless() && m.Spec.GetMinReplicas() == 0 && !isWarmPrimaryLane(m)
}

// holderReleaseStanding reports whether m carries a release no demand has
// answered. It mirrors releaseStanding in controllers/model_release.go, and the
// two must agree: the proxy uses it to tell a holder that is about to give the
// card back from one that keeps it.
func holderReleaseStanding(m *aiv1alpha2.Model, now time.Time) bool {
	if m == nil || m.Status.ReleasedAt == nil {
		return false
	}
	if activationRequestInWindow(m, now) {
		return false
	}
	if last := m.Status.LastActiveTime; last != nil && last.After(m.Status.ReleasedAt.Time) {
		return false
	}
	return true
}

// activationRequestInWindow mirrors activationRequestInWindowAt in
// controllers/model_activation.go.
func activationRequestInWindow(m *aiv1alpha2.Model, now time.Time) bool {
	if m == nil || m.Status.ActivationRequestedAt == nil {
		return false
	}
	return now.Sub(m.Status.ActivationRequestedAt.Time) <= electionActivationDemandWindow
}

// holderDemandFresh reports whether m's election demand (demandTime in
// controllers/model_activation.go: the later of lastActiveTime and an
// activation request still in its window) is inside m's activity window
// (sharedActivityWindow in controllers/model_shared_gpu.go). A holder whose
// demand is not fresh is the idle Ready holder a warm primary reclaims from.
// The release case is checked before this is called.
func holderDemandFresh(m *aiv1alpha2.Model, now time.Time) bool {
	var demand time.Time
	if last := m.Status.LastActiveTime; last != nil {
		demand = last.Time
	}
	if activationRequestInWindow(m, now) && m.Status.ActivationRequestedAt.After(demand) {
		demand = m.Status.ActivationRequestedAt.Time
	}
	if demand.IsZero() {
		return false
	}
	window := electionSharedDemandWindow
	if m.Spec.IsServerless() && m.Spec.GetMinReplicas() == 0 && m.Spec.Serverless != nil && m.Spec.Serverless.IdleTimeout != nil {
		if idle := m.Spec.Serverless.IdleTimeout.Duration; idle > 0 && idle < window {
			window = idle
		}
	}
	return now.Sub(demand) <= window
}

// isWarmPrimaryLane mirrors isWarmPrimaryModel in controllers/model_helpers.go.
func isWarmPrimaryLane(m *aiv1alpha2.Model) bool {
	return strings.EqualFold(strings.TrimSpace(m.Spec.ConfigString("warmPolicy", "")), warmPolicyPrimary)
}

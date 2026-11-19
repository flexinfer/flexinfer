package proxy

import (
	"context"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
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
//     release (holderReleaseStanding);
//   - when m is a warm primary (spec.config.warmPolicy: primary), that
//     Model's demand is still inside its activity window.
//
// The election hands the card to a demanded lane only when it outranks the
// holder, or ties it while the holder is idle (chooseSharedGroupLeader in
// controllers/model_shared_gpu.go). A strictly lower lane therefore waits for
// the holder to idle out or be released, whatever its own demand. Two
// handovers do come on a short clock, and m keeps the full wait for both:
//
//   - A holder with a standing release gives the card up after the
//     controller's 45 s releaseGrace (the job runner wrote releasedAt after
//     its last job). m is about to come back, so its request waits for it.
//   - A warm primary reclaims the card from a holder whose demand is older
//     than the holder's activity window (2 min, or its idleTimeout when that
//     is shorter and the holder scales to zero), even from a higher-priority
//     holder; the swap cooldown delays it by at most 5 min more. While the
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
	if holderReleaseStanding(holder, now) {
		return ""
	}
	if isWarmPrimaryLane(m) && !holderDemandFresh(holder, now) {
		return ""
	}
	return holderName
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

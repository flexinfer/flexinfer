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
//   - that Model's gpu.priority is strictly higher than m's.
//
// The election hands the card to a demanded lane only when it outranks the
// holder, or ties it while the holder is idle (chooseSharedGroupLeader in
// controllers/model_shared_gpu.go). A strictly lower lane therefore waits for
// the holder to idle out or be released, whatever its own demand. An equal
// priority lane can win on demand, so it keeps the full wait. A failover
// stand-in (flexinfer.ai/failover-for) and a force-promoted lane are ranked
// differently by the election and also keep the full wait. Every case this
// does not recognise keeps today's behaviour.
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
	return holderName
}

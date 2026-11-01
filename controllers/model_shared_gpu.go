/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// handleSharedGPU implements GPU sharing logic for models with gpu.shared set.
// Demand-based swap tuning constants for shared GPU groups.
const (
	// sharedDemandWindow is how recent a model's demandTime must be to count as
	// active demand — either proxy traffic (LastActiveTime) or an in-window
	// explicit activation request.
	sharedDemandWindow = 2 * time.Minute

	// sharedSwapCooldown prevents thrashing by blocking demand-based swaps
	// for this duration after the most recent preemption.  Set high enough
	// to cover model loading (large diffusers models need 3-5 min).
	sharedSwapCooldown = 5 * time.Minute
)

// failoverStandIns maps a group member to the Model it stands in for
// (flexinfer.ai/failover-for) while that Model is down. The election ranks a
// stand-in at the higher of its own and the down Model's gpu.priority, and
// counts the down Model's demand as its own: the proxy keeps stamping
// lastActiveTime on the Model its callers name, so traffic for a dead
// always-on lane is what lets its warm copy on another card preempt a
// lower-priority tenant there. The nil map is the plain election.
type failoverStandIns map[string]*aiv1alpha2.Model

// priority is m's election priority.
func (f failoverStandIns) priority(m *aiv1alpha2.Model) int32 {
	p := m.Spec.GetPriority()
	if down := f[m.Name]; down != nil && down.Spec.GetPriority() > p {
		return down.Spec.GetPriority()
	}
	return p
}

// demand is m's election demand time: the later of its own and, while m
// stands in, the down Model's.
func (f failoverStandIns) demand(m *aiv1alpha2.Model, now time.Time) *metav1.Time {
	own := demandTime(m, now)
	down := f[m.Name]
	if down == nil {
		return own
	}
	if inherited := demandTime(down, now); inherited != nil && (own == nil || inherited.After(own.Time)) {
		return inherited
	}
	return own
}

func chooseSharedGroupLeader(groupModels []*aiv1alpha2.Model, now time.Time, failover failoverStandIns) *aiv1alpha2.Model {
	if len(groupModels) == 0 {
		return nil
	}

	better := func(a, b *aiv1alpha2.Model) *aiv1alpha2.Model {
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		pa := failover.priority(a)
		pb := failover.priority(b)
		if pa != pb {
			if pa > pb {
				return a
			}
			return b
		}
		da, db := failover.demand(a, now), failover.demand(b, now)
		if da != nil && db != nil {
			if !da.Equal(db) {
				if da.After(db.Time) {
					return a
				}
				return b
			}
		}
		if a.Name <= b.Name {
			return a
		}
		return b
	}

	// Operator-forced promotion: any group member with gpu.forcePromotion=true
	// wins leadership purely on priority among other force-promoted members.
	// This bypasses both the Ready-first preference (priority steps 3-7 below)
	// and the anti-thrashing cooldown. Intended for canary rollouts and
	// kill-tests where the new claimant must get a Deployment in order to
	// become Ready (the chooser would otherwise return the existing Ready
	// warm-primary at priority step 3 and never give the new claimant a chance
	// to reach Phase=Ready). Use sparingly; while a model is force-promoted
	// it preempts warm-primaries without proxy traffic.
	var forcedLeader *aiv1alpha2.Model
	for _, m := range groupModels {
		if m.Spec.IsForcePromoted() {
			forcedLeader = better(forcedLeader, m)
		}
	}
	if forcedLeader != nil {
		return forcedLeader
	}

	// Anti-thrashing: if a swap happened recently, keep the currently
	// active model regardless of demand or priority.
	// Use the maximum nonnegative SwapCooldown from any model in the group,
	// falling back to the default (5m) if none specify a valid override.
	// An explicit zero disables the cooldown.
	cooldown := sharedSwapCooldown
	hasCooldownOverride := false
	for _, m := range groupModels {
		if m.Spec.GPU != nil && m.Spec.GPU.SwapCooldown != nil {
			if d := m.Spec.GPU.SwapCooldown.Duration; d >= 0 && (!hasCooldownOverride || d > cooldown) {
				cooldown = d
				hasCooldownOverride = true
			}
		}
	}
	recentSwap := false
	for _, m := range groupModels {
		if m.Status.SharedGroup != nil && m.Status.SharedGroup.PreemptedAt != nil {
			if cooldown > 0 && now.Sub(m.Status.SharedGroup.PreemptedAt.Time) < cooldown {
				recentSwap = true
				break
			}
		}
	}
	if recentSwap {
		for _, m := range groupModels {
			if m.Status.SharedGroup != nil && m.Status.SharedGroup.State == "Active" {
				return m
			}
		}
	}

	var readyLeader *aiv1alpha2.Model
	var recentLeader *aiv1alpha2.Model
	var runnableFallbackLeader *aiv1alpha2.Model
	var fallbackLeader *aiv1alpha2.Model
	var demandedLeader *aiv1alpha2.Model
	var warmPrimaryLeader *aiv1alpha2.Model
	var warmPinnedLeader *aiv1alpha2.Model
	var activeLoadingLeader *aiv1alpha2.Model
	for _, m := range groupModels {
		fallbackLeader = better(fallbackLeader, m)
		if isActiveSharedModelLoading(m) && withinSharedActivationWindow(m, now) {
			activeLoadingLeader = better(activeLoadingLeader, m)
		}
		if !sharedModelCanTakeDemand(m) {
			continue
		}
		runnableFallbackLeader = better(runnableFallbackLeader, m)
		if isWarmPrimaryModel(m) {
			warmPrimaryLeader = better(warmPrimaryLeader, m)
		}
		if isWarmPinnedSharedModel(m) {
			warmPinnedLeader = better(warmPinnedLeader, m)
		}
		if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
			readyLeader = better(readyLeader, m)
			continue
		}
		md := failover.demand(m, now)
		if md == nil {
			continue
		}
		if now.Sub(md.Time) < sharedActivityWindow(m, 5*time.Minute) {
			recentLeader = better(recentLeader, m)
		}
		if now.Sub(md.Time) < sharedActivityWindow(m, sharedDemandWindow) {
			demandedLeader = better(demandedLeader, m)
		}
	}

	// Keep the current loading model active for its cold-start budget. Large
	// first pulls can outlive the short demand window; dropping leadership here
	// scales the pod down before kubelet can finish pulling the backend image.
	if activeLoadingLeader != nil {
		return activeLoadingLeader
	}

	// Demand-based preemption: if a non-ready model has recent demand (the proxy
	// set its LastActiveTime, or an operator activated it) and the current ready
	// leader is idle, swap to the demanded model.
	if demandedLeader != nil && readyLeader != nil {
		readyDemand := failover.demand(readyLeader, now)
		readyIdle := readyDemand == nil ||
			now.Sub(readyDemand.Time) > sharedActivityWindow(readyLeader, sharedDemandWindow)
		demandedPriority, readyPriority := failover.priority(demandedLeader), failover.priority(readyLeader)
		if demandedPriority > readyPriority || (readyIdle && demandedPriority >= readyPriority) {
			return demandedLeader
		}
	}

	// Warm-primary reclaim: the operator-designated primary (warmPolicy=primary)
	// reclaims the shared slot from an idle Ready borrower whose demand window
	// has lapsed -- even a higher-priority one. A higher-priority lane still
	// preempts the primary the instant it sees real traffic (the demand path
	// above fires first), so this only governs the idle case: an on-demand lane
	// may borrow the card under load but must not squat on it once quiet.
	// Without this, an on-demand high-priority lane that briefly went Ready holds
	// the slot indefinitely against the warm primary because the Ready preference
	// below returns unconditionally -- the 7900xtx-textgen "swap-from-idle" gap
	// where whisper (ASR, priority 400) parked gemma4 (chat primary, priority
	// 350) after a burst of probes left whisper Ready. The reclaim is bounded by
	// the anti-thrashing cooldown checked above, so it cannot flap the slot.
	if warmPrimaryLeader != nil && readyLeader != nil && !isWarmPrimaryModel(readyLeader) {
		readyDemand := failover.demand(readyLeader, now)
		readyIdle := readyDemand == nil ||
			now.Sub(readyDemand.Time) > sharedActivityWindow(readyLeader, sharedDemandWindow)
		if readyIdle {
			return warmPrimaryLeader
		}
	}

	if readyLeader != nil {
		return readyLeader
	}
	if recentLeader != nil && warmPrimaryLeader != nil {
		if failover.priority(recentLeader) >= failover.priority(warmPrimaryLeader) {
			return recentLeader
		}
		return warmPrimaryLeader
	}
	if recentLeader != nil {
		return recentLeader
	}
	if warmPrimaryLeader != nil {
		return warmPrimaryLeader
	}
	// Warm-pinned preference: when no member is Ready, recently active, or under
	// demand, prefer a member the operator pinned warm (minReplicas>=1) over an
	// idle scale-to-zero member that would otherwise win on raw priority via the
	// runnable fallback. Without this, a higher-priority idle minReplicas:0
	// member permanently holds the single runtime slot and starves the warm
	// incumbent -- the gtx980ti-models pathology where nomic-embed-text
	// (minReplicas:1, priority 100) stayed Queued for weeks behind an idle
	// gemma4-e4b-gguf (minReplicas:0, priority 200). This only fires in the
	// no-demand fallback path, so a higher-priority model still preempts the
	// warm incumbent the moment it sees real traffic (demand path above).
	if warmPinnedLeader != nil {
		return warmPinnedLeader
	}
	if runnableFallbackLeader != nil {
		return runnableFallbackLeader
	}
	return fallbackLeader
}

// vramEstimateMB returns a model's declared VRAM estimate in MB (0 if unset).
func vramEstimateMB(m *aiv1alpha2.Model) int64 {
	if m == nil || m.Spec.GPU == nil || m.Spec.GPU.VRAMEstimateMB == nil {
		return 0
	}
	return *m.Spec.GPU.VRAMEstimateMB
}

// hwQueueBound bounds the summed hardware compute-queue estimate of a
// multi-model Active set: GPUProfile maxHWQueues - reservedHWQueues. The zero
// value disables the bound, which keeps election VRAM-only.
type hwQueueBound struct {
	limit    int32
	reserved int32
	estimate func(*aiv1alpha2.Model) int32
}

func (q hwQueueBound) enabled() bool { return q.limit > 0 && q.estimate != nil }

// of returns m's queue estimate, or 0 while the bound is disabled.
func (q hwQueueBound) of(m *aiv1alpha2.Model) int32 {
	if !q.enabled() || m == nil {
		return 0
	}
	return q.estimate(m)
}

// sum returns the summed queue estimate of models.
func (q hwQueueBound) sum(models []*aiv1alpha2.Model) int32 {
	var used int32
	for _, m := range models {
		used += q.of(m)
	}
	return used
}

// sharedModelWantsToRun reports whether a member should be considered for one of
// several concurrent Active slots in multi-model mode: it must be runnable and
// either pinned warm, currently Ready, or under recent demand. (The primary
// leader is chosen separately by chooseSharedGroupLeader and always included.)
func sharedModelWantsToRun(m *aiv1alpha2.Model, now time.Time, failover failoverStandIns) bool {
	if !sharedModelCanTakeDemand(m) {
		return false
	}
	if isWarmPinnedSharedModel(m) {
		return true
	}
	if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
		return true
	}
	if md := failover.demand(m, now); md != nil && now.Sub(md.Time) < sharedActivityWindow(m, sharedDemandWindow) {
		return true
	}
	return false
}

// Short serverless idle timers must release both the subprocess and its VRAM
// reservation. Otherwise an Idle fast-swap model can hold the elected slot for
// the legacy five-minute recent-activity window after its process is gone.
func sharedActivityWindow(m *aiv1alpha2.Model, fallback time.Duration) time.Duration {
	if m.Spec.IsServerless() && m.Spec.GetMinReplicas() == 0 {
		if m.Spec.Serverless != nil && m.Spec.Serverless.IdleTimeout != nil {
			if idle := m.Spec.Serverless.IdleTimeout.Duration; idle > 0 && idle < fallback {
				return idle
			}
		}
	}
	return fallback
}

// chooseSharedGroupLeaders returns the set of group members that should be
// Active concurrently.
//
// With multiModel=false (the default) it returns exactly the single leader from
// chooseSharedGroupLeader -- identical to single-slot election, preserving all
// existing behavior (demand preemption, warm-pinned preference, etc).
//
// With multiModel=true it returns a VRAM-bounded set: the primary leader plus
// every other member that wants to run, admitted in descending priority order
// while the summed gpu.vramEstimateMB stays within budgetMB. The primary is
// always included (parity with the single-slot guarantee that the elected leader
// runs, even if its own estimate exceeds the budget). budgetMB<=0 disables the
// VRAM bound (admit all wanters); callers pass the GPUProfile VRAMMB.
// When leased is true a training/quant workload holds a scheduler-honored GPU
// lease on the group: the election yields NO servable leader so every serving
// member parks (scale-to-0 -> Preempted) and stays parked until the lease is
// released or expires. The lease is the highest-priority transient member of
// the group -- it beats even forcePromotion (the spec's "park-and-hold").
func chooseSharedGroupLeaders(groupModels []*aiv1alpha2.Model, now time.Time, multiModel bool, budgetMB int64, leased bool) []*aiv1alpha2.Model {
	return chooseSharedGroupLeadersBounded(groupModels, now, multiModel, budgetMB, hwQueueBound{}, nil, leased)
}

// chooseSharedGroupLeadersBounded is chooseSharedGroupLeaders with a second
// admission bound: with multiModel=true a non-primary wanter joins the Active
// set only if it also keeps the summed hardware compute-queue estimate within
// queues.limit (GPUProfile maxHWQueues - reservedHWQueues). The primary is
// always included, as with the VRAM bound. A disabled bound changes nothing.
// failover ranks stand-ins for down Models (see failoverStandIns) in both the
// leader choice and the admission order; it never relaxes either bound.
func chooseSharedGroupLeadersBounded(groupModels []*aiv1alpha2.Model, now time.Time, multiModel bool, budgetMB int64, queues hwQueueBound, failover failoverStandIns, leased bool) []*aiv1alpha2.Model {
	if leased {
		return nil
	}
	primary := chooseSharedGroupLeader(groupModels, now, failover)
	if primary == nil {
		return nil
	}
	if !multiModel {
		return []*aiv1alpha2.Model{primary}
	}

	active := []*aiv1alpha2.Model{primary}
	used := vramEstimateMB(primary)
	usedQueues := queues.of(primary)

	// Other wanters, highest priority first (deterministic tiebreak by name).
	var candidates []*aiv1alpha2.Model
	for _, m := range groupModels {
		if m.Name == primary.Name {
			continue
		}
		if sharedModelWantsToRun(m, now, failover) {
			candidates = append(candidates, m)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		pi, pj := failover.priority(candidates[i]), failover.priority(candidates[j])
		if pi != pj {
			return pi > pj
		}
		return candidates[i].Name < candidates[j].Name
	})

	for _, m := range candidates {
		est := vramEstimateMB(m)
		if budgetMB > 0 && used+est > budgetMB {
			continue
		}
		q := queues.of(m)
		if queues.enabled() && usedQueues+q > queues.limit {
			continue
		}
		active = append(active, m)
		used += est
		usedQueues += q
	}
	return active
}

// leaseFreesCard reports whether an active GPU lease should actually free the
// shared card by parking the serving incumbents (slice-5 preempt policy).
//
//   - No lease -> false (serving runs normally).
//   - Lease with nil Priority -> true: unconditional park-and-hold, the original
//     behavior the legacy ConfigMap carrier and pre-slice-5 CRs rely on.
//   - Priority-gated lease -> honored only when the lease strictly outranks
//     EVERY serving member of the group (every member's gpu.priority is below
//     lease.Priority). A single member at or above the threshold keeps the card
//     and the leasing workload waits (its Job stays Pending), so a low-priority
//     training run can never evict a higher- or equal-priority serving lane.
//     A failover stand-in counts at its election priority.
func leaseFreesCard(lease *activeLease, groupModels []*aiv1alpha2.Model, failover failoverStandIns) bool {
	if lease == nil {
		return false
	}
	if lease.Priority == nil {
		return true
	}
	for _, m := range groupModels {
		if failover.priority(m) >= *lease.Priority {
			return false
		}
	}
	return true
}

// isWarmPinnedSharedModel reports whether the operator has pinned this model
// warm via minReplicas>=1. Such a model is meant to stay running, so in a
// single-slot shared group it should hold leadership over an idle scale-to-zero
// member when neither has demand -- otherwise the pinned warmth never
// materializes because a higher-priority idle member keeps the slot.
func isWarmPinnedSharedModel(model *aiv1alpha2.Model) bool {
	return model != nil && model.Spec.GetMinReplicas() >= 1
}

// staticallyParkedBehindPrimary reports whether member can NEVER be promoted off
// its park while leader holds the shared slot under the current config. It is
// true only when the leader will not yield by idling (it is warm-pinned via
// minReplicas>=1, or the operator-designated warmPolicy=primary) AND the leader
// outranks member on strict priority. The demand-swap in chooseSharedGroupLeader
// requires member.priority >= leader.priority to preempt, so a strictly-lower
// member behind a never-idling leader is doomed: every cold-start it attempts is
// parked the instant it spawns. The proxy reads this (via the PreemptedBy
// "primary/" prefix the election stamps) to fast-fail instead of cold-starting.
//
// It is deliberately conservative: an equal-or-higher-priority member still
// demand-swaps, and a member behind a scale-to-zero leader can serve once that
// leader idles out (warm-primary reclaim / demand path), so neither is flagged.
//
// This is the pairwise single-slot core. The stamping site in handleSharedGPU
// goes through staticallyParkedInGroup, which adds the multi-model exemption:
// under multi-model election, "loses to the leader on priority" does not mean
// "can never serve".
//
// Priorities are election priorities: a failover stand-in parks the members it
// outranks while its down Model's priority lifts it, and is itself parked only
// by a leader that outranks that lifted priority.
func staticallyParkedBehindPrimary(member, leader *aiv1alpha2.Model, failover failoverStandIns) bool {
	if member == nil || leader == nil || member.Name == leader.Name {
		return false
	}
	if !isWarmPinnedSharedModel(leader) && !isWarmPrimaryModel(leader) {
		return false
	}
	return failover.priority(leader) > failover.priority(member)
}

// staticallyParkedInGroup is the group-aware stamping predicate for the
// "primary/" PreemptedBy prefix: staticallyParkedBehindPrimary, minus the
// members multi-model election could still admit.
//
// Single-slot groups reduce to the pairwise check -- only outranking the
// never-idling leader can promote the member. Multi-model groups admit members
// by VRAM budget instead (chooseSharedGroupLeaders): a parked member whose
// gpu.vramEstimateMB still fits budgetMB alongside the current Active set joins
// that set the moment it registers demand, so it must NOT be stamped. The stamp
// makes the proxy 503 WITHOUT recording demand, so a stamped scale-to-zero
// member can never register the demand that would admit it -- the
// snac-decode-gfx1100 deadlock of 2026-08-24 (worked around by warm-pinning the
// member in MR !998). A member whose estimate no longer fits stays stamped:
// demand cannot buy it a slot while the current Active set holds the budget,
// and the stamp self-heals on the reconcile after the set shrinks.
func staticallyParkedInGroup(member, leader *aiv1alpha2.Model, activeModels []*aiv1alpha2.Model, multiModel bool, budgetMB int64) bool {
	return staticallyParkedInGroupBounded(member, leader, activeModels, multiModel, budgetMB, hwQueueBound{}, nil)
}

// staticallyParkedInGroupBounded adds the hardware-queue bound: a multi-model
// member whose queue estimate does not fit beside the Active set stays stamped
// for the same reason as one whose VRAM does not fit. failover is the
// election's stand-in ranking.
func staticallyParkedInGroupBounded(member, leader *aiv1alpha2.Model, activeModels []*aiv1alpha2.Model, multiModel bool, budgetMB int64, queues hwQueueBound, failover failoverStandIns) bool {
	if !staticallyParkedBehindPrimary(member, leader, failover) {
		return false
	}
	if !multiModel {
		return true
	}
	// An unbounded VRAM budget admits every wanter on VRAM
	// (chooseSharedGroupLeaders skips that bound), so only the queue bound can
	// then park a member statically.
	vramFull := false
	if budgetMB > 0 {
		var used int64
		for _, m := range activeModels {
			used += vramEstimateMB(m)
		}
		vramFull = used+vramEstimateMB(member) > budgetMB
	}
	queuesFull := queues.enabled() && queues.sum(activeModels)+queues.of(member) > queues.limit
	return vramFull || queuesFull
}

func sharedModelCanTakeDemand(model *aiv1alpha2.Model) bool {
	if model == nil {
		return false
	}
	if model.Status.Phase == aiv1alpha2.ModelPhaseFailed {
		return false
	}
	if model.Status.Cache != nil && !model.Status.Cache.Ready {
		return false
	}
	return true
}

func isActiveSharedModelLoading(model *aiv1alpha2.Model) bool {
	if model == nil || model.Status.SharedGroup == nil || model.Status.SharedGroup.State != "Active" {
		return false
	}
	return model.Status.Phase == aiv1alpha2.ModelPhasePending || model.Status.Phase == aiv1alpha2.ModelPhaseLoading
}

func withinSharedActivationWindow(model *aiv1alpha2.Model, now time.Time) bool {
	md := demandTime(model, now)
	if md == nil {
		return false
	}

	window := sharedSwapCooldown
	if model.Spec.Serverless != nil && model.Spec.Serverless.ColdStartTimeout != nil {
		if d := model.Spec.Serverless.ColdStartTimeout.Duration; d > window {
			window = d
		}
	}
	return now.Sub(md.Time) <= window
}

func preserveActiveSharedLoadingDuringCacheRefresh(model *aiv1alpha2.Model, now time.Time) bool {
	return isActiveSharedModelLoading(model) && activeSharedModelWithinActivationWindow(model, now)
}

func activeSharedModelWithinActivationWindow(model *aiv1alpha2.Model, now time.Time) bool {
	if model == nil || model.Status.SharedGroup == nil || model.Status.SharedGroup.State != "Active" {
		return false
	}
	return withinSharedActivationWindow(model, now)
}

func queuePositionForSharedModel(modelName string, activeModel *aiv1alpha2.Model, groupModels []*aiv1alpha2.Model) int32 {
	if activeModel != nil && modelName == activeModel.Name {
		return 0
	}

	queued := make([]*aiv1alpha2.Model, 0, len(groupModels))
	for _, m := range groupModels {
		if activeModel != nil && m.Name == activeModel.Name {
			continue
		}
		queued = append(queued, m)
	}
	sort.Slice(queued, func(i, j int) bool {
		pi := queued[i].Spec.GetPriority()
		pj := queued[j].Spec.GetPriority()
		if pi != pj {
			return pi > pj
		}
		return queued[i].Name < queued[j].Name
	})
	for i, m := range queued {
		if m.Name == modelName {
			return int32(i + 1)
		}
	}
	return 0
}

func sharedGroupStatusEqual(a, b *aiv1alpha2.SharedGroupStatus) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	if a.GroupName != b.GroupName || a.State != b.State || a.QueuePosition != b.QueuePosition || a.PreemptedBy != b.PreemptedBy {
		return false
	}
	switch {
	case a.PreemptedAt == nil && b.PreemptedAt == nil:
		return true
	case a.PreemptedAt == nil || b.PreemptedAt == nil:
		return false
	default:
		return a.PreemptedAt.Equal(b.PreemptedAt)
	}
}

func cloneSharedGroupStatus(in *aiv1alpha2.SharedGroupStatus) *aiv1alpha2.SharedGroupStatus {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// sharedGroupMultiModel resolves whether the model's GPU architecture runtime
// can host concurrent model subprocesses (GPUProfile feature flag) and the VRAM
// budget (MB, from the profile's usable VRAMMB) used to bound the Active set.
// Returns (false, 0) when no GPUProfile declares MultiModel -- the safe
// single-slot default that preserves single-leader election everywhere.
func (r *ModelReconciler) sharedGroupMultiModel(model *aiv1alpha2.Model) (bool, int64) {
	if r.GPUProfiles == nil || model == nil {
		return false, 0
	}
	arch := gpuArchFromNodeSelector(model.Spec.NodeSelector)
	if arch == "" {
		return false, 0
	}
	profile, ok := r.GPUProfiles.Lookup(arch)
	if !ok || profile == nil || !profile.Features.MultiModel {
		return false, 0
	}
	return true, profile.VRAMMB
}

// sharedGroupHWQueueBound resolves the hardware compute-queue bound for the
// model's GPU: the GPUProfile's maxHWQueues - reservedHWQueues and a per-member
// estimator (spec.gpu.hwQueueEstimate, else the backend default for the
// member's effective GPU_MAX_HW_QUEUES). It returns the disabled bound when
// the profile sets no maxHWQueues, and the resolved architecture.
func (r *ModelReconciler) sharedGroupHWQueueBound(model *aiv1alpha2.Model) (hwQueueBound, string) {
	if r.GPUProfiles == nil || model == nil {
		return hwQueueBound{}, ""
	}
	arch := gpuArchFromNodeSelector(model.Spec.NodeSelector)
	if arch == "" {
		return hwQueueBound{}, ""
	}
	profile, ok := r.GPUProfiles.Lookup(arch)
	if !ok {
		return hwQueueBound{}, arch
	}
	maxQueues, reserved, ok := backend.HWQueueBudget(profile)
	if !ok {
		return hwQueueBound{}, arch
	}
	vendor := backend.GPUVendor(strings.ToLower(strings.TrimSpace(profile.Vendor)))
	return hwQueueBound{
		limit:    maxQueues - reserved,
		reserved: reserved,
		estimate: func(m *aiv1alpha2.Model) int32 {
			return backend.ModelHWQueueEstimate(m, profile, vendor, arch)
		},
	}, arch
}

// failoverStandInsFor returns the members of groupModels whose
// flexinfer.ai/failover-for target is down, keyed by member name. Targets are
// read from models, the namespace list the election already holds, so they
// may sit in another group or be exclusive. A missing or self-referencing
// target, or one that is up, leaves the member on its own rank.
//
// Only a member that runs once elected can stand in: warm-pinned
// (minReplicas >= 1) or not serverless. A scale-to-zero member sizes its
// replicas from its own lastActiveTime, so electing it on inherited demand
// would hold the card for a lane that never starts.
func (r *ModelReconciler) failoverStandInsFor(ctx context.Context, groupModels []*aiv1alpha2.Model, models []aiv1alpha2.Model) failoverStandIns {
	var standIns failoverStandIns
	for _, m := range groupModels {
		name := strings.TrimSpace(m.Annotations[constants.AnnotationFailoverFor])
		if name == "" || name == m.Name {
			continue
		}
		if m.Spec.IsServerless() && !isWarmPinnedSharedModel(m) {
			continue
		}
		for i := range models {
			target := &models[i]
			if target.Name != name {
				continue
			}
			if r.failoverTargetDown(ctx, target) {
				if standIns == nil {
					standIns = failoverStandIns{}
				}
				standIns[m.Name] = target
			}
			break
		}
	}
	return standIns
}

// failoverTargetDown reports whether a failover-for target cannot serve: it is
// Failed (GPUDegraded or NoMatchingNodes park, failed load, stalled engine),
// or the node its hostname selector pins carries the GPU health sentinel's
// degraded label. The target turns Failed on its own next reconcile; reading
// the label lets a stand-in reconciled first by the same node event act on it.
func (r *ModelReconciler) failoverTargetDown(ctx context.Context, target *aiv1alpha2.Model) bool {
	if target.Status.Phase == aiv1alpha2.ModelPhaseFailed {
		return true
	}
	host := target.Spec.NodeSelector[corev1.LabelHostname]
	if host == "" {
		return false
	}
	node := &corev1.Node{}
	if err := r.Get(ctx, client.ObjectKey{Name: host}, node); err != nil {
		return false
	}
	return nodeGPUDegraded(node)
}

func (r *ModelReconciler) handleSharedGPU(ctx context.Context, model *aiv1alpha2.Model) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if model.Spec.GPU == nil || model.Spec.GPU.Shared == "" {
		return ctrl.Result{}, nil
	}

	groupName := model.Spec.GPU.Shared
	// One member of a group elects at a time (see sharedGroupLocks).
	defer r.lockSharedGroup(model.Namespace, groupName)()

	// Find all models in the same shared group
	modelList := &aiv1alpha2.ModelList{}
	if err := r.List(ctx, modelList, client.InNamespace(model.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	var groupModels []*aiv1alpha2.Model
	for i := range modelList.Items {
		m := &modelList.Items[i]
		if m.Spec.GPU != nil && m.Spec.GPU.Shared == groupName {
			groupModels = append(groupModels, m)
		}
	}

	now := time.Now()

	// Members that stand in for a down Model elsewhere (flexinfer.ai/failover-for)
	// rank as that Model; its status comes from the namespace list above.
	failover := r.failoverStandInsFor(ctx, groupModels, modelList.Items)

	// GPU lease: a training/quant workload may hold a scheduler-honored hold on
	// this group's card. While leased, the election yields no leader so every
	// serving member parks (and stays parked) until the lease releases/expires,
	// freeing amd.com/gpu for the training Job. A lease read error fails OPEN
	// toward serving -- we proceed as unleased rather than risk a strand.
	leaseHolder, leaseErr := r.groupHasActiveLease(ctx, model.Namespace, groupName, now)
	if leaseErr != nil {
		log.Error(leaseErr, "Failed to read GPU lease; proceeding as unleased", "group", groupName)
	}
	// A lease only parks serving when its preempt-policy threshold permits it
	// (slice-5). An ungated lease (nil Priority) parks unconditionally; a
	// priority-gated lease parks only when it strictly outranks every serving
	// member, otherwise the higher/equal-priority lane keeps the card and the
	// leasing workload waits. PreemptedBy attribution below still reads
	// leaseHolder directly, so a not-honored lease correctly falls back to
	// normal leader-based preemption.
	leased := leaseFreesCard(leaseHolder, groupModels, failover)

	// Gaming overlay: an active GamingSession on the group's pinned node parks
	// the group exactly like an unconditional GPU lease — no leader elected,
	// demand cannot promote onto the card — so a play session never needs the
	// group's manifests edited. A real GPU lease keeps attribution precedence.
	var gamingHolder *aiv1alpha2.GamingSession
	if !leased {
		if gamingHolder = r.gamingIntentForGroup(ctx, model.Namespace, groupModels, now); gamingHolder != nil {
			leased = true
		}
	}

	// Multi-model election: when the group's GPU runtime can host concurrent
	// subprocesses (GPUProfile feature flag), keep a VRAM-bounded SET of members
	// Active instead of a single leader. Single-slot (default) returns exactly
	// the one leader, so behavior is unchanged for every existing group.
	multiModel, budgetMB := r.sharedGroupMultiModel(model)
	// Hardware compute queues bound the multi-model set beside VRAM where the
	// GPUProfile sets maxHWQueues (gfx906: 24 HWS queues).
	queues, queueArch := r.sharedGroupHWQueueBound(model)
	activeModels := chooseSharedGroupLeadersBounded(groupModels, now, multiModel, budgetMB, queues, failover, leased)
	if queues.enabled() {
		metrics.SharedGroupHWQueues.WithLabelValues(groupName, model.Namespace, queueArch, "estimated").Set(float64(queues.sum(activeModels)))
		metrics.SharedGroupHWQueues.WithLabelValues(groupName, model.Namespace, queueArch, "budget").Set(float64(queues.limit))
		metrics.SharedGroupHWQueues.WithLabelValues(groupName, model.Namespace, queueArch, "reserved").Set(float64(queues.reserved))
	}
	// An empty active set is normal under a lease (everyone parks). Without a
	// lease it is the "shouldn't happen" guard -- requeue and retry.
	if len(activeModels) == 0 && !leased {
		return ctrl.Result{RequeueAfter: requeueFast}, nil
	}
	// primary is the elected leader (set[0]); used for PreemptedBy/queue position.
	// It is nil while leased (no leader); preemptedBy then attributes to the lease.
	var primary *aiv1alpha2.Model
	if len(activeModels) > 0 {
		primary = activeModels[0]
	}
	activeSet := make(map[string]bool, len(activeModels))
	for _, am := range activeModels {
		activeSet[am.Name] = true
	}

	// preemptedBy attributes the park: the elected leader's name normally, a
	// "gpu-lease/<owner>" sentinel while a training lease holds the card, or a
	// "gaming/<session>" sentinel while a gaming session holds the node.
	preemptedBy := ""
	switch {
	case primary != nil:
		preemptedBy = primary.Name
	case leaseHolder != nil:
		preemptedBy = gpuLeasePreemptedByPrefix + leaseHolder.Owner
	case gamingHolder != nil:
		preemptedBy = gamingPreemptedByPrefix + gamingHolder.Name
	}

	// Update this model's shared group status
	origPhase := model.Status.Phase
	origShared := cloneSharedGroupStatus(model.Status.SharedGroup)

	if model.Status.SharedGroup == nil {
		model.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{}
	}
	model.Status.SharedGroup.GroupName = groupName

	if activeSet[model.Name] {
		// This model should be active
		model.Status.SharedGroup.State = "Active"
		model.Status.SharedGroup.QueuePosition = 0
		model.Status.SharedGroup.PreemptedBy = ""
		// Keep PreemptedAt until the model becomes Ready again so swap latency can be observed.
		if origShared == nil || origShared.State != "Active" {
			if down := failover[model.Name]; down != nil {
				log.Info("Model is active in shared group", "group", groupName,
					"failoverFor", down.Name, "electionPriority", failover.priority(model))
			} else {
				log.Info("Model is active in shared group", "group", groupName)
			}
		}
		// Emit shared-group state metric
		metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Active").Set(1)
		metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Queued").Set(0)
		metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Preempted").Set(0)
	} else {
		// This model should be preempted/queued (not in the Active set).
		// Tag a statically-un-promotable park with the "primary/" PreemptedBy
		// prefix, so the reason resolver surfaces ReasonParkedBehindPrimary and
		// the proxy fast-fails its doomed cold-starts. Single-slot: lower
		// priority behind a never-idling warm leader. Multi-model: additionally
		// requires the member's VRAM estimate to exceed the remaining budget --
		// a member that still fits is admitted by demand, so its cold-start must
		// queue normally. Lease parks take precedence and keep their own prefix.
		effectivePreemptedBy := preemptedBy
		if !leased && primary != nil && staticallyParkedInGroupBounded(model, primary, activeModels, multiModel, budgetMB, queues, failover) {
			effectivePreemptedBy = aiv1alpha2.PreemptedByPrimaryPrefix + primary.Name
		}
		model.Status.SharedGroup.State = "Queued"
		model.Status.SharedGroup.QueuePosition = queuePositionForSharedModel(model.Name, primary, groupModels)
		model.Status.SharedGroup.PreemptedBy = effectivePreemptedBy

		if model.Status.Phase == aiv1alpha2.ModelPhaseReady {
			// Preempt this model
			log.Info("Preempting model", "preemptedBy", effectivePreemptedBy, "leased", leased)
			model.Status.Phase = aiv1alpha2.ModelPhasePreempted
			model.Status.LoadingSubstage = aiv1alpha2.LoadingSubstagePreempted
			preemptReason, preemptMsg := preemptedConditionReasonMessage(model)
			model.Status.Message = preemptMsg
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, preemptReason, preemptMsg)
			model.Status.SharedGroup.PreemptedAt = &metav1.Time{Time: time.Now()}
			if leased {
				// preemptMsg already distinguishes a training lease from a
				// gaming session hold.
				r.Recorder.Event(model, corev1.EventTypeNormal, "Preempted", preemptMsg)
			} else {
				r.Recorder.Event(model, corev1.EventTypeNormal, "Preempted",
					fmt.Sprintf("Preempted by %s with priority %d", primary.Name, failover.priority(primary)))
			}
			metrics.SharedGroupPreemptionsTotal.WithLabelValues(groupName, model.Namespace, model.Name, effectivePreemptedBy).Inc()
			metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Preempted").Set(1)
			metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Active").Set(0)
			metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Queued").Set(0)
		} else {
			metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Queued").Set(1)
			metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Active").Set(0)
			metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Preempted").Set(0)
		}
	}

	// Sync active service labels on Services for the entire group.
	// Each Active model's Service gets its serviceLabels written into
	// ai.flexinfer/active-services; all other group members have it cleared.
	// This lets the proxy route group-wide names (serviceLabels) only to the
	// Active model(s) -- in multi-model mode each Active member advertises its
	// own distinct labels (e.g. embeddings vs rerank) concurrently.
	r.syncActiveServiceLabels(ctx, activeSet, groupModels)

	if origPhase == model.Status.Phase && sharedGroupStatusEqual(origShared, model.Status.SharedGroup) {
		// The election reproduced this member's recorded state: nothing is in
		// flight for it, so re-elect at the slow cadence. Anything that can
		// change the outcome sooner arrives as an event — the proxy's demand
		// stamp on this Model, a sibling's Phase/State transition, a GPULease
		// or GamingSession, a node GPU health flip, the runtime pod — and the
		// Service annotation sync above re-enqueues the members it touches.
		return ctrl.Result{RequeueAfter: requeueSharedSteady}, nil
	}

	if err := r.persistStatus(ctx, model); err != nil {
		return ctrl.Result{}, err
	}

	// A transition is in flight (promotion, park, queue shuffle): poll fast
	// until this member settles.
	return ctrl.Result{RequeueAfter: requeueFast}, nil
}

// syncActiveServiceLabels sets the ai.flexinfer/active-services annotation on
// every Service in a shared-GPU group.  The active model gets its serviceLabels
// written into the annotation; all other group members get an empty string.
// An empty annotation (key present, value "") tells the proxy "this service is
// managed but currently inactive -- do not fall back to static service-labels".
func (r *ModelReconciler) syncActiveServiceLabels(ctx context.Context, activeSet map[string]bool, groupModels []*aiv1alpha2.Model) {
	log := log.FromContext(ctx)
	annoKey := constants.ServiceAnnotationActiveLabels

	for _, m := range groupModels {
		svc := &corev1.Service{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Name}, svc); err != nil {
			continue // service may not exist yet
		}
		if svc.Annotations == nil {
			svc.Annotations = make(map[string]string)
		}

		var desired string
		if activeSet[m.Name] && len(m.Spec.ServiceLabels) > 0 {
			desired = strings.Join(m.Spec.ServiceLabels, ",")
		}
		// desired is "" for non-active members -- annotation is still SET so
		// the proxy knows not to fall back to static service-labels.

		current, exists := svc.Annotations[annoKey]
		if exists && current == desired {
			continue
		}

		svc.Annotations[annoKey] = desired
		if desired != "" {
			log.Info("Setting active service labels", "model", m.Name, "labels", desired)
		} else {
			log.Info("Clearing active service labels (inactive member)", "model", m.Name)
		}
		if err := r.Update(ctx, svc); err != nil {
			log.Error(err, "Failed to update active service labels", "model", m.Name)
		}
	}
}

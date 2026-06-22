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
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// handleSharedGPU implements GPU sharing logic for models with gpu.shared set.
// Demand-based swap tuning constants for shared GPU groups.
const (
	// sharedDemandWindow is how recent a LastActiveTime must be to count as
	// active demand from the proxy.
	sharedDemandWindow = 2 * time.Minute

	// sharedSwapCooldown prevents thrashing by blocking demand-based swaps
	// for this duration after the most recent preemption.  Set high enough
	// to cover model loading (large diffusers models need 3-5 min).
	sharedSwapCooldown = 5 * time.Minute
)

func chooseSharedGroupLeader(groupModels []*aiv1alpha2.Model, now time.Time) *aiv1alpha2.Model {
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
		pa := a.Spec.GetPriority()
		pb := b.Spec.GetPriority()
		if pa != pb {
			if pa > pb {
				return a
			}
			return b
		}
		if a.Status.LastActiveTime != nil && b.Status.LastActiveTime != nil {
			if !a.Status.LastActiveTime.Equal(b.Status.LastActiveTime) {
				if a.Status.LastActiveTime.After(b.Status.LastActiveTime.Time) {
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
	// Use the maximum SwapCooldown from any model in the group, falling
	// back to the default (5m) if none specify it.
	cooldown := sharedSwapCooldown
	for _, m := range groupModels {
		if m.Spec.GPU != nil && m.Spec.GPU.SwapCooldown != nil {
			if d := m.Spec.GPU.SwapCooldown.Duration; d > cooldown {
				cooldown = d
			}
		}
	}
	recentSwap := false
	for _, m := range groupModels {
		if m.Status.SharedGroup != nil && m.Status.SharedGroup.PreemptedAt != nil {
			if now.Sub(m.Status.SharedGroup.PreemptedAt.Time) < cooldown {
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
		if m.Status.LastActiveTime == nil {
			continue
		}
		if now.Sub(m.Status.LastActiveTime.Time) < 5*time.Minute {
			recentLeader = better(recentLeader, m)
		}
		if now.Sub(m.Status.LastActiveTime.Time) < sharedDemandWindow {
			demandedLeader = better(demandedLeader, m)
		}
	}

	// Keep the current loading model active for its cold-start budget. Large
	// first pulls can outlive the short demand window; dropping leadership here
	// scales the pod down before kubelet can finish pulling the backend image.
	if activeLoadingLeader != nil {
		return activeLoadingLeader
	}

	// Demand-based preemption: if a non-ready model has recent demand
	// (proxy set its LastActiveTime) and the current ready leader is idle,
	// swap to the demanded model.
	if demandedLeader != nil && readyLeader != nil {
		readyIdle := readyLeader.Status.LastActiveTime == nil ||
			now.Sub(readyLeader.Status.LastActiveTime.Time) > sharedDemandWindow
		higherPriority := demandedLeader.Spec.GetPriority() > readyLeader.Spec.GetPriority()
		if higherPriority || (readyIdle && demandedLeader.Spec.GetPriority() >= readyLeader.Spec.GetPriority()) {
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
		readyIdle := readyLeader.Status.LastActiveTime == nil ||
			now.Sub(readyLeader.Status.LastActiveTime.Time) > sharedDemandWindow
		if readyIdle {
			return warmPrimaryLeader
		}
	}

	if readyLeader != nil {
		return readyLeader
	}
	if recentLeader != nil && warmPrimaryLeader != nil {
		if recentLeader.Spec.GetPriority() >= warmPrimaryLeader.Spec.GetPriority() {
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

// sharedModelWantsToRun reports whether a member should be considered for one of
// several concurrent Active slots in multi-model mode: it must be runnable and
// either pinned warm, currently Ready, or under recent demand. (The primary
// leader is chosen separately by chooseSharedGroupLeader and always included.)
func sharedModelWantsToRun(m *aiv1alpha2.Model, now time.Time) bool {
	if !sharedModelCanTakeDemand(m) {
		return false
	}
	if isWarmPinnedSharedModel(m) {
		return true
	}
	if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
		return true
	}
	if m.Status.LastActiveTime != nil && now.Sub(m.Status.LastActiveTime.Time) < sharedDemandWindow {
		return true
	}
	return false
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
	if leased {
		return nil
	}
	primary := chooseSharedGroupLeader(groupModels, now)
	if primary == nil {
		return nil
	}
	if !multiModel {
		return []*aiv1alpha2.Model{primary}
	}

	active := []*aiv1alpha2.Model{primary}
	used := vramEstimateMB(primary)

	// Other wanters, highest priority first (deterministic tiebreak by name).
	var candidates []*aiv1alpha2.Model
	for _, m := range groupModels {
		if m.Name == primary.Name {
			continue
		}
		if sharedModelWantsToRun(m, now) {
			candidates = append(candidates, m)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		pi, pj := candidates[i].Spec.GetPriority(), candidates[j].Spec.GetPriority()
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
		active = append(active, m)
		used += est
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
func leaseFreesCard(lease *activeLease, groupModels []*aiv1alpha2.Model) bool {
	if lease == nil {
		return false
	}
	if lease.Priority == nil {
		return true
	}
	for _, m := range groupModels {
		if m.Spec.GetPriority() >= *lease.Priority {
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
	if model == nil || model.Status.LastActiveTime == nil {
		return false
	}

	window := sharedSwapCooldown
	if model.Spec.Serverless != nil && model.Spec.Serverless.ColdStartTimeout != nil {
		if d := model.Spec.Serverless.ColdStartTimeout.Duration; d > window {
			window = d
		}
	}
	return now.Sub(model.Status.LastActiveTime.Time) <= window
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

func (r *ModelReconciler) handleSharedGPU(ctx context.Context, model *aiv1alpha2.Model) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if model.Spec.GPU == nil || model.Spec.GPU.Shared == "" {
		return ctrl.Result{}, nil
	}

	groupName := model.Spec.GPU.Shared

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
	leased := leaseFreesCard(leaseHolder, groupModels)

	// Multi-model election: when the group's GPU runtime can host concurrent
	// subprocesses (GPUProfile feature flag), keep a VRAM-bounded SET of members
	// Active instead of a single leader. Single-slot (default) returns exactly
	// the one leader, so behavior is unchanged for every existing group.
	multiModel, budgetMB := r.sharedGroupMultiModel(model)
	activeModels := chooseSharedGroupLeaders(groupModels, now, multiModel, budgetMB, leased)
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

	// preemptedBy attributes the park: the elected leader's name normally, or a
	// "gpu-lease/<owner>" sentinel while a lease holds the card.
	preemptedBy := ""
	switch {
	case primary != nil:
		preemptedBy = primary.Name
	case leaseHolder != nil:
		preemptedBy = "gpu-lease/" + leaseHolder.Owner
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
			log.Info("Model is active in shared group", "group", groupName)
		}
		// Emit shared-group state metric
		metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Active").Set(1)
		metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Queued").Set(0)
		metrics.SharedGroupState.WithLabelValues(groupName, model.Name, model.Namespace, "Preempted").Set(0)
	} else {
		// This model should be preempted/queued (not in the Active set).
		model.Status.SharedGroup.State = "Queued"
		model.Status.SharedGroup.QueuePosition = queuePositionForSharedModel(model.Name, primary, groupModels)
		model.Status.SharedGroup.PreemptedBy = preemptedBy

		if model.Status.Phase == aiv1alpha2.ModelPhaseReady {
			// Preempt this model
			log.Info("Preempting model", "preemptedBy", preemptedBy, "leased", leased)
			model.Status.Phase = aiv1alpha2.ModelPhasePreempted
			model.Status.LoadingSubstage = aiv1alpha2.LoadingSubstagePreempted
			model.Status.Message = preemptedStatusMessage(model)
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonPreempted, model.Status.Message)
			model.Status.SharedGroup.PreemptedAt = &metav1.Time{Time: time.Now()}
			if leased {
				r.Recorder.Event(model, corev1.EventTypeNormal, "Preempted",
					fmt.Sprintf("Preempted by GPU lease %q (training holds the card)", preemptedBy))
			} else {
				r.Recorder.Event(model, corev1.EventTypeNormal, "Preempted",
					fmt.Sprintf("Preempted by %s with priority %d", primary.Name, primary.Spec.GetPriority()))
			}
			metrics.SharedGroupPreemptionsTotal.WithLabelValues(groupName, model.Namespace, model.Name, preemptedBy).Inc()
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
		return ctrl.Result{RequeueAfter: requeueFast}, nil
	}

	if err := r.Status().Update(ctx, model); err != nil {
		return ctrl.Result{}, err
	}

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

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
	for _, m := range groupModels {
		fallbackLeader = better(fallbackLeader, m)
		if !sharedModelCanTakeDemand(m) {
			continue
		}
		runnableFallbackLeader = better(runnableFallbackLeader, m)
		if isWarmPrimaryModel(m) {
			warmPrimaryLeader = better(warmPrimaryLeader, m)
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
	if runnableFallbackLeader != nil {
		return runnableFallbackLeader
	}
	return fallbackLeader
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

	activeModel := chooseSharedGroupLeader(groupModels, time.Now())
	if activeModel == nil {
		return ctrl.Result{RequeueAfter: requeueFast}, nil
	}

	// Update this model's shared group status
	origPhase := model.Status.Phase
	origShared := cloneSharedGroupStatus(model.Status.SharedGroup)

	if model.Status.SharedGroup == nil {
		model.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{}
	}
	model.Status.SharedGroup.GroupName = groupName

	if activeModel.Name == model.Name {
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
		// This model should be preempted/queued
		model.Status.SharedGroup.State = "Queued"
		model.Status.SharedGroup.QueuePosition = queuePositionForSharedModel(model.Name, activeModel, groupModels)
		model.Status.SharedGroup.PreemptedBy = activeModel.Name

		if model.Status.Phase == aiv1alpha2.ModelPhaseReady {
			// Preempt this model
			log.Info("Preempting model in favor of higher priority", "preemptedBy", activeModel.Name)
			model.Status.Phase = aiv1alpha2.ModelPhasePreempted
			model.Status.LoadingSubstage = aiv1alpha2.LoadingSubstagePreempted
			model.Status.Message = preemptedStatusMessage(model)
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonPreempted, model.Status.Message)
			model.Status.SharedGroup.PreemptedAt = &metav1.Time{Time: time.Now()}
			r.Recorder.Event(model, corev1.EventTypeNormal, "Preempted",
				fmt.Sprintf("Preempted by %s with priority %d", activeModel.Name, activeModel.Spec.GetPriority()))
			metrics.SharedGroupPreemptionsTotal.WithLabelValues(groupName, model.Namespace, model.Name, activeModel.Name).Inc()
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
	// The active model's Service gets ai.flexinfer/active-services set;
	// all other group members have it removed.  This lets the proxy
	// route group-wide names (serviceLabels) only to the active model.
	r.syncActiveServiceLabels(ctx, activeModel, groupModels)

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
func (r *ModelReconciler) syncActiveServiceLabels(ctx context.Context, activeModel *aiv1alpha2.Model, groupModels []*aiv1alpha2.Model) {
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
		if m.Name == activeModel.Name && len(m.Spec.ServiceLabels) > 0 {
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

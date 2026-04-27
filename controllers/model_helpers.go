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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

const (
	warmPolicyPrimary = "primary"
	nodeModeGaming    = "gaming"
)

// desiredReplicas calculates the desired replica count for the model.
// For serverless models, this is driven by LastActiveTime (written by the proxy) and idle timeout.
func (r *ModelReconciler) desiredReplicas(model *aiv1alpha2.Model, b backend.Backend) int32 {
	return r.desiredReplicasForContext(context.Background(), model, b)
}

func (r *ModelReconciler) desiredReplicasForContext(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) int32 {
	// KV-cache eviction: override to 0 replicas while an active KV-cache
	// policy is evicting. Ignore stale status left behind after the policy is
	// removed so old pressure events cannot pin a model at zero forever.
	if model.Spec.KVCache != nil && model.Status.KVCache != nil && model.Status.KVCache.Evicted {
		return 0
	}

	// Shared GPU: only the active model should run.
	if model.Spec.IsShared() && model.Status.SharedGroup != nil {
		if model.Status.SharedGroup.State != "Active" {
			return 0
		}
	}

	if !model.Spec.IsServerless() {
		return 1
	}

	minReplicas := model.Spec.GetMinReplicas()
	if minReplicas < 1 && r.shouldKeepWarmPrimary(ctx, model) {
		minReplicas = 1
	}
	if model.Status.LastActiveTime == nil {
		return minReplicas
	}

	// Never scale down a model that is still loading. The proxy sets
	// LastActiveTime once at request arrival; if loading takes longer than
	// idleTimeout the model would be incorrectly reaped mid-load.
	if model.Status.Phase == aiv1alpha2.ModelPhaseLoading {
		return 1
	}

	idleTimeout := getIdleTimeout(model, b)
	if time.Since(model.Status.LastActiveTime.Time) > idleTimeout {
		return minReplicas
	}

	if minReplicas < 1 {
		return 1
	}
	return minReplicas
}

func (r *ModelReconciler) shouldKeepWarmPrimary(ctx context.Context, model *aiv1alpha2.Model) bool {
	if warmPolicy(model) != warmPolicyPrimary {
		return false
	}

	nodeName := ""
	if model.Spec.NodeSelector != nil {
		nodeName = model.Spec.NodeSelector["kubernetes.io/hostname"]
	}
	if nodeName != "" && r.nodeHasActivePipelineWork(ctx, model.Namespace, nodeName) {
		return false
	}

	if r.Runtime == nil {
		return true
	}

	endpoint, err := r.Runtime.FindRuntimeForNode(ctx, model.Namespace, model.Spec.NodeSelector)
	if err != nil || endpoint == nil || !endpoint.Ready {
		return true
	}
	mode, err := r.Runtime.GetMode(ctx, endpoint)
	if err != nil {
		return true
	}
	return mode != nodeModeGaming
}

func warmPolicy(model *aiv1alpha2.Model) string {
	if model == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(model.Spec.ConfigString("warmPolicy", "")))
}

func isWarmPrimaryModel(model *aiv1alpha2.Model) bool {
	return warmPolicy(model) == warmPolicyPrimary
}

func (r *ModelReconciler) nodeHasActivePipelineWork(ctx context.Context, namespace, nodeName string) bool {
	if nodeName == "" || r.Client == nil {
		return false
	}

	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(namespace)); err != nil {
		return false
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if !podTargetsNode(pod, nodeName) {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodPending, corev1.PodRunning:
		default:
			continue
		}
		if isActivePipelinePod(pod) {
			return true
		}
	}
	return false
}

// podTargetsNode reports whether pod is either already scheduled to nodeName
// or is Pending with a nodeSelector pinning it to nodeName. Unscheduled Pending
// pods have an empty Spec.NodeName, so a naive NodeName comparison misses
// quant/abliterate jobs blocked on a warm-primary GPU.
func podTargetsNode(pod *corev1.Pod, nodeName string) bool {
	if pod == nil || nodeName == "" {
		return false
	}
	if pod.Spec.NodeName == nodeName {
		return true
	}
	if pod.Spec.NodeName == "" && pod.Status.Phase == corev1.PodPending {
		if pod.Spec.NodeSelector["kubernetes.io/hostname"] == nodeName {
			return true
		}
	}
	return false
}

func isActivePipelinePod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	switch pod.Labels[LabelComponent] {
	case "abliterator", "quantizer", "finetuner", "publisher":
		return true
	}

	name := pod.Name
	jobName := pod.Labels["job-name"]
	for _, v := range []string{name, jobName} {
		if strings.Contains(v, "-abliterate") ||
			strings.Contains(v, "-quantize") ||
			strings.Contains(v, "-finetune") ||
			strings.Contains(v, "-publish") {
			return true
		}
	}
	return false
}

// cleanupModel removes all resources created for the model.
func (r *ModelReconciler) cleanupModel(ctx context.Context, model *aiv1alpha2.Model) error {
	log := log.FromContext(ctx)

	// Delete deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err == nil {
		if err := r.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
			return err
		}
		log.Info("Deleted deployment", "name", model.Name)
	}

	// Delete service
	service := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, service); err == nil {
		if err := r.Delete(ctx, service); err != nil && !errors.IsNotFound(err) {
			return err
		}
		log.Info("Deleted service", "name", model.Name)
	}

	// Clean up persistent flash-tmpfs on the node for shared models.
	if err := r.cleanupFlashTmpfs(ctx, model); err != nil {
		log.Error(err, "Failed to create flash-tmpfs cleanup job (non-fatal)")
	}

	return nil
}

// labelsForModel returns the labels to apply to resources for this model.
func (r *ModelReconciler) labelsForModel(model *aiv1alpha2.Model) map[string]string {
	labels := r.selectorLabelsForModel(model)

	var gpuGroup string
	if model.Spec.GPU != nil && model.Spec.GPU.Shared != "" {
		gpuGroup = model.Spec.GPU.Shared
	}
	if gpuGroup == "" && model.Status.SharedGroup != nil && model.Status.SharedGroup.GroupName != "" {
		gpuGroup = model.Status.SharedGroup.GroupName
	}
	if gpuGroup != "" {
		labels[LabelGPUGroup] = gpuGroup
	}

	return labels
}

func (r *ModelReconciler) selectorLabelsForModel(model *aiv1alpha2.Model) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "model",
		"app.kubernetes.io/instance":   model.Name,
		"app.kubernetes.io/managed-by": "flexinfer",
		LabelModel:                     model.Name,
		LabelBackend:                   model.Spec.Backend,
	}
}

// getIdleTimeout returns the idle timeout for the model.
func getIdleTimeout(model *aiv1alpha2.Model, b backend.Backend) time.Duration {
	if model.Spec.Serverless != nil && model.Spec.Serverless.IdleTimeout != nil {
		return model.Spec.Serverless.IdleTimeout.Duration
	}
	return b.DefaultIdleTimeout()
}

func modelUsesPersistentVolume(model *aiv1alpha2.Model) bool {
	if _, _, ok := parsePVCSource(model.Spec.Source); ok {
		return true
	}
	s := cacheStrategy(model)
	return s == "SharedPVC" || s == "Local"
}

// checkAliasConflicts detects litellm.aliases and copilotAlias conflicts
// across all Models in the namespace. Sets the ConfigValid condition accordingly.
// litellm.aliases are global (proxy resolves across ALL models), so duplicates
// cause non-deterministic routing. serviceLabels are exempt -- they are group-aware.
func (r *ModelReconciler) checkAliasConflicts(ctx context.Context, model *aiv1alpha2.Model) {
	if model.Spec.LiteLLM == nil {
		setModelCondition(model, aiv1alpha2.ConditionConfigValid, true, aiv1alpha2.ReasonConfigValid, "No litellm config to validate")
		return
	}

	var allModels aiv1alpha2.ModelList
	if err := r.List(ctx, &allModels, client.InNamespace(model.Namespace)); err != nil {
		return // skip check on list failure
	}

	// Build alias -> owner map from all other models.
	type claim struct {
		alias string
		owner string
	}
	var conflicts []claim

	aliasOwners := make(map[string]string) // alias -> model name
	for i := range allModels.Items {
		m := &allModels.Items[i]
		if m.Name == model.Name || m.Spec.LiteLLM == nil {
			continue
		}
		if served := m.Spec.LiteLLM.ServedModelName; served != "" {
			aliasOwners[served] = m.Name
		}
		for _, alias := range m.Spec.LiteLLM.Aliases {
			aliasOwners[alias] = m.Name
		}
		if cop := m.Spec.LiteLLM.CopilotAlias; cop != "" {
			aliasOwners["copilotAlias:"+cop] = m.Name
		}
	}

	// Check this model's aliases against the map.
	if served := model.Spec.LiteLLM.ServedModelName; served != "" {
		if owner, ok := aliasOwners[served]; ok {
			conflicts = append(conflicts, claim{alias: served, owner: owner})
		}
	}
	for _, alias := range model.Spec.LiteLLM.Aliases {
		if owner, ok := aliasOwners[alias]; ok {
			conflicts = append(conflicts, claim{alias: alias, owner: owner})
		}
	}
	if cop := model.Spec.LiteLLM.CopilotAlias; cop != "" {
		if owner, ok := aliasOwners["copilotAlias:"+cop]; ok {
			conflicts = append(conflicts, claim{alias: "copilotAlias:" + cop, owner: owner})
		}
	}

	if len(conflicts) > 0 {
		msgs := make([]string, 0, len(conflicts))
		for _, c := range conflicts {
			msgs = append(msgs, fmt.Sprintf("%q conflicts with %s", c.alias, c.owner))
		}
		msg := "litellm alias conflicts (use serviceLabels for group-wide routing): " + strings.Join(msgs, "; ")
		setModelCondition(model, aiv1alpha2.ConditionConfigValid, false, aiv1alpha2.ReasonAliasConflict, msg)
		r.Recorder.Event(model, corev1.EventTypeWarning, aiv1alpha2.ReasonAliasConflict, msg)
		return
	}

	setModelCondition(model, aiv1alpha2.ConditionConfigValid, true, aiv1alpha2.ReasonConfigValid, "No alias conflicts detected")
}

// updateStatusFromDeployment updates the Model status based on the deployment state.
func (r *ModelReconciler) updateStatusFromDeployment(ctx context.Context, model *aiv1alpha2.Model) error {
	log := log.FromContext(ctx)
	prevPhase := model.Status.Phase
	prevReadyCond := modelCondition(model.Status.Conditions, aiv1alpha2.ConditionModelReady)
	readyStartedAt := time.Time{}
	prevReadyReason := ""
	if prevReadyCond != nil && prevReadyCond.Status == metav1.ConditionFalse {
		readyStartedAt = prevReadyCond.LastTransitionTime.Time
		prevReadyReason = prevReadyCond.Reason
	} else if prevReadyCond != nil {
		prevReadyReason = prevReadyCond.Reason
	}
	now := time.Now()

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Update endpoint
	port := int32(80)
	if b, ok := backend.Get(model.Spec.Backend); ok {
		port = b.Port()
	} else {
		log.Error(fmt.Errorf("backend %q not found", model.Spec.Backend), "failed to resolve backend port for endpoint", "backend", model.Spec.Backend)
	}
	model.Status.Endpoint = k8surl.ServiceURL(model.Name, model.Namespace, port, false)

	// If the cache is not ready, keep the model in Pending regardless of deployment replicas.
	if model.Status.Cache != nil && !model.Status.Cache.Ready {
		setModelCondition(model, aiv1alpha2.ConditionModelCached, false, aiv1alpha2.ReasonCacheNotReady, "Cache is not ready")
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonCacheNotReady, "Waiting for cache to be ready")
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			model.Status.Phase = aiv1alpha2.ModelPhasePending
		}
		return r.Status().Update(ctx, model)
	}

	// Cache is ready (or not applicable)
	if model.Status.Cache != nil && model.Status.Cache.Ready {
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, aiv1alpha2.ReasonBackendReady, "Cache is ready")
	}

	// Determine phase from deployment status and set conditions.
	// Substage/message/progress timestamp are Loading-phase-only: reset them on
	// every pass and repopulate below only when phase transitions to (or remains
	// on) Loading.
	model.Status.LoadingSubstage = ""
	model.Status.Message = ""
	model.Status.LoadingProgressAt = nil

	if deployment.Status.ReadyReplicas > 0 {
		model.Status.Phase = aiv1alpha2.ModelPhaseReady
		setModelCondition(model, aiv1alpha2.ConditionModelReady, true, aiv1alpha2.ReasonBackendReady, "Backend is ready to serve requests")

		if prevPhase != aiv1alpha2.ModelPhaseReady && !readyStartedAt.IsZero() {
			loadDuration := now.Sub(readyStartedAt).Seconds()
			metrics.ModelColdStartDurationSeconds.WithLabelValues(
				model.Name,
				model.Namespace,
				model.Spec.Backend,
				cacheStrategy(model),
			).Observe(loadDuration)

			// Set last-known load time for the Mission Control dashboard.
			nodeName := ""
			if model.Status.GPU != nil {
				nodeName = model.Status.GPU.Node
			}
			metrics.ModelLoadSeconds.WithLabelValues(model.Name, nodeName).Set(loadDuration)
		}

		if model.Spec.IsShared() && model.Status.SharedGroup != nil && model.Status.SharedGroup.State == "Active" {
			group := model.Status.SharedGroup.GroupName
			swapStart := time.Time{}
			if model.Status.SharedGroup.PreemptedAt != nil {
				swapStart = model.Status.SharedGroup.PreemptedAt.Time
			} else if prevReadyReason == aiv1alpha2.ReasonPreempted && !readyStartedAt.IsZero() {
				swapStart = readyStartedAt
			}
			if !swapStart.IsZero() {
				metrics.ModelSwapDurationSeconds.WithLabelValues(
					model.Name,
					model.Namespace,
					model.Spec.Backend,
					group,
				).Observe(now.Sub(swapStart).Seconds())
				model.Status.SharedGroup.PreemptedAt = nil
			}
		}
	} else if *deployment.Spec.Replicas == 0 {
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			model.Status.Phase = aiv1alpha2.ModelPhaseIdle
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonWaitingForActivation, "Model is idle, waiting for traffic")
		} else {
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonPreempted, "Model was preempted by higher priority model")
		}
	} else if deployment.Status.UnavailableReplicas > 0 {
		model.Status.Phase = aiv1alpha2.ModelPhaseLoading
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonStartingBackend, "Backend container is starting")
		r.populateLoadingSubstage(ctx, model)
	} else {
		model.Status.Phase = aiv1alpha2.ModelPhasePending
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonStartingBackend, "Waiting for deployment to be ready")
	}

	// Emit model lifecycle metrics for phase changes computed above.
	r.recordPhaseMetrics(model, prevPhase, model.Status.Phase)

	return r.Status().Update(ctx, model)
}

// updatePhase updates just the phase field in status and emits lifecycle metrics.
func (r *ModelReconciler) updatePhase(ctx context.Context, model *aiv1alpha2.Model, phase aiv1alpha2.ModelPhase) error {
	oldPhase := model.Status.Phase
	model.Status.Phase = phase
	r.recordPhaseMetrics(model, oldPhase, phase)
	return r.Status().Update(ctx, model)
}

// recordPhaseMetrics emits Prometheus metrics for a model phase transition.
func (r *ModelReconciler) recordPhaseMetrics(model *aiv1alpha2.Model, from, to aiv1alpha2.ModelPhase) {
	ns := model.Namespace
	name := model.Name

	// Update the phase gauge: set current phase to 1, clear all others.
	allPhases := []aiv1alpha2.ModelPhase{
		aiv1alpha2.ModelPhaseIdle, aiv1alpha2.ModelPhasePending,
		aiv1alpha2.ModelPhaseLoading, aiv1alpha2.ModelPhaseReady,
		aiv1alpha2.ModelPhasePreempted, aiv1alpha2.ModelPhaseFailed,
	}
	for _, p := range allPhases {
		val := float64(0)
		if p == to {
			val = 1
		}
		metrics.ModelPhase.WithLabelValues(name, ns, string(p)).Set(val)
	}

	// Count the transition.
	if from != "" && from != to {
		metrics.ModelTransitionsTotal.WithLabelValues(name, ns, string(from), string(to), "reconcile").Inc()
	}

	// Record ready latency when transitioning to Ready for the first time.
	if to == aiv1alpha2.ModelPhaseReady && from != aiv1alpha2.ModelPhaseReady {
		if !model.CreationTimestamp.IsZero() {
			latency := time.Since(model.CreationTimestamp.Time).Seconds()
			metrics.ModelReadyLatencySeconds.WithLabelValues(name, ns, model.Spec.Backend).Observe(latency)
		}
	}
}

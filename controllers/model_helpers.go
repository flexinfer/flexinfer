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
	"slices"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
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
	// Gaming overlay: while a GamingSession holds this model's pinned node,
	// nothing inference-shaped runs there — including dedicated Deployments and
	// explicit minReplicas warm floors, which previously had to be zeroed by
	// hand-editing manifests for every lease.
	if gs := r.gamingHoldForModel(ctx, model); gs != nil {
		return 0
	}

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

	// An honored activation request is demand for a bounded window: it wakes a
	// scale-to-zero model without pinning minReplicas, and expires on its own.
	// It sits below the gaming, KV-eviction and shared-GPU overrides above, so
	// an operator poke can never fight a lease or evict a group leader.
	if activationDemand(model) {
		return max(1, minReplicas)
	}

	if model.Status.LastActiveTime == nil {
		// preload-on-deploy: warm a non-shared serverless model proactively from
		// deploy until its first request, so the first post-deploy request skips
		// cold start. Distinct from minReplicas=1 (which never scales to zero):
		// once the first request sets LastActiveTime, normal idle scale-to-zero
		// resumes. Shared-GPU models are excluded (the guard above returns 0 for
		// non-Active members) so preload can never evict a live group leader.
		if isPreloadWarming(model) {
			return 1
		}
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

// preloadOnDeploy reports whether the model opts into proactive warm-up after
// deploy via `config.preloadOnDeploy: true`. Opt-in, default off.
func preloadOnDeploy(model *aiv1alpha2.Model) bool {
	if model == nil {
		return false
	}
	return model.Spec.ConfigBool("preloadOnDeploy", false)
}

// isPreloadWarming reports whether a model should be held warm by the
// preload-on-deploy policy: opted in, serverless, scale-to-zero (minReplicas<1),
// NOT part of a shared GPU group, and not yet activated since deploy
// (LastActiveTime nil). Shared-GPU members are intentionally excluded so preload
// can never contend with or evict a group leader; they use warm-pinning
// (minReplicas>=1) instead.
func isPreloadWarming(model *aiv1alpha2.Model) bool {
	if model == nil {
		return false
	}
	return preloadOnDeploy(model) &&
		model.Spec.IsServerless() &&
		!model.Spec.IsShared() &&
		model.Spec.GetMinReplicas() < 1 &&
		model.Status.LastActiveTime == nil
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
// across all advertised Models in the namespace. Sets the ConfigValid condition
// accordingly. litellm.aliases are global (proxy resolves across ALL advertised
// models), so duplicates cause non-deterministic routing. serviceLabels are
// exempt -- they are group-aware.
func (r *ModelReconciler) checkAliasConflicts(ctx context.Context, model *aiv1alpha2.Model) {
	if model.Spec.LiteLLM == nil {
		setModelCondition(model, aiv1alpha2.ConditionConfigValid, true, aiv1alpha2.ReasonConfigValid, "No litellm config to validate")
		return
	}
	if !litellmEnabled(model) {
		setModelCondition(model, aiv1alpha2.ConditionConfigValid, true, aiv1alpha2.ReasonConfigValid, "litellm disabled; aliases ignored")
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
		if m.Name == model.Name || m.Spec.LiteLLM == nil || !litellmEnabled(m) {
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
	// Snapshot the status before any mutation so the trailing writes can be
	// skipped when nothing meaningful changed. An unconditional Status().Update
	// on a steady idle/preempted model still bumps the object's
	// resourceVersion (field-manager bookkeeping churns because the `proxy`
	// manager co-owns status.lastActiveTime), and the unfiltered For(&Model{})
	// watch then re-enqueues the Model on its own write — a self-sustaining
	// ~1Hz reconcile loop that also re-emits the VRAMPressure event every pass.
	origStatus := model.Status.DeepCopy()
	prevPhase := model.Status.Phase
	readyStartedAt, prevReadyReason := readyTransitionSnapshot(model)
	now := time.Now()

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Record actual placement (status.gpu) from the scheduled backend pod
	// before the metric blocks below read Status.GPU.Node.
	r.stampGPUStatusFromPods(ctx, model)

	// Update endpoint
	port := int32(80)
	if b, ok := backend.Get(model.Spec.Backend); ok {
		port = b.Port()
	} else {
		log.Error(fmt.Errorf("backend %q not found", model.Spec.Backend), "failed to resolve backend port for endpoint", "backend", model.Spec.Backend)
	}
	model.Status.Endpoint = k8surl.ServiceURL(model.Name, model.Namespace, port, false)

	// ensureCache is the sole owner of the Cached condition's detailed
	// reason/message. Rewriting it here from CacheCopy/CacheStage/CacheCheck to a
	// generic deployment reason makes the two reconcile stages fight over status
	// and continuously re-arm the Model watch.
	//
	// If the cache is not ready, keep the model in Pending regardless of deployment replicas.
	if model.Status.Cache != nil && !model.Status.Cache.Ready {
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonCacheNotReady, "Waiting for cache to be ready")
		clearLoadingStatus(model)
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			model.Status.Phase = aiv1alpha2.ModelPhasePending
		}
		return r.statusUpdateIfChanged(ctx, model, origStatus)
	}

	// Determine phase from deployment status and set conditions.
	// LoadingSubstage/Message/LoadingProgressAt are Loading-phase-only, but do
	// not clear them before a Loading update. populateLoadingSubstage compares
	// against the previous values so LoadingProgressAt advances only when the
	// observed load state changes; that frozen timestamp is what lets the proxy
	// detect wedged weight loads.

	if deployment.Status.ReadyReplicas > 0 {
		recheckDeleted := false
		if r.KubeClient != nil {
			deleted, err := r.observeReadyKVPool(ctx, model, func(pod *corev1.Pod) (string, error) {
				return readKVPoolBootLog(ctx, r.KubeClient, pod)
			})
			if err != nil {
				return err
			}
			recheckDeleted = deleted
		}
		if recheckDeleted {
			// Publishing Ready here would release the proxy's queued requests
			// onto the terminating pod; the replacement's readiness re-marks Ready.
			model.Status.Phase = aiv1alpha2.ModelPhaseLoading
			model.Status.Message = "Restarting vLLM pod to recheck a short KV cache pool"
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonKVPoolRecheck, model.Status.Message)
		} else {
			model.Status.Phase = aiv1alpha2.ModelPhaseReady
			clearLoadingStatus(model)
			setModelCondition(model, aiv1alpha2.ConditionModelReady, true, aiv1alpha2.ReasonBackendReady, "Backend is ready to serve requests")
		}

		if !recheckDeleted && prevPhase != aiv1alpha2.ModelPhaseReady {
			observeColdStart(model, readyStartedAt, now, metrics.ServingPathDeployment)
		}
		if observeSharedSwap(model, readyStartedAt, prevReadyReason, now, metrics.ServingPathDeployment) {
			model.Status.SharedGroup.PreemptedAt = nil
		}
	} else if *deployment.Spec.Replicas == 0 {
		clearLoadingStatus(model)
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			model.Status.Phase = aiv1alpha2.ModelPhaseIdle
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonWaitingForActivation, "Model is idle, waiting for traffic")
		} else {
			reason, msg := preemptedConditionReasonMessage(model)
			model.Status.LoadingSubstage = aiv1alpha2.LoadingSubstagePreempted
			model.Status.Message = msg
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, reason, msg)
		}
	} else if deployment.Status.UnavailableReplicas > 0 {
		model.Status.Phase = aiv1alpha2.ModelPhaseLoading
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonStartingBackend, "Backend container is starting")
		r.populateLoadingSubstage(ctx, model)
	} else {
		model.Status.Phase = aiv1alpha2.ModelPhasePending
		clearLoadingStatus(model)
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonStartingBackend, "Waiting for deployment to be ready")
	}

	// Emit model lifecycle metrics for phase changes computed above.
	r.recordPhaseMetrics(model, prevPhase, model.Status.Phase)

	return r.statusUpdateIfChanged(ctx, model, origStatus)
}

// readyTransitionSnapshot returns when ModelReady last went False (zero while it
// is True or absent) and its current reason. Callers take it before writing the
// pass's Ready condition, so a Model that just became Ready still reports when
// its load started.
func readyTransitionSnapshot(model *aiv1alpha2.Model) (time.Time, string) {
	cond := modelCondition(model.Status.Conditions, aiv1alpha2.ConditionModelReady)
	if cond == nil {
		return time.Time{}, ""
	}
	if cond.Status == metav1.ConditionFalse {
		return cond.LastTransitionTime.Time, cond.Reason
	}
	return time.Time{}, cond.Reason
}

// observeColdStart records the activation-to-Ready latency for a Model that
// just became Ready, measured from readyStartedAt (see readyTransitionSnapshot),
// in the cold-start histogram and the last-load gauge. serving_path labels the serving
// path so dedicated-Deployment and in-pod runtime loads can be compared.
func observeColdStart(model *aiv1alpha2.Model, readyStartedAt, now time.Time, path string) {
	if readyStartedAt.IsZero() {
		return
	}
	loadDuration := now.Sub(readyStartedAt).Seconds()
	metrics.ModelColdStartDurationSeconds.WithLabelValues(
		model.Name,
		model.Namespace,
		model.Spec.Backend,
		cacheStrategy(model),
		path,
	).Observe(loadDuration)

	// Set last-known load time for the Mission Control dashboard.
	nodeName := ""
	if model.Status.GPU != nil {
		nodeName = model.Status.GPU.Node
	}
	metrics.ModelLoadSeconds.WithLabelValues(model.Name, nodeName).Set(loadDuration)
}

// observeSharedSwap records swap-in latency for an Active shared-group member
// that is Ready: from SharedGroup.PreemptedAt when the election recorded one,
// else from the ModelReady=False transition when that was a preemption or lease
// hold. It reports whether it observed; clearing PreemptedAt is the caller's
// decision, because the election also reads it as its anti-thrash anchor.
func observeSharedSwap(model *aiv1alpha2.Model, readyStartedAt time.Time, prevReadyReason string, now time.Time, path string) bool {
	if !model.Spec.IsShared() || model.Status.SharedGroup == nil || model.Status.SharedGroup.State != "Active" {
		return false
	}
	swapStart := time.Time{}
	if model.Status.SharedGroup.PreemptedAt != nil {
		swapStart = model.Status.SharedGroup.PreemptedAt.Time
	} else if (prevReadyReason == aiv1alpha2.ReasonPreempted || prevReadyReason == aiv1alpha2.ReasonGPULeaseHeld || prevReadyReason == aiv1alpha2.ReasonGamingLeaseHeld) && !readyStartedAt.IsZero() {
		swapStart = readyStartedAt
	}
	if swapStart.IsZero() {
		return false
	}
	metrics.ModelSwapDurationSeconds.WithLabelValues(
		model.Name,
		model.Namespace,
		model.Spec.Backend,
		model.Status.SharedGroup.GroupName,
		path,
	).Observe(now.Sub(swapStart).Seconds())
	return true
}

// statusUpdateIfChanged writes the Model status only when it differs from the
// supplied snapshot. Skipping no-op writes is what breaks the idle-reconcile
// hot loop: a Status().Update with byte-identical status still advances the
// object's resourceVersion (field-manager time bookkeeping), and the
// unfiltered For(&Model{}) watch re-enqueues the Model on its own write. The
// generated DeepEqual is conservative — any genuine phase/condition/endpoint
// change still falls through to the write.
func (r *ModelReconciler) statusUpdateIfChanged(ctx context.Context, model *aiv1alpha2.Model, orig *aiv1alpha2.ModelStatus) error {
	if orig != nil && apiequality.Semantic.DeepEqual(orig, &model.Status) {
		return nil
	}
	if err := r.Status().Update(ctx, model); err != nil {
		return err
	}
	// Keep the pass-wide observation (persistStatus) current so a later writer
	// in the same reconcile does not repeat this write.
	observedStatusFrom(ctx).refresh(model)
	return nil
}

func preemptedStatusMessage(model *aiv1alpha2.Model) string {
	if model != nil && model.Status.SharedGroup != nil && model.Status.SharedGroup.PreemptedBy != "" {
		return fmt.Sprintf("preempted by %s", model.Status.SharedGroup.PreemptedBy)
	}
	return "Model was preempted by higher priority model"
}

// gpuLeasePreemptedByPrefix marks a SharedGroup.PreemptedBy attribution as a GPU
// lease (training/quant) rather than a sibling serving model. The election sets
// "gpu-lease/<owner>"; consumers detect a lease park via this prefix.
const gpuLeasePreemptedByPrefix = "gpu-lease/"

// preemptedConditionReasonMessage returns the ModelReady condition reason and
// message for a parked shared-GPU model, distinguishing a training/quant GPU
// lease hold (ReasonGPULeaseHeld) from a serving-vs-serving preemption
// (ReasonPreempted). A lease park is intentional and transient, so surfacing it
// with a distinct reason keeps dashboards/proxy/operators from reading the
// parked lane as an outage.
func preemptedConditionReasonMessage(model *aiv1alpha2.Model) (reason, message string) {
	if model != nil && model.Status.SharedGroup != nil {
		pb := model.Status.SharedGroup.PreemptedBy
		if strings.HasPrefix(pb, gpuLeasePreemptedByPrefix) {
			owner := strings.TrimPrefix(pb, gpuLeasePreemptedByPrefix)
			return aiv1alpha2.ReasonGPULeaseHeld,
				fmt.Sprintf("Card held by training (GPU lease %q); serving parked until the lease releases", owner)
		}
		if strings.HasPrefix(pb, gamingPreemptedByPrefix) {
			session := strings.TrimPrefix(pb, gamingPreemptedByPrefix)
			return aiv1alpha2.ReasonGamingLeaseHeld,
				fmt.Sprintf("Node leased for gaming (session %q); serving parked until the session ends", session)
		}
		if strings.HasPrefix(pb, aiv1alpha2.PreemptedByPrimaryPrefix) {
			primary := strings.TrimPrefix(pb, aiv1alpha2.PreemptedByPrimaryPrefix)
			return aiv1alpha2.ReasonParkedBehindPrimary,
				fmt.Sprintf("Parked behind warm primary %q (lower priority; not promotable on demand under current config)", primary)
		}
	}
	return aiv1alpha2.ReasonPreempted, preemptedStatusMessage(model)
}

func clearLoadingStatus(model *aiv1alpha2.Model) {
	if model == nil {
		return
	}
	model.Status.LoadingSubstage = ""
	model.Status.Message = ""
	model.Status.LoadingProgressAt = nil
}

// updatePhase updates just the phase field in status and emits lifecycle
// metrics. The write is skipped when the pass reproduced the recorded status
// (persistStatus), so re-asserting the current phase costs nothing.
func (r *ModelReconciler) updatePhase(ctx context.Context, model *aiv1alpha2.Model, phase aiv1alpha2.ModelPhase) error {
	oldPhase := model.Status.Phase
	model.Status.Phase = phase
	r.recordPhaseMetrics(model, oldPhase, phase)
	return r.persistStatus(ctx, model)
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

	// Surface whether preload-on-deploy is currently holding this model warm
	// ahead of its first request (1) or not (0).
	preloadVal := float64(0)
	if isPreloadWarming(model) {
		preloadVal = 1
	}
	metrics.ModelPreloadActive.WithLabelValues(name, ns).Set(preloadVal)

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

// observeReadyKVPool reads each ready pod once and consumes the ReplicaSet's
// recheck budget in status before deletion. A failed delete consumes the budget
// too: safety takes precedence over retrying an ambiguous API result. The boot
// log read is best-effort: a failure leaves status.kvPool untouched (so the next
// reconcile retries) and never blocks Ready. deleted reports that a pod was
// deleted this pass, so the caller must not publish Ready.
func (r *ModelReconciler) observeReadyKVPool(ctx context.Context, model *aiv1alpha2.Model, readLog func(*corev1.Pod) (string, error)) (deleted bool, err error) {
	if model.Spec.Backend != "vllm" || !model.Spec.ConfigBool("kvPoolRecheck", true) {
		return false, nil
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(model.Namespace), client.MatchingLabels{LabelModel: model.Name}); err != nil {
		return false, err
	}
	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].CreationTimestamp.After(pods.Items[j].CreationTimestamp.Time)
	})
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		ready := false
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if !ready {
			continue
		}
		owner := metav1.GetControllerOf(pod)
		if owner == nil || owner.Kind != "ReplicaSet" {
			continue
		}
		// The pod's controlling ReplicaSet UID is stable across replacements
		// and changes on rollout. Reading it from the owner reference avoids
		// requiring ReplicaSet read permissions on the controller.
		pool := model.Status.KVPool
		if pool == nil || pool.PodName != pod.Name || pool.PodUID != string(pod.UID) {
			var marker []string
			if pool != nil {
				marker = pool.RecheckedReplicaSets
			}
			bootLog, err := readLog(pod)
			if err != nil {
				log.FromContext(ctx).Info("skipping KV pool observation: boot log read failed", "pod", pod.Name, "error", err.Error())
				return false, nil
			}
			tokens, concurrency, found := parseVLLMKVPool(bootLog)
			pool = &aiv1alpha2.KVPoolStatus{PodName: pod.Name, PodUID: string(pod.UID), ObservedAt: metav1.Now(), RecheckedReplicaSets: marker}
			if found {
				pool.KVCacheTokens = &tokens
				pool.MaxConcurrency = concurrency
			}
			model.Status.KVPool = pool
		}
		if pool.KVCacheTokens != nil {
			metrics.ModelKVCacheTokens.WithLabelValues(model.Name).Set(float64(*pool.KVCacheTokens))
		} else {
			metrics.ModelKVCacheTokens.DeleteLabelValues(model.Name)
		}
		short := updateKVPoolCondition(model)
		generation := string(owner.UID)
		if !short || generation == "" || slices.Contains(pool.RecheckedReplicaSets, generation) {
			return false, nil
		}
		pool.RecheckedReplicaSets = append(pool.RecheckedReplicaSets, generation)
		if err := r.Status().Update(ctx, model); err != nil {
			return false, err
		}
		// UID precondition ensures a replacement with the same name is never deleted.
		uid := pod.UID
		if err := r.Delete(ctx, pod, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		metrics.ModelKVPoolRecheckTotal.WithLabelValues(model.Name).Inc()
		if r.Recorder != nil {
			r.Recorder.Eventf(model, corev1.EventTypeNormal, aiv1alpha2.ReasonKVPoolRecheck, "Rechecking pod %s: kvCacheTokens=%d maxConcurrency=%sx maxNumSeqs=%d", pod.Name, *pool.KVCacheTokens, pool.MaxConcurrency, model.Spec.ConfigInt("maxNumSeqs", 0))
		}
		return true, nil
	}
	return false, nil
}

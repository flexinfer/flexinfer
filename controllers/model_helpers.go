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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

var managedModelAnnotations = []string{
	"litellm.flexinfer.ai/served-model",
	"litellm.flexinfer.ai/aliases",
	"litellm.flexinfer.ai/copilot-model",
	"litellm.flexinfer.ai/capabilities",
	"flexinfer.ai/service-labels",
}

var managedModelPodAnnotations = []string{
	"flexinfer.ai/model",
	"flexinfer.ai/backend",
	"flexinfer.ai/gpu.vram-estimate-mb",
}

func applyManagedAnnotations(existing map[string]string, desired map[string]string, managedKeys []string) map[string]string {
	out := make(map[string]string, len(existing)+len(desired))
	for k, v := range existing {
		out[k] = v
	}
	for _, k := range managedKeys {
		if desired != nil {
			if v, ok := desired[k]; ok && v != "" {
				out[k] = v
				continue
			}
		}
		delete(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringMap(existing map[string]string, additional map[string]string) map[string]string {
	if len(existing) == 0 && len(additional) == 0 {
		return nil
	}
	out := make(map[string]string, len(existing)+len(additional))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range additional {
		out[k] = v
	}
	return out
}

// desiredReplicas calculates the desired replica count for the model.
// For serverless models, this is driven by LastActiveTime (written by the proxy) and idle timeout.
func (r *ModelReconciler) desiredReplicas(model *aiv1alpha2.Model, b backend.Backend) int32 {
	// KV-cache eviction: override to 0 replicas while evicted.
	if model.Status.KVCache != nil && model.Status.KVCache.Evicted {
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

// cleanupFlashTmpfs creates a short-lived Job to remove the persistent
// /dev/shm/flexinfer/{ns}/{model} directory on the target node.
// Only applies to shared models that use hostPath-based flash-tmpfs.
func (r *ModelReconciler) cleanupFlashTmpfs(ctx context.Context, model *aiv1alpha2.Model) error {
	if !model.Spec.IsShared() {
		return nil
	}
	if len(model.Spec.NodeSelector) == 0 {
		return nil
	}

	log := log.FromContext(ctx)
	flashDir := filepath.Join("/dev/shm/flexinfer", model.Namespace, model.Name)

	// Tolerate dedicated GPU nodes so the cleanup pod can schedule on tainted nodes.
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-tmpfs-cleanup",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(1)),
			TTLSecondsAfterFinished: ptr.To(int32(120)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeSelector:                 model.Spec.NodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "cleanup",
						Image:   "busybox:1.37",
						Command: []string{"rm", "-rf", flashDir},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("16Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
						},
					}},
				},
			},
		},
	}

	// No owner reference — model is being deleted, so the Job must
	// outlive the model. TTLSecondsAfterFinished handles auto-cleanup.
	if err := r.Create(ctx, job); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create flash-tmpfs cleanup job: %w", err)
	}
	log.Info("Created flash-tmpfs cleanup job", "path", flashDir)
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
		labels["flexinfer.ai/gpu-group"] = gpuGroup
	}

	return labels
}

func (r *ModelReconciler) selectorLabelsForModel(model *aiv1alpha2.Model) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "model",
		"app.kubernetes.io/instance":   model.Name,
		"app.kubernetes.io/managed-by": "flexinfer",
		"flexinfer.ai/model":           model.Name,
		"flexinfer.ai/backend":         model.Spec.Backend,
	}
}

func (r *ModelReconciler) podAnnotationsForModel(model *aiv1alpha2.Model) map[string]string {
	ann := map[string]string{
		"flexinfer.ai/model":   model.Name,
		"flexinfer.ai/backend": model.Spec.Backend,
	}
	if model.Spec.GPU != nil && model.Spec.GPU.VRAMEstimateMB != nil && *model.Spec.GPU.VRAMEstimateMB > 0 {
		ann["flexinfer.ai/gpu.vram-estimate-mb"] = fmt.Sprintf("%d", *model.Spec.GPU.VRAMEstimateMB)
	}
	return ann
}

// shouldScaleToZero checks if the model should be scaled to zero.
// getIdleTimeout returns the idle timeout for the model.
func getIdleTimeout(model *aiv1alpha2.Model, b backend.Backend) time.Duration {
	if model.Spec.Serverless != nil && model.Spec.Serverless.IdleTimeout != nil {
		return model.Spec.Serverless.IdleTimeout.Duration
	}
	return b.DefaultIdleTimeout()
}

type flashLoaderRuntimeConfig struct {
	Enabled         bool
	Image           string
	Concurrency     int
	TmpfsSizeLimit  *resource.Quantity
	BufferSizeKB    int
	VerifyIntegrity bool
	ExcludePatterns string
}

const (
	defaultFlashLoaderImage       = "registry.harbor.lan/flexinfer/flash-loader:latest"
	defaultFlashLoaderConcurrency = 4
	defaultSHMSizeLimitRaw        = "8Gi"
)

func defaultSHMSizeLimit() resource.Quantity {
	raw := strings.TrimSpace(os.Getenv("DEFAULT_SHM_SIZE_LIMIT"))
	if raw == "" {
		raw = defaultSHMSizeLimitRaw
	}
	if parsed, err := resource.ParseQuantity(raw); err == nil {
		return parsed
	}
	return resource.MustParse(defaultSHMSizeLimitRaw)
}

func envStringOrDefault(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envBoolOrDefault(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseOptionalQuantity(raw string) (*resource.Quantity, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return nil, false
	}
	return &q, true
}

func modelUsesPersistentVolume(model *aiv1alpha2.Model) bool {
	if _, _, ok := parsePVCSource(model.Spec.Source); ok {
		return true
	}
	s := cacheStrategy(model)
	return s == "SharedPVC" || s == "Local"
}

// resolveFlashLoaderConfig decides if flash-loader should be injected and which runtime settings to use.
// Resolution layers (lowest to highest priority): env vars -> v1alpha1 ModelCache -> v1alpha2 CacheSpec.FlashLoader.
func (r *ModelReconciler) resolveFlashLoaderConfig(ctx context.Context, model *aiv1alpha2.Model) flashLoaderRuntimeConfig {
	// Layer 1: Environment variable defaults
	cfg := flashLoaderRuntimeConfig{
		Enabled:         envBoolOrDefault("DEFAULT_FLASH_LOADER_ENABLED", false),
		Image:           envStringOrDefault("DEFAULT_FLASH_LOADER_IMAGE", defaultFlashLoaderImage),
		Concurrency:     envIntOrDefault("DEFAULT_FLASH_LOADER_CONCURRENCY", defaultFlashLoaderConcurrency),
		BufferSizeKB:    envIntOrDefault("DEFAULT_FLASH_LOADER_BUFFER_KB", 4096),
		VerifyIntegrity: envBoolOrDefault("DEFAULT_FLASH_LOADER_VERIFY", false),
		ExcludePatterns: envStringOrDefault("DEFAULT_FLASH_LOADER_EXCLUDE", ""),
	}
	if tmpfs, ok := parseOptionalQuantity(os.Getenv("DEFAULT_FLASH_LOADER_TMPFS_SIZE_LIMIT")); ok {
		cfg.TmpfsSizeLimit = tmpfs
	}

	// Layer 2: v1alpha1 ModelCache overrides
	if mc := r.matchingModelCache(ctx, model); mc != nil && mc.Spec.FlashLoader != nil {
		flash := mc.Spec.FlashLoader
		cfg.Enabled = flash.Enabled
		if strings.TrimSpace(flash.Image) != "" {
			cfg.Image = strings.TrimSpace(flash.Image)
		}
		if flash.Concurrency > 0 {
			cfg.Concurrency = flash.Concurrency
		}
		if flash.TmpfsSizeLimit != nil {
			if tmpfs, ok := parseOptionalQuantity(*flash.TmpfsSizeLimit); ok {
				cfg.TmpfsSizeLimit = tmpfs
			}
		}
	}

	// Layer 3: v1alpha2 Model.Spec.Cache.FlashLoader (highest priority)
	if model.Spec.Cache != nil && model.Spec.Cache.FlashLoader != nil {
		fl := model.Spec.Cache.FlashLoader
		if fl.Enabled != nil {
			cfg.Enabled = *fl.Enabled
		}
		if fl.Image != "" {
			cfg.Image = fl.Image
		}
		if fl.Concurrency != nil && *fl.Concurrency > 0 {
			cfg.Concurrency = int(*fl.Concurrency)
		}
		if fl.TmpfsSizeLimit != "" {
			if tmpfs, ok := parseOptionalQuantity(fl.TmpfsSizeLimit); ok {
				cfg.TmpfsSizeLimit = tmpfs
			}
		}
		if fl.BufferSizeKB != nil {
			cfg.BufferSizeKB = int(*fl.BufferSizeKB)
		}
		if fl.VerifyIntegrity != nil {
			cfg.VerifyIntegrity = *fl.VerifyIntegrity
		}
	}

	// Auto-enable for shared GPU models on Local strategy
	if model.Spec.Cache != nil && model.Spec.Cache.FlashLoader == nil &&
		model.Spec.IsShared() && cacheStrategy(model) == "Local" {
		cfg.Enabled = true
	}

	if cfg.Concurrency < 1 {
		cfg.Concurrency = defaultFlashLoaderConcurrency
	}
	if cfg.BufferSizeKB < 32 {
		cfg.BufferSizeKB = 4096
	}
	if !modelUsesPersistentVolume(model) {
		cfg.Enabled = false
	}
	return cfg
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

	// Determine phase from deployment status and set conditions
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

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
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/modelmeta"
)

// ensureService creates or updates the Service for the model.
func (r *ModelReconciler) ensureService(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) error {
	return r.ensureServiceWithPort(ctx, model, b, b.Port())
}

func (r *ModelReconciler) ensureServiceWithPort(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend, port int32) error {
	log := log.FromContext(ctx)

	service := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, service)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	// Build annotations including LiteLLM and service labels
	annotations := make(map[string]string)
	if litellmEnabled(model) {
		servedModel := litellmServedModel(model)
		annotations[AnnotationLiteLLMServedModel] = servedModel
		if aliases := litellmAliases(model, servedModel); len(aliases) > 0 {
			annotations[AnnotationLiteLLMAliases] = strings.Join(aliases, ",")
		}
		if model.Spec.LiteLLM != nil && model.Spec.LiteLLM.CopilotAlias != "" {
			annotations[AnnotationLiteLLMCopilot] = model.Spec.LiteLLM.CopilotAlias
		}
		capsJSON, _ := json.Marshal(resolveCapabilities(model, b))
		annotations[AnnotationLiteLLMCapabilities] = string(capsJSON)
		modelmeta.ApplyTokenLimitAnnotations(annotations, modelmeta.ResolveTokenLimits(&model.Spec))
	}

	// Add service labels for routing
	if len(model.Spec.ServiceLabels) > 0 {
		annotations[AnnotationServiceLabels] = strings.Join(model.Spec.ServiceLabels, ",")
	}

	desiredService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        model.Name,
			Namespace:   model.Namespace,
			Labels:      r.labelsForModel(model),
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: r.selectorLabelsForModel(model),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromInt32(port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	if errors.IsNotFound(err) {
		log.Info("Creating Service", "name", model.Name)
		return r.Create(ctx, desiredService)
	}

	// Update service. Avoid clobbering immutable fields (e.g., clusterIP/clusterIPs).
	// Do NOT overwrite .Spec.Selector here — it is managed separately:
	//  - Set on creation (above)
	//  - Cleared by removeRuntimeServiceSelector for runtime-managed models
	//  - Restored by restoreServiceSelector when falling back to Deployment flow
	// Overwriting it here causes an infinite reconcile loop when runtime management
	// clears the selector and ensureService re-adds it on the next reconcile.
	existingPorts := service.Spec.Ports
	existingLabels := service.Labels
	existingAnnotations := service.Annotations

	service.Spec.Ports = desiredService.Spec.Ports
	service.Labels = desiredService.Labels
	service.Annotations = applyManagedAnnotations(service.Annotations, annotations, managedModelAnnotations)

	if apiequality.Semantic.DeepEqual(service.Spec.Ports, existingPorts) &&
		apiequality.Semantic.DeepEqual(service.Labels, existingLabels) &&
		apiequality.Semantic.DeepEqual(service.Annotations, existingAnnotations) {
		return nil
	}

	log.Info("Updating Service", "name", model.Name)
	return r.Update(ctx, service)
}

// restoreServiceSelector re-adds the selector to a Service that was previously
// cleared by removeRuntimeServiceSelector. Called when a model falls back from
// runtime-managed to deployment-managed flow.
func (r *ModelReconciler) restoreServiceSelector(ctx context.Context, model *aiv1alpha2.Model) error {
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, svc); err != nil {
		if errors.IsNotFound(err) {
			return nil // Service will be created by ensureService
		}
		return err
	}
	desired := r.selectorLabelsForModel(model)
	if apiequality.Semantic.DeepEqual(svc.Spec.Selector, desired) {
		return nil
	}
	svc.Spec.Selector = desired
	log := log.FromContext(ctx)
	log.Info("Restoring selector on Service for deployment management", "service", svc.Name)
	return r.Update(ctx, svc)
}

// ensureDeployment creates or updates the Deployment for the model.
func (r *ModelReconciler) ensureDeployment(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string, desiredReplicas int32) error {
	log := log.FromContext(ctx)

	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	// Build ModelSpec for backend, applying GPUProfile-declared backend
	// defaults before container args are rendered.
	spec := r.buildBackendModelSpecForArch(model, b, gpuVendor, gpuArch)
	spec.GPUArch = gpuArch

	applyKVCacheReconfigureOverrides(model, spec)

	storagePlan := resolveBackendStoragePlan(model, b, spec.Config)

	// Get container configuration from backend.
	// GPUProfile image override wins over the backend's hardcoded arch rules;
	// see backend.ResolveBackendImage for the precedence contract.
	var profileSpec *aiv1alpha2.GPUProfileSpec
	if r.GPUProfiles != nil {
		if profile, ok := r.GPUProfiles.Lookup(gpuArch); ok {
			profileSpec = profile
		}
	}
	image := backend.ResolveBackendImage(b, profileSpec, gpuVendor, gpuArch)
	if profileSpec != nil {
		if profileImage, ok := backend.ImageFromProfile(profileSpec, b.Name()); ok && image == profileImage {
			log.V(1).Info("Using GPUProfile image override", "backend", b.Name(), "arch", gpuArch, "image", profileImage)
		}
	}
	// Per-model image override takes highest precedence.
	if model.Spec.Image != "" {
		log.V(1).Info("Using per-model image override", "model", model.Name, "image", model.Spec.Image)
		image = model.Spec.Image
	}
	// Pin the resolved image to an immutable digest when requested. Applied
	// last so it reproducibly fixes whatever image won the precedence chain
	// above (per-model override, GPUProfile, or backend default).
	if model.Spec.ImageDigest != "" {
		if pinned := backend.ApplyImageDigest(image, model.Spec.ImageDigest); pinned != image {
			log.V(1).Info("Pinning model image to digest", "model", model.Name, "image", pinned)
			image = pinned
		}
	}
	port := b.Port()
	// Most backends use a static command, but some (llamacpp) must adjust it to
	// the resolved image layout: the explicit binary-path override only applies
	// to custom builds, while public upstream images launch via their own
	// entrypoint. Unlike the runtime subprocess path, this Deployment sets
	// Command verbatim with no PATH-resolution fallback, so the image-aware
	// selection is what keeps a no-spec.image Model runnable.
	command := b.Command()
	if ic, ok := b.(backend.ImageAwareCommander); ok {
		command = ic.CommandForImage(image)
	}
	args := b.Args(spec)
	env := b.Env(spec)

	// Override hardcoded ROCmEnvVars (baked into b.Env()) with GPUProfile-declared
	// env when present. backend.ResolveBackendROCmEnv enforces the GPUProfile-first
	// contract: when profile.Env is set it is returned; otherwise it falls through
	// to ROCmEnvVars(arch), which b.Env() already injected — so no merge is needed
	// in the fallback case.
	if profileEnv, ok := backend.EnvFromProfile(profileSpec); ok {
		env = mergeEnv(env, profileEnv)
	}
	probe := b.ReadinessProbe()
	startupProbe := backendStartupProbe(b, spec)

	// Append KV-cache tuning args if the backend supports it.
	if model.Spec.KVCache != nil {
		if kvc, ok := b.(backend.KVCacheConfigurer); ok {
			var swapGiB *float64
			if model.Spec.KVCache.SwapSpace != nil {
				v := model.Spec.KVCache.SwapSpace.AsApproximateFloat64()
				swapGiB = &v
			}
			if extra := kvc.KVCacheArgs(model.Spec.KVCache.MaxBlockSize, swapGiB); len(extra) > 0 {
				args = append(args, extra...)
			}
		}
	}

	// Append LoRA base args if the model has LoRA adapters and backend supports it.
	if ls, ok := b.(backend.LoRASupporter); ok && ls.SupportsLoRA() {
		// Check for LoRA adapter CRs referencing this model.
		loraList := &aiv1alpha2.LoRAAdapterList{}
		if err := r.List(ctx, loraList, client.InNamespace(model.Namespace)); err == nil {
			count := 0
			for _, la := range loraList.Items {
				if la.Spec.ModelRef == model.Name {
					count++
				}
			}
			if count > 0 {
				maxAdapters := count
				if maxAdapters < 4 {
					maxAdapters = 4 // minimum headroom
				}
				args = append(args, ls.LoRABaseArgs(maxAdapters)...)
			}
		}
	}

	// Store HuggingFace cache metadata on the model volume when available.
	if storagePlan.HFCacheBasePath != "" {
		env = mergeEnv(env, hfCacheEnvVars(storagePlan.HFCacheBasePath))
	}

	// Build resource requirements
	resources := model.Spec.Resources
	if resources.Limits == nil {
		resources.Limits = corev1.ResourceList{}
	}
	gpuCount := model.Spec.GetGPUCount()
	if gpuCount > 0 {
		gpuResourceName := gpuVendor.ResourceName()
		if gpuResourceName != "" {
			resources.Limits[gpuResourceName] = *resource.NewQuantity(int64(gpuCount), resource.DecimalSI)
		}
	}

	// Build node selector
	nodeSelector := model.Spec.NodeSelector
	if nodeSelector == nil {
		nodeSelector = make(map[string]string)
	}

	// Tolerate dedicated GPU nodes when requesting GPUs.
	var tolerations []corev1.Toleration
	if gpuCount > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	// Build container
	container := corev1.Container{
		Name:            "model",
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         command,
		Args:            args,
		Env:             env,
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Resources:      resources,
		ReadinessProbe: probe,
		StartupProbe:   startupProbe,
		// Set K8s defaults explicitly to prevent reconcile loops.
		// The API server adds these on write; without them, every read-back
		// differs from the generated spec, causing continuous updates.
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}

	// Add volume mounts if backend needs volume
	var volumes []corev1.Volume
	if b.NeedsVolume() {
		// Add model volume mount
		volumeMount := corev1.VolumeMount{
			Name:      "model",
			MountPath: "/models",
		}
		// Backends can require a subpath view of the mounted model volume.
		if storagePlan.ModelVolumeSubPath != "" {
			volumeMount.SubPath = storagePlan.ModelVolumeSubPath
		}
		container.VolumeMounts = append(container.VolumeMounts, volumeMount)

		// Determine volume source based on cache strategy
		volumeSource := r.getVolumeSource(model)
		volumes = append(volumes, corev1.Volume{
			Name:         "model",
			VolumeSource: volumeSource,
		})
	}

	// Add shared memory volume for ML workloads
	shmSizeLimit := defaultSHMSizeLimit()
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "shm",
		MountPath: "/dev/shm",
	})
	volumes = append(volumes, corev1.Volume{
		Name: "shm",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium:    corev1.StorageMediumMemory,
				SizeLimit: &shmSizeLimit,
			},
		},
	})

	// Flash-loader init container: when enabled, copy model files from PVC to tmpfs
	// before starting the backend for lower cold-start/swap latency.
	var initContainers []corev1.Container
	flashCfg := r.resolveFlashLoaderConfig(ctx, model)
	if flashCfg.Enabled {
		flashVerify := "false"
		if flashCfg.VerifyIntegrity {
			flashVerify = "true"
		}
		// Derive FLASH_VARIANT from model's useFp16 config — when fp16 is enabled,
		// flash-loader skips fp32 safetensors files that have fp16 counterparts.
		flashVariant := ""
		if model.Spec.ConfigString("useFp16", "") == "1" {
			flashVariant = "fp16"
		}

		flashContainer := corev1.Container{
			Name:            "flash-loader",
			Image:           flashCfg.Image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Env: []corev1.EnvVar{
				{Name: "FLASH_SRC", Value: "/src"},
				{Name: "FLASH_DST", Value: "/models"},
				{Name: "FLASH_CONCURRENCY", Value: strconv.Itoa(flashCfg.Concurrency)},
				{Name: "FLASH_BUFFER_KB", Value: strconv.Itoa(flashCfg.BufferSizeKB)},
				{Name: "FLASH_VERIFY", Value: flashVerify},
				{Name: "FLASH_EXCLUDE", Value: flashCfg.ExcludePatterns},
				{Name: "FLASH_VARIANT", Value: flashVariant},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "model", MountPath: "/src", ReadOnly: true},
				{Name: "flash-tmpfs", MountPath: "/models"},
			},
			// Set K8s defaults explicitly to prevent reconcile loops.
			// The API server adds these on write; without them, every read-back
			// differs from the generated spec, causing continuous updates.
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		}
		initContainers = append(initContainers, flashContainer)

		// Replace the model volume mount on the main container to use tmpfs
		for i := range container.VolumeMounts {
			if container.VolumeMounts[i].Name == "model" {
				container.VolumeMounts[i].Name = "flash-tmpfs"
			}
		}

		// Persistent flash-tmpfs for shared models: use hostPath on /dev/shm
		// so model weights survive pod restarts. Flash-loader's shouldCopy()
		// skips files with matching sizes, making subsequent swaps near-instant.
		if model.Spec.IsShared() {
			flashDir := filepath.Join("/dev/shm/flexinfer", model.Namespace, model.Name)
			volumes = append(volumes, corev1.Volume{
				Name: "flash-tmpfs",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: flashDir,
						Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
					},
				},
			})
		} else {
			// Non-shared: ephemeral emptyDir (existing behavior)
			flashTmpfs := &corev1.EmptyDirVolumeSource{
				Medium: corev1.StorageMediumMemory,
			}
			if flashCfg.TmpfsSizeLimit != nil {
				sizeLimit := flashCfg.TmpfsSizeLimit.DeepCopy()
				flashTmpfs.SizeLimit = &sizeLimit
			}
			volumes = append(volumes, corev1.Volume{
				Name: "flash-tmpfs",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: flashTmpfs,
				},
			})
		}
	}

	// Compilation cache: persistent hostPath for MIOpen/PyTorch/Triton caches.
	// Survives pod restarts to avoid recompilation on GPU swaps.
	if hostDir, ccEnabled := resolveCompilationCache(model); ccEnabled {
		if ccConfigurer, ok := b.(backend.CompilationCacheConfigurer); ok {
			volumes = append(volumes, corev1.Volume{
				Name: "compile-cache",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: hostDir,
						Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
					},
				},
			})
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "compile-cache",
				MountPath: compilationCacheMountPath,
			})
			container.Env = append(container.Env, ccConfigurer.CompilationCacheEnvVars(compilationCacheMountPath)...)
		}
	}

	desiredDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: func() map[string]string {
				annotations := make(map[string]string)
				if litellmEnabled(model) {
					servedModel := litellmServedModel(model)
					annotations[AnnotationLiteLLMServedModel] = servedModel
					if aliases := litellmAliases(model, servedModel); len(aliases) > 0 {
						annotations[AnnotationLiteLLMAliases] = strings.Join(aliases, ",")
					}
					if model.Spec.LiteLLM != nil && model.Spec.LiteLLM.CopilotAlias != "" {
						annotations[AnnotationLiteLLMCopilot] = model.Spec.LiteLLM.CopilotAlias
					}
					capsJSON, _ := json.Marshal(resolveCapabilities(model, b))
					annotations[AnnotationLiteLLMCapabilities] = string(capsJSON)
					modelmeta.ApplyTokenLimitAnnotations(annotations, modelmeta.ResolveTokenLimits(&model.Spec))
				}
				if len(model.Spec.ServiceLabels) > 0 {
					annotations[AnnotationServiceLabels] = strings.Join(model.Spec.ServiceLabels, ",")
				}
				if len(annotations) == 0 {
					return nil
				}
				return annotations
			}(),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(desiredReplicas),
			Strategy: func() appsv1.DeploymentStrategy {
				// GPU workloads frequently run on tightly-constrained nodes where a second
				// pod cannot be scheduled (e.g., 1x GPU nodes, or multi-GPU nodes where
				// we allocate all GPUs). Avoid rolling update deadlocks by disabling surge.
				if gpuCount > 0 {
					maxSurge := intstr.FromInt(0)
					maxUnavailable := intstr.FromInt(1)
					return appsv1.DeploymentStrategy{
						Type: appsv1.RollingUpdateDeploymentStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxSurge:       &maxSurge,
							MaxUnavailable: &maxUnavailable,
						},
					}
				}
				return appsv1.DeploymentStrategy{}
			}(),
			Selector: &metav1.LabelSelector{
				MatchLabels: r.selectorLabelsForModel(model),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      r.labelsForModel(model),
					Annotations: r.podAnnotationsForModel(model),
				},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Tolerations:  tolerations,
					// ROCm devices (/dev/kfd, /dev/dri/renderD*) are typically 0660 root:render.
					// Add the render group GID (992 on most systems) to supplementalGroups so
					// non-root users can access GPU devices without running as root.
					SecurityContext: func() *corev1.PodSecurityContext {
						if gpuVendor != backend.GPUVendorAMD || gpuCount == 0 {
							return nil
						}
						return &corev1.PodSecurityContext{
							// Render group GID varies by distro: 992 (Arch), 109 (Debian/Ubuntu).
							// Include both so GPU device access works on either.
							SupplementalGroups: []int64{109, 992},
						}
					}(),
					Affinity: func() *corev1.Affinity {
						// For multi-replica models, enforce one pod per node (best-effort load balancing
						// across identical GPU nodes, and avoids accidentally packing both replicas onto
						// a single multi-GPU node).
						if desiredReplicas <= 1 {
							return nil
						}
						return &corev1.Affinity{
							PodAntiAffinity: &corev1.PodAntiAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: r.selectorLabelsForModel(model),
										},
										TopologyKey: "kubernetes.io/hostname",
									},
								},
							},
						}
					}(),
					TopologySpreadConstraints: func() []corev1.TopologySpreadConstraint {
						// If we run multiple replicas (e.g., active-active on two GPU nodes),
						// spread them across nodes when possible, but allow co-location as fallback.
						if desiredReplicas <= 1 {
							return nil
						}
						return []corev1.TopologySpreadConstraint{
							{
								MaxSkew:           1,
								TopologyKey:       "kubernetes.io/hostname",
								WhenUnsatisfiable: corev1.ScheduleAnyway,
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: r.selectorLabelsForModel(model),
								},
							},
						}
					}(),
					InitContainers:   initContainers,
					Containers:       []corev1.Container{container},
					Volumes:          volumes,
					ImagePullSecrets: r.ModelImagePullSecrets,
					RuntimeClassName: func() *string {
						// NVIDIA GPUs require the "nvidia" runtime to inject /dev/nvidia* and driver libs.
						// Without this, pods may schedule with nvidia.com/gpu but have no CUDA devices.
						if gpuVendor == backend.GPUVendorNVIDIA && gpuCount > 0 {
							runtime := "nvidia"
							return &runtime
						}
						return nil
					}(),
					// Model pods do not need to talk to the Kubernetes API. Avoid mounting a service
					// account token by default to reduce blast radius if a backend container is compromised.
					AutomountServiceAccountToken: ptr.To(false),
					RestartPolicy:                corev1.RestartPolicyAlways,
					ServiceAccountName:           "default",
				},
			},
		},
	}

	if errors.IsNotFound(err) {
		log.Info("Creating Deployment", "name", model.Name, "replicas", desiredReplicas)
		return r.Create(ctx, desiredDeployment)
	}

	// Detect selector drift: if the existing deployment's immutable selector differs
	// from the desired selector (e.g., "app.kubernetes.io/name" changed from model
	// name to "model"), the service endpoint will never match the pods.  Delete and
	// recreate the deployment to pick up the corrected selector.
	desiredSelector := r.selectorLabelsForModel(model)
	if deployment.Spec.Selector != nil {
		for k, want := range desiredSelector {
			if got, ok := deployment.Spec.Selector.MatchLabels[k]; ok && got != want {
				log.Info("Selector drift detected — deleting deployment for recreation",
					"name", model.Name, "key", k, "existing", got, "desired", want)
				if delErr := r.Delete(ctx, deployment); delErr != nil {
					return fmt.Errorf("delete deployment for selector recreation: %w", delErr)
				}
				log.Info("Creating Deployment (selector corrected)", "name", model.Name, "replicas", desiredReplicas)
				return r.Create(ctx, desiredDeployment)
			}
		}
	}

	// Build the desired deployment state and compare only controller-managed fields.
	// Using a targeted comparison (rather than DeepEqual on the entire spec) avoids
	// infinite update loops caused by K8s-defaulted fields that the controller doesn't
	// set (terminationGracePeriodSeconds, dnsPolicy, schedulerName, revisionHistoryLimit,
	// progressDeadlineSeconds, container terminationMessagePath/Policy, imagePullPolicy).
	desired := desiredDeployment.Spec

	// Deployment selectors are immutable. Preserve the existing selector on updates to avoid
	// deadlocking reconciliation when labels change (e.g., shared GPU group assignment).
	// Merge existing selector labels into desired template labels so pods always match.
	desiredTemplateLabels := desired.Template.Labels
	if deployment.Spec.Selector != nil && deployment.Spec.Selector.MatchLabels != nil {
		desiredTemplateLabels = mergeStringMap(desiredTemplateLabels, deployment.Spec.Selector.MatchLabels)
	}

	desiredAnnotations := applyManagedAnnotations(deployment.Annotations, desiredDeployment.Annotations, managedModelAnnotations)
	desiredPodAnnotations := applyManagedAnnotations(
		deployment.Spec.Template.Annotations,
		desiredDeployment.Spec.Template.Annotations,
		managedModelPodAnnotations,
	)

	// Compare only controller-managed fields to decide if an update is needed.
	changed := deploymentManagedFieldChanges(deployment, &desired, desiredDeployment.Labels, desiredAnnotations, desiredTemplateLabels, desiredPodAnnotations)
	if len(changed) == 0 {
		return nil
	}

	// Apply all controller-managed fields onto the existing deployment.
	deployment.Spec.Replicas = desired.Replicas
	deployment.Spec.Strategy = desired.Strategy
	deployment.Spec.Template.Labels = desiredTemplateLabels
	deployment.Spec.Template.Annotations = desiredPodAnnotations
	deployment.Spec.Template.Spec.NodeSelector = desired.Template.Spec.NodeSelector
	deployment.Spec.Template.Spec.Tolerations = desired.Template.Spec.Tolerations
	deployment.Spec.Template.Spec.SecurityContext = desired.Template.Spec.SecurityContext
	deployment.Spec.Template.Spec.Affinity = desired.Template.Spec.Affinity
	deployment.Spec.Template.Spec.TopologySpreadConstraints = desired.Template.Spec.TopologySpreadConstraints
	deployment.Spec.Template.Spec.InitContainers = desired.Template.Spec.InitContainers
	deployment.Spec.Template.Spec.Volumes = desired.Template.Spec.Volumes
	deployment.Spec.Template.Spec.ImagePullSecrets = desired.Template.Spec.ImagePullSecrets
	deployment.Spec.Template.Spec.RuntimeClassName = desired.Template.Spec.RuntimeClassName
	deployment.Spec.Template.Spec.AutomountServiceAccountToken = desired.Template.Spec.AutomountServiceAccountToken
	deployment.Spec.Template.Spec.RestartPolicy = desired.Template.Spec.RestartPolicy
	deployment.Spec.Template.Spec.ServiceAccountName = desired.Template.Spec.ServiceAccountName
	// Merge containers: update only controller-managed fields, preserve K8s defaults.
	// If container count changed, full replacement is needed.
	if len(deployment.Spec.Template.Spec.Containers) != len(desired.Template.Spec.Containers) {
		deployment.Spec.Template.Spec.Containers = desired.Template.Spec.Containers
	} else {
		mergeContainers(deployment.Spec.Template.Spec.Containers, desired.Template.Spec.Containers)
	}
	deployment.Labels = desiredDeployment.Labels
	deployment.Annotations = desiredAnnotations

	log.Info("Updating Deployment", "name", model.Name, "changedFields", changed)

	return r.Update(ctx, deployment)
}

func backendStartupProbe(b backend.Backend, spec *backend.ModelSpec) *corev1.Probe {
	if configured, ok := b.(backend.StartupProbeConfigurer); ok {
		return configured.StartupProbeForSpec(spec)
	}
	return b.StartupProbe()
}

// deploymentChangedFields returns a human-readable summary of what changed
// between two deployment specs. Compares the most operationally relevant fields.
func deploymentChangedFields(old, new *appsv1.DeploymentSpec) []string {
	var fields []string

	if !ptr.Equal(old.Replicas, new.Replicas) {
		fields = append(fields, fmt.Sprintf("replicas(%d→%d)",
			ptr.Deref(old.Replicas, 1), ptr.Deref(new.Replicas, 1)))
	}

	oldC, newC := firstContainer(old), firstContainer(new)
	if oldC != nil && newC != nil {
		if oldC.Image != newC.Image {
			fields = append(fields, fmt.Sprintf("image(%s→%s)", oldC.Image, newC.Image))
		}
		if !apiequality.Semantic.DeepEqual(oldC.Args, newC.Args) {
			fields = append(fields, "args")
		}
		if !apiequality.Semantic.DeepEqual(oldC.Env, newC.Env) {
			fields = append(fields, "env")
		}
		if !apiequality.Semantic.DeepEqual(oldC.Resources, newC.Resources) {
			fields = append(fields, "resources")
		}
		if !apiequality.Semantic.DeepEqual(oldC.VolumeMounts, newC.VolumeMounts) {
			fields = append(fields, "volumeMounts")
		}
	}

	if !apiequality.Semantic.DeepEqual(old.Template.Spec.NodeSelector, new.Template.Spec.NodeSelector) {
		fields = append(fields, "nodeSelector")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Spec.Volumes, new.Template.Spec.Volumes) {
		fields = append(fields, "volumes")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Spec.InitContainers, new.Template.Spec.InitContainers) {
		fields = append(fields, "initContainers")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Spec.ImagePullSecrets, new.Template.Spec.ImagePullSecrets) {
		fields = append(fields, "imagePullSecrets")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Annotations, new.Template.Annotations) {
		fields = append(fields, "podAnnotations")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Labels, new.Template.Labels) {
		fields = append(fields, "podLabels")
	}
	if !ptr.Equal(old.RevisionHistoryLimit, new.RevisionHistoryLimit) {
		fields = append(fields, "revisionHistoryLimit")
	}
	if !ptr.Equal(old.ProgressDeadlineSeconds, new.ProgressDeadlineSeconds) {
		fields = append(fields, "progressDeadlineSeconds")
	}
	if !apiequality.Semantic.DeepEqual(old.Strategy, new.Strategy) {
		fields = append(fields, "strategy")
	}

	if len(fields) == 0 {
		fields = append(fields, "other")
	}
	return fields
}

func firstContainer(spec *appsv1.DeploymentSpec) *corev1.Container {
	if len(spec.Template.Spec.Containers) > 0 {
		return &spec.Template.Spec.Containers[0]
	}
	return nil
}

func applyKVCacheReconfigureOverrides(model *aiv1alpha2.Model, spec *backend.ModelSpec) {
	if model.Status.KVCache == nil || !model.Status.KVCache.Reconfigured {
		return
	}
	if model.Status.KVCache.ReconfiguredMaxNumSeqs == nil && model.Status.KVCache.ReconfiguredMaxModelLen == nil {
		return
	}
	if spec.Config == nil {
		spec.Config = map[string]any{}
	}
	if model.Status.KVCache.ReconfiguredMaxNumSeqs != nil {
		spec.Config["maxNumSeqs"] = float64(*model.Status.KVCache.ReconfiguredMaxNumSeqs)
	}
	if model.Status.KVCache.ReconfiguredMaxModelLen != nil {
		spec.Config["maxModelLen"] = float64(*model.Status.KVCache.ReconfiguredMaxModelLen)
	}
}

// deploymentManagedFieldChanges compares only the fields the controller manages between
// the existing deployment and the desired state. Returns a list of changed field names,
// or nil if nothing needs updating. This avoids false positives from K8s-defaulted fields
// that the controller doesn't set (terminationGracePeriodSeconds, dnsPolicy, schedulerName,
// revisionHistoryLimit, progressDeadlineSeconds, container terminationMessagePath, etc.).
func deploymentManagedFieldChanges(
	existing *appsv1.Deployment,
	desired *appsv1.DeploymentSpec,
	desiredLabels map[string]string,
	desiredAnnotations map[string]string,
	desiredTemplateLabels map[string]string,
	desiredPodAnnotations map[string]string,
) []string {
	var fields []string
	e := &existing.Spec

	// Deployment-level.
	if !ptr.Equal(e.Replicas, desired.Replicas) {
		fields = append(fields, fmt.Sprintf("replicas(%d→%d)",
			ptr.Deref(e.Replicas, 1), ptr.Deref(desired.Replicas, 1)))
	}
	if !apiequality.Semantic.DeepEqual(e.Strategy, desired.Strategy) {
		fields = append(fields, "strategy")
	}

	// Pod template metadata.
	if !apiequality.Semantic.DeepEqual(existing.Spec.Template.Labels, desiredTemplateLabels) {
		fields = append(fields, "podLabels")
	}
	if !apiequality.Semantic.DeepEqual(existing.Spec.Template.Annotations, desiredPodAnnotations) {
		fields = append(fields, "podAnnotations")
	}

	// Pod spec (only controller-managed fields).
	if !apiequality.Semantic.DeepEqual(e.Template.Spec.NodeSelector, desired.Template.Spec.NodeSelector) {
		fields = append(fields, "nodeSelector")
	}
	if !apiequality.Semantic.DeepEqual(e.Template.Spec.Tolerations, desired.Template.Spec.Tolerations) {
		fields = append(fields, "tolerations")
	}
	if !podSecurityContextEqual(e.Template.Spec.SecurityContext, desired.Template.Spec.SecurityContext) {
		fields = append(fields, "securityContext")
	}
	if !podObjectEqual(e.Template.Spec.Affinity, desired.Template.Spec.Affinity) {
		fields = append(fields, "affinity")
	}
	if !apiequality.Semantic.DeepEqual(e.Template.Spec.TopologySpreadConstraints, desired.Template.Spec.TopologySpreadConstraints) {
		fields = append(fields, "topologySpreadConstraints")
	}
	if !apiequality.Semantic.DeepEqual(e.Template.Spec.Volumes, desired.Template.Spec.Volumes) {
		fields = append(fields, "volumes")
	}
	if !apiequality.Semantic.DeepEqual(e.Template.Spec.InitContainers, desired.Template.Spec.InitContainers) {
		fields = append(fields, "initContainers")
	}
	if !apiequality.Semantic.DeepEqual(e.Template.Spec.ImagePullSecrets, desired.Template.Spec.ImagePullSecrets) {
		fields = append(fields, "imagePullSecrets")
	}
	if !apiequality.Semantic.DeepEqual(e.Template.Spec.RuntimeClassName, desired.Template.Spec.RuntimeClassName) {
		fields = append(fields, "runtimeClassName")
	}
	if !ptr.Equal(e.Template.Spec.AutomountServiceAccountToken, desired.Template.Spec.AutomountServiceAccountToken) {
		fields = append(fields, "automountServiceAccountToken")
	}
	if e.Template.Spec.RestartPolicy != desired.Template.Spec.RestartPolicy {
		fields = append(fields, "restartPolicy")
	}
	if e.Template.Spec.ServiceAccountName != desired.Template.Spec.ServiceAccountName {
		fields = append(fields, "serviceAccountName")
	}

	// Container comparison — only controller-managed fields (image, args, env, resources,
	// volumeMounts, ports, imagePullPolicy). Ignores K8s-defaulted fields like
	// terminationMessagePath and terminationMessagePolicy.
	if containerManagedFieldsChanged(e.Template.Spec.Containers, desired.Template.Spec.Containers) {
		fields = append(fields, "containers")
	}

	// Deployment metadata.
	if !apiequality.Semantic.DeepEqual(existing.Labels, desiredLabels) {
		fields = append(fields, "labels")
	}
	if !apiequality.Semantic.DeepEqual(existing.Annotations, desiredAnnotations) {
		fields = append(fields, "annotations")
	}

	return fields
}

// containerManagedFieldsChanged compares only the fields the controller sets on containers.
func containerManagedFieldsChanged(existing, desired []corev1.Container) bool {
	if len(existing) != len(desired) {
		return true
	}
	for i := range existing {
		e, d := &existing[i], &desired[i]
		if e.Name != d.Name || e.Image != d.Image {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.Args, d.Args) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.Command, d.Command) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.Env, d.Env) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.EnvFrom, d.EnvFrom) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.Resources, d.Resources) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.VolumeMounts, d.VolumeMounts) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.Ports, d.Ports) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.ReadinessProbe, d.ReadinessProbe) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.LivenessProbe, d.LivenessProbe) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.StartupProbe, d.StartupProbe) {
			return true
		}
		if !apiequality.Semantic.DeepEqual(e.SecurityContext, d.SecurityContext) {
			return true
		}
		if e.WorkingDir != d.WorkingDir {
			return true
		}
		if e.ImagePullPolicy != d.ImagePullPolicy {
			return true
		}
	}
	return false
}

// mergeContainers updates controller-managed fields on existing containers from desired,
// preserving K8s-defaulted fields (terminationMessagePath, terminationMessagePolicy).
func mergeContainers(existing []corev1.Container, desired []corev1.Container) {
	// If container count changed, we can't merge — the full replacement is needed.
	// This case is handled by clearing and re-appending.
	if len(existing) != len(desired) {
		// Full replacement needed, caller handles this case via direct assignment.
		return
	}
	for i := range existing {
		e, d := &existing[i], &desired[i]
		e.Name = d.Name
		e.Image = d.Image
		e.Command = d.Command
		e.Args = d.Args
		e.Env = d.Env
		e.EnvFrom = d.EnvFrom
		e.Resources = d.Resources
		e.VolumeMounts = d.VolumeMounts
		e.Ports = d.Ports
		e.ReadinessProbe = d.ReadinessProbe
		e.LivenessProbe = d.LivenessProbe
		e.StartupProbe = d.StartupProbe
		e.SecurityContext = d.SecurityContext
		e.WorkingDir = d.WorkingDir
		e.ImagePullPolicy = d.ImagePullPolicy
	}
}

// podSecurityContextEqual compares two PodSecurityContext pointers, treating nil
// and the zero value (&PodSecurityContext{}) as equal. K8s stores removed security
// contexts as {} rather than null, causing nil vs {} oscillation.
func podSecurityContextEqual(a, b *corev1.PodSecurityContext) bool {
	normalize := func(p *corev1.PodSecurityContext) *corev1.PodSecurityContext {
		if p == nil {
			return &corev1.PodSecurityContext{}
		}
		return p
	}
	return apiequality.Semantic.DeepEqual(normalize(a), normalize(b))
}

// podObjectEqual compares two objects where nil and zero-value pointer should
// be treated as equal (same K8s nil-vs-empty problem as PodSecurityContext).
func podObjectEqual[T any](a, b *T) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil {
		return apiequality.Semantic.DeepEqual(new(T), b)
	}
	if b == nil {
		return apiequality.Semantic.DeepEqual(a, new(T))
	}
	return apiequality.Semantic.DeepEqual(a, b)
}

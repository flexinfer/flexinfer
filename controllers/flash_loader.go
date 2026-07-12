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
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/envutil"
)

type flashLoaderRuntimeConfig struct {
	Enabled         bool
	Image           string
	Concurrency     int
	TmpfsSizeLimit  *resource.Quantity
	BufferSizeKB    int
	VerifyIntegrity bool
	ExcludePatterns string
}

const defaultFlashLoaderConcurrency = 4

var defaultFlashLoaderImage = envutil.StringOrDefault(
	"FLEXINFER_FLASH_LOADER_IMAGE",
	"registry.harbor.lan/flexinfer/flash-loader:latest",
)

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
	flashRoot := "/dev/shm/flexinfer"
	flashDir := filepath.Join(flashRoot, model.Namespace, model.Name)
	directoryOrCreate := corev1.HostPathDirectoryOrCreate

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
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "flash-tmpfs-root",
							MountPath: flashRoot,
						}},
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
					Volumes: []corev1.Volume{{
						Name: "flash-tmpfs-root",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: flashRoot,
								Type: &directoryOrCreate,
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

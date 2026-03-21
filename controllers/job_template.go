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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// CacheJobParams captures all parameters needed to build a cache-related Job.
// Each job-building function populates the unique fields and delegates
// the shared boilerplate to buildCacheJob().
type CacheJobParams struct {
	// Identity
	Name        string
	Namespace   string
	Labels      map[string]string
	Annotations map[string]string

	// Scheduling
	NodeSelector map[string]string
	Tolerations  []corev1.Toleration

	// Job behavior
	BackoffLimit            int32
	RestartPolicy           corev1.RestartPolicy
	TTLSecondsAfterFinished *int32

	// Container
	ContainerName string
	Image         string
	Command       []string
	Args          []string
	Env           []corev1.EnvVar
	Resources     corev1.ResourceRequirements

	// Volumes
	Volumes      []corev1.Volume
	VolumeMounts []corev1.VolumeMount
}

// buildCacheJob constructs a batch/v1 Job from the given parameters.
// It captures the shared boilerplate that all cache job functions repeat:
// ObjectMeta, PodSpec scheduling, container setup, and volume wiring.
func buildCacheJob(params CacheJobParams) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        params.Name,
			Namespace:   params.Namespace,
			Labels:      params.Labels,
			Annotations: params.Annotations,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(params.BackoffLimit),
			TTLSecondsAfterFinished: params.TTLSecondsAfterFinished,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                params.RestartPolicy,
					NodeSelector:                 params.NodeSelector,
					Tolerations:                  params.Tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:         params.ContainerName,
						Image:        params.Image,
						Command:      params.Command,
						Args:         params.Args,
						Env:          params.Env,
						Resources:    params.Resources,
						VolumeMounts: params.VolumeMounts,
					}},
					Volumes: params.Volumes,
				},
			},
		},
	}
	return job
}

// modelNodeSelectorAndTolerations extracts the common node selector and GPU
// toleration pattern used by all cache job builders.
func modelNodeSelectorAndTolerations(model *aiv1alpha2.Model) (map[string]string, []corev1.Toleration) {
	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}
	return nodeSelector, tolerations
}

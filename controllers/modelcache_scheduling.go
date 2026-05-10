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
	corev1 "k8s.io/api/core/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func modelCachePipelineTolerations(modelCache *aiv1alpha1.ModelCache, includeGPUDefault bool) []corev1.Toleration {
	if modelCache == nil {
		return nil
	}
	tolerations := make([]corev1.Toleration, 0, len(modelCache.Spec.Tolerations)+1)
	if includeGPUDefault {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}
	tolerations = append(tolerations, modelCache.Spec.Tolerations...)
	return tolerations
}

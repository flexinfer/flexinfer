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
	"encoding/json"
	"fmt"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/modelmeta"
)

var managedModelAnnotations = []string{
	AnnotationLiteLLMServedModel,
	AnnotationLiteLLMAliases,
	AnnotationLiteLLMCopilot,
	AnnotationLiteLLMCapabilities,
	AnnotationLiteLLMContextWindow,
	AnnotationLiteLLMMaxInputTokens,
	AnnotationLiteLLMMaxOutputTokens,
	AnnotationServiceLabels,
}

var managedModelPodAnnotations = []string{
	LabelModel,
	LabelBackend,
	AnnotationVRAMEstimate,
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

// managedAnnotationsForModel builds the desired controller-managed annotation
// set — the litellm advertisement (served model, aliases, copilot alias,
// capabilities, token limits) plus the routing service labels. The same set is
// stamped onto both the child Service and the child Deployment: the proxy
// routes from the Service annotations while litellm's manager gathers aliases
// from the Deployment annotations, so the two must never diverge.
func (r *ModelReconciler) managedAnnotationsForModel(model *aiv1alpha2.Model, b backend.Backend) map[string]string {
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
	return annotations
}

func (r *ModelReconciler) podAnnotationsForModel(model *aiv1alpha2.Model) map[string]string {
	ann := map[string]string{
		LabelModel:   model.Name,
		LabelBackend: model.Spec.Backend,
	}
	if model.Spec.GPU != nil && model.Spec.GPU.VRAMEstimateMB != nil && *model.Spec.GPU.VRAMEstimateMB > 0 {
		ann[AnnotationVRAMEstimate] = fmt.Sprintf("%d", *model.Spec.GPU.VRAMEstimateMB)
	}
	return ann
}

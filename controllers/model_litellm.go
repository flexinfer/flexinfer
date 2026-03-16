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
	"sort"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func litellmEnabled(model *aiv1alpha2.Model) bool {
	if model.Spec.LiteLLM == nil {
		return false
	}
	if model.Spec.LiteLLM.Enabled == nil {
		return true
	}
	return *model.Spec.LiteLLM.Enabled
}

func litellmServedModel(model *aiv1alpha2.Model) string {
	if model.Spec.LiteLLM != nil && model.Spec.LiteLLM.ServedModelName != "" {
		return model.Spec.LiteLLM.ServedModelName
	}
	return model.Name
}

func litellmAliases(model *aiv1alpha2.Model, servedModel string) []string {
	unique := make(map[string]struct{})
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || v == servedModel {
			return
		}
		unique[v] = struct{}{}
	}

	for _, label := range model.Spec.ServiceLabels {
		add(label)
	}
	if model.Spec.LiteLLM != nil {
		for _, alias := range model.Spec.LiteLLM.Aliases {
			add(alias)
		}
	}

	out := make([]string, 0, len(unique))
	for v := range unique {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// ResolvedCapabilities is the fully-resolved set of model capabilities,
// with auto-inference applied and explicit overrides merged.
type ResolvedCapabilities struct {
	ToolCalling     bool `json:"toolCalling"`
	Vision          bool `json:"vision"`
	ImageGeneration bool `json:"imageGeneration"`
}

// resolveCapabilities auto-infers capabilities from backend type and config,
// then applies explicit overrides from spec.capabilities.
func resolveCapabilities(model *aiv1alpha2.Model, b backend.Backend) ResolvedCapabilities {
	caps := ResolvedCapabilities{
		ImageGeneration: b.IsImageGeneration(),
	}

	switch model.Spec.Backend {
	case "vllm", "ollama":
		caps.ToolCalling = true
	case "llamacpp":
		caps.ToolCalling = model.Spec.ConfigBool("jinja", false)
		caps.Vision = model.Spec.ConfigString("mmproj", "") != ""
	}

	if oc := model.Spec.Capabilities; oc != nil {
		if oc.ToolCalling != nil {
			caps.ToolCalling = *oc.ToolCalling
		}
		if oc.Vision != nil {
			caps.Vision = *oc.Vision
		}
		if oc.ImageGeneration != nil {
			caps.ImageGeneration = *oc.ImageGeneration
		}
	}
	return caps
}

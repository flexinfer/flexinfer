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

package v1alpha2

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWorkflowProfileServedNames(t *testing.T) {
	profile := &WorkflowProfile{
		ObjectMeta: metav1ObjectMeta("fast-chat"),
		Spec: WorkflowProfileSpec{
			Aliases: []string{"chat-fast", "fast-chat", "chat-fast"},
		},
	}

	got := profile.ServedNames()
	want := []string{"fast-chat", "chat-fast"}
	if len(got) != len(want) {
		t.Fatalf("ServedNames() len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ServedNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if !profile.MatchesName("chat-fast") {
		t.Error("MatchesName(chat-fast) = false, want true")
	}
	if profile.MatchesName("slow-chat") {
		t.Error("MatchesName(slow-chat) = true, want false")
	}
}

func TestWorkflowProfileSelectorMatchesModel(t *testing.T) {
	toolCalling := true
	vision := false
	model := &Model{
		ObjectMeta: metav1ObjectMeta("qwen-fast"),
		Spec: ModelSpec{
			Backend:       "vllm",
			ServiceLabels: []string{"textgen", "fast", "code"},
			Capabilities: &ModelCapabilities{
				ToolCalling: &toolCalling,
				Vision:      &vision,
			},
		},
	}
	model.Labels = map[string]string{"lane": "interactive"}

	selector := &WorkflowProfileSelector{
		ModelRefs:          []corev1.LocalObjectReference{{Name: "qwen-fast"}},
		MatchLabels:        map[string]string{"lane": "interactive"},
		MatchServiceLabels: []string{"textgen", "fast"},
		Backends:           []string{"vllm", "ollama"},
		Capabilities: &ModelCapabilities{
			ToolCalling: &toolCalling,
		},
	}

	if !selector.MatchesModel(model) {
		t.Error("MatchesModel() = false, want true")
	}

	selector.MatchServiceLabels = []string{"textgen", "long"}
	if selector.MatchesModel(model) {
		t.Error("MatchesModel() = true for missing service label, want false")
	}
}

func TestWorkflowGatesAllowPhase(t *testing.T) {
	gates := &WorkflowGates{
		RequireReadyOrColdStart: true,
		AllowedPhases:           []ModelPhase{ModelPhaseIdle, ModelPhaseLoading},
	}

	if !gates.AllowsPhase(ModelPhaseReady) {
		t.Error("AllowsPhase(Ready) = false, want true")
	}
	if !gates.AllowsPhase(ModelPhaseIdle) {
		t.Error("AllowsPhase(Idle) = false, want true")
	}
	if gates.AllowsPhase(ModelPhaseFailed) {
		t.Error("AllowsPhase(Failed) = true, want false")
	}
}

func TestWorkflowProfileSpecSelectRoute(t *testing.T) {
	toolCalling := true
	shortMax := int32(12000)
	longMax := int32(32768)
	spec := &WorkflowProfileSpec{
		Routes: []WorkflowRoute{
			{
				Name:            "ready-fast",
				Objective:       WorkflowRouteObjectivePreferReady,
				MaxPromptTokens: &shortMax,
				RequiredCapabilities: &ModelCapabilities{
					ToolCalling: &toolCalling,
				},
			},
			{
				Name:            "fallback-long",
				Objective:       WorkflowRouteObjectiveLongestContext,
				MaxPromptTokens: &longMax,
			},
		},
	}

	route := spec.SelectRoute(8000, &ModelCapabilities{ToolCalling: &toolCalling})
	if route == nil || route.Name != "ready-fast" {
		t.Fatalf("SelectRoute(8000, toolCalling) = %#v, want ready-fast", route)
	}

	route = spec.SelectRoute(20000, nil)
	if route == nil || route.Name != "fallback-long" {
		t.Fatalf("SelectRoute(20000, nil) = %#v, want fallback-long", route)
	}

	if route := spec.SelectRoute(40000, nil); route != nil {
		t.Fatalf("SelectRoute(40000, nil) = %#v, want nil", route)
	}
}

func TestCapabilitiesMatch(t *testing.T) {
	enabled := true
	disabled := false

	required := &ModelCapabilities{ToolCalling: &enabled}
	offered := &ModelCapabilities{ToolCalling: &enabled}
	if !CapabilitiesMatch(required, offered) {
		t.Error("CapabilitiesMatch(true, true) = false, want true")
	}

	offered.ToolCalling = &disabled
	if CapabilitiesMatch(required, offered) {
		t.Error("CapabilitiesMatch(true, false) = true, want false")
	}

	if CapabilitiesMatch(required, nil) {
		t.Error("CapabilitiesMatch(true, nil) = true, want false")
	}

	if !CapabilitiesMatch(nil, nil) {
		t.Error("CapabilitiesMatch(nil, nil) = false, want true")
	}
}

func metav1ObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}

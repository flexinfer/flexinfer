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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestModelSpecDefaults(t *testing.T) {
	spec := &ModelSpec{}

	// Test default priority
	if spec.GetPriority() != 100 {
		t.Errorf("GetPriority() = %d, want 100", spec.GetPriority())
	}

	// Test default GPU count
	if spec.GetGPUCount() != 0 {
		t.Errorf("GetGPUCount() = %d, want 0", spec.GetGPUCount())
	}

	// Test not shared by default
	if spec.IsShared() {
		t.Error("IsShared() = true, want false for empty spec")
	}

	// Test serverless enabled by default
	if !spec.IsServerless() {
		t.Error("IsServerless() = false, want true by default")
	}

	// Test default min replicas (serverless)
	if spec.GetMinReplicas() != 0 {
		t.Errorf("GetMinReplicas() = %d, want 0", spec.GetMinReplicas())
	}
}

func TestModelSpecWithGPUConfig(t *testing.T) {
	priority := int32(200)
	count := int32(2)

	spec := &ModelSpec{
		GPU: &GPUSpec{
			Vendor:   GPUVendorAMD,
			Shared:   "homelab-gpu",
			Priority: &priority,
			Count:    &count,
		},
	}

	if spec.GetPriority() != 200 {
		t.Errorf("GetPriority() = %d, want 200", spec.GetPriority())
	}

	if spec.GetGPUCount() != 2 {
		t.Errorf("GetGPUCount() = %d, want 2", spec.GetGPUCount())
	}

	if !spec.IsShared() {
		t.Error("IsShared() = false, want true for shared GPU")
	}

	if spec.GPU.Shared != "homelab-gpu" {
		t.Errorf("GPU.Shared = %q, want 'homelab-gpu'", spec.GPU.Shared)
	}

	if spec.GetGPUVendor() != GPUVendorAMD {
		t.Errorf("GetGPUVendor() = %q, want %q", spec.GetGPUVendor(), GPUVendorAMD)
	}
}

func TestModelSpecCPUVendor(t *testing.T) {
	spec := &ModelSpec{
		GPU: &GPUSpec{
			Vendor: GPUVendorCPU,
		},
	}

	if spec.GetGPUVendor() != GPUVendorCPU {
		t.Errorf("GetGPUVendor() = %q, want %q", spec.GetGPUVendor(), GPUVendorCPU)
	}
	if spec.GetGPUCount() != 0 {
		t.Errorf("GetGPUCount() = %d, want 0 for CPU vendor", spec.GetGPUCount())
	}
}

func TestModelSpecServerlessConfig(t *testing.T) {
	// Test explicit enabled
	enabled := true
	minReplicas := int32(1)
	spec := &ModelSpec{
		Serverless: &ServerlessSpec{
			Enabled:     &enabled,
			MinReplicas: &minReplicas,
		},
	}
	if !spec.IsServerless() {
		t.Error("IsServerless() = false when explicitly enabled")
	}
	if spec.GetMinReplicas() != 1 {
		t.Errorf("GetMinReplicas() = %d, want 1", spec.GetMinReplicas())
	}

	// Test explicit disabled
	disabled := false
	spec.Serverless.Enabled = &disabled
	if spec.IsServerless() {
		t.Error("IsServerless() = true when explicitly disabled")
	}
	if spec.GetMinReplicas() != 1 {
		t.Errorf("GetMinReplicas() = %d, want 1 when serverless disabled", spec.GetMinReplicas())
	}
}

func TestModelSpecConfigHelpers(t *testing.T) {
	// Create a JSON config
	configJSON := []byte(`{
		"mode": "server",
		"maxNumSequence": 4,
		"trustRemoteCode": true,
		"floatValue": 0.9
	}`)

	spec := &ModelSpec{
		Config: &apiextensionsv1.JSON{Raw: configJSON},
	}

	// Test ConfigString
	if v := spec.ConfigString("mode", ""); v != "server" {
		t.Errorf("ConfigString('mode') = %q, want 'server'", v)
	}
	if v := spec.ConfigString("missing", "default"); v != "default" {
		t.Errorf("ConfigString('missing') = %q, want 'default'", v)
	}

	// Test ConfigInt
	if v := spec.ConfigInt("maxNumSequence", 0); v != 4 {
		t.Errorf("ConfigInt('maxNumSequence') = %d, want 4", v)
	}
	if v := spec.ConfigInt("missing", 10); v != 10 {
		t.Errorf("ConfigInt('missing') = %d, want 10", v)
	}

	// Test ConfigBool
	if v := spec.ConfigBool("trustRemoteCode", false); !v {
		t.Errorf("ConfigBool('trustRemoteCode') = %v, want true", v)
	}
	if v := spec.ConfigBool("missing", true); !v {
		t.Errorf("ConfigBool('missing') = %v, want true", v)
	}
}

func TestModelSpecNilConfig(t *testing.T) {
	spec := &ModelSpec{}

	// Should return defaults for nil config
	if v := spec.ConfigString("key", "default"); v != "default" {
		t.Errorf("ConfigString with nil config = %q, want 'default'", v)
	}
	if v := spec.ConfigInt("key", 42); v != 42 {
		t.Errorf("ConfigInt with nil config = %d, want 42", v)
	}
	if v := spec.ConfigBool("key", true); !v {
		t.Errorf("ConfigBool with nil config = %v, want true", v)
	}
}

func TestModelPhases(t *testing.T) {
	phases := []ModelPhase{
		ModelPhaseIdle,
		ModelPhasePending,
		ModelPhaseLoading,
		ModelPhaseReady,
		ModelPhasePreempted,
		ModelPhaseFailed,
	}

	expected := []string{"Idle", "Pending", "Loading", "Ready", "Preempted", "Failed"}

	for i, phase := range phases {
		if string(phase) != expected[i] {
			t.Errorf("Phase %d = %q, want %q", i, phase, expected[i])
		}
	}
}

func TestLoadingSubstages(t *testing.T) {
	substages := []LoadingSubstage{
		LoadingSubstageImagePulling,
		LoadingSubstageInitializing,
		LoadingSubstageLoadingWeights,
		LoadingSubstageCompiling,
		LoadingSubstageHealthCheckPending,
		LoadingSubstagePreempted,
	}

	expected := []string{"ImagePulling", "Initializing", "LoadingWeights", "Compiling", "HealthCheckPending", "Preempted"}

	for i, substage := range substages {
		if string(substage) != expected[i] {
			t.Errorf("Substage %d = %q, want %q", i, substage, expected[i])
		}
	}
}

func TestModelStatus(t *testing.T) {
	status := &ModelStatus{
		Phase:    ModelPhaseReady,
		Endpoint: "http://qwen3-8b.default.svc:8000",
		GPU: &GPUStatus{
			Node:         "gpu-node-1",
			Device:       "0",
			Vendor:       "AMD",
			Architecture: "gfx1100",
			MemoryMB:     24576,
		},
		Metrics: &MetricsStatus{
			TokensPerSecond: "45.2",
			LoadTimeSeconds: "12.5",
		},
	}

	if status.Phase != ModelPhaseReady {
		t.Errorf("Phase = %q, want 'Ready'", status.Phase)
	}

	if status.GPU.Vendor != "AMD" {
		t.Errorf("GPU.Vendor = %q, want 'AMD'", status.GPU.Vendor)
	}

	if status.GPU.Architecture != "gfx1100" {
		t.Errorf("GPU.Architecture = %q, want 'gfx1100'", status.GPU.Architecture)
	}
}

func TestSharedGroupStatus(t *testing.T) {
	status := &SharedGroupStatus{
		GroupName:     "homelab-gpu",
		State:         "Active",
		QueuePosition: 0,
	}

	if status.GroupName != "homelab-gpu" {
		t.Errorf("GroupName = %q, want 'homelab-gpu'", status.GroupName)
	}

	if status.State != "Active" {
		t.Errorf("State = %q, want 'Active'", status.State)
	}

	// Test preempted state
	preempted := &SharedGroupStatus{
		GroupName:   "homelab-gpu",
		State:       "Preempted",
		PreemptedBy: "high-priority-model",
	}

	if preempted.PreemptedBy != "high-priority-model" {
		t.Errorf("PreemptedBy = %q, want 'high-priority-model'", preempted.PreemptedBy)
	}
}

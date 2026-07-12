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
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func gpuNode(name string, capacityResource string, labels map[string]string) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
	if capacityResource != "" {
		node.Status.Capacity = corev1.ResourceList{
			corev1.ResourceName(capacityResource): resource.MustParse("1"),
		}
	}
	return node
}

func TestGPUInfoFromNode(t *testing.T) {
	tests := []struct {
		name       string
		node       *corev1.Node
		wantVendor backend.GPUVendor
		wantArch   string
		wantOK     bool
	}{
		{
			name:       "nvidia via compute.major",
			node:       gpuNode("n", "nvidia.com/gpu", map[string]string{"nvidia.com/gpu.compute.major": "8"}),
			wantVendor: backend.GPUVendorNVIDIA,
			wantArch:   "sm_8",
			wantOK:     true,
		},
		{
			name:       "nvidia via flexinfer arch label",
			node:       gpuNode("n", "nvidia.com/gpu", map[string]string{LabelGPUArch: "sm_52"}),
			wantVendor: backend.GPUVendorNVIDIA,
			wantArch:   "sm_52",
			wantOK:     true,
		},
		{
			name:       "nvidia without arch labels",
			node:       gpuNode("n", "nvidia.com/gpu", nil),
			wantVendor: backend.GPUVendorNVIDIA,
			wantArch:   "",
			wantOK:     true,
		},
		{
			name:       "amd via gpu-architecture label",
			node:       gpuNode("n", "amd.com/gpu", map[string]string{"gpu.amd.com/gpu-architecture": "gfx1100"}),
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx1100",
			wantOK:     true,
		},
		{
			name:       "amd via flexinfer arch label",
			node:       gpuNode("n", "amd.com/gpu", map[string]string{LabelGPUArch: "gfx906"}),
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx906",
			wantOK:     true,
		},
		{
			name:       "amd via GC 11.0.0 family fallback",
			node:       gpuNode("n", "amd.com/gpu", map[string]string{"amd.com/gpu.family.GC_11_0_0": "1"}),
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx1100",
			wantOK:     true,
		},
		{
			name:       "amd via 7900 model-name fallback",
			node:       gpuNode("n", "amd.com/gpu", map[string]string{"gpu.amd.com/model": "Radeon RX 7900 XTX"}),
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx1100",
			wantOK:     true,
		},
		{
			name:       "no gpu capacity",
			node:       gpuNode("n", "", map[string]string{LabelGPUArch: "gfx1100"}),
			wantVendor: backend.GPUVendorUnknown,
			wantArch:   "",
			wantOK:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vendor, arch, ok := gpuInfoFromNode(tt.node)
			if vendor != tt.wantVendor || arch != tt.wantArch || ok != tt.wantOK {
				t.Fatalf("gpuInfoFromNode() = (%q, %q, %v), want (%q, %q, %v)",
					vendor, arch, ok, tt.wantVendor, tt.wantArch, tt.wantOK)
			}
		})
	}
}

func TestSetGPUStatus(t *testing.T) {
	model := &aiv1alpha2.Model{}

	// Empty node name is a no-op.
	setGPUStatus(model, "", backend.GPUVendorAMD, "gfx1100")
	if model.Status.GPU != nil {
		t.Fatal("empty node name must not create status.gpu")
	}

	setGPUStatus(model, "node-a", backend.GPUVendorAMD, "gfx1100")
	got := model.Status.GPU
	if got == nil || got.Node != "node-a" || got.Vendor != "AMD" || got.Architecture != "gfx1100" {
		t.Fatalf("status.gpu = %+v, want node-a/AMD/gfx1100", got)
	}

	// Fields not owned by setGPUStatus survive re-stamping, and empty
	// vendor/arch inputs keep the previously stamped values.
	model.Status.GPU.Device = "0"
	model.Status.GPU.MemoryMB = 24576
	setGPUStatus(model, "node-b", backend.GPUVendorUnknown, "")
	got = model.Status.GPU
	if got.Node != "node-b" {
		t.Fatalf("node = %q, want node-b", got.Node)
	}
	if got.Vendor != "AMD" || got.Architecture != "gfx1100" {
		t.Fatalf("unknown vendor/empty arch clobbered previous values: %+v", got)
	}
	if got.Device != "0" || got.MemoryMB != 24576 {
		t.Fatalf("device/memoryMB not preserved: %+v", got)
	}
}

// TestUpdateStatusFromDeployment_StampsGPUStatus is the regression guard for
// the never-populated status.gpu block: a Ready deployment-served model must
// carry the scheduled pod's node plus the node's vendor/architecture, so
// consumers do not have to fall back to spec.nodeSelector.
func TestUpdateStatusFromDeployment_StampsGPUStatus(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		appsv1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "served-model", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "pvc://weights",
			GPU:     &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD},
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "served-model", Namespace: "flexinfer-system"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	node := gpuNode("gpu-node-a", "amd.com/gpu", map[string]string{"gpu.amd.com/gpu-architecture": "gfx1100"})

	// Two pods: an older terminating one on a stale node and the current one.
	// The newest scheduled pod must win.
	oldPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "served-model-old",
			Namespace:         "flexinfer-system",
			Labels:            map[string]string{LabelModel: "served-model"},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
		},
		Spec: corev1.PodSpec{NodeName: "gpu-node-stale"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "served-model-new",
			Namespace:         "flexinfer-system",
			Labels:            map[string]string{LabelModel: "served-model"},
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Spec: corev1.PodSpec{NodeName: "gpu-node-a"},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		WithObjects(model, deployment, node, oldPod, pod).
		Build()
	r := &ModelReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	current := &aiv1alpha2.Model{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(model), current); err != nil {
		t.Fatalf("get model: %v", err)
	}
	if err := r.updateStatusFromDeployment(ctx, current); err != nil {
		t.Fatalf("updateStatusFromDeployment: %v", err)
	}

	persisted := &aiv1alpha2.Model{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(model), persisted); err != nil {
		t.Fatalf("get model after update: %v", err)
	}
	gpu := persisted.Status.GPU
	if gpu == nil {
		t.Fatal("status.gpu not populated")
	}
	if gpu.Node != "gpu-node-a" {
		t.Errorf("status.gpu.node = %q, want gpu-node-a", gpu.Node)
	}
	if gpu.Vendor != "AMD" {
		t.Errorf("status.gpu.vendor = %q, want AMD", gpu.Vendor)
	}
	if gpu.Architecture != "gfx1100" {
		t.Errorf("status.gpu.architecture = %q, want gfx1100", gpu.Architecture)
	}
	if persisted.Status.Phase != aiv1alpha2.ModelPhaseReady {
		t.Errorf("phase = %q, want Ready", persisted.Status.Phase)
	}
}

// TestStampGPUStatusFromPods_CPUAndUnscheduled covers the gates: CPU-only
// models and models with no scheduled pod must not stamp (or clobber)
// status.gpu.
func TestStampGPUStatusFromPods_CPUAndUnscheduled(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}
	ctx := context.Background()

	// CPU-only model (no spec.gpu): never stamped even with a scheduled pod.
	cpuModel := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu-model", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "llamacpp", Source: "pvc://weights"},
	}
	cpuPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cpu-model-pod",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{LabelModel: "cpu-model"},
		},
		Spec: corev1.PodSpec{NodeName: "cpu-node"},
	}
	r := &ModelReconciler{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(cpuPod).Build(), Scheme: s}
	r.stampGPUStatusFromPods(ctx, cpuModel)
	if cpuModel.Status.GPU != nil {
		t.Fatalf("CPU-only model stamped status.gpu: %+v", cpuModel.Status.GPU)
	}

	// GPU model whose pods are gone keeps the last-known placement.
	gpuModel := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-model", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "pvc://weights",
			GPU:     &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD},
		},
		Status: aiv1alpha2.ModelStatus{
			GPU: &aiv1alpha2.GPUStatus{Node: "gpu-node-a", Vendor: "AMD", Architecture: "gfx1100"},
		},
	}
	r = &ModelReconciler{Client: fake.NewClientBuilder().WithScheme(s).Build(), Scheme: s}
	r.stampGPUStatusFromPods(ctx, gpuModel)
	if gpuModel.Status.GPU == nil || gpuModel.Status.GPU.Node != "gpu-node-a" {
		t.Fatalf("last-known placement lost: %+v", gpuModel.Status.GPU)
	}
}

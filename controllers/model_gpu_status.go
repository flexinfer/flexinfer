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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// nvidiaGPUArchFromNode reports whether the node exposes an NVIDIA GPU and,
// if so, its architecture. arch may be empty when the node carries the GPU
// resource but no recognizable architecture label.
func nvidiaGPUArchFromNode(node *corev1.Node) (string, bool) {
	qty, ok := node.Status.Capacity["nvidia.com/gpu"]
	if !ok || qty.Value() < 1 {
		return "", false
	}
	arch := ""
	if node.Labels != nil {
		if major := node.Labels["nvidia.com/gpu.compute.major"]; major != "" {
			arch = "sm_" + major
		}
		// Fall back to flexinfer.ai/gpu.arch label (same as AMD detection).
		if arch == "" {
			arch = node.Labels[LabelGPUArch]
		}
	}
	return arch, true
}

// amdGPUArchFromNode reports whether the node exposes an AMD GPU and, if so,
// its architecture. arch may be empty when the node carries the GPU resource
// but no recognizable architecture label.
func amdGPUArchFromNode(node *corev1.Node) (string, bool) {
	qty, ok := node.Status.Capacity["amd.com/gpu"]
	if !ok || qty.Value() < 1 {
		return "", false
	}
	arch := ""
	if node.Labels != nil {
		arch = node.Labels["gpu.amd.com/gpu-architecture"]
		if arch == "" {
			// FlexInfer agent sets this label via rocminfo detection.
			arch = node.Labels[LabelGPUArch]
		}
		if arch == "" {
			// ROCm arch label isn't always present; fall back to common node-level labels.
			// Prefer RDNA3 dGPU (GC 11.0.0) when multiple AMD GPUs exist on the same node.
			if node.Labels["amd.com/gpu.family.GC_11_0_0"] != "" {
				arch = "gfx1100"
			} else if node.Labels["amd.com/gpu.family.GC_10_3_6"] != "" {
				arch = "gfx1036"
			} else if modelName := node.Labels["gpu.amd.com/model"]; strings.Contains(modelName, "7900") {
				arch = "gfx1100"
			}
		}
	}
	return arch, true
}

// gpuInfoFromNode classifies a single node's GPU vendor and architecture
// using the same capacity + label ladder detectGPU uses for placement. ok is
// false when the node exposes no known GPU resource.
func gpuInfoFromNode(node *corev1.Node) (backend.GPUVendor, string, bool) {
	if arch, ok := nvidiaGPUArchFromNode(node); ok {
		return backend.GPUVendorNVIDIA, arch, true
	}
	if arch, ok := amdGPUArchFromNode(node); ok {
		return backend.GPUVendorAMD, arch, true
	}
	return backend.GPUVendorUnknown, "", false
}

// gpuVendorStatusString maps a backend vendor to the canonical
// status.gpu.vendor form documented on GPUStatus ("NVIDIA", "AMD", "Intel").
func gpuVendorStatusString(vendor backend.GPUVendor) string {
	switch vendor {
	case backend.GPUVendorNVIDIA:
		return "NVIDIA"
	case backend.GPUVendorAMD:
		return "AMD"
	case backend.GPUVendorIntel:
		return "Intel"
	default:
		return ""
	}
}

// setGPUStatus records observed GPU placement on the Model status. An empty
// vendor or arch leaves any previously stamped value in place, and fields
// this function does not own (Device, MemoryMB) are preserved. Callers are
// responsible for persisting status.
func setGPUStatus(model *aiv1alpha2.Model, nodeName string, vendor backend.GPUVendor, arch string) {
	if nodeName == "" {
		return
	}
	if model.Status.GPU == nil {
		model.Status.GPU = &aiv1alpha2.GPUStatus{}
	}
	model.Status.GPU.Node = nodeName
	if v := gpuVendorStatusString(vendor); v != "" {
		model.Status.GPU.Vendor = v
	}
	if arch != "" {
		model.Status.GPU.Architecture = arch
	}
}

// stampGPUStatusFromPods populates status.gpu from the model's most recently
// scheduled backend pod. This is the Deployment-flow counterpart of the
// runtime flow's endpoint-based stamp in reconcileViaRuntime: consumers (the
// KV-cache pressure check, backfill node affinity, external dashboards) read
// status.gpu.node for actual placement instead of parsing spec scheduling
// hints. Best-effort: any lookup failure leaves the previous (last-known)
// placement untouched, so a scaled-to-zero model keeps the node it last ran
// on.
func (r *ModelReconciler) stampGPUStatusFromPods(ctx context.Context, model *aiv1alpha2.Model) {
	// CPU-only models allocate no GPU; mirror detectGPU's gate.
	if model.Spec.GetGPUCount() == 0 || model.Spec.GetGPUVendor() == aiv1alpha2.GPUVendorCPU {
		return
	}

	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(model.Namespace),
		client.MatchingLabels{LabelModel: model.Name},
	); err != nil {
		log.FromContext(ctx).V(1).Info("stampGPUStatusFromPods: list pods failed", "error", err.Error())
		return
	}

	// Newest scheduled pod wins: ReplicaSet churn leaves older terminating
	// pods around that would otherwise pin placement to a stale node.
	var newest *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName == "" {
			continue
		}
		if newest == nil || pod.CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = pod
		}
	}
	if newest == nil {
		return
	}

	vendor, arch := backend.GPUVendorUnknown, ""
	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: newest.Spec.NodeName}, node); err == nil {
		vendor, arch, _ = gpuInfoFromNode(node)
	} else {
		log.FromContext(ctx).V(1).Info("stampGPUStatusFromPods: get node failed",
			"node", newest.Spec.NodeName, "error", err.Error())
	}
	setGPUStatus(model, newest.Spec.NodeName, vendor, arch)
}

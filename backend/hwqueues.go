/*
Copyright 2025 FlexInfer Authors.

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

package backend

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Hardware compute queue (HWS / HQD) accounting.
//
// An AMD GPU's hardware scheduler maps a fixed number of compute queues at
// once (KFD num_cp_queues: 24 on the Radeon VII). Past that the kernel logs
// "Runlist is getting oversubscribed" and time-slices the queues, which stalls
// co-resident lanes for seconds. VRAM admission cannot see this, so the
// controller and the runtime also admit against a per-Model queue estimate.
//
// The estimate counts compute queues per HIP process and HIP processes per
// backend. Measured on cblevins-radeonvii through
// /sys/class/kfd/kfd/proc/<pid>/queues (docs/dev/gfx906-gpu-support.md):
//
//   - llama-server with GPU_MAX_HW_QUEUES=1 and HSA_ENABLE_SDMA=0 holds 3
//     compute queues (four live lanes, 2026-10-24 and 2026-10-30).
//   - Uncapped (ROCclr default GPU_MAX_HW_QUEUES=4) the same server held 4 and
//     grew to 6 under concurrent requests.
//
// So a HIP process holds GPU_MAX_HW_QUEUES kernel queues plus 2 runtime queues
// at most. With SDMA enabled one of those 2 becomes an SDMA queue, which the
// compute budget does not count; the estimate keeps 2 (safe direction).

const (
	// EnvGPUMaxHWQueues is the ROCclr variable that caps the kernel queues a
	// HIP process creates per device.
	EnvGPUMaxHWQueues = "GPU_MAX_HW_QUEUES"

	// DefaultGPUMaxHWQueues is ROCclr's GPU_MAX_HW_QUEUES default.
	DefaultGPUMaxHWQueues int32 = 4

	// hipRuntimeQueuesPerProcess is the compute queues a HIP process holds on
	// top of its GPU_MAX_HW_QUEUES kernel queues (3 measured at a cap of 1).
	hipRuntimeQueuesPerProcess int32 = 2
)

// HIPProcessesPerModel returns how many HIP processes a backend keeps on the
// GPU for one loaded model.
//
//   - llamacpp: one llama-server (measured).
//   - ollama: one runner subprocess per loaded model; the `ollama serve` parent
//     only probes devices from short-lived children.
//   - diffusers, snac, comfyui, mlc-llm: one Python/TVM server process.
//   - whispercpp: one whisper-server.
//   - vllm, vllm-omni: two. The API server and the engine-core process are
//     separate processes (V1 EngineCore, V0 MQLLMEngine). Not measured on
//     gfx906, so the estimate assumes both reach the device; set
//     spec.gpu.hwQueueEstimate after measuring a lane.
func HIPProcessesPerModel(backendName string) int32 {
	switch canonicalBackendName(backendName) {
	case NameVLLM, NameVLLMOmni:
		return 2
	default:
		return 1
	}
}

// HWQueuesPerHIPProcess returns the compute queues one HIP process holds for a
// GPU_MAX_HW_QUEUES value ("" or an invalid value means the ROCclr default).
func HWQueuesPerHIPProcess(gpuMaxHWQueues string) int32 {
	n := DefaultGPUMaxHWQueues
	if v, err := strconv.Atoi(strings.TrimSpace(gpuMaxHWQueues)); err == nil && v > 0 {
		n = int32(v)
	}
	return n + hipRuntimeQueuesPerProcess
}

// DefaultHWQueueEstimate is a model's compute-queue estimate from its backend
// and the environment its processes start with. The last GPU_MAX_HW_QUEUES in
// env wins, as it does when the env list is rendered.
func DefaultHWQueueEstimate(backendName string, env []corev1.EnvVar) int32 {
	return HIPProcessesPerModel(backendName) * HWQueuesPerHIPProcess(LastEnvValue(env, EnvGPUMaxHWQueues))
}

// LastEnvValue returns the value of the last entry named name, or "".
func LastEnvValue(env []corev1.EnvVar, name string) string {
	value := ""
	for _, e := range env {
		if e.Name == name {
			value = e.Value
		}
	}
	return value
}

// ModelHWQueueEstimate returns the compute queues a Model is expected to hold
// on one GPU: spec.gpu.hwQueueEstimate when set, otherwise
// DefaultHWQueueEstimate over the env its processes get (the GPUProfile env,
// or the in-code ROCm env without a profile, overlaid by spec.config.env).
func ModelHWQueueEstimate(model *aiv1alpha2.Model, profile *aiv1alpha2.GPUProfileSpec, vendor GPUVendor, arch string) int32 {
	if model == nil {
		return 0
	}
	if model.Spec.GPU != nil && model.Spec.GPU.HWQueueEstimate != nil {
		return *model.Spec.GPU.HWQueueEstimate
	}
	env := append([]corev1.EnvVar(nil), ResolveBackendROCmEnv(profile, vendor, arch)...)
	// An invalid spec.config.env is rejected by validation before the
	// estimate matters; ignore it here.
	if modelEnv, err := ModelEnvVars(model.Spec.GetConfigMap()); err == nil {
		env = append(env, modelEnv...)
	}
	return DefaultHWQueueEstimate(model.Spec.Backend, env)
}

// HWQueueBudget returns the GPUProfile's compute-queue limit and the queues it
// reserves for non-Model tenants. ok is false when the profile does not set
// maxHWQueues, which disables queue admission.
func HWQueueBudget(profile *aiv1alpha2.GPUProfileSpec) (maxQueues, reserved int32, ok bool) {
	if profile == nil || profile.MaxHWQueues <= 0 {
		return 0, 0, false
	}
	reserved = profile.ReservedHWQueues
	if reserved < 0 {
		reserved = 0
	}
	return profile.MaxHWQueues, reserved, true
}

func canonicalBackendName(name string) string {
	if b, ok := Get(name); ok {
		return b.Name()
	}
	return strings.TrimSpace(name)
}

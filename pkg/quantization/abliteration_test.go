package quantization

import (
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func ablitInt32Ptr(i int32) *int32    { return &i }
func ablitInt64Ptr(i int64) *int64    { return &i }
func ablitStringPtr(s string) *string { return &s }
func ablitBoolPtr(b bool) *bool       { return &b }

func TestBuildAbliterationJob_Defaults(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: true,
	}
	params := JobParams{
		Name:      "test-cache",
		Namespace: "flexinfer-system",
		PVCName:   "test-pvc",
		ModelPath: "test-cache",
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	// Job name
	if job.Name != "test-cache-abliterate" {
		t.Errorf("job.Name = %q, want %q", job.Name, "test-cache-abliterate")
	}

	// Labels
	if job.Labels["flexinfer.ai/component"] != "abliterator" {
		t.Errorf("component label = %q, want %q", job.Labels["flexinfer.ai/component"], "abliterator")
	}
	if job.Labels["flexinfer.ai/cache"] != "test-cache" {
		t.Errorf("cache label = %q, want %q", job.Labels["flexinfer.ai/cache"], "test-cache")
	}

	// BackoffLimit
	if *job.Spec.BackoffLimit != 2 {
		t.Errorf("BackoffLimit = %d, want 2", *job.Spec.BackoffLimit)
	}

	// Default deadline (14400s)
	if *job.Spec.ActiveDeadlineSeconds != 14400 {
		t.Errorf("ActiveDeadlineSeconds = %d, want 14400", *job.Spec.ActiveDeadlineSeconds)
	}

	// Container name
	container := job.Spec.Template.Spec.Containers[0]
	if container.Name != "abliterator" {
		t.Errorf("container.Name = %q, want %q", container.Name, "abliterator")
	}

	// GPU resource request (AMD)
	gpuReq := container.Resources.Requests["amd.com/gpu"]
	if gpuReq.String() != "1" {
		t.Errorf("GPU request = %q, want 1", gpuReq.String())
	}

	// Memory request should leave headroom for node scheduling while retaining
	// the full runtime limit.
	memReq := container.Resources.Requests["memory"]
	if memReq.String() != "44Gi" {
		t.Errorf("memory request = %q, want 44Gi", memReq.String())
	}
	memLimit := container.Resources.Limits["memory"]
	if memLimit.String() != "56Gi" {
		t.Errorf("memory limit = %q, want 56Gi", memLimit.String())
	}

	// PYTORCH_HIP_ALLOC_CONF for AMD
	foundHipConf := false
	for _, env := range container.Env {
		if env.Name == "PYTORCH_HIP_ALLOC_CONF" {
			foundHipConf = true
			if env.Value != "expandable_segments:True" {
				t.Errorf("PYTORCH_HIP_ALLOC_CONF = %q, want expandable_segments:True", env.Value)
			}
		}
	}
	if !foundHipConf {
		t.Error("missing PYTORCH_HIP_ALLOC_CONF env var for AMD GPU")
	}
}

func TestBuildAbliterationJob_CustomSpec(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		TargetLayers:     ablitStringPtr("10-55"),
		WeightMatrices:   []string{"o_proj", "down_proj", "gate_proj"},
		NumSamples:       ablitInt32Ptr(64),
		MaxMemoryGB:      ablitInt32Ptr(32),
		TimeoutSeconds:   ablitInt64Ptr(3600),
		UseGPU:           false,
		SkipVisionLayers: ablitBoolPtr(false),
	}
	params := JobParams{
		Name:      "custom-cache",
		Namespace: "default",
		PVCName:   "custom-pvc",
		ModelPath: "my-model",
		GPUVendor: "nvidia",
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	// Custom deadline
	if *job.Spec.ActiveDeadlineSeconds != 3600 {
		t.Errorf("ActiveDeadlineSeconds = %d, want 3600", *job.Spec.ActiveDeadlineSeconds)
	}

	// Custom memory should request less than the limit.
	container := job.Spec.Template.Spec.Containers[0]
	memReq := container.Resources.Requests["memory"]
	if memReq.String() != "25Gi" {
		t.Errorf("memory request = %q, want 25Gi", memReq.String())
	}
	memLimit := container.Resources.Limits["memory"]
	if memLimit.String() != "32Gi" {
		t.Errorf("memory limit = %q, want 32Gi", memLimit.String())
	}

	// No GPU resource when UseGPU=false
	if _, found := container.Resources.Requests["nvidia.com/gpu"]; found {
		t.Error("GPU resource should not be set when UseGPU=false")
	}
	if _, found := container.Resources.Requests["amd.com/gpu"]; found {
		t.Error("GPU resource should not be set when UseGPU=false")
	}
}

func TestBuildAbliterationJob_NilSpec(t *testing.T) {
	params := JobParams{
		Name:      "test",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test",
	}

	_, err := BuildAbliterationJob(params, nil)
	if err == nil {
		t.Error("expected error for nil spec, got nil")
	}
}

func TestBuildAbliterationJob_NvidiaGPU(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: true,
	}
	params := JobParams{
		Name:      "test-cache",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-cache",
		GPUVendor: "nvidia",
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	gpuReq := container.Resources.Requests["nvidia.com/gpu"]
	if gpuReq.String() != "1" {
		t.Errorf("GPU request = %q, want 1", gpuReq.String())
	}

	// Should NOT have HIP alloc conf for NVIDIA
	for _, env := range container.Env {
		if env.Name == "PYTORCH_HIP_ALLOC_CONF" {
			t.Error("PYTORCH_HIP_ALLOC_CONF should not be set for NVIDIA")
		}
	}
}

func TestAbliterationEnv_Content(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		NumSamples:       ablitInt32Ptr(64),
		TargetLayers:     ablitStringPtr("10-55"),
		WeightMatrices:   []string{"o_proj", "down_proj"},
		UseGPU:           true,
		SkipVisionLayers: ablitBoolPtr(true),
	}

	env := abliterationEnv("my-model", spec)

	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	checks := []struct {
		name, key, want string
	}{
		{"model dir", "MODEL_DIR", "/cache/my-model"},
		{"num samples", "NUM_SAMPLES", "64"},
		{"target layers", "TARGET_LAYERS", "10-55"},
		{"weight matrices", "WEIGHT_MATRICES", "o_proj,down_proj"},
		{"skip vision", "SKIP_VISION", "true"},
		{"device map auto", "DEVICE_MAP", "auto"},
		{"progress interval", "ABLITERATION_PROGRESS_INTERVAL", "10"},
		{"prompt max length", "ABLITERATION_PROMPT_MAX_LENGTH", "256"},
		{"save format", "ABLITERATION_SAVE_FORMAT", "auto"},
		{"activation capture mode", "ABLITERATION_ACTIVATION_CAPTURE_MODE", "hooks"},
		{"memory trim interval", "ABLITERATION_MEMORY_TRIM_INTERVAL", "1"},
		{"forward use cache", "ABLITERATION_FORWARD_USE_CACHE", "false"},
		{"save shard size", "ABLITERATION_SAVE_MAX_SHARD_SIZE", "1GB"},
		{"save impl", "ABLITERATION_SAVE_IMPL", "streaming"},
		{"cpu max memory", "ABLITERATION_CPU_MAX_MEMORY_GB", "20"},
		{"gpu max memory", "ABLITERATION_GPU_MAX_MEMORY_GB", "20"},
		{"offload dir", "ABLITERATION_OFFLOAD_DIR", "/workspace/abliteration-offload"},
		{"telemetry", "FLEXINFER_TELEMETRY", "true"},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if got := envMap[check.key]; got != check.want {
				t.Errorf("%s = %q, want %q", check.key, got, check.want)
			}
		})
	}

	if got := envMap["ABLITERATION_MODEL_POLICIES"]; !strings.Contains(got, "qwen3.5-save-safetensors") {
		t.Errorf("ABLITERATION_MODEL_POLICIES = %q, want default qwen3.5 policy JSON", got)
	}
}

func TestAbliterationEnv_OperatorOverrides(t *testing.T) {
	t.Setenv("FLEXINFER_ABLITERATION_PROGRESS_INTERVAL", "5")
	t.Setenv("FLEXINFER_ABLITERATION_PROMPT_MAX_LENGTH", "384")
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_FORMAT", "safetensors")
	t.Setenv("FLEXINFER_ABLITERATION_ACTIVATION_CAPTURE_MODE", "hidden_states")
	t.Setenv("FLEXINFER_ABLITERATION_MEMORY_TRIM_INTERVAL", "3")
	t.Setenv("FLEXINFER_ABLITERATION_FORWARD_USE_CACHE", "true")
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_MAX_SHARD_SIZE", "2GB")
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_IMPL", "materialized")
	t.Setenv("FLEXINFER_ABLITERATION_CPU_MAX_MEMORY_GB", "28")
	t.Setenv("FLEXINFER_ABLITERATION_GPU_MAX_MEMORY_GB", "18")
	t.Setenv("FLEXINFER_ABLITERATION_OFFLOAD_DIR", "/tmp/ablit-offload")
	t.Setenv("FLEXINFER_ABLITERATION_MODEL_POLICIES", `[{"name":"custom"}]`)

	env := abliterationEnv("my-model", &aiv1alpha1.AbliterationSpec{})
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if got := envMap["ABLITERATION_PROGRESS_INTERVAL"]; got != "5" {
		t.Errorf("ABLITERATION_PROGRESS_INTERVAL = %q, want 5", got)
	}
	if got := envMap["ABLITERATION_PROMPT_MAX_LENGTH"]; got != "384" {
		t.Errorf("ABLITERATION_PROMPT_MAX_LENGTH = %q, want 384", got)
	}
	if got := envMap["ABLITERATION_SAVE_FORMAT"]; got != "safetensors" {
		t.Errorf("ABLITERATION_SAVE_FORMAT = %q, want safetensors", got)
	}
	if got := envMap["ABLITERATION_ACTIVATION_CAPTURE_MODE"]; got != "hidden_states" {
		t.Errorf("ABLITERATION_ACTIVATION_CAPTURE_MODE = %q, want hidden_states", got)
	}
	if got := envMap["ABLITERATION_MEMORY_TRIM_INTERVAL"]; got != "3" {
		t.Errorf("ABLITERATION_MEMORY_TRIM_INTERVAL = %q, want 3", got)
	}
	if got := envMap["ABLITERATION_FORWARD_USE_CACHE"]; got != "true" {
		t.Errorf("ABLITERATION_FORWARD_USE_CACHE = %q, want true", got)
	}
	if got := envMap["ABLITERATION_SAVE_MAX_SHARD_SIZE"]; got != "2GB" {
		t.Errorf("ABLITERATION_SAVE_MAX_SHARD_SIZE = %q, want 2GB", got)
	}
	if got := envMap["ABLITERATION_SAVE_IMPL"]; got != "materialized" {
		t.Errorf("ABLITERATION_SAVE_IMPL = %q, want materialized", got)
	}
	if got := envMap["ABLITERATION_CPU_MAX_MEMORY_GB"]; got != "28" {
		t.Errorf("ABLITERATION_CPU_MAX_MEMORY_GB = %q, want 28", got)
	}
	if got := envMap["ABLITERATION_GPU_MAX_MEMORY_GB"]; got != "18" {
		t.Errorf("ABLITERATION_GPU_MAX_MEMORY_GB = %q, want 18", got)
	}
	if got := envMap["ABLITERATION_OFFLOAD_DIR"]; got != "/tmp/ablit-offload" {
		t.Errorf("ABLITERATION_OFFLOAD_DIR = %q, want /tmp/ablit-offload", got)
	}
	if got := envMap["ABLITERATION_MODEL_POLICIES"]; got != `[{"name":"custom"}]` {
		t.Errorf("ABLITERATION_MODEL_POLICIES = %q, want custom JSON", got)
	}
}

func TestAbliterationEnv_CPUMode(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: false,
	}

	env := abliterationEnv("test-model", spec)

	for _, e := range env {
		if e.Name == "DEVICE_MAP" {
			if e.Value != "cpu" {
				t.Errorf("DEVICE_MAP = %q, want cpu", e.Value)
			}
			return
		}
	}
	t.Error("DEVICE_MAP env var not found")
}

func TestAbliterationWrapperScript(t *testing.T) {
	script := abliterationWrapperScript()

	if !strings.Contains(script, "python3 /opt/flexinfer/scripts/abliterate.py") {
		t.Error("wrapper script should invoke abliterate.py")
	}
	if !strings.Contains(script, "set -euo pipefail") {
		t.Error("wrapper script should use strict mode")
	}
	if !strings.Contains(script, ".abliteration-checkpoint.json") {
		t.Error("wrapper script should dump the last checkpoint on failure")
	}
}

func TestBuildAbliterationJob_Tolerations(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: true,
	}
	params := JobParams{
		Name:      "test-cache",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-cache",
		GPUVendor: "amd",
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": "my-node",
		},
		Tolerations: []corev1.Toleration{
			{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			},
		},
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	podSpec := job.Spec.Template.Spec
	if len(podSpec.Tolerations) != 1 {
		t.Fatalf("expected 1 toleration, got %d", len(podSpec.Tolerations))
	}
	if podSpec.Tolerations[0].Key != "dedicated" {
		t.Errorf("toleration key = %q, want dedicated", podSpec.Tolerations[0].Key)
	}
	if podSpec.NodeSelector["kubernetes.io/hostname"] != "my-node" {
		t.Errorf("nodeSelector hostname = %q, want my-node", podSpec.NodeSelector["kubernetes.io/hostname"])
	}
}

func TestBuildAbliterationJob_Volumes(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: true,
	}
	params := JobParams{
		Name:      "test-cache",
		Namespace: "default",
		PVCName:   "my-pvc",
		ModelPath: "test-cache",
		GPUVendor: "amd",
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	podSpec := job.Spec.Template.Spec
	if len(podSpec.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(podSpec.Volumes))
	}

	// Check PVC volume
	pvcVol := podSpec.Volumes[0]
	if pvcVol.Name != "model-cache" {
		t.Errorf("volume 0 name = %q, want model-cache", pvcVol.Name)
	}
	if pvcVol.PersistentVolumeClaim.ClaimName != "my-pvc" {
		t.Errorf("PVC claim = %q, want my-pvc", pvcVol.PersistentVolumeClaim.ClaimName)
	}

	// Check workspace volume
	wsVol := podSpec.Volumes[1]
	if wsVol.Name != "workspace" {
		t.Errorf("volume 1 name = %q, want workspace", wsVol.Name)
	}

	// Check container mounts
	container := podSpec.Containers[0]
	if len(container.VolumeMounts) != 2 {
		t.Fatalf("expected 2 volume mounts, got %d", len(container.VolumeMounts))
	}
	if container.VolumeMounts[0].MountPath != "/cache" {
		t.Errorf("mount 0 path = %q, want /cache", container.VolumeMounts[0].MountPath)
	}
	if container.VolumeMounts[1].MountPath != "/workspace" {
		t.Errorf("mount 1 path = %q, want /workspace", container.VolumeMounts[1].MountPath)
	}
}

func TestMemoryRequestForLimitGB(t *testing.T) {
	tests := []struct {
		limit int32
		want  int32
	}{
		{limit: 56, want: 44},
		{limit: 60, want: 48},
		{limit: 32, want: 25},
		{limit: 8, want: 8},
		{limit: 0, want: 1},
	}

	for _, tt := range tests {
		if got := memoryRequestForLimitGB(tt.limit); got != tt.want {
			t.Errorf("memoryRequestForLimitGB(%d) = %d, want %d", tt.limit, got, tt.want)
		}
	}
}

func TestAbliterationMemoryBudgets(t *testing.T) {
	if got := abliterationCPUMaxMemoryGB(56); got != 20 {
		t.Errorf("abliterationCPUMaxMemoryGB(56) = %d, want 20", got)
	}
	if got := abliterationCPUMaxMemoryGB(60); got != 24 {
		t.Errorf("abliterationCPUMaxMemoryGB(60) = %d, want 24", got)
	}
	if got := abliterationGPUMaxMemoryGB(true); got != 20 {
		t.Errorf("abliterationGPUMaxMemoryGB(true) = %d, want 20", got)
	}
	if got := abliterationGPUMaxMemoryGB(false); got != 0 {
		t.Errorf("abliterationGPUMaxMemoryGB(false) = %d, want 0", got)
	}
}

func TestAbliterationImage_EnvOverride(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "custom-registry.io/abliterator:v1")

	img := abliterationImage("amd", "gfx1100")
	if img != "custom-registry.io/abliterator:v1" {
		t.Errorf("abliterationImage = %q, want custom override", img)
	}
}

func TestAbliterationImage_UnifiedRuntime(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "should-not-use-this")

	img := abliterationImage("amd", "gfx1100")
	if img != "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100" {
		t.Errorf("abliterationImage = %q, want unified runtime image", img)
	}
}

func TestAbliterationImage_DefaultAMD(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "")

	img := abliterationImage("amd", "gfx1100")
	if img != DefaultGPTQROCmImage {
		t.Errorf("abliterationImage(amd, gfx1100) = %q, want %q", img, DefaultGPTQROCmImage)
	}
}

func TestAbliterationImage_DefaultNvidia(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "")

	img := abliterationImage("nvidia", "")
	if img != DefaultGPTQImage {
		t.Errorf("abliterationImage(nvidia) = %q, want %q", img, DefaultGPTQImage)
	}
}

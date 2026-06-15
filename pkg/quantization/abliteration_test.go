package quantization

import (
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
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
	if got := job.Spec.Template.Spec.PriorityClassName; got != PriorityClassTransform {
		t.Errorf("PriorityClassName = %q, want %q", got, PriorityClassTransform)
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

	// PYTORCH_ALLOC_CONF for AMD
	foundAllocConf := false
	for _, env := range container.Env {
		if env.Name == "PYTORCH_ALLOC_CONF" {
			foundAllocConf = true
			if env.Value != rocmAllocatorConfig {
				t.Errorf("PYTORCH_ALLOC_CONF = %q, want %q", env.Value, rocmAllocatorConfig)
			}
		}
	}
	if !foundAllocConf {
		t.Error("missing PYTORCH_ALLOC_CONF env var for AMD GPU")
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
		if env.Name == "PYTORCH_ALLOC_CONF" {
			t.Error("PYTORCH_ALLOC_CONF should not be set for NVIDIA")
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

	env := abliterationEnv("my-model", "gfx1100", spec, DefaultGPUMemoryConfig())

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
		{"ablate lm_head", "ABLITERATION_ABLITERATE_LM_HEAD", "false"},
		{"skip vision", "SKIP_VISION", "true"},
		{"device map auto", "DEVICE_MAP", "auto"},
		{"progress interval", "ABLITERATION_PROGRESS_INTERVAL", "10"},
		{"heartbeat interval", "ABLITERATION_HEARTBEAT_INTERVAL", "30"},
		{"prompt max length", "ABLITERATION_PROMPT_MAX_LENGTH", "256"},
		{"save format", "ABLITERATION_SAVE_FORMAT", "auto"},
		{"activation capture mode", "ABLITERATION_ACTIVATION_CAPTURE_MODE", "hooks"},
		{"memory trim interval", "ABLITERATION_MEMORY_TRIM_INTERVAL", "1"},
		{"forward use cache", "ABLITERATION_FORWARD_USE_CACHE", "false"},
		{"save shard size", "ABLITERATION_SAVE_MAX_SHARD_SIZE", "1GB"},
		{"save policy", "ABLITERATION_SAVE_POLICY", "auto"},
		{"save impl", "ABLITERATION_SAVE_IMPL", "streaming"},
		{"disk offload save impl", "ABLITERATION_DISK_OFFLOAD_SAVE_IMPL", ""},
		{"resume", "ABLITERATION_RESUME", "true"},
		{"cpu max memory", "ABLITERATION_CPU_MAX_MEMORY_GB", "36"},
		{"gpu max memory", "ABLITERATION_GPU_MAX_MEMORY_GB", "20"},
		{"offload dir", "ABLITERATION_OFFLOAD_DIR", "/workspace/abliteration-offload"},
		{"skip caching allocator warmup", "ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP", "false"},
		{"safe sharded load", "ABLITERATION_SAFE_SHARDED_LOAD", "false"},
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
	if got := envMap["ABLITERATION_MODEL_POLICIES"]; !strings.Contains(got, "gemma4-text") {
		t.Errorf("ABLITERATION_MODEL_POLICIES = %q, want default gemma4-text policy JSON", got)
	}
	if got := envMap["ABLITERATION_MODEL_POLICIES"]; !strings.Contains(got, "AutoModelForImageTextToText") {
		t.Errorf("ABLITERATION_MODEL_POLICIES = %q, want gemma4 load_auto_class", got)
	}
	if got := envMap["ABLITERATION_MODEL_POLICIES"]; !strings.Contains(got, `"decoder_layers_path":"model.language_model.layers"`) {
		t.Errorf("ABLITERATION_MODEL_POLICIES = %q, want gemma4 decoder_layers_path override", got)
	}
	if got := envMap["ABLITERATION_MODEL_POLICIES"]; !strings.Contains(got, `"lm_head_path":"lm_head"`) {
		t.Errorf("ABLITERATION_MODEL_POLICIES = %q, want gemma4 lm_head_path override", got)
	}
}

func TestAbliterationEnv_OperatorOverrides(t *testing.T) {
	t.Setenv("FLEXINFER_ABLITERATION_PROGRESS_INTERVAL", "5")
	t.Setenv("FLEXINFER_ABLITERATION_HEARTBEAT_INTERVAL", "15")
	t.Setenv("FLEXINFER_ABLITERATION_PROMPT_MAX_LENGTH", "384")
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_FORMAT", "safetensors")
	t.Setenv("FLEXINFER_ABLITERATION_DEVICE_MAP", "sequential")
	t.Setenv("FLEXINFER_ABLITERATION_ACTIVATION_CAPTURE_MODE", "hidden_states")
	t.Setenv("FLEXINFER_ABLITERATION_MEMORY_TRIM_INTERVAL", "3")
	t.Setenv("FLEXINFER_ABLITERATION_FORWARD_USE_CACHE", "true")
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_MAX_SHARD_SIZE", "2GB")
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_POLICY", "workspace")
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_IMPL", "materialized")
	t.Setenv("FLEXINFER_ABLITERATION_DISK_OFFLOAD_SAVE_IMPL", "streaming")
	t.Setenv("FLEXINFER_ABLITERATION_RESUME", "false")
	t.Setenv("FLEXINFER_ABLITERATION_CPU_MAX_MEMORY_GB", "48")
	t.Setenv("FLEXINFER_ABLITERATION_GPU_MAX_MEMORY_GB", "18")
	t.Setenv("FLEXINFER_ABLITERATION_OFFLOAD_DIR", "/tmp/ablit-offload")
	t.Setenv("FLEXINFER_ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP", "true")
	t.Setenv("FLEXINFER_ABLITERATION_SAFE_SHARDED_LOAD", "true")
	t.Setenv("FLEXINFER_ABLITERATION_MODEL_POLICIES", `[{"name":"custom"}]`)
	t.Setenv("FLEXINFER_ABLITERATION_TRANSFORMERS_PACKAGE", "git+https://github.com/huggingface/transformers.git@abc123")
	t.Setenv("FLEXINFER_ABLITERATION_TRANSFORMERS_RUNTIME_INSTALL", "fallback")

	env := abliterationEnv("my-model", "gfx1100", &aiv1alpha1.AbliterationSpec{}, DefaultGPUMemoryConfig())
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if got := envMap["ABLITERATION_PROGRESS_INTERVAL"]; got != "5" {
		t.Errorf("ABLITERATION_PROGRESS_INTERVAL = %q, want 5", got)
	}
	if got := envMap["ABLITERATION_HEARTBEAT_INTERVAL"]; got != "15" {
		t.Errorf("ABLITERATION_HEARTBEAT_INTERVAL = %q, want 15", got)
	}
	if got := envMap["ABLITERATION_PROMPT_MAX_LENGTH"]; got != "384" {
		t.Errorf("ABLITERATION_PROMPT_MAX_LENGTH = %q, want 384", got)
	}
	if got := envMap["ABLITERATION_SAVE_FORMAT"]; got != "safetensors" {
		t.Errorf("ABLITERATION_SAVE_FORMAT = %q, want safetensors", got)
	}
	if got := envMap["DEVICE_MAP"]; got != "sequential" {
		t.Errorf("DEVICE_MAP = %q, want sequential", got)
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
	if got := envMap["ABLITERATION_DISK_OFFLOAD_SAVE_IMPL"]; got != "streaming" {
		t.Errorf("ABLITERATION_DISK_OFFLOAD_SAVE_IMPL = %q, want streaming", got)
	}
	if got := envMap["ABLITERATION_SAVE_POLICY"]; got != "workspace" {
		t.Errorf("ABLITERATION_SAVE_POLICY = %q, want workspace", got)
	}
	if got := envMap["ABLITERATION_RESUME"]; got != "false" {
		t.Errorf("ABLITERATION_RESUME = %q, want false", got)
	}
	if got := envMap["ABLITERATION_CPU_MAX_MEMORY_GB"]; got != "48" {
		t.Errorf("ABLITERATION_CPU_MAX_MEMORY_GB = %q, want 48", got)
	}
	if got := envMap["ABLITERATION_GPU_MAX_MEMORY_GB"]; got != "18" {
		t.Errorf("ABLITERATION_GPU_MAX_MEMORY_GB = %q, want 18", got)
	}
	if got := envMap["ABLITERATION_OFFLOAD_DIR"]; got != "/tmp/ablit-offload" {
		t.Errorf("ABLITERATION_OFFLOAD_DIR = %q, want /tmp/ablit-offload", got)
	}
	if got := envMap["ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP"]; got != "true" {
		t.Errorf("ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP = %q, want true", got)
	}
	if got := envMap["ABLITERATION_SAFE_SHARDED_LOAD"]; got != "true" {
		t.Errorf("ABLITERATION_SAFE_SHARDED_LOAD = %q, want true", got)
	}
	if got := envMap["ABLITERATION_MODEL_POLICIES"]; got != `[{"name":"custom"}]` {
		t.Errorf("ABLITERATION_MODEL_POLICIES = %q, want custom JSON", got)
	}
	if got := envMap["ABLITERATION_TRANSFORMERS_PACKAGE"]; got != "git+https://github.com/huggingface/transformers.git@abc123" {
		t.Errorf("ABLITERATION_TRANSFORMERS_PACKAGE = %q, want pinned package", got)
	}
	if got := envMap["ABLITERATION_TRANSFORMERS_RUNTIME_INSTALL"]; got != "fallback" {
		t.Errorf("ABLITERATION_TRANSFORMERS_RUNTIME_INSTALL = %q, want fallback", got)
	}
}

func TestAbliterationEnv_CPUMode(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: false,
	}

	env := abliterationEnv("test-model", "gfx906", spec, DefaultGPUMemoryConfig())

	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
		if e.Name == "DEVICE_MAP" {
			if e.Value != "cpu" {
				t.Errorf("DEVICE_MAP = %q, want cpu", e.Value)
			}
		}
	}
	if got := envMap["DEVICE_MAP"]; got == "" {
		t.Error("DEVICE_MAP env var not found")
	}
	if got := envMap["ABLITERATION_SAVE_POLICY"]; got != "auto" {
		t.Errorf("ABLITERATION_SAVE_POLICY = %q, want auto", got)
	}
}

// TestAbliterationEnv_CPUMemoryClampsToContainer is a regression test for the
// 2026-04-21 gemma4-26b-a4b-gptq-dense abliteration OOM loop. GPUProfile
// gfx1100 declared maxCPUMemoryGB=44 (fine for the profile's default 48Gi
// container), but the per-ModelCache override set spec.abliteration.maxMemoryGB=28,
// capping the container at 40Gi. The resulting ABLITERATION_CPU_MAX_MEMORY_GB=44
// told accelerate to target 44Gi CPU offload and the cgroup killed the process
// at 40Gi before the collection phase even started. Every retry used the same
// mismatched values so the pipeline was stuck in a guaranteed-failure loop.
func TestAbliterationEnv_CPUMemoryClampsToContainer(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU:      true,
		MaxMemoryGB: ablitInt32Ptr(28),
	}
	memCfg := GPUMemoryConfig{
		ContainerMemoryGB: 48,
		MaxCPUMemoryGB:    44,
		MaxGPUMemoryGB:    18,
		GPUDriverMemoryMB: 12288,
	}

	env := abliterationEnv("gemma4-26b-a4b-gptq-dense", "gfx1100", spec, memCfg)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	const wantClamped = "24"
	if got := envMap["ABLITERATION_CPU_MAX_MEMORY_GB"]; got != wantClamped {
		t.Errorf("ABLITERATION_CPU_MAX_MEMORY_GB = %q, want %q (clamped to spec.maxMemoryGB minus 4Gi Python/activation headroom; profile declared 44 which would OOM a 28Gi container)", got, wantClamped)
	}
}

func TestAbliterationEnv_DefaultSavePolicyIsAuto(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU:      true,
		MaxMemoryGB: ablitInt32Ptr(96),
	}

	env := abliterationEnv("gemma4-31b-gptq", "gfx906", spec, DefaultGPUMemoryConfig())
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if got := envMap["ABLITERATION_SAVE_POLICY"]; got != "auto" {
		t.Fatalf("ABLITERATION_SAVE_POLICY = %q, want auto", got)
	}
}

func TestAbliterationEnv_ExplicitSavePolicyPassedThrough(t *testing.T) {
	t.Setenv("FLEXINFER_ABLITERATION_SAVE_POLICY", "inplace")

	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU:      true,
		MaxMemoryGB: ablitInt32Ptr(96),
	}

	env := abliterationEnv("gemma4-31b-gptq", "gfx906", spec, DefaultGPUMemoryConfig())
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if got := envMap["ABLITERATION_SAVE_POLICY"]; got != "inplace" {
		t.Fatalf("ABLITERATION_SAVE_POLICY = %q, want inplace", got)
	}
}

func TestAbliterationEnv_AblateLMHeadOptIn(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		AblitateLmHead: ablitBoolPtr(true),
		WeightMatrices: []string{"o_proj", "lm_head"},
	}

	env := abliterationEnv("test-model", "gfx1100", spec, DefaultGPUMemoryConfig())
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if got := envMap["ABLITERATION_ABLITERATE_LM_HEAD"]; got != "true" {
		t.Fatalf("ABLITERATION_ABLITERATE_LM_HEAD = %q, want true", got)
	}
	if got := envMap["WEIGHT_MATRICES"]; got != "o_proj,lm_head" {
		t.Fatalf("WEIGHT_MATRICES = %q, want o_proj,lm_head", got)
	}
}

func TestAbliterationEnv_AblateLMHeadDefaultsOff(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		WeightMatrices: []string{"o_proj", "lm_head"},
	}

	env := abliterationEnv("test-model", "gfx1100", spec, DefaultGPUMemoryConfig())
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if got := envMap["ABLITERATION_ABLITERATE_LM_HEAD"]; got != "false" {
		t.Fatalf("ABLITERATION_ABLITERATE_LM_HEAD = %q, want false", got)
	}
}

func TestAbliterationEnv_GFX906DisablesCachingAllocatorWarmup(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: true,
	}

	env := abliterationEnv("test-model", "gfx906", spec, DefaultGPUMemoryConfig())
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if got := envMap["DEVICE_MAP"]; got != "auto" {
		t.Fatalf("DEVICE_MAP = %q, want auto", got)
	}
	if got := envMap["ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP"]; got != "true" {
		t.Fatalf("ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP = %q, want true", got)
	}
	if got := envMap["ABLITERATION_SAFE_SHARDED_LOAD"]; got != "true" {
		t.Fatalf("ABLITERATION_SAFE_SHARDED_LOAD = %q, want true", got)
	}
	if got := envMap["ABLITERATION_GPU_MAX_MEMORY_GB"]; got != "14" {
		t.Fatalf("ABLITERATION_GPU_MAX_MEMORY_GB = %q, want 14", got)
	}
	if got := envMap["ABLITERATION_SAVE_POLICY"]; got != "auto" {
		t.Fatalf("ABLITERATION_SAVE_POLICY = %q, want auto", got)
	}
	if got := envMap["ABLITERATION_DISK_OFFLOAD_SAVE_IMPL"]; got != "streaming" {
		t.Fatalf("ABLITERATION_DISK_OFFLOAD_SAVE_IMPL = %q, want streaming for gfx906", got)
	}
}

func TestBuildAbliterationJob_ProfileEnvOverridesDefaults(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: true,
	}
	params := JobParams{
		Name:      "test-cache",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-cache",
		GPUVendor: "amd",
		GPUArch:   "gfx906",
		ProfileEnv: []corev1.EnvVar{
			{Name: "HSA_ENABLE_SDMA", Value: "0"},
			{Name: "ABLITERATION_ACTIVATION_CAPTURE_MODE", Value: "hidden_states"},
			{Name: "ABLITERATION_SAFE_SHARDED_LOAD", Value: "profile-override"},
			{Name: "ABLITERATION_SAVE_POLICY", Value: "inplace"},
			{Name: "ABLITERATION_TRANSFORMERS_PACKAGE", Value: "git+https://github.com/huggingface/transformers.git@profile"},
			{Name: "ABLITERATION_TRANSFORMERS_RUNTIME_INSTALL", Value: "fallback"},
		},
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	envMap := make(map[string]string)
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		envMap[env.Name] = env.Value
	}
	if got := envMap["HSA_ENABLE_SDMA"]; got != "0" {
		t.Fatalf("HSA_ENABLE_SDMA = %q, want 0", got)
	}
	if got := envMap["ABLITERATION_ACTIVATION_CAPTURE_MODE"]; got != "hidden_states" {
		t.Fatalf("ABLITERATION_ACTIVATION_CAPTURE_MODE = %q, want hidden_states", got)
	}
	if got := envMap["ABLITERATION_SAFE_SHARDED_LOAD"]; got != "profile-override" {
		t.Fatalf("ABLITERATION_SAFE_SHARDED_LOAD = %q, want profile-override", got)
	}
	if got := envMap["ABLITERATION_SAVE_POLICY"]; got != "inplace" {
		t.Fatalf("ABLITERATION_SAVE_POLICY = %q, want inplace", got)
	}
	if got := envMap["ABLITERATION_TRANSFORMERS_PACKAGE"]; got != "git+https://github.com/huggingface/transformers.git@profile" {
		t.Fatalf("ABLITERATION_TRANSFORMERS_PACKAGE = %q, want profile package", got)
	}
	if got := envMap["ABLITERATION_TRANSFORMERS_RUNTIME_INSTALL"]; got != "fallback" {
		t.Fatalf("ABLITERATION_TRANSFORMERS_RUNTIME_INSTALL = %q, want fallback", got)
	}
}

func TestAbliterationWrapperScript(t *testing.T) {
	script := abliterationWrapperScript()

	if !strings.Contains(script, "/opt/flexinfer/scripts/abliterate.py") {
		t.Error("wrapper script should invoke abliterate.py")
	}
	if !strings.Contains(script, "set -euo pipefail") {
		t.Error("wrapper script should use strict mode")
	}
	if !strings.Contains(script, ".abliteration-checkpoint.json") {
		t.Error("wrapper script should dump the last checkpoint on failure")
	}
	if !strings.Contains(script, "Download marker present but no source weight files exist") {
		t.Error("wrapper script should fail fast when download marker exists but no weights are present")
	}
	if !strings.Contains(script, ".download_complete") {
		t.Error("wrapper script should wait for the downloader completion marker")
	}
	if !strings.Contains(script, ".source-integrity.json") {
		t.Error("wrapper script should use source integrity metadata for source-weight readiness")
	}
	if !strings.Contains(script, "model.safetensors.index.json") {
		t.Error("wrapper script should validate sharded-model completeness before starting")
	}
	if !strings.Contains(script, "Rebuilt missing source integrity metadata") {
		t.Error("wrapper script should repair missing source integrity metadata for complete caches")
	}
	if !strings.Contains(script, "Waiting for source weights to finish downloading") {
		t.Error("wrapper script should log when abliteration is waiting for source weights")
	}
	if !strings.Contains(script, "missing_shards") {
		t.Error("wrapper script should log missing shard counts while waiting")
	}
	if !strings.Contains(script, "Download marker present but source weights are incomplete") {
		t.Error("wrapper script should fail fast when the completion marker exists but shards are missing")
	}
	if !strings.Contains(script, "ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP") {
		t.Error("wrapper script should support disabling transformers caching allocator warmup")
	}
	if !strings.Contains(script, "caching_allocator_warmup") {
		t.Error("wrapper script should patch transformers caching allocator warmup")
	}
	if !strings.Contains(script, "ABLITERATION_SAFE_SHARDED_LOAD") {
		t.Error("wrapper script should support gfx906 safe sharded load")
	}
	if !strings.Contains(script, "AutoModelForCausalLM.from_pretrained = _safe_sharded_from_pretrained") {
		t.Error("wrapper script should patch AutoModelForCausalLM.from_pretrained for gfx906")
	}
	if !strings.Contains(script, "Safe sharded load patch: constructing model from config") {
		t.Error("wrapper script should log model construction timing for safe sharded load")
	}
	if !strings.Contains(script, "Safe sharded load patch: sample gpu targets:") {
		t.Error("wrapper script should log sample GPU dispatch targets")
	}
	if !strings.Contains(script, "Safe sharded load patch: dispatch failed after") {
		t.Error("wrapper script should log dispatch failures with elapsed time")
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
	// 56 - 20 = 36, cap = 56*4/5 = 44 → 36
	if got := abliterationCPUMaxMemoryGB(56); got != 36 {
		t.Errorf("abliterationCPUMaxMemoryGB(56) = %d, want 36", got)
	}
	// 60 - 20 = 40, cap = 60*4/5 = 48 → 40
	if got := abliterationCPUMaxMemoryGB(60); got != 40 {
		t.Errorf("abliterationCPUMaxMemoryGB(60) = %d, want 40", got)
	}
	// 96 - 20 = 76, cap = 96*4/5 = 76 → 76
	if got := abliterationCPUMaxMemoryGB(96); got != 76 {
		t.Errorf("abliterationCPUMaxMemoryGB(96) = %d, want 76", got)
	}
	if got := abliterationGPUMaxMemoryGB(true, "gfx1100"); got != 20 {
		t.Errorf("abliterationGPUMaxMemoryGB(true, gfx1100) = %d, want 20", got)
	}
	if got := abliterationGPUMaxMemoryGB(true, "gfx906"); got != 14 {
		t.Errorf("abliterationGPUMaxMemoryGB(true, gfx906) = %d, want 14", got)
	}
	if got := abliterationGPUMaxMemoryGB(false, "gfx906"); got != 0 {
		t.Errorf("abliterationGPUMaxMemoryGB(false, gfx906) = %d, want 0", got)
	}
}

func TestAbliterationGPUMaxMemoryFromVRAMMB(t *testing.T) {
	cases := []struct {
		name   string
		vramMB int64
		want   int32
	}{
		{"unknown vram", 0, 0},
		{"negative vram", -1, 0},
		{"sub-gigabyte vram", 512, 0},
		{"gfx906 16GiB matches heuristic", 16384, 14},
		{"gfx1100 24GiB", 24576, 21},
		{"MI250 64GiB", 65536, 56},
		{"MI300X 192GiB", 196608, 168},
		{"tiny 6GiB reserves floor", 6144, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := abliterationGPUMaxMemoryFromVRAMMB(tc.vramMB); got != tc.want {
				t.Errorf("abliterationGPUMaxMemoryFromVRAMMB(%d) = %d, want %d", tc.vramMB, got, tc.want)
			}
		})
	}
}

// TestAbliterationEnv_VRAMDerivedGPUCap exercises the GPU-memory priority ladder:
// env var > GPUProfile maxGPUMemoryGB > VRAM-derived (flag-gated) > arch heuristic.
func TestAbliterationEnv_VRAMDerivedGPUCap(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{UseGPU: true}
	gpuCap := func(env []corev1.EnvVar) string {
		for _, e := range env {
			if e.Name == "ABLITERATION_GPU_MAX_MEMORY_GB" {
				return e.Value
			}
		}
		return ""
	}

	t.Run("flag off falls through to arch heuristic", func(t *testing.T) {
		memCfg := DefaultGPUMemoryConfig()
		memCfg.GPUVramMB = 65536 // would derive 56 if enabled
		env := abliterationEnv("m", "gfx1100", spec, memCfg)
		if got := gpuCap(env); got != "20" {
			t.Errorf("GPU cap = %q, want arch heuristic 20 when flag off", got)
		}
	})

	t.Run("flag on derives from vram when no explicit cap", func(t *testing.T) {
		t.Setenv("FLEXINFER_ABLIT_PROFILE_CAPS", "true")
		memCfg := DefaultGPUMemoryConfig()
		memCfg.GPUVramMB = 65536 // 64 GiB -> 56
		env := abliterationEnv("m", "gfx942", spec, memCfg)
		if got := gpuCap(env); got != "56" {
			t.Errorf("GPU cap = %q, want VRAM-derived 56", got)
		}
	})

	t.Run("explicit maxGPUMemoryGB wins over vram derivation", func(t *testing.T) {
		t.Setenv("FLEXINFER_ABLIT_PROFILE_CAPS", "true")
		memCfg := DefaultGPUMemoryConfig()
		memCfg.MaxGPUMemoryGB = 22
		memCfg.GPUVramMB = 24576
		env := abliterationEnv("m", "gfx1100", spec, memCfg)
		if got := gpuCap(env); got != "22" {
			t.Errorf("GPU cap = %q, want explicit profile cap 22", got)
		}
	})

	t.Run("env var override wins over everything", func(t *testing.T) {
		t.Setenv("FLEXINFER_ABLIT_PROFILE_CAPS", "true")
		t.Setenv("FLEXINFER_ABLITERATION_GPU_MAX_MEMORY_GB", "18")
		memCfg := DefaultGPUMemoryConfig()
		memCfg.MaxGPUMemoryGB = 22
		memCfg.GPUVramMB = 65536
		env := abliterationEnv("m", "gfx942", spec, memCfg)
		if got := gpuCap(env); got != "18" {
			t.Errorf("GPU cap = %q, want env override 18", got)
		}
	})

	t.Run("flag on but no vram falls through to heuristic", func(t *testing.T) {
		t.Setenv("FLEXINFER_ABLIT_PROFILE_CAPS", "true")
		memCfg := DefaultGPUMemoryConfig() // GPUVramMB = 0
		env := abliterationEnv("m", "gfx906", spec, memCfg)
		if got := gpuCap(env); got != "14" {
			t.Errorf("GPU cap = %q, want arch heuristic 14", got)
		}
	})
}

func TestResolveImage_Abliteration_EnvOverride(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "custom-registry.io/abliterator:v1")

	img := ResolveImage(ImageFormatAbliteration, "", "amd", "gfx1100")
	if img != "custom-registry.io/abliterator:v1" {
		t.Errorf("ResolveImage(Abliteration) = %q, want custom override", img)
	}
}

func TestResolveImage_Abliteration_UnifiedRuntime(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "should-not-use-this")

	img := ResolveImage(ImageFormatAbliteration, "", "amd", "gfx1100")
	if img != "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100" {
		t.Errorf("ResolveImage(Abliteration) = %q, want unified runtime image", img)
	}
}

func TestResolveImage_Abliteration_DefaultAMD(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "")

	img := ResolveImage(ImageFormatAbliteration, "", "amd", "gfx1100")
	if img != DefaultGPTQROCmImage {
		t.Errorf("ResolveImage(Abliteration, amd, gfx1100) = %q, want %q", img, DefaultGPTQROCmImage)
	}
}

func TestResolveImage_Abliteration_DefaultNvidia(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "")
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "")

	img := ResolveImage(ImageFormatAbliteration, "", "nvidia", "")
	if img != DefaultGPTQImage {
		t.Errorf("ResolveImage(Abliteration, nvidia) = %q, want %q", img, DefaultGPTQImage)
	}
}

func TestBuildAbliterationJob_GPUDriverMemoryInflation(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		MaxMemoryGB: ablitInt32Ptr(32),
		UseGPU:      true,
	}
	params := JobParams{
		Name:      "test-driver-mem",
		Namespace: "flexinfer-system",
		PVCName:   "test-pvc",
		ModelPath: "test-model",
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
		MemoryConfig: GPUMemoryConfig{
			GPUDriverMemoryMB: 12288, // 12 GiB
		},
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// memoryGB=32, driverOverhead=12288/1024=12 → schedulingMemoryGB=44
	memLimit := container.Resources.Limits["memory"]
	if memLimit.String() != "44Gi" {
		t.Errorf("memory limit = %q, want 44Gi (32 + 12 driver overhead)", memLimit.String())
	}

	// memoryRequestForLimitGB(44) = 44*4/5 = 35
	memReq := container.Resources.Requests["memory"]
	if memReq.String() != "35Gi" {
		t.Errorf("memory request = %q, want 35Gi", memReq.String())
	}
}

func TestBuildAbliterationJob_NoDriverMemoryOverhead(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		MaxMemoryGB: ablitInt32Ptr(32),
		UseGPU:      true,
	}
	params := JobParams{
		Name:      "test-no-driver",
		Namespace: "flexinfer-system",
		PVCName:   "test-pvc",
		ModelPath: "test-model",
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
		MemoryConfig: GPUMemoryConfig{
			GPUDriverMemoryMB: 0, // no overhead
		},
	}

	job, err := BuildAbliterationJob(params, spec)
	if err != nil {
		t.Fatalf("BuildAbliterationJob returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// No inflation: limit stays at 32
	memLimit := container.Resources.Limits["memory"]
	if memLimit.String() != "32Gi" {
		t.Errorf("memory limit = %q, want 32Gi (no driver overhead)", memLimit.String())
	}
}

func TestGPUMemoryConfigFromProfile_DriverMemory(t *testing.T) {
	driverMB := int32(12288)
	containerGB := int32(48)
	profile := &aiv1alpha2.GPUProfileSpec{
		ContainerMemoryGB: &containerGB,
		GPUDriverMemoryMB: &driverMB,
	}

	cfg := GPUMemoryConfigFromProfile(profile)
	if cfg.GPUDriverMemoryMB != 12288 {
		t.Errorf("GPUDriverMemoryMB = %d, want 12288", cfg.GPUDriverMemoryMB)
	}
	if cfg.ContainerMemoryGB != 48 {
		t.Errorf("ContainerMemoryGB = %d, want 48", cfg.ContainerMemoryGB)
	}
}

func TestGPUMemoryConfigFromProfile_NilDriverMemory(t *testing.T) {
	profile := &aiv1alpha2.GPUProfileSpec{}

	cfg := GPUMemoryConfigFromProfile(profile)
	if cfg.GPUDriverMemoryMB != 0 {
		t.Errorf("GPUDriverMemoryMB = %d, want 0 (default)", cfg.GPUDriverMemoryMB)
	}
}

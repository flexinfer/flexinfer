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

	// Memory default (56Gi)
	memReq := container.Resources.Requests["memory"]
	if memReq.String() != "56Gi" {
		t.Errorf("memory request = %q, want 56Gi", memReq.String())
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

	// Custom memory
	container := job.Spec.Template.Spec.Containers[0]
	memReq := container.Resources.Requests["memory"]
	if memReq.String() != "32Gi" {
		t.Errorf("memory request = %q, want 32Gi", memReq.String())
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

func TestBuildAbliterationScript_Content(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		NumSamples:       ablitInt32Ptr(64),
		TargetLayers:     ablitStringPtr("10-55"),
		WeightMatrices:   []string{"o_proj", "down_proj"},
		UseGPU:           true,
		SkipVisionLayers: ablitBoolPtr(true),
	}

	script := buildAbliterationScript("my-model", spec)

	// Check key content
	checks := []struct {
		name    string
		content string
	}{
		{"model dir", `MODEL_DIR="/cache/my-model"`},
		{"num samples", "NUM_SAMPLES=64"},
		{"target layers", `TARGET_LAYERS="10-55"`},
		{"weight matrices", `WEIGHT_MATRICES="o_proj,down_proj"`},
		{"skip vision", `SKIP_VISION="true"`},
		{"device map auto", `DEVICE_MAP="auto"`},
		{"python script", "from transformers import AutoModelForCausalLM"},
		{"harmful prompts", "HARMFUL_PROMPTS"},
		{"harmless prompts", "HARMLESS_PROMPTS"},
		{"termination log", "/dev/termination-log"},
		{"save pretrained", "model.save_pretrained"},
		{"orthogonalize", "torch.outer(proj, d)"},
		{"abliteration status", ".abliteration-status.json"},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if !strings.Contains(script, check.content) {
				t.Errorf("script missing %q", check.content)
			}
		})
	}
}

func TestBuildAbliterationScript_CPUMode(t *testing.T) {
	spec := &aiv1alpha1.AbliterationSpec{
		UseGPU: false,
	}

	script := buildAbliterationScript("test-model", spec)

	if !strings.Contains(script, `DEVICE_MAP="cpu"`) {
		t.Error("script should use device_map=cpu when UseGPU=false")
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

func TestContrastivePrompts(t *testing.T) {
	prompts := contrastivePrompts()

	if !strings.Contains(prompts, "HARMFUL_PROMPTS") {
		t.Error("contrastivePrompts() missing HARMFUL_PROMPTS")
	}
	if !strings.Contains(prompts, "HARMLESS_PROMPTS") {
		t.Error("contrastivePrompts() missing HARMLESS_PROMPTS")
	}

	// Both lists should have 128 entries
	harmfulCount := strings.Count(prompts, `"How do I`)
	if harmfulCount < 100 {
		t.Errorf("expected at least 100 prompt entries, got %d", harmfulCount)
	}
}

func TestAbliterationImage_EnvOverride(t *testing.T) {
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "custom-registry.io/abliterator:v1")
	defer t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "")

	img := abliterationImage("amd", "gfx1100")
	if img != "custom-registry.io/abliterator:v1" {
		t.Errorf("abliterationImage = %q, want custom override", img)
	}
}

func TestAbliterationImage_DefaultAMD(t *testing.T) {
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "")

	img := abliterationImage("amd", "gfx1100")
	if img != DefaultGPTQROCmImage {
		t.Errorf("abliterationImage(amd, gfx1100) = %q, want %q", img, DefaultGPTQROCmImage)
	}
}

func TestAbliterationImage_DefaultNvidia(t *testing.T) {
	t.Setenv("FLEXINFER_ABLITERATOR_IMAGE", "")

	img := abliterationImage("nvidia", "")
	if img != DefaultGPTQImage {
		t.Errorf("abliterationImage(nvidia) = %q, want %q", img, DefaultGPTQImage)
	}
}

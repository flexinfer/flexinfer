package quantization

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestIsValidGGUFType(t *testing.T) {
	tests := []struct {
		ggufType string
		want     bool
	}{
		{"Q4_K_M", true},
		{"Q5_K_M", true},
		{"Q8_0", true},
		{"Q2_K", true},
		{"Q3_K_S", true},
		{"Q6_K", true},
		{"invalid", false},
		{"q4_k_m", false}, // case sensitive
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ggufType, func(t *testing.T) {
			if got := IsValidGGUFType(tt.ggufType); got != tt.want {
				t.Errorf("IsValidGGUFType(%q) = %v, want %v", tt.ggufType, got, tt.want)
			}
		})
	}
}

func TestGetBuilder(t *testing.T) {
	// GGUF should return a builder
	builder, err := GetBuilder(aiv1alpha1.QuantizationFormatGGUF)
	if err != nil {
		t.Fatalf("GetBuilder(GGUF) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha1.QuantizationFormatGGUF {
		t.Errorf("builder.Format() = %v, want GGUF", builder.Format())
	}

	// AWQ should return a builder
	builder, err = GetBuilder(aiv1alpha1.QuantizationFormatAWQ)
	if err != nil {
		t.Fatalf("GetBuilder(AWQ) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha1.QuantizationFormatAWQ {
		t.Errorf("builder.Format() = %v, want AWQ", builder.Format())
	}

	// GPTQ should return a builder
	builder, err = GetBuilder(aiv1alpha1.QuantizationFormatGPTQ)
	if err != nil {
		t.Fatalf("GetBuilder(GPTQ) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha1.QuantizationFormatGPTQ {
		t.Errorf("builder.Format() = %v, want GPTQ", builder.Format())
	}

	// EXL2 should return a builder
	builder, err = GetBuilder(aiv1alpha1.QuantizationFormatEXL2)
	if err != nil {
		t.Fatalf("GetBuilder(EXL2) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha1.QuantizationFormatEXL2 {
		t.Errorf("builder.Format() = %v, want EXL2", builder.Format())
	}

	// FP8 should return a builder
	builder, err = GetBuilder(aiv1alpha1.QuantizationFormatFP8)
	if err != nil {
		t.Fatalf("GetBuilder(FP8) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha1.QuantizationFormatFP8 {
		t.Errorf("builder.Format() = %v, want FP8", builder.Format())
	}

	// Unknown format should remain unsupported.
	_, err = GetBuilder(aiv1alpha1.QuantizationFormat("INVALID"))
	if err == nil {
		t.Error("GetBuilder(INVALID) should return error for unimplemented format")
	}
}

func TestGGUFJobBuilder_Validate(t *testing.T) {
	builder := &GGUFJobBuilder{}

	tests := []struct {
		name    string
		spec    *aiv1alpha1.QuantizationSpec
		wantErr bool
	}{
		{
			name: "valid GGUF with Q4_K_M",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:   aiv1alpha1.QuantizationFormatGGUF,
				GGUFType: "Q4_K_M",
			},
			wantErr: false,
		},
		{
			name: "valid GGUF with empty type (uses default)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGGUF,
			},
			wantErr: false,
		},
		{
			name: "invalid format for GGUF builder",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatAWQ,
			},
			wantErr: true,
		},
		{
			name: "invalid GGUF type",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:   aiv1alpha1.QuantizationFormatGGUF,
				GGUFType: "Q99_INVALID",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := builder.Validate(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGGUFJobBuilder_BuildJob(t *testing.T) {
	builder := &GGUFJobBuilder{}

	params := JobParams{
		Name:      "llama3-8b",
		Namespace: "flexinfer-system",
		PVCName:   "llama3-8b",
		ModelPath: "llama3-8b",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format:   aiv1alpha1.QuantizationFormatGGUF,
			GGUFType: "Q4_K_M",
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	// Verify job metadata
	if job.Name != "llama3-8b-quantize" {
		t.Errorf("job.Name = %q, want %q", job.Name, "llama3-8b-quantize")
	}
	if job.Namespace != "flexinfer-system" {
		t.Errorf("job.Namespace = %q, want %q", job.Namespace, "flexinfer-system")
	}

	// Verify labels
	if job.Labels["flexinfer.ai/format"] != "GGUF" {
		t.Errorf("label flexinfer.ai/format = %q, want %q", job.Labels["flexinfer.ai/format"], "GGUF")
	}
	if job.Labels["flexinfer.ai/cache"] != "llama3-8b" {
		t.Errorf("label flexinfer.ai/cache = %q, want %q", job.Labels["flexinfer.ai/cache"], "llama3-8b")
	}

	// Verify pod template
	podSpec := job.Spec.Template.Spec
	if len(podSpec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(podSpec.Containers))
	}

	container := podSpec.Containers[0]
	if container.Name != "quantizer" {
		t.Errorf("container.Name = %q, want %q", container.Name, "quantizer")
	}
	if len(container.Args) == 0 {
		t.Fatal("expected quantizer container args to include the shell script")
	}
	script := container.Args[0]
	if !contains(script, "/dev/termination-log") {
		t.Error("script should write quantization metadata to /dev/termination-log")
	}
	if !contains(script, "quantizationTimeSeconds") {
		t.Error("script should include quantizationTimeSeconds metadata")
	}

	// Verify volumes (PVC + workspace)
	if len(podSpec.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(podSpec.Volumes))
	}
	if podSpec.Volumes[0].PersistentVolumeClaim == nil {
		t.Error("first volume should be a PVC")
	}
	if podSpec.Volumes[0].PersistentVolumeClaim.ClaimName != "llama3-8b" {
		t.Errorf("PVC name = %q, want %q", podSpec.Volumes[0].PersistentVolumeClaim.ClaimName, "llama3-8b")
	}
	if podSpec.Volumes[1].EmptyDir == nil {
		t.Error("second volume should be an emptyDir")
	}

	// Verify volume mounts
	if len(container.VolumeMounts) != 2 {
		t.Fatalf("expected 2 volume mounts, got %d", len(container.VolumeMounts))
	}
	if container.VolumeMounts[0].MountPath != "/cache" {
		t.Errorf("first mount path = %q, want /cache", container.VolumeMounts[0].MountPath)
	}
	if container.VolumeMounts[1].MountPath != "/workspace" {
		t.Errorf("second mount path = %q, want /workspace", container.VolumeMounts[1].MountPath)
	}

	// Verify deadline
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != DefaultActiveDeadlineSeconds {
		t.Errorf("ActiveDeadlineSeconds = %v, want %d", job.Spec.ActiveDeadlineSeconds, DefaultActiveDeadlineSeconds)
	}

	// Verify restart policy
	if podSpec.RestartPolicy != "Never" {
		t.Errorf("RestartPolicy = %q, want %q", podSpec.RestartPolicy, "Never")
	}
}

func TestGGUFJobBuilder_BuildJob_DefaultType(t *testing.T) {
	builder := &GGUFJobBuilder{}

	params := JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-model",
		ModelPath: "test-model",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatGGUF,
			// GGUFType is empty — should default to Q4_K_M
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	// The script should contain the default type
	script := job.Spec.Template.Spec.Containers[0].Args[0]
	if !contains(script, "Q4_K_M") {
		t.Error("script should contain default GGUF type Q4_K_M")
	}
}

func TestGGUFJobBuilder_BuildJob_CustomMemory(t *testing.T) {
	builder := &GGUFJobBuilder{}
	maxMem := int32(64)

	params := JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-model",
		ModelPath: "test-model",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format:      aiv1alpha1.QuantizationFormatGGUF,
			GGUFType:    "Q5_K_M",
			MaxMemoryGB: &maxMem,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	memLimit := container.Resources.Limits.Memory()
	if memLimit.String() != "64Gi" {
		t.Errorf("memory limit = %q, want %q", memLimit.String(), "64Gi")
	}
}

func TestAWQJobBuilder_Validate(t *testing.T) {
	builder := &AWQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)

	valid := &aiv1alpha1.QuantizationSpec{
		Format:    aiv1alpha1.QuantizationFormatAWQ,
		Bits:      &bits,
		GroupSize: &groupSize,
		UseGPU:    true,
	}
	if err := builder.Validate(valid); err != nil {
		t.Fatalf("Validate(valid AWQ) returned error: %v", err)
	}

	invalidBits := int32(8)
	invalidSpec := &aiv1alpha1.QuantizationSpec{
		Format:    aiv1alpha1.QuantizationFormatAWQ,
		Bits:      &invalidBits,
		GroupSize: &groupSize,
		UseGPU:    true,
	}
	if err := builder.Validate(invalidSpec); err == nil {
		t.Fatal("Validate(invalid AWQ bits) should return error")
	}
}

func TestAWQJobBuilder_BuildJob(t *testing.T) {
	builder := &AWQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	params := JobParams{
		Name:      "llama3-awq",
		Namespace: "flexinfer-system",
		PVCName:   "llama3-awq",
		ModelPath: "llama3-awq",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format:    aiv1alpha1.QuantizationFormatAWQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != DefaultAWQImage {
		t.Fatalf("container.Image = %q, want %q", container.Image, DefaultAWQImage)
	}
	gpuResource := corev1.ResourceName("nvidia.com/gpu")
	gpuLimit, ok := container.Resources.Limits[gpuResource]
	if !ok {
		t.Fatal("expected GPU limit to be set")
	}
	if gpuLimit.Cmp(resource.MustParse("1")) != 0 {
		t.Fatalf("GPU limit = %q, want 1", gpuLimit.String())
	}
	script := container.Args[0]
	if !contains(script, "AutoAWQForCausalLM") {
		t.Fatal("expected AWQ script to reference AutoAWQForCausalLM")
	}
	if !contains(script, "/dev/termination-log") {
		t.Fatal("expected AWQ script to write termination metadata")
	}
}

func TestAWQJobBuilder_BuildJob_AMDVendor(t *testing.T) {
	builder := &AWQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	params := JobParams{
		Name:      "qwen3-awq-amd",
		Namespace: "flexinfer-system",
		PVCName:   "qwen3-awq-amd",
		ModelPath: "qwen3-awq-amd",
		GPUVendor: "amd",
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": "gpu-node",
		},
		Spec: &aiv1alpha1.QuantizationSpec{
			Format:    aiv1alpha1.QuantizationFormatAWQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	amdGPU := corev1.ResourceName("amd.com/gpu")
	gpuLimit, ok := container.Resources.Limits[amdGPU]
	if !ok {
		t.Fatal("expected amd.com/gpu limit to be set")
	}
	if gpuLimit.Cmp(resource.MustParse("1")) != 0 {
		t.Fatalf("GPU limit = %q, want 1", gpuLimit.String())
	}

	// nvidia.com/gpu should NOT be present
	nvidiaGPU := corev1.ResourceName("nvidia.com/gpu")
	if _, ok := container.Resources.Limits[nvidiaGPU]; ok {
		t.Fatal("nvidia.com/gpu should not be set for AMD vendor")
	}

	// NodeSelector should be propagated
	podSpec := job.Spec.Template.Spec
	if podSpec.NodeSelector == nil || podSpec.NodeSelector["kubernetes.io/hostname"] != "gpu-node" {
		t.Fatalf("NodeSelector not propagated, got %v", podSpec.NodeSelector)
	}

	script := container.Args[0]
	if !contains(script, "device_map=None") {
		t.Fatal("expected AWQ script to use device_map=None for ROCm compatibility")
	}
	if !contains(script, `"version": "GEMM"`) {
		t.Fatal("expected AWQ script to use version GEMM for native AWQ format")
	}
}

func TestGPTQJobBuilder_BuildJob(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(8)
	groupSize := int32(128)
	params := JobParams{
		Name:      "llama3-gptq",
		Namespace: "flexinfer-system",
		PVCName:   "llama3-gptq",
		ModelPath: "llama3-gptq",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format:    aiv1alpha1.QuantizationFormatGPTQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != DefaultGPTQImage {
		t.Fatalf("container.Image = %q, want %q", container.Image, DefaultGPTQImage)
	}
	script := container.Args[0]
	if !contains(script, "AutoGPTQForCausalLM") {
		t.Fatal("expected GPTQ script to reference AutoGPTQForCausalLM")
	}
	if !contains(script, "W${BITS}_G${GROUP_SIZE}") {
		t.Fatal("expected GPTQ script type marker")
	}
}

func TestEXL2JobBuilder_Validate(t *testing.T) {
	builder := &EXL2JobBuilder{}
	bits := int32(4)

	valid := &aiv1alpha1.QuantizationSpec{
		Format: aiv1alpha1.QuantizationFormatEXL2,
		Bits:   &bits,
		UseGPU: true,
	}
	if err := builder.Validate(valid); err != nil {
		t.Fatalf("Validate(valid EXL2) returned error: %v", err)
	}

	invalidBits := int32(7)
	invalidSpec := &aiv1alpha1.QuantizationSpec{
		Format: aiv1alpha1.QuantizationFormatEXL2,
		Bits:   &invalidBits,
		UseGPU: true,
	}
	if err := builder.Validate(invalidSpec); err == nil {
		t.Fatal("Validate(invalid EXL2 bits) should return error")
	}
}

func TestEXL2JobBuilder_BuildJob(t *testing.T) {
	builder := &EXL2JobBuilder{}
	bits := int32(5)
	params := JobParams{
		Name:      "llama3-exl2",
		Namespace: "flexinfer-system",
		PVCName:   "llama3-exl2",
		ModelPath: "llama3-exl2",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatEXL2,
			Bits:   &bits,
			UseGPU: true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != DefaultEXL2Image {
		t.Fatalf("container.Image = %q, want %q", container.Image, DefaultEXL2Image)
	}
	script := container.Args[0]
	if !contains(script, "EXL2 Quantization") {
		t.Fatal("expected EXL2 script banner")
	}
	if !contains(script, "convert.py") {
		t.Fatal("expected EXL2 script to invoke convert.py")
	}
	if !contains(script, "EXL2_B${BITS}") {
		t.Fatal("expected EXL2 script type marker")
	}
	if !contains(script, "/dev/termination-log") {
		t.Fatal("expected EXL2 script to write termination metadata")
	}
}

func TestFP8JobBuilder_Validate(t *testing.T) {
	builder := &FP8JobBuilder{}
	bits := int32(8)

	valid := &aiv1alpha1.QuantizationSpec{
		Format: aiv1alpha1.QuantizationFormatFP8,
		Bits:   &bits,
		UseGPU: true,
	}
	if err := builder.Validate(valid); err != nil {
		t.Fatalf("Validate(valid FP8) returned error: %v", err)
	}

	invalidBits := int32(4)
	invalidSpec := &aiv1alpha1.QuantizationSpec{
		Format: aiv1alpha1.QuantizationFormatFP8,
		Bits:   &invalidBits,
		UseGPU: true,
	}
	if err := builder.Validate(invalidSpec); err == nil {
		t.Fatal("Validate(invalid FP8 bits) should return error")
	}
}

func TestFP8JobBuilder_BuildJob(t *testing.T) {
	builder := &FP8JobBuilder{}
	bits := int32(8)
	params := JobParams{
		Name:      "llama3-fp8",
		Namespace: "flexinfer-system",
		PVCName:   "llama3-fp8",
		ModelPath: "llama3-fp8",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatFP8,
			Bits:   &bits,
			UseGPU: true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != DefaultFP8Image {
		t.Fatalf("container.Image = %q, want %q", container.Image, DefaultFP8Image)
	}
	script := container.Args[0]
	if !contains(script, "FP8 Quantization") {
		t.Fatal("expected FP8 script banner")
	}
	if !contains(script, "FP8_B${BITS}") {
		t.Fatal("expected FP8 script type marker")
	}
	if !contains(script, "--dtype") || !contains(script, "fp8") {
		t.Fatal("expected FP8 script dtype arguments")
	}
	if !contains(script, "/dev/termination-log") {
		t.Fatal("expected FP8 script to write termination metadata")
	}
}

func TestFormatBackendCompatibility(t *testing.T) {
	// GGUF should be compatible with llamacpp and ollama
	ggufBackends := FormatBackendCompatibility[aiv1alpha1.QuantizationFormatGGUF]
	if !containsStr(ggufBackends, "llamacpp") {
		t.Error("GGUF should be compatible with llamacpp")
	}
	if !containsStr(ggufBackends, "ollama") {
		t.Error("GGUF should be compatible with ollama")
	}

	// AWQ should be compatible with vllm
	awqBackends := FormatBackendCompatibility[aiv1alpha1.QuantizationFormatAWQ]
	if !containsStr(awqBackends, "vllm") {
		t.Error("AWQ should be compatible with vllm")
	}
}

func TestRecommendSpec(t *testing.T) {
	t.Run("amd gfx1100 defaults to GGUF", func(t *testing.T) {
		rec := RecommendSpec(RecommendationInput{
			Source: "HF://mlc-ai/Qwen3-8B-Instruct",
			NodeSelector: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
			},
		})
		if rec.Spec == nil {
			t.Fatal("recommendation spec is nil")
		}
		if rec.Spec.Format != aiv1alpha1.QuantizationFormatGGUF {
			t.Fatalf("Format = %q, want GGUF", rec.Spec.Format)
		}
		if rec.Spec.GGUFType != DefaultGGUFType {
			t.Fatalf("GGUFType = %q, want %q", rec.Spec.GGUFType, DefaultGGUFType)
		}
		if rec.GPUVendor != "AMD" {
			t.Fatalf("GPUVendor = %q, want AMD", rec.GPUVendor)
		}
		if rec.GPUArchitecture != "gfx1100" {
			t.Fatalf("GPUArchitecture = %q, want gfx1100", rec.GPUArchitecture)
		}
	})

	t.Run("large model on AMD prefers tighter GGUF type", func(t *testing.T) {
		rec := RecommendSpec(RecommendationInput{
			Source: "HF://Qwen/Qwen3-32B-Instruct",
			NodeSelector: map[string]string{
				"amd.com/gpu.arch": "gfx1100",
			},
		})
		if rec.Spec == nil {
			t.Fatal("recommendation spec is nil")
		}
		if rec.Spec.Format != aiv1alpha1.QuantizationFormatGGUF {
			t.Fatalf("Format = %q, want GGUF", rec.Spec.Format)
		}
		if rec.Spec.GGUFType != "Q3_K_M" {
			t.Fatalf("GGUFType = %q, want Q3_K_M", rec.Spec.GGUFType)
		}
	})

	t.Run("nvidia sm89 prefers AWQ", func(t *testing.T) {
		rec := RecommendSpec(RecommendationInput{
			Source: "huggingface://meta-llama/Llama-3-8B-Instruct",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "8",
				"nvidia.com/gpu.compute.minor": "9",
			},
		})
		if rec.Spec == nil {
			t.Fatal("recommendation spec is nil")
		}
		if rec.Spec.Format != aiv1alpha1.QuantizationFormatAWQ {
			t.Fatalf("Format = %q, want AWQ", rec.Spec.Format)
		}
		if rec.Spec.Bits == nil || *rec.Spec.Bits != int32(DefaultAWQBits) {
			t.Fatalf("Bits = %v, want %d", rec.Spec.Bits, DefaultAWQBits)
		}
		if rec.Spec.GroupSize == nil || *rec.Spec.GroupSize != int32(DefaultQuantizationGroupSize) {
			t.Fatalf("GroupSize = %v, want %d", rec.Spec.GroupSize, DefaultQuantizationGroupSize)
		}
		if !rec.Spec.UseGPU {
			t.Fatal("UseGPU = false, want true")
		}
	})

	t.Run("nvidia sm90 prefers FP8", func(t *testing.T) {
		rec := RecommendSpec(RecommendationInput{
			Source: "huggingface://meta-llama/Llama-3-70B",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "9",
				"nvidia.com/gpu.compute.minor": "0",
			},
		})
		if rec.Spec == nil {
			t.Fatal("recommendation spec is nil")
		}
		if rec.Spec.Format != aiv1alpha1.QuantizationFormatFP8 {
			t.Fatalf("Format = %q, want FP8", rec.Spec.Format)
		}
		if rec.Spec.Bits == nil || *rec.Spec.Bits != int32(DefaultFP8Bits) {
			t.Fatalf("Bits = %v, want %d", rec.Spec.Bits, DefaultFP8Bits)
		}
		if !rec.Spec.UseGPU {
			t.Fatal("UseGPU = false, want true")
		}
	})

	t.Run("maxwell falls back to GGUF", func(t *testing.T) {
		rec := RecommendSpec(RecommendationInput{
			Source: "huggingface://meta-llama/Llama-2-7B",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "Maxwell",
			},
		})
		if rec.Spec == nil {
			t.Fatal("recommendation spec is nil")
		}
		if rec.Spec.Format != aiv1alpha1.QuantizationFormatGGUF {
			t.Fatalf("Format = %q, want GGUF", rec.Spec.Format)
		}
		if rec.Spec.GGUFType != "Q3_K_M" {
			t.Fatalf("GGUFType = %q, want Q3_K_M", rec.Spec.GGUFType)
		}
	})
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && containsStr([]string{s}, substr)
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
		// For substring matching in scripts
		if len(v) > len(s) {
			for i := 0; i <= len(v)-len(s); i++ {
				if v[i:i+len(s)] == s {
					return true
				}
			}
		}
	}
	return false
}

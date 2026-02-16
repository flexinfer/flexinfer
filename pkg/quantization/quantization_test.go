package quantization

import (
	"testing"

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

	// AWQ should return an error (not yet implemented)
	_, err = GetBuilder(aiv1alpha1.QuantizationFormatAWQ)
	if err == nil {
		t.Error("GetBuilder(AWQ) should return error for unimplemented format")
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

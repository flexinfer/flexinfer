package quantization

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
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
	builder, err := GetBuilder(aiv1alpha2.QuantizationFormatGGUF)
	if err != nil {
		t.Fatalf("GetBuilder(GGUF) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha2.QuantizationFormatGGUF {
		t.Errorf("builder.Format() = %v, want GGUF", builder.Format())
	}

	// AWQ should return a builder
	builder, err = GetBuilder(aiv1alpha2.QuantizationFormatAWQ)
	if err != nil {
		t.Fatalf("GetBuilder(AWQ) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha2.QuantizationFormatAWQ {
		t.Errorf("builder.Format() = %v, want AWQ", builder.Format())
	}

	// GPTQ should return a builder
	builder, err = GetBuilder(aiv1alpha2.QuantizationFormatGPTQ)
	if err != nil {
		t.Fatalf("GetBuilder(GPTQ) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha2.QuantizationFormatGPTQ {
		t.Errorf("builder.Format() = %v, want GPTQ", builder.Format())
	}

	// EXL2 should return a builder
	builder, err = GetBuilder(aiv1alpha2.QuantizationFormatEXL2)
	if err != nil {
		t.Fatalf("GetBuilder(EXL2) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha2.QuantizationFormatEXL2 {
		t.Errorf("builder.Format() = %v, want EXL2", builder.Format())
	}

	// FP8 should return a builder
	builder, err = GetBuilder(aiv1alpha2.QuantizationFormatFP8)
	if err != nil {
		t.Fatalf("GetBuilder(FP8) returned error: %v", err)
	}
	if builder.Format() != aiv1alpha2.QuantizationFormatFP8 {
		t.Errorf("builder.Format() = %v, want FP8", builder.Format())
	}

	// Unknown format should remain unsupported.
	_, err = GetBuilder(aiv1alpha2.QuantizationFormat("INVALID"))
	if err == nil {
		t.Error("GetBuilder(INVALID) should return error for unimplemented format")
	}
}

func TestGGUFJobBuilder_Validate(t *testing.T) {
	builder := &GGUFJobBuilder{}

	tests := []struct {
		name    string
		spec    *aiv1alpha2.QuantizationSpec
		wantErr bool
	}{
		{
			name: "valid GGUF with Q4_K_M",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:   aiv1alpha2.QuantizationFormatGGUF,
				GGUFType: "Q4_K_M",
			},
			wantErr: false,
		},
		{
			name: "valid GGUF with empty type (uses default)",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatGGUF,
			},
			wantErr: false,
		},
		{
			name: "invalid format for GGUF builder",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatAWQ,
			},
			wantErr: true,
		},
		{
			name: "invalid GGUF type",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:   aiv1alpha2.QuantizationFormatGGUF,
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:   aiv1alpha2.QuantizationFormatGGUF,
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
	if !contains(script, "quantize_gguf.sh") {
		t.Error("expected GGUF wrapper script to invoke quantize_gguf.sh")
	}
	// Verify env vars are set correctly
	env := containerEnvMap(container.Env)
	if env["MODEL_DIR"] != "/cache/llama3-8b" {
		t.Errorf("MODEL_DIR env = %q, want /cache/llama3-8b", env["MODEL_DIR"])
	}
	if env["GGUF_TYPE"] != "Q4_K_M" {
		t.Errorf("GGUF_TYPE env = %q, want Q4_K_M", env["GGUF_TYPE"])
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatGGUF,
			// GGUFType is empty — should default to Q4_K_M
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	// Default type should be set in env var
	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["GGUF_TYPE"] != "Q4_K_M" {
		t.Errorf("GGUF_TYPE env = %q, want Q4_K_M", env["GGUF_TYPE"])
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:      aiv1alpha2.QuantizationFormatGGUF,
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

	valid := &aiv1alpha2.QuantizationSpec{
		Format:    aiv1alpha2.QuantizationFormatAWQ,
		Bits:      &bits,
		GroupSize: &groupSize,
		UseGPU:    true,
	}
	if err := builder.Validate(valid); err != nil {
		t.Fatalf("Validate(valid AWQ) returned error: %v", err)
	}

	invalidBits := int32(8)
	invalidSpec := &aiv1alpha2.QuantizationSpec{
		Format:    aiv1alpha2.QuantizationFormatAWQ,
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatAWQ,
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
	if !contains(script, "quantize_awq.py") {
		t.Fatal("expected AWQ wrapper script to invoke quantize_awq.py")
	}
	if !contains(script, "/dev/termination-log") {
		t.Fatal("expected AWQ wrapper script to write termination metadata")
	}
	env := containerEnvMap(container.Env)
	if env["BITS"] != "4" {
		t.Errorf("BITS env = %q, want 4", env["BITS"])
	}
	if env["GROUP_SIZE"] != "128" {
		t.Errorf("GROUP_SIZE env = %q, want 128", env["GROUP_SIZE"])
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
		Tolerations: []corev1.Toleration{
			{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			},
		},
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatAWQ,
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

	// Tolerations should be propagated
	if len(podSpec.Tolerations) != 1 {
		t.Fatalf("expected 1 toleration, got %d", len(podSpec.Tolerations))
	}
	if podSpec.Tolerations[0].Key != "dedicated" || podSpec.Tolerations[0].Value != "gpu" {
		t.Fatalf("unexpected toleration: %+v", podSpec.Tolerations[0])
	}

	// PYTORCH_ALLOC_CONF should be set for AMD GPUs.
	var allocConf string
	for _, e := range container.Env {
		if e.Name == "PYTORCH_ALLOC_CONF" {
			allocConf = e.Value
		}
	}
	if allocConf != rocmAllocatorConfig {
		t.Fatalf("PYTORCH_ALLOC_CONF = %q, want %q", allocConf, rocmAllocatorConfig)
	}

	script := container.Args[0]
	if !contains(script, "quantize_awq.py") {
		t.Fatal("expected AWQ wrapper script to invoke quantize_awq.py")
	}
	env := containerEnvMap(container.Env)
	if env["MODEL_DIR"] != "/cache/qwen3-awq-amd" {
		t.Errorf("MODEL_DIR env = %q, want /cache/qwen3-awq-amd", env["MODEL_DIR"])
	}
}

func TestGGUFJobBuilder_BuildJob_Tolerations(t *testing.T) {
	builder := &GGUFJobBuilder{}
	params := JobParams{
		Name:      "test-model",
		Namespace: "flexinfer-system",
		PVCName:   "test-model",
		ModelPath: "test-model",
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": "gpu-node",
		},
		Tolerations: []corev1.Toleration{
			{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			},
		},
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:   aiv1alpha2.QuantizationFormatGGUF,
			GGUFType: "Q4_K_M",
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	podSpec := job.Spec.Template.Spec

	if podSpec.NodeSelector == nil || podSpec.NodeSelector["kubernetes.io/hostname"] != "gpu-node" {
		t.Fatalf("NodeSelector not propagated, got %v", podSpec.NodeSelector)
	}

	if len(podSpec.Tolerations) != 1 {
		t.Fatalf("expected 1 toleration, got %d", len(podSpec.Tolerations))
	}
	if podSpec.Tolerations[0].Key != "dedicated" || podSpec.Tolerations[0].Value != "gpu" {
		t.Fatalf("unexpected toleration: %+v", podSpec.Tolerations[0])
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
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
	memReq := container.Resources.Requests.Memory()
	if memReq.String() != "38Gi" {
		t.Fatalf("memory request = %q, want %q", memReq.String(), "38Gi")
	}
	script := container.Args[0]
	if !contains(script, "quantize_gptq.py") {
		t.Fatal("expected GPTQ wrapper script to invoke quantize_gptq.py")
	}
	if !contains(script, "W${BITS}_G${GROUP_SIZE}") {
		t.Fatal("expected GPTQ wrapper script type marker")
	}
	// Default sym=True, descAct=False via env vars
	env := containerEnvMap(container.Env)
	if env["SYM"] != "True" {
		t.Fatalf("SYM env = %q, want True", env["SYM"])
	}
	if env["DESC_ACT"] != "False" {
		t.Fatalf("DESC_ACT env = %q, want False", env["DESC_ACT"])
	}
}

func TestGPTQJobBuilder_BuildJob_SymFalse(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	sym := false
	descAct := true
	params := JobParams{
		Name:      "qwen3-gptq",
		Namespace: "flexinfer-system",
		PVCName:   "qwen3-gptq",
		ModelPath: "qwen3-gptq",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
			Sym:       &sym,
			DescAct:   &descAct,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["SYM"] != "False" {
		t.Fatalf("SYM env = %q, want False", env["SYM"])
	}
	if env["DESC_ACT"] != "True" {
		t.Fatalf("DESC_ACT env = %q, want True", env["DESC_ACT"])
	}
}

func TestGPTQJobBuilder_BuildJob_AMDVendor(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	params := JobParams{
		Name:      "qwen3-gptq-amd",
		Namespace: "flexinfer-system",
		PVCName:   "qwen3-gptq-amd",
		ModelPath: "qwen3-gptq-amd",
		GPUVendor: "amd",
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": "gpu-node",
		},
		Tolerations: []corev1.Toleration{
			{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			},
		},
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
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

	// ROCm image should be selected for AMD vendor
	if container.Image != DefaultGPTQROCmImage {
		t.Fatalf("container.Image = %q, want %q", container.Image, DefaultGPTQROCmImage)
	}

	// amd.com/gpu should be set
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

	// PYTORCH_ALLOC_CONF should be set for AMD GPUs.
	var allocConf string
	for _, e := range container.Env {
		if e.Name == "PYTORCH_ALLOC_CONF" {
			allocConf = e.Value
		}
	}
	if allocConf != rocmAllocatorConfig {
		t.Fatalf("PYTORCH_ALLOC_CONF = %q, want %q", allocConf, rocmAllocatorConfig)
	}

	script := container.Args[0]
	if !contains(script, "quantize_gptq.py") {
		t.Fatal("expected GPTQ wrapper script to invoke quantize_gptq.py")
	}
}

func TestGPTQJobBuilder_BuildJob_AMDVendor_GFX906(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	params := JobParams{
		Name:      "qwen35-27b-gptq-gfx906",
		Namespace: "flexinfer-system",
		PVCName:   "qwen35-27b-gptq-gfx906",
		ModelPath: "qwen35-27b-gptq-gfx906",
		GPUVendor: "amd",
		GPUArch:   "gfx906",
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": "cblevins-radeonvii",
		},
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
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

	// gfx906 image should be selected
	if container.Image != DefaultGPTQROCmGFX906Image {
		t.Fatalf("container.Image = %q, want %q", container.Image, DefaultGPTQROCmGFX906Image)
	}
	env := containerEnvMap(container.Env)
	if env["QUANTIZE_DEVICE_MAP"] != "cpu" {
		t.Fatalf("QUANTIZE_DEVICE_MAP = %q, want cpu", env["QUANTIZE_DEVICE_MAP"])
	}

	// amd.com/gpu should be set
	amdGPU := corev1.ResourceName("amd.com/gpu")
	if _, ok := container.Resources.Limits[amdGPU]; !ok {
		t.Fatal("expected amd.com/gpu limit to be set")
	}

	script := container.Args[0]
	if !contains(script, "Skipping torchao on gfx906/gfx900; wheel triggers SIGILL on older x86 hosts") {
		t.Fatal("expected gfx906 wrapper script to skip torchao imports on Broadwell-class hosts")
	}
	if !contains(script, "python3 -m pip uninstall -y pypcre") {
		t.Fatal("expected gfx906 wrapper script to remove the crashing pypcre wheel")
	}
	if !contains(script, "cat > /tmp/pcre.py") {
		t.Fatal("expected gfx906 wrapper script to inject a stdlib-backed pcre shim")
	}
	if !contains(script, "GPTQ_PY_IMPORTS=\"import tokenicer; from gptqmodel import GPTQModel, QuantizeConfig\"") {
		t.Fatal("expected gfx906 wrapper script to verify the GPTQModel API import path")
	}
}

func TestGPTQJobBuilder_BuildJob_AMDVendor_GFX1100_CPUDeviceMap(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	params := JobParams{
		Name:      "qwen35-27b-gptq-gfx1100",
		Namespace: "flexinfer-system",
		PVCName:   "qwen35-27b-gptq-gfx1100",
		ModelPath: "qwen35-27b-gptq-gfx1100",
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": "cblevins-7900xtx",
		},
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["QUANTIZE_DEVICE_MAP"] != "cpu" {
		t.Fatalf("QUANTIZE_DEVICE_MAP = %q, want cpu for gfx1100", env["QUANTIZE_DEVICE_MAP"])
	}
}

func TestGPTQJobBuilder_BuildJob_VLMConfigExtraction(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	params := JobParams{
		Name:      "qwen35-27b-gptq",
		Namespace: "flexinfer-system",
		PVCName:   "qwen35-27b-gptq",
		ModelPath: "qwen35-27b-gptq",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
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
	script := container.Args[0]
	// Wrapper script delegates to external Python script.
	if !contains(script, "quantize_gptq.py") {
		t.Fatal("expected GPTQ wrapper script to invoke quantize_gptq.py")
	}
	// Wrapper script handles ROCm gfx900 detection.
	if !contains(script, "HSA_OVERRIDE_GFX_VERSION=9.0.6") {
		t.Fatal("expected GPTQ wrapper script to auto-detect gfx900 and set HSA_OVERRIDE_GFX_VERSION")
	}
	// Env vars control script behavior.
	env := containerEnvMap(container.Env)
	if env["GPU_MEMORY_FRACTION"] != DefaultGPUMemoryFraction {
		t.Fatalf("GPU_MEMORY_FRACTION env = %q, want %q", env["GPU_MEMORY_FRACTION"], DefaultGPUMemoryFraction)
	}
	if env["MAX_MEMORY_GB"] != "48" {
		t.Fatalf("MAX_MEMORY_GB env = %q, want 48", env["MAX_MEMORY_GB"])
	}
	// Dynamic exclusion defaults to "auto".
	if env["DYNAMIC_EXCLUSION"] != "auto" {
		t.Fatalf("DYNAMIC_EXCLUSION env = %q, want auto", env["DYNAMIC_EXCLUSION"])
	}
}

func TestGPTQJobBuilder_BuildJob_DynamicExclusionNone(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	dynExcl := "none"
	params := JobParams{
		Name:      "qwen35-gptq-pure",
		Namespace: "flexinfer-system",
		PVCName:   "qwen35-gptq-pure",
		ModelPath: "qwen35-gptq-pure",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:           aiv1alpha2.QuantizationFormatGPTQ,
			Bits:             &bits,
			GroupSize:        &groupSize,
			UseGPU:           true,
			DynamicExclusion: &dynExcl,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	env := containerEnvMap(container.Env)
	// "none" mode is passed as env var; the Python script handles the logic.
	if env["DYNAMIC_EXCLUSION"] != "none" {
		t.Fatalf("DYNAMIC_EXCLUSION env = %q, want none", env["DYNAMIC_EXCLUSION"])
	}
	// Wrapper script should still invoke the Python script.
	script := container.Args[0]
	if !contains(script, "quantize_gptq.py") {
		t.Fatal("expected GPTQ wrapper script to invoke quantize_gptq.py")
	}
}

func TestGPTQJobBuilder_BuildJob_DynamicExclusionAuto(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	dynExcl := "auto"
	params := JobParams{
		Name:      "qwen35-gptq-auto",
		Namespace: "flexinfer-system",
		PVCName:   "qwen35-gptq-auto",
		ModelPath: "qwen35-gptq-auto",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:           aiv1alpha2.QuantizationFormatGPTQ,
			Bits:             &bits,
			GroupSize:        &groupSize,
			UseGPU:           true,
			DynamicExclusion: &dynExcl,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	env := containerEnvMap(container.Env)
	// "auto" mode is passed as env var; the Python script handles the logic.
	if env["DYNAMIC_EXCLUSION"] != "auto" {
		t.Fatalf("DYNAMIC_EXCLUSION env = %q, want auto", env["DYNAMIC_EXCLUSION"])
	}
	// Wrapper script should still invoke the Python script.
	script := container.Args[0]
	if !contains(script, "quantize_gptq.py") {
		t.Fatal("expected GPTQ wrapper script to invoke quantize_gptq.py")
	}
}

func TestGPTQJobBuilder_BuildJob_CustomGPUMemoryFraction(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	gpuFrac := "0.95"
	params := JobParams{
		Name:      "test-gptq-gpufrac",
		Namespace: "flexinfer-system",
		PVCName:   "test-gptq-gpufrac",
		ModelPath: "test-gptq-gpufrac",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:            aiv1alpha2.QuantizationFormatGPTQ,
			Bits:              &bits,
			GroupSize:         &groupSize,
			UseGPU:            true,
			GPUMemoryFraction: &gpuFrac,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["GPU_MEMORY_FRACTION"] != "0.95" {
		t.Errorf("GPU_MEMORY_FRACTION env = %q, want 0.95", env["GPU_MEMORY_FRACTION"])
	}
}

func TestGPTQJobBuilder_BuildJob_CustomCalibrationDataset(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	dataset := "wikitext/wikitext-2-raw-v1"
	params := JobParams{
		Name:      "test-gptq-dataset",
		Namespace: "flexinfer-system",
		PVCName:   "test-gptq-dataset",
		ModelPath: "test-gptq-dataset",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
			Calibration: &aiv1alpha2.CalibrationSpec{
				Dataset: &dataset,
			},
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["DATASET"] != "wikitext/wikitext-2-raw-v1" {
		t.Errorf("DATASET env = %q, want wikitext/wikitext-2-raw-v1", env["DATASET"])
	}
}

func TestAWQJobBuilder_BuildJob_CustomCalibrationDataset(t *testing.T) {
	builder := &AWQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	dataset := "allenai/c4"
	params := JobParams{
		Name:      "test-awq-dataset",
		Namespace: "flexinfer-system",
		PVCName:   "test-awq-dataset",
		ModelPath: "test-awq-dataset",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatAWQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
			Calibration: &aiv1alpha2.CalibrationSpec{
				Dataset: &dataset,
			},
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["DATASET"] != "allenai/c4" {
		t.Errorf("DATASET env = %q, want allenai/c4", env["DATASET"])
	}
}

func TestGPTQJobBuilder_BuildJob_DefaultGPUMemoryFraction(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	params := JobParams{
		Name:      "test-gptq-default-frac",
		Namespace: "flexinfer-system",
		PVCName:   "test-gptq-default-frac",
		ModelPath: "test-gptq-default-frac",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
			// GPUMemoryFraction nil — should default to 0.80
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["GPU_MEMORY_FRACTION"] != "0.80" {
		t.Errorf("GPU_MEMORY_FRACTION env = %q, want 0.80", env["GPU_MEMORY_FRACTION"])
	}
}

func TestEXL2JobBuilder_Validate(t *testing.T) {
	builder := &EXL2JobBuilder{}
	bits := int32(4)

	valid := &aiv1alpha2.QuantizationSpec{
		Format: aiv1alpha2.QuantizationFormatEXL2,
		Bits:   &bits,
		UseGPU: true,
	}
	if err := builder.Validate(valid); err != nil {
		t.Fatalf("Validate(valid EXL2) returned error: %v", err)
	}

	invalidBits := int32(7)
	invalidSpec := &aiv1alpha2.QuantizationSpec{
		Format: aiv1alpha2.QuantizationFormatEXL2,
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatEXL2,
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

	valid := &aiv1alpha2.QuantizationSpec{
		Format: aiv1alpha2.QuantizationFormatFP8,
		Bits:   &bits,
		UseGPU: true,
	}
	if err := builder.Validate(valid); err != nil {
		t.Fatalf("Validate(valid FP8) returned error: %v", err)
	}

	invalidBits := int32(4)
	invalidSpec := &aiv1alpha2.QuantizationSpec{
		Format: aiv1alpha2.QuantizationFormatFP8,
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatFP8,
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
	ggufBackends := FormatBackendCompatibility[aiv1alpha2.QuantizationFormatGGUF]
	if !containsStr(ggufBackends, "llamacpp") {
		t.Error("GGUF should be compatible with llamacpp")
	}
	if !containsStr(ggufBackends, "ollama") {
		t.Error("GGUF should be compatible with ollama")
	}

	// AWQ should be compatible with vllm
	awqBackends := FormatBackendCompatibility[aiv1alpha2.QuantizationFormatAWQ]
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
		if rec.Spec.Format != aiv1alpha2.QuantizationFormatGGUF {
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
		if rec.Spec.Format != aiv1alpha2.QuantizationFormatGGUF {
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
		if rec.Spec.Format != aiv1alpha2.QuantizationFormatAWQ {
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
		if rec.Spec.Format != aiv1alpha2.QuantizationFormatFP8 {
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
		if rec.Spec.Format != aiv1alpha2.QuantizationFormatGGUF {
			t.Fatalf("Format = %q, want GGUF", rec.Spec.Format)
		}
		if rec.Spec.GGUFType != "Q3_K_M" {
			t.Fatalf("GGUFType = %q, want Q3_K_M", rec.Spec.GGUFType)
		}
	})
}

func TestGGUFJobBuilder_BuildJob_CustomTimeout(t *testing.T) {
	builder := &GGUFJobBuilder{}
	timeout := int64(14400) // 4 hours

	params := JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-model",
		ModelPath: "test-model",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:         aiv1alpha2.QuantizationFormatGGUF,
			GGUFType:       "Q4_K_M",
			TimeoutSeconds: &timeout,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != timeout {
		t.Errorf("ActiveDeadlineSeconds = %v, want %d", job.Spec.ActiveDeadlineSeconds, timeout)
	}
}

func TestAWQJobBuilder_BuildJob_Calibration(t *testing.T) {
	builder := &AWQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	maxSeqLen := int32(2048)
	maxSamples := int32(512)
	nParallel := int32(8)

	params := JobParams{
		Name:      "test-awq-calib",
		Namespace: "default",
		PVCName:   "test-awq-calib",
		ModelPath: "test-awq-calib",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatAWQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
			Calibration: &aiv1alpha2.CalibrationSpec{
				MaxSeqLen:             &maxSeqLen,
				MaxSamples:            &maxSamples,
				NParallelCalibSamples: &nParallel,
			},
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["MAX_SEQ_LEN"] != "2048" {
		t.Errorf("MAX_SEQ_LEN env = %q, want 2048", env["MAX_SEQ_LEN"])
	}
	if env["MAX_SAMPLES"] != "512" {
		t.Errorf("MAX_SAMPLES env = %q, want 512", env["MAX_SAMPLES"])
	}
	if env["N_PARALLEL_CALIB_SAMPLES"] != "8" {
		t.Errorf("N_PARALLEL_CALIB_SAMPLES env = %q, want 8", env["N_PARALLEL_CALIB_SAMPLES"])
	}
}

func TestAWQJobBuilder_BuildJob_DefaultCalibration(t *testing.T) {
	builder := &AWQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)

	params := JobParams{
		Name:      "test-awq-default",
		Namespace: "default",
		PVCName:   "test-awq-default",
		ModelPath: "test-awq-default",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatAWQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
			// Calibration is nil — should use defaults
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["MAX_SEQ_LEN"] != "4096" {
		t.Errorf("MAX_SEQ_LEN env = %q, want 4096", env["MAX_SEQ_LEN"])
	}
	if env["MAX_SAMPLES"] != "256" {
		t.Errorf("MAX_SAMPLES env = %q, want 256", env["MAX_SAMPLES"])
	}
	// N_PARALLEL_CALIB_SAMPLES should not be present when not configured
	if _, ok := env["N_PARALLEL_CALIB_SAMPLES"]; ok {
		t.Error("N_PARALLEL_CALIB_SAMPLES should not be set when not configured")
	}
}

func TestGPTQJobBuilder_BuildJob_Calibration(t *testing.T) {
	builder := &GPTQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	maxSeqLen := int32(1024)
	maxSamples := int32(64)

	params := JobParams{
		Name:      "test-gptq-calib",
		Namespace: "default",
		PVCName:   "test-gptq-calib",
		ModelPath: "test-gptq-calib",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatGPTQ,
			Bits:      &bits,
			GroupSize: &groupSize,
			UseGPU:    true,
			Calibration: &aiv1alpha2.CalibrationSpec{
				MaxSeqLen:  &maxSeqLen,
				MaxSamples: &maxSamples,
			},
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	env := containerEnvMap(job.Spec.Template.Spec.Containers[0].Env)
	if env["MAX_SEQ_LEN"] != "1024" {
		t.Errorf("MAX_SEQ_LEN env = %q, want 1024", env["MAX_SEQ_LEN"])
	}
	if env["MAX_SAMPLES"] != "64" {
		t.Errorf("MAX_SAMPLES env = %q, want 64", env["MAX_SAMPLES"])
	}
	if env["DATASET"] != DefaultCalibrationDataset {
		t.Errorf("DATASET env = %q, want %q", env["DATASET"], DefaultCalibrationDataset)
	}
	if !contains(env["QUANTIZE_MODEL_POLICIES"], "\"name\":\"qwen3.5-text\"") {
		t.Errorf("QUANTIZE_MODEL_POLICIES missing qwen3.5 policy: %q", env["QUANTIZE_MODEL_POLICIES"])
	}
	if !contains(env["QUANTIZE_MODEL_POLICIES"], "\"loader\":\"manual_sharded_state_dict\"") {
		t.Errorf("QUANTIZE_MODEL_POLICIES missing manual_sharded_state_dict loader override: %q", env["QUANTIZE_MODEL_POLICIES"])
	}
	if !contains(env["QUANTIZE_MODEL_POLICIES"], "\"match_path_substrings\":[\"qwen35\",\"qwen3.5\"]") {
		t.Errorf("QUANTIZE_MODEL_POLICIES missing path-based matcher for remapped retries: %q", env["QUANTIZE_MODEL_POLICIES"])
	}
	if !contains(env["QUANTIZE_MODEL_POLICIES"], "\"offload_to_disk\":false") {
		t.Errorf("QUANTIZE_MODEL_POLICIES missing offload_to_disk override: %q", env["QUANTIZE_MODEL_POLICIES"])
	}
	if env["GPTQ_HESSIAN_REPAIR"] != "true" {
		t.Errorf("GPTQ_HESSIAN_REPAIR env = %q, want true", env["GPTQ_HESSIAN_REPAIR"])
	}
	if env["GPTQ_HESSIAN_SANITIZE_NONFINITE"] != "true" {
		t.Errorf("GPTQ_HESSIAN_SANITIZE_NONFINITE env = %q, want true", env["GPTQ_HESSIAN_SANITIZE_NONFINITE"])
	}
	if env["GPTQ_HESSIAN_DIAG_FLOOR_SCALE"] != "1e-6" {
		t.Errorf("GPTQ_HESSIAN_DIAG_FLOOR_SCALE env = %q, want 1e-6", env["GPTQ_HESSIAN_DIAG_FLOOR_SCALE"])
	}
	if env["GPTQ_HESSIAN_FLOOR_MULTIPLIER"] != "10" {
		t.Errorf("GPTQ_HESSIAN_FLOOR_MULTIPLIER env = %q, want 10", env["GPTQ_HESSIAN_FLOOR_MULTIPLIER"])
	}
	if env["GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS"] != "6" {
		t.Errorf("GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS env = %q, want 6", env["GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS"])
	}

	// Wrapper script should invoke the Python script
	script := job.Spec.Template.Spec.Containers[0].Args[0]
	if !contains(script, "/opt/flexinfer/scripts/quantize_gptq.py") {
		t.Error("expected GPTQ wrapper script to invoke quantize_gptq.py")
	}
	if !contains(script, "torch.linalg.qr = safe_qr") {
		t.Error("expected GPTQ wrapper script to patch torch.linalg.qr")
	}
	if !contains(script, "cholesky/eigh/svd/qr") {
		t.Error("expected GPTQ wrapper script patch banner to mention qr fallback")
	}
	if !contains(script, "import tokenicer") {
		t.Error("expected GPTQ wrapper script to bootstrap tokenicer when missing")
	}
	if !contains(script, "GPTQ_PY_IMPORTS=\"import tokenicer, pcre, kernels\"") {
		t.Error("expected GPTQ wrapper script to define the base GPTQModel runtime dependency imports")
	}
	if !contains(script, "python3 -m pip uninstall -y torchao") {
		t.Error("expected GPTQ wrapper script to proactively remove torchao on unstable torch builds")
	}
	if contains(script, "\"torchao>=0.16.0\"") {
		t.Error("expected GPTQ wrapper script to avoid reinstalling torchao after removing it")
	}
	if !contains(script, "Skipping torchao on gfx906/gfx900; wheel triggers SIGILL on older x86 hosts") {
		t.Error("expected GPTQ wrapper script to explain why torchao is skipped on gfx906/gfx900")
	}
	if !contains(script, "python3 -m pip uninstall -y pypcre") {
		t.Error("expected GPTQ wrapper script to remove pypcre on gfx906/gfx900 hosts")
	}
	if !contains(script, "cat > /tmp/pcre.py") {
		t.Error("expected GPTQ wrapper script to create a stdlib-backed pcre shim")
	}
	if !contains(script, "\"hf_transfer>=0.1.9\"") {
		t.Error("expected GPTQ wrapper script self-heal path to install hf_transfer")
	}
	if !contains(script, "direct_init_kwargs.pop(\"device_map\", None)") {
		t.Error("expected GPTQ wrapper script to patch GPTQModel direct CPU loader to drop device_map")
	}
	if !contains(script, "direct_init_kwargs[\"low_cpu_mem_usage\"] = True") {
		t.Error("expected GPTQ wrapper script to enable low_cpu_mem_usage=true for incremental shard loading")
	}
	if !contains(script, "Patched quantize_gptq.py for Qwen3.5 direct load + text-only module tree") {
		t.Error("expected GPTQ wrapper script to retain bundled-script fallback patch for stale runtime images")
	}
	if !contains(script, "Adapted GPTQModel module tree for text-only Qwen3.5 causal LM (model.layers.*)") {
		t.Error("expected GPTQ wrapper script to patch GPTQModel's Qwen3.5 module tree for text-only loads")
	}
	if !contains(script, "def load_state_dict_materialized(module, state_dict, strict=False):") {
		t.Error("expected GPTQ wrapper script to inject a helper that materializes meta-backed shard loads")
	}
	if !contains(script, "module.load_state_dict(state_dict, strict=strict, assign=True)") {
		t.Error("expected GPTQ wrapper script to prefer load_state_dict(assign=True) when materializing checkpoint shards")
	}
	if !contains(script, "load_state_dict_materialized(model, state_dict, strict=False)") {
		t.Error("expected GPTQ wrapper script CPU shard loader to use the meta-materializing load helper")
	}
	if !contains(script, "def patch_gptq_save_meta_tensors():") {
		t.Error("expected GPTQ wrapper script to inject a helper that strips meta-backed tensors before save")
	}
	if !contains(script, "patch_gptq_save_meta_tensors()") {
		t.Error("expected GPTQ wrapper script to invoke the save-path meta tensor patch helper")
	}
	if !contains(script, `gptq_writer.get_state_dict_for_save = _patched_get_state_dict_for_save`) {
		t.Error("expected GPTQ wrapper script to patch GPTQModel writer save state_dict resolution")
	}
	if !contains(script, "Patched GPTQModel save path to skip meta-backed tensors") {
		t.Error("expected GPTQ wrapper script to announce the save-path meta tensor patch")
	}
	if !contains(script, `module_tree[:3] != ["model", "language_model", "layers"]`) {
		t.Error("expected GPTQ wrapper script to detect the composite Qwen3.5 module root before rewriting it")
	}
	if !contains(script, `model_definition.module_tree = ["model", "layers", *copy.deepcopy(module_tree[3:])]`) {
		t.Error("expected GPTQ wrapper script to rewrite Qwen3.5 module roots to model.layers")
	}
	// init_empty_weights injection for non-CPU device maps (GPU path)
	if !contains(script, "from accelerate import init_empty_weights") {
		t.Error("expected GPTQ wrapper script to inject init_empty_weights for GPU device map loading")
	}
	if !contains(script, "load_checkpoint_in_model") {
		t.Error("expected GPTQ wrapper script to use load_checkpoint_in_model for shard-by-shard loading")
	}
	if !contains(script, "fc_pattern = re.compile(r'^([ \\t]+)model = ([A-Za-z_][A-Za-z0-9_\\.]*)\\.from_config\\(config, \\*\\*init_kwargs\\)'") {
		t.Error("expected GPTQ wrapper script to match both model_definition.loader and loader_cls from_config calls")
	}
	if !contains(script, "loader_expr = fc_match.group(2)") {
		t.Error("expected GPTQ wrapper script to preserve the bundled loader expression when rewriting from_config")
	}
	if !contains(script, "model = {loader_expr}.from_config(config, **init_kwargs)") {
		t.Error("expected GPTQ wrapper script replacement to keep the matched loader expression")
	}
}

func TestGPUQuantizationJob_CustomTimeout(t *testing.T) {
	builder := &AWQJobBuilder{}
	bits := int32(4)
	groupSize := int32(128)
	timeout := int64(10800) // 3 hours

	params := JobParams{
		Name:      "test-timeout",
		Namespace: "default",
		PVCName:   "test-timeout",
		ModelPath: "test-timeout",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:         aiv1alpha2.QuantizationFormatAWQ,
			Bits:           &bits,
			GroupSize:      &groupSize,
			UseGPU:         true,
			TimeoutSeconds: &timeout,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() returned error: %v", err)
	}

	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != timeout {
		t.Errorf("ActiveDeadlineSeconds = %v, want %d", job.Spec.ActiveDeadlineSeconds, timeout)
	}
}

func TestCleanupTrapInScripts(t *testing.T) {
	tests := []struct {
		name    string
		builder JobBuilder
		params  JobParams
	}{
		{
			name:    "AWQ has cleanup trap",
			builder: &AWQJobBuilder{},
			params: JobParams{
				Name: "test", Namespace: "default", PVCName: "test", ModelPath: "test",
				Spec: &aiv1alpha2.QuantizationSpec{
					Format: aiv1alpha2.QuantizationFormatAWQ, Bits: int32Ptr(4), GroupSize: int32Ptr(128), UseGPU: true,
				},
			},
		},
		{
			name:    "GPTQ has cleanup trap",
			builder: &GPTQJobBuilder{},
			params: JobParams{
				Name: "test", Namespace: "default", PVCName: "test", ModelPath: "test",
				Spec: &aiv1alpha2.QuantizationSpec{
					Format: aiv1alpha2.QuantizationFormatGPTQ, Bits: int32Ptr(4), GroupSize: int32Ptr(128), UseGPU: true,
				},
			},
		},
		{
			name:    "EXL2 has cleanup trap",
			builder: &EXL2JobBuilder{},
			params: JobParams{
				Name: "test", Namespace: "default", PVCName: "test", ModelPath: "test",
				Spec: &aiv1alpha2.QuantizationSpec{
					Format: aiv1alpha2.QuantizationFormatEXL2, Bits: int32Ptr(4), UseGPU: true,
				},
			},
		},
		{
			name:    "FP8 has cleanup trap",
			builder: &FP8JobBuilder{},
			params: JobParams{
				Name: "test", Namespace: "default", PVCName: "test", ModelPath: "test",
				Spec: &aiv1alpha2.QuantizationSpec{
					Format: aiv1alpha2.QuantizationFormatFP8, Bits: int32Ptr(8), UseGPU: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := tt.builder.BuildJob(tt.params)
			if err != nil {
				t.Fatalf("BuildJob() returned error: %v", err)
			}
			script := job.Spec.Template.Spec.Containers[0].Args[0]
			if !contains(script, "trap cleanup EXIT") {
				t.Error("script should contain cleanup trap")
			}
			if !contains(script, "trap - EXIT") {
				t.Error("script should disable cleanup trap before metadata")
			}
		})
	}
}

func int32Ptr(v int32) *int32 { return &v }

func containerEnvMap(envVars []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(envVars))
	for _, e := range envVars {
		m[e.Name] = e.Value
	}
	return m
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

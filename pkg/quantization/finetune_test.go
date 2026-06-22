package quantization

import (
	"reflect"
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestFinetuneEnv_CoversSpecFields(t *testing.T) {
	mode := aiv1alpha1.FinetuneModeFull
	merge := false
	grad := false
	loraDropout := "0.2"
	dataset := "tatsu-lab/alpaca"
	pvcName := "dataset-pvc"
	pvcSubPath := "train/sft"
	split := "validation"
	spec := &aiv1alpha1.FinetuneSpec{
		Mode:                  &mode,
		Epochs:                int32PtrFT(7),
		BatchSize:             int32PtrFT(8),
		LearningRate:          stringPtrFT("1e-5"),
		MaxSeqLen:             int32PtrFT(4096),
		MergeAdapter:          &merge,
		GradientCheckpointing: &grad,
		LoRA: &aiv1alpha1.FinetuneLoRAConfig{
			Rank:          int32PtrFT(64),
			Alpha:         int32PtrFT(128),
			Dropout:       &loraDropout,
			TargetModules: []string{"q_proj", "k_proj"},
		},
		Dataset: aiv1alpha1.FinetuneDatasetSpec{
			HuggingFace: &dataset,
			PVCName:     &pvcName,
			PVCSubPath:  &pvcSubPath,
			Split:       &split,
			MaxSamples:  int32PtrFT(1024),
		},
	}

	env := finetuneEnv("qwen3-8b", spec)
	find := func(name string) string {
		for _, e := range env {
			if e.Name == name {
				return e.Value
			}
		}
		return ""
	}

	want := map[string]string{
		"MODEL_DIR":           "/cache/qwen3-8b",
		"MODE":                "full",
		"EPOCHS":              "7",
		"BATCH_SIZE":          "8",
		"LEARNING_RATE":       "1e-5",
		"MAX_SEQ_LEN":         "4096",
		"LORA_RANK":           "64",
		"LORA_ALPHA":          "128",
		"LORA_DROPOUT":        "0.2",
		"TARGET_MODULES":      "q_proj,k_proj",
		"MERGE_ADAPTER":       "false",
		"GRAD_CHECKPOINT":     "false",
		"DATASET_SOURCE":      "tatsu-lab/alpaca",
		"DATASET_SPLIT":       "validation",
		"MAX_SAMPLES":         "1024",
		"DATASET_PVC_PATH":    "/datasets",
		"FLEXINFER_TELEMETRY": "true",
	}

	for key, wantValue := range want {
		if got := find(key); got != wantValue {
			t.Fatalf("%s = %q, want %q", key, got, wantValue)
		}
	}
}

func TestBuildFinetuneJob_GeneratesExpectedPodSpec(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "false")
	t.Setenv("FLEXINFER_FINETUNE_IMAGE", "")

	pvcName := "dataset-pvc"
	pvcSubPath := "subset"
	dataset := "myorg/trainset"
	split := "train"
	merge := true
	grad := true
	spec := &aiv1alpha1.FinetuneSpec{
		UseGPU:                boolPtrFT(true),
		MaxMemoryGB:           int32PtrFT(72),
		TimeoutSeconds:        int64PtrFT(7200),
		MergeAdapter:          &merge,
		GradientCheckpointing: &grad,
		Dataset: aiv1alpha1.FinetuneDatasetSpec{
			HuggingFace: &dataset,
			PVCName:     &pvcName,
			PVCSubPath:  &pvcSubPath,
			Split:       &split,
		},
	}
	params := JobParams{
		Name:                  "qwen3-8b",
		Namespace:             "flexinfer-system",
		PVCName:               "model-pvc",
		ModelPath:             "qwen3-8b",
		GPUVendor:             "amd",
		GPUArch:               "gfx1100",
		NodeSelector:          map[string]string{"kubernetes.io/hostname": "node-a"},
		Tolerations:           []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "gpu", Effect: corev1.TaintEffectNoSchedule}},
		ProfileQuantizerImage: "profile/image:tag",
	}

	job, err := BuildFinetuneJob(params, spec)
	if err != nil {
		t.Fatalf("BuildFinetuneJob() error = %v", err)
	}

	if job.Name != "qwen3-8b-finetune" {
		t.Fatalf("job.Name = %q, want qwen3-8b-finetune", job.Name)
	}
	if job.Namespace != "flexinfer-system" {
		t.Fatalf("job.Namespace = %q", job.Namespace)
	}
	if got := job.Spec.ActiveDeadlineSeconds; got == nil || *got != 7200 {
		t.Fatalf("ActiveDeadlineSeconds = %v, want 7200", got)
	}
	if got := job.Spec.BackoffLimit; got == nil || *got != 2 {
		t.Fatalf("BackoffLimit = %v, want 2", got)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "profile/image:tag" {
		t.Fatalf("container.Image = %q, want profile/image:tag", container.Image)
	}
	if container.Name != "finetuner" {
		t.Fatalf("container.Name = %q", container.Name)
	}
	if container.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("container.ImagePullPolicy = %s", container.ImagePullPolicy)
	}
	if got := container.Resources.Requests[corev1.ResourceCPU]; got.String() != "8" {
		t.Fatalf("CPU request = %s, want 8", got.String())
	}
	if got := container.Resources.Requests[corev1.ResourceMemory]; got.String() != "72Gi" {
		t.Fatalf("memory request = %s, want 72Gi", got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceMemory]; got.String() != "72Gi" {
		t.Fatalf("memory limit = %s, want 72Gi", got.String())
	}
	if got := container.Resources.Requests[corev1.ResourceName("amd.com/gpu")]; got.String() != "1" {
		t.Fatalf("amd gpu request = %s, want 1", got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceName("amd.com/gpu")]; got.String() != "1" {
		t.Fatalf("amd gpu limit = %s, want 1", got.String())
	}

	if got := findEnvVar(container.Env, "PYTORCH_ALLOC_CONF"); got != rocmAllocatorConfig {
		t.Fatalf("PYTORCH_ALLOC_CONF = %q, want %q", got, rocmAllocatorConfig)
	}
	if got := findEnvVar(container.Env, "DATASET_PVC_PATH"); got != "/datasets" {
		t.Fatalf("DATASET_PVC_PATH = %q, want /datasets", got)
	}
	if got := findEnvVar(container.Env, "DATASET_SOURCE"); got != "myorg/trainset" {
		t.Fatalf("DATASET_SOURCE = %q, want myorg/trainset", got)
	}

	volumes := job.Spec.Template.Spec.Volumes
	if len(volumes) != 3 {
		t.Fatalf("len(volumes) = %d, want 3", len(volumes))
	}
	if volumes[2].Name != "dataset" {
		t.Fatalf("dataset volume = %q", volumes[2].Name)
	}
	if got := volumes[2].PersistentVolumeClaim; got == nil || got.ClaimName != pvcName || !got.ReadOnly {
		t.Fatalf("dataset PVC volume = %#v", got)
	}

	mounts := container.VolumeMounts
	if len(mounts) != 3 {
		t.Fatalf("len(volumeMounts) = %d, want 3", len(mounts))
	}
	if mounts[2].Name != "dataset" || mounts[2].MountPath != "/datasets" || !mounts[2].ReadOnly || mounts[2].SubPath != pvcSubPath {
		t.Fatalf("dataset mount = %#v", mounts[2])
	}

	if !reflect.DeepEqual(job.Spec.Template.Spec.NodeSelector, map[string]string{"kubernetes.io/hostname": "node-a"}) {
		t.Fatalf("NodeSelector = %#v", job.Spec.Template.Spec.NodeSelector)
	}
	if !reflect.DeepEqual(job.Spec.Template.Spec.Tolerations, params.Tolerations) {
		t.Fatalf("Tolerations = %#v", job.Spec.Template.Spec.Tolerations)
	}
}

func TestBuildFinetuneJob_CPUOnlyAndDefaults(t *testing.T) {
	spec := &aiv1alpha1.FinetuneSpec{
		UseGPU:         boolPtrFT(false),
		Dataset:        aiv1alpha1.FinetuneDatasetSpec{},
		MaxMemoryGB:    int32PtrFT(0),
		TimeoutSeconds: int64PtrFT(240),
	}

	job, err := BuildFinetuneJob(JobParams{
		Name:      "cpu-model",
		Namespace: "default",
		PVCName:   "model-pvc",
		ModelPath: "cpu-model",
		GPUVendor: "nvidia",
		GPUArch:   "sm_80",
	}, spec)
	if err != nil {
		t.Fatalf("BuildFinetuneJob() error = %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != DefaultGPTQImage {
		t.Fatalf("container.Image = %q, want %q", container.Image, DefaultGPTQImage)
	}
	if _, ok := container.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; ok {
		t.Fatalf("unexpected gpu request in CPU-only job")
	}
	if got := findEnvVar(container.Env, "PYTORCH_ALLOC_CONF"); got != "" {
		t.Fatalf("unexpected PYTORCH_ALLOC_CONF = %q", got)
	}
	if got := job.Spec.ActiveDeadlineSeconds; got == nil || *got != DefaultFinetuneDeadlineSeconds {
		t.Fatalf("ActiveDeadlineSeconds = %v, want default %d", got, DefaultFinetuneDeadlineSeconds)
	}
	if got := container.Resources.Requests[corev1.ResourceCPU]; got.String() != "8" {
		t.Fatalf("CPU request = %s, want 8", got.String())
	}
	if got := container.Resources.Requests[corev1.ResourceMemory]; got.String() != "32Gi" {
		t.Fatalf("memory request = %s, want 32Gi", got.String())
	}
	if got := findEnvVar(container.Env, "DATASET_PVC_PATH"); got != "" {
		t.Fatalf("DATASET_PVC_PATH = %q, want empty", got)
	}
	if len(job.Spec.Template.Spec.Volumes) != 2 {
		t.Fatalf("len(volumes) = %d, want 2", len(job.Spec.Template.Spec.Volumes))
	}
	if vol := job.Spec.Template.Spec.Volumes[1].EmptyDir; vol == nil || vol.SizeLimit == nil || vol.SizeLimit.String() != "64Gi" {
		t.Fatalf("workspace volume = %#v, want 64Gi emptyDir", vol)
	}
}

func TestBuildFinetuneJob_NilSpec(t *testing.T) {
	if _, err := BuildFinetuneJob(JobParams{}, nil); err == nil {
		t.Fatal("BuildFinetuneJob(nil) error = nil, want error")
	}
}

func findEnvVar(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func boolPtrFT(v bool) *bool {
	return &v
}

func int64PtrFT(v int64) *int64 {
	return &v
}

func stringPtrFT(v string) *string {
	return &v
}

func int32PtrFT(v int32) *int32 {
	return &v
}

func TestBuildFinetuneJob_GPUDriverMemoryInflation(t *testing.T) {
	spec := &aiv1alpha1.FinetuneSpec{
		UseGPU:      boolPtrFT(true),
		MaxMemoryGB: int32PtrFT(32),
		Dataset:     aiv1alpha1.FinetuneDatasetSpec{},
	}
	params := JobParams{
		Name:      "test-model",
		Namespace: "flexinfer-system",
		PVCName:   "model-pvc",
		ModelPath: "test-model",
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
		MemoryConfig: GPUMemoryConfig{
			ContainerMemoryGB: 32,
			GPUDriverMemoryMB: 12288, // 12 GiB
		},
	}

	job, err := BuildFinetuneJob(params, spec)
	if err != nil {
		t.Fatalf("BuildFinetuneJob() error = %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	// memoryGB=32, driverOverhead=12288/1024=12 → schedulingMemoryGB=44
	if got := container.Resources.Limits[corev1.ResourceMemory]; got.String() != "44Gi" {
		t.Fatalf("memory limit = %s, want 44Gi (32+12 driver overhead)", got.String())
	}
	if got := container.Resources.Requests[corev1.ResourceMemory]; got.String() != "44Gi" {
		t.Fatalf("memory request = %s, want 44Gi", got.String())
	}
}

func TestBuildFinetuneJob_NoDriverOverhead(t *testing.T) {
	spec := &aiv1alpha1.FinetuneSpec{
		UseGPU:      boolPtrFT(true),
		MaxMemoryGB: int32PtrFT(32),
		Dataset:     aiv1alpha1.FinetuneDatasetSpec{},
	}
	params := JobParams{
		Name:      "test-model",
		Namespace: "flexinfer-system",
		PVCName:   "model-pvc",
		ModelPath: "test-model",
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
		MemoryConfig: GPUMemoryConfig{
			ContainerMemoryGB: 32,
			GPUDriverMemoryMB: 0, // no overhead
		},
	}

	job, err := BuildFinetuneJob(params, spec)
	if err != nil {
		t.Fatalf("BuildFinetuneJob() error = %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if got := container.Resources.Limits[corev1.ResourceMemory]; got.String() != "32Gi" {
		t.Fatalf("memory limit = %s, want 32Gi (no driver overhead)", got.String())
	}
}

// TestBuildFinetuneJob_DefaultFitsGFX1100Node is the regression test for the
// 2026-06-20 finding: a QLoRA finetune Job built from a default FinetuneSpec
// (no maxMemoryGB override) requested ~68Gi on gfx1100 and stayed Pending with
// "Insufficient memory" because the cblevins-5930k node only has ~57Gi
// allocatable. The default must now fit a 64 GiB node after GPU-driver
// inflation, so no manual maxMemoryGB override is required.
func TestBuildFinetuneJob_DefaultFitsGFX1100Node(t *testing.T) {
	// 64 GiB node: kubelet/system reserve leaves ~57Gi allocatable.
	const nodeAllocatableGi = 57

	// Default spec for a small QLoRA finetune — no MaxMemoryGB set, mirroring
	// the live ft-crd-flexland (Qwen3-1.7B) ModelCache.
	spec := &aiv1alpha1.FinetuneSpec{
		UseGPU:  boolPtrFT(true),
		Dataset: aiv1alpha1.FinetuneDatasetSpec{},
	}
	params := JobParams{
		Name:      "ft-crd-flexland",
		Namespace: "flexinfer-system",
		PVCName:   "model-pvc",
		ModelPath: "qwen3-1.7b",
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
		// Mirrors deploy/gpuprofiles/gfx1100.yaml::gpuDriverMemoryMB.
		MemoryConfig: GPUMemoryConfig{GPUDriverMemoryMB: 12288},
	}

	job, err := BuildFinetuneJob(params, spec)
	if err != nil {
		t.Fatalf("BuildFinetuneJob() error = %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Default 32 + 12Gi driver inflation = 44Gi, well under the 50Gi target.
	limit := container.Resources.Limits[corev1.ResourceMemory]
	if limit.String() != "44Gi" {
		t.Fatalf("memory limit = %s, want 44Gi (32 default + 12Gi driver overhead)", limit.String())
	}

	// The scheduler reserves on the request — it must fit the node allocatable
	// so the Job binds without a manual maxMemoryGB override.
	request := container.Resources.Requests[corev1.ResourceMemory]
	requestGi := request.Value() / (1 << 30)
	if requestGi > nodeAllocatableGi {
		t.Fatalf("memory request = %s (%dGi) exceeds node allocatable %dGi — Job would stay Pending",
			request.String(), requestGi, nodeAllocatableGi)
	}
	if requestGi > 50 {
		t.Fatalf("memory request = %s (%dGi) exceeds 50Gi target after driver inflation", request.String(), requestGi)
	}
}

func TestResourceQuantityHelpers(t *testing.T) {
	q := resource.MustParse("112Gi")
	if q.String() != "112Gi" {
		t.Fatalf("resource quantity = %s", q.String())
	}
	if !strings.Contains(finetuneWrapperScript(), "finetune_complete") {
		t.Fatalf("wrapper script missing completion event")
	}
}

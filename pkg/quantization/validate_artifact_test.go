package quantization

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func ptrString(s string) *string { return &s }
func ptrInt32(v int32) *int32    { return &v }
func ptrInt64(v int64) *int64    { return &v }
func ptrBool(b bool) *bool       { return &b }

func validateBaseParams() JobParams {
	return JobParams{
		Name:      "demo",
		Namespace: "flexinfer-system",
		PVCName:   "demo-pvc",
		ModelPath: "demo/gptq-w4-g128",
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": "cblevins-7900xtx",
		},
	}
}

func TestBuildValidateArtifactJob_NilSpecRejected(t *testing.T) {
	if _, err := BuildValidateArtifactJob(validateBaseParams(), nil); err == nil {
		t.Fatal("expected error for nil spec, got nil")
	}
}

func TestBuildValidateArtifactJob_DisabledRejected(t *testing.T) {
	spec := &aiv1alpha1.PublishValidateSpec{Enabled: false}
	if _, err := BuildValidateArtifactJob(validateBaseParams(), spec); err == nil {
		t.Fatal("expected error for disabled spec, got nil")
	}
}

func TestBuildValidateArtifactJob_DefaultsAndShape(t *testing.T) {
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:test")
	t.Setenv("FLEXINFER_VALIDATOR_IMAGE", "")

	spec := &aiv1alpha1.PublishValidateSpec{Enabled: true}
	job, err := BuildValidateArtifactJob(validateBaseParams(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if job.Name != "demo"+ValidatorJobSuffix {
		t.Fatalf("job name = %q, want %q", job.Name, "demo"+ValidatorJobSuffix)
	}
	if job.Namespace != "flexinfer-system" {
		t.Fatalf("namespace = %q, want flexinfer-system", job.Namespace)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != int64(DefaultValidatorDeadlineSeconds) {
		t.Fatalf("default deadline not applied, got %v", job.Spec.ActiveDeadlineSeconds)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != int32(1) {
		t.Fatalf("backoff = %v, want 1", job.Spec.BackoffLimit)
	}

	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(job.Spec.Template.Spec.Containers))
	}
	c := job.Spec.Template.Spec.Containers[0]
	if c.Name != "validator" {
		t.Errorf("container name = %q, want validator", c.Name)
	}
	if c.Image != "registry.harbor.lan/flexinfer/runtime:test" {
		t.Errorf("image = %q, want runtime image", c.Image)
	}
	// No GPU — validator is CPU-only.
	if _, hasNvidia := c.Resources.Requests["nvidia.com/gpu"]; hasNvidia {
		t.Errorf("validator requested nvidia GPU, expected none")
	}
	if _, hasAMD := c.Resources.Requests["amd.com/gpu"]; hasAMD {
		t.Errorf("validator requested amd GPU, expected none")
	}

	wantSubstr := []string{
		"ARTIFACT_PATH=\"/cache/demo/gptq-w4-g128\"",
		ValidatorScriptPath,
		"--layout 'auto'",
		"--family 'auto'",
		"--json",
		"tee /dev/termination-log",
	}
	args := strings.Join(c.Args, " ")
	for _, want := range wantSubstr {
		if !strings.Contains(args, want) {
			t.Errorf("script missing %q\n--- args ---\n%s", want, args)
		}
	}

	// PVC mount must reference the cache PVC.
	if c.VolumeMounts[0].Name != "model-cache" {
		t.Errorf("volume mount name = %q, want model-cache", c.VolumeMounts[0].Name)
	}
	if job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "demo-pvc" {
		t.Errorf("PVC claim = %q, want demo-pvc",
			job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
	}

	// NodeSelector + tolerations propagated.
	if job.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"] != "cblevins-7900xtx" {
		t.Errorf("node selector not propagated: %v", job.Spec.Template.Spec.NodeSelector)
	}
}

func TestBuildValidateArtifactJob_LayoutAndFamilyOverrides(t *testing.T) {
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime:img")

	spec := &aiv1alpha1.PublishValidateSpec{
		Enabled: true,
		Layout:  ptrString("vllm-gptq"),
		Family:  ptrString("gemma4-26b-a4b"),
	}
	job, err := BuildValidateArtifactJob(validateBaseParams(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(job.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "--layout 'vllm-gptq'") {
		t.Errorf("layout override not in script: %s", args)
	}
	if !strings.Contains(args, "--family 'gemma4-26b-a4b'") {
		t.Errorf("family override not in script: %s", args)
	}
}

func TestBuildValidateArtifactJob_Qwen36GDNValidationGate(t *testing.T) {
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime:img")

	spec := &aiv1alpha1.PublishValidateSpec{
		Enabled:        true,
		Layout:         ptrString("vllm-gptq"),
		Family:         ptrString("qwen36-27b"),
		FailOnWarnings: ptrBool(false),
	}
	job, err := BuildValidateArtifactJob(validateBaseParams(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(job.Spec.Template.Spec.Containers[0].Args, " ")
	for _, want := range []string{
		"--layout 'vllm-gptq'",
		"--family 'qwen36-27b'",
		"--json",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("qwen36 validation wrapper missing %q: %s", want, args)
		}
	}
}

func TestBuildValidateArtifactJob_CustomImageWinsOverEnv(t *testing.T) {
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime:env")
	t.Setenv("FLEXINFER_VALIDATOR_IMAGE", "validator:env")

	spec := &aiv1alpha1.PublishValidateSpec{
		Enabled: true,
		Image:   ptrString("override:custom"),
	}
	job, err := BuildValidateArtifactJob(validateBaseParams(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := job.Spec.Template.Spec.Containers[0].Image; got != "override:custom" {
		t.Errorf("image = %q, want override:custom", got)
	}
}

func TestBuildValidateArtifactJob_ValidatorImageOverridesRuntime(t *testing.T) {
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime:env")
	t.Setenv("FLEXINFER_VALIDATOR_IMAGE", "validator:env")

	spec := &aiv1alpha1.PublishValidateSpec{Enabled: true}
	job, err := BuildValidateArtifactJob(validateBaseParams(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := job.Spec.Template.Spec.Containers[0].Image; got != "validator:env" {
		t.Errorf("image = %q, want validator:env", got)
	}
}

func TestBuildValidateArtifactJob_CustomTimeoutAndMemory(t *testing.T) {
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime:env")

	spec := &aiv1alpha1.PublishValidateSpec{
		Enabled:        true,
		MaxMemoryGB:    ptrInt32(8),
		TimeoutSeconds: ptrInt64(900),
		FailOnWarnings: ptrBool(true),
	}
	job, err := BuildValidateArtifactJob(validateBaseParams(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *job.Spec.ActiveDeadlineSeconds != 900 {
		t.Errorf("deadline = %d, want 900", *job.Spec.ActiveDeadlineSeconds)
	}
	c := job.Spec.Template.Spec.Containers[0]
	mem := c.Resources.Limits[corev1.ResourceMemory]
	if mem.String() != "8Gi" {
		t.Errorf("memory limit = %s, want 8Gi", mem.String())
	}
}

func TestValidatorWrapperScript_StripsQuotesFromUserInput(t *testing.T) {
	spec := &aiv1alpha1.PublishValidateSpec{
		Enabled: true,
		Layout:  ptrString("vllm-gptq'; rm -rf /"),
		Family:  ptrString("gemma4'evil"),
	}
	script := validatorWrapperScript("model/path", spec)
	if strings.Contains(script, "'; rm -rf") {
		t.Errorf("script preserved single-quote injection: %s", script)
	}
	// Single-quoted args should still close cleanly.
	if !strings.Contains(script, "--layout 'vllm-gptq; rm -rf /'") {
		t.Errorf("expected sanitized layout arg in script: %s", script)
	}
}

package quantization

import (
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// TestResolveQuantizationMemoryGB locks in the container-memory precedence
// (spec.maxMemoryGB > MemoryConfig.ContainerMemoryGB > default) that the AWQ,
// FP8, EXL2, compressed-tensors, and GPTQ builders previously inlined.
func TestResolveQuantizationMemoryGB(t *testing.T) {
	tests := []struct {
		name   string
		spec   *aiv1alpha2.QuantizationSpec
		memCfg GPUMemoryConfig
		want   int32
	}{
		{
			name: "default when nothing set",
			spec: &aiv1alpha2.QuantizationSpec{},
			want: int32(DefaultGPUQuantizationMemoryGB),
		},
		{
			name:   "profile container memory overrides default",
			spec:   &aiv1alpha2.QuantizationSpec{},
			memCfg: GPUMemoryConfig{ContainerMemoryGB: 64},
			want:   64,
		},
		{
			name:   "spec maxMemoryGB wins over profile",
			spec:   &aiv1alpha2.QuantizationSpec{MaxMemoryGB: int32Ptr(96)},
			memCfg: GPUMemoryConfig{ContainerMemoryGB: 64},
			want:   96,
		},
		{
			name: "spec maxMemoryGB wins over default",
			spec: &aiv1alpha2.QuantizationSpec{MaxMemoryGB: int32Ptr(32)},
			want: 32,
		},
		{
			name:   "zero container memory falls through to default",
			spec:   &aiv1alpha2.QuantizationSpec{},
			memCfg: GPUMemoryConfig{ContainerMemoryGB: 0},
			want:   int32(DefaultGPUQuantizationMemoryGB),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveQuantizationMemoryGB(tt.spec, tt.memCfg); got != tt.want {
				t.Fatalf("resolveQuantizationMemoryGB = %d, want %d", got, tt.want)
			}
		})
	}
}

// inlineMemoryGB reproduces the pre-refactor inline precedence so the parity
// assertion below is grounded in the original logic, not just the new helper.
func inlineMemoryGB(spec *aiv1alpha2.QuantizationSpec, memCfg GPUMemoryConfig) int32 {
	memoryGB := int32(DefaultGPUQuantizationMemoryGB)
	if memCfg.ContainerMemoryGB > 0 {
		memoryGB = memCfg.ContainerMemoryGB
	}
	if spec.MaxMemoryGB != nil {
		memoryGB = *spec.MaxMemoryGB
	}
	return memoryGB
}

func TestResolveQuantizationMemoryGB_MatchesInline(t *testing.T) {
	specs := []*aiv1alpha2.QuantizationSpec{
		{},
		{MaxMemoryGB: int32Ptr(16)},
		{MaxMemoryGB: int32Ptr(128)},
	}
	cfgs := []GPUMemoryConfig{
		{},
		{ContainerMemoryGB: 48},
		{ContainerMemoryGB: 80},
	}
	for _, spec := range specs {
		for _, cfg := range cfgs {
			want := inlineMemoryGB(spec, cfg)
			if got := resolveQuantizationMemoryGB(spec, cfg); got != want {
				t.Fatalf("helper=%d inline=%d (spec=%+v cfg=%+v)", got, want, spec, cfg)
			}
		}
	}
}

// TestResolveBitsAndGroupSize locks in the bits/groupSize extraction shared by
// the AWQ, GPTQ, and compressed-tensors builders.
func TestResolveBitsAndGroupSize(t *testing.T) {
	tests := []struct {
		name             string
		spec             *aiv1alpha2.QuantizationSpec
		defaultBits      int
		defaultGroupSize int
		wantBits         int
		wantGroupSize    int
	}{
		{
			name:             "both defaults",
			spec:             &aiv1alpha2.QuantizationSpec{},
			defaultBits:      DefaultGPTQBits,
			defaultGroupSize: DefaultQuantizationGroupSize,
			wantBits:         4,
			wantGroupSize:    128,
		},
		{
			name:             "explicit bits override",
			spec:             &aiv1alpha2.QuantizationSpec{Bits: int32Ptr(8)},
			defaultBits:      DefaultGPTQBits,
			defaultGroupSize: DefaultQuantizationGroupSize,
			wantBits:         8,
			wantGroupSize:    128,
		},
		{
			name:             "explicit group size override",
			spec:             &aiv1alpha2.QuantizationSpec{GroupSize: int32Ptr(64)},
			defaultBits:      DefaultAWQBits,
			defaultGroupSize: DefaultQuantizationGroupSize,
			wantBits:         4,
			wantGroupSize:    64,
		},
		{
			name:             "both explicit",
			spec:             &aiv1alpha2.QuantizationSpec{Bits: int32Ptr(4), GroupSize: int32Ptr(32)},
			defaultBits:      DefaultCompressedTensorsBits,
			defaultGroupSize: DefaultCompressedTensorsGroupSize,
			wantBits:         4,
			wantGroupSize:    32,
		},
		{
			name:             "zero group size preserved (validation handles rejection)",
			spec:             &aiv1alpha2.QuantizationSpec{GroupSize: int32Ptr(0)},
			defaultBits:      DefaultAWQBits,
			defaultGroupSize: DefaultQuantizationGroupSize,
			wantBits:         4,
			wantGroupSize:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bits, groupSize := resolveBitsAndGroupSize(tt.spec, tt.defaultBits, tt.defaultGroupSize)
			if bits != tt.wantBits {
				t.Errorf("bits = %d, want %d", bits, tt.wantBits)
			}
			if groupSize != tt.wantGroupSize {
				t.Errorf("groupSize = %d, want %d", groupSize, tt.wantGroupSize)
			}
		})
	}
}

// TestBuildFormatQuantizationJob_MatchesDirectConstruction asserts the wrapper
// produces the identical Job the AWQ/FP8/EXL2 builders produced when they called
// ResolveImage + buildGPUQuantizationJob directly.
func TestBuildFormatQuantizationJob_MatchesDirectConstruction(t *testing.T) {
	params := JobParams{
		Name:      "test-cache",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-cache",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatFP8,
		},
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
	}
	script := "echo quantize"
	const memoryGB int32 = 48

	want, err := buildGPUQuantizationJob(
		params,
		ResolveImage(ImageFormatFP8, params.ProfileQuantizerImage, params.GPUVendor, params.GPUArch),
		script,
		memoryGB,
		nil,
	)
	if err != nil {
		t.Fatalf("direct construction error: %v", err)
	}

	got, err := buildFormatQuantizationJob(params, ImageFormatFP8, script, memoryGB, nil)
	if err != nil {
		t.Fatalf("buildFormatQuantizationJob error: %v", err)
	}

	wantImage := want.Spec.Template.Spec.Containers[0].Image
	gotImage := got.Spec.Template.Spec.Containers[0].Image
	if wantImage != gotImage {
		t.Fatalf("image mismatch: got %q, want %q", gotImage, wantImage)
	}
	if got.Spec.Template.Spec.Containers[0].Args[0] != script {
		t.Fatalf("script not propagated: got %q", got.Spec.Template.Spec.Containers[0].Args[0])
	}
}

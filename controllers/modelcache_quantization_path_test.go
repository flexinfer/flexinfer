package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestQuantizedOutputPath(t *testing.T) {
	tests := []struct {
		name     string
		spec     *aiv1alpha1.QuantizationSpec
		basePath string
		want     string
	}{
		{
			name:     "nil spec returns basePath",
			spec:     nil,
			basePath: "/models/qwen3",
			want:     "/models/qwen3",
		},
		{
			name: "GPTQ default bits/group",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3/gptq-w4-g128",
		},
		{
			name: "GPTQ custom bits/group",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatGPTQ,
				Bits:      int32Ptr(8),
				GroupSize: int32Ptr(64),
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3/gptq-w8-g64",
		},
		{
			name: "GPTQ GDN policy gets an isolated output path",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:           aiv1alpha1.QuantizationFormatGPTQ,
				DynamicExclusion: stringPtr("gdn"),
			},
			basePath: "/models/qwen35",
			want:     "/models/qwen35/gptq-w4-g128-gdn",
		},
		{
			name: "Gemma4 26B GPTQ uses versioned hybrid path",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
			},
			basePath: "/models/gemma4-26b-a4b-gptq",
			want:     "/models/gemma4-26b-a4b-gptq/gptq-w4-g128-hybrid-v12",
		},
		{
			name: "AWQ default bits/group",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatAWQ,
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3/awq-w4-g128",
		},
		{
			name: "AWQ custom bits",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatAWQ,
				Bits:   int32Ptr(8),
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3/awq-w8-g128",
		},
		{
			name: "GGUF format returns basePath (no subdirectory)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGGUF,
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3",
		},
		{
			name: "EXL2 format returns basePath (no subdirectory)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatEXL2,
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3",
		},
		{
			name: "FP8 format returns basePath",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatFP8,
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3",
		},
		{
			name: "COMPRESSED_TENSORS default bits/group",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatCompressedTensors,
			},
			basePath: "/models/qwen3",
			want:     "/models/qwen3/compressed-tensors-w4-g128",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quantizedOutputPath(tt.spec, tt.basePath)
			assert.Equal(t, tt.want, got)
		})
	}
}

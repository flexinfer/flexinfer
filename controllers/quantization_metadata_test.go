package controllers

import (
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseQuantizationMetadata(t *testing.T) {
	meta, err := parseQuantizationMetadata(`{"type":"Q4_K_M","originalSizeBytes":16000,"compressedSizeBytes":4200,"quantizationTimeSeconds":154}`)
	if err != nil {
		t.Fatalf("parseQuantizationMetadata returned error: %v", err)
	}
	if meta.Type != "Q4_K_M" {
		t.Fatalf("Type = %q, want %q", meta.Type, "Q4_K_M")
	}
	if meta.OriginalSizeBytes != 16000 {
		t.Fatalf("OriginalSizeBytes = %d, want %d", meta.OriginalSizeBytes, 16000)
	}
	if meta.CompressedSizeBytes != 4200 {
		t.Fatalf("CompressedSizeBytes = %d, want %d", meta.CompressedSizeBytes, 4200)
	}
	if meta.QuantizationTimeSeconds != 154 {
		t.Fatalf("QuantizationTimeSeconds = %d, want %d", meta.QuantizationTimeSeconds, 154)
	}
}

func TestParseQuantizationMetadata_Invalid(t *testing.T) {
	if _, err := parseQuantizationMetadata("not-json"); err == nil {
		t.Fatal("parseQuantizationMetadata should return an error for invalid JSON")
	}
}

func TestQuantizationMetadataFromPod_PrefersQuantizer(t *testing.T) {
	finishedSidecar := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	finishedQuantizer := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "sidecar",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Message:    `{"type":"Q8_0","originalSizeBytes":100,"compressedSizeBytes":80,"quantizationTimeSeconds":10}`,
							FinishedAt: finishedSidecar,
						},
					},
				},
				{
					Name: "quantizer",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Message:    `{"type":"Q4_K_M","originalSizeBytes":16000,"compressedSizeBytes":4200,"quantizationTimeSeconds":154}`,
							FinishedAt: finishedQuantizer,
						},
					},
				},
			},
		},
	}

	meta, finished := quantizationMetadataFromPod(pod)
	if meta == nil {
		t.Fatal("quantizationMetadataFromPod returned nil metadata")
	}
	if meta.Type != "Q4_K_M" {
		t.Fatalf("Type = %q, want %q", meta.Type, "Q4_K_M")
	}
	if !finished.Equal(finishedQuantizer.Time) {
		t.Fatalf("FinishedAt = %v, want %v", finished, finishedQuantizer.Time)
	}
}

func TestQuantizationDurationFromJobStatus(t *testing.T) {
	start := metav1.NewTime(time.Now())
	end := metav1.NewTime(start.Add(95 * time.Second))
	job := &batchv1.Job{
		Status: batchv1.JobStatus{
			StartTime:      &start,
			CompletionTime: &end,
		},
	}

	duration, ok := quantizationDurationFromJobStatus(job)
	if !ok {
		t.Fatal("quantizationDurationFromJobStatus should return ok=true")
	}
	if duration != 95*time.Second {
		t.Fatalf("duration = %v, want %v", duration, 95*time.Second)
	}
}

func TestQuantizationCompressionRatioAndFormatting(t *testing.T) {
	ratio, ok := quantizationCompressionRatio(15000, 4000)
	if !ok {
		t.Fatal("quantizationCompressionRatio should return ok=true for positive sizes")
	}
	if got := formatCompressionRatio(ratio); got != "3.75" {
		t.Fatalf("formatCompressionRatio = %q, want %q", got, "3.75")
	}
}

func TestQuantizationTypeFromSpec(t *testing.T) {
	gguf := &aiv1alpha1.QuantizationSpec{
		Format:   aiv1alpha1.QuantizationFormatGGUF,
		GGUFType: "Q5_K_M",
	}
	if got := quantizationTypeFromSpec(gguf); got != "Q5_K_M" {
		t.Fatalf("GGUF type = %q, want %q", got, "Q5_K_M")
	}

	awqBits := int32(4)
	awqGroup := int32(128)
	awq := &aiv1alpha1.QuantizationSpec{
		Format:    aiv1alpha1.QuantizationFormatAWQ,
		Bits:      &awqBits,
		GroupSize: &awqGroup,
		UseGPU:    true,
	}
	if got := quantizationTypeFromSpec(awq); got != "W4_G128" {
		t.Fatalf("AWQ type = %q, want %q", got, "W4_G128")
	}

	gptq := &aiv1alpha1.QuantizationSpec{
		Format: aiv1alpha1.QuantizationFormatGPTQ,
	}
	if got := quantizationTypeFromSpec(gptq); got != "W4_G128" {
		t.Fatalf("GPTQ default type = %q, want %q", got, "W4_G128")
	}
}

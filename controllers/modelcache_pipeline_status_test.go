package controllers

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestAbliterationCompleted(t *testing.T) {
	progress := int32(12)

	if abliterationCompleted(nil) {
		t.Fatal("nil status should not be complete")
	}

	if abliterationCompleted(&aiv1alpha1.AbliterationStatus{
		Progress:       &progress,
		ProgressDetail: "elapsed 3m",
	}) {
		t.Fatal("progress-only abliteration status should not be complete")
	}

	if !abliterationCompleted(&aiv1alpha1.AbliterationStatus{
		AbliterationTime: "14m32s",
	}) {
		t.Fatal("abliteration with completion time should be complete")
	}
}

func TestFinetuneCompleted(t *testing.T) {
	progress := int32(24)

	if finetuneCompleted(nil) {
		t.Fatal("nil status should not be complete")
	}

	if finetuneCompleted(&aiv1alpha1.FinetuneStatus{
		Progress:       &progress,
		ProgressDetail: "elapsed 9m",
	}) {
		t.Fatal("progress-only finetune status should not be complete")
	}

	if !finetuneCompleted(&aiv1alpha1.FinetuneStatus{
		FinetuneTime: "42m1s",
	}) {
		t.Fatal("finetune with completion time should be complete")
	}
}

func TestQuantizationCompleted(t *testing.T) {
	progress := int32(7)
	now := metav1.Now()

	if quantizationCompleted(nil) {
		t.Fatal("nil status should not be complete")
	}

	if quantizationCompleted(&aiv1alpha1.QuantizationStatus{
		Progress:       &progress,
		ProgressDetail: "elapsed 2m",
	}) {
		t.Fatal("progress-only quantization status should not be complete")
	}

	if !quantizationCompleted(&aiv1alpha1.QuantizationStatus{
		CompletedAt: &now,
	}) {
		t.Fatal("quantization with completedAt should be complete")
	}

	if !quantizationCompleted(&aiv1alpha1.QuantizationStatus{
		QuantizationTime: "33m10s",
	}) {
		t.Fatal("quantization with completion time should be complete")
	}
}

func TestAbliterationFailureNeedsRedownload(t *testing.T) {
	if !abliterationFailureNeedsRedownload("Timed out waiting for downloaded source weights in /cache/model") {
		t.Fatal("timeout waiting for source weights should trigger re-download")
	}

	if !abliterationFailureNeedsRedownload(`{"event":"abliteration_waiting_for_download","attempt":174,"marker":"present","weight_files":0}`) {
		t.Fatal("wait-loop telemetry with marker present and zero weight files should trigger re-download")
	}

	if !abliterationFailureNeedsRedownload("Download marker present but no source weight files exist in /cache/model") {
		t.Fatal("marker-only cache with zero weights should trigger re-download")
	}

	if !abliterationFailureNeedsRedownload(`Download marker present but source weights are incomplete in /cache/model (weight_files=13 expected=63 missing=50)`) {
		t.Fatal("incomplete cache with a completion marker should trigger re-download")
	}

	if abliterationFailureNeedsRedownload("CUDA out of memory while loading shards") {
		t.Fatal("ordinary model load failures should not trigger re-download")
	}
}

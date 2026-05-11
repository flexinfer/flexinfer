package controllers

import aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"

// downloadCompleted returns true when the download phase has completed successfully.
// The definitive signal is Status.Path being non-empty (set when the download job
// succeeds) combined with the phase having moved past Provisioning (or the current
// phase being explicitly set to a downstream phase).
func downloadCompleted(status *aiv1alpha1.ModelCacheStatus) bool {
	if status.Path == "" {
		return false
	}
	switch status.CurrentPhase {
	case "abliteration", "finetune", "quantization", "publish", "ready":
		return true
	}
	// If the phase is still Pending or empty, download hasn't finished.
	switch status.Phase {
	case "", aiv1alpha1.ModelCachePhasePending:
		return false
	}
	return true
}

func abliterationCompleted(status *aiv1alpha1.AbliterationStatus) bool {
	return status != nil && status.FailureMessage == "" && status.AbliterationTime != ""
}

func finetuneCompleted(status *aiv1alpha1.FinetuneStatus) bool {
	return status != nil && status.FailureMessage == "" && status.FinetuneTime != ""
}

func quantizationCompleted(status *aiv1alpha1.QuantizationStatus) bool {
	if status == nil || status.FailureMessage != "" {
		return false
	}
	return status.CompletedAt != nil || status.QuantizationTime != "" || status.CompressedSizeBytes > 0
}

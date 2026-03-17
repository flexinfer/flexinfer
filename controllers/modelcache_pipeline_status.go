package controllers

import aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"

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

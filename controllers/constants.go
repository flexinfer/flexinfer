package controllers

import "time"

const (
	huggingFaceRepositoryBaseURL = "https://huggingface.co"

	// Requeue intervals for reconcile loops. Using named constants makes
	// it easy to tune cadence across all controllers from a single place.
	requeueFast   = 3 * time.Second  // shared-GPU swap checks
	requeueShort  = 5 * time.Second  // waiting for pod/job readiness
	requeueMedium = 10 * time.Second // waiting for cache / LoRA load
	requeueLong   = 30 * time.Second // slow operations, default requeue

	// httpClientTimeout is the default timeout for outbound HTTP calls
	// made by controllers (e.g., runtime API, LoRA adapter loading).
	httpClientTimeout = 30 * time.Second

	// httpClientShort is the timeout for lightweight health/status checks.
	httpClientShort = 5 * time.Second

	// failedPodCutoff is how old a failed pod must be before the controller
	// garbage-collects it from the model deployment.
	failedPodCutoff = 5 * time.Minute

	// defaultEvictionThreshold is the default VRAM usage percentage above
	// which KV-cache eviction is triggered.
	defaultEvictionThreshold = int32(85)

	// defaultEvictionPriority is the default priority for KV-cache eviction
	// ordering when multiple models share a GPU group.
	defaultEvictionPriority = int32(50)

	// defaultDownloadBackoffLimit is the default number of retries for
	// download/quantization jobs before they are considered permanently failed.
	defaultDownloadBackoffLimit = int32(3)
)

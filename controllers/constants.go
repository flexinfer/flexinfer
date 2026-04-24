package controllers

import (
	"time"

	"github.com/flexinfer/flexinfer/pkg/modelmeta"
)

const (
	huggingFaceRepositoryBaseURL = "https://huggingface.co"

	// Requeue intervals for reconcile loops. Using named constants makes
	// it easy to tune cadence across all controllers from a single place.
	requeueFast   = 3 * time.Second  // shared-GPU swap checks
	requeueShort  = 5 * time.Second  // waiting for pod/job readiness
	requeueMedium = 10 * time.Second // waiting for cache / LoRA load
	requeueLong   = 30 * time.Second // slow operations, default requeue
	// runtimeLoadRetryBackoff suppresses duplicate runtime load requests for a
	// short window while the runtime is still surfacing model health.
	runtimeLoadRetryBackoff = 20 * time.Second

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

	// imageDriftGraceWindow is how long after a quantization job starts the
	// controller will preempt a running pod to pick up a new quantizer image
	// digest from the GPUProfile. Past this window, a long-running job is
	// assumed to be making real progress and is left alone — GPTQModel does
	// not resume mid-run, so a delete/recreate would cost hours of work.
	// Admins who need to force a new image mid-run can set
	// AnnotationForceImageUpdate on the ModelCache.
	imageDriftGraceWindow = 10 * time.Minute
)

// Annotation keys used on Jobs, Deployments, Pods, and Services.
const (
	AnnotationSource      = "flexinfer.ai/source"
	AnnotationCacheKind   = "flexinfer.ai/cache-kind"
	AnnotationCachePVC    = "flexinfer.ai/cache-pvc"
	AnnotationCachePvcUID = "flexinfer.ai/cache-pvc-uid"
	AnnotationCacheDest   = "flexinfer.ai/cache-dest"
	AnnotationCachePath   = "flexinfer.ai/cache-path"
	AnnotationCacheSrcPVC = "flexinfer.ai/cache-src-pvc"

	AnnotationServiceLabels = "flexinfer.ai/service-labels"
	AnnotationVRAMEstimate  = "flexinfer.ai/gpu.vram-estimate-mb"
	AnnotationKVCacheUsage  = "flexinfer.ai/kv-cache-usage"

	// AnnotationForceImageUpdate, when "true" on a ModelCache, bypasses the
	// imageDriftGraceWindow and lets the controller preempt a running
	// quantization job to pick up the current GPUProfile image. Used for
	// admin override; normal operation should never rely on this.
	AnnotationForceImageUpdate = "flexinfer.ai/force-image-update"

	// Quantized artifact promotion gate annotations. A Model with
	// AnnotationPromotionGate=quantized-artifact-v1 can run as a canary or
	// scale-to-zero model without evidence, but warm-primary promotion requires
	// validation evidence recorded on the object.
	AnnotationPromotionGate       = "flexinfer.ai/promotion-gate"
	AnnotationPromotionState      = "flexinfer.ai/promotion-state"
	AnnotationPromotionValidation = "flexinfer.ai/promotion-validation"
	AnnotationPromotionEvidence   = "flexinfer.ai/promotion-evidence"

	// LiteLLM proxy annotations.
	AnnotationLiteLLMServedModel     = "litellm.flexinfer.ai/served-model"
	AnnotationLiteLLMAliases         = "litellm.flexinfer.ai/aliases"
	AnnotationLiteLLMCopilot         = "litellm.flexinfer.ai/copilot-model"
	AnnotationLiteLLMCapabilities    = "litellm.flexinfer.ai/capabilities"
	AnnotationLiteLLMContextWindow   = modelmeta.AnnotationLiteLLMContextWindow
	AnnotationLiteLLMMaxInputTokens  = modelmeta.AnnotationLiteLLMMaxInputTokens
	AnnotationLiteLLMMaxOutputTokens = modelmeta.AnnotationLiteLLMMaxOutputTokens
)

// Label keys used on Pods, Jobs, and Deployments.
const (
	LabelModel          = "flexinfer.ai/model"
	LabelBackend        = "flexinfer.ai/backend"
	LabelGPUGroup       = "flexinfer.ai/gpu-group"
	LabelGPUArch        = "flexinfer.ai/gpu.arch"
	LabelGPUArchLegacy  = "flexinfer.ai/gpu-arch"
	LabelComponent      = "flexinfer.ai/component"
	LabelFormat         = "flexinfer.ai/format"
	LabelCache          = "flexinfer.ai/cache"
	LabelFederatedModel = "flexinfer.ai/federated-model"
)

// Container images used by cache/download jobs.
const (
	ImageAlpine     = "alpine:3.20"
	ImagePythonSlim = "python:3.10-slim"
	ImageDebianSlim = "debian:bookworm-slim"
	ImageORAS       = "ghcr.io/oras-project/oras:v1.2.2"
)

// Package constants defines shared annotation and label keys used across the
// flexinfer control plane (controller, scheduler, proxy, agent).
package constants

// Pod annotations used on model inference pods.
const (
	AnnotationModel          = "flexinfer.ai/model"
	AnnotationBackend        = "flexinfer.ai/backend"
	AnnotationVRAMEstimateMB = "flexinfer.ai/gpu.vram-estimate-mb"
)

// Node annotations written by the flexinfer-agent for scheduler scoring.
const (
	NodeAnnotationGPUUtil           = "flexinfer.ai/gpu.util"
	NodeAnnotationCost              = "flexinfer.ai/cost"
	NodeAnnotationKVCacheUsage      = "flexinfer.ai/kv-cache-usage"
	NodeAnnotationGPUFreeMemory     = "flexinfer.ai/gpu-free-memory"
	NodeAnnotationSpotTerminating   = "flexinfer.ai/spot-terminating"
	NodeAnnotationSpotTerminatingAt = "flexinfer.ai/spot-terminating-at"
)

// Service annotations for model routing.
const (
	ServiceAnnotationActiveLabels  = "ai.flexinfer/active-services"
	ServiceAnnotationServiceLabels = "flexinfer.ai/service-labels"
)

// LiteLLM service/deployment annotations for proxy discovery.
const (
	LiteLLMAnnotationServedModel  = "litellm.flexinfer.ai/served-model"
	LiteLLMAnnotationAliases      = "litellm.flexinfer.ai/aliases"
	LiteLLMAnnotationCopilotModel = "litellm.flexinfer.ai/copilot-model"
	LiteLLMAnnotationCapabilities = "litellm.flexinfer.ai/capabilities"
)

// Job annotations for cache operations.
const (
	JobAnnotationSource      = "flexinfer.ai/source"
	JobAnnotationCacheKind   = "flexinfer.ai/cache-kind"
	JobAnnotationCachePVC    = "flexinfer.ai/cache-pvc"
	JobAnnotationCacheDest   = "flexinfer.ai/cache-dest"
	JobAnnotationCachePath   = "flexinfer.ai/cache-path"
	JobAnnotationCacheSrcPVC = "flexinfer.ai/cache-src-pvc"
)

// Package constants defines shared annotation and label keys used across the
// flexinfer control plane (controller, scheduler, proxy, agent).
package constants

// Pod annotations used on model inference pods.
const (
	AnnotationModel          = "flexinfer.ai/model"
	AnnotationBackend        = "flexinfer.ai/backend"
	AnnotationVRAMEstimateMB = "flexinfer.ai/gpu.vram-estimate-mb"
)

// Model routing-policy annotations consumed by the proxy.
const (
	// AnnotationServePaths optionally restricts which inference path prefixes a
	// Model serves (comma-separated, e.g. "/v1/audio/transcriptions" for an
	// ASR-only model). The proxy rejects requests to any other path at the edge
	// without serving, cold-starting, or touching demand. Absent/empty means the
	// model serves all paths (default).
	AnnotationServePaths = "flexinfer.ai/serve-paths"

	// AnnotationServingPriorityClass, when set on a Model, is written to the
	// serving Deployment pod's PriorityClassName. Use it to protect a warm
	// retrieval/serving lane from GPU preemption by lower-priority ModelCache
	// transform/quant Jobs that share the same single-GPU node -- e.g. the
	// radeonvii embed/rerank plane set to "flexinfer-serving-critical" (150000),
	// above flexinfer-modelcache-transform (100000). The named PriorityClass MUST
	// already exist in the cluster or the pod fails admission; absent/empty means
	// no PriorityClass (the default, unchanged for every other model).
	AnnotationServingPriorityClass = "flexinfer.ai/serving-priority-class"
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

// GPU lease (training-vs-serving scheduling) labels and data keys.
//
// A GPU lease is a transient, scheduler-honored hold placed on a shared-GPU
// group by a training/quant workload so it can obtain the card without grabbing
// amd.com/gpu and contending with the serving leader. While an unexpired lease
// exists for a group, chooseSharedGroupLeaders parks every serving member (no
// leader) and keeps them parked until the lease is released or expires.
//
// Slice-1 carrier: a labeled ConfigMap named "gpu-lease-<group>". Slice 2 may
// promote this to a first-class GPULease CRD without changing the election
// contract (it only needs groupHasActiveLease).
const (
	// LabelGPULeaseGroup marks a ConfigMap as a GPU lease and records the
	// shared-GPU group it holds. Used to list active leases.
	LabelGPULeaseGroup = "ai.flexinfer/gpu-lease"

	// GPULeaseDataGroup is the shared-GPU group the lease holds.
	GPULeaseDataGroup = "group"
	// GPULeaseDataNode is the node whose card the lease holds (informational).
	GPULeaseDataNode = "node"
	// GPULeaseDataOwner is the owning workload (e.g. ModelCache name) for
	// observability and PreemptedBy attribution.
	GPULeaseDataOwner = "owner"
	// GPULeaseDataAcquiredAt is the RFC3339 acquisition timestamp.
	GPULeaseDataAcquiredAt = "acquiredAt"
	// GPULeaseDataExpiresAt is the RFC3339 TTL deadline. The election ignores a
	// lease once now >= expiresAt, so a dead acquirer cannot strand serving
	// forever (crash-safety backstop).
	GPULeaseDataExpiresAt = "expiresAt"
)

// GPULeaseConfigMapName returns the canonical lease ConfigMap name for a group.
func GPULeaseConfigMapName(group string) string {
	return "gpu-lease-" + group
}

// Job annotations for cache operations.
const (
	JobAnnotationSource      = "flexinfer.ai/source"
	JobAnnotationCacheKind   = "flexinfer.ai/cache-kind"
	JobAnnotationCachePVC    = "flexinfer.ai/cache-pvc"
	JobAnnotationCacheDest   = "flexinfer.ai/cache-dest"
	JobAnnotationCachePath   = "flexinfer.ai/cache-path"
	JobAnnotationCacheSrcPVC = "flexinfer.ai/cache-src-pvc"
)

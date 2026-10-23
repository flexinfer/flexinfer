package constants

// Node labels for GPU discovery set by the flexinfer-agent.
const (
	NodeLabelGPUVendor = "flexinfer.ai/gpu.vendor"
	NodeLabelGPUArch   = "flexinfer.ai/gpu.arch"
	NodeLabelGPUVRAM   = "flexinfer.ai/gpu.vram"
	NodeLabelGPUCount  = "flexinfer.ai/gpu.count"
	NodeLabelGPUInt4   = "flexinfer.ai/gpu.int4"

	// NodeLabelGPUHealth is the node agent's verdict on the node's discrete
	// GPUs: GPUHealthOK or GPUHealthDegraded. The scheduler extender filters
	// degraded nodes out and the controller treats them as non-matching for
	// Model placement, so a card that has dropped off the PCIe bus or whose SMU
	// stopped answering is parked automatically instead of eating 25-minute
	// cold-start timeouts (the 2026-10 RX 7900 XTX incident: five drops in a
	// week, each parked by hand with a bogus hostname selector). The reason
	// lives in NodeAnnotationGPUHealthReason.
	NodeLabelGPUHealth = "flexinfer.ai/gpu.health"
)

// Values of NodeLabelGPUHealth.
const (
	GPUHealthOK       = "ok"
	GPUHealthDegraded = "degraded"
)

// Resource labels applied to controller-managed objects.
const (
	LabelComponent      = "flexinfer.ai/component"
	LabelModel          = "flexinfer.ai/model"
	LabelBackend        = "flexinfer.ai/backend"
	LabelGPUGroup       = "flexinfer.ai/gpu-group"
	LabelFederatedModel = "flexinfer.ai/federated-model"
	LabelCache          = "flexinfer.ai/cache"
	LabelFormat         = "flexinfer.ai/format"
)

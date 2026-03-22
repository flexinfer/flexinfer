package constants

// Node labels for GPU discovery set by the flexinfer-agent.
const (
	NodeLabelGPUVendor = "flexinfer.ai/gpu.vendor"
	NodeLabelGPUArch   = "flexinfer.ai/gpu.arch"
	NodeLabelGPUVRAM   = "flexinfer.ai/gpu.vram"
	NodeLabelGPUCount  = "flexinfer.ai/gpu.count"
	NodeLabelGPUInt4   = "flexinfer.ai/gpu.int4"
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

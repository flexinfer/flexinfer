package runtime

import "errors"

// ErrModelBusy means a lifecycle operation must retry after accepted inference
// requests drain. The runtime reports it as HTTP 409 without stopping the engine.
var ErrModelBusy = errors.New("model is draining accepted requests")

// ErrInsufficientVRAM means another subprocess must release memory before load.
var ErrInsufficientVRAM = errors.New("insufficient VRAM for model admission")

// ErrInsufficientHWQueues means the GPU's hardware compute queues are
// committed (GPUProfile maxHWQueues); another subprocess must exit before load.
// The runtime reports it as HTTP 503, like ErrInsufficientVRAM.
var ErrInsufficientHWQueues = errors.New("insufficient hardware compute queues for model admission")

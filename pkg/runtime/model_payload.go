package runtime

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// RuntimeModelBasePath is the runtime pods' model mount root. Payload paths
// for pvc:// and Local-cache sources resolve under it.
const RuntimeModelBasePath = "/models"

// NewModelLoadPayload renders the runtime load request for model. It is the
// one builder behind both load paths: the controller's reconcile path and the
// proxy's direct-load fast path. Given the same Model, adapters and GPUProfile
// they send byte-identical payloads, so a load the proxy wins carries the
// Model's LoRA options and the launch SpecHash the controller compares
// against, instead of starting a lane without its adapters that the controller
// then no-ops on or reloads.
//
// reader lists the LoRAAdapter CRs targeting the Model; a list error fails the
// render rather than loading the lane without its adapters. vendor is the GPU
// vendor the caller resolved (the controller's node detection, or
// ModelGPUVendor); when it is not a concrete vendor the GPUProfile's applies.
func NewModelLoadPayload(ctx context.Context, reader client.Reader, model *aiv1alpha2.Model, b backend.Backend, vendor backend.GPUVendor, profile *aiv1alpha2.GPUProfileSpec) (*LoadPayload, error) {
	if model == nil {
		return nil, fmt.Errorf("model is required")
	}
	if b == nil {
		return nil, fmt.Errorf("backend is required")
	}
	opts := BuildLoadOptions{
		ModelBasePath: RuntimeModelBasePath,
		GPUVendor:     vendor,
		GPUProfile:    profile,
	}
	// LoRA support flows through the payload config so the runtime launches
	// vLLM with the same --enable-lora flags a dedicated Deployment would get.
	if ls, ok := b.(backend.LoRASupporter); ok && ls.SupportsLoRA() {
		count, maxRank, err := LoRALaunchConfig(ctx, reader, model)
		if err != nil {
			return nil, err
		}
		opts.LoRAAdapters, opts.LoRAMaxRank = count, maxRank
	}
	return NewLoadPayloadForModel(model, b, opts)
}

// LoRALaunchConfig returns the LoRA launch parameters for model: the number of
// LoRAAdapter CRs in its namespace whose modelRef names it, and the largest
// rank they declare. count == 0 means LoRA stays off.
func LoRALaunchConfig(ctx context.Context, reader client.Reader, model *aiv1alpha2.Model) (count, maxRank int, err error) {
	if reader == nil || model == nil {
		return 0, 0, nil
	}
	list := &aiv1alpha2.LoRAAdapterList{}
	if err := reader.List(ctx, list, client.InNamespace(model.Namespace)); err != nil {
		return 0, 0, fmt.Errorf("listing LoRA adapters for %s: %w", model.Name, err)
	}
	for i := range list.Items {
		la := &list.Items[i]
		if la.Spec.ModelRef != model.Name {
			continue
		}
		count++
		if la.Spec.MaxRank != nil && int(*la.Spec.MaxRank) > maxRank {
			maxRank = int(*la.Spec.MaxRank)
		}
	}
	return count, maxRank, nil
}

// ModelGPUVendor is the GPU vendor a Model's spec pins, resolved the way the
// controller's node detection resolves it without reading nodes: no GPUs (or
// an explicit cpu) is CPU, amd and nvidia are themselves, and auto is unknown
// so the payload builder falls back to the GPUProfile's vendor.
func ModelGPUVendor(model *aiv1alpha2.Model) backend.GPUVendor {
	if model == nil {
		return backend.GPUVendorUnknown
	}
	if model.Spec.GetGPUCount() == 0 || model.Spec.GetGPUVendor() == aiv1alpha2.GPUVendorCPU {
		return backend.GPUVendorCPU
	}
	switch model.Spec.GetGPUVendor() {
	case aiv1alpha2.GPUVendorAMD:
		return backend.GPUVendorAMD
	case aiv1alpha2.GPUVendorNVIDIA:
		return backend.GPUVendorNVIDIA
	default:
		return backend.GPUVendorUnknown
	}
}

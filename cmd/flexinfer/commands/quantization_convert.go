/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commands

import (
	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// convertQuantizationSpecV2toV1 converts a v1alpha2.QuantizationSpec to the
// v1alpha1 equivalent for patching v1alpha1 ModelCache objects.
func convertQuantizationSpecV2toV1(in *aiv1alpha2.QuantizationSpec) *aiv1alpha1.QuantizationSpec {
	if in == nil {
		return nil
	}
	out := &aiv1alpha1.QuantizationSpec{
		Format:            aiv1alpha1.QuantizationFormat(in.Format),
		GGUFType:          in.GGUFType,
		Bits:              in.Bits,
		GroupSize:         in.GroupSize,
		UseGPU:            in.UseGPU,
		MaxMemoryGB:       in.MaxMemoryGB,
		TimeoutSeconds:    in.TimeoutSeconds,
		Sym:               in.Sym,
		DescAct:           in.DescAct,
		GPUMemoryFraction: in.GPUMemoryFraction,
		DynamicExclusion:  in.DynamicExclusion,
	}
	if in.Calibration != nil {
		out.Calibration = &aiv1alpha1.CalibrationSpec{
			MaxSeqLen:             in.Calibration.MaxSeqLen,
			MaxSamples:            in.Calibration.MaxSamples,
			NParallelCalibSamples: in.Calibration.NParallelCalibSamples,
			Dataset:               in.Calibration.Dataset,
		}
	}
	return out
}

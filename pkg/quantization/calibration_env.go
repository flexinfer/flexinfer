package quantization

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// CalibrationParams holds resolved calibration values with defaults applied.
type CalibrationParams struct {
	MaxSeqLen             int32
	MaxSamples            int32
	Dataset               string
	NParallelCalibSamples *int32
}

// ResolveCalibration extracts calibration parameters from the spec, applying
// defaults for any unset fields. This eliminates repeated nil-check boilerplate
// across AWQ, GPTQ, and future calibration-based quantization builders.
func ResolveCalibration(calib *aiv1alpha2.CalibrationSpec) CalibrationParams {
	p := CalibrationParams{
		MaxSeqLen:  int32(DefaultCalibrationMaxSeqLen),
		MaxSamples: int32(DefaultCalibrationMaxSamples),
		Dataset:    DefaultCalibrationDataset,
	}
	if calib == nil {
		return p
	}
	if calib.MaxSeqLen != nil {
		p.MaxSeqLen = *calib.MaxSeqLen
	}
	if calib.MaxSamples != nil {
		p.MaxSamples = *calib.MaxSamples
	}
	if calib.Dataset != nil {
		p.Dataset = *calib.Dataset
	}
	if calib.NParallelCalibSamples != nil {
		p.NParallelCalibSamples = calib.NParallelCalibSamples
	}
	return p
}

// BuildCalibrationEnv returns the common calibration environment variables
// (MAX_SEQ_LEN, MAX_SAMPLES, DATASET, and optionally N_PARALLEL_CALIB_SAMPLES).
func BuildCalibrationEnv(calib *aiv1alpha2.CalibrationSpec) []corev1.EnvVar {
	p := ResolveCalibration(calib)
	env := []corev1.EnvVar{
		{Name: "MAX_SEQ_LEN", Value: fmt.Sprintf("%d", p.MaxSeqLen)},
		{Name: "MAX_SAMPLES", Value: fmt.Sprintf("%d", p.MaxSamples)},
		{Name: "DATASET", Value: p.Dataset},
	}
	if p.NParallelCalibSamples != nil {
		env = append(env, corev1.EnvVar{
			Name:  "N_PARALLEL_CALIB_SAMPLES",
			Value: fmt.Sprintf("%d", *p.NParallelCalibSamples),
		})
	}
	return env
}

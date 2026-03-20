package quantization

import (
	"fmt"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestResolveCalibration_Defaults(t *testing.T) {
	p := ResolveCalibration(nil)

	if p.MaxSeqLen != int32(DefaultCalibrationMaxSeqLen) {
		t.Errorf("MaxSeqLen = %d, want %d", p.MaxSeqLen, DefaultCalibrationMaxSeqLen)
	}
	if p.MaxSamples != int32(DefaultCalibrationMaxSamples) {
		t.Errorf("MaxSamples = %d, want %d", p.MaxSamples, DefaultCalibrationMaxSamples)
	}
	if p.Dataset != DefaultCalibrationDataset {
		t.Errorf("Dataset = %q, want %q", p.Dataset, DefaultCalibrationDataset)
	}
	if p.NParallelCalibSamples != nil {
		t.Errorf("NParallelCalibSamples = %v, want nil", p.NParallelCalibSamples)
	}
}

func TestResolveCalibration_CustomValues(t *testing.T) {
	seqLen := int32(2048)
	samples := int32(64)
	dataset := "custom/dataset"
	parallel := int32(32)

	calib := &aiv1alpha1.CalibrationSpec{
		MaxSeqLen:             &seqLen,
		MaxSamples:            &samples,
		Dataset:               &dataset,
		NParallelCalibSamples: &parallel,
	}
	p := ResolveCalibration(calib)

	if p.MaxSeqLen != seqLen {
		t.Errorf("MaxSeqLen = %d, want %d", p.MaxSeqLen, seqLen)
	}
	if p.MaxSamples != samples {
		t.Errorf("MaxSamples = %d, want %d", p.MaxSamples, samples)
	}
	if p.Dataset != dataset {
		t.Errorf("Dataset = %q, want %q", p.Dataset, dataset)
	}
	if p.NParallelCalibSamples == nil || *p.NParallelCalibSamples != parallel {
		t.Errorf("NParallelCalibSamples = %v, want %d", p.NParallelCalibSamples, parallel)
	}
}

func TestBuildCalibrationEnv_Defaults(t *testing.T) {
	envVars := BuildCalibrationEnv(nil)

	envMap := make(map[string]string)
	for _, e := range envVars {
		envMap[e.Name] = e.Value
	}

	if envMap["MAX_SEQ_LEN"] != fmt.Sprintf("%d", DefaultCalibrationMaxSeqLen) {
		t.Errorf("MAX_SEQ_LEN = %q, want %q", envMap["MAX_SEQ_LEN"], fmt.Sprintf("%d", DefaultCalibrationMaxSeqLen))
	}
	if envMap["MAX_SAMPLES"] != fmt.Sprintf("%d", DefaultCalibrationMaxSamples) {
		t.Errorf("MAX_SAMPLES = %q, want %q", envMap["MAX_SAMPLES"], fmt.Sprintf("%d", DefaultCalibrationMaxSamples))
	}
	if envMap["DATASET"] != DefaultCalibrationDataset {
		t.Errorf("DATASET = %q, want %q", envMap["DATASET"], DefaultCalibrationDataset)
	}
	if _, ok := envMap["N_PARALLEL_CALIB_SAMPLES"]; ok {
		t.Error("N_PARALLEL_CALIB_SAMPLES should not be present with nil calib")
	}
}

func TestBuildCalibrationEnv_WithParallel(t *testing.T) {
	parallel := int32(16)
	calib := &aiv1alpha1.CalibrationSpec{
		NParallelCalibSamples: &parallel,
	}
	envVars := BuildCalibrationEnv(calib)

	envMap := make(map[string]string)
	for _, e := range envVars {
		envMap[e.Name] = e.Value
	}

	if envMap["N_PARALLEL_CALIB_SAMPLES"] != "16" {
		t.Errorf("N_PARALLEL_CALIB_SAMPLES = %q, want %q", envMap["N_PARALLEL_CALIB_SAMPLES"], "16")
	}
}

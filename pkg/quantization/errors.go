package quantization

import "errors"

var (
	// ErrUnsupportedFormat is returned when a quantization format is not recognized.
	ErrUnsupportedFormat = errors.New("unsupported quantization format")

	// ErrGPURequired is returned when quantization requires GPU but useGPU is false.
	ErrGPURequired = errors.New("quantization requires useGPU=true")

	// ErrInvalidBits is returned for unsupported bit widths.
	ErrInvalidBits = errors.New("unsupported bit width")

	// ErrInvalidGroupSize is returned when groupSize is invalid.
	ErrInvalidGroupSize = errors.New("groupSize must be > 0")

	// ErrFormatNotConfigured is returned when a quantization format is wired in
	// but missing required runtime/image configuration to launch jobs.
	ErrFormatNotConfigured = errors.New("quantization format is not configured for job execution")
)

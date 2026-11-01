package backend

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// WhisperCppBackend runs whisper.cpp's whisper-server inside flexinfer-runtime
// as an OpenAI /v1/audio/transcriptions lane. Only runtime images that build
// whisper.cpp (gfx906 today) can host it; it has no standalone image.
//
// whisper-server also serves POST /load, which loads any model path on the
// server. Models expose it only through flexinfer.ai/serve-paths, which must
// stay limited to /v1/audio/transcriptions.
type WhisperCppBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&WhisperCppBackend{})
}

func (b *WhisperCppBackend) Name() string {
	return NameWhisperCpp
}

func (b *WhisperCppBackend) Aliases() []string {
	return []string{"whisper.cpp", "whisper-cpp"}
}

func (b *WhisperCppBackend) Image(GPUVendor, string) string {
	// whisper-server is a subprocess in the runtime image, not a standalone image.
	return ""
}

func (b *WhisperCppBackend) Port() int32 {
	return PortWhisperCpp
}

// Command returns the runtime image path. The runtime falls back to a PATH
// lookup of the basename when the path is absent.
func (b *WhisperCppBackend) Command() []string {
	return []string{"/opt/whispercpp/bin/whisper-server"}
}

// Args builds the whisper-server command line. Config keys:
//   - modelFile: ggml weights inside the staged cache (joined to ModelPath)
//   - language: default "auto"; a request's language field overrides it
//   - threads: CPU threads for mel and sampling, default 4
//   - flashAttention: false passes --no-flash-attn (whisper.cpp defaults on)
//   - beamSize / bestOf: decoder search, server defaults when unset
func (b *WhisperCppBackend) Args(spec *ModelSpec) []string {
	port := spec.ConfigInt("port", int(b.Port()))
	args := []string{
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", port),
		"--inference-path", WhisperCppInferencePath,
		"--language", spec.ConfigString("language", "auto"),
		"--threads", fmt.Sprintf("%d", spec.ConfigInt("threads", 4)),
		// OpenAI verbose_json carries no language_probabilities; skipping
		// them saves one encoder pass per request.
		"--no-language-probabilities",
	}
	// llamaCppModelPath joins modelFile without double-joining a ModelPath the
	// runtime payload already resolved to the file.
	if modelPath := llamaCppModelPath(spec); modelPath != "" {
		args = append(args, "--model", modelPath)
	} else if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.GPUVendor == GPUVendorCPU {
		args = append(args, "--no-gpu")
	}
	if flashAttention, ok := spec.ConfigOptionalBool("flashAttention"); ok && !flashAttention {
		args = append(args, "--no-flash-attn")
	}
	if beam := spec.ConfigInt("beamSize", 0); beam > 0 {
		args = append(args, "--beam-size", fmt.Sprintf("%d", beam))
	}
	if bestOf := spec.ConfigInt("bestOf", 0); bestOf > 0 {
		args = append(args, "--best-of", fmt.Sprintf("%d", bestOf))
	}
	return args
}

// SupportsGPUVendor includes CPU: whisper-server runs there with --no-gpu.
func (b *WhisperCppBackend) SupportsGPUVendor(vendor GPUVendor) bool {
	return vendor == GPUVendorCPU || vendor == GPUVendorAMD || vendor == GPUVendorNVIDIA
}

func (b *WhisperCppBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	var env []corev1.EnvVar
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
	}
	return env
}

func (b *WhisperCppBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", PortWhisperCpp, 0, 5, 3)
}

func (b *WhisperCppBackend) StartupProbe() *corev1.Probe {
	return HTTPStartupProbe("/health", PortWhisperCpp, b.StartupTimeout())
}

func (b *WhisperCppBackend) StartupTimeout() time.Duration {
	return 5 * time.Minute
}

func (b *WhisperCppBackend) DefaultIdleTimeout() time.Duration {
	return 30 * time.Minute
}

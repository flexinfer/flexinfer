package backend

import (
	"fmt"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	// DefaultPyannotePipeline is the gated HF pipeline the server loads when
	// the Model names none. Its segmentation-3.0 sub-model is gated too, so
	// the token's account must accept both model cards.
	DefaultPyannotePipeline = "pyannote/speaker-diarization-3.1"

	// RuntimeSecretsDir is where a runtime pod mounts the Secrets its bundled
	// backends read, one directory per Secret (<dir>/<secret>/<key>). Secret
	// values never travel in a load payload or a subprocess command line.
	RuntimeSecretsDir = "/var/run/flexinfer/secrets"

	// DefaultHFTokenSecretKey is the key read from spec.config.hfTokenSecret.
	DefaultHFTokenSecretKey = "HF_TOKEN"

	// PyannotePython is the interpreter of the pyannote layer's venv. It sees
	// the runtime's torch and fastapi (--system-site-packages) but keeps
	// pyannote.audio's pins (huggingface_hub < 1.0) out of the runtime's own
	// site-packages, which diffusers and transformers 5 need newer.
	PyannotePython = "/opt/pyannote/bin/python"
	// PyannoteServerScript is the bundled server the venv runs.
	PyannoteServerScript = "/opt/flexinfer/server-pyannote.py"
)

// PyannoteBackend runs pyannote.audio speaker diarization inside
// flexinfer-runtime as a /v1/audio/diarizations lane (D5-a of
// .loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.5).
// It has no image of its own: the bundled build/server-pyannote.py needs the
// pyannote layer (build/Dockerfile.runtime-gfx906-pyannote, a venv at
// /opt/pyannote) on a runtime with gfx906 torch kernels, and the controller refuses the backend on any
// GPUProfile whose runtime image carries no pyannote/speaker-diarization
// certificate (controllers/model_backend_certificate.go).
//
// Config keys:
//   - hfTokenSecret: Secret holding the HF token for the gated pipeline. The
//     runtime pod must mount it at RuntimeSecretsDir/<name> (the gfx906
//     profile's extraVolumes); the server reads the file named by
//     PYANNOTE_HF_TOKEN_FILE.
//   - hfTokenSecretKey: key in that Secret, default HF_TOKEN.
//   - pipeline: HF pipeline id, overriding the id taken from spec.source.
//   - device: "cuda" (default) or "cpu"; cuda falls back to CPU only when the
//     server's GPU probe fails, and says so in /health.
type PyannoteBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&PyannoteBackend{})
}

func (b *PyannoteBackend) Name() string {
	return NamePyannote
}

func (b *PyannoteBackend) Aliases() []string {
	return []string{"pyannote-audio", "pyannote.audio"}
}

func (b *PyannoteBackend) Image(GPUVendor, string) string {
	// server-pyannote.py is a subprocess in the runtime image.
	return ""
}

// Command runs the server with the layer's venv interpreter. On a runtime
// without the layer the runtime falls back to python on PATH, and the load
// fails importing pyannote; the certificate gate keeps it from getting there.
func (b *PyannoteBackend) Command() []string {
	return []string{PyannotePython, PyannoteServerScript}
}

func (b *PyannoteBackend) Port() int32 {
	return PortPyannote
}

func (b *PyannoteBackend) Args(*ModelSpec) []string {
	return nil
}

// Env carries everything the server reads. The HF token is passed by file
// path, never by value.
func (b *PyannoteBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "PYANNOTE_PIPELINE", Value: PyannotePipeline(spec)},
		{Name: "PYANNOTE_DEVICE", Value: spec.ConfigString("device", "cuda")},
		{Name: "PORT", Value: fmt.Sprintf("%d", spec.ConfigInt("port", int(b.Port())))},
	}
	if file := PyannoteHFTokenFile(spec.Config); file != "" {
		env = append(env, corev1.EnvVar{Name: "PYANNOTE_HF_TOKEN_FILE", Value: file})
	}
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
	}
	return env
}

// SupportsGPUVendor includes CPU: pyannote runs there, slowly.
func (b *PyannoteBackend) SupportsGPUVendor(vendor GPUVendor) bool {
	return vendor == GPUVendorCPU || vendor == GPUVendorAMD || vendor == GPUVendorNVIDIA
}

func (b *PyannoteBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", PortPyannote, 0, 5, 3)
}

func (b *PyannoteBackend) StartupProbe() *corev1.Probe {
	return HTTPStartupProbe("/health", PortPyannote, b.StartupTimeout())
}

// StartupTimeout covers the first load's download of the gated sub-models
// (~50 MiB) and the torch import.
func (b *PyannoteBackend) StartupTimeout() time.Duration {
	return 5 * time.Minute
}

// NeedsVolume is false: the server pulls the pipeline from the HF hub into
// the runtime's HF cache, as server-snac.py does.
func (b *PyannoteBackend) NeedsVolume() bool {
	return false
}

func (b *PyannoteBackend) DefaultIdleTimeout() time.Duration {
	return 30 * time.Minute
}

// PyannotePipeline is the HF pipeline id a pyannote Model loads:
// config.pipeline, else the id from spec.source, else the default.
func PyannotePipeline(spec *ModelSpec) string {
	if p := strings.TrimSpace(spec.ConfigString("pipeline", "")); p != "" {
		return p
	}
	if m := strings.TrimSpace(spec.Model); m != "" {
		return m
	}
	return DefaultPyannotePipeline
}

// PyannoteHFTokenFile is the token path for config.hfTokenSecret and
// hfTokenSecretKey, or "" when no valid Secret is named.
func PyannoteHFTokenFile(cfg map[string]any) string {
	name, key, err := PyannoteHFTokenSecret(cfg)
	if err != nil || name == "" {
		return ""
	}
	return path.Join(RuntimeSecretsDir, name, key)
}

// PyannoteHFTokenSecret returns the Secret name and key a pyannote Model
// reads its HF token from. name is "" when hfTokenSecret is unset; err
// reports a value that is not a Secret name or key.
func PyannoteHFTokenSecret(cfg map[string]any) (name, key string, err error) {
	spec := &ModelSpec{Config: cfg}
	name = strings.TrimSpace(spec.ConfigString("hfTokenSecret", ""))
	key = strings.TrimSpace(spec.ConfigString("hfTokenSecretKey", DefaultHFTokenSecretKey))
	if name == "" {
		return "", "", nil
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "", "", fmt.Errorf("spec.config.hfTokenSecret %q is not a Secret name: %s", name, strings.Join(errs, "; "))
	}
	if errs := validation.IsConfigMapKey(key); len(errs) > 0 {
		return "", "", fmt.Errorf("spec.config.hfTokenSecretKey %q is not a Secret key: %s", key, strings.Join(errs, "; "))
	}
	return name, key, nil
}

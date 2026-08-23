package backend

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// SNACBackend runs the bundled SNAC waveform decoder inside flexinfer-runtime.
type SNACBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&SNACBackend{})
}

func (b *SNACBackend) Name() string {
	return NameSNAC
}

func (b *SNACBackend) Image(GPUVendor, string) string {
	// SNAC is a subprocess in the unified runtime image, not a standalone image.
	return ""
}

func (b *SNACBackend) Port() int32 {
	return PortSNAC
}

func (b *SNACBackend) Args(*ModelSpec) []string {
	return nil
}

func (b *SNACBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	modelID := spec.Model
	if modelID == "" {
		modelID = "hubertsiuzdak/snac_24khz"
	}
	port := spec.ConfigInt("port", int(b.Port()))
	env := []corev1.EnvVar{
		{Name: "SNAC_MODEL_ID", Value: modelID},
		{Name: "PORT", Value: fmt.Sprintf("%d", port)},
	}
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
	}
	return env
}

func (b *SNACBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", PortSNAC, 0, 5, 3)
}

func (b *SNACBackend) StartupProbe() *corev1.Probe {
	return HTTPStartupProbe("/health", PortSNAC, b.StartupTimeout())
}

func (b *SNACBackend) StartupTimeout() time.Duration {
	return 5 * time.Minute
}

func (b *SNACBackend) NeedsVolume() bool {
	return false
}

func (b *SNACBackend) DefaultIdleTimeout() time.Duration {
	return 5 * time.Minute
}

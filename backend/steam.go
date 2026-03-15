package backend

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

// SteamBackend implements the Backend interface for headless Steam gaming.
// When loaded via the runtime manager, it launches a headless Steam process
// that accepts Remote Play connections, enabling GPU gaming mode on inference nodes.
type SteamBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&SteamBackend{})
}

func (b *SteamBackend) Name() string {
	return "steam"
}

func (b *SteamBackend) Aliases() []string {
	return []string{"gaming", "steam-headless"}
}

func (b *SteamBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	// Steam runs as a subprocess inside the runtime pod — no standalone image.
	return ""
}

func (b *SteamBackend) Port() int32 {
	return 27036 // Steam Remote Play
}

func (b *SteamBackend) Command() []string {
	return []string{"steam"}
}

func (b *SteamBackend) Args(spec *ModelSpec) []string {
	return []string{"-no-browser", "-silent", "-tcp"}
}

func (b *SteamBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	return nil
}

func (b *SteamBackend) ReadinessProbe() *corev1.Probe {
	// TCP check on Steam Remote Play port.
	return TCPReadinessProbe(27036, 5, 10, 5)
}

func (b *SteamBackend) StartupTimeout() time.Duration {
	return 60 * time.Second
}

func (b *SteamBackend) NeedsVolume() bool {
	return false
}

func (b *SteamBackend) SupportsGPUVendor(vendor GPUVendor) bool {
	return vendor != GPUVendorCPU
}

func (b *SteamBackend) DefaultIdleTimeout() time.Duration {
	return 0 // Never auto-idle gaming sessions.
}

package backend

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

// SteamBackend implements the Backend interface for headless Steam Remote Play.
//
// DEPRECATED / EXPERIMENTAL: the Xvfb software-GL path here never ran a game
// (see .loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md). The default
// gaming backend is now SunshineBackend (Sunshine + gamescope + Mesa RADV +
// VA-API HW encode). Steam is kept only for explicit opt-in via the "steam"
// name; it no longer claims the "gaming" alias, so SetMode(gaming) resolves to
// Sunshine.
type SteamBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&SteamBackend{})
}

func (b *SteamBackend) Name() string {
	return NameSteam
}

func (b *SteamBackend) Aliases() []string {
	// The "gaming" alias now belongs to SunshineBackend. Keep only the
	// Steam-specific alias so existing "steam-headless" references still work.
	return []string{"steam-headless"}
}

func (b *SteamBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	// Steam runs as a subprocess inside the runtime pod — no standalone image.
	return ""
}

func (b *SteamBackend) Port() int32 {
	return PortSteam
}

func (b *SteamBackend) Command() []string {
	return []string{"/opt/flexinfer/steam-headless.sh"}
}

func (b *SteamBackend) Args(spec *ModelSpec) []string {
	return nil
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

package backend

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

// SunshineBackend implements the Backend interface for headless GPU game
// streaming via Sunshine (a self-hosted Moonlight host).
//
// When loaded via the runtime manager (NodeMode=gaming), it launches a
// headless GPU-accelerated Wayland session (gamescope on Mesa RADV) and a
// Sunshine host that Moonlight clients pair against. This is the default
// gaming backend, replacing the Xvfb software-GL SteamBackend; Steam still
// works as a game *launched under* Sunshine.
//
// The AMD/RDNA3 (gfx1100) substrate — Mesa RADV Vulkan render + VA-API
// hardware encode (H.264/HEVC/AV1) inside a privileged container with
// /dev/dri — was validated by the Slice 1 kill-test
// (.loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md).
type SunshineBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&SunshineBackend{})
}

func (b *SunshineBackend) Name() string {
	return NameSunshine
}

func (b *SunshineBackend) Aliases() []string {
	// "gaming" resolves to Sunshine by default; SetMode(gaming) loads this
	// backend unless overridden by the gaming-backend config.
	return []string{"gaming", "moonlight", "sunshine-headless"}
}

func (b *SunshineBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	// Sunshine runs as a subprocess inside the runtime pod (gaming image
	// layer) — no standalone image.
	return ""
}

func (b *SunshineBackend) Port() int32 {
	return PortSunshine
}

func (b *SunshineBackend) Command() []string {
	return []string{"/opt/flexinfer/sunshine-headless.sh"}
}

func (b *SunshineBackend) Args(spec *ModelSpec) []string {
	return nil
}

func (b *SunshineBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	// The launch script (build/sunshine-headless.sh) reads GAMING_* env vars
	// from the runtime environment; nothing model-derived to inject here.
	return nil
}

func (b *SunshineBackend) ReadinessProbe() *corev1.Probe {
	// TCP check on Sunshine's Moonlight HTTP control port. Startup is slow
	// (compositor + Sunshine init + first-run steam/proton runtime), so allow
	// a generous initial delay before probing.
	return TCPReadinessProbe(PortSunshine, 10, 10, 5)
}

func (b *SunshineBackend) StartupTimeout() time.Duration {
	// Headless compositor + Sunshine + first-run client-runtime download can
	// take a while on cold start.
	return 120 * time.Second
}

func (b *SunshineBackend) NeedsVolume() bool {
	// Gaming state (Sunshine pairing/config, the Steam library) is persisted
	// via a dedicated volume wired at the DaemonSet layer (Slice 4), not the
	// per-model /models volume mechanism.
	return false
}

func (b *SunshineBackend) SupportsGPUVendor(vendor GPUVendor) bool {
	// Needs a real graphics GPU (RADV/NVENC). CPU-only cannot render/encode.
	return vendor != GPUVendorCPU
}

func (b *SunshineBackend) DefaultIdleTimeout() time.Duration {
	// Never auto-idle a gaming session from the backend default. An optional
	// no-client idle guard is a separate, opt-in feature (Slice 5).
	return 0
}

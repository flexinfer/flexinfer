package controllers

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// gfx1100CertifiedRuntimes is the set of runtime images allowed to back the
// gfx1100 runtime DaemonSets (deploy/system/values-k3s.yaml runtime.profiles)
// and the gfx1100 GPUProfile spec.runtime.image, keyed by digest, with the
// evidence that admitted each one. A repin adds its digest here with that
// evidence; dropping a digest needs every consumer and the rollback anchor
// moved first. The tests below fail when one side of a repin lands without
// the other.
var gfx1100CertifiedRuntimes = map[string]string{
	// rocm-gfx1100-serving-b8637f35 (vLLM 0.17 lineage): the XTX runtime
	// DaemonSet since 2026-10-23, matrix row gfx1100-serving-2026-10-23.
	// Rollback anchor for the D1 repin.
	"sha256:cfefae18061fd2f9f07cfbfb9b0c2abb51cb4ab34dabda86f3969c13eca9f278": "gfx1100-serving-2026-10-23",
	// rocm-gfx1100-serving-v032-c444b5ff (vLLM 0.32 stacked serving persona,
	// S6a !1174, spawn contract verified at build time). Its supervisor
	// predates the R1/R4/C5 runtime work; fallback repin target only.
	"sha256:ef28b7b0d1cb0103953a296ae4aeff8c567b5548002ad1e000b39a77853208f2": "S6a !1174",
	// rocm-gfx1100-serving-v032-5ca58769: the master CI rebuild of the same
	// Dockerfile and frozen stack (pipeline 36312, build-time verify passed)
	// with the R1 reload, R4 load-substage and C5 shared-cache supervisor. The
	// D1 repin target before the creative-lane deps.
	"sha256:7ae8e92ff28a99a301c3f9df42a7e8d50f174161c7140794cf208e5e9e024250": "CI pipeline 36312 job 442027",
	// rocm-gfx1100-serving-v032-ddf22437: 7ae8e92f's Dockerfile and frozen
	// stack plus the creative-lane deps (bitsandbytes 0.50.2 with its ROCm
	// 7.2 library, torchsde, imageio + imageio-ffmpeg, ftfy; !1269), the Wan
	// VAE MIOpen bypass (!1268) and the D5-a diarization backend. Build-time
	// verify passed. The recommended D1 repin target.
	"sha256:527e2714c163f3c284bcba7e168590e8cb8960e2b1cf247b00976bb7b8f30d0b": "CI pipeline 37264 job 460293",
}

var runtimeDigestPattern = regexp.MustCompile(`^registry\.harbor\.lan/flexinfer/runtime@(sha256:[0-9a-f]{64})$`)

type runtimePinValues struct {
	ImagePrewarm struct {
		Profiles []struct {
			Name         string            `json:"name"`
			NodeSelector map[string]string `json:"nodeSelector"`
			Images       []string          `json:"images"`
		} `json:"profiles"`
	} `json:"imagePrewarm"`
	Runtime struct {
		Profiles []runtimePinProfile `json:"profiles"`
	} `json:"runtime"`
}

type runtimePinProfile struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	GPUArch      string            `json:"gpuArch"`
	NodeSelector map[string]string `json:"nodeSelector"`
	Parked       bool              `json:"parked"`
}

func loadRuntimePinValues(t *testing.T) runtimePinValues {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "system", "values-k3s.yaml"))
	if err != nil {
		t.Fatalf("read values-k3s.yaml: %v", err)
	}
	var values runtimePinValues
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("decode values-k3s.yaml: %v", err)
	}
	if len(values.Runtime.Profiles) == 0 || len(values.ImagePrewarm.Profiles) == 0 {
		t.Fatalf("values-k3s.yaml has no runtime or imagePrewarm profiles")
	}
	return values
}

func loadGfx1100GPUProfile(t *testing.T) *aiv1alpha2.GPUProfile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "gpuprofiles", "gfx1100.yaml"))
	if err != nil {
		t.Fatalf("read gfx1100.yaml: %v", err)
	}
	profile := &aiv1alpha2.GPUProfile{}
	if err := yaml.UnmarshalStrict(raw, profile); err != nil {
		t.Fatalf("decode gfx1100.yaml: %v", err)
	}
	if profile.Spec.Runtime == nil || profile.Spec.Runtime.Image == "" {
		t.Fatalf("gfx1100 GPUProfile has no spec.runtime.image")
	}
	return profile
}

func runtimeDigest(t *testing.T, where, image string) string {
	t.Helper()
	m := runtimeDigestPattern.FindStringSubmatch(image)
	if m == nil {
		t.Fatalf("%s pins %q, want registry.harbor.lan/flexinfer/runtime@sha256:<digest>", where, image)
	}
	return m[1]
}

// prewarmedRuntimeDigests returns the runtime digests the prewarm DaemonSet
// holds on the node the selector names.
func prewarmedRuntimeDigests(values runtimePinValues, hostname string) map[string]bool {
	held := map[string]bool{}
	for _, p := range values.ImagePrewarm.Profiles {
		if p.NodeSelector["kubernetes.io/hostname"] != hostname {
			continue
		}
		for _, image := range p.Images {
			if m := runtimeDigestPattern.FindStringSubmatch(image); m != nil {
				held[m[1]] = true
			}
		}
	}
	return held
}

func gfx1100RuntimeProfiles(t *testing.T, values runtimePinValues) []runtimePinProfile {
	t.Helper()
	var out []runtimePinProfile
	for _, p := range values.Runtime.Profiles {
		if p.GPUArch == "gfx1100" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Fatalf("values-k3s.yaml has no gfx1100 runtime profile")
	}
	return out
}

// TestGfx1100RuntimePinsAreCertified: every gfx1100 runtime DaemonSet profile,
// parked or not, and the GPUProfile pin a certified runtime digest.
func TestGfx1100RuntimePinsAreCertified(t *testing.T) {
	for digest, evidence := range gfx1100CertifiedRuntimes {
		if evidence == "" {
			t.Errorf("certified runtime %s names no evidence", digest)
		}
	}
	values := loadRuntimePinValues(t)
	for _, p := range gfx1100RuntimeProfiles(t, values) {
		digest := runtimeDigest(t, "runtime profile "+p.Name, p.Image)
		if _, ok := gfx1100CertifiedRuntimes[digest]; !ok {
			t.Errorf("runtime profile %s pins %s, which is not in gfx1100CertifiedRuntimes", p.Name, digest)
		}
	}
	gpuProfile := loadGfx1100GPUProfile(t)
	digest := runtimeDigest(t, "gfx1100 GPUProfile spec.runtime.image", gpuProfile.Spec.Runtime.Image)
	if _, ok := gfx1100CertifiedRuntimes[digest]; !ok {
		t.Errorf("gfx1100 GPUProfile spec.runtime.image pins %s, which is not in gfx1100CertifiedRuntimes", digest)
	}
}

// TestGfx1100GPUProfileRuntimeImageFollowsLiveDaemonSet: the GPUProfile
// documents the image the live (unparked) gfx1100 runtime DaemonSet runs, so a
// repin moves both in one change. A parked profile (desired=0) may trail.
func TestGfx1100GPUProfileRuntimeImageFollowsLiveDaemonSet(t *testing.T) {
	values := loadRuntimePinValues(t)
	gpuProfile := loadGfx1100GPUProfile(t)
	live := 0
	for _, p := range gfx1100RuntimeProfiles(t, values) {
		if p.Parked {
			continue
		}
		live++
		if p.Image != gpuProfile.Spec.Runtime.Image {
			t.Errorf("runtime profile %s pins %s but the gfx1100 GPUProfile spec.runtime.image pins %s; repin both together",
				p.Name, p.Image, gpuProfile.Spec.Runtime.Image)
		}
	}
	if live == 0 {
		t.Fatalf("no unparked gfx1100 runtime profile: the GPUProfile pin follows nothing")
	}
}

// TestRuntimePinsArePrewarmedOnTheirNode: every live runtime DaemonSet image
// is held by its node's prewarm DaemonSet, and every certified gfx1100 digest
// is held on each node a live gfx1100 runtime runs on, so a repin and its
// rollback are container starts and Harbor's pinned-* guard keeps both.
func TestRuntimePinsArePrewarmedOnTheirNode(t *testing.T) {
	values := loadRuntimePinValues(t)
	for _, p := range values.Runtime.Profiles {
		if p.Parked {
			continue
		}
		hostname := p.NodeSelector["kubernetes.io/hostname"]
		if hostname == "" {
			t.Errorf("runtime profile %s has no kubernetes.io/hostname nodeSelector", p.Name)
			continue
		}
		held := prewarmedRuntimeDigests(values, hostname)
		digest := runtimeDigest(t, "runtime profile "+p.Name, p.Image)
		if !held[digest] {
			t.Errorf("runtime profile %s pins %s, which no imagePrewarm profile holds on %s", p.Name, digest, hostname)
		}
		if p.GPUArch != "gfx1100" {
			continue
		}
		var missing []string
		for certified := range gfx1100CertifiedRuntimes {
			if !held[certified] {
				missing = append(missing, certified)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("certified gfx1100 runtimes %v are not prewarmed on %s (runtime profile %s)", missing, hostname, p.Name)
		}
	}
}

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
	// 2026-11-24 (XTX prewarm trim): three certified digests that nothing
	// pinned were dropped from the XTX prewarm list and so from this map
	// (TestRuntimePinsArePrewarmedOnTheirNode holds every entry here on the
	// XTX). To restore one, put it back here and in the prewarm list:
	//   sha256:ef28b7b0d1cb0103953a296ae4aeff8c567b5548002ad1e000b39a77853208f2
	//     rocm-gfx1100-serving-v032-c444b5ff, "S6a !1174": fallback only, its
	//     supervisor predates the R1/R4/C5 runtime work.
	//   sha256:7ae8e92ff28a99a301c3f9df42a7e8d50f174161c7140794cf208e5e9e024250
	//     rocm-gfx1100-serving-v032-5ca58769, "CI pipeline 36312 job 442027":
	//     the D1 target before the creative-lane deps.
	//   sha256:12f30206d9e6abf4b470fe1012b85b9236aa7ef901eb6ea918b6fe43721a42ba
	//     rocm-gfx1100-serving-v032-527e2714-dfo-fb8182fc, "overlay build
	//     2026-11-21": the first diffusers overlay, two pins behind the live one.
	// rocm-gfx1100-serving-v032-ddf22437: the vLLM 0.32 stacked serving
	// persona's Dockerfile and frozen stack plus the creative-lane deps
	// (bitsandbytes 0.50.2 with its ROCm 7.2 library, torchsde, imageio +
	// imageio-ffmpeg, ftfy; !1269), the Wan
	// VAE MIOpen bypass (!1268) and the D5-a diarization backend. Build-time
	// verify passed. The recommended D1 repin target.
	"sha256:527e2714c163f3c284bcba7e168590e8cb8960e2b1cf247b00976bb7b8f30d0b": "CI pipeline 37264 job 460293",
	// rocm-gfx1100-serving-v032-527e2714-dfo-a056a493: 527e2714 with only
	// server-diffusers.py replaced, built off-CI from
	// build/Dockerfile.runtime-serving-v032-diffusers-overlay with the base
	// image's build-time verifier re-run. Master a056a493's server carries
	// the non-finite render guard and the opt-in TORCH_DETERMINISTIC and
	// exact-shape video warmup (!1379), the bounded Brownian-tree noise
	// sampler (!1389) for in-pod Stable Audio and the
	// Wan VACE exact-shape warmup with a conditioning frame (!1399). Base
	// verifier re-run. The D1 Stable Audio and VACE repin target.
	"sha256:a8d525e02c2106856a20768349b6708334eff1f5f53ecd849170fa134564a409": "overlay build 2026-11-22, Dockerfile.runtime-serving-v032-diffusers-overlay",
	// rocm-gfx1100-serving-v032-527e2714-dfo-75a382f7: the same overlay with
	// master 75a382f7's server-diffusers.py, which adds the WAN_VAE_MIOPEN
	// switch (!1407) for the in-pod VACE float32 VAE. Base verifier re-run.
	"sha256:c54cad123fd667bd82413e102e785348347148856b144d1fff1ef894901ab0c7": "overlay build 2026-11-22, Dockerfile.runtime-serving-v032-diffusers-overlay",
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
	Name               string            `json:"name"`
	Image              string            `json:"image"`
	GPUArch            string            `json:"gpuArch"`
	NodeSelector       map[string]string `json:"nodeSelector"`
	Parked             bool              `json:"parked"`
	SharedCompileCache bool              `json:"sharedCompileCache"`
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

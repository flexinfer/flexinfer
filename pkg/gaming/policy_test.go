package gaming

import (
	"testing"
	"time"
)

func TestDefaultPolicyIsSecure(t *testing.T) {
	p := Default()
	if len(p.ApprovedImages) != 0 {
		t.Fatalf("default policy must approve no images, got %v", p.ApprovedImages)
	}
	if !p.RequireOwner {
		t.Fatal("default policy must require an owner")
	}
	if p.DefaultTTL <= 0 || p.MaxTTL <= 0 || p.DrainTimeout <= 0 {
		t.Fatalf("default bounds must be positive: %+v", p)
	}
}

func TestParsePolicy(t *testing.T) {
	p := ParsePolicy(map[string]string{
		KeyApprovedImages: "# canary passed 2026-09-01\nregistry.harbor.lan/flexinfer/runtime@sha256:aaa\nsha256:bbb,  registry.example/x:tag  \n\n",
		KeyDefaultTTL:     "90m",
		KeyMaxTTL:         "6h",
		KeyDrainTimeout:   "5m",
		KeyRequireOwner:   "false",
	})
	want := []string{"registry.harbor.lan/flexinfer/runtime@sha256:aaa", "sha256:bbb", "registry.example/x:tag"}
	if len(p.ApprovedImages) != len(want) {
		t.Fatalf("approved = %v, want %v", p.ApprovedImages, want)
	}
	for i := range want {
		if p.ApprovedImages[i] != want[i] {
			t.Fatalf("approved[%d] = %q, want %q", i, p.ApprovedImages[i], want[i])
		}
	}
	if p.DefaultTTL != 90*time.Minute || p.MaxTTL != 6*time.Hour || p.DrainTimeout != 5*time.Minute {
		t.Fatalf("durations wrong: %+v", p)
	}
	if p.RequireOwner {
		t.Fatal("requireOwner=false not honored")
	}
}

func TestParsePolicyBadValuesKeepDefaults(t *testing.T) {
	p := ParsePolicy(map[string]string{
		KeyDefaultTTL:   "not-a-duration",
		KeyMaxTTL:       "-2h",
		KeyRequireOwner: "maybe",
	})
	d := Default()
	if p.DefaultTTL != d.DefaultTTL || p.MaxTTL != d.MaxTTL || p.RequireOwner != d.RequireOwner {
		t.Fatalf("bad values must keep defaults: %+v vs %+v", p, d)
	}
}

func TestImageApproved(t *testing.T) {
	p := Policy{ApprovedImages: []string{
		"registry.harbor.lan/flexinfer/runtime@sha256:aaa",
		"sha256:bbb",
	}}
	cases := []struct {
		image, imageID string
		want           bool
	}{
		{"registry.harbor.lan/flexinfer/runtime@sha256:aaa", "", true},
		{"", "registry.harbor.lan/flexinfer/runtime@sha256:aaa", true},
		{"registry.harbor.lan/flexinfer/runtime@sha256:bbb", "", true}, // bare digest entry
		{"other.registry/x@sha256:bbb", "", true},                      // bare digest matches any repo
		{"registry.harbor.lan/flexinfer/runtime@sha256:ccc", "", false},
		{"registry.harbor.lan/flexinfer/runtime:latest", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := p.ImageApproved(tc.image, tc.imageID); got != tc.want {
			t.Fatalf("ImageApproved(%q, %q) = %v, want %v", tc.image, tc.imageID, got, tc.want)
		}
	}
	if (Policy{}).ImageApproved("anything@sha256:aaa", "") {
		t.Fatal("empty policy must approve nothing")
	}
}

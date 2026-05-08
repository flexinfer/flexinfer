/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha2

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newGPUProfile(name string, annotations map[string]string) *GPUProfile {
	return &GPUProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "flexinfer",
			Annotations: annotations,
		},
		Spec: GPUProfileSpec{
			Architecture: "gfx1100",
			Vendor:       "amd",
		},
	}
}

func TestBackendCanaryAnnotationKeys(t *testing.T) {
	if got, want := BackendCanaryAnnotationKey("vllm"), "flexinfer.ai/backend-canary-vllm"; got != want {
		t.Errorf("BackendCanaryAnnotationKey(vllm) = %q, want %q", got, want)
	}
	if got, want := BackendCanarySinceAnnotationKey("vllm"), "flexinfer.ai/backend-canary-vllm-since"; got != want {
		t.Errorf("BackendCanarySinceAnnotationKey(vllm) = %q, want %q", got, want)
	}
	if got, want := BackendCanaryEvidenceAnnotationKey("vllm"), "flexinfer.ai/backend-canary-vllm-evidence"; got != want {
		t.Errorf("BackendCanaryEvidenceAnnotationKey(vllm) = %q, want %q", got, want)
	}
}

func TestGetBackendCanary_NoAnnotations(t *testing.T) {
	tests := []struct {
		name    string
		profile *GPUProfile
	}{
		{name: "nil profile", profile: nil},
		{name: "nil annotations map", profile: newGPUProfile("gfx1100", nil)},
		{name: "empty annotations map", profile: newGPUProfile("gfx1100", map[string]string{})},
		{name: "annotations without canary keys", profile: newGPUProfile("gfx1100", map[string]string{
			"flexinfer.ai/some-other": "value",
		})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isCanary, since, evidence := GetBackendCanary(tc.profile, "vllm")
			if isCanary {
				t.Errorf("isCanary = true, want false")
			}
			if !since.IsZero() {
				t.Errorf("since = %v, want zero time", since)
			}
			if evidence != "" {
				t.Errorf("evidence = %q, want empty", evidence)
			}
		})
	}
}

func TestGetBackendCanary_EmptyBackendName(t *testing.T) {
	profile := newGPUProfile("gfx1100", map[string]string{
		BackendCanaryAnnotationKey("vllm"): "true",
	})
	isCanary, since, evidence := GetBackendCanary(profile, "")
	if isCanary || !since.IsZero() || evidence != "" {
		t.Errorf("empty backend name should return zero tuple, got (%v, %v, %q)", isCanary, since, evidence)
	}
}

func TestSetBackendCanary_RoundtripGet(t *testing.T) {
	profile := newGPUProfile("gfx1100", nil)
	evidence := "https://docs.example.com/canary/vllm-gfx1100"

	before := time.Now().UTC().Add(-time.Second)
	SetBackendCanary(profile, "vllm", evidence)
	after := time.Now().UTC().Add(time.Second)

	isCanary, since, gotEvidence := GetBackendCanary(profile, "vllm")
	if !isCanary {
		t.Fatalf("isCanary = false, want true after Set")
	}
	if gotEvidence != evidence {
		t.Errorf("evidence = %q, want %q", gotEvidence, evidence)
	}
	if since.Before(before) || since.After(after) {
		t.Errorf("since = %v, want between %v and %v", since, before, after)
	}

	// Verify the underlying annotation keys are populated.
	annotations := profile.GetAnnotations()
	if got := annotations[BackendCanaryAnnotationKey("vllm")]; got != "true" {
		t.Errorf("canary annotation = %q, want %q", got, "true")
	}
	if _, err := time.Parse(time.RFC3339, annotations[BackendCanarySinceAnnotationKey("vllm")]); err != nil {
		t.Errorf("since annotation not RFC3339-parseable: %v", err)
	}
	if got := annotations[BackendCanaryEvidenceAnnotationKey("vllm")]; got != evidence {
		t.Errorf("evidence annotation = %q, want %q", got, evidence)
	}
}

func TestSetBackendCanary_OverwriteRefreshesTimestamp(t *testing.T) {
	profile := newGPUProfile("gfx1100", nil)

	SetBackendCanary(profile, "vllm", "https://first.example.com")
	_, firstSince, firstEvidence := GetBackendCanary(profile, "vllm")
	if firstEvidence != "https://first.example.com" {
		t.Fatalf("first evidence = %q, want %q", firstEvidence, "https://first.example.com")
	}

	// Sleep just enough to guarantee a different RFC3339 second.
	time.Sleep(1100 * time.Millisecond)
	SetBackendCanary(profile, "vllm", "https://second.example.com")
	_, secondSince, secondEvidence := GetBackendCanary(profile, "vllm")

	if !secondSince.After(firstSince) {
		t.Errorf("secondSince (%v) not after firstSince (%v)", secondSince, firstSince)
	}
	if secondEvidence != "https://second.example.com" {
		t.Errorf("second evidence = %q, want %q", secondEvidence, "https://second.example.com")
	}
}

func TestSetBackendCanary_NilProfileNoOp(t *testing.T) {
	// Should not panic.
	SetBackendCanary(nil, "vllm", "https://example.com")
}

func TestSetBackendCanary_EmptyBackendNoOp(t *testing.T) {
	profile := newGPUProfile("gfx1100", nil)
	SetBackendCanary(profile, "", "https://example.com")
	if got := len(profile.GetAnnotations()); got != 0 {
		t.Errorf("empty backend wrote %d annotations, want 0", got)
	}
}

func TestClearBackendCanary_RemovesAllThreeKeys(t *testing.T) {
	profile := newGPUProfile("gfx1100", nil)
	SetBackendCanary(profile, "vllm", "https://example.com")

	ClearBackendCanary(profile, "vllm")

	isCanary, since, evidence := GetBackendCanary(profile, "vllm")
	if isCanary {
		t.Errorf("isCanary = true, want false after Clear")
	}
	if !since.IsZero() {
		t.Errorf("since = %v, want zero time after Clear", since)
	}
	if evidence != "" {
		t.Errorf("evidence = %q, want empty after Clear", evidence)
	}

	annotations := profile.GetAnnotations()
	for _, key := range []string{
		BackendCanaryAnnotationKey("vllm"),
		BackendCanarySinceAnnotationKey("vllm"),
		BackendCanaryEvidenceAnnotationKey("vllm"),
	} {
		if _, ok := annotations[key]; ok {
			t.Errorf("annotation %q still present after Clear", key)
		}
	}
}

func TestClearBackendCanary_PreservesOtherAnnotations(t *testing.T) {
	profile := newGPUProfile("gfx1100", map[string]string{
		"unrelated.example.com/key": "preserved",
	})
	SetBackendCanary(profile, "vllm", "https://example.com")

	ClearBackendCanary(profile, "vllm")

	if got := profile.GetAnnotations()["unrelated.example.com/key"]; got != "preserved" {
		t.Errorf("unrelated annotation = %q, want %q", got, "preserved")
	}
}

func TestClearBackendCanary_NilOrEmptyNoOp(t *testing.T) {
	// nil profile should not panic.
	ClearBackendCanary(nil, "vllm")

	// empty backend should leave annotations untouched.
	profile := newGPUProfile("gfx1100", nil)
	SetBackendCanary(profile, "vllm", "https://example.com")
	annotationsBefore := len(profile.GetAnnotations())
	ClearBackendCanary(profile, "")
	if got := len(profile.GetAnnotations()); got != annotationsBefore {
		t.Errorf("annotation count after Clear with empty backend = %d, want %d", got, annotationsBefore)
	}

	// profile with no annotations should be a no-op.
	bare := newGPUProfile("gfx1100", nil)
	ClearBackendCanary(bare, "vllm")
	if got := len(bare.GetAnnotations()); got != 0 {
		t.Errorf("Clear on bare profile produced %d annotations, want 0", got)
	}
}

func TestBackendCanary_MultipleBackendsCoexist(t *testing.T) {
	profile := newGPUProfile("gfx1100", nil)

	SetBackendCanary(profile, "vllm", "https://docs.example.com/canary/vllm")
	SetBackendCanary(profile, "diffusers", "https://docs.example.com/canary/diffusers")
	SetBackendCanary(profile, "comfyui", "https://docs.example.com/canary/comfyui")

	for _, backend := range []string{"vllm", "diffusers", "comfyui"} {
		isCanary, since, evidence := GetBackendCanary(profile, backend)
		if !isCanary {
			t.Errorf("backend %q: isCanary = false, want true", backend)
		}
		if since.IsZero() {
			t.Errorf("backend %q: since is zero, want non-zero", backend)
		}
		if evidence != "https://docs.example.com/canary/"+backend {
			t.Errorf("backend %q: evidence = %q", backend, evidence)
		}
	}

	// Clearing one backend leaves the others intact.
	ClearBackendCanary(profile, "diffusers")

	isCanary, _, _ := GetBackendCanary(profile, "vllm")
	if !isCanary {
		t.Errorf("vllm canary was cleared by diffusers Clear")
	}
	isCanary, _, _ = GetBackendCanary(profile, "comfyui")
	if !isCanary {
		t.Errorf("comfyui canary was cleared by diffusers Clear")
	}
	isCanary, since, evidence := GetBackendCanary(profile, "diffusers")
	if isCanary || !since.IsZero() || evidence != "" {
		t.Errorf("diffusers Clear left residue: (%v, %v, %q)", isCanary, since, evidence)
	}
}

func TestGetBackendCanary_MalformedAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantCanary  bool
		wantZero    bool   // since should be zero
		wantEvid    string // evidence
	}{
		{
			name: "boolean false explicitly",
			annotations: map[string]string{
				BackendCanaryAnnotationKey("vllm"): "false",
			},
			wantCanary: false,
			wantZero:   true,
			wantEvid:   "",
		},
		{
			name: "boolean truthy with whitespace",
			annotations: map[string]string{
				BackendCanaryAnnotationKey("vllm"): "  TRUE  ",
			},
			wantCanary: true,
			wantZero:   true,
			wantEvid:   "",
		},
		{
			name: "boolean garbage",
			annotations: map[string]string{
				BackendCanaryAnnotationKey("vllm"): "yes",
			},
			wantCanary: false,
			wantZero:   true,
			wantEvid:   "",
		},
		{
			name: "since unparseable",
			annotations: map[string]string{
				BackendCanaryAnnotationKey("vllm"):      "true",
				BackendCanarySinceAnnotationKey("vllm"): "not-a-timestamp",
			},
			wantCanary: true,
			wantZero:   true,
			wantEvid:   "",
		},
		{
			name: "since valid, evidence empty",
			annotations: map[string]string{
				BackendCanaryAnnotationKey("vllm"):      "true",
				BackendCanarySinceAnnotationKey("vllm"): "2026-05-06T12:00:00Z",
			},
			wantCanary: true,
			wantZero:   false,
			wantEvid:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := newGPUProfile("gfx1100", tc.annotations)
			isCanary, since, evidence := GetBackendCanary(profile, "vllm")
			if isCanary != tc.wantCanary {
				t.Errorf("isCanary = %v, want %v", isCanary, tc.wantCanary)
			}
			if tc.wantZero && !since.IsZero() {
				t.Errorf("since = %v, want zero", since)
			}
			if !tc.wantZero && since.IsZero() {
				t.Errorf("since is zero, want non-zero")
			}
			if evidence != tc.wantEvid {
				t.Errorf("evidence = %q, want %q", evidence, tc.wantEvid)
			}
		})
	}
}

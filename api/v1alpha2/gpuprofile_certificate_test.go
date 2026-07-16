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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBackendCapabilityCertificateLifecycle(t *testing.T) {
	profile := &GPUProfile{ObjectMeta: metav1.ObjectMeta{Name: "gfx906"}}
	image := "registry.example/llamacpp@sha256:0123456789abcdef"
	evidence := ".loom/gfx906-llamacpp-proof.md"

	SetBackendCapabilityCertificate(profile, "llamacpp", "stateful-slots", image, evidence)
	gotImage, since, gotEvidence := GetBackendCapabilityCertificate(profile, "llamacpp", "stateful-slots")
	if gotImage != image {
		t.Fatalf("certificate image = %q, want %q", gotImage, image)
	}
	if since.IsZero() {
		t.Fatal("certificate timestamp is zero")
	}
	if gotEvidence != evidence {
		t.Fatalf("certificate evidence = %q, want %q", gotEvidence, evidence)
	}

	ClearBackendCapabilityCertificate(profile, "llamacpp", "stateful-slots")
	gotImage, since, gotEvidence = GetBackendCapabilityCertificate(profile, "llamacpp", "stateful-slots")
	if gotImage != "" || !since.IsZero() || gotEvidence != "" {
		t.Fatalf("certificate survived clear: image=%q since=%v evidence=%q", gotImage, since, gotEvidence)
	}
}

func TestSetBackendCapabilityCertificateRejectsPartialCertificate(t *testing.T) {
	profile := &GPUProfile{ObjectMeta: metav1.ObjectMeta{Name: "gfx906"}}
	SetBackendCapabilityCertificate(profile, "llamacpp", "stateful-slots", "registry.example/image@sha256:test", "")
	if len(profile.GetAnnotations()) != 0 {
		t.Fatalf("partial certificate mutated annotations: %#v", profile.GetAnnotations())
	}
}

func TestGetBackendCapabilityCertificateTreatsMalformedTimestampAsZero(t *testing.T) {
	profile := &GPUProfile{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		BackendCapabilityCertificateAnnotationKey("llamacpp", "ngram-simple"):         "registry.example/image@sha256:test",
		BackendCapabilityCertificateSinceAnnotationKey("llamacpp", "ngram-simple"):    "not-a-time",
		BackendCapabilityCertificateEvidenceAnnotationKey("llamacpp", "ngram-simple"): "proof.md",
	}}}

	_, since, _ := GetBackendCapabilityCertificate(profile, "llamacpp", "ngram-simple")
	if !since.IsZero() {
		t.Fatalf("malformed timestamp parsed as %v", since)
	}
}

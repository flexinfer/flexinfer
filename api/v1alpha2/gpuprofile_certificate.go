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
	"strings"
	"time"
)

// Backend capability certificates bind an opt-in backend feature to the exact
// image artifact that passed its hardware kill-test. They live on GPUProfile
// annotations because they are probe evidence, not desired-state settings.
//
// Three keys move together for each backend and capability:
//
//   - flexinfer.ai/backend-certificate-<backend>-<capability>: digest-pinned image
//   - flexinfer.ai/backend-certificate-<backend>-<capability>-since: RFC3339 time
//   - flexinfer.ai/backend-certificate-<backend>-<capability>-evidence: URL or path
//
// Callers must treat a partial triple as uncertified. In particular, the
// image value must match the artifact selected for the workload; a boolean
// architecture-level capability would allow an untested tag or older runtime
// binary to inherit the certificate.
const (
	BackendCertificateAnnotationPrefix = "flexinfer.ai/backend-certificate-"
	backendCertificateSinceSuffix      = "-since"
	backendCertificateEvidenceSuffix   = "-evidence"

	// LlamaCppCapabilityStatefulSlots certifies --slot-save-path on the
	// selected artifact.
	LlamaCppCapabilityStatefulSlots = "stateful-slots"
	// LlamaCppCapabilityNgramSimpleDraft16 certifies the exact speculative
	// envelope proven on gfx906: ngram-simple, draft-max=16, draft-min=1.
	LlamaCppCapabilityNgramSimpleDraft16 = "ngram-simple-draft16-min1"
)

func BackendCapabilityCertificateAnnotationKey(backend, capability string) string {
	return BackendCertificateAnnotationPrefix + backend + "-" + capability
}

func BackendCapabilityCertificateSinceAnnotationKey(backend, capability string) string {
	return BackendCapabilityCertificateAnnotationKey(backend, capability) + backendCertificateSinceSuffix
}

func BackendCapabilityCertificateEvidenceAnnotationKey(backend, capability string) string {
	return BackendCapabilityCertificateAnnotationKey(backend, capability) + backendCertificateEvidenceSuffix
}

// GetBackendCapabilityCertificate returns the image, timestamp, and evidence
// stored for a backend capability. Empty backend/capability values, nil
// profiles, missing annotations, and malformed timestamps return zero values.
func GetBackendCapabilityCertificate(profile *GPUProfile, backend, capability string) (image string, since time.Time, evidence string) {
	if profile == nil || strings.TrimSpace(backend) == "" || strings.TrimSpace(capability) == "" {
		return "", time.Time{}, ""
	}
	annotations := profile.GetAnnotations()
	if len(annotations) == 0 {
		return "", time.Time{}, ""
	}

	image = strings.TrimSpace(annotations[BackendCapabilityCertificateAnnotationKey(backend, capability)])
	if raw := strings.TrimSpace(annotations[BackendCapabilityCertificateSinceAnnotationKey(backend, capability)]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			since = parsed
		}
	}
	evidence = strings.TrimSpace(annotations[BackendCapabilityCertificateEvidenceAnnotationKey(backend, capability)])
	return image, since, evidence
}

// SetBackendCapabilityCertificate records a complete certificate triple on a
// GPUProfile. The profile is mutated in memory; callers persist it. Empty
// backend, capability, image, or evidence values are rejected with no mutation
// because incomplete certificates must never pass a fail-closed gate.
func SetBackendCapabilityCertificate(profile *GPUProfile, backend, capability, image, evidence string) {
	backend = strings.TrimSpace(backend)
	capability = strings.TrimSpace(capability)
	image = strings.TrimSpace(image)
	evidence = strings.TrimSpace(evidence)
	if profile == nil || backend == "" || capability == "" || image == "" || evidence == "" {
		return
	}
	annotations := profile.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 3)
	}
	annotations[BackendCapabilityCertificateAnnotationKey(backend, capability)] = image
	annotations[BackendCapabilityCertificateSinceAnnotationKey(backend, capability)] = time.Now().UTC().Format(time.RFC3339)
	annotations[BackendCapabilityCertificateEvidenceAnnotationKey(backend, capability)] = evidence
	profile.SetAnnotations(annotations)
}

// ClearBackendCapabilityCertificate removes the complete certificate triple.
func ClearBackendCapabilityCertificate(profile *GPUProfile, backend, capability string) {
	if profile == nil || strings.TrimSpace(backend) == "" || strings.TrimSpace(capability) == "" {
		return
	}
	annotations := profile.GetAnnotations()
	if len(annotations) == 0 {
		return
	}
	delete(annotations, BackendCapabilityCertificateAnnotationKey(backend, capability))
	delete(annotations, BackendCapabilityCertificateSinceAnnotationKey(backend, capability))
	delete(annotations, BackendCapabilityCertificateEvidenceAnnotationKey(backend, capability))
	profile.SetAnnotations(annotations)
}

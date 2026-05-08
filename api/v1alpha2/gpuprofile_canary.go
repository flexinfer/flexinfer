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

// BackendCanary status annotation contract.
//
// The contract ships as ObjectMeta annotations on a GPUProfile, not as new CRD
// spec fields. This was the design recommendation in slice 1 of the
// gfx1100/gfx906 platform plan (see
// docs/planning/gpuprofile-contract-followups.md priority 4): canary state is
// per-arch+per-backend status that mutates outside the spec lifecycle, so
// annotations avoid CRD schema churn and let operators or canary-lane
// tooling write directly without reconciling the spec.
//
// Three keys exist per backend:
//
//   - flexinfer.ai/backend-canary-<backend>          : "true" | "false"
//     Whether the backend is currently in canary mode on this GPU
//     architecture. Absent annotation is equivalent to "false" — production.
//
//   - flexinfer.ai/backend-canary-<backend>-since    : RFC3339 timestamp
//     When the canary state was last set. Updated by SetBackendCanary on
//     every call so cleared+set cycles produce a fresh timestamp. Absent or
//     unparseable -> zero time.
//
//   - flexinfer.ai/backend-canary-<backend>-evidence : URL or path string
//     Free-form pointer to the validation matrix row, runbook, MR, or
//     dashboard documenting why canary mode is active. Absent -> "".
//
// The three keys move together: SetBackendCanary writes all three,
// ClearBackendCanary deletes all three. GetBackendCanary surfaces the tuple
// and treats partial annotations as best-effort (the boolean is the only
// field that gates behavior; since/evidence are advisory).
//
// Helpers in this file mutate the GPUProfile object in memory only — callers
// are responsible for persisting the change with a controller client Update
// or Patch. This keeps the contract testable without requiring a fake client
// and matches the slice-1..5 helper style.
const (
	// BackendCanaryAnnotationPrefix is the common prefix for all
	// BackendCanary annotation keys.
	BackendCanaryAnnotationPrefix = "flexinfer.ai/backend-canary-"

	// backendCanarySinceSuffix is appended to the canary key to form the
	// "-since" timestamp annotation key.
	backendCanarySinceSuffix = "-since"

	// backendCanaryEvidenceSuffix is appended to the canary key to form the
	// "-evidence" pointer annotation key.
	backendCanaryEvidenceSuffix = "-evidence"
)

// BackendCanaryAnnotationKey returns the boolean canary annotation key for a
// backend (e.g. "flexinfer.ai/backend-canary-vllm").
func BackendCanaryAnnotationKey(backend string) string {
	return BackendCanaryAnnotationPrefix + backend
}

// BackendCanarySinceAnnotationKey returns the "-since" timestamp annotation
// key for a backend.
func BackendCanarySinceAnnotationKey(backend string) string {
	return BackendCanaryAnnotationPrefix + backend + backendCanarySinceSuffix
}

// BackendCanaryEvidenceAnnotationKey returns the "-evidence" pointer
// annotation key for a backend.
func BackendCanaryEvidenceAnnotationKey(backend string) string {
	return BackendCanaryAnnotationPrefix + backend + backendCanaryEvidenceSuffix
}

// GetBackendCanary reports whether a backend is in canary mode on the given
// GPUProfile and returns the timestamp + evidence pointer recorded with the
// canary state.
//
// Returned values:
//
//   - isCanary  : true only when the canary annotation parses to a truthy
//     value ("true"). Any other value (including absent or
//     malformed) returns false. The strict parse keeps the
//     gate conservative: callers should treat "we couldn't tell"
//     as "not canary" rather than failing safe-open.
//   - since     : RFC3339-parsed timestamp from the "-since" annotation.
//     Zero time if absent or unparseable.
//   - evidence  : raw value from the "-evidence" annotation. Empty string
//     if absent.
//
// A nil profile or empty backend name returns the zero tuple.
func GetBackendCanary(profile *GPUProfile, backend string) (isCanary bool, since time.Time, evidence string) {
	if profile == nil || backend == "" {
		return false, time.Time{}, ""
	}
	annotations := profile.GetAnnotations()
	if len(annotations) == 0 {
		return false, time.Time{}, ""
	}

	if raw, ok := annotations[BackendCanaryAnnotationKey(backend)]; ok {
		isCanary = strings.EqualFold(strings.TrimSpace(raw), "true")
	}

	if raw, ok := annotations[BackendCanarySinceAnnotationKey(backend)]; ok {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
			since = parsed
		}
	}

	evidence = annotations[BackendCanaryEvidenceAnnotationKey(backend)]
	return isCanary, since, evidence
}

// SetBackendCanary marks a backend as in canary mode on the given GPUProfile.
// All three annotations are written atomically (in-memory): the boolean key
// is set to "true", the "-since" key receives the current UTC time formatted
// as RFC3339, and the "-evidence" key receives the supplied pointer string
// (URL, path, MR link). Empty evidence is permitted and stored as "" so the
// helper still surfaces the canary state.
//
// The profile object is mutated in place; callers must persist the change
// with the controller client. Calling SetBackendCanary on a backend that is
// already in canary mode refreshes the "-since" timestamp and overwrites the
// "-evidence" pointer — this is intentional so the helper can also serve as
// "renew/replace evidence".
//
// A nil profile or empty backend name is a no-op.
func SetBackendCanary(profile *GPUProfile, backend string, evidence string) {
	if profile == nil || backend == "" {
		return
	}
	annotations := profile.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 3)
	}
	annotations[BackendCanaryAnnotationKey(backend)] = "true"
	annotations[BackendCanarySinceAnnotationKey(backend)] = time.Now().UTC().Format(time.RFC3339)
	annotations[BackendCanaryEvidenceAnnotationKey(backend)] = evidence
	profile.SetAnnotations(annotations)
}

// ClearBackendCanary removes the BackendCanary annotation triple for a backend
// from the GPUProfile. After Clear, GetBackendCanary returns the zero tuple.
//
// The helper deletes the keys rather than setting the boolean to "false" so
// that "absent" remains the canonical "not canary" state and downstream
// listers do not have to filter out tombstoned-but-still-present rows.
//
// A nil profile, empty backend name, or profile without any annotations is a
// no-op.
func ClearBackendCanary(profile *GPUProfile, backend string) {
	if profile == nil || backend == "" {
		return
	}
	annotations := profile.GetAnnotations()
	if len(annotations) == 0 {
		return
	}
	delete(annotations, BackendCanaryAnnotationKey(backend))
	delete(annotations, BackendCanarySinceAnnotationKey(backend))
	delete(annotations, BackendCanaryEvidenceAnnotationKey(backend))
	profile.SetAnnotations(annotations)
}

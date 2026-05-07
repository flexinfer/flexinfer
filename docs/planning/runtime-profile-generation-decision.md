# Runtime Profile Generation Decision

Status: Accepted

Date: 2026-05-06

## Context

`build/runtime.yaml` defines the runtime image build matrix (registry, base
images, backends, env, etc.) for each GPU profile (`scripts/promote-runtime-digest.sh:101-115`).
`deploy/gpuprofiles/*.yaml` declare cluster-side `GPUProfile` CRDs that the
controller consumes for backend selection and resource budgeting
(`deploy/gpuprofiles/gfx1100.yaml`, `deploy/gpuprofiles/gfx906.yaml`).
`deploy/system/values-k3s.yaml` carries Helm `runtime.profiles[]` entries that
inject digest-pinned runtime images into the cluster.

`scripts/check-runtime-profile-consistency.sh` (RG-2, commit `7ed1eb2f`)
validates the contract between these three surfaces:

- Every managed `GPUProfile` matches `build/runtime.yaml` on `architecture`
  and `vendor` (`scripts/check-runtime-profile-consistency.sh:75-101`).
- Helm `runtime.profiles[].image` entries are digest-pinned and reference an
  arch that has a `GPUProfile` (`scripts/check-runtime-profile-consistency.sh:103-128`).
- `GPUProfile.spec.runtime.image` is digest-pinned (`scripts/check-runtime-profile-consistency.sh:88`).

The open question recorded in `.loom/gfx1100-gfx906-platform-enhancements-spec.md:100`:
should `build/runtime.yaml` *generate* the `GPUProfile` manifests, or is the
consistency test enough?

## Decision

Stay consistency-test-only. Do not introduce a `GPUProfile` generator until a
third architecture lands or a real drift incident proves the test is
insufficient.

## Consequences

- The consistency test continues to enforce the most common drift surfaces
  (arch, vendor, digest pinning, profile-name match, one-to-one Helm-to-
  `GPUProfile` arch coverage).
- Operational `GPUProfile` fields the test does not cover remain hand-
  authored: `vramMB`, `deviceCount`, `usableDeviceIndices`, per-backend
  `support` levels, `features`, `containerMemoryGB`, `maxGPUMemoryGB`,
  `maxCPUMemoryGB`, `gpuDriverMemoryMB`, `quantization.images`, and the
  per-arch `env[]` block. These are tuned by hand based on cluster evidence
  (e.g. `deploy/gpuprofiles/gfx1100.yaml:88-102` documents the 44 -> 24 GiB
  CPU memory drop tied to a specific incident).
- Drift in those operational fields is still possible; the consistency test
  will not catch it. The risk is currently low because there are only two
  active profiles and changes to the fields above are rare and reviewed.
- Revisit when any of:
  - A third managed `GPUProfile` lands (e.g. gfx942/CDNA3, sm_52/Maxwell,
    sm_75/Turing). At that point, hand-maintaining identical structure across
    three profiles starts to cost more than a generator would.
  - A drift incident shows that an unguarded field (memory budget, backend
    support, quantization image) caused a regression that a generator or a
    larger consistency test would have caught.
  - `build/runtime.yaml` grows to encode fields the `GPUProfile` already owns
    (memory budgets, quantization images), which would imply we need a single
    source of truth.

## Alternatives Considered

- **Generate `GPUProfile` manifests from `build/runtime.yaml`.** Rejected for
  now. Generating would require `build/runtime.yaml` to encode every field
  the `GPUProfile` CRD owns: per-backend support enums, memory budgets,
  feature flags, quantization image references, env arrays. That turns
  `build/runtime.yaml` into a second CRD-shaped schema with its own
  round-trip parity tests. The cost outweighs the drift risk at two profiles.
- **Expand `check-runtime-profile-consistency.sh` to cover more fields.**
  Possible follow-up. Would require deciding which fields are
  "should-be-equal" vs "intentionally-divergent". For example, env vars
  overlap between `build/runtime.yaml` and `GPUProfile.spec.env`, but the
  `GPUProfile` env block also carries application-level config
  (`ABLITERATION_*`) that the build matrix should not own. Worth doing
  incrementally as specific drift surfaces are identified, but does not need
  a single up-front design pass.
- **Move all runtime profile state into the Helm chart.** Rejected: it
  conflates build-time inputs (base images, backend toggles) with runtime
  cluster policy (memory budgets, backend support levels). Each lives in the
  layer where its evidence comes from.

## Sources

- `scripts/check-runtime-profile-consistency.sh:55-130` — current
  consistency surface.
- `scripts/promote-runtime-digest.sh:101-115` — digest promotion uses
  `build/runtime.yaml` as the source of truth for the image ref to resolve.
- `build/runtime.yaml:19-38` — runtime build matrix entry shape (gfx1100).
- `build/runtime.yaml:215-238` — runtime build matrix entry shape (gfx906).
- `deploy/gpuprofiles/gfx1100.yaml:18-113` — `GPUProfile` shape with
  per-backend support, env, memory budgets, and quantization images.
- `deploy/gpuprofiles/gfx906.yaml:18-91` — same fields with
  arch-specific values.
- `.loom/gfx1100-gfx906-platform-enhancements-spec.md:100` — original open
  question.
- `.loom/gfx1100-gfx906-platform-enhancements-plan.md:185-189` — open
  decisions block where this resolution is recorded.

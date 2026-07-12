# Runtime Digest Promotion

FlexInfer runtime images are built from `build/runtime.yaml`, but the cluster
consumes immutable image references from GPUProfile manifests and Helm values.
Use `scripts/promote-runtime-digest.sh` to promote a validated runtime tag to a
digest without hand-editing each consumer.

## Dry Run

```bash
scripts/promote-runtime-digest.sh gfx1100
```

By default the script resolves `build/runtime.yaml`'s profile tag with `crane`
or Docker buildx, then prints the exact diff it would apply to:

- `deploy/gpuprofiles/<profile-or-arch>.yaml`
- matching `runtime.profiles` entries in `deploy/system/values-k3s.yaml`

Profiles can narrow their promotion targets in `build/runtime.yaml`. Today
`gfx1100` promotes only the broad GPUProfile runtime image, while
`gfx1100-serving` promotes the persistent `gfx1100` Helm runtime profiles. This
keeps serving DaemonSets on the slim serving persona instead of accidentally
rolling them back to the broader unified image.

No files are changed in dry-run mode.

## Apply

```bash
scripts/promote-runtime-digest.sh gfx1100 \
  --validation-row "Required canary: gfx1100 textgen" \
  --rollback-digest <previous-sha256> \
  --apply
```

If the digest has already been captured from CI or a registry command, pass it
explicitly so promotion is repeatable:

```bash
scripts/promote-runtime-digest.sh gfx1100 \
  --digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  --validation-row "Required canary: gfx1100 textgen" \
  --rollback-digest <previous-sha256> \
  --apply
```

Promote the serving persona separately when the serving tag is the intended
DaemonSet runtime:

```bash
scripts/promote-runtime-digest.sh gfx1100-serving \
  --digest sha256:3333333333333333333333333333333333333333333333333333333333333333 \
  --validation-row "Required canary: gfx1100 serving" \
  --rollback-digest <previous-serving-sha256> \
  --apply
```

For heavyweight runtime images, stage the exact digest first with a
release-gated `imagePrewarm` profile. Then make live cache readiness part of
the apply gate (repeat `--prewarm-profile` for every target profile):

```bash
scripts/promote-runtime-digest.sh gfx1100-serving \
  --digest sha256:3333333333333333333333333333333333333333333333333333333333333333 \
  --validation-row "Required canary: gfx1100 serving" \
  --rollback-digest <previous-serving-sha256> \
  --prewarm-profile 5930k-gfx1100-runtime-candidate \
  --prewarm-profile 7900xtx-gfx1100-runtime-candidate \
  --apply
```

The promotion script calls `scripts/check-image-prewarm.sh` before editing any
consumer manifest. It fails closed if the DaemonSet does not reference the
candidate digest, is not fully available, or any selected pod reports a
different image ID. The `gfx1100-serving` build profile declares
`promotion.require_prewarm: true`, so its `--apply` path also fails when no
`--prewarm-profile` is supplied.

`--apply` is intentionally gated. The matching row in
`.loom/60-validation-matrix.md` must already name the target digest, rollback
digest, canary command, result, and GitOps manifest pointer. `--validation-row`
records which row carries that evidence; `--rollback-digest` records the exact
digest to use if the new runtime regresses.

## Rollback

Rollback is the same operation with the previous digest:

```bash
scripts/promote-runtime-digest.sh gfx1100 \
  --digest <previous-sha256> \
  --validation-row "rollback: <row/artifact>" \
  --rollback-digest <current-sha256> \
  --apply
```

Before reconciling Flux, review the diff and confirm the digest maps to the
runtime that passed smoke testing.

## Verification

```bash
scripts/test-promote-runtime-digest.sh
git diff --check
```

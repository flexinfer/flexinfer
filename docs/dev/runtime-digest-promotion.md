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

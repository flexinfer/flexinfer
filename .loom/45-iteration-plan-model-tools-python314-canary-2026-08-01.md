# RALPH Iteration Plan: Model Tools Python 3.14 Candidate

## Review

- Roadmap milestone: controlled major Docker dependency rollout, phases 0 and
  1 of `docs/planning/docker-major-dependency-rollout.md`.
- Spec sections: `ROADMAP.md` "Next" and
  `docs/planning/docker-major-dependency-rollout.md` "Rollout Sequence",
  "Tagging and Rollback", and "Ready-for-Implementation Slices".
- Prior decisions to preserve: split major image changes from routine Renovate
  batches; publish a commit-specific candidate before promotion; leave GPU
  images and the Maxwell CUDA 11.8 lane untouched; use GitOps for deployment
  changes.

## Riskiest assumption + kill-test

**Load-bearing assumption**: the existing model-tools scripts and their current
Python dependency set runs unchanged on Python 3.14, so
the utility image can move interpreters without coupling in an application-code
or GPU-runtime migration.

**Kill test**: in no more than 30 minutes, build the exact-digest Python 3.14
candidate on the amd64 remote builder with a final build gate that requires
Python 3.14, ORAS 1.2.2, the locked dependency set plus `pip check`, imports of
both packaged libraries, byte-compilation and import of both copied scripts,
and `validate_quantized_artifact.py --help` to pass. Publish only a
commit-specific tag and inspect its registry digest. Production remains pinned
to the resolved Python 3.11 baseline throughout the test.

**Failure mode if the assumption is wrong**: the image fails to build because a
wheel or dependency is unavailable, either script fails to compile/import, or
the validator CLI fails to start. Any failure blocks publication/promotion and
keeps the Python 3.11 digest as the live rollback anchor.

**Disconfirming search**: Python 3.14's porting notes call out removed deprecated
`argparse` nesting and `BooleanOptionalAction` behaviors. A source scan found
neither pattern in the model-tools scripts; the validator uses a conventional
`ArgumentParser`, and its postponed annotations avoid the interpreter's changed
annotation evaluation semantics. PyPI metadata declares Python >=3.10 for the
resolved `huggingface_hub==1.24.0` and `safetensors==0.8.0`; safetensors ships an
abi3 Linux wheel usable by Python 3.14. These are supporting signals, not a
substitute for the image kill test.

**Status**: build gate passed 2026-08-01; Kubernetes validator/publisher canary
and promotion passed 2026-09-04.

## Align

- Slice name: immutable model-tools baseline plus Python 3.14 candidate.
- Scope in: pin the current live model-tools digest; pin the Python 3.14 base
  and the baseline's fully resolved Python dependency set; build, smoke, and
  publish one commit-specific candidate; remove post-merge stable-tag rebuilds;
  add an isolated per-cache publisher override; run a live validator/publisher
  canary; promote only the tested digest; record evidence and roadmap state.
- Scope out: moving `master`/`latest`, changing GGUF or GPU images, and changing
  the completed canary artifact or its quantization recipe.
- Acceptance criteria:
  - The prior `model-tools:master` image is recorded and configured by digest.
  - The candidate changes only the interpreter/base while preserving ORAS and
    the baseline's resolved Python package versions.
  - The bounded image kill test passes and the commit-specific candidate digest
    is recorded.
  - Post-merge CI continues publishing commit tags but never rebuilds
    model-tools under `master`, timestamp, or `latest` tags; promotion changes
    GitOps values to the already-tested digest.
  - Relevant Go tests, repository tests, Helm rendering, and whitespace checks
    pass.
  - Production values reference the candidate only after both live jobs pass,
    with the prior Python 3.11 digest retained as the rollback anchor.
- Dependencies/blockers: all cleared. The remote amd64 builder, Harbor,
  Kubernetes canary window, CRD rollout, and fixed controller are verified.

## Land

- Planned file areas: `.gitlab-ci.yml`, `build/Dockerfile.model-tools`,
  `build/requirements-model-tools.txt`, ModelCache API/controller/tests and
  generated CRDs, `deploy/system/values-k3s.yaml`, the representative
  ModelCache manifest, this plan, `.loom/00-index.md`, `ROADMAP.md`, and
  `docs/planning/docker-major-dependency-rollout.md`.
- Implementation steps:
  1. Resolve and pin the existing production digest as the rollback anchor.
  2. Pin the Python 3.14 base and current package versions, then build and smoke
     a commit-tagged candidate.
  3. Land the additive `spec.publish.image` API/CRD field through a green MR.
  4. Pin one completed ModelCache's validator and publisher to the candidate,
     use timestamp tagging, and inspect both Jobs' images and termination data.
  5. Promote the tested digest through GitOps in a separate MR and rerun the
     same ModelCache using controller-wide defaults.

## Prove

- Tests to run: candidate image kill test; focused publish/controller tests;
  `make test`; Helm lint/template rendering; server-side ModelCache dry run;
  live validator and publisher Job verification.
- Lint/static checks: source scan for Python 3.14 removals, Dockerfile diff
  review, `git diff --check`, controller lint/vet, and rendered environment
  checks for the exact promoted digest.
- CI checks: full GitLab merge-request pipeline and post-merge pipeline if the
  repository triggers one.

## Handoff/Harvest

- Docs to update: this iteration plan, context-pack index, controlled-rollout
  capsule, and top-level roadmap.
- Agent-context entries to add: immutable baseline finding, compatibility
  decision, kill-test verdict, candidate digest, and promotion boundary.
- Next-slice candidates: repeat the isolated build, job-canary, and promotion
  sequence for the GGUF helper image, then advance one GPU image family per MR.

## Evidence

- Rollback anchor: `model-tools:master` resolved on 2026-08-01 to
  `sha256:fe048a433779b7c1f6f8e9cfa4373117e846f071440eaf8575762a640125bf5a`.
  `deploy/system/values-k3s.yaml` now uses that digest, not the mutable tag.
- Baseline isolation: the production image's 16 resolved Python runtime
  packages are version- and wheel-hash-locked in
  `build/requirements-model-tools.txt`, with binary wheels required. ORAS
  remains 1.2.2 and is copied from
  `ghcr.io/oras-project/oras@sha256:cd549d80c4aa89638aea5964a3cd8193a6dd8abf939a43b5d562c24dbab08ff1`;
  no mutable apt, curl, or release-archive input remains.
- Candidate base: Python 3.14.6 from
  `python:3.14-slim@sha256:cea0e6040540fb2b965b6e7fb5ffa00871e632eef63719f0ea54bca189ce14a6`.
- Build kill test: PASS. The final Docker build reported Python 3.14.6, ORAS
  1.2.2, `huggingface_hub` 1.24.0, and `safetensors` 0.8.0; `pip check`, both
  script imports and byte-compilation, and validator `--help` all succeeded.
- Candidate publication: source commit `1db78d5e`; tag
  `registry.harbor.lan/flexinfer/model-tools:1db78d5e`; registry digest
  `sha256:41f948bafa42c154a17ac567a0ade1f49fd3e17e6241c7e8bb44dc7a48265f30`.
  Pulling the tag back from Harbor resolved to the same digest.
- Superseded evidence: the earlier `ec0efeeb` image was an exploratory
  publication made before the ORAS digest and wheel hashes were locked. It was
  left unpromoted and is not the candidate authorized for a job canary.
- Repository proof: `go test ./pkg/quantization/...`, `make test`, `helm lint`,
  Helm rendering with `deploy/system/values-k3s.yaml`, and `git diff --check`
  passed. Test-generated controller-gen drift was removed from the slice.
- CI drift repair: the values change triggered a stale WAN warm-lane assertion
  even though GitOps and the read-only live parent Model both intentionally use
  `minReplicas: 0` plus `warmPolicy: ondemand` (live phase `Preempted`) for the
  twin-canary window. The checker now asserts that parked shape; no Model or
  other cluster object was mutated.
- Canary API: MR !1012 added the optional `spec.publish.image` override with
  precedence over controller defaults and generated both ModelCache CRDs.
  MR !1013 pinned publisher and validator to the candidate on the completed
  `qwen36-27b-fable-gptq` artifact without rerunning its quantization.
- Ready-cache repair: the first reconciliation exposed that a Ready quantized
  cache returned before publish/validation reprocessing. MR !1015 added a
  regression test and dispatched changed publish/validation specifications
  back through the publish state machine. Post-merge pipeline 25568 passed all
  24 executed jobs and published the controller digest
  `sha256:fd5bef16a9f0f6f11955c5996b9c9208f7d30a48d70dd85c98abe23ba72afded`.
- Kubernetes job canary: PASS on 2026-09-04. Validator Job
  `qwen36-27b-fable-gptq-validate-publish` ran the candidate digest to exit 0
  with `ok: true`, layout `vllm-gptq`, and family `qwen36-27b`; it completed at
  `00:35:55Z`. Publisher Job `qwen36-27b-fable-gptq-publish` ran the same
  digest, pushed 17,808,736,564 bytes across 16 files, and completed at
  `00:37:50Z` with artifact digest
  `sha256:2a04f841cdde05e6207b81b321503bfb5b862093238c22948958c9ab22208788`.
  Its timestamp-only canary tag was
  `gptq-w4-g128-gfx1100-v1-20260904-003557`, so the stable tag was not moved.
- Promotion: the proven candidate digest is now the GitOps default for model
  tools and validation. The one-shot per-cache image pins, timestamp policy,
  and revalidation annotation were removed so a post-promotion run proves the
  controller-wide path. The exact Python 3.11 rollback digest remains
  `sha256:fe048a433779b7c1f6f8e9cfa4373117e846f071440eaf8575762a640125bf5a`.

## Slice Handoff

### Slice Summary

- Milestone: controlled major Docker dependency rollout, phases 0 and 1.
- Slice: immutable model-tools baseline plus Python 3.14 candidate.
- Status: implementation, bounded proof, isolated job canary, and default
  promotion complete.

### What Landed

- Key changes: immutable Python 3.11 rollback pin, exact Python 3.14 base,
  locked baseline dependencies, embedded build gate, commit-only CI publishing,
  and a commit-specific Harbor candidate.
- Key files: `build/Dockerfile.model-tools`,
  `build/requirements-model-tools.txt`, and `deploy/system/values-k3s.yaml`.
- Validation results: image gate, targeted quantization tests, full repository
  tests, Helm validation, and candidate digest round-trip all passed.

### What Is Still Open

- The model-tools Python 3.14 slice has no remaining acceptance criteria.
- CUDA, ROCm, PyTorch-bearing, and GGUF helper lanes remain independently
  gated follow-on work under the controlled major-dependency rollout.

### Next Actions

1. Retain the Python 3.11 digest as the documented rollback anchor.
2. Repeat the isolated candidate and Kubernetes job-canary sequence for the
   GGUF helper image.
3. Continue with one CUDA, ROCm, or PyTorch-bearing image family per MR.

### Context Links

- Agent-context session: `b989c83a4f16f73e`.
- Task ID: `fa1c1a379a2b9100`.
- Rollout capsule: `docs/planning/docker-major-dependency-rollout.md`.

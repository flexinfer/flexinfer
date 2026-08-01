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

**Status**: not run.

## Align

- Slice name: immutable model-tools baseline plus Python 3.14 candidate.
- Scope in: pin the current live model-tools digest; pin the Python 3.14 base
  and the baseline's fully resolved Python dependency set; build, smoke, and publish one
  commit-specific candidate; record evidence and roadmap state.
- Scope out: changing `validatorImage`, promoting the candidate into a job,
  moving `master`/`latest`, changing GGUF or GPU images, and cluster mutations.
- Acceptance criteria:
  - The prior `model-tools:master` image is recorded and configured by digest.
  - The candidate changes only the interpreter/base while preserving ORAS and
    the baseline's resolved Python package versions.
  - The bounded image kill test passes and the commit-specific candidate digest
    is recorded.
  - Relevant Go tests, repository tests, Helm rendering, and whitespace checks
    pass.
  - No production value references the candidate.
- Dependencies/blockers: remote amd64 Docker builder, Harbor push access, and
  availability of the pinned upstream Python base and Python wheels.

## Land

- Planned file areas: `build/Dockerfile.model-tools`,
  `build/requirements-model-tools.txt`,
  `deploy/system/values-k3s.yaml`, this plan, `.loom/00-index.md`, `ROADMAP.md`,
  and `docs/planning/docker-major-dependency-rollout.md`.
- Implementation steps:
  1. Resolve and pin the existing production digest as the rollback anchor.
  2. Pin the Python 3.14 base and current package versions, then build and smoke
     a commit-tagged candidate.
  3. Record the candidate digest/verdict, synchronize roadmap truth, and land
     through a green merge request without promotion.

## Prove

- Tests to run: candidate image kill test; `go test ./pkg/quantization/...`;
  `make test`; Helm template rendering.
- Lint/static checks: source scan for Python 3.14 removals, Dockerfile diff
  review, `git diff --check`, and a negative search for live candidate refs.
- CI checks: full GitLab merge-request pipeline and post-merge pipeline if the
  repository triggers one.

## Handoff/Harvest

- Docs to update: this iteration plan, context-pack index, controlled-rollout
  capsule, and top-level roadmap.
- Agent-context entries to add: immutable baseline finding, compatibility
  decision, kill-test verdict, candidate digest, and promotion boundary.
- Next-slice candidates: run a real ModelCache publish/validation job against
  the candidate and promote it only if that canary passes; otherwise isolate
  the first failing dependency or script as a corrective slice.

# RALPH Iteration Plan: BuildKit Publish Observability

## Riskiest assumption + kill-test

**Load-bearing assumption**: The pinned BuildKit v0.12.5 client used by the
`publish` job supports durable `plain` progress and `--metadata-file`, and a
POSIX streaming wrapper can retain that output without masking the real
`buildctl` exit status.

**Kill test**: Run `scripts/test-buildkit-publish-image.sh` with its fake
BuildKit client and verify that a failed extraction is streamed, retained,
retried, and summarized with cache/extraction counts plus the final digest.
Then run `buildctl build --help` in the pinned CI image and require both
`--progress` and `--metadata-file` before the publish job can change.

**Failure mode if the assumption is wrong**: The wrapper would either hide a
failed or stalled extraction, report a successful `tee` instead of a failed
build, or break the production image-publish job on unsupported client flags.

**Status**: passed 2026-07-15. The local fake-client test preserved the
simulated extraction failure's exit code and evidence through retry; pinned
BuildKit v0.12.5 contract job
[`184696`](https://gitlab.flexinfer.ai/services/flexinfer/-/jobs/184696) passed
in [MR pipeline 19144](https://gitlab.flexinfer.ai/services/flexinfer/-/pipelines/19144).

Positive evidence: the [upstream BuildKit README](https://github.com/moby/buildkit)
documents `--metadata-file`, `buildctl du -v`, and registry/inline cache
behavior; upstream build examples use `--progress plain`. Negative evidence:
the [BuildKit migration notes in moby/moby#40379](https://github.com/moby/moby/issues/40379)
call out that BuildKit progress is written to stderr. Pipeline wrappers can
therefore accidentally observe `tee` rather than the builder's status, so the
wrapper must explicitly preserve the child exit code.

## Review

- Roadmap milestone: production CI reliability and build-node controls.
- Spec sections: `.gitlab-ci.yml` publish stage and
  `docs/dev/build-node-disk-management.md`.
- Prior decisions to preserve: continue using remote BuildKit v0.12.5, push one
  tag per invocation, retain five-attempt exponential retry behavior, and do
  not change image contents or tag selection.

## Align

- Slice name: Observable BuildKit publish attempts.
- Scope in: plain progress, live+retained logs, per-attempt elapsed time,
  cache/extraction counts, image digest metadata, CI artifacts, and a pinned
  client contract test.
- Scope out: BuildKit daemon upgrades, GC policy changes, cache pruning,
  OpenTelemetry deployment, image/tag policy changes, and backend image jobs.
- Acceptance criteria:
  - every component-image publish attempt has an unambiguous start/end record;
  - extraction and cache activity remains visible live in GitLab logs;
  - the underlying `buildctl` failure status survives log streaming;
  - successful attempts report the pushed image digest;
  - logs and metadata are retained as CI artifacts;
  - the v0.12.5 client contract and wrapper regression tests pass.
- Dependencies/blockers: remote BuildKit and GitLab CI availability. Direct
  access to the in-cluster BuildKit hostname is unavailable from the workstation.

## Land

- Planned file areas: `.gitlab-ci.yml`, `scripts/`, build operations docs.
- Implementation steps:
  1. Extract the component publish loop into a POSIX helper.
  2. Add progress capture, structured summaries, metadata, and retries.
  3. Add fake-client regression coverage and pinned-client CI validation.
  4. Document artifact interpretation and failure triage.

## Prove

- Tests to run: helper regression test, POSIX syntax check, CI YAML validation,
  and the repository's existing build-node disk tests.
- Lint/static checks: ShellCheck when available; repository lint/test gates.
- CI checks: MR pipeline, pinned BuildKit client contract job, master publish,
  post-publish scans.

## Handoff/Harvest

- Docs to update: `docs/dev/build-node-disk-management.md`.
- Agent-context entries to add: chosen output contract, CI evidence, and any
  v0.12.5 compatibility findings once the transport is available.
- Next-slice candidates: aggregate BuildKit cache metrics over time or add
  daemon-side OpenTelemetry only if the new per-build evidence shows a need.

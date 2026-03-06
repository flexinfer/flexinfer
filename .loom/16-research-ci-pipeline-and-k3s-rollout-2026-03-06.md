# Research: CI Pipeline And k3s Rollout (2026-03-06)

## Goal

Reduce loom-core CI wall time at the repository level and verify the corresponding local and k3s deployment path end to end.

## Facts Found

- `go.mod` depended on three sibling private modules through local workspace-only assumptions instead of pinned remote versions, which forced CI to recreate those repos in job bootstrap. Sources:
  - `go.mod:35`
  - `go.mod:36`
  - `go.mod:37`
  - historical CI bootstrap before change in `.gitlab-ci.yml:109`
- CI already had runner-side CPU override headroom up to `cpu_limit_overwrite_max_allowed = "4"`, so loom-core could claim more CPU without changing runner policy. Source:
  - `/Users/cblevins/workspace/platform/gitops/k3s/ci/gitlab/helmrelease-runner.yaml:202`
- The heavy repository-local costs were:
  - manual source bootstrap plus private-module setup in `.gitlab-ci.yml:109`
  - repeated lint tool installation in `.gitlab-ci.yml:168`
  - serial MCP binary fan-out in `.gitlab-ci.yml:340`
  - always-on race+coverage in `test:unit` before this session, now represented by the updated coverage-only job at `.gitlab-ci.yml:398`
- The local `custom-server` image build path originally used the entire workspace as the Docker build context, which caused multi-gigabyte context transfers during deploy attempts. The corrected path now uses the repo root plus a named `libs` BuildKit context. Sources:
  - `Makefile:979`
  - `Dockerfile.custom-server.local:21`
- Before rollout, `loom-hub` custom-server-backed deployments were on `registry.harbor.lan/mcp/custom-server:20603862`; after rollout they converged on `registry.harbor.lan/mcp/custom-server:20260306-ci-fix`. Sources:
  - live cluster snapshot captured via `kubectl`/`k8s_get` during this session
  - `/Users/cblevins/workspace/platform/gitops/k3s/loom-hub/servers/kustomization.yaml:215`

## Assumptions

- CI jobs have a valid `CI_JOB_TOKEN`, so Git URL rewrite is sufficient for direct private module fetches when the module proxy does not already have the version cached.
- Local developer work happens inside the workspace layout where `../../libs/*` exists, so a committed `go.work` is acceptable for workspace mode.

## Conclusions

- The biggest safe repo-local wins were:
  - replace local-only `go.mod` rewrites with pinned module versions plus `go.work`
  - authenticate private module fetches in CI instead of recloning sibling repos
  - parallelize MCP builds and explicitly request more CPU for the heavy jobs
  - split unit coverage from the broader race run so every branch pipeline no longer pays the full race cost
  - shrink the local Docker build context for k3s rollout work

## Sources

- `go.mod:35`
- `go.mod:36`
- `go.mod:37`
- `.gitlab-ci.yml:61`
- `.gitlab-ci.yml:125`
- `.gitlab-ci.yml:168`
- `.gitlab-ci.yml:340`
- `.gitlab-ci.yml:398`
- `Makefile:979`
- `Dockerfile.custom-server.local:10`
- `Dockerfile.custom-server.local:32`
- `/Users/cblevins/workspace/platform/gitops/k3s/ci/gitlab/helmrelease-runner.yaml:202`
- `/Users/cblevins/workspace/platform/gitops/k3s/loom-hub/servers/kustomization.yaml:215`

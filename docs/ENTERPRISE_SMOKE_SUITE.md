# Enterprise Smoke Suite

This smoke suite provides a fast pre-merge confidence check for enterprise-critical paths:

- gateway bearer auth path
- RBAC denial + stage audit attribution
- devbox backend execution path guards

## Local

```bash
make ci-test-enterprise-smoke
```

## CI

The GitLab pipeline runs `test:enterprise-smoke`, which executes:

```bash
bash scripts/ci/enterprise_smoke_suite.sh
```

## Current Coverage Slice

The suite currently runs these deterministic tests:

- `internal/daemon`
  - `TestHTTPIntegration_BearerTokenAuth`
  - `TestHandleCall_RBACDenialEmitsAuditAndCost`
  - `TestHandleCall_StageBoundaryAuditRegression`
- `internal/devbox/backend`
  - `TestDockerBuild_MonorepoContextPathPassedToCLI`
  - `TestBuild_MonorepoContextCompletesWithFakeK8s`
- `cmd/mcp-devbox`
  - `TestRegisterTools_RegistersExpectedToolset`
  - `TestBuildMounts_DockerMonorepoMountsWorkspaceRoot`
  - `TestActiveExecs`

## Triage Guidance

Map failures to the first failing stage:

1. Auth: `TestHTTPIntegration_BearerTokenAuth`
2. Policy/Audit: RBAC denial + stage audit regression tests
3. Execution: devbox backend/manager smoke tests

For stage-specific failures, run the failing package test with `-v` and `-run` locally for focused debugging.

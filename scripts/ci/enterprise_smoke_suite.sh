#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-/tmp/loom-core-gocache}"
export GOMODCACHE="${GOMODCACHE:-/tmp/loom-core-gomodcache}"
mkdir -p "$GOCACHE" "$GOMODCACHE"

echo "==> Enterprise smoke: gateway auth + RBAC/audit"
go test ./internal/daemon -run 'TestHTTPIntegration_BearerTokenAuth|TestHandleCall_RBACDenialEmitsAuditAndCost|TestHandleCall_StageBoundaryAuditRegression' -count=1

echo "==> Enterprise smoke: devbox execution backend paths"
go test ./internal/devbox/backend -run 'TestDockerBuild_MonorepoContextPathPassedToCLI|TestBuild_MonorepoContextCompletesWithFakeK8s' -count=1

echo "==> Enterprise smoke: devbox tool surface guardrails"
go test ./cmd/mcp-devbox -run 'TestRegisterTools_RegistersExpectedToolset|TestBuildMounts_DockerMonorepoMountsWorkspaceRoot|TestActiveExecs' -count=1

echo "Enterprise smoke suite passed."

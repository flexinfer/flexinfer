# Technical Debt Remediation Plan — Cycle 2

## Summary

- **Planning date:** 2026-03-23
- **Scope:** FlexInfer — systematic coverage gaps, build complexity, and legacy API
- **Previous cycle:** DEBT-001 through DEBT-008 — all resolved (2026-02-21)
- **Total items:** 10 new debt items identified
- **Scoring:** impact 35% + risk_reduction 30% + drag_reduction 20% + effort_inverse 15%

---

## Wave 1 — Test Coverage for Critical Paths (Score >= 69)

**Goal:** Add unit tests to the most complex, most-changed, and highest-risk untested code paths. These files have caused the most production issues.

**Estimated effort:** 3-4 sessions | **Risk reduction:** High

### DEBT-104: Test proxy model_resolver.go + k8s_activator.go (Score: 76, Effort: S)

- **Component:** `internal/proxy/model_resolver.go` (255 LOC), `internal/proxy/k8s_activator.go` (195 LOC)
- **Problem:** These are on the critical path for every inference request. model_resolver handles alias caching (source of DEBT-004 production bug). k8s_activator handles scale-from-zero (cold start reliability issues).
- **Fix:**
  1. Create `internal/proxy/model_resolver_test.go` — table-driven tests for alias resolution, TTL cache expiry, missing model fallback
  2. Create `internal/proxy/k8s_activator_test.go` — table-driven tests for activation flow, timeout handling, concurrent activation dedup
- **Acceptance criteria:**
  - Both files have >=80% line coverage
  - Alias resolution edge cases covered (empty alias, duplicate aliases, stale cache)
  - Scale-from-zero timeout and error paths tested
- **Rollback:** N/A (additive)
- **Effort:** S (2 points) — clean interfaces, straightforward mocking

### DEBT-105: Test quantization image.go + finetune.go (Score: 69, Effort: S)

- **Component:** `pkg/quantization/image.go` (157 LOC), `pkg/quantization/finetune.go` (377 LOC)
- **Problem:** image.go resolves GPU arch-specific container images — a bug here caused wrong image selection that masked arch-specific env vars. finetune.go builds job specs with complex env var propagation.
- **Fix:**
  1. Create `pkg/quantization/image_test.go` — test GPU arch → image resolution matrix, env var precedence (arch-specific > generic > default)
  2. Create `pkg/quantization/finetune_test.go` — test job spec generation, env var propagation, volume mounts
- **Acceptance criteria:**
  - image.go: all GPU arch combinations tested (gfx1100, gfx906, cuda)
  - image.go: env var precedence chain verified
  - finetune.go: generated job specs match expected structure
- **Rollback:** N/A
- **Effort:** S (2 points) — existing quantization_test.go provides patterns to follow

### DEBT-107: Test model_cache.go (Score: 79, Effort: M)

- **Component:** `controllers/model_cache.go` (904 LOC, 6 functions)
- **Problem:** Largest untested controller file. Orchestrates download jobs, cache readiness, PVC management, and status transitions. Bugs here have caused: local-path PVC disappearing, download marker races, hf_transfer OOMs, Longhorn RWX stale mounts.
- **Fix:**
  1. Create `controllers/model_cache_test.go` with envtest or mock client
  2. Test each reconciliation path: fresh download, cached model, download failure, PVC missing
  3. Test status transition state machine
- **Acceptance criteria:**
  - All 6 exported functions have test coverage
  - Download job creation logic verified with different storage strategies
  - Status transition matrix tested (Pending → Downloading → Ready, error paths)
- **Rollback:** N/A
- **Effort:** M (3 points) — requires envtest setup or K8s client mocking

### DEBT-108: Replace interface{} with any (Score: 36, Effort: XS)

- **Why in Wave 1:** Mechanical change, zero risk, instant cleanup. Good warmup commit.
- **Fix:** `sed -i 's/interface{}/any/g'` across all Go files + `gofmt`
- **Acceptance criteria:** Zero `interface{}` in source (excluding vendor). All tests pass.
- **Effort:** XS (1 point) — single commit

**Not in Wave 1:**
- DEBT-101 (pipeline orchestration tests) — large scope, break into Wave 1 + Wave 2 pieces
- DEBT-103 (Dockerfile consolidation) — XL effort, needs design first

---

## Wave 2 — Pipeline Tests + Build Simplification (Score 54-87)

**Goal:** Cover the remaining high-risk pipeline controllers and begin reducing build surface area.

**Estimated effort:** 4-5 sessions | **Risk reduction:** High

### DEBT-101: Pipeline controller test coverage (Score: 87, Effort: L — split across waves)

Wave 2 focuses on the pipeline orchestration files:
- `controllers/modelcache_quantization.go` (709 LOC, 15 funcs) — most complex
- `controllers/modelcache_abliteration.go` (501 LOC, 7 funcs)
- `controllers/modelcache_finetune.go` (439 LOC, 5 funcs)
- `controllers/model_cache_jobs_local.go` (366 LOC)

**Fix:**
1. Create test files for each using envtest patterns from existing `controllers/modelcache_test.go`
2. Focus on job creation logic, status transitions, and error handling
3. Test cleanup trap logic (conditional cleanup on save success)

**Acceptance criteria:**
- Each pipeline controller has >=70% line coverage
- Job creation with different GPU arch/vendor combinations tested
- Status transitions and error recovery paths covered

**Effort:** L (4 points) — complex orchestration logic, needs careful mock setup

### DEBT-106: Deprecate MLC-LLM backend (Score: 54, Effort: S)

- **Fix:**
  1. Add deprecation notice to `backend/mlc_llm.go`
  2. Mark MLC-LLM CI jobs as `when: manual` (don't build by default)
  3. Document deprecation in CHANGELOG
  4. Remove in a follow-up cycle if no users report dependency
- **Acceptance criteria:**
  - CI pipeline runs faster (skip 4 MLC-LLM image builds)
  - Deprecation warning emitted when MLC-LLM backend selected
- **Effort:** S (2 points)

### DEBT-110: Test metrics exporter (Score: 59, Effort: S)

- **Fix:**
  1. Create `pkg/metrics/exporter_test.go`
  2. Verify metric descriptor names and labels match expected values
  3. Test collection callbacks with mock data
  4. Verify no cardinality explosions from label values
- **Acceptance criteria:**
  - All metric descriptors verified
  - Label cardinality bounds tested
- **Effort:** S (2 points) — prometheus/client_golang provides test helpers

**Not in Wave 2:**
- Remaining DEBT-101 files (runtime_controller.go, flash_loader.go, etc.) — lower priority, can be picked up incrementally

---

## Wave 3 — Strategic Refactors (Score 58-69)

**Goal:** Address structural debt that requires coordination and has a wider blast radius.

**Estimated effort:** 5-7 sessions | **Risk reduction:** Medium-High

### DEBT-103: Consolidate Dockerfiles and CI pipeline (Score: 69, Effort: XL — phased)

**Phase 3a:** Remove MLC-LLM Dockerfiles (blocked by DEBT-106 deprecation period)
- Delete 8 `build/Dockerfile.mlc-*` files
- Remove 16 CI job references
- Reduces build surface by ~17%

**Phase 3b:** Template-ize ROCm backend Dockerfiles
- Create shared `build/Dockerfile.rocm-base` with common ROCm setup
- Consolidate vLLM variants (6 files → 1 parameterized file with `--build-arg GPU_ARCH`)
- Consolidate diffusers variants (4 files → 1 parameterized)
- Target: 47 Dockerfiles → ~20

**Phase 3c:** CI pipeline simplification
- Use `changes:` rules more aggressively to skip unchanged backend builds
- Consolidate publish jobs using matrix strategy
- Target: 1,638 LOC → ~800

**Acceptance criteria:**
- All existing images can still be built
- CI pipeline time reduced by >=30% for non-backend changes
- Build arg parameterization documented

### DEBT-102 + DEBT-109: v1alpha1 API removal (Score: 61+58, Effort: L)

**Preconditions:** All deployed CRs migrated to v1alpha2. Migration guide validated.

**Fix:**
1. Update CLI commands (list, status, delete, benchmark, cache, quantize) to use v1alpha2
2. Remove `api/v1alpha1/` package (~2,700 LOC)
3. Remove `controllers/model_deployment.go` (865 LOC)
4. Remove `quantization_convert.go` bridge
5. Re-enable SA1019 in `.golangci.yml`
6. Remove 3 `nolint:staticcheck` directives

**Acceptance criteria:**
- No v1alpha1 imports remain outside test fixtures
- SA1019 lint rule re-enabled and passing
- CLI commands work with v1alpha2 resources
- ~3,500 LOC removed

**Not in Wave 3:**
- DEBT-101 remaining controller files — pick up incrementally as needed

---

## Backlog Conversion

| Debt ID | Wave | Title | Effort | Status |
|---|---|---|---|---|
| DEBT-108 | 1 | interface{} → any modernization | XS | **resolved** (2026-03-23) |
| DEBT-104 | 1 | Proxy model_resolver + k8s_activator tests | S | **resolved** (2026-03-24) |
| DEBT-105 | 1 | Quantization image.go + finetune.go tests | S | **resolved** (2026-03-24) |
| DEBT-107 | 1 | model_cache.go test coverage | M | **resolved** (2026-03-24) |
| DEBT-110 | 2 | Metrics exporter tests | S | **resolved** (2026-03-24) |
| DEBT-106 | 2 | Deprecate MLC-LLM backend | S | open |
| DEBT-101 | 2 | Pipeline controller test coverage (main batch) | L | open |
| DEBT-103 | 3 | Dockerfile/CI consolidation (phased) | XL | open |
| DEBT-102 | 3 | v1alpha1 API removal | L | open |
| DEBT-109 | 3 | model_deployment.go dedup (via v1alpha1 removal) | M | open |

## Deferred / Not In Scope

- **Multi-cluster test coverage** — Phase 5 (multi-cluster) code is newer and less frequently changed
- **E2E test expansion** — 6/7 pass, remaining ColdStart test requires spare GPU
- **Grafana dashboard testing** — operational concern, not code debt

---
title: Phase 4: Operational Polish
description: E2E testing and documentation consolidation.
---

# Phase 4: Operational Polish

> Last updated: 2026-01-30

This is the checklist for Phase 4: improve operational readiness with **E2E testing** and **consolidated documentation**.

## Goals

- Add E2E test harness for real cluster regression testing.
- Consolidate and refresh user-facing documentation.
- Document known quirks and operational runbooks.

## Non-Goals

- New features (covered in earlier phases).
- Performance optimization beyond routing (covered in Phase 3).

## Work items (PR-sized)

### 1) E2E test harness setup

- [x] Create `e2e/` directory with test framework:
  - Use Go test with real K8s client (not envtest)
  - Support running against any cluster via kubeconfig
  - Skip if cluster not available (CI-friendly)
- [x] Add smoke test for basic Model lifecycle:
  - Create Model → wait for Ready → make inference request → delete
- [x] Add test for serverless scale-to-zero:
  - Create Model with serverless config → verify Idle after timeout → request → verify Ready
- [x] Document how to run E2E tests locally and in CI

**Acceptance**
- `make test-e2e` runs against a real cluster. ✓
- Tests are skippable when no cluster is available. ✓

**Primary files**
- `e2e/e2e_test.go` ✓
- `Makefile` ✓

### 2) INSTALL.md refresh

- [x] Review and update installation steps:
  - Prerequisites (K8s version, GPU operators, runtime classes)
  - Helm chart installation
  - Manual manifest installation
- [x] Add verification steps after installation
- [x] Add troubleshooting section for common issues

**Acceptance**
- New user can install FlexInfer following INSTALL.md without external help. ✓

**Primary files**
- `docs/INSTALL.md` ✓

### 3) User quickstart guide

- [x] Create `docs/user/quickstart.md` with:
  - Deploy your first model (simple example)
  - Make an inference request
  - Enable serverless scaling
  - Monitor with Prometheus metrics
- [ ] Add examples for each backend (ollama, vllm, mlc-llm)

**Acceptance**
- New user can deploy and query a model within 15 minutes. ✓

**Primary files**
- `docs/user/quickstart.md` ✓

### 4) GPU/backend quirks runbook

- [x] Document known issues and workarounds:
  - NVIDIA runtime class requirements
  - AMD ROCm container setup
  - MLC-LLM model format requirements
  - vLLM memory configuration
  - llama.cpp GGUF format notes
- [x] Add troubleshooting decision tree

**Acceptance**
- Operators can diagnose common GPU/backend issues without escalation. ✓

**Primary files**
- `docs/user/operations.md` ✓ (expanded with quirks and troubleshooting)

### 5) Documentation index/navigation

- [x] Create or update `docs/README.md` with navigation:
  - Getting started (INSTALL, quickstart)
  - User guides (operations, routing, api-compatibility)
  - Configuration reference
  - Troubleshooting
- [x] Ensure all docs are linked and discoverable

**Acceptance**
- Users can find relevant documentation from a single entry point. ✓

**Primary files**
- `docs/README.md` ✓

## Tracking

- This checklist is the source-of-truth for Phase 4 items.
- When a PR lands, add a checkbox + link to the PR/commit in this doc.

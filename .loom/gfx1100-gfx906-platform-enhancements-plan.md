# gfx1100/gfx906 Platform Enhancements Plan

Date: 2026-05-06

## Execution Shape

Use one planning umbrella with six PR-sized implementation slices. Keep runtime-image changes separate from CRD/controller changes unless a slice needs a contract and a consumer in the same MR.

## Slice 1: Capability Matrix Reconciliation

Status: complete for the initial RALPH pass on 2026-05-06.

Goal: make the current truth self-consistent before adding new fields.

Target files:
- `build/runtime.yaml`
- `deploy/gpuprofiles/gfx1100.yaml`
- `deploy/gpuprofiles/gfx906.yaml`
- `deploy/system/values-k3s.yaml`
- `docs/user/backends-rocm-gfx1100.md`
- `build/README-gfx906.md`
- `examples/v1alpha2/*gfx*.yaml`

Work:
- [x] Add a checked-in support matrix for `gfx1100` and `gfx906` covering backends, runtime image source, quantization support, required env, and canary status.
- [x] Resolve the `gfx906` vLLM contradiction: runtime build disables it while GPUProfile declares full support.
- [x] Mark experimental lanes with explicit canary gates and rollback notes.

Output:
- `docs/planning/rocm-gfx1100-gfx906-platform-slice.md`
- `deploy/gpuprofiles/gfx906.yaml`
- `build/README-gfx906.md`
- `examples/v1alpha2/model-vllm-gfx906.yaml`

Validation:
- `rg -n "gfx1100|gfx906|vllm|diffusers|mlc-llm|llamacpp|ollama" build deploy docs examples`
- `git diff --check`

Rollback:
- Docs/profile-only; revert the MR if the matrix incorrectly demotes a live path.

## Slice 2: GPUProfile Contract Hardening

Goal: make GPUProfile the obvious API for arch capability decisions.

Target files:
- `api/v1alpha2/gpuprofile_types.go`
- `controllers/gpuprofile_controller.go`
- `controllers/model_backend.go`
- `controllers/model_runtime.go`
- `pkg/quantization/*.go`
- `config/crd/ai.flexinfer_gpuprofiles.yaml`

Work:
- Decide whether backend support needs `canary` as a formal state or annotations/status for runtime validation.
- Add status fields or annotations for last validated runtime digest, canary result, and support-level reason if this can be done without overloading spec.
- Ensure runtime pods/jobs get env and memory budgets from GPUProfile before falling back to package defaults.
- Keep CRD changes minimal; prefer consistency tests if status is enough.

Validation:
- `make manifests`
- `go test ./api/v1alpha2/... ./controllers/... ./pkg/quantization/...`
- `git diff --check`

Rollback:
- CRD/status additions must be backward-compatible. If a status field causes controller churn, revert controller use first, then schema docs.

## Slice 3: Runtime Build + Promotion Loop

Status: partial; RG-2 consistency checks landed in the second RALPH pass.

Goal: make the runtime build matrix, digest promotion, and model-manifest promotion repeatable for both arches.

Target files:
- `build/runtime.yaml`
- `build/build-runtime.sh`
- `scripts/promote-runtime-digest.sh`
- `scripts/test-promote-runtime-digest.sh`
- `docs/dev/runtime-digest-promotion.md`
- `deploy/gpuprofiles/*.yaml`

Work:
- [ ] Extend dry-run output to include validation reminders and profile-specific model manifests.
- [x] Add consistency tests that fail if `build/runtime.yaml`, `deploy/gpuprofiles/*.yaml`, and Helm runtime profiles disagree on arch/vendor/runtime-profile basics.
- [x] Preserve digest-pinned cluster consumption; mutable tags remain build inputs only.

Validation:
- `scripts/check-runtime-profile-consistency.sh`
- `scripts/test-promote-runtime-digest.sh`
- `scripts/promote-runtime-digest.sh gfx1100 --digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
- `scripts/promote-runtime-digest.sh gfx906 --digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`

Rollback:
- Re-run promotion with the prior digest as documented in `docs/dev/runtime-digest-promotion.md`.

## Slice 4: gfx1100 Capability Push

Goal: strengthen the RDNA3 lane where it is already differentiated.

Target files:
- `build/runtime.yaml`
- `build/scripts/*vllm*`
- `deploy/modelcaches/*gfx1100*.yaml`
- `examples/v1alpha2/diffusers-sdxl-gfx1100.yaml`
- `.loom/60-validation-matrix.md`
- runtime dashboards/metrics files if canary metrics are surfaced

Work:
- Treat Navi vLLM 0.14 and TurboQuant/Gemma4 as canary lanes until image digest plus smoke evidence exists.
- Track long-context KV ceilings separately from artifact validity; current evidence shows a 32K canary can fail after weights load because KV memory is insufficient.
- Keep FLUX/Fill/NF4 imagegen as a first-class canary path with `512x512` and `1024x1024` warmup evidence.

Validation:
- `go test ./backend/... ./controllers/... ./internal/proxy/...`
- Cluster smoke: vLLM textgen decode/prompt TPS, long-context init ceiling, FLUX text-to-image, FLUX Fill/edit.

Rollback:
- Revert GPUProfile runtime digest and any model manifest image override to the previous known-good digest.

## Slice 5: gfx906 First-Class Conservative Lane

Goal: make Vega20 useful without pretending it behaves like RDNA3.

Target files:
- `build/runtime.yaml`
- `build/Dockerfile.*gfx906*`
- `deploy/gpuprofiles/gfx906.yaml`
- `deploy/debug/qwen35-gfx906-abliteration-gpu-load.yaml`
- `docs/user/gptq-quantization-runbook.md`
- `build/README-gfx906.md`
- `.loom/60-validation-matrix.md`

Work:
- Choose the default stance for vLLM on `gfx906`: validate and promote, or demote to experimental/unsupported.
- Make source-built bitsandbytes expectations explicit for FLUX/diffusers and quantization lanes.
- Keep `HSA_OVERRIDE_GFX_VERSION=9.0.6`, `HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, safe sharded load, streaming save, and hidden-state activation capture as documented defaults.
- Add slow-path defaults for GPTQ timeout/memory, using the existing runbook evidence.

Validation:
- `go test ./pkg/quantization/... ./controllers/...`
- Cluster smoke: llama.cpp or Ollama textgen, GPTQ/abliteration debug job, diffusers `512x512` offload imagegen.

Rollback:
- Demote backend support level and promote the previous runtime digest; keep docs honest if runtime canaries fail.

## Slice 6: Validation Matrix + Observability

Status: partial; runtime profile/digest labels landed in the runtime info metric.

Goal: make runtime promotion decisions auditable.

Target files:
- `.loom/60-validation-matrix.md`
- `charts/flexinfer/templates/grafana-dashboard-runtime.yaml`
- metrics packages under `pkg/metrics` or `internal/proxy` as needed
- `docs/planning/spec-driven-delivery.md`

Work:
- Add columns for `gpu_arch`, `runtime_digest`, `backend`, `support_level`, `canary_command`, `ready_seconds`, `cold_load_seconds`, `decode_tps`, `imagegen_seconds`, `gate`, and `rollback_digest`.
- [x] Surface runtime digest/profile labels in metrics where available.
- [ ] Expand the validation matrix schema with explicit promotion audit fields.
- Link validation rows back to spec slices and runtime promotion commands.

Validation:
- `go test ./pkg/metrics/... ./internal/proxy/...`
- Dashboard lint/render path used by this repo, if available.
- At least four filled canary rows before promotion.

Rollback:
- Matrix/docs-only changes can be corrected in follow-up; metric label changes should be reverted if they create high cardinality.

## Suggested Order

1. Slice 1 first, because it removes contradictory truth.
2. Slice 3 next, because digest promotion protects every runtime experiment.
3. Slice 6 in parallel with runtime canaries, because evidence capture should not wait until the end.
4. Slice 5 before risky `gfx906` feature claims.
5. Slice 4 once the RDNA3 runtime candidate image exists.
6. Slice 2 only where the earlier slices prove the API needs to grow.

## Open Decisions

- `canary` as a support enum vs status annotation.
- Generated GPUProfile manifests from `build/runtime.yaml` vs consistency test.
- Whether to invest in `gfx906` vLLM or formally steer that node to llama.cpp/Ollama/GPTQ/diffusers-offload.

## Sources

- Spec evidence: `.loom/gfx1100-gfx906-platform-enhancements-spec.md`
- Runtime matrix: `build/runtime.yaml:20`, `build/runtime.yaml:215`
- GPUProfiles: `deploy/gpuprofiles/gfx1100.yaml:32`, `deploy/gpuprofiles/gfx906.yaml:29`
- Promotion script: `scripts/promote-runtime-digest.sh:36`
- Validation standard: `docs/planning/spec-driven-delivery.md:41`

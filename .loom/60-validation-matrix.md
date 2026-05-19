# Runtime Promotion Validation Matrix

This is the canonical canary and runtime-promotion evidence table for GPU
model/runtime work. It connects planning specs, roadmap items, build artifacts,
runtime canaries, observed failure modes, and promotion decisions so a reviewer
can audit a promotion without reading chat history.

Scope:

- Primary GPU class: AMD Radeon RX 7900 XTX / ROCm `gfx1100`.
- Secondary validation class: AMD Radeon VII / ROCm `gfx906` for runtime
  compatibility canaries and comparison rows.
- Primary roadmap/spec link: SD-3 in
  `docs/planning/spec-driven-delivery.md` and
  `docs/planning/next-roadmap.md`.
- Existing Gemma4 and Qwen evidence remains in this file; rows may stay `TBD`
  until the artifact reaches a real validation layer.

## Validation Contract

Every runtime or canary row must capture these audit fields before it can be
promoted:

| Field | Required value |
|---|---|
| `artifact` | ModelCache name, model family, PVC path, or artifact label being evaluated. |
| `context_length` | Runtime `maxModelLen`, benchmark context, or explicit `n/a` with reason. |
| `gpu_class` | Hardware class such as `gfx1100/7900xtx`, `gfx906/radeonvii`, or `sm_52/maxwell`. |
| `backend` | Runtime engine: `vllm`, `diffusers`, `llamacpp`, `ollama`, `mlc`, or `n/a` for offline artifact rows. |
| `support_level` | Lifecycle posture: `supported`, `experimental`, `deprecated`, `unsupported`, or `n/a`. Mirrors the `flexinfer.ai/runtime-support` posture in GPUProfile defaults. |
| `runtime_image` | Image digest, immutable OCI ref, or temporary tag plus follow-up to pin digest. |
| `oci_ref` | Published model OCI ref or PVC/local artifact ref when OCI is not used. |
| `observed_failure_mode` | `none` for a clean canary, or the concrete scheduler/runtime/cache failure. |
| `canary_command` | Reproducible smoke command (curl / `kubectl exec`) or `script:` path that proves Ready + coherence. `TBD: <reason>` while no command has been captured. |
| `rollback_digest` | Previous known-good runtime image digest or model OCI ref to revert to if the row regresses. `TBD: <reason>` until a known-good predecessor is recorded. |
| `spec_roadmap_link` | Spec, roadmap item, issue, MR, or commit proving why the row exists. |
| `promotion_decision` | `promote`, `conditional`, `block`, `fail`, `skip`, or `pending`. |

Timing and throughput fields (`smoke.ready_minutes`, `smoke.cold_load_min`,
`smoke.decode_tps`, `smoke.prompt_tps`, image generation seconds) are captured
in the Runtime Smoke section below rather than duplicated as table columns,
so a row can stay narrow while still pointing at canonical evidence.

Promotion rules:

- `promote`: metadata validation passes, runtime canary is coherent, the model
  reaches Ready, and the runtime image or model artifact is pinned by digest or
  immutable OCI ref.
- `conditional`: the canary serves successfully but has a documented warning,
  temporary tag, manual family override, or follow-up that does not block the
  current operator outcome.
- `block`: evidence is incomplete or a known prerequisite is missing.
- `fail`: the attempted runtime cannot satisfy the target context or returns
  incoherent/error responses.
- `skip`: intentionally not a target for this GPU class or slice.
- `pending`: no runtime evidence has been captured yet.

## Promotion-Ready Row Template

Before a runtime digest, GPUProfile support level, model canary, or user-facing
alias is promoted, add or update one matrix row with the fields below filled in.
Do not mark a row `promote` while any required field is still `TBD` unless the
missing value is explicitly explained in `observed_failure_mode` and the
decision is only `conditional`.

Copy/paste row template:

```markdown
| `<artifact-or-model>` | `<context or n/a>` | `<gfx1100/7900xtx|gfx906/radeonvii|sm_52/maxwell>` | `<backend>` | `<supported|experimental|deprecated|unsupported|n/a>` | `<registry/repo@sha256:... or n/a>` | `<oci-ref-or-pvc-ref>` | `<validator/runtime evidence summary>` | `<none or concrete failure>` | `<kubectl/curl/script command>` | `<previous digest/ref or rollback manifest>` | `<spec/issue/MR/doc link>` | `<promote|conditional|block|fail|skip|pending>` |
```

Promotion-ready checklist:

- Runtime image is digest-pinned, or the row explains why no runtime image is
  involved.
- `scripts/promote-runtime-digest.sh --apply` is allowed only when the operator
  passes `--validation-row <row/artifact>` and `--rollback-digest
  <previous-sha256>` for the populated row.
- Canary command proves the target reaches Ready and returns coherent output, or
  the row is explicitly `block`, `fail`, `skip`, or `pending`.
- Rollback digest/ref or rollback manifest path is present.
- `spec_roadmap_link` points to the spec, issue, MR, or decision that justified
  the promotion.
- One of the required hardware lanes is represented when applicable:
  `gfx1100` textgen, `gfx1100` imagegen, `gfx906` textgen/quantization,
  `gfx906` imagegen/offload.

## Validation Layers

The codebase exposes two separate validation surfaces. Keep them separate in
the row notes:

1. **Artifact metadata validator**: `build/scripts/validate_quantized_artifact.py`
   through `flexinfer quantize validate-artifact`. Offline, no GPU required.
   Checks layout, shapes, module coverage, optional generation repetition probe,
   and emits structured `checks` JSON. It does not measure cosine or perplexity.
2. **Dense-module cosine gate**: quantize-time gate when `denseModulePolicy:
   validate` and `denseModuleCosineThreshold` are set on a `ModelCache`.
   Requires re-quantization; it does not run against already-published artifacts.
3. **Runtime canary**: cluster deployment or direct runtime smoke that proves
   the target artifact/image reaches Ready and serves coherent output at the
   target context length.

## Field Capture Reference

### Metadata validator

| Field | How captured |
|---|---|
| `val.status` | `PASS` / `FAIL` from validator stdout. |
| `val.layout` | Resolved layout: `vllm-gptq`, `compressed-tensors`, or `hf-native`. |
| `val.family` | Detected or forced family id. |
| `val.shards` | `checks.shard_mode` and shard count from index. |
| `val.mods_shape` | `checks.modules_in_block_to_quantize_shape` (`nested` or `flat`). |
| `val.declared_missing_qweight` | `checks.declared_modules_without_qweight` length/list. |
| `val.quantized_modules` | Count of distinct module families with qweight tensors. |
| `val.gen_ok` | `checks.generation_probe.ok` when `--run-generation` is used. |
| `val.warnings` | Count of warnings. |
| `val.errors` | Count of errors. |

### Quantize-time cosine gate

| Field | How captured |
|---|---|
| `cos.min` | Per-layer minimum cosine across dense modules. |
| `cos.mean` | Per-layer mean cosine. |
| `cos.layers_below_threshold` | Count of layers below `denseModuleCosineThreshold`. |

### Runtime smoke

| Field | How captured |
|---|---|
| `smoke.ready_minutes` | ModelCache phase timing: download, ablit, quant, publish, runtime Ready. |
| `smoke.cold_load_min` | Fresh activation time from demand/scale-up to runtime Ready. |
| `smoke.decode_tps` | vLLM: `decode_tokens / decode_seconds`. |
| `smoke.prompt_tps` | vLLM prompt processing tokens/sec. |
| `smoke.coherent` | Manual review of smoke prompt output. |
| `runtime_image` | Pod image ID digest, Helm value digest, or OCI runtime image ref. |
| `oci_ref` | `status.publish.ociRef`, model registry ref, or PVC/local artifact ref. |

## Promotion Matrix

The four required canary lanes (`gfx1100` textgen, `gfx1100` imagegen,
`gfx906` textgen/quantization, `gfx906` imagegen/offload) are each represented
by at least one row even when evidence is incomplete.

| `artifact` | `context_length` | `gpu_class` | `backend` | `support_level` | `runtime_image` | `oci_ref` | `validation_evidence` | `observed_failure_mode` | `canary_command` | `rollback_digest` | `spec_roadmap_link` | `promotion_decision` |
|---|---:|---|---|---|---|---|---|---|---|---|---|---|
| `runtime:rocm-gfx1100` GPUProfile fallback digest promotion (2026-05-17) | n/a | `gfx1100/7900xtx+5930k` | `runtime` | `supported` | `registry.harbor.lan/flexinfer/runtime@sha256:24cbefd79152e3995c1e44623bddb1d98767da8e5ea0393fdd452a7ad3d1bea5` | n/a | MR !408 split promotion targets so the broad `gfx1100` profile updates only `deploy/gpuprofiles/gfx1100.yaml`; post-merge master pipeline #10082 passed. Local promotion gate: `scripts/promote-runtime-digest.sh gfx1100 --digest sha256:24cbefd79152e3995c1e44623bddb1d98767da8e5ea0393fdd452a7ad3d1bea5 --validation-row "runtime:rocm-gfx1100 GPUProfile fallback digest promotion (2026-05-17)" --rollback-digest sha256:04631a8db20fb7c2ea62a9846938b155d031d180c962477e011936765bcfeb88 --apply`, followed by `scripts/test-promote-runtime-digest.sh`, `scripts/check-runtime-profile-consistency.sh`, `git diff --check`, and repo tests before merge. | none for manifest promotion; serving DaemonSets are intentionally left on `gfx1100-serving`. | `scripts/promote-runtime-digest.sh gfx1100 --digest sha256:24cbefd79152e3995c1e44623bddb1d98767da8e5ea0393fdd452a7ad3d1bea5 --validation-row "runtime:rocm-gfx1100 GPUProfile fallback digest promotion (2026-05-17)" --rollback-digest sha256:04631a8db20fb7c2ea62a9846938b155d031d180c962477e011936765bcfeb88 --apply` | `sha256:04631a8db20fb7c2ea62a9846938b155d031d180c962477e011936765bcfeb88` | MR !408; `docs/dev/runtime-digest-promotion.md`; `deploy/gpuprofiles/gfx1100.yaml` | `promote` |
| `runtime:rocm-gfx1100-serving` Helm runtime digest promotion (2026-05-17) | n/a | `gfx1100/7900xtx+5930k` | `runtime` | `supported` | `registry.harbor.lan/flexinfer/runtime@sha256:7899640c4ac93e9c10b49a9a0e80e65559b448bb40a9fdf67f3f8598732c8836` | n/a | MR !408 split promotion targets so `gfx1100-serving` updates the persistent Helm runtime DaemonSet profiles without rolling them back to the broad unified image. Local promotion gate: `scripts/promote-runtime-digest.sh gfx1100-serving --digest sha256:7899640c4ac93e9c10b49a9a0e80e65559b448bb40a9fdf67f3f8598732c8836 --validation-row "runtime:rocm-gfx1100-serving Helm runtime digest promotion (2026-05-17)" --rollback-digest sha256:9fbf14371fd2d8d394c452d849f5b3eb26d8b702ba763dbeaa820df839664061 --apply`, followed by runtime profile consistency checks and repo tests. | none for manifest promotion; GPUProfile fallback remains on the broad `gfx1100` digest. | `scripts/promote-runtime-digest.sh gfx1100-serving --digest sha256:7899640c4ac93e9c10b49a9a0e80e65559b448bb40a9fdf67f3f8598732c8836 --validation-row "runtime:rocm-gfx1100-serving Helm runtime digest promotion (2026-05-17)" --rollback-digest sha256:9fbf14371fd2d8d394c452d849f5b3eb26d8b702ba763dbeaa820df839664061 --apply` | `sha256:9fbf14371fd2d8d394c452d849f5b3eb26d8b702ba763dbeaa820df839664061` | MR !408; `docs/dev/runtime-digest-promotion.md`; `deploy/system/values-k3s.yaml` | `promote` |
| `runtime:rocm-gfx906` Radeon VII runtime digest promotion (2026-05-17) | n/a | `gfx906/radeonvii` | `runtime` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4` | n/a | Promotes the rebuilt gfx906 runtime carrying the recent conservative-lane fixes into both `deploy/gpuprofiles/gfx906.yaml` and the Helm `runtime.profiles[gfx906]` image. Local promotion gate: `scripts/promote-runtime-digest.sh gfx906 --digest sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4 --validation-row "runtime:rocm-gfx906 Radeon VII runtime digest promotion (2026-05-17)" --rollback-digest sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b --apply`, followed by runtime profile consistency checks and repo tests. | none for manifest promotion; live qwen3-1p7b and imagegen smoke remains the post-merge rollout validation path. | `scripts/promote-runtime-digest.sh gfx906 --digest sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4 --validation-row "runtime:rocm-gfx906 Radeon VII runtime digest promotion (2026-05-17)" --rollback-digest sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b --apply` | `sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b` | MR !394, MR !397, MR !400, MR !408; `deploy/gpuprofiles/gfx906.yaml`; `deploy/system/values-k3s.yaml` | `promote` |
| `GPUProfile` vLLM defaults consumer (2026-05-17) | n/a | `gfx1100/7900xtx+5930k`, `gfx906/radeonvii` | `vllm` | `n/a` | n/a | n/a | Controller-managed Deployments and runtime-managed load payloads now apply `GPUProfile.spec.backends.vllm.vllm.defaults` for missing `enforceEager` and `kvCacheDtype` config keys while preserving explicit Model config. `cudagraphMode: NONE` maps to eager mode for older vLLM builds that do not expose a stable mode flag. Focused validation: `go test ./backend ./pkg/runtime ./controllers` and `git diff --check`. | none for config-default consumer. Follow-up live gfx906 vLLM canary proved the defaults were no longer the blocker; it is now blocked by runtime readiness/load separation after preemption. | `go test ./backend ./pkg/runtime ./controllers` | Revert this MR to return vLLM defaults to schema-only behavior. | `.loom/brainstorm-gpuprofile-vllm-defaults-2026-05-17.md`; `.loom/ralph-gpuprofile-vllm-defaults-2026-05-17.md`; `backend/gpu_compat.go`; `pkg/runtime/payload.go`; `controllers/model_backend.go` | `promote` |
| `gemma4-26b-a4b-gptq` default chat primary (2026-05-16) | 16384 | `gfx1100/7900xtx` | `vllm` | `supported` | `registry.harbor.lan/flexinfer/runtime@sha256:310988969f3448ccb7b6001d36df0610c40a0354cacbd7e3410cf9d9592dd187` | `pvc://gemma4-26b-a4b-gptq/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` | Promoted to own shared default chat aliases with sister instance: `gemma4-26b`, `quality-chat`, `mid-chat`, `fast-chat`, `fast-text`, `gpt-4`, `gpt-3.5-turbo`, `copilot`, `qwen3-default`, `project-mgmt`. Live pre-GitOps validation: Model `Ready`, `ConfigValid=True`, direct backend smoke returned HTTP 200, proxy smoke through `model=fast-chat` returned coherent one-word output from Gemma4. | Service label CRD cap is 10 entries, so direct resource names and the older `gemma4-26b-a4b` family label stay out of `serviceLabels`; node-specific `litellm.aliases` only. | `kubectl -n flexinfer-system run gemma26-routing-smoke --rm -i --restart=Never --image=curlimages/curl:8.11.1 -- sh -lc '<direct primary + direct sister + proxy fast-chat smoke>'` | Re-enable `fast-chat-7900xtx.yaml` in `deploy/models/kustomization.yaml` and remove fast/default labels from both Gemma4 manifests. | `deploy/models/gemma4-26b-a4b-gptq.yaml`; `docs/planning/fast-chat-resilience.md` | `pass` |
| `gemma4-26b-a4b-gptq-5930k` default chat sister 2/256 canary (2026-05-16) | 16384 | `gfx1100/5930k` | `vllm` | `supported` | `registry.harbor.lan/flexinfer/runtime@sha256:310988969f3448ccb7b6001d36df0610c40a0354cacbd7e3410cf9d9592dd187` | `pvc://gemma4-26b-a4b-gptq-oci-5930k/gemma4-26b-a4b-gptq` | Promoted with the same 10 shared service labels as the 7900xtx primary. Follow-up canary after the vLLM startup-probe budget fix raised `serverless.coldStartTimeout` to `25m` (`startupProbe.failureThreshold=750` at 2s) and matched the primary profile: `maxNumSeqs=2`, `maxNumBatchedTokens=256`. Pod reached Ready with zero restarts. Logs: weights loaded in 20.94s, model loaded in 21.69s, Dynamo transform in 16.55s, application startup complete. Direct benchmark returned coherent `1..50` output. Single run: 141 completion tokens in 2.625s (~53.7 tok/s). After the first two-way graph/capture warmup pass, three repeated `parallel2` rounds served 282 completion tokens in 2.34-2.41s (~117-120 aggregate tok/s, ~60 tok/s/request). | First two-way request after the profile change was a one-time slow capture/warmup pass: 282 tokens in 53.35s (~5.3 aggregate tok/s). Repeats were fast. 5930k remains slower than the 7900xtx host, but the previous kubelet-startup blocker for `2/256` is cleared. | `kubectl -n flexinfer-system run bench-5930k-repeat --rm -i --restart=Never --image=python:3.11-alpine -- python3 - <<'PY' ... direct service parallel2 benchmark ... PY` | Restore `coldStartTimeout: 15m`, `maxNumSeqs: 1`, and `maxNumBatchedTokens: 160` in `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml`, then reconcile `flexinfer-models`. | `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml`; `.loom/10-research.md` 2026-05-16 parity follow-through; this MR | `pass` |
| `gemma4-26b-a4b-gptq` attnfp16-clean active artifact | 8192 | `gfx1100/7900xtx` | `vllm` | `experimental` | TBD | `pvc:///models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean`; OCI TBD | `val.status=PASS`, `val.layout=vllm-gptq`, forced `val.family=gemma4-26b-a4b`, flat modules warning, 2 MoE module families quantized, no dense cosine, runtime args captured from live pod | Family auto-detection returns `None`; flat `modules_in_block_to_quantize`; runtime image digest not recorded | TBD: live canary script not yet captured to a tracked file | TBD: no prior known-good vLLM digest pinned for this artifact | SD-3 / Issue #57; `.loom/30-implementation-plan.md`; raw 2026-04-18 evidence below | `conditional` |
| `gemma4-26b-a4b-gptq` warm primary @ 16K FP8 KV (MR !343, 2026-05-13) | 16384 | `gfx1100/7900xtx` | `vllm` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:0b05b32b92e6ab99cd648837a9bf80cf3dd437275b1d97fb71378a9f829cdaac` | Same artifact path as attnfp16-clean row above; OCI TBD | Promoted to warm quality lane (priority 350, minReplicas 1, warmPolicy primary). Cache PVC migrated from Longhorn 3r → `local-path` 50Gi on `cblevins-7900xtx`. Cluster transitions observed end-to-end: Model CR applied 2026-05-13T17:55:54Z by Flux → cache copy job started 18:06:06Z (4m to copy ~17 GiB from `nvme-1r-gpu` source PVC) → vLLM pod up 18:08:30Z → API server listening 18:09:03Z → phase Ready ≈18:11Z; total reconcile-to-Ready ≈13m. Direct `/v1/chat/completions` smoke at backend Service `gemma4-26b-a4b-gptq.flexinfer-system.svc:8000` returned `"4"` for greedy `2+2` prompt (27 prompt / 2 completion tokens, stop_reason 106). Proxy smoke via `flexinfer-proxy:80` using alias `project-mgmt` returned coherent prioritization on a 3-task project-mgmt prompt (55 / 23 tokens). `qwen3-8b-fast-7900xtx` correctly scaled to Idle (pod removed) when 26B claimed the warm lane. | `qwen3-8b-fast-7900xtx` (priority 300) now cold-starts on `qwen3-default` / `qwen3-8b` / `gpt-3.5-turbo` / `copilot` / `fast-chat` alias traffic — ≤10m budget per manifest, not yet captured under real load | `kubectl run smoke --image=curlimages/curl -- curl -sS -X POST http://flexinfer-proxy.flexinfer-system.svc/v1/chat/completions -d '{"model":"project-mgmt","messages":[{"role":"user","content":"<3-task triage prompt>"}],"max_tokens":80,"temperature":0}'` | Revert MR !343 to restore `qwen3-8b-fast-7900xtx` as warm primary (manifest pair-edit, no artifact changes) | MR !343; `deploy/models/gemma4-26b-a4b-gptq.yaml`; `deploy/models/fast-chat-7900xtx.yaml`; `.loom/50-worklog.md` 2026-05-13 entry | `pass` |
| `gemma4-26b-a4b-gptq-5930k` sister instance via OCI pull (MR !352, 2026-05-14) | 16384 | `gfx1100/5930k` | `vllm` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:69569cbfc0db7c4f8755cf07ad329361a59514ebc7112fb859c5b08c8787b759` (`runtime:rocm-gfx1100-gemma4-moe-patched`) | OCI ref `registry.harbor.lan/flexinfer/gemma4-26b-a4b:gptq-w4-g128-attnfp16-clean` (digest `sha256:ef26e6c7b614e187b37a78f362d7afe176137fdf815c003cecc9b1be1fb6c932`, 42 files, ~17 GiB); pulled into PVC `gemma4-26b-a4b-gptq-oci-5930k`; serving path `pvc://gemma4-26b-a4b-gptq-oci-5930k/gemma4-26b-a4b-gptq` (one level shallower than the 7900xtx primary because `oras push` ran from inside the `gptq-w4-g128-attnfp16-clean/` subdir on the 7900xtx, so the OCI artifact landed flat under the modelPath). | Bug fixed by MR !352: prior `spec.source` had a trailing `/gptq-w4-g128-attnfp16-clean` segment that did not exist in the OCI-pulled cache; the controller cache-copy job hit `Missing source path: /src/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` on every retry until `BackoffLimit` exhausted, leaving the Model `Pending/CacheNotReady` for ~9 h after the un-pause merge. After fix: Flux reconciled `master@d81e5e4a`, controller's source-hash drift detection deleted the stale failed job, recreated a fresh cache-copy that succeeded in 72 s (OCI PVC + cache PVC both on `local-path` on the same NVMe → much faster than the 7900xtx primary's ~4 min cross-PVC copy). vLLM pod reached `1/1 Ready` ~3 min after weight load; Model phase `Loading → Ready`. Direct `/v1/chat/completions` smoke at backend Service `gemma4-26b-a4b-gptq-5930k.flexinfer-system.svc:8000` returned `"4"` for greedy `2+2` prompt (26 / 2 tokens, stop_reason 106). `/v1/models` shows both 26B instances Ready, identical `service_labels` (`gemma4-26b-a4b-gptq`, `gemma4-26b-a4b`, `gemma4-26b`, `quality-chat`, `mid-chat`, `gpt-4`, `project-mgmt`) and node-specific `litellm.aliases` (`gemma4-26b-5930k`, `gemma4-26b-a4b-5930k`). | Proxy `ResolveServiceLabel` returns only `claimants[0]` (first-by-priority) per shared label, so a 10-request load probe through `quality-chat` routed 10/10 to the 7900xtx instance — true load-balancing across instances requires using the existing `labelGroupCache` for round-robin/least-loaded routing. Tracked as the next slice. | Direct smoke: `kubectl run smoke --image=curlimages/curl -- curl -sS -X POST http://gemma4-26b-a4b-gptq-5930k.flexinfer-system.svc:8000/v1/chat/completions -H 'Content-Type: application/json' -d '{"model":"gemma4-26b-a4b-gptq-5930k","messages":[{"role":"user","content":"What is 2+2?"}],"temperature":0,"max_tokens":4}'` | Revert MR !352 to restore the broken nested source path (does not roll back the sister instance — would re-enter the Pending/CacheNotReady loop). To fully retire the sister, also revert MR !350 (un-pause). Image rollback to predecessor `sha256:0b05b32b92e6...` (decoder-debug-ON, ~7.5 tok/s) if MoE patch regresses. | MR !352 (`fix(serving): drop nested subdir from 26B 5930k source path`); MR !350 (`feat(serving): un-pause 26B instance #2 — OCI artifact seeded`); `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml`; `deploy/modelcaches/gemma4-26b-a4b-gptq-oci-5930k.yaml`; `.loom/50-worklog.md` 2026-05-14 entry | `pass` |
| Proxy round-robin Ready-member routing across shared service-labels (MR !354, 2026-05-14) | n/a (proxy-side feature) | `n/a` (proxy logic, not a model) | `n/a` | `n/a` | `registry.harbor.lan/flexinfer/flexinfer-proxy@sha256:ad1f7bd13c7bbe9164dd7df3b047c7bd23b5ce605b1801be7be76417d6c771f0` (master after pipeline 9457) | n/a (code change, not a model artifact) | New `ModelResolver.ResolveServiceLabelGroup` + `Proxy.pickReadyMember` swap the first-claimant resolver at the routing path (proxy.go:417) for a Ready-preferring round-robin picker driven by an atomic per-label counter. Sorted claimants in `refreshServiceLabelCache` so the ring is stable across cache refreshes. 5 unit tests cover single-member short-circuit, exact 10/10 split, Ready preference, alphabetical fallback when none Ready, and per-label counter isolation. Pipeline 9457 publish succeeded after one `proxy_test` retry (transient runner pod eviction, not a logic failure). After deployment rollout, a 20-request load probe through `quality-chat` (`POST /v1/chat/completions` with 0.5 s spacing) showed exactly 10 served by `gemma4-26b-a4b-gptq` and 10 served by `gemma4-26b-a4b-gptq-5930k` (the `model` field in each successful response identifies the upstream `--served-model-name`). Direct `service_labels` aliases (`quality-chat`, `mid-chat`, `gpt-4`, `project-mgmt`, `gemma4-26b-a4b-gptq`, `gemma4-26b-a4b`, `gemma4-26b`) all inherit the same behavior. | Concurrent-load race: an earlier 20-req probe **without** spacing returned 16/20 success and 4/20 vLLM 404s (`The model 'gemma4-26b-a4b-gptq-5930k' does not exist.` from the 7900xtx upstream — i.e., the proxy occasionally forwarded a 5930k-labeled body to the wrong Service during the 5-second `serviceLabelCacheTTL` refresh window). With 0.5 s spacing the probe is 20/20 clean. Fixed by MR !356 (see next row). | Probe script (paste into a curl sidecar): `for i in $(seq 1 20); do curl -sS -X POST http://flexinfer-proxy.flexinfer-system.svc/v1/chat/completions -H 'Content-Type: application/json' -d '{"model":"quality-chat","messages":[{"role":"user","content":"UP"}],"max_tokens":3,"temperature":0}' \| grep -o '"model":"[^"]*"' ; sleep 0.5 ; done \| sort \| uniq -c` — expected `10 "model":"gemma4-26b-a4b-gptq"` + `10 "model":"gemma4-26b-a4b-gptq-5930k"`. | Revert MR !354 (`feat(proxy): round-robin Ready members for shared service labels`) to restore the first-claimant behavior. The Model CRs do not need any change to roll back — the shared `service_labels` declarations are inert without the proxy-side picker. | MR !354; `internal/proxy/model_resolver.go` (`ResolveServiceLabelGroup`); `internal/proxy/resolver.go` (`pickReadyMember`); `internal/proxy/proxy.go:417` (call site); `internal/proxy/pick_member_test.go`; `.loom/50-worklog.md` 2026-05-14 entry. | `pass` |
| 26B fleet asymmetric decode rate: 5930k node is 2.2x slower (2026-05-14, documented, not fixed) | n/a (operational finding) | `n/a` | `vllm` | `experimental` | n/a (no image change) | n/a (same artifact, same config) | Matched-workload benchmark: both pods run identical `gemma4-26b-a4b-gptq` vLLM config (`enforce_eager: true`, `maxNumSeqs: 1`, FP8 KV, TRITON_ATTN). Same prompt ("Count from 1 to 50…", 32 prompt tokens, ~141 completion tokens). Sequential 3-req probe direct to each upstream Service from a host-timed shell: **7900xtx avg 22.99 s/req, 5930k avg 48.89 s/req (2.13x slower)**. Engine init logs corroborate: aiter JIT compile 12.2 s vs 22.9 s (1.88x); model weight load 21.5 s vs 40.1 s (1.87x). CPU diff via `lscpu` from each node: cblevins-7900xtx = AMD Ryzen 9 7900X3D (12c/24t Zen 4, 5.6 GHz boost, 2023); cblevins-5930k = Intel Xeon E5-2680 v4 (14c Broadwell-EP, 2.4 GHz base / 3.3 GHz boost, 2016 — hostname is legacy; actual CPU is the Xeon). `enforce_eager: true` (correctness lock, see manifest comment) disables CUDA graphs so every decoded token bears Python-side CPU overhead; `maxNumSeqs: 1` removes batching that would amortize that cost. Result: the gap is hardware-bound. Cannot be fixed serving-side. | The 26B fleet currently round-robins reqs 1:1 between the two upstreams, so mean request latency is `(22.99 + 48.89) / 2 = 35.9 s` — about 1.6x worse than what an all-7900xtx config would yield (`~23 s/req`). 7900xtx alone would saturate at `maxNumSeqs: 1 × 1 instance = 1 concurrent req`. Mitigation options live in the worklog 2026-05-14 entry; none are implemented today. | Reproduce: `for i in 1 2 3; do t0=$(python3 -c 'import time; print(time.time())') ; kubectl run probe-$i --rm -i --restart=Never --quiet --overrides='{\"spec\":{\"tolerations\":[{\"key\":\"dedicated\",\"operator\":\"Equal\",\"value\":\"gpu\",\"effect\":\"NoSchedule\"}],\"nodeSelector\":{\"kubernetes.io/hostname\":\"<NODE>\"}}}' --image=curlimages/curl:8.5.0 --command -- curl -sS -m 120 -X POST http://<MODEL>.flexinfer-system.svc:8000/v1/chat/completions -H 'Content-Type: application/json' -d '{\"model\":\"<MODEL>\",\"messages\":[{\"role\":\"user\",\"content\":\"Count from 1 to 50 with each number on its own line, no commentary.\"}],\"temperature\":0,\"max_tokens\":200}' >/dev/null; t1=$(python3 -c 'import time; print(time.time())'); python3 -c \"print(f'seq=$i elapsed={(${t1}-${t0}):.2f}s')\"; done` (run with `<NODE>=cblevins-7900xtx`, `<MODEL>=gemma4-26b-a4b-gptq` and again with `<NODE>=cblevins-5930k`, `<MODEL>=gemma4-26b-a4b-gptq-5930k`). | n/a (no change to roll back) | flexinfer `.loom/50-worklog.md` 2026-05-14 entry; engine init logs in `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq{,-5930k}-* \| grep -iE 'weights took\|aiter'`. | `documented` |
| Proxy concurrent-load cross-routing fix: stop auto-LeastLoaded for label-group (MR !356, 2026-05-14) | n/a (proxy-side fix) | `n/a` (proxy logic) | `n/a` | `n/a` | `registry.harbor.lan/flexinfer/flexinfer-proxy@sha256:c5c4497cc6a102df1328d65022e4685dc9c7d6c0c3137b6ba62904260a23af90` (master after pipeline 9473) | n/a (code change) | Root cause was NOT a cache-refresh race: `internal/proxy/routing.go:getRoutingStrategy` was auto-defaulting to `StrategyLeastLoaded` for any model in a label group (≥2 service-label claimants). Paired with `refreshEndpoints`' label-group aggregation pass that writes the **union** of all members' pod endpoints into each member's router ring, that pre-existing logic cross-routed requests: a body resolved to `gemma4-26b-a4b-gptq-5930k` could be forwarded to the 7900xtx pod (10.42.0.7:8000), which 404s because `--served-model-name=gemma4-26b-a4b-gptq`. Captured via the per-request `slog.Debug("forwarding to upstream", model, resolved_model, backend_model, target, target_pod)` log (MR !355) with `--log-level=debug` patched into the live deployment — entries showed `model=gemma4-26b-a4b-gptq-5930k target=http://10.42.0.7:8000`. Fix removes the two `isModelInLabelGroup` auto-default branches in `getRoutingStrategy` (v1alpha2 and v1alpha1 paths). The MR !354 picker handles cross-model selection on its own; the router branch now stays dormant unless an operator explicitly opts in via `flexinfer.ai/routing`. The aggregation in `refreshEndpoints` is preserved for that explicit case (`TestRefreshEndpoints_LabelGroupAggregation` still passes). Test `TestGetRoutingStrategy_LabelGroup_DefaultsToLeastLoaded` renamed → `_StaysDefault` with inverted assertion + long lock-in comment. **Post-rollout proof:** 20 reqs at parallelism 2 → **20/20 success, exact 10/10 split**, and 16/16 forwarding logs showed model-name matching target (0 mismatches). | At higher parallelism (`-P 10`, `-P 20`) HTTP=000 connection failures appear because both 26B Models run `maxNumSeqs: 1` and queue-saturate, NOT because of routing. 50 reqs at `-P 10`: 41/50 success / 9/50 timeouts. 100 reqs at `-P 20`: 58/100 success / 42/100 timeouts. Scaling beyond 2 concurrent requests would need higher `maxNumSeqs` per upstream (separate config-tuning slice). | `kubectl run probe-p2 --image=curlimages/curl -- sh -c 'for i in $(seq 1 20); do echo "$i"; done \| xargs -P 2 -I{} sh -c "curl -sS -X POST http://flexinfer-proxy.flexinfer-system.svc/v1/chat/completions -H 'Content-Type: application/json' -d '\\''{\"model\":\"quality-chat\",\"messages\":[{\"role\":\"user\",\"content\":\"UP\"}],\"max_tokens\":3,\"temperature\":0}'\\''"' ; expected exact 10/10 split between `gemma4-26b-a4b-gptq` and `gemma4-26b-a4b-gptq-5930k`. Cross-routing detection: tail proxy logs (with `--log-level=debug`) for `"forwarding to upstream"` and assert `model` matches the Service host in `target`. | Revert MR !356 to restore the auto-default-to-LeastLoaded for label-group members. That re-introduces the cross-routing bug under concurrent load but keeps the MR !354 picker working at low concurrency. | MR !356; `internal/proxy/routing.go:230-268` (`getRoutingStrategy` without auto-default); `internal/proxy/label_group_test.go` (`TestGetRoutingStrategy_LabelGroup_StaysDefault`); pipeline 9473 (publish 214 s, total ~10 min); `.loom/50-worklog.md` 2026-05-14 entry. | `pass` |
| `gemma4-26b-a4b-gptq` hybrid-v10 on PVC | n/a, not served | `gfx1100/7900xtx` | `n/a` | `experimental` | n/a | `pvc:///models/gemma4-26b-a4b-gptq/gptq-w4-g128-hybrid-v10`; OCI n/a | `val.status=PASS`, `val.layout=vllm-gptq`, forced `val.family=gemma4-26b-a4b`, 9 module families, no dense cosine | Not served; `self_attn.v_proj` present on only 25/30 layers, likely `attention_k_eq_v` but not promotion-ready | n/a: artifact-only, never served | n/a: artifact-only | SD-3 / Issue #57; raw 2026-04-18 evidence below | `block` |
| `gemma4-26b-a4b-gptq-long` fp16-KV canary | 32768 target; observed max estimate 8896 | `gfx1100/7900xtx` | `vllm` | `experimental` | TBD | Model ref inherits hybrid/long cache; OCI n/a | Inherits validator evidence from hybrid line; live canary loaded weights in 56.69s with 17.74 GiB model memory | vLLM KV memory ceiling: 1.87 GiB available for KV, 6.88 GiB required for 32768 tokens; blocks 16K/32K promotion on fp16-KV lane | TBD: failing canary; recapture once KV ceiling fix lands | TBD: no prior 16K/32K success to roll back to | SD-3 / Issue #57; `.loom/gemma4-26b-31b-gptq-turboquant-plan.md`; raw 2026-04-26 evidence below | `fail` |
| `gemma4-26b-a4b-gptq-dense` dense validate rebuild | TBD: rebuild not yet completed | `gfx1100/7900xtx` | `n/a` | `experimental` | n/a | Dense-validated cache; OCI n/a | Dense cosine not reached; re-quant required with `denseModulePolicy=validate` | 4h abliteration deadline stopped at harmful prompt 80/128; retry restarts partial harmful pass | TBD: rebuild not yet completed | n/a: artifact never reached runtime | SD-3 / Issue #57; raw 2026-04-26 evidence below | `block` |
| `gemma4-31b-gptq` keqv recovery | 2048 | `gfx1100/7900xtx`, 2 GPUs | `vllm` | `experimental` | TBD | `pvc://gemma4-31b-gptq/gemma4-31b-gptq/gptq-w4-g128-keqv`; OCI n/a | Postprocess/copy succeeded; `val.status=PASS`; direct smoke returned HTTP 200 with answer `4` in 0.158s, then 0.304s after restoring 31B | Runtime image digest not recorded; context only proven at 2048 | `kubectl port-forward svc/gemma4-31b-gptq 8000:8000` then `/v1/completions` greedy `2+2` smoke (raw 2026-04-26 evidence below) | TBD: runtime image digest not yet pinned | SD-3 / Issue #57; raw 2026-04-26 evidence below | `conditional` |
| `gemma4-e4b-gptq` | TBD: not yet built | `gfx1100/7900xtx` | TBD: backend not chosen | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| `omnicoder-9b-gptq` | TBD: not yet served | `gfx1100/7900xtx` | TBD: backend not chosen | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| `qwen35-9b-gptq-gfx1100` | TBD: not yet served | `gfx1100/7900xtx` | `vllm` | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| **Required canary: `gfx1100` textgen** — `qwen36-27b-gptq` abliterated GPTQ W4_G128 | 8192 | `gfx1100/5930k` | `vllm` | `experimental` | `registry.harbor.lan/flexinfer/vllm:rocm-gfx1100-qwen35-patched-nodiag-textcfg` (digest TBD) | `registry.harbor.lan/flexinfer/qwen36-27b:gptq-w4-g128-gfx1100@sha256:fe3a6bea0cd2cdf254a5db6194e01402f1f7f93c4b86d8c717695470fdd3849d` | Cache Ready; vLLM reached Ready with `quantization=gptq`, `kvCacheDtype=auto`, `maxNumSeqs=2`; direct proxy and service smoke returned HTTP 200; quarantined from reconciled serving manifests on 2026-05-07; DEBT-302 adds warning-first publish validation with `layout=vllm-gptq`, `family=qwen36-27b`, and `checks.gdn_gptq_policy` to surface any `linear_attn.*.qweight` tensors before OCI publish | First activation exposed proxy `lastActiveTime` conflict; cold start was dominated by 17.6GB image pull; `fp8_e4m3` KV crashed Triton cache update; `gptq_marlin` rejected because artifact config declares `gptq`; current `gptq` runtime serves incoherent output (`!!!!!!!!!!!!` / multilingual junk), flat punctuation logprobs, and live profile traffic like `-current Lockheedпуст劳逸...`; too slow for the 5930k shared lane | Direct `/v1/completions` greedy smoke against `qwen36-27b-gptq:8000` for `The color of the sky is` (raw 2026-05-06 evidence below); publish validator gate runs during next qwen36 ModelCache publish | TBD: failing canary; predecessor `qwen3-14b-abliterated` GPTQ digest is referenced in MR !247 but has no captured success on 5930k to roll back to | MR !247 replacement; MR !248 runtime hardening; MR !253/!254 quiet runtime; 2026-05-05, 2026-05-06, 2026-05-07 smoke evidence; DEBT-302 validator tests | `fail` |
| `qwen3-14b-gptq` | TBD: not yet served | `gfx1100/5930k` | `vllm` | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| **Required canary: `gfx1100` imagegen** — `gonzalomo-fluxpony-imagegen` FLUX Schnell text-to-image | n/a, 512x512 + 1024x1024 warmup resolutions | `gfx1100/5930k` | `diffusers` | `supported` | TBD: diffusers runtime digest not yet pinned in this matrix; see `deploy/models/gonzalomo-fluxpony-imagegen.yaml` | `HF://black-forest-labs/FLUX.1-schnell` (Apache 2.0); manifest `deploy/models/gonzalomo-fluxpony-imagegen.yaml` | NF4 + bfloat16 compute dtype; `WARMUP_RESOLUTIONS=512x512,1024x1024` precompiles MIOpen kernels; `MIOPEN_FIND_MODE=2` works around ROCm#4729 VAE crash; primary imagegen on `5930k-imagegen-textgen` shared lane (priority 200) per current model layout | TBD: live cold-load + 512/1024 generation timings not yet captured to a tracked artifact in this matrix | TBD: capture `curl /v1/images/generations` once runtime digest is pinned | TBD: no prior diffusers runtime digest recorded for this lane | RG-4 / `.loom/gfx1100-gfx906-platform-enhancements-plan.md` Slice 4; `docs/user/backends-rocm-gfx1100.md:344-460` | `pending` |
| `gemma4-31b-gptq` Radeon VII comparison | n/a | `gfx906/radeonvii` | `n/a` | `unsupported` | n/a | n/a | n/a | Off-gfx1100 comparison row; VRAM ceiling for this promotion lane | n/a: not a target | n/a | SD-3 / Issue #57 | `skip` |
| **Required canary: `gfx906` textgen/quantization** — Qwen3.5 GPTQ Radeon VII pipeline (`docs/user/gptq-quantization-runbook.md`) | TBD: gfx906 runtime currently paused (DiskPressure) so no live serving canary | `gfx906/radeonvii` | `vllm` | `deprecated` | TBD: gfx906 vLLM runtime is paused via `flexinfer.ai/runtime-paused=true` after the digest pull repeatedly hit DiskPressure | TBD: 31B GPTQ artifact reused from gfx1100 (`pvc:///gemma4-31b-gptq/gptq-w4-g128-keqv`) | GPTQ runbook documents abliteration + GPTQ flow on Radeon VII (`docs/user/gptq-quantization-runbook.md`); 2026-05-07 evidence below records DaemonSet pause + DiskPressure history. CPU loading + community PyTorch wheel restore allocations under 16 GiB. Live serving canary not currently runnable on radeonvii. | Root-backed containerd fills to 100% on first pull of the 17 GiB digest-pinned `runtime` image, evicting kubelet workloads. The replacement `qwen3-1p7b-tools-radeonvii` llama.cpp lane is queued precisely because vLLM cannot run here today. | TBD: re-enable canary after storage relocation; recapture before lifting `runtime-paused` | `registry.harbor.lan/flexinfer/runtime@sha256:7c05960614517dbd5d6453944125a01e78f0451f6695467a8eaf6a6859d461dd` (last gfx906 runtime digest before the `dd0a1936...` promotion that hit DiskPressure) | `.loom/gfx1100-gfx906-platform-enhancements-plan.md` Slice 5; `docs/user/gptq-quantization-runbook.md`; 2026-05-07 gfx906 runtime digest promotion evidence below | `pending` |
| **Required canary: `gfx906` imagegen/offload** — `sdxl-inpainting-radeonvii` Diffusers inpaint | n/a, 512x512 image edit | `gfx906/radeonvii` | `diffusers` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:94045d0ca4b12deb3c46bb22070f67bfedad8b719bb992e5d3ce128ad27ad597` | `local:///models/flexinfer-system/sdxl-inpainting-radeonvii` | Slim runtime image (cycle 2: `Dockerfile.runtime-gfx906` on `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` base, 36.9 GB extracted vs prior 59.2 GB) promoted via MR !282 after MR !281. DaemonSet pod Ready on `cblevins-radeonvii`; cold-start `/v1/images/edits` smoke returned HTTP 200 in 107.7s with one 512x512 PNG, `b64_len=252372`. Pre-pull verified root holds at 65% (78G/127G used) post-image-pull; bind-mounted `/var/lib/flexinfer/models` to `/mnt/nvme/longhorn/flexinfer/models` via fstab, reclaiming 21G on root. | None on the runtime path. Cold-start latency increased from prior 48.35s warm to 107.7s cold (deployment scale-up + weights load from freshly bind-mounted NVMe path). Failed pull on root LVM exposed pull-time peak ~1.5x final extracted size. | Multipart `POST /model/sdxl-inpainting-radeonvii/v1/images/edits` through `flexinfer-proxy` with 512x512 PNG image+mask (raw 2026-05-07 evidence below) | `registry.harbor.lan/flexinfer/runtime@sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97` (the prior 59.2 GB digest replaced by this promotion) | RG-4 / `.loom/gfx1100-gfx906-platform-enhancements-plan.md`; `.loom/gfx1100-gfx906-next-round-plan.md` Track B-3; 2026-05-07 Radeon VII evidence below | `conditional` |
| `qwen3-1p7b-tools-radeonvii` GGUF tool-router | 8192 | `gfx906/radeonvii` | `llamacpp` | `experimental` | Persistent runtime `registry.harbor.lan/flexinfer/runtime@sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b`; standalone profile image `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` digest TBD | `HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF` / `qwen3-1.7b-q4_k_m.gguf` | Cache prefetched and ready on 2026-05-16. MR !394 fixed runtime-manager fallback from stale absolute backend paths to `PATH`; MR !395 added the missing live `runtime:rocm-gfx906` publish job. MR !397 separated the flexinfer-runtime API port (`8080`) from the runtime-managed backend port (`8000`); MR !399 changed the canary to Local cache and staged the GGUF under `/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf`. MR !400 hardened llama.cpp launch args, and pipeline 9980 published this runtime digest. MR !402 made the reconciled manifest use the proven CPU fallback, and MR !403 fixed proxy fallback routing to prefer the actual Service port. Normal-path proxy smoke returned HTTP 200 with content `Blue`, `prompt_per_second=119.15`, and `predicted_per_second=69.99`. | GPU llama.cpp on Radeon VII still aborts in `hipMemGetInfo`; this canary passes only as a CPU fallback with ROCm devices hidden while remaining colocated with the gfx906 runtime. Keep `tool-router` and `qwen3-1.7b` aliases only; do not make this the default chat route unless coherence and latency pass. | `kubectl -n flexinfer-system run smoke --rm -i --restart=Never --image=curlimages/curl:8.11.1 -- curl -sS http://flexinfer-proxy.flexinfer-system.svc.cluster.local/model/qwen3-1p7b-tools-radeonvii/v1/chat/completions -H 'Content-Type: application/json' -d '{"model":"qwen3-1.7b-tools","messages":[{"role":"user","content":"Reply with exactly one word: blue"}],"max_tokens":8,"temperature":0}'` | `registry.harbor.lan/flexinfer/runtime@sha256:b36bb0ab008efa3d8a127cdf7cd9813c8ad88ddf7d62d8736bc6ad0976fe20f0` | RG-4 / `.loom/real-hardware-platform-improvements-plan.md` Slice 4; MR !394; MR !395; MR !397; MR !399; MR !400; MR !402; MR !403; pipelines 9917, 9946, 9976, 9980, and 9994; 2026-05-16 live failure and final proxy-smoke evidence below | `conditional` |
| `qwen3-1p7b-vllm-radeonvii` vLLM canary prerequisites (2026-05-17) | 2048 | `gfx906/radeonvii` | `vllm` | `experimental` | Persistent runtime `registry.harbor.lan/flexinfer/runtime@sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4`; standalone vLLM image `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:84f0ae2bb1ea46163885aad55181540bee9995b4b4b0c656f3943b7580e07e1e` | `HF://facebook/opt-125m`; Local cache at `/models/flexinfer-system/qwen3-1p7b-vllm-radeonvii` | RALPH fixed the canary prerequisites: DNS-label-safe resource name, `Local` cache, canary-scoped aliases, standalone image path for gfx906 vLLM, and image-pull-secret propagation for controller-created model Deployments. The Radeon VII k3s containerd image store was moved from root LVM to the NVMe-backed `/mnt/nvme/longhorn/k3s-containerd/containerd` bind mount. Harbor pull access later worked and the standalone canary image reached container startup; MR !420 added a `transformers` tokenizer compatibility hook, MR !421 added a Triton `default_cache_dir` compatibility hook, MR !422 added `default_dump_dir`, MR !424 added `default_override_dir`, MR !426 fixed active-pod cache-refresh preservation, MR !427 adds a guarded PyTorch ROCm `mem_get_info` fallback, MR !428 switched the canary artifact from unsupported Qwen3 to Qwen2.5, MR !434 exposed `disableSlidingWindow` for vLLM args, and MR !435 pivoted the artifact to OPT-125M after Qwen2.5's SWA/rope path proved unsafe. The next image-source slice extracts those compatibility hooks into `build/scripts/install_vllm_gfx906_compat.py` and adds faulthandler / child-process traceback diagnostics before another live smoke. | No HTTP 200 vLLM smoke yet. OPT-125M is cached and the pinned standalone image launches without node pressure, but both attention variants fail at model weight load: default `VLLM_USE_TRITON_FLASH_ATTN=0` and a live-only forced `VLLM_USE_TRITON_FLASH_ATTN=1` / `ROCmFlashAttention` patch both reach `model_runner.py:1110] Starting to load model facebook/opt-125m...` and then report `RuntimeError: Engine process failed to start`; the model container exits `0` and restarts. This is now an image/runtime worker-crash blocker, not a cache, registry, profile-env, Qwen architecture, or node-capacity blocker. The next canary must use a newly published diagnostic digest and capture the child traceback or faulthandler output before trying more model/profile flags. | `kubectl -n flexinfer-system run smoke-gfx906-vllm --rm -i --restart=Never --image=curlimages/curl:8.11.1 -- curl -sS -m 900 -X POST http://flexinfer-proxy.flexinfer-system.svc.cluster.local/model/qwen3-1p7b-vllm-radeonvii/v1/chat/completions -H 'Content-Type: application/json' -d '{"model":"opt-125m-vllm","messages":[{"role":"user","content":"The color of the sky is"}],"max_tokens":16,"temperature":0}'` | Delete `Model/qwen3-1p7b-vllm-radeonvii`, restore `qwen3-1p7b-tools-radeonvii` `minReplicas: 1`, and recycle `flexinfer-runtime-gfx906` if `/readyz` hangs. | `.loom/ralph-gfx906-vllm-smoke-2026-05-17.md`; `.loom/ralph-gfx906-vllm-worker-diagnostics-2026-05-19.md`; `deploy/models/qwen3-1p7b-vllm-radeonvii.yaml`; `deploy/gpuprofiles/gfx906.yaml`; MR !420; MR !421; MR !422; MR !424; MR !426; MR !427; MR !428; MR !434; MR !435; live imagefs/auth/tokenizer/triton/mem-info/runtime-lock/CK-FA/SWA/OPT follow-up 2026-05-18/19 | `block` |

## Artifact Layout Notes

- `--layout hf-native`: standard HF safetensors, no quantization metadata
  (source or abliteration artifact).
- `--layout vllm-gptq`: GPTQ with vLLM-style
  `modules_in_block_to_quantize` in `config.json`.
- `--layout compressed-tensors`: RedHatAI / compressed-tensors pack; currently
  about 2 tok/s on gfx1100, avoid for promotion unless a slice specifically
  targets that layout.

## Raw Evidence Archive

Large JSON outputs and smoke transcripts go under
`.loom/local/validation/<family>/<timestamp>/`. Summaries only belong in this
tracked file. Each archive should include the exact command, artifact path,
runtime image digest or OCI ref when available, and smoke response transcript.

### 2026-05-18 gfx906 vLLM imagefs relocation and Harbor auth blocker

The Radeon VII node image-store blocker was addressed live before retrying the
vLLM canary:

- `cblevins-radeonvii` k3s containerd was moved from
  `/var/lib/rancher/k3s/agent/containerd` on root LVM to the NVMe-backed
  `/mnt/nvme/longhorn/k3s-containerd/containerd` bind mount.
- Root LVM had roughly 100 GiB free afterward, and the containerd imagefs
  mounted from NVMe had hundreds of GiB free.
- The node remained `Ready=True` and `DiskPressure=False`.
- `flexinfer-runtime-gfx906` was recycled after the k3s restart and returned
  `1/1 Running`.
- A proxy demand for `qwen3-1p7b-vllm-radeonvii` staged the local cache and
  reached the standalone vLLM image pull path without returning DiskPressure.

The next blocker is registry authorization, not storage pressure. Pulling
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:beadf394fc81c031799799f5d965664e419e5f3ffb4c5873a9d7677f0e1e06b8`
returned `401 Unauthorized` with both the namespace `harbor-creds` pull secret
and a temporary dockerconfig secret derived from `harbor-oci-creds`. The canary
was cleaned up afterward: the smoke pod was deleted, the standalone Deployment
was scaled to zero, cache staging completed, and the Model returned to `Idle`.

### 2026-05-18 gfx906 vLLM tokenizer compatibility blocker

After Harbor pull access was fixed, the same vLLM canary reached container
startup on Radeon VII. The old standalone image then failed before readiness:
vLLM 0.7.3 called `Qwen2Tokenizer.all_special_tokens_extended`, but the
installed `transformers` 5.8.0 tokenizer did not expose that attribute.

MR !420 rebuilds the standalone gfx906 vLLM image with a small site-packages
compatibility hook and pins the image to
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:0eebd5a70e184d31c706457ef4b7f393b10d4193a7b728dd5112a17d3457797f`.
The same MR hardens shared-GPU leader choice so transient cache revalidation
cannot scale down an active Pending/Loading canary during the cold-start window.

Live validation of that pinned image got past the tokenizer failure and reached
ROCm attention backend selection. The next blocker is another dependency API
drift: vLLM 0.7.3 imports `triton.runtime.cache.default_cache_dir`, but the
installed Triton package does not export it. The next image rebuild extends the
site hook with a `default_cache_dir` shim.
That rebuilt image is pinned as
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:bec2ad7d136ce9c2add97c692901dec8e10a9240ecdc2960ed5b028cd18e24e1`.

Live validation of that pinned image got past `default_cache_dir` and reached
the same ROCm attention backend selection point. The next missing symbol was
`triton.runtime.cache.default_dump_dir`. MR !422 extends the hook with a
`default_dump_dir` shim using `TRITON_DUMP_DIR` or `~/.triton/dump`. That image
is pinned as
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:8350619f10e31e5172fd94e8686e9b185292d6182c911711d4e026e4acce23d6`.

Live validation of that pinned image got past `default_dump_dir`; the remaining
vLLM 0.7.3 Triton cache-manager import was `default_override_dir`. The same
run reproduced the controller cache-refresh downscale: `ensureCache` briefly
returned not-ready and forced replicas to zero while the active shared model was
still pulling/loading. MR !424 adds `default_override_dir` and preserves active
shared-GPU `Pending`/`Loading` models during cache refresh. The image is pinned
as
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:021d31f322b2ff789a0d7bfa1f79c713b8a1cbcf3498e2bc58ddb0a5fe26386d`.

Live validation of the MR !424 image after MR !426 showed the controller guard
held: the active pod was not scaled down during cache refresh. vLLM then failed
inside PyTorch memory discovery because `torch.cuda.mem_get_info()` returned
HIP `invalid argument` on gfx906. MR !427 adds a guarded fallback based on
device properties and PyTorch allocation counters. The image is pinned as
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:84f0ae2bb1ea46163885aad55181540bee9995b4b4b0c656f3943b7580e07e1e`.

### 2026-05-16 gfx906 llama.cpp runtime path failure

RALPH RG-4 follow-up checked the Radeon VII conservative lane before running a
promotion smoke. Live state:

- `cblevins-radeonvii` was Ready with `flexinfer.ai/gpu.arch=gfx906` and one
  15Gi GPU.
- `qwen3-1p7b-tools-radeonvii` cache prefetch had succeeded, but the Model was
  `Loading` with `Ready=False`, reason `RuntimeStarting`.
- The runtime pod
  `flexinfer-runtime-gfx906-q6wgb` was running image
  `registry.harbor.lan/flexinfer/runtime@sha256:94045d0ca4b12deb3c46bb22070f67bfedad8b719bb992e5d3ce128ad27ad597`
  on `cblevins-radeonvii`.
- `kubectl exec` showed the image contains
  `/opt/llamacpp/bin/llama-server`, but not
  `/opt/src/llama.cpp/build/bin/llama-server`.
- Runtime logs showed repeated load failures:
  `failed to start backend: fork/exec /opt/src/llama.cpp/build/bin/llama-server: no such file or directory`.

Decision: do not promote the `qwen3-1p7b-tools-radeonvii` canary yet. The
active slice teaches `flexinfer-runtime` to preserve valid absolute backend
commands while falling back to the same basename in `PATH` when a runtime image
moves a bundled binary. After that runtime image is rebuilt and rolled out,
re-run the one-word proxy smoke recorded in the row above.

### 2026-05-16 gfx906 llama.cpp runtime port collision

After MR !394 and MR !395, the promoted runtime digest
`sha256:a536d25f8b0f154e16c080ce852da6f1430783bedcb53e7098cee3a9baa264c5`
rolled out to `flexinfer-runtime-gfx906-78jvf`.

- Runtime startup logs reported the new digest and loaded
  `qwen3-1p7b-tools-radeonvii`.
- The path fallback worked: the runtime requested
  `/opt/src/llama.cpp/build/bin/llama-server` and resolved
  `/opt/llamacpp/bin/llama-server` from `PATH`.
- The backend then crashed because `llama-server` args still included
  `--port 8080`, and the flexinfer-runtime API already listened on `:8080`.
- MR !397 keeps standalone llama.cpp on port `8080`, but injects and routes
  the runtime-managed backend on port `8000`, matching the runtime DaemonSet's
  `backend` container port. Master pipeline 9946 published
  `runtime:rocm-gfx906-6c92809b` as
  `sha256:b36bb0ab008efa3d8a127cdf7cd9813c8ad88ddf7d62d8736bc6ad0976fe20f0`.

### 2026-05-16 gfx906 runtime-local GGUF path blocker

After MR !398 promoted the port-fix digest and Flux rolled out
`flexinfer-runtime-gfx906-bn4qf`, live logs proved:

- Runtime digest `sha256:b36bb0ab...` was active and the path fallback still
  resolved `/opt/llamacpp/bin/llama-server`.
- The backend subprocess started with `--port 8000`, proving MR !397.
- The model path was still `/models/qwen3-1p7b-tools-radeonvii`, but that path
  did not exist in the runtime pod. Existing runtime-local caches are staged
  under `/models/flexinfer-system/<model>`.
- `qwen3-1p7b-tools-radeonvii` used `cache.strategy: SharedPVC`, unlike the
  working Radeon VII imagegen models that use `Local`.

Active fix: switch this canary to `cache.strategy: Local` and make the shared
runtime payload builder append `config.ggufFile` for Local-cache GGUF models,
yielding `/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf`.

### 2026-05-16 gfx906 llama.cpp backend GGUF launch guard

After MR !399 merged and Flux rolled out chart `1.0.2+f39decc7a946.3`, live
state showed the canary cache was now `Local`, cache status was Ready, and the
GGUF file existed in the runtime pod at
`/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf`.

However, a fresh runtime restart still launched llama.cpp with:

```text
--port 8000 --model /models/flexinfer-system/qwen3-1p7b-tools-radeonvii
srv load_model: loading model '/models/flexinfer-system/qwen3-1p7b-tools-radeonvii'
hipMemGetInfo(free, total)
```

Decision: harden `backend/llamacpp` itself so a directory-shaped `ModelPath`
plus `config.ggufFile`/`modelFile` resolves to the actual GGUF file before
building `llama-server --model` args. This keeps runtime-managed and
controller-managed launch paths aligned even when a caller passes the staged
cache root.

### 2026-05-16 gfx906 llama.cpp backend digest promotion

MR !400 merged at `d3a86071`. Master pipeline 9980 published:

- `registry.harbor.lan/flexinfer/runtime:rocm-gfx906@sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b`
- `registry.harbor.lan/flexinfer/runtime:rocm-gfx906-d3a86071@sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b`

Promote that digest into `deploy/gpuprofiles/gfx906.yaml` and
`deploy/system/values-k3s.yaml`, then reconcile Flux and rerun the one-word
proxy smoke for `qwen3-1p7b-tools-radeonvii`.

### 2026-05-16 gfx906 qwen router CPU fallback

After the `f0537a...` runtime rollout, `flexinfer-runtime-gfx906-z7czl` started
the backend with the correct file path:

```text
--port 8000 --model /models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf
srv load_model: loading model '/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf'
hipMemGetInfo(free, total)
```

The model path issue is resolved. The remaining blocker is Radeon VII ROCm
inside the llama.cpp image. Manual validation while the canary was temporarily
set to `minReplicas: 0` showed:

- `nGPULayers=0` alone still initializes ROCm and aborts in `hipMemGetInfo`,
  even with `-fit off`.
- Hiding ROCm devices with `HIP_VISIBLE_DEVICES=-1` and
  `ROCR_VISIBLE_DEVICES=-1` lets the same GGUF load CPU-only.
- Manual OpenAI-compatible `/v1/chat/completions` smoke returned HTTP 200 with
  content `blue`, `prompt_per_second=124.5`, and `predicted_per_second=74.6`
  using a 1024-token context.
- The production-shaped 8192-context, parallel-2, q4 KV config also reached
  `/health` OK CPU-only with ROCm hidden.

Decision: keep the router colocated with the Radeon VII runtime for now, but
set `nGPULayers: 0`, `hipVisibleDevices: "-1"`, and
`rocrVisibleDevices: "-1"` in the reconciled Model. This is a conservative
tool-router fallback, not a general gfx906 llama.cpp GPU promotion.

### 2026-05-16 gfx906 qwen router proxy service-port blocker

After MR !402 merged and Flux reconciled, `qwen3-1p7b-tools-radeonvii` reached
Ready on runtime digest
`sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b`.
The live runtime pod loaded the GGUF and listened on backend port `8000`.
Direct localhost OpenAI-compatible smoke inside the runtime pod returned HTTP
200 with content `Blue`.

The normal proxy path still returned HTTP 502. Proxy logs showed it dialing the
model Service at `10.43.134.64:8080`, while the Service and EndpointSlice were
correctly exposing `qwen3-1p7b-tools-radeonvii` on port `8000` with a Ready
endpoint. Root cause: proxy fallback routing derived the Service URL port from
the backend default (`llamacpp` = `8080`) instead of the reconciled Service's
actual port. Active fix: make `getBackendPort` prefer the model Service port,
using the `http` port when present and falling back to the first valid Service
port before backend defaults.

MR !403 shipped that proxy fix. Branch and MR pipelines passed, master pipeline
9994 published the updated chart and proxy image, and Helm rolled
`flexinfer-proxy` to chart `1.0.2+0d0fc833aaa1.3` with proxy image digest
`sha256:3a650e2b6613be373a81b6d1bb385cc3884eeb2a57e4b38112d598c0527ebb95`.
Harbor briefly refused image pulls while the VM restarted, so the first restart
kept the old proxy pod serving; once Harbor recovered, deleting the failed new
pod retried the pull and the deployment completed.

Final normal-path smoke through `flexinfer-proxy` succeeded:

```text
POST http://flexinfer-proxy.flexinfer-system.svc.cluster.local/model/qwen3-1p7b-tools-radeonvii/v1/chat/completions
{"model":"qwen3-1.7b-tools","messages":[{"role":"user","content":"Reply with exactly one word: blue"}],"max_tokens":8,"temperature":0}
HTTP 200
content: Blue
model: qwen3-1.7b-q4_k_m.gguf
prompt_per_second: 119.15
predicted_per_second: 69.99
```

### 2026-04-18 gemma4-26b-a4b-gptq findings (Slice A1-lite)

- Active serving artifact:
  `/models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` (confirmed from
  vLLM cmdline `--model ...` on pod
  `gemma4-26b-a4b-gptq-87c45466d-wpkg6`).
- Serving args: `--quantization gptq --attention-backend TRITON_ATTN
  --enforce-eager --max-num-seqs 1 --gpu-memory-utilization 0.95
  --max-model-len 8192`.
- Clean variant: 30 layers x 2 MoE module families quantized
  (`moe.down_proj`, `moe.gate_up_proj`); 30
  `attention-fp16-layer-NN.safetensors` shards hold the FP16 attention weights
  alongside 4 `model-X-of-00004.safetensors` base shards; total 777 tensors,
  34 shard files.
- Hybrid-v10 variant (on-PVC but not served): fully quantized layout (MoE +
  MLP + attention q/k/v/o) with `self_attn.v_proj` only present on 25/30 layers.
  This is consistent with `attention_k_eq_v: true` in the config, but it must be
  confirmed before promoting this variant.
- Validator follow-ups:
  1. Family auto-detection gap: `detected_family: null` for both variants.
     `FAMILY_PROFILES` in `build/scripts/validate_quantized_artifact.py`
     matches tensor-name hints; add a `model_type: gemma4_text` or architecture
     signal so the CLI works without a forced `--family` flag.
  2. Flat `modules_in_block_to_quantize` warning is always-on for vLLM-serving
     artifacts. Either silence it for that layout or make it informational-only.
- No re-quant or cosine gate ran. `denseModulePolicy: validate` remains
  commented out in `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml`.

Raw outputs:
`.loom/local/validation/gemma4-26b-a4b-gptq/20260418-085841/{clean.json,clean.txt,hybrid-v10.json}`
(gitignored).

### 2026-05-05 qwen36-27b-gptq smoke findings

- Artifact pipeline completed: ModelCache `qwen36-27b-gptq-gfx1100`
  abliterated 3 layers in `1h53m20s`, quantized GPTQ `W4_G128` in `1h19m30s`,
  and published
  `registry.harbor.lan/flexinfer/qwen36-27b:gptq-w4-g128-gfx1100@sha256:fe3a6bea0cd2cdf254a5db6194e01402f1f7f93c4b86d8c717695470fdd3849d`.
- Replacement Model `qwen36-27b-gptq` activated on `cblevins-5930k`. Initial
  proxy activation returned 503 because `LastActiveTime` status updates hit a
  conflict after scale-up had already started.
- Cold activation reached the pod quickly, but kubelet spent `12m33s` pulling
  the 17.6GB ROCm vLLM image. Cache flash from hostPath to `/dev/shm` took
  about 9 seconds.
- Runtime config `kvCacheDtype: fp8_e4m3` failed during vLLM KV warm-up:
  Triton reported `type fp8e4nv not supported in this architecture`. Live
  canary with `kvCacheDtype: auto`, `calculateKvScales: false`, and
  `maxNumSeqs: 2` reached Ready.
- `gptq_marlin` was tested as a coherence fix but vLLM rejected it because the
  model config declares quantization method `gptq`.
- Direct FlexInfer proxy and direct service requests returned HTTP 200, but
  output was incoherent: exact-answer prompts produced repeated exclamation
  marks and multilingual junk. Treat this as a model artifact/runtime blocker,
  not a routing success.
- Follow-up direct safetensor check on `cblevins-5930k` mounted
  `qwen36-27b-gptq-gfx1100` and dequantized representative GPTQ attention
  tensors against the post-abliteration FP16 parent. Layers 11 and 15
  `q/k/v/o` had no NaNs/Infs, sane weight stats, cosine about `0.99`, and
  relative L2 about `0.13-0.16`, so the cache is not broadly corrupt.
- Next runtime fix: Qwen3.5-patched vLLM must use the ROCm GPTQ reference
  fallback already proven necessary for Gemma4. The Qwen patch stack now adds a
  `GPTQLinearMethod.apply` ROCm/4-bit slow path so the next rebuilt runtime can
  test coherence without the fused `gptq_gemm` kernel.

### 2026-05-06 qwen36-27b-gptq quality recheck

- Runtime image was quiet and digest-pinned:
  `registry.harbor.lan/flexinfer/vllm@sha256:cb6d92c956ee150b4b8210e625586140e1b5da4c204caa422b1965e953de78e8`.
- Greedy chat smoke through `flexinfer-proxy`:
  `{"model":"qwen36-27b","messages":[{"role":"user","content":"Answer with exactly one word: blue"}],"max_tokens":24,"temperature":0,"top_p":1}`
  returned HTTP 200 with `!!!!!!!!!!!!!!!!!!!!!!!!`.
- Direct `/v1/completions` smoke against `qwen36-27b-gptq:8000` for prompt
  `The color of the sky is` returned `!!!!!!!!!!!!!!!!` with identical
  top-logprobs for punctuation tokens (`!`, `"`, `#`, `$`, `%`) at each
  generated position (`-12.422473907470703`), indicating a flat/collapsed
  logits distribution rather than a sampling or chat-template problem.
- Pod logs confirmed the quiet patch applied the ROCm GPTQ reference fallback,
  naive FLA kernels, RMSNorm native path, and direct `gdn_attention_core`
  bypass, so the remaining blocker is deeper artifact/runtime math or
  quantized-weight interpretation.
- Serving posture: keep `qwen36-27b-gptq` as a direct canary only. Do not expose
  replacement labels such as `qwen3-coder` or `qwen3-30b-a3b` until a coherent
  deterministic smoke passes.
- 2026-05-07 operator traffic confirmed the failure is user-visible, not just a
  synthetic prompt problem: the `qwen36-27b` profile returned mixed token soup
  beginning `-current Lockheedпуст...`. The model was manually scaled to zero
  and removed from reconciled `deploy/models/kustomization.yaml` because it is
  both incoherent and slow on the `5930k-imagegen-textgen` shared lane.
- 2026-05-07 fast-chat recovery posture: remove `qwen36-27b-gptq`,
  `gemma4-31b-gptq`, and `gemma4-26b-a4b-gptq-long` from the reconciled model
  set so slow or incoherent canaries stop owning user-facing aliases. Promote
  `qwen3-8b-fast-7900xtx` as the warm `fast-chat` / `gpt-3.5-turbo` MLC route
  on `7900xtx-textgen`. A 5930k fallback was attempted but removed from the
  reconciled set: the node-local MLC PVC lacks the model directory, and the
  shared NFS copy lacks `mlc-chat-config.json`, so MLC exits before serving.
  The attempted `qwen3-14b-abliterated-v2-5930k` fallback stayed disabled
  because its GPTQ source PVC is absent in the live cluster.
- 2026-05-06 Track H static triage (`.loom/local/qwen36-coherence-triage.md`):
  ranked three hypotheses against the published `model.safetensors.index.json`
  and live ModelCache spec. Most likely: GDN linear-attention sub-modules
  (`in_proj_qkvz`, `in_proj_ba`, `conv1d`) were int4-quantized because
  `spec.quantization.dynamicExclusion: "none"` on `qwen36-27b-gptq-gfx1100`.
  Earlier dequant cosine sanity at layers 11/15 only covered q/k/v/o
  (full-attention modules), so GDN weight quality was never measured. Section
  16 fixup in `build/scripts/vllm_qwen35_patches.py` only reverts to
  `nn.Linear` when `.qweight` is missing from the index, so degraded but
  present GDN qweights bypass the safety net. Confirming experiment: dump
  `model.safetensors.index.json` from PVC `qwen36-27b-oci`, grep for
  `model.layers.0.linear_attn.in_proj_ba.qweight`; if present, dequant vs FP16
  parent and check cosine threshold 0.98. Re-quant fix is one line at
  `deploy/modelcaches/qwen36-27b-gptq-gfx1100.yaml:87`:
  `dynamicExclusion: "gdn"`. Hypothesis 1 (`text_config`/vocab corruption) and
  hypothesis 3 (lm_head abliteration) eliminated: published config has
  `model_type=qwen3_5_text` + `vocab_size=248320` + `tie_word_embeddings=false`,
  and ModelCache spec has `ablitateLmHead=false` with `refusalDirNorm=41`
  (under the 100 abort threshold).

### 2026-05-06 qwen36-27b-gptq Track D-1 root cause confirmed

- PVC `qwen36-27b-oci` was inspected directly on `cblevins-5930k` via a
  busybox debug pod mounting `/models/qwen36-27b/` (the published GPTQ
  artifact at digest `sha256:fe3a6bea...`).
- `model.safetensors.index.json` contains `.qweight` tensors for all 48
  GDN linear-attention layers. Three modules per layer were quantized:
  `linear_attn.in_proj_qkv.qweight`, `linear_attn.in_proj_z.qweight`, and
  `linear_attn.out_proj.qweight`. Counts: 48 each (one per GDN layer).
- `linear_attn.conv1d` kept `.weight` (1D conv, not a `nn.Linear`, so
  GPTQ skipped it as expected).
- `quant_log.csv` confirms layer 0 (a GDN layer per the `layer_types`
  schedule) recorded GPTQ losses for `linear_attn.in_proj_qkv` (loss
  0.00524), `linear_attn.in_proj_z` (loss 0.00343), and
  `linear_attn.out_proj` (loss ~3.9e-6).
- Earlier dequant cosine sanity (2026-05-05) only covered q/k/v/o on
  layers 11/15, both *full*-attention layers. The GDN sub-modules were
  never measured; their weight quality is unknown by this experiment but
  the quantization-then-GDN-runtime path is architecturally wrong (GDN
  GatedDeltaNet expects FP weights for in_proj_qkv/in_proj_z/out_proj).
- Module names differ from Track H's hypothesized
  `in_proj_qkvz`/`in_proj_ba`: this artifact uses the defused
  `in_proj_qkv`/`in_proj_z` split. The fix still applies — switch
  `dynamicExclusion` from `none` to `gdn` so GPTQModel skips
  `linear_attn.*` patterns on the next quantization run.
- ModelCache CRD updated in MR (Track D-1) to set
  `quantization.dynamicExclusion: "gdn"`. Re-quant has not been run yet;
  serve coherence smoke and dequant cosine on a non-GDN layer remain
  required before the matrix row flips.
- 2026-05-09 DEBT-302 added a reusable artifact validator policy for
  `family=qwen36-27b`: `linear_attn.*` qweight tensors are reported in
  `checks.gdn_gptq_policy.quantized_gdn_modules` and emitted as warnings.
  The qwen36 ModelCache publish gate starts with `failOnWarnings=false` so
  the recovery cycle records evidence without unexpectedly blocking publish.

### 2026-05-06 sdxl-inpainting-radeonvii runtime smoke

- Model `sdxl-inpainting-radeonvii` was Ready through the direct runtime path:
  `phase=Ready`, Ready reason `RuntimeReady`, message `Model ready via runtime`.
- Runtime pod `flexinfer-runtime-gfx906-dh8st` ran digest-pinned image
  `registry.harbor.lan/flexinfer/runtime@sha256:7c05960614517dbd5d6453944125a01e78f0451f6695467a8eaf6a6859d461dd`.
- Runtime load selected local model path
  `/models/flexinfer-system/sdxl-inpainting-radeonvii`,
  `StableDiffusionXLInpaintPipeline`, dtype `float32`, fixed VAE, CPU offload,
  and attention slicing. Warmup completed in 60.7s.
- The initial request to `/v1/images/generations` returned HTTP 500 because an
  SDXL inpaint pipeline requires an input image and mask. The runtime remained
  healthy and the failure was not a GPU crash.
- Correct multipart smoke through `flexinfer-proxy`:
  `/model/sdxl-inpainting-radeonvii/v1/images/edits` with 512x512 PNG image
  and mask returned HTTP 200 in 48.35s, one image, `b64_len=24152`.
- Runtime logs recorded 22 denoise steps in 40s and
  `POST /v1/images/edits HTTP/1.1` 200 OK. Decoded response artifact:
  `/private/tmp/sdxl-radeonvii-edits-output.png`, PNG, 1024x1024 RGB.
- Promotion posture: conditional pass for the gfx906 runtime lane. Keep the row
  conditional because this canary depends on CPU offload and uses the image-edit
  endpoint only; text/image-generation endpoint parity is not implied.

### 2026-05-07 gfx906 runtime digest promotion

- Built and pushed `registry.harbor.lan/flexinfer/runtime:rocm-gfx906` from
  `master@d8c75658`, producing digest
  `sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97`.
- Smoke checked the image entrypoint with:
  `docker --context 7900xtx run --rm --entrypoint /usr/local/bin/flexinfer-runtime registry.harbor.lan/flexinfer/runtime:rocm-gfx906 --help`.
  The binary reported default `-gpu-vendor amd` and `-gpu-arch gfx906`.
- Promoted the digest with
  `scripts/promote-runtime-digest.sh gfx906 --digest sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97 --apply`.
- Promotion corrected existing drift: `deploy/gpuprofiles/gfx906.yaml` had
  `sha256:ba4570f5...`, while `deploy/system/values-k3s.yaml` had
  `sha256:7c059606...`; both now point at `sha256:dd0a1936...`.
- Validation before merge: `scripts/check-runtime-profile-consistency.sh`,
  `scripts/test-promote-runtime-digest.sh`, `git diff --check`, and targeted
  runtime image digest resolution with `crane digest`.
- Follow-up during fast-chat recovery: applying the digest-pinned gfx906 runtime
  to `cblevins-radeonvii` repeatedly filled root-backed containerd storage to
  100% and triggered kubelet `DiskPressure` evictions. The live DaemonSet was
  paused and `deploy/system/values-k3s.yaml` now mirrors that pause with
  `flexinfer.ai/runtime-paused=true` on the gfx906 runtime profile until the
  image/storage issue is fixed.
- To keep Radeon VII useful without repeating the pull failure, the next
  reconciled workload is `qwen3-1p7b-tools-radeonvii`: llama.cpp GGUF,
  `HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF` /
  `qwen3-1.7b-q4_k_m.gguf`, `tool-router` aliases only, and the much smaller
  `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` runtime path.
  The live Kubernetes object avoids dots in the name so generated Services pass
  DNS-1035 validation. The original upstream `Qwen/Qwen3-1.7B-GGUF` source was
  not a valid Q4_K_M cache source for this manifest; the prefetcher matched
  zero files before this correction. Chat serving also disables Qwen3 thinking
  output with `reasoningFormat: none` and `reasoningBudget: 0` so LiteLLM
  aliases behave like low-latency utility routes instead of returning hidden
  reasoning markers.

### 2026-05-07 gfx906 slim runtime promotion + cold-start canary (Track B-3)

- Round 2 of the next-round parallel plan closed the gfx906 disk-pressure block
  end-to-end. The deployed runtime digest `dd0a1936...` (built from
  `Dockerfile.runtime` for the gfx906 profile, 59.2 GB extracted) repeatedly
  drove `cblevins-radeonvii` root LVM (127 GB) to 100% on pull and triggered
  kubelet `DiskPressure` evictions. Track B-1 (drain + bind-mount containerd
  to NVMe LVM) was abandoned because the node hosts 194 pods including
  Prometheus/Loki/Tempo/Langfuse-Clickhouse and 11 StatefulSets — drain blast
  radius too broad for an unscheduled maintenance window.
- Track B-3 cycle 1 (MR !280) slimmed `Dockerfile.unified-gfx906` from
  33.1 → 32.8 GB, but that is the batch quantization image (entrypoint
  `/bin/bash`, no `flexinfer-runtime` Go binary) — not the runtime DaemonSet
  image that is actually deployed. Real win came from cycle 2 (MR !281):
  introduced a per-profile `dockerfile:` override in `build/runtime.yaml` and
  `build/build-runtime.sh`, added `build/Dockerfile.runtime-gfx906` mirroring
  the multi-stage pattern (go-builder, llamacpp-builder, ollama-builder) with
  the runtime stage on `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3`. Combined
  with cycle 1 techniques (≤5 RUN layers, `__pycache__`/`.py[co]` strip,
  `pip cache purge`, `apt-get clean`, `setuptools<78` for bnb 0.49.2),
  pushed digest `sha256:94045d0ca4b12deb3c46bb22070f67bfedad8b719bb992e5d3ce128ad27ad597`
  at 36.9 GB extracted — a 38% reduction.
- First pre-pull on radeonvii failed at root 100% / `available: 0`. The
  36.9 GB final number was correct but pull-time peak (compressed content
  tarballs + extracting layers concurrently) was ~1.5x final ≈ 55 GB, which
  exceeded the 55 GB free root we had. Containerd auto-cleaned the partial
  extraction on failure; root recovered to 47%. SDXL inpaint cache-stage Job
  pod was evicted (controller-managed, no data loss).
- Discovered 21 GB of model weights at `/var/lib/flexinfer/models/flexinfer-system/sdxl-inpainting-radeonvii`
  on root LVM. Bind-mounted that path to `/mnt/nvme/longhorn/flexinfer/models`
  (fstab entry, no k3s restart, no drain). 22 GB rsynced in 2m17s; `diff -r`
  confirmed integrity; `.old` reclaimed. Root went 57 → 36 GB used / 85 GB
  free.
- Re-pull succeeded in 8m48s. Post-pull root: 78 GB used / 44 GB free / 65%.
  No DiskPressure transition.
- Promotion (MR !282): `scripts/promote-runtime-digest.sh gfx906 --digest sha256:94045d0c... --apply`
  updated `deploy/gpuprofiles/gfx906.yaml` and `deploy/system/values-k3s.yaml`,
  and dropped the `flexinfer.ai/runtime-paused: "true"` annotation on the
  gfx906 nodeSelector. `scripts/check-runtime-profile-consistency.sh` passed.
- Flux reconciled to `master@sha1:5aae1f34`. DaemonSet `flexinfer-runtime-gfx906`
  came up as `flexinfer-runtime-gfx906-ff4p6` (1/1 Running) within ~30s of
  reconcile; logs confirm
  `runtimeProfile=gfx906`, `runtimeDigest=sha256:94045d0c...`,
  `backends=[ollama, steam, vllm, vllm-omni, comfyui, diffusers, llamacpp, mlc-llm]`.
  Non-fatal entrypoint warning:
  `vllm_gemma4_moe_gptq_patch.py: Cannot find vLLM installation` — expected
  because vLLM is intentionally `false` for the gfx906 profile (memory note);
  follow-up to suppress.
- Cold-start canary: `Model/sdxl-inpainting-radeonvii` was `Idle` (serverless
  pattern — no Deployment exists when idle). Multipart
  `POST /model/sdxl-inpainting-radeonvii/v1/images/edits` through
  `flexinfer-proxy` (port-forward to `flexinfer-system/flexinfer-proxy:80`)
  with 512x512 PNG image+mask + prompt
  `"a vibrant orange flower with green leaves, photorealistic"` returned
  **HTTP 200 in 107.7s** with one PNG result, `b64_len=252372`. Cold-start
  latency includes deployment scale-up + pod start + weights load from the
  freshly bind-mounted NVMe path + GPU warmup. Compared to the 2026-05-06
  warm canary (HTTP 200 in 48.35s, `b64_len=24152`), the +60s is consistent
  with cold-start overhead and the bigger b64 is a more visually complex
  generation, not an error.
- Post-canary disk: root 71 GB used / 51 GB free / 59%; NVMe LVM 338 GB used
  / 409 GB free / 46%.
- Round-2 net: gfx906 lane unblocked, runtime digest pinned by digest, model
  state on NVMe instead of root, dynamic `runtime_profile`/`runtime_digest`
  metric labels now populated for the radeonvii lane, MR !282 merged.

### 2026-04-26 gemma4 26B/31B execution findings

- Live Flux truth before hot validation: `flexinfer-system` and
  `flexinfer-models` were Ready at
  `master@sha1:50cf1d977d502357df1c5c6b998c05b1dc05f429`; !193 and !194 were
  already merged.
- `gemma4-31b-gptq` was Ready/Active with `minReplicas: 1`, `priority: 250`,
  `gpu.count: 2`, `warmPolicy: primary`, and `maxModelLen: 2048`. The direct
  smoke through a port-forward returned HTTP 200 with answer `4` in 0.158s.
- After the long-canary hot test, 31B was restored to Ready/Running and a second
  direct smoke returned HTTP 200 with answer `4` in 0.304s.
- `gemma4-26b-a4b-gptq-long` has the safe dGPU selector, cache Ready,
  `minReplicas: 0`, and `maxModelLen: 32768`, but the fp16-KV long-context
  canary failed at engine initialization. vLLM loaded the weights successfully
  (`17.74 GiB`, `56.69s`) but reported only `1.87 GiB` available for KV while
  `32768` tokens required `6.88 GiB`; the logged estimated maximum model length
  was `8896`. This blocks both 16K and 32K promotion on the current
  hybrid/fp16-KV lane.
- The 26B dense-validated cache did not reach the cosine gate. Its latest retry
  reached only harmful prompt `80/128` before the 4h abliteration deadline; the
  checkpoint remained in `stage: harmful_activations`. Because `abliterate.py`
  resumes only completed activation payloads, each retry restarts the partial
  harmful pass. The manifest now raises abliteration and quantization deadlines
  to 24h so the next Flux-managed rebuild can reach dense cosine validation.
- TurboQuant primitive sharing is implemented behind `TQ4_SHARE_PRIMITIVES=1`
  and the patcher was verified idempotent against upstream `turboquant-vllm`
  commit `9d19b87cef462cf0abd5643f6d052ac5a3bc99b6`. Runtime canaries still
  require a rebuilt image carrying the patched profile.

## Schema Change Log

Track schema rotations of this matrix here. Each entry records the date,
the columns added or retired, and the rationale so a reader can audit how
older rows mapped to newer columns.

### 2026-05-06: rotate matrix into canonical runtime-promotion shape (Track E)

- Promoted this file from a gfx1100-only quantization tracker to the canonical
  canary and runtime-promotion table for `gfx1100` and `gfx906`.
- Added explicit audit columns to the Validation Contract and the Promotion
  Matrix header: `backend`, `support_level`, `canary_command`,
  `rollback_digest`.
- Did not duplicate timing/throughput fields (`smoke.ready_minutes`,
  `smoke.cold_load_min`, `smoke.decode_tps`, `smoke.prompt_tps`, image
  generation seconds) into table columns; the Runtime Smoke section under
  Field Capture Reference remains the canonical source.
- Backfilled the four required canary lanes called out in the Track E spec:
  `gfx1100` textgen (`qwen36-27b-gptq`), `gfx1100` imagegen
  (`gonzalomo-fluxpony-imagegen` FLUX Schnell), `gfx906` textgen/quantization
  (Qwen3.5 GPTQ runbook lane, currently paused under DiskPressure), and
  `gfx906` imagegen/offload (`sdxl-inpainting-radeonvii`).
- Cells without captured evidence are written as `TBD: <reason>` so the
  promotion rules can keep treating them as `pending` instead of fabricating
  a `promote`.
- Source plan: Track E in the gfx1100/gfx906 next-round plan, picking up
  Slice 6 of `.loom/gfx1100-gfx906-platform-enhancements-plan.md`.

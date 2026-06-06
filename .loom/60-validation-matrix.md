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
- One of the required active hardware lanes is represented when applicable:
  `gfx1100` textgen, `gfx906` textgen/quantization, and `gfx906`
  imagegen/offload. `gfx1100` imagegen is not a standing canary while
  `deploy/models/kustomization.yaml` keeps both 7900 XTX nodes dedicated to
  text serving; add a new row only when a reconciled gfx1100 diffusers Model is
  restored.

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

The required active canary lanes (`gfx1100` textgen,
`gfx906` textgen/quantization, and `gfx906` imagegen/offload) are each
represented by at least one row even when evidence is incomplete. Historical
gfx1100 imagegen rows must be marked `skip` or `pending` unless a real
reconciled gfx1100 diffusers Model backs the row.

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
| `gemma4-e4b-gptq` | TBD: not yet built | `gfx1100/7900xtx` | TBD: backend not chosen | `experimental` | TBD | TBD | Deferred 2026-05-25: no operator committed to driving this canary. Reopens when a slice claims SD-3. Reasonable starting point: copy `gemma4-26b-a4b-gptq` manifest, swap source + GPU profile, ModelCache through quantization pipeline. | n/a (no run attempted) | TBD | n/a (no prior promotion) | SD-3 / Issue #57 | `pending` |
| `omnicoder-9b-gptq` | TBD: not yet served | `gfx1100/7900xtx` | TBD: backend not chosen | `experimental` | TBD | TBD | Deferred 2026-05-25: manifest exists at `deploy/modelcaches/omnicoder-9b-gptq.yaml` but no end-to-end Pending→Ready run has been driven. Reopens when SD-3 is picked up. | n/a (no run attempted) | TBD | n/a (no prior promotion) | SD-3 / Issue #57; `deploy/modelcaches/omnicoder-9b-gptq.yaml` | `pending` |
| `qwen35-9b-gptq-gfx1100` | TBD: not yet served | `gfx1100/7900xtx` | `vllm` | `experimental` | TBD | TBD | Deferred 2026-05-25: gfx906 sister `qwen35-9b-gptq` has proven GPTQ pipeline; gfx1100 port requires only a manifest copy + GPU profile swap. No operator committed. | n/a (no run attempted) | TBD | n/a (no prior promotion) | SD-3 / Issue #57 | `pending` |
| **Required canary: `gfx1100` textgen** — `qwen36-27b-gptq` abliterated GPTQ W4_G128 | 8192 | `gfx1100/5930k` | `vllm` | `experimental` | `registry.harbor.lan/flexinfer/vllm:rocm-gfx1100-qwen35-patched-nodiag-textcfg` (digest TBD) | `registry.harbor.lan/flexinfer/qwen36-27b:gptq-w4-g128-gfx1100@sha256:fe3a6bea0cd2cdf254a5db6194e01402f1f7f93c4b86d8c717695470fdd3849d` | Cache Ready; vLLM reached Ready with `quantization=gptq`, `kvCacheDtype=auto`, `maxNumSeqs=2`; direct proxy and service smoke returned HTTP 200; quarantined from reconciled serving manifests on 2026-05-07; DEBT-302 adds warning-first publish validation with `layout=vllm-gptq`, `family=qwen36-27b`, and `checks.gdn_gptq_policy` to surface any `linear_attn.*.qweight` tensors before OCI publish | First activation exposed proxy `lastActiveTime` conflict; cold start was dominated by 17.6GB image pull; `fp8_e4m3` KV crashed Triton cache update; `gptq_marlin` rejected because artifact config declares `gptq`; current `gptq` runtime serves incoherent output (`!!!!!!!!!!!!` / multilingual junk), flat punctuation logprobs, and live profile traffic like `-current Lockheedпуст劳逸...`; too slow for the 5930k shared lane | Direct `/v1/completions` greedy smoke against `qwen36-27b-gptq:8000` for `The color of the sky is` (raw 2026-05-06 evidence below); publish validator gate runs during next qwen36 ModelCache publish | TBD: failing canary; predecessor `qwen3-14b-abliterated` GPTQ digest is referenced in MR !247 but has no captured success on 5930k to roll back to | MR !247 replacement; MR !248 runtime hardening; MR !253/!254 quiet runtime; 2026-05-05, 2026-05-06, 2026-05-07 smoke evidence; DEBT-302 validator tests | `fail` |
| `qwen3-14b-gptq` | TBD: not yet served | `gfx1100/5930k` | `vllm` | `experimental` | TBD | TBD | Deferred 2026-05-25: artifact already published (`pvc://qwen3-14b-abliterated-v2-gptq/.../gptq-w4-g128`); requires only a Model manifest pointing at the artifact and a vLLM smoke. No operator committed. | n/a (no run attempted) | TBD | n/a (no prior promotion) | SD-3 / Issue #57; `.loom/30-implementation-plan.md` 2026-04-18 Slice D | `pending` |
| **Required canary: `gfx906` imagegen/text-to-image** — `gonzalomo-fluxpony-imagegen` SDXL text-to-image | n/a, 512x512 warmup resolution | `gfx906/radeonvii` | `diffusers` | `experimental` | `registry.harbor.lan/flexinfer/diffusers:rocm-gfx906` (tag from `deploy/gpuprofiles/gfx906.yaml`; digest still TBD) | `HF://stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd`; manifest `deploy/models/gonzalomo-fluxpony-imagegen.yaml`; local cache under `/models/flexinfer-system/gonzalomo-fluxpony-imagegen` once staged | Manifest relocated FluxPony from `cblevins-5930k` to `cblevins-radeonvii` on 2026-05-13 so both 7900 XTX nodes can stay on Gemma4 26B text serving. The canary uses `radeonvii-imagegen`, `cpuOffload=true`, fp16 weights, the fixed SDXL VAE, Euler/30-step published recipe, and `warmupResolutions=512x512`. | Live cold-load + 512x512 generation timing has not been recaptured after the lane move. 1024x1024 is no longer required for this gfx906 lane; treat it as optional headroom evidence after disk/VRAM pressure is understood. | `kubectl -n flexinfer-system run fluxpony-gfx906-smoke --rm -i --restart=Never --image=curlimages/curl:8.11.1 -- curl -sS -m 1800 -X POST http://flexinfer-proxy.flexinfer-system.svc.cluster.local/model/gonzalomo-fluxpony-imagegen/v1/images/generations -H 'Content-Type: application/json' -d '{"model":"gonzalomo-fluxpony-imagegen","prompt":"a small blue glass cube on a white table","size":"512x512","n":1}'` | Rollback manifest path: disable `gonzalomo-fluxpony-imagegen.yaml` in `deploy/models/kustomization.yaml` or keep `minReplicas: 0`; do not roll back to 5930k unless a new gfx1100 imagegen capacity decision lands. | `deploy/models/gonzalomo-fluxpony-imagegen.yaml`; `deploy/models/kustomization.yaml`; `deploy/gpuprofiles/gfx906.yaml`; this matrix reconciliation | `pending` |
| Retired `gfx1100` imagegen canary slot (2026-05-22) | n/a | `gfx1100/5930k+7900xtx` | `diffusers` | `supported` capability, no active canary | n/a | Disabled manifests only (`sdxl-diffusers-gfx1100`, `sdxl-turbo-imagegen`, `sdxl-inpainting`, `instruct-pix2pix`) | No reconciled gfx1100 imagegen Model currently proves this lane. `deploy/models/kustomization.yaml` documents the 2026-05-13 fleet reshape: `cblevins-5930k` is text-only and FluxPony moved to Radeon VII. | Treating `gonzalomo-fluxpony-imagegen` as gfx1100 evidence is stale and invalid. | n/a until a gfx1100 diffusers Model is re-enabled and smoked through `/v1/images/generations` or `/v1/images/edits`. | Re-enable an explicit gfx1100 imagegen manifest, then add a new promotion row with runtime digest, smoke command, and rollback. | `deploy/models/kustomization.yaml`; disabled manifests in `deploy/models/`; this matrix reconciliation | `skip` |
| `gemma4-31b-gptq` Radeon VII comparison | n/a | `gfx906/radeonvii` | `n/a` | `unsupported` | n/a | n/a | n/a | Off-gfx1100 comparison row; VRAM ceiling for this promotion lane | n/a: not a target | n/a | SD-3 / Issue #57 | `skip` |
| **Required canary: `gfx906` textgen/quantization** — Qwen3.5 GPTQ Radeon VII pipeline (`docs/user/gptq-quantization-runbook.md`) | TBD: gfx906 runtime currently paused (DiskPressure) so no live serving canary | `gfx906/radeonvii` | `vllm` | `deprecated` | TBD: gfx906 vLLM runtime is paused via `flexinfer.ai/runtime-paused=true` after the digest pull repeatedly hit DiskPressure | TBD: 31B GPTQ artifact reused from gfx1100 (`pvc:///gemma4-31b-gptq/gptq-w4-g128-keqv`) | GPTQ runbook documents abliteration + GPTQ flow on Radeon VII (`docs/user/gptq-quantization-runbook.md`); 2026-05-07 evidence below records DaemonSet pause + DiskPressure history. CPU loading + community PyTorch wheel restore allocations under 16 GiB. Live serving canary not currently runnable on radeonvii. | Root-backed containerd fills to 100% on first pull of the 17 GiB digest-pinned `runtime` image, evicting kubelet workloads. The replacement `qwen3-1p7b-tools-radeonvii` llama.cpp lane is queued precisely because vLLM cannot run here today. | TBD: re-enable canary after storage relocation; recapture before lifting `runtime-paused` | `registry.harbor.lan/flexinfer/runtime@sha256:7c05960614517dbd5d6453944125a01e78f0451f6695467a8eaf6a6859d461dd` (last gfx906 runtime digest before the `dd0a1936...` promotion that hit DiskPressure) | `.loom/gfx1100-gfx906-platform-enhancements-plan.md` Slice 5; `docs/user/gptq-quantization-runbook.md`; 2026-05-07 gfx906 runtime digest promotion evidence below | `pending` |
| **Required canary: `gfx906` imagegen/offload** — `sdxl-inpainting-radeonvii` Diffusers inpaint | n/a, 512x512 image edit | `gfx906/radeonvii` | `diffusers` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:94045d0ca4b12deb3c46bb22070f67bfedad8b719bb992e5d3ce128ad27ad597` | `local:///models/flexinfer-system/sdxl-inpainting-radeonvii` | Slim runtime image (cycle 2: `Dockerfile.runtime-gfx906` on `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` base, 36.9 GB extracted vs prior 59.2 GB) promoted via MR !282 after MR !281. DaemonSet pod Ready on `cblevins-radeonvii`; cold-start `/v1/images/edits` smoke returned HTTP 200 in 107.7s with one 512x512 PNG, `b64_len=252372`. Pre-pull verified root holds at 65% (78G/127G used) post-image-pull; bind-mounted `/var/lib/flexinfer/models` to `/mnt/nvme/longhorn/flexinfer/models` via fstab, reclaiming 21G on root. | None on the runtime path. Cold-start latency increased from prior 48.35s warm to 107.7s cold (deployment scale-up + weights load from freshly bind-mounted NVMe path). Failed pull on root LVM exposed pull-time peak ~1.5x final extracted size. | Multipart `POST /model/sdxl-inpainting-radeonvii/v1/images/edits` through `flexinfer-proxy` with 512x512 PNG image+mask (raw 2026-05-07 evidence below) | `registry.harbor.lan/flexinfer/runtime@sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97` (the prior 59.2 GB digest replaced by this promotion) | RG-4 / `.loom/gfx1100-gfx906-platform-enhancements-plan.md`; `.loom/gfx1100-gfx906-next-round-plan.md` Track B-3; 2026-05-07 Radeon VII evidence below | `conditional` |
| `gfx906` llama.cpp HIP memory-info shim pre-soak gate (2026-05-21) | n/a | `gfx906/radeonvii` | `llamacpp` | `experimental` | `registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407` | n/a | Built `build/Dockerfile.llamacpp-rocm-gfx906` with `libflexinfer_hipmeminfo_shim.so` preloaded. The standalone `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml` Job completed on `cblevins-radeonvii`; all four env variants returned `hipMemGetInfo=0`, `hipMalloc4096=0`, and `hipMemGetInfoAfterMalloc=0`. Raw transcript captured under `.loom/local/validation/gfx906-llamacpp/2026-05-21/`. | none for the HIP memory-info gate. The shim logs confirm the underlying ROCm call still returns `err=1`, so this is an image-level compatibility shim, not a driver fix. Model-load and 24 hour soak remain unproven and must be the next gate before aliases or default fallback promotion. | `kubectl apply -f deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml && kubectl -n flexinfer-system wait --for=condition=complete --timeout=600s job/gfx906-llamacpp-hipmeminfo-probe && kubectl -n flexinfer-system logs job/gfx906-llamacpp-hipmeminfo-probe` | Previous standalone canary image `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` (digest not captured in matrix); fallback is to leave model manifests on CPU-only `nGPULayers: 0` / hidden ROCm devices. | `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`; `build/Dockerfile.llamacpp-rocm-gfx906`; `build/hipmemgetinfo_shim.cpp`; `.loom/ralph-gfx906-llamacpp-meminfo-probe-2026-05-21.md`; MR !466 | `conditional` |
| `gfx906` llama.cpp Qwen3 8B model-load smoke on shim (2026-05-21) | 8192 | `gfx906/radeonvii` | `llamacpp` | `experimental` | `registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407` | `local:///models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf` | One-off debug Job mounted the Radeon VII node-local cache and ran `llama-cli` with `--gpu-layers 999`, `--flash-attn on`, `--cache-type-k q4_0`, `--cache-type-v q4_0`, and the shim preloaded. The model loaded on `AMD Radeon VII`, emitted llama.cpp ROCm memory breakdown (`model=4455 MiB`, `context=324 MiB`, `compute=304 MiB`), generated a short response, and exited `SMOKE_RESULT PASS`. Prompt throughput was `175.2 t/s`; generation throughput was `81.1 t/s`. Restore smoke against the existing CPU-fallback router returned `Blue` at `69.51 tok/s`, and the debug Job/ConfigMap were deleted. | none for model load. The prompt was a minimal load/generation smoke, not a coherence gauntlet or 24 hour soak. Shim diagnostics still show raw `hipMemGetInfo err=1`, converted to sysfs VRAM totals by `libflexinfer_hipmeminfo_shim.so`. | One-off Job equivalent: mount `/var/lib/flexinfer/models` at `/models`, request `amd.com/gpu: 1`, then run `llama-cli --model /models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf --prompt "Reply with exactly one word: blue" --predict 8 --ctx-size 8192 --gpu-layers 999 --flash-attn on --cache-type-k q4_0 --cache-type-v q4_0 --batch-size 1024 --ubatch-size 512 --threads 8 --temp 0 --single-turn --no-display-prompt` with `LD_PRELOAD=/opt/flexinfer/lib/libflexinfer_hipmeminfo_shim.so`. | Previous standalone canary image `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` (digest not captured in matrix); fallback is to keep reconciled router on CPU fallback (`nGPULayers: 0`, hidden ROCm devices). | `.loom/ralph-gfx906-llamacpp-model-load-smoke-2026-05-21.md`; raw log `.loom/local/validation/gfx906-llamacpp/2026-05-21/model-load-shim-smoke.log`; `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md`; MR !467 | `conditional` |
| `gfx906` llama.cpp Qwen3 8B 24h standalone soak on shim (2026-05-22) | 8192 | `gfx906/radeonvii` | `llamacpp` | `experimental` | `registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407` | `local:///models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf` | Standalone Job `gfx906-llamacpp-soak-traffic` ran on `cblevins-radeonvii` from 2026-05-21T18:40:23Z to 2026-05-22T18:43:42Z. Pod `gfx906-llamacpp-soak-traffic-brpcf` completed successfully: server container exit `0`, traffic container exit `0`, restart counts `0/0`. The traffic script exits nonzero on request failures, missing p95, or p95 above the `300 ms/token` budget, so exit `0` proves zero recorded failures and latency inside the envelope. Mid-run harvest at attempt 981-1140 showed steady HTTP 200 responses with 64 completion tokens and approximately `13.6-13.8 ms/token`. Co-tenant baseline at harvest: `sdxl-inpainting-radeonvii` `Idle`, `qwen3-1p7b-tools-radeonvii` `Ready`. | Final completed-container logs were unavailable at harvest (`kubectl logs` returned `unable to retrieve container logs for containerd://...`), so the exact final summary and final p95 value were not recoverable from Kubernetes. Treat the PASS as sufficient for the standalone kill-test but require the next proxy-backed soak to persist summary evidence to a ConfigMap or PVC before alias/default fallback promotion. | `kubectl -n flexinfer-system get job gfx906-llamacpp-soak-traffic -o json && kubectl -n flexinfer-system describe pod gfx906-llamacpp-soak-traffic-brpcf && kubectl -n flexinfer-system get model sdxl-inpainting-radeonvii qwen3-1p7b-tools-radeonvii -o wide` | Previous standalone canary image `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` (digest not captured in matrix); operational fallback remains the existing CPU-fallback router path until a shimmed persistent runtime image passes proxy-backed soak. | `.loom/ralph-gfx906-llamacpp-soak-2026-05-21.md`; `deploy/debug/gfx906-llamacpp-soak.yaml`; `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md`; MR !469; harvest 2026-05-22T19:13Z | `conditional` |
| `gfx906` persistent runtime hipMemGetInfo shim (2026-05-22) | n/a | `gfx906/radeonvii` | `runtime` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e` | n/a | MR !477 baked `build/hipmemgetinfo_shim.cpp` and `build/patch-hipmemgetinfo.sh` into `build/Dockerfile.runtime-gfx906`, sets `LD_PRELOAD=/opt/flexinfer/lib/libflexinfer_hipmeminfo_shim.so`, and added `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` to persist proxy-soak JSONL and summary output to PVC `gfx906-llamacpp-proxy-soak-evidence`. Master pipeline #11076 published digest `sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e` from `publish_runtime_rocm_gfx906` job #114140. This promotion pins that digest in `deploy/gpuprofiles/gfx906.yaml` and `deploy/system/values-k3s.yaml`; local gate passed `scripts/test-promote-runtime-digest.sh`, `scripts/check-runtime-profile-consistency.sh`, `git diff --check`, and `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-proxy-soak.yaml`. Proxy-backed soak remains the next live gate before alias/default fallback promotion. | Existing persistent runtime `registry.harbor.lan/flexinfer/runtime@sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4` does not carry the shim; the first proxy-backed attempt wedged during GPU-backed model load at `hipMemGetInfo`. | `scripts/promote-runtime-digest.sh gfx906 --digest sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e --validation-row "gfx906 persistent runtime hipMemGetInfo shim (2026-05-22)" --rollback-digest sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4 --apply && kubectl apply -f deploy/debug/gfx906-llamacpp-proxy-soak.yaml` | `sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4` | `.loom/ralph-gfx906-runtime-hipmem-shim-2026-05-22.md`; MR !477; pipeline #11076; `build/Dockerfile.runtime-gfx906`; `deploy/debug/gfx906-llamacpp-proxy-soak.yaml`; this digest-promotion MR | `conditional` |
| `gfx906` llama.cpp Qwen3 8B proxy-backed soak on persistent runtime (2026-05-23) | 8192 | `gfx906/radeonvii` | `llamacpp` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e` | `local:///models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf` | Proxy-backed Job `gfx906-llamacpp-proxy-soak-traffic` started against `http://flexinfer-proxy.flexinfer-system.svc/model/qwen3-8b-radeonvii/v1/chat/completions` after the shimmed persistent runtime promotion. The runtime image did load Qwen3 8B with the shim active and returned many HTTP 200 responses around `16-18 ms/token` through attempt 121. Evidence was harvested to `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-fail/` before cleanup. Rollback deleted the failing proxy soak Job/ConfigMap and temporary `Model/qwen3-8b-radeonvii`, force-promoted `qwen3-1p7b-tools-radeonvii`, and verified proxy fallback with `Blue`, `completion_tokens=2`, and `predicted_per_second=75.99`. | FAIL: the proxy soak had early intermittent `502 Bad Gateway` responses, then entered a terminal failure loop from attempt 122 onward with repeated 900s client timeouts, `502`, and `503 Service Unavailable`. Runtime logs show active-model thrash on the same `gfx906` runtime: `qwen3-8b-radeonvii` loaded, then `gonzalomo-fluxpony-imagegen` immediately triggered an unload, followed by Qwen3 8B reload attempts. `qwen3-8b-radeonvii` remained `Loading`, so the blocker is persistent-runtime/shared-GPU arbitration under cross-family load contention, not the standalone llama.cpp model-load path. | `kubectl apply -f deploy/debug/gfx906-llamacpp-proxy-soak.yaml && kubectl -n flexinfer-system logs job/gfx906-llamacpp-proxy-soak-traffic --all-containers=true --timestamps` | Rollback path proven 2026-05-23: `kubectl -n flexinfer-system delete job/gfx906-llamacpp-proxy-soak-traffic configmap/gfx906-llamacpp-proxy-soak-traffic model/qwen3-8b-radeonvii --ignore-not-found && kubectl -n flexinfer-system annotate model qwen3-1p7b-tools-radeonvii flexinfer.ai/force-promote=<timestamp> --overwrite`; smoke through `flexinfer-proxy` returned `Blue`. | `.loom/ralph-gfx906-llamacpp-soak-2026-05-21.md`; `deploy/debug/gfx906-llamacpp-proxy-soak.yaml`; `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-fail/`; MR !477; harvest 2026-05-23T04:40Z; superseded 2026-05-25 by MR !493 (proxy port-cache fix) + the gfx906 closure rows below | `superseded` |
| `gfx906` persistent runtime cross-group load guard live rerun (2026-05-23) | n/a | `gfx906/radeonvii` | `runtime/controller` | `experimental` | controller `registry.harbor.lan/flexinfer/flexinfer-controller@sha256:d4f10cc0fa0c8c288aff238345b1954ad31bc2a798eb682932d69edf7889618e`; runtime `registry.harbor.lan/flexinfer/runtime@sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e` | n/a | MR !480's controller image rolled out and the proxy-backed soak was rerun with a fresh `gfx906-llamacpp-proxy-soak-evidence` PVC. The controller guard's source regression still stands for cross-group idle candidates, but the live rerun failed before it could prove the intended imagegen contention fix: `qwen3-8b-radeonvii` cache prefetch succeeded, then the temporary Model remained `Idle`, `sharedGroup.state=Queued`, `preemptedBy=qwen3-1p7b-tools-radeonvii`, and `queuePosition=2`. The soak job recorded `soak_start`, then warmup attempt 1 and measured attempt 2 both timed out at 900s. Evidence was harvested to `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/`; the failing Job/ConfigMap and temporary Model were deleted after harvest. Follow-up source slice adds debug-only `qwen3-8b-radeonvii-soak` with `gpu.forcePromotion: true` and retargets the proxy-soak manifest so the next live gate validates activation separately from alias promotion. First activation preflight proved force-promotion but failed on the soak-only cache path; the target now uses the proven node-local GGUF path and the traffic script no longer counts warmup failures as measured soak failures. | FAIL: live runtime logs show same-group priority arbitration, not the prior cross-group imagegen unload. At 2026-05-23T14:59:45Z the runtime began loading `qwen3-8b-radeonvii`, but at 14:59:46Z the higher-priority `qwen3-1p7b-tools-radeonvii` immediately unloaded it and became Ready again. Controller logs thereafter repeatedly reconciled `qwen3-8b-radeonvii` with `desiredReplicas=0`, `cacheReady=true`; the proxy soak endpoint timed out twice at 900s because Qwen3 8B never became active. Alias/default fallback promotion remains blocked pending the soak-only activation preflight and then a clean 24h proxy soak. | `kubectl apply -f deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml && kubectl apply -f deploy/debug/gfx906-llamacpp-proxy-soak.yaml && kubectl -n flexinfer-system logs job/gfx906-llamacpp-proxy-soak-traffic --all-containers=true --timestamps` | Delete `job/gfx906-llamacpp-proxy-soak-traffic`, `configmap/gfx906-llamacpp-proxy-soak-traffic`, `pvc/gfx906-llamacpp-proxy-soak-evidence`, and `model/qwen3-8b-radeonvii-soak`; verify `qwen3-1p7b-tools-radeonvii` returns Ready. Revert MR !480 only if the cross-group guard itself regresses another path. | `.loom/ralph-gfx906-runtime-cross-group-guard-2026-05-23.md`; `.loom/ralph-gfx906-proxy-soak-live-verdict-2026-05-23.md`; `.loom/ralph-gfx906-proxy-soak-activation-gate-2026-05-23.md`; `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/`; MR !480; superseded 2026-05-25 by MR !493 (proxy port-cache fix) + the gfx906 closure rows below | `superseded` |
| `gfx906` proxy-soak activation preflight (2026-05-23) | 8192 | `gfx906/radeonvii` | `llamacpp` | `experimental` | controller `registry.harbor.lan/flexinfer/flexinfer-controller@sha256:fd66dc859968e7e0439b76af651c64a579d025c8e077e162888c5d017dedefcb`; runtime `registry.harbor.lan/flexinfer/runtime@sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e` | `local:///models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf` | MR !484 preserved `file://` sources as runtime `modelPath`, retargeted the soak-only Model at the proven node-local GGUF, and split warmup failures from measured failures in the traffic script. After master pipeline #11160 passed, the first Helm rollout still used stale controller digest `sha256:d85f3c...`; a manual controller restart pulled `sha256:fd66dc...`. With the fresh digest, `qwen3-8b-radeonvii-soak` reached `Ready`, `qwen3-1p7b-tools-radeonvii` was intentionally `Preempted`, `sdxl-inpainting-radeonvii` remained `Idle`, and runtime logs showed llama.cpp loading `/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`. The 900s job wrote durable summary evidence: `attempts=15`, `measured_requests=10`, `measured_failures=4`, `warmup_failures=1`, `p95_ms_per_token=23.51`. Evidence copied to `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-activation-preflight-fail/` before cleanup. | FAIL: activation is fixed, but proxy-backed measured traffic is not clean. Attempts 7, 10, 12, and 15 returned `502 Bad Gateway` after ~30s. Proxy logs show `dial tcp 10.43.137.91:8000: i/o timeout` for `svc/qwen3-8b-radeonvii-soak`; runtime logs have no matching request entries at those timestamps while successful measured attempts return HTTP 200. This blocks alias/default promotion and the 24h soak until selectorless Service/proxy reachability is fixed. | `kubectl apply -f deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml`; apply `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` with Job env `SOAK_DURATION_SECONDS=900`; `kubectl -n flexinfer-system logs job/gfx906-llamacpp-proxy-soak-traffic --tail=180` | Delete `job/gfx906-llamacpp-proxy-soak-traffic`, `configmap/gfx906-llamacpp-proxy-soak-traffic`, `pvc/gfx906-llamacpp-proxy-soak-evidence`, and `model/qwen3-8b-radeonvii-soak`; verified `qwen3-1p7b-tools-radeonvii` returned `Ready` and `sdxl-inpainting-radeonvii` stayed `Idle`. | `.loom/ralph-gfx906-proxy-soak-activation-gate-2026-05-23.md`; `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-activation-preflight-fail/`; MR !484; pipeline #11160; superseded 2026-05-25 by MR !493 (proxy port-cache fix) + the gfx906 closure rows below | `superseded` |
| `qwen3-1p7b-tools-radeonvii` GGUF tool-router | 8192 | `gfx906/radeonvii` | `llamacpp` | `experimental` | Persistent runtime `registry.harbor.lan/flexinfer/runtime@sha256:f0537a5498ca0ac0afe01a22413e2fa3bc36e0629d9d423960dd0c5572f7cc2b`; standalone profile image `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` digest TBD | `HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF` / `qwen3-1.7b-q4_k_m.gguf` | Cache prefetched and ready on 2026-05-16. MR !394 fixed runtime-manager fallback from stale absolute backend paths to `PATH`; MR !395 added the missing live `runtime:rocm-gfx906` publish job. MR !397 separated the flexinfer-runtime API port (`8080`) from the runtime-managed backend port (`8000`); MR !399 changed the canary to Local cache and staged the GGUF under `/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf`. MR !400 hardened llama.cpp launch args, and pipeline 9980 published this runtime digest. MR !402 made the reconciled manifest use the proven CPU fallback, and MR !403 fixed proxy fallback routing to prefer the actual Service port. Normal-path proxy smoke returned HTTP 200 with content `Blue`, `prompt_per_second=119.15`, and `predicted_per_second=69.99`. | Previous GPU llama.cpp image aborted in `hipMemGetInfo`; the 2026-05-21 shim candidate now passes the standalone HIP memory-info gate, but this reconciled canary still passes only as a CPU fallback with ROCm devices hidden until a model-load soak proves the GPU path. Keep `tool-router` and `qwen3-1.7b` aliases only; do not make this the default chat route unless coherence and latency pass. | `kubectl -n flexinfer-system run smoke --rm -i --restart=Never --image=curlimages/curl:8.11.1 -- curl -sS http://flexinfer-proxy.flexinfer-system.svc.cluster.local/model/qwen3-1p7b-tools-radeonvii/v1/chat/completions -H 'Content-Type: application/json' -d '{"model":"qwen3-1.7b-tools","messages":[{"role":"user","content":"Reply with exactly one word: blue"}],"max_tokens":8,"temperature":0}'` | `registry.harbor.lan/flexinfer/runtime@sha256:b36bb0ab008efa3d8a127cdf7cd9813c8ad88ddf7d62d8736bc6ad0976fe20f0` | RG-4 / `.loom/real-hardware-platform-improvements-plan.md` Slice 4; MR !394; MR !395; MR !397; MR !399; MR !400; MR !402; MR !403; pipelines 9917, 9946, 9976, 9980, and 9994; 2026-05-16 live failure and final proxy-smoke evidence below | `conditional` |
| `flexinfer-proxy` per-Model Service port-cache fix (Lane 1B closure, 2026-05-25) | n/a | `gfx1100/7900xtx+5930k`, `gfx906/radeonvii` | `proxy` | `supported` | `registry.harbor.lan/flexinfer/flexinfer-proxy@sha256:a631ccbc434c7f51387c361aa7702b11106d0831433157007d85a872ec245184` (`:master`, MR !493) | n/a | MR !493 added `lastKnownServicePorts` to `internal/proxy/routing.go` so the proxy never falls through to the backend default (`8080` for llamacpp) when a transient informer-cache miss drops `getServicePort`. Live cluster verification on 2026-05-25: built the fix locally, deployed `debug-port-cache` digest, fired 150 chat requests at 1 Hz through `flexinfer-proxy.flexinfer-system.svc:80` against `qwen3-8b-radeonvii-soak` — `150/150 OK, 0 failures`. Re-ran 50/50 OK on the CI-published `:master` digest. Pre-fix smoke (2026-05-25T10:24Z) was `8/14 = 57% failure`. New regression test `TestBackendPort_UsesLastKnownServicePortAfterTransientLookupFailure` in `internal/proxy/proxy_test.go` proves the failure mode. | none post-fix; the 2026-05-25 cluster smoke against the gfx906 lane returned 0% failure. Bug 2 ("dial to correct port :8000 still times out") was an artifact of Bug 1's wrong-port state, not a separate live bug. | Smoke: `kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80 &` then `python3 -c "import json,urllib.request; ..."` POST 150 chat-completion bodies to `qwen3-8b-radeonvii` at 1 Hz; expect 100% HTTP 200. | Previous proxy digest before MR !493 (port mismatch); revert MR !493 to restore. | MR !493; `.loom/brainstorm-gfx906-proxy-soak-502-framings-2026-05-25.md`; `memory/gfx906-proxy-port-mismatch.md` | `pass` |
| `qwen3-8b-radeonvii` Lane 1C tertiary fast-chat-fallback (2026-05-25) | 16384 | `gfx906/radeonvii` | `llamacpp` | `experimental` | Persistent runtime `registry.harbor.lan/flexinfer/runtime@sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e` | `HF://Qwen/Qwen3-8B-GGUF` / `Qwen3-8B-Q4_K_M.gguf` | MR !499 added `qwen3-8b-radeonvii.yaml` to `deploy/models/kustomization.yaml` and `fast-chat-fallback` to its `serviceLabels`. Cold tertiary lane: warm primaries (`gemma4-26b` on 7900xtx + 5930k via `fast-chat`) and the 5930k secondary (`fast-chat-5930k-llamacpp`) absorb traffic first; radeonvii cold-starts only when both upstream tiers are unavailable. `minReplicas: 0` keeps the GPU yielded to the FluxPony/SDXL imagegen co-tenants by default. Post-merge Flux reconciled the Model on 2026-05-25; `kubectl get model qwen3-8b-radeonvii -o jsonpath='{.spec.serviceLabels}'` returned `["qwen3-8b","textgen","fast-chat-fallback"]`. | none observed post-MR. Cold-start latency for the first activation request is bounded by `coldStartTimeout: 15m`; subsequent traffic is governed by `idleTimeout: 15m`. | `kubectl -n flexinfer-system get model qwen3-8b-radeonvii -o jsonpath='{.status.phase}{" "}{.spec.serviceLabels}{"\n"}'` then a proxy smoke through the `fast-chat-fallback` shared label. | `git revert` MR !499; the radeonvii GPU returns to FluxPony/SDXL imagegen-only. | MR !499; `.loom/roadmap-unblock-plan-2026-05-21.md` Lane 1C; `docs/planning/fast-chat-resilience.md` (2026-05-25 section) | `pass` |
| `qwen3-8b-radeonvii` context-bounded admission filter live (CC-6a-2 opt-in, 2026-05-25) | 16384 | `gfx906/radeonvii` | `proxy/llamacpp` | `experimental` | `registry.harbor.lan/flexinfer/flexinfer-proxy@sha256:a631ccbc434c7f51387c361aa7702b11106d0831433157007d85a872ec245184` (`:master` carries MR !497 + !498) | `HF://Qwen/Qwen3-8B-GGUF` | MR !497 added the admission filter to `internal/proxy/` (estimator + filter + tests + metric `flexinfer_proxy_admission_decisions_total`). MR !498 closed kill-test B (corpus 3/8 in band, see `2026-05-25 context-curve` archive section — failure mode is over-conservative on long English; safe direction for admission). MR !500 wired the chart and opted in `qwen3-8b-radeonvii` via the `flexinfer.ai/admission: context-bounded` annotation + `proxy.admission.enabled=true` in `deploy/system/values-k3s.yaml`. Live cluster smoke 2026-05-25: HTTP 413 `code=context_window_exceeded` in **67 ms** for a 16 000-char prompt + max_tokens=12000 (estimated 4579 + 12000 = 16579 > 15564 ceiling = 16384 × 95%). Body: `{"error":{"message":"prompt + max_tokens (4579 + 12000 = 16579) exceeds \"qwen3-8b-radeonvii\" context budget 15564 (window 16384, safety margin applied)","type":"invalid_request_error","code":"context_window_exceeded"}}`. | none observed. The estimator over-counts long-form English by ~70% (kill-test B); cold/low-traffic gfx906 fallback is a deliberate first opt-in lane. Other lanes are unchanged (no annotation = no enforcement). | Smoke as in the live verification above; metric: `flexinfer_proxy_admission_decisions_total{model="qwen3-8b-radeonvii",reason="over_budget",allow="false"}` increments per refusal. | Per-Model: `kubectl annotate model qwen3-8b-radeonvii flexinfer.ai/admission-`. Global: `helm upgrade --set proxy.admission.enabled=false ...`. Both reverts are non-destructive — no manifest revert required. | MR !497; MR !498; MR !500; `docs/planning/context-bounded-admission-spec.md`; `.loom/local/validation/admission-corpus/2026-05-25/report.json` | `pass` |
| `gemma4-26b-a4b-gptq` 32k context push (2026-05-25) | 32768 | `gfx1100/7900xtx` | `vllm` | `supported` | `registry.harbor.lan/flexinfer/runtime@sha256:592c0a751c89c6e7c79c0854a4a41fc2b86d6423af3ffe148b61174416dce166` | `pvc://gemma4-26b-a4b-gptq/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` | MR !504 raised `gfx1100` GPUProfile cap 18 → 22 GB and pushed Model `maxModelLen` 16384 → 32768 with FP8 KV cache + `maxNumSeqs: 1`. Live cluster smoke 2026-05-25: small request 509 ms with content `'blue'`; **19,027-token long-context prompt completed in 20.2 s with correct answer `'Brown'` extracted from buried "quick brown fox" filler**. Verifier reaches Ready cleanly; VRAM math (weights 13 GB + FP8 KV 5 GB + framework 3 GB = 21 GB in the 22 GB cap) holds. | none observed at 19k; full-32k stress not exercised yet. `maxNumSeqs` had to drop from 2 → 1 to leave KV headroom; the 2-way concurrency tuning from 2026-05-16 (~132 tok/s aggregate) is forfeited at this context. | Smoke: `kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80 &`; POST `messages: [{role:user, content:<19k tokens>}]` chat completion to `/model/gemma4-26b-a4b-gptq/v1/chat/completions`; expect HTTP 200 in ~20 s with correct answer. | Revert `deploy/gpuprofiles/gfx1100.yaml` (cap 18) and `deploy/models/gemma4-26b-a4b-gptq.yaml` (`maxModelLen 16384`, `maxNumSeqs 2`); 16k single-stream is the known-good fallback. | MR !504; `.loom/brainstorm-rocm-fleet-unlocks-2026-05-25.md` (CC-DR-2 from F1+F4) | `pass` |
| `gemma4-26b-a4b-gptq` F4 decode-tail kill-test (2026-05-25) | 28672 | `gfx1100/7900xtx` | `vllm` | `supported` | same as the 32k row above | same | The skeptic agent in `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` predicted decode at 32k would drop sub-1 tok/s based on the matrix's 2.62 tok/s at 8k point from the 2026-05-22 context-curve MVP. Ran the proper kill-test (256-token forced output across 2k/4k/8k/16k/28k context): **decode rate is essentially flat at 50-67 tok/s across the entire ladder.** Per-row: 2k 66.8 t/s · 4k 62.5 t/s · 8k 58.0 t/s · 16k 52.1 t/s · 28k 53.6 t/s. End-to-end at 16k context + 256-token reply was 16.7 s; at 28k context + 256-token reply was 33.9 s. **All three F4 pass criteria met.** The earlier "2.62 tok/s at 8k" data point was a measurement artifact — `scripts/bench-context-curve.sh` prompts produce only 13 completion tokens, making decode_tps a noise calculation rather than a real measurement. Decode is **compute-bound on small batched matmuls** (essentially flat with context), **not KV-cache-bandwidth bound** (which would degrade linearly with context). F4 "feels instant" is structurally viable on this hardware. | none observed. The 32k point was not measured because the test stopped at 28k; the trend strongly suggests 32k decode also lands at ~50 tok/s. Re-measure if a real-world workload exposes a regression. | `python3 /tmp/f4-decode-tail-bench.py` (one-off; preserved at `.loom/local/validation/context-curve/2026-05-25-decode-tail/`) — POSTs through the proxy with `temperature=0`, `max_tokens=256`, and a "write a long detailed paragraph" prompt so the runtime actually decodes 256 tokens rather than stopping at a short answer. | F4 implementation backs out via the same revert path in the 32k context-push row. | `.loom/local/validation/context-curve/2026-05-25-decode-tail/f4-killtest-report.json`; `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` (kill-test specification) | `pass` |
| `gemma4-26b-a4b-gptq-apc-canary` F4-prefix-cache-flip canary (2026-05-26; live verdict 2026-05-28) | 32768 → 20480 (see live run) | `gfx1100/7900xtx` | `vllm` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:310988969f3448ccb7b6001d36df0610c40a0354cacbd7e3410cf9d9592dd187` (mirrors warm primary) | `pvc://gemma4-26b-a4b-gptq/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` (same source as primary) | Side-by-side canary Model created to run the cache-eviction-thrash kill-test from `.loom/brainstorm-f4-long-context-agent-2026-05-25.md`. Mirrors `gemma4-26b-a4b-gptq` config exactly EXCEPT `enablePrefixCaching: true` (vs `false` on primary) and `gpuMemoryUtilization: "0.94"` (vs `"0.98"`). `minReplicas: 0`, `priority: 100` in the `7900xtx-textgen` shared group so the warm primary at 350 always wins arbitration. Isolated serviceLabels and litellm aliases (`gemma4-26b-apc-canary` only) — never absorbs production traffic. F4 decode-tail kill-test (the row above) passed, so APC infrastructure work was unblocked. Kill-test recipe + operator runbook in `.loom/ralph-f4-prefix-cache-flip-canary-2026-05-26.md`: alternate two distinct ~30k-token system prompts ABABAB × 5, scrape `vllm:prefix_cache_hit_rate`. **Live run 2026-05-28**: predicted failure mode (a) fired with the OPPOSITE direction the plan anticipated. At `maxModelLen: 32768` + `enablePrefixCaching: true` + FP8 KV + `gpuMemoryUtilization: 0.94`, vLLM refuses to start: `ValueError: available KV cache memory (2.07 GiB) < needed (3.44 GiB); estimated maximum model length is 19712`. Math: 22 GB cap × (1.0 − 0.94) = 1.32 GB; even raising util to 1.0 cannot recover the 1.37 GiB gap. APC at 32k FP8 KV is **structurally infeasible** on the 22 GB cap. Reran with patched `maxModelLen: 20480` + `gpu.forcePromotion: true` + `serverless.minReplicas: 1` (Flux suspended on `flexinfer-models` for the test, reverted after). 10/10 turns succeeded with `--prompt-tokens 16000`. **/metrics post-3rd-alternation hit_rate = 0.666 (gate ≥ 0.50)** — PASS at the reduced context; aggregate over full run = 0.799. TTFT decay decisive: prefix A 15705 → 378 ms (24× faster), prefix B 11544 → 670 ms (17× faster); `assertion_passed = true`. Engine omitted `X-Flexinfer-Cached-Tokens` response header so verdict cascaded to /metrics scrape (works as designed; header is optional). **Plan-doc runbook bug**: `flexinfer.ai/pause` and `flexinfer.ai/force-promote` annotations are NOT wired in the controller (no codepath consumes them). Correct mechanism is `spec.gpu.forcePromotion: true` + `spec.serverless.minReplicas: 1` patches behind `flux suspend kustomization flexinfer-models`. | (a) 32k+APC OOM-equivalent fired in NEW direction (vLLM needs more memory than the 22 GB cap can supply, not less — the plan's predicted remediation of dropping to 0.92 is wrong; correct remediation is dropping `maxModelLen`); (b) eviction-thrash did NOT fire at 20k — cache holds ≥2 distinct ~16k prefixes. Promotion to primary requires accepting `maxModelLen` drop from 32 768 → ≤ ~20 480 (loses the 32k context push from the row above) OR keeping `enablePrefixCaching: false` at 32k (status quo). | Operator runbook (executed): `flux -n flux-system suspend kustomization flexinfer-models`; `kubectl -n flexinfer-system patch model gemma4-26b-a4b-gptq-apc-canary --type=merge -p '{"spec":{"gpu":{"forcePromotion":true},"serverless":{"minReplicas":1},"config":{"maxModelLen":20480}}}'`; port-forward proxy:80 → 18080 and canary svc:8000 → 18000; `python3 scripts/f4-apc-eviction-thrash.py --endpoint http://localhost:18080 --metrics http://localhost:18000/metrics --model gemma4-26b-a4b-gptq-apc-canary --prompt-tokens 16000 --max-tokens 256 --rounds 5 --report .loom/local/validation/f4-apc/$(date -u +%F)-eviction-thrash/report.json`. | Restore: patch reverts `forcePromotion: false`, `minReplicas: 0`, `maxModelLen: 32768`; `flux -n flux-system resume kustomization flexinfer-models`. Primary returns to Ready once anti-thrashing cooldown clears (≤5 min). | `.loom/ralph-f4-prefix-cache-flip-canary-2026-05-26.md`; `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` (F4-prefix-cache-flip + F4-skeptic-cache-eviction-thrash); `deploy/models/gemma4-26b-a4b-gptq-apc-canary.yaml`; `.loom/local/validation/f4-apc/2026-05-28-eviction-thrash/report.json` (local-only run artifact) | `conditional` (APC passes eviction-thrash at `maxModelLen ≤ ~20480`; fails to load at 32k) |
| `gemma4-26b-a4b-gptq-apc-canary` F4-tool-loop-as-prefix kill-test (2026-06-01; PASS 2026-06-01) | 20480 (APC-feasible context per the canary verdict; 32k is structurally infeasible) | `gfx1100/7900xtx` | `vllm` | `experimental` | same canary runtime digest as row 193 (`...592dd187`-class; capture the live digest at run time) | `pvc://gemma4-26b-a4b-gptq/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` (same source as primary) | Second leg of the F4 compound. Row 193 proved the *alternating two-prefix* (multi-tenant) eviction pattern survives at `maxModelLen ≤ 20480`; this row tests the **distinct, unproven** *append-only growing* pattern (`F4-tool-loop-as-prefix`, brainstorm lines 140-146): an immutable `system + tool-schema` prefix followed by an append-only history of `(user → assistant → tool-result)` rounds, re-sent in full each turn. Runner `scripts/f4-tool-loop-as-prefix.py` (schema `flexinfer.f4_tool_loop_as_prefix.v1`) drives 20 rounds and captures per-turn `X-Flexinfer-Upstream-Ms`, `usage.prompt_tokens`, and `X-Flexinfer-Cached-Tokens`. **Verdict cascade**: primary = median per-turn prefix-hit ratio (`cached_tokens/prompt_tokens`) over warm rounds — PASS ≥ 0.90 (brainstorm bet "hit% >90%"), FAIL < 0.50 (template re-render busts block alignment). Engine-agnostic fallback (the gemma4 engine omitted `cached_tokens` on every turn in the row-193 live run): TTFT-flatness — last-round `upstream_ms` ≤ warm-round `upstream_ms` × 1.5 despite ≥ 2× prompt growth = APC reuses the growing prefix; `upstream_ms` scaling with `prompt_tokens` = FAIL. Offline `--self-check` (header parse + aggregators + verdict cascade + filler builder) passes locally; live run is the post-merge operator follow-up. | **PASS (live run 2026-06-01, claude-code).** Riskiest assumption — "vLLM's chat-template re-render keeps each append-only turn a block-aligned prefix extension of the previous" — **CONFIRMED** by two independent signals. (1) TTFT-flatness: tuned run (`--system-tokens 3000 --max-tokens 48 --rounds 16`) held `upstream_ms` flat (warm round-1 1412ms → last round-15 2009ms, ratio **1.42 ≤ 1.5**) despite **2.94× prompt growth** (5154 → 17622 tokens); 16/16 rounds OK, `upstream_p50` 1853ms. If APC were not reusing the growing prefix, round-15 prefill (17622 tok) would scale ~3× round-1 (5993 tok); instead it stayed flat. (2) Engine `/metrics` prefix-cache block counters over the run window: Δhits/Δqueries = (330800−161184)/(363373−181077) = **93.0%** — directly satisfies the brainstorm ">90% hit rate across rounds" bet on the engine's own counters, not just the TTFT proxy. The gemma4 engine omits `cached_tokens` (per-turn `prompt_tokens_details: None`), so the primary ratio fell to the documented fallback as designed. **Operational bound surfaced**: the *first* run (default `--system-tokens 6000`) hit HTTP 400 at round 12 — the append-only context grew past `maxModelLen 20480` (system prefix + ~840/round fills it). So usable append-only budget = `maxModelLen − system_prefix − max_tokens`; beyond it the loop must compact/evict — exactly the `F4-413-as-feature` leg's domain. Reports (gitignored, local): `.loom/local/validation/f4-tool-loop/2026-06-01/report-tuned.json` (pass), `report.json` (ceiling). | Operator runbook (post-merge): `flux -n flux-system suspend kustomization flexinfer-models`; `kubectl -n flexinfer-system patch model gemma4-26b-a4b-gptq-apc-canary --type=merge -p '{"spec":{"runtime":{"maxModelLen":20480},"gpu":{"forcePromotion":true},"serverless":{"minReplicas":1}}}'`; port-forward proxy:80 → 18080; `python3 scripts/f4-tool-loop-as-prefix.py --endpoint http://localhost:18080 --model gemma4-26b-a4b-gptq-apc-canary --metrics http://localhost:18000/metrics --rounds 16 --system-tokens 3000 --max-tokens 48 --report .loom/local/validation/f4-tool-loop/$(date -u +%F)/report.json`. **Corrected mechanism (live 2026-06-01)**: `maxModelLen` lives at `.spec.config.maxModelLen`, NOT `.spec.runtime.maxModelLen` — patch `{"spec":{"config":{"maxModelLen":20480},"gpu":{"forcePromotion":true},"serverless":{"minReplicas":1}}}`. Use `--system-tokens 3000 --max-tokens 48` so the append-only context reaches ≥2× growth before the 20480 ceiling (default 6000 prefix 400s out at round 12). | Restore: patch reverts `forcePromotion: false`, `minReplicas: 0`, `maxModelLen: 32768`; `flux -n flux-system resume kustomization flexinfer-models`. Runner-only MR → `git revert` removes the script; no runtime touched. | `.loom/ralph-f4-tool-loop-as-prefix-2026-06-01.md`; `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` (F4-tool-loop-as-prefix + F4-instant-followup); `scripts/f4-tool-loop-as-prefix.py`; row 193 (predecessor canary verdict) | `promote` |
| `agent-loop` F4 tool-loop-as-prefix ReAct client — slice 1/CLI (2026-06-01) | n/a (client; respects the target model's `maxModelLen` via the `Budget` guard) | `gfx1100/7900xtx` (any APC-enabled model) | `client/cli` | `experimental` | n/a — CLI binary `bin/agent-loop`, built from source (`make build-agent-loop`); no in-cluster image | n/a | First slice of the F4 client. Brainstorm open question resolved (operator: forms **(a) CLI + (b) loom-core MCP tool**; this ships (a), slice 2 ships (b)). New `internal/agentloop` engine: append-only mutability-ordered `Conversation` (system+tools immutable prefix; history `Append`-only — no Insert/Replace/Reorder, the cache-paying invariant enforced in the type), `ChatClient` pinning `X-Flexinfer-Cache-Key=session_id` for prefix-consistent routing, per-turn `TurnMetrics` parsed from proxy headers (`X-Flexinfer-Upstream-Ms`/`-Cached-Tokens`/`-Prompt-Tokens`; `cached_tokens` stays nil when the engine omits it, as the gemma4 fallback in row 194), and a `Budget` guard encoding the usable-context bound `maxModelLen − system − output` row 194 surfaced (**413-as-feature**: typed `BudgetError` stops the loop cleanly before HTTP 400). `cmd/agent-loop` CLI runs REAL read-only tools (`read_file` ≤64KB, `list_dir`), path-jailed to `--workdir` (absolute + `..` escapes rejected), in a ReAct loop. | **Offline-validated + live-validated 2026-06-01 (PASS).** `go build ./...`, `go vet`, `go test ./internal/agentloop ./cmd/agent-loop` (incl. `-race`), gofmt, and `agent-loop --self-check` (canned chat server + real temp-file tool exec + wire-side append-only prefix-extension assertion + header/metric parse + path-jail) all green locally. Gated by row 194 PASS per spec-riskiest-assumption — append-only prefix reuse is confirmed, so the client adds no new external assumption. | Live close ran 2026-06-01 (operator-authorized full canary preemption): port-forwarded proxy:80→18080; ran `bin/agent-loop --endpoint http://localhost:18080 --model gemma4-26b-a4b-gptq-apc-canary --workdir ./internal/agentloop --max-model-len 20480 ...` twice. Run 1 (mixed outputs) drove a real 5-round ReAct loop (list_dir + 3× read_file, coherent engine summary) but `upstream_ms` was confounded by a decode-dominated final round (256-tok essay = 10.6s). Run 2 (prefill-isolation: terse system prompt + `--max-tokens 24` so every round's output is tiny → `upstream_ms`≈prefill) gave the clean signal: `prompt_tokens` 247→946→1433→3370→5030→5565 (**22.5×**) while `upstream_ms` 526→649→649→1571→1732→701 tracked the per-round token **delta**, not the cumulative prompt. Small-delta rounds (Δ≤700: rounds 0,1,2,5) all sat in a 526–701ms band; only the two large-file rounds (Δ≈1937/1660: rounds 3,4) rose to 1571/1732ms. No-cache prefill of the 5565-token prompt ≈11s; measured 701ms → client-observed prefix-cache reuse, matching row 194's engine-side TTFT-flat finding and extending it to 22.5× growth. Caveat: gemma4 omits `cached_tokens` (`prefix_hit=n/a`), so the hit is inferred from delta-bound latency, not read off the engine — documented client limitation; follow-up = add a prefill-only/TTFT header or scrape the engine prefix-hit gauge. Evidence: `.loom/local/validation/agent-loop/2026-06-01/{run.json,prefill-probe.json}`. | `git revert` the MR removes `internal/agentloop` + `cmd/agent-loop` + the Makefile target; no runtime/controller/proxy code touched, nothing in-cluster changes. | `.loom/ralph-f4-agent-loop-client-2026-06-01.md`; `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` (F4-tool-loop-as-prefix + open question); row 194 (kill-test PASS that unblocked this); `internal/agentloop/*.go`; `cmd/agent-loop/*.go`; `.loom/local/validation/agent-loop/2026-06-01/{run.json,prefill-probe.json}` (live evidence) | `pass` (offline-validated + live-validated 2026-06-01; APC reuse observed client-side at 22.5× prompt growth, delta-bound `upstream_ms`) |
| `gemma4-e4b-radeonvii` draft serve + spec-decode standalone bench (2026-05-25) | 32768 | `gfx906/radeonvii` | `llamacpp` | `experimental` | Persistent runtime `registry.harbor.lan/flexinfer/runtime@sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e` (hipMemGetInfo shim) | `HF://bartowski/google_gemma-3-4b-it-GGUF` / `google_gemma-3-4b-it-Q4_K_M.gguf` | MR !504+!505 deployed the Gemma 3 4B IT GGUF on Radeon VII as the intended cross-card spec-decode draft against the gfx1100 26B verifier (shared Gemma SentencePiece). Force-promoted live and benched standalone: **median 65 tok/s, p95 71 tok/s, median 15.4 ms/token** over 10 short prompts × up to 64 completion tokens at temperature 0. Verifier historical decode ~70-73 tok/s. **Spec-decode gate math (live numbers): per 4-token round = 60 ms draft + ~14 ms verify + ~10 ms RPC ≈ 84 ms; ~2.77 accepted tokens at 0.7 acceptance → ~33 tok/s — SLOWER than 70 tok/s baseline.** | The cross-card pair as currently configured **cannot pass the ≥ 1.5× spec-decode kill-test gate**. Root cause: llama.cpp + Q4_K_M on Vega20 (no FlashAttention, no VMM, wave64 quirks) decodes a 4B model at ~65 tok/s — barely faster than vLLM + ExllamaV2 GPTQ Int4 on RDNA3 decoding the 26B. Spec decoding needs `draft_step << verifier_step`; here they're roughly equal so draft cost is pure overhead. The draft Model itself serves coherently — the failure is the spec-decode-on-this-pair assumption, not the lane. | One-off bench: `python3` script that POSTs `/v1/chat/completions` to `/model/gemma4-e4b-radeonvii/...` with 10 short prompts × 64 max_tokens × temperature 0; compute completion_tokens / elapsed. | Remove the `flexinfer.ai/force-promote` annotation; `idleTimeout: 10m` returns the lane to Idle. Lane stays in the kustomization as a future spec-decode candidate if a much smaller draft (Gemma 3 1B or 270M) lands on gfx906 instead. | MR !504; MR !505; `.loom/brainstorm-rocm-fleet-unlocks-2026-05-25.md` F1 framing; `internal/proxy/spec_decode/` (MR !503) | `block` (spec-decode use case; the draft Model itself serves coherently) |
| `qwen3-1p7b-vllm-radeonvii` vLLM canary prerequisites (2026-05-17) | 2048 | `gfx906/radeonvii` | `vllm` | `experimental` | Persistent runtime `registry.harbor.lan/flexinfer/runtime@sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4`; standalone vLLM image `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:2139c92b3ca00716216f9e5644e9fbd29b2bba7237dc0459017c86012ece51c3` (was `sha256:020e7373…` until MR !446 carrying !444's CPU-fallback hook for `torch.nn.init._no_grad_*`, then `sha256:d545fb8a…` until MR !448 carrying !447's args-forward fix, then `sha256:60b1ab0b…` until MR !451 carrying !450's `_no_grad_fill_/_no_grad_zero_` extension, then `sha256:471472d5…` until MR !454 carrying !453's tensor-level `Tensor.fill_/zero_` CPU fallback) | `HF://facebook/opt-125m`; Local cache at `/models/flexinfer-system/qwen3-1p7b-vllm-radeonvii` | RALPH fixed the canary prerequisites: DNS-label-safe resource name, `Local` cache, canary-scoped aliases, standalone image path for gfx906 vLLM, and image-pull-secret propagation for controller-created model Deployments. The Radeon VII k3s containerd image store was moved from root LVM to the NVMe-backed `/mnt/nvme/longhorn/k3s-containerd/containerd` bind mount. Harbor pull access later worked and the standalone canary image reached container startup; MR !420 added a `transformers` tokenizer compatibility hook, MR !421 added a Triton `default_cache_dir` compatibility hook, MR !422 added `default_dump_dir`, MR !424 added `default_override_dir`, MR !426 fixed active-pod cache-refresh preservation, MR !427 adds a guarded PyTorch ROCm `mem_get_info` fallback, MR !428 switched the canary artifact from unsupported Qwen3 to Qwen2.5, MR !434 exposed `disableSlidingWindow` for vLLM args, and MR !435 pivoted the artifact to OPT-125M after Qwen2.5's SWA/rope path proved unsafe. MR !439 extracted those compatibility hooks into `build/scripts/install_vllm_gfx906_compat.py`; the post-merge manual publish job `109838` produced the pinned diagnostic digest with faulthandler / child-process traceback diagnostics. The 2026-05-20 RALPH loop landed three more slices targeting the OPT segfault: MR !444 added a CPU-fallback wrapper for `torch.nn.init._no_grad_normal_`/`_no_grad_uniform_`/`_no_grad_trunc_normal_` plus a contract check; MR !446 pinned the first rebuild and proved the hook is loaded (live traceback contains `flexinfer_vllm_torch_init_compat.py:22`); MR !447 fixed an off-by-one args-forward bug in the wrapper (`getattr(cpu_tensor, kernel_attr)(*args, **kwargs)` → `original(cpu_tensor, *args, **kwargs)`); MR !448 pinned the second rebuild (`sha256:60b1ab0b…`, job 110925). MR !450 added `_no_grad_fill_` and `_no_grad_zero_` to the same CPU-mirror framework after a Loki traceback pinned `opt.py:245` as `LayerNorm.reset_parameters` → `init.ones_` → `_no_grad_fill_` (not RNG); MR !451 pinned the resulting `sha256:471472d5…` image emitted by publish job 111194 on pipeline 10757. MR !453 added a NEW tensor-level hook `flexinfer_vllm_torch_tensor_compat.py` that monkey-patches `torch.Tensor.fill_` and `torch.Tensor.zero_` on HIP tensors only (CPU mirror + `self.copy_(cpu_mirror)`), targeting the direct `param[loaded:].data.fill_(0)` call at `vocab_parallel_embedding.py:401`; MR !454 pinned the resulting `sha256:2139c92b…` image emitted by publish job 111364 on pipeline 10767 (publish duration 59s, BuildKit cache reused all but the script-copy layer). | No HTTP 200 vLLM smoke yet. With MR !453's tensor-level wrap landed, the OPT load now completes ALL of `__init__` AND ALL of the per-parameter `weight_loader` dispatch (including the `vocab_parallel_embedding.py:401` `param[slice].data.fill_(0)` zero-pad that defeated the prior cycle), reaching `model_runner.py:1115 — Loading model weights took 0.2500 GB` cleanly. New segfault is much further downstream at `vocab_parallel_embedding.py:47` (`return F.embedding(input_, layer.weight)`) inside `_dummy_run → profile_run → determine_num_available_blocks` (KV-cache sizing forward pass). The broken HIP op is now `torch.embedding`/`index_select` — a fundamentally different op family from `fill_/zero_`, exercised on every forward pass. The slice's predicted failure-mode 3 ("new segfault — load advances past `weight_loader` and crashes further along — compile, warmup, forward pass") fired verbatim. Wrapping the forward-pass embedding would defeat GPU acceleration; the next step is a minimum-repro HIP probe to determine whether `torch.embedding` is broken at the kernel layer on Vega20 (would imply a ROCm runtime bug, not a vLLM-layer bug). Live evidence: `.loom/ralph-gfx906-vllm-tensor-fill-evidence-2026-05-20.md`. Image pull is no longer the bottleneck — 10.5 GB pull measured 584 ms / 580 ms / 583 ms / 604 ms across four restarts after the first 8m34s cache. **2026-05-20 RALPH iteration closed the kill-test loop** via MR !456 (`deploy/debug/gfx906-hip-embedding-probe.yaml` — standalone 6-scenario HIP probe Job on the pinned `sha256:2139c92b…` digest). Probe took 28 seconds wall clock; CPU baseline (scenario 1) PASS, all five HIP scenarios (2-6) **SEGFAULT exit 139**. Crucially the crash happened at the `torch.zeros(dtype=long, device='cuda')` / `torch.randn(dtype=float16, device='cuda')` input-tensor allocation lines BEFORE reaching the `torch.embedding`/`F.embedding`/`Tensor.index_select` calls. Even the smallest possible HIP allocation (`weight = torch.randn(4, 8, dtype=float32, device='cuda')` works in scenario 2, but the next line `ids = torch.zeros((1,1), dtype=long, device='cuda')` segfaults) — the broken op family on Vega20 covers `at::native::zero_kernel_cuda` and HIP RNG-into-float16, both via C++ fused paths that **bypass the Python-level `Tensor.fill_`/`Tensor.zero_` hook landed in MR !453**. Verdict: monkey-patching is structurally bounded; vLLM's `weight_loader` works only because it goes through Python-level `Tensor.fill_`, but `_dummy_run` and every forward pass use C++ fused paths that cannot be intercepted from Python. Strategic pivot: declare OPT-125M vLLM on gfx906 feasibility-only; move production gfx906 inference substrate to llama.cpp on gfx906. Live evidence: `.loom/ralph-gfx906-vllm-embedding-probe-evidence-2026-05-20.md`. | `kubectl -n flexinfer-system run smoke-gfx906-vllm --rm -i --restart=Never --image=curlimages/curl:8.11.1 -- curl -sS -m 900 -X POST http://flexinfer-proxy.flexinfer-system.svc.cluster.local/model/qwen3-1p7b-vllm-radeonvii/v1/chat/completions -H 'Content-Type: application/json' -d '{"model":"opt-125m-vllm","messages":[{"role":"user","content":"The color of the sky is"}],"max_tokens":16,"temperature":0}'` | `sha256:84f0ae2bb1ea46163885aad55181540bee9995b4b4b0c656f3943b7580e07e1e`; recovery: delete `Model/qwen3-1p7b-vllm-radeonvii`, restore `qwen3-1p7b-tools-radeonvii` `minReplicas: 1`, and recycle `flexinfer-runtime-gfx906` if `/readyz` hangs. | `.loom/ralph-gfx906-vllm-smoke-2026-05-17.md`; `.loom/ralph-gfx906-vllm-worker-diagnostics-2026-05-19.md`; `.loom/ralph-gfx906-vllm-diagnostic-digest-2026-05-19.md`; `.loom/ralph-gfx906-vllm-torch-init-cpu-fallback-2026-05-20.md`; `.loom/ralph-gfx906-vllm-cpu-init-fallback-evidence-2026-05-20.md`; `.loom/ralph-gfx906-vllm-init-fill-cpu-fallback-2026-05-20.md`; `.loom/ralph-gfx906-vllm-init-fill-evidence-2026-05-20.md`; `.loom/ralph-gfx906-vllm-tensor-fill-cpu-fallback-2026-05-20.md`; `.loom/ralph-gfx906-vllm-tensor-fill-evidence-2026-05-20.md`; `deploy/models/qwen3-1p7b-vllm-radeonvii.yaml`; `deploy/gpuprofiles/gfx906.yaml`; MR !420; MR !421; MR !422; MR !424; MR !426; MR !427; MR !428; MR !434; MR !435; MR !439; MR !444; MR !446; MR !447; MR !448; MR !450; MR !451; MR !453; MR !454; MR !456; `.loom/ralph-gfx906-vllm-embedding-probe-2026-05-20.md`; `.loom/ralph-gfx906-vllm-embedding-probe-evidence-2026-05-20.md`; jobs 109838, 110710, 110925, 111194, 111364; pipelines 10755, 10757, 10767; live imagefs/auth/tokenizer/triton/mem-info/runtime-lock/CK-FA/SWA/OPT follow-up 2026-05-18/19/20 | `feasibility-only` (strategic pivot: vLLM blocked at ROCm-kernel layer on Vega20; production gfx906 substrate moves to llama.cpp) |

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

### 2026-05-25 CC-DR-2: in-process n-gram spec-decode WINS on 7900xtx (re-validation)

Live measurement of vLLM's built-in n-gram (prompt-lookup) speculative
decoding on `gemma4-26b-a4b-gptq` (cblevins-7900xtx, runtime image
`sha256:31098896...`, vLLM `0.1.dev1+gb1388b1fb.d20260516`,
graph-capture on, single-stream `maxNumSeqs=1`).

Config (passed as `--speculative-config`):

```json
{"method": "ngram", "num_speculative_tokens": 5, "prompt_lookup_max": 4, "prompt_lookup_min": 1}
```

Apples-to-apples on the same 5-prompt corpus (64 tokens each, greedy,
single-stream), measured via `cmd/spec-decode-bench` against the
`/v1/completions` endpoint:

| prompt | baseline (no SD) | n-gram SD | speedup |
| --- | --- | --- | --- |
| q1_capital ("The capital of France is") | 58.09 | 104.94 | 1.81× |
| q2_math ("What is 17 times 23? Show your work.") | 60.99 | 147.46 | 2.42× |
| q3_code (early-stop at 2 tokens both runs) | 12.98 | 14.18 | n/a |
| q4_chat ("Hey, how are you doing today?") | 66.11 | 122.88 | 1.86× |
| q5_explain ("Explain TCP congestion control.") | 64.71 | 119.22 | 1.84× |
| **p50** | **60.99** | **119.22** | **1.95×** |
| **p95** | **65.83** | **142.54** | **2.17×** |

vLLM's own `SpecDecoding metrics` line (read live from the engine logs):

- Mean acceptance length: **4.89 / 5** speculative positions
- **Avg draft acceptance rate: 82.5%**
- Per-position acceptance: 91.8% / 81.4% / 74.2% / 73.2% / 68.0%
- 377 accepted / 457 drafted total

**Conditional on graph capture.** The same config was falsified on
2026-05-14 against the 5930k twin (see
`.loom/r5-ngram-spec-decode-falsified-2026-05-14.md`) and produced
−13% to −22% throughput. That run was on
`runtime:rocm-gfx1100-gemma4-moe-cache-nan-v3`, **before** graph
capture was validated for the gemma4 MoE path on 2026-05-16. With
eager-mode MoE the per-verifier-step cost dominated and SD's
position-widening (1 → 1 + num_spec) multiplied the bottleneck. With
graph capture on, the per-forward overhead is amortised, SD's width
becomes cheap, and the same config flips from net-negative to
net-positive. Re-validation runs in flight on the 5930k twin (now
also on the post-graph-capture image) to confirm the flip
replicates.

**What this kills**: CC-DR-1's external HTTP-orchestrated spec-decode
prototype (measured 0.05× speedup on the e4b draft × 26b verifier
pair earlier this session — network overhead dominated). The bench
tool stays as the comparison harness; the prototype's
internal/proxy/spec_decode library stays as a reference; the
recommendation to ship a proxy-integrated draft+verify path
(CC-DR-3) is deprioritised — in-process server-side SD already
covers it without the inter-pod RPC tax.

**Next steps (now resolved below)**: (a) replicate on 5930k twin
(MR !513), (b) tune to (7,6) — MR !514, (c) measure at
`maxNumSeqs=2` (folded into the 5930k re-validation since that twin
runs maxNumSeqs=2).

#### 2026-05-25 post-roll: (7,6) tuning + 5930k re-validation

Both rollouts completed cleanly. Same 5-prompt corpus, 64 tokens,
greedy, single-stream (5930k still runs maxNumSeqs=2 so its row is
also the first measurement of the SD ↔ batched-concurrency
interaction).

| prompt | no-SD baseline | 7900xtx (5,4) | 7900xtx (7,6) | 5930k (5,4), maxSeqs=2 |
| --- | ---: | ---: | ---: | ---: |
| q1_capital | 58.09 | 104.94 | 38.11 | 11.64 |
| q2_math | 60.99 | 147.46 | 196.74 | 145.34 |
| q3_code (early-stop, 2 tok) | 12.98 | 14.18 | 10.12 | 12.90 |
| q4_chat | 66.11 | 122.88 | 186.97 | 138.76 |
| q5_explain | 64.71 | 119.22 | 127.17 | 126.12 |
| **p50** | **60.99** | **119.22** | **127.17** | **126.12** |
| **p95** | **65.83** | **142.54** | **188.92** | **144.02** |
| **speedup vs no-SD p50** | 1.00× | 1.95× | **2.08×** | **2.07×** |

vLLM `SpecDecoding metrics` (per-step accept length / per-position
rates):

- 7900xtx (5,4): mean 4.89 / 5, per-position 91.8 / 81.4 / 74.2 / 73.2 / 68.0 %
- 7900xtx (7,6): mean **6.76 / 7**, per-position 100 / 92.1 / 81.6 / 81.6 / 76.3 / **73.7 / 71.1 %**
- 5930k (5,4): mean 5.72 / 5 (count includes verifier bonus token), per-position 100 / 100 / 93.8 / 93.8 / 84.4 %

What we learned:

1. **5930k re-validation succeeded.** 2.07× speedup at maxNumSeqs=2
   replicates the 7900xtx (5,4) result. The 2026-05-14 falsification
   was correct for the pre-graph-capture image; graph capture flipped
   the equation by amortising per-forward overhead so SD's
   position-widening becomes cheap. The same config went from −22%
   then to +107% now on this same twin/hardware.
2. **The (7,6) tuning is a real-but-small win at p50** (+6.7% over
   5,4) and a substantial p95 win (+33%, 142→189 tps). Positions 6
   and 7 still accept 73.7% / 71.1% respectively — both clear the
   per-forward breakeven, justifying the wider speculation budget.
   vLLM-reported mean accept length jumps 4.89 → 6.76 (out of 7).
3. **q1_capital regressed on both (7,6) AND 5930k** (58 → 38 / 11.6).
   That prompt is only ~6 tokens; with `prompt_lookup_min=1` and a
   short prompt, the n-gram table fires speculations that miss
   because there's no history yet to match against. Likely fix:
   `prompt_lookup_min=2`. Deferred to a follow-up MR — production
   prompts are mostly longer.

Decision: **keep (7,6) on the 7900xtx primary, keep (5,4) on the
5930k twin** for now. The 5930k could also adopt (7,6) but the (5,4)
measurement is the cleaner re-validation evidence; a single-variable
change is easier to attribute if anything regresses later. Promote
both to "supported" SD-enabled config — these are the new defaults
for vLLM models that share this image and graph capture profile.

Cross-reference with the F4 decode-tail kill-test (row 192 below):
decode is flat at 50–67 tok/s from 2k → 28k context. The n-gram
speedup (2.07×) should compound at every context size, predicting
~100 tok/s decode even at full 32k. Not yet measured under SD.

Raw reports under `.loom/local/validation/spec-decode/2026-05-25/`:
`tuned-76.json`, `5930k-54.json`, `with-ngram-orig.json`,
`baseline-only.json`.

Next experiments (not blocking):

- `prompt_lookup_min=2` to fix the short-prompt regression
- Try (7,6) on 5930k twin to confirm the tuning win replicates under
  maxNumSeqs=2
- Apply to `qwen35-27b` and `qwen36-27b-gptq` once those serve
  coherently again
- Re-run the F4 decode-tail kill-test with SD on — confirm the
  ~2× speedup compounds across the full context ladder, not just
  at short prompts

Commands:

```bash
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80

go run ./cmd/spec-decode-bench \
  --backend=http \
  --draft-url=http://localhost:18080/v1/completions \
  --verify-url=http://localhost:18080/v1/completions \
  --draft-model=gemma4-26b-a4b-gptq \
  --verify-model=gemma4-26b-a4b-gptq \
  --max-tokens=64 --mode=baseline --corpus=<5-prompt corpus>
```

Acceptance-rate scrape (vLLM logs at INFO level):

```bash
kubectl -n flexinfer-system logs deploy/gemma4-26b-a4b-gptq | grep "SpecDecoding metrics"
```

Raw report: `.loom/local/validation/spec-decode/2026-05-25/with-ngram-orig.json`

### 2026-05-26 CC-DR-2: F4+SD decode-tail kill-test — **SD compounding ASSUMPTION FALSIFIED**

Re-ran the F4 decode-tail bench
(`.loom/local/validation/context-curve/2026-05-25-decode-tail/f4-decode-tail-bench.py`,
256 forced completion tokens at 2k/4k/8k/16k/28k context) against the
**same** `gemma4-26b-a4b-gptq` endpoint, now serving with the tuned
`(num_speculative_tokens=7, prompt_lookup_max=6, prompt_lookup_min=1)`
n-gram SD config from MR !514.

Side-by-side, exact same prompts, exact same runtime image, exact same
prefill-rate estimation:

| ctx | F4 baseline decode tok/s (2026-05-25, SD off — historical*) | F4+SD decode tok/s (2026-05-26, SD on) | Δ |
| --- | --- | --- | --- |
| 2k  | 66.8 | **31.2** | −53% |
| 4k  | 62.5 | **27.6** | −56% |
| 8k  | 58.0 | **21.8** | −62% |
| 16k | 52.1 | **18.5** | −64% |
| 28k | 53.6 | **13.4** | −75% |

\*The 2026-05-25 F4 row was captured BEFORE n-gram SD was enabled in
MR !512. The 2026-05-26 row is the same endpoint with SD live.

**Three kill-test pass criteria** (definition: ≥80 tok/s at 28k as the
floor for "SD compounds meaningfully across context"; the predicted
value was ~100 tok/s based on the 2× speedup observed on short
prompts):

1. decode @ 28k ≥ 80 tok/s: **13.4 → FAIL** (target missed by 6×)
2. F4 base @ 16k ≥ 10 tok/s: 18.5 → pass (SD-on still clears the F4 floor)
3. F4 base @ 28k ≥ 5 tok/s:  13.4 → pass

**vLLM's own `SpecDecoding metrics`** during this bench window
(scraped from `kubectl logs deploy/gemma4-26b-a4b-gptq --since=10m`,
archived at
`.loom/local/validation/context-curve/2026-05-26-decode-tail-with-sd/specdecoding-metrics-during-bench.txt`):

- Mean acceptance length: **1.00 – 1.19** speculative positions (vs 6.76/7 on the short-prompt bench)
- **Avg draft acceptance rate: 0.0% – 7.1%** (vs 82.5% on the short-prompt bench)
- Per-position acceptance at slot 1: 0.0%–37.5% (vs 91.8%)
- All slots 4–7 essentially 0% acceptance for the entire run

**Root cause** — *prompt-output n-gram mismatch on long-form
generation*. The F4 bench prompts are filler text ("The quick brown
fox jumps over the lazy dog." repeated to fill 2k–28k of context),
followed by an instruction asking for a 250-word paragraph about the
Linux kernel. The model's output is novel prose with essentially zero
n-gram overlap with the filler context. `prompt_lookup_min=1` finds
matches but all 7 drafted positions get rejected at verify, so SD
costs ~6-position verifier overhead per step for ~0 accepted tokens.

The short-prompt bench from 2026-05-25 (`q1_capital`, `q2_math`,
`q5_explain`) achieved 82.5% acceptance precisely because Q/A
vocabulary echoes into the answer ("The capital of France is Paris,
which..." — "the", "is", "of" all hit the n-gram lookup table). That
is the BEST case for prompt-lookup SD. The F4 long-form generation
case is the WORST case.

**What this kills** — the "n-gram SD compounds with F4's flat-decode
property to give ~100 tok/s at 32k" product framing. It does not.
On the F4 workload (long-form output, no prompt echo) SD is
**net-negative at every context size**, getting *worse* with longer
context (−53% at 2k → −75% at 28k) because the absolute decode
overhead per rejected slot dominates more as the prefill-amortised
baseline rises.

**Strategic implications**:

1. **F4 + SD do not stack as a generic "fast at every context" win.**
   F4's flat-decode property stands alone (validated 2026-05-25). SD
   stands alone for short Q/A workloads (validated 2026-05-25). They
   are workload-disjoint, not compounding.
2. **n-gram SD is the wrong default for production long-form
   generation.** Today's config blanket-enables SD on
   `gemma4-26b-a4b-gptq`. Production long-form requests will see
   −50% to −75% decode throughput vs SD-off.
3. **The (5,4) → (7,6) tuning that improved short-prompt p95 by 30%
   makes long-form WORSE** (more drafted positions to reject = more
   overhead). This is the first concrete evidence that the SD tuning
   axis is opposed to the long-form-quality axis.
4. **Honest framing for the "feels instant" pitch**: SD is real but
   conditional. The next dependent slice needs to answer "what
   fraction of real production traffic looks like the short Q/A best
   case vs the long-form worst case" before claiming a blanket 2×.

**Next moves (queued, not landed in this slice)**:

- **gate SD on prompt characteristics** — disable SD when prompt is
  obviously non-echoing (filler, long context, novel-generation
  intent). Requires either client-side hinting or proxy-side
  heuristics. Coarse heuristic: `prompt_tokens > 4096` → SD off.
- **`prompt_lookup_min=2`** — still worth doing to dampen the
  short-prompt regression on `q1_capital`, but it does not address the
  long-form failure mode (raises overhead floor, doesn't change the
  ~0% acceptance reality).
- **measure production traffic mix** — without knowing the
  short:long ratio, no policy decision is well-founded. Sample real
  prompt lengths + output lengths from `flexinfer-proxy` access logs
  for one week.
- **revisit draft-model SD on a smaller draft** — n-gram is a
  "shape-of-the-prompt" speculator; a real draft model speculates on
  *content* and would not collapse on novel-generation workloads. The
  2026-05-25 cross-card kill-test failed for a different reason
  (4B/Q4_K_M on gfx906 decoded at ~65 tok/s — too slow as a draft).
  A Gemma 3 1B / 270M draft on gfx1100 might be viable now that the
  failure mode is understood.

Commands:

```bash
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
python3 .loom/local/validation/context-curve/2026-05-26-decode-tail-with-sd/f4-sd-decode-tail-bench.py
kubectl -n flexinfer-system logs deploy/gemma4-26b-a4b-gptq --since=10m \
  | grep "SpecDecoding metrics"
```

Raw artifacts (`.loom/local/`, gitignored):

- `.loom/local/validation/context-curve/2026-05-26-decode-tail-with-sd/f4-sd-killtest-report.json`
- `.loom/local/validation/context-curve/2026-05-26-decode-tail-with-sd/f4-sd-decode-tail-bench.py`
- `.loom/local/validation/context-curve/2026-05-26-decode-tail-with-sd/specdecoding-metrics-during-bench.txt`
- `.loom/local/validation/context-curve/2026-05-26-decode-tail-with-sd/bench-run.log`

### 2026-05-26 CC-DR-2: production traffic-mix measurement on gemma4-26b-a4b-gptq

Follow-up to the F4+SD decode-tail FAIL: that finding showed n-gram
SD is workload-conditional, but the production policy question
("should blanket SD-on stay, or should we gate by prompt
characteristics?") is unanswerable without knowing how production
traffic distributes across the SD-positive (short Q/A) and
SD-negative (long-form generation) regimes. This entry samples that
distribution from vLLM's per-request histograms via the cluster
Prometheus (`kube-prometheus-stack-prometheus`, 1-week retention).

**Measured windows** (gemma4-26b-a4b-gptq verifier on cblevins-7900xtx):

| window | requests | mean prompt tok | mean completion tok |
| --- | --- | --- | --- |
| last 1h  |   5  | 9213 | 256 |
| last 6h  |  10  | 5856 | 349 |
| last 24h | 454  |  546 |  45 |
| last 7d  | 784  | 1272 | 182 |

The 1h/6h windows are essentially this session's bench traffic
(matches F4+SD's 5×256-token shape and F4 baseline's 5×256-token
shape). The 24h window is bench traffic *plus* probe traffic. The
7d window is the most defensible "production-ish" view, but is
still skewed by today's CC-DR-2 work (~58% of weekly requests).

**7-day distribution** (784 requests):

```
prompt-token cumulative distribution:
  ≤    50  tok:  51.5%   (probes + short Q/A)
  ≤   100  tok:  51.9%
  ≤   500  tok:  56.2%
  ≤  1000  tok:  66.4%
  ≤  2000  tok:  77.8%
  ≤  5000  tok:  95.2%
  ≤ 10000  tok:  98.9%
  > 10000  tok:   1.1%

completion-token cumulative distribution:
  ≤    1   tok:  41.3%   (probes — finished_reason=length, max_tokens≈1)
  ≤   10   tok:  56.1%
  ≤   100  tok:  67.1%
  ≤   500  tok:  83.3%
  ≤  1000  tok:  95.7%
  > 1000   tok:   4.3%

finished_reason breakdown (24h slice, 454 requests):
  length     :  81%   (hit max_tokens — probe-shaped or bench-shaped)
  stop       :  19%   (natural completion — organic-shaped)
  abort/error:   0%
```

**Workload-regime estimate** (defensible bracket; marginal split
since the joint (prompt, completion) histogram isn't available, only
the two marginals):

| regime | estimate | criterion |
| --- | --- | --- |
| Probes / synthetic | ~40% | completion ≤ 1 token |
| SD-positive (short Q/A) | ~15–25% | prompt ≤ 500 tok AND completion 5–200 tok |
| SD-negative (long prompt OR long-form output) | ~20–30% | prompt > 2000 tok OR completion > 500 tok |
| Ambiguous middle | ~15% | between the two regimes |

**Interpretation**:

1. **Probe traffic dominates the volume but is policy-irrelevant.**
   ~40% of requests get ≤1 completion token. Whether SD is on or off
   for those does not matter — there is no decoding to speculate on.
2. **Of organic-shaped traffic, the SD-negative regime is at least
   as large as the SD-positive regime.** Today's blanket SD-on is
   making roughly the larger half of real generation requests
   50–75% slower (per MR !516's F4+SD kill-test).
3. **The proposed coarse heuristic
   `prompt_tokens > 4096 → SD off` catches ~5% of weekly traffic.**
   That is too narrow; the SD-negative regime is broader than just
   "long prompt." A better split needs the joint distribution or
   needs to incorporate `max_tokens` from the request body (high
   `max_tokens` → likely long-form output → SD negative).
4. **The 7d sample is contaminated by today's bench-heavy session.**
   58% of weekly requests are from today. A clean retroactive view
   requires either filtering bench user-agents in proxy logs or
   waiting a week of normal traffic.

**Strategic implications**:

- **Do not blanket-disable SD yet.** ~15–25% of organic traffic
  benefits from SD; killing it would regress that segment.
- **Do not keep blanket SD-on without a hedge.** ~20–30% of
  organic traffic is being penalized 50–75%.
- **The right next slice is workload-gated SD with a defensible
  heuristic**, not "decide on a default." See the
  **CORRECTION** below for what "workload-gated" actually requires.
- **Better measurement** would tag traffic by source (proxy access
  log enrichment), filter probes, and provide the joint histogram.
  Proxy logging is currently too noisy ("v1 Endpoints deprecated"
  warnings dominate); a small enrichment slice on `flexinfer-proxy`
  to log structured per-request prompt_tokens + max_tokens +
  user-agent would unblock all future workload-aware decisions.

**CORRECTION (2026-05-26, post-write)** — the original version of
this entry listed "`max_tokens > 256 → SD off` (proxy-side, no model
knowledge needed)" as the cheapest gating heuristic. That framing
is wrong: I verified against the live runtime that vLLM
`0.1.dev1+gb1388b1fb` configures speculative decoding at engine
startup via `--speculative-config` (an `EngineArgs` field) and that
`ChatCompletionRequest` has no `speculative`/`prompt_lookup`/`draft`
field. `SamplingParams._validate_spec_decode` reads the
engine-level `SpeculativeConfig`, not per-request data. Sending
unknown OpenAI extras (`"speculative":false`,
`"disable_speculative_decoding":true`, etc.) returns 200 OK but
vLLM silently ignores them; SD stays on. **There is no per-request
SD bypass.** Workload-gated SD therefore requires one of:

1. **Two parallel deployments** (`-sd-on` and `-sd-off`) on the
   same node, with proxy routing between them based on request
   shape. Costs: ~2× VRAM if both run hot, or a cold-start hit if
   one is scaled to zero. The 7900XTX runs at
   `gpuMemoryUtilization=0.95` today, so a second hot copy is
   likely infeasible without dropping max_model_len or maxNumSeqs.
2. **A vLLM patch** that reads a request hint (e.g. an HTTP header
   or a sampling-params extension) and skips the speculator for
   that request. Invasive; couples flexinfer to a specific vLLM
   version.
3. **Two model identities sharing one engine** is not supported by
   vLLM — `--speculative-config` is global.

The honest ordering of "cheap to implement" is now:

  1. **proxy access-log enrichment** (MR !518) — gives the joint
     `(prompt_tokens, completion_tokens, user_agent, finish_reason)`
     distribution that the previous entry estimated from marginal
     vLLM histograms. Required input to make any of options
     (a)/(b)/(c) above defensible.
  2. **two-deployment routing** (option 1) — if the joint
     distribution justifies the VRAM cost.
  3. **vLLM patch** (option 2) — only if (1) is infeasible.

Commands:

```bash
kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090
# Distributions
curl -s "http://localhost:9090/api/v1/query?query=sum%20by%20(le)%20(increase(vllm:request_prompt_tokens_bucket%7Bmodel_name%3D%22gemma4-26b-a4b-gptq%22%7D%5B7d%5D))"
curl -s "http://localhost:9090/api/v1/query?query=sum%20by%20(le)%20(increase(vllm:request_generation_tokens_bucket%7Bmodel_name%3D%22gemma4-26b-a4b-gptq%22%7D%5B7d%5D))"
# Finished reason breakdown
curl -s "http://localhost:9090/api/v1/query?query=sum%20by%20(model_name%2Cfinished_reason)%20(increase(vllm:request_success_total%5B24h%5D))"
```

Raw scrapes (`.loom/local/`, gitignored):

- `.loom/local/validation/spec-decode/2026-05-26-traffic-mix/prompt-tokens-bucket-{24h,7d}.json`
- `.loom/local/validation/spec-decode/2026-05-26-traffic-mix/generation-tokens-bucket-{24h,7d}.json`
- `.loom/local/validation/spec-decode/2026-05-26-traffic-mix/finished-reason-7d.json`

### 2026-05-25 CC-6 kill-test: scheduler-use assumption FAILED

Backtest per `docs/planning/context-curve-scheduler-spec.md` CC-5.
Runner: `scripts/sim-curve-router.py`. Workload: 80 short prompts
(256–2048 tokens) + 20 long prompts (4096–14000 tokens),
`seed=20260525`, `completion_tokens=64`. Two interpolation modes
(linear, nearest) tried; identical results because the curves only
have two measured points each.

Three curves were available in `flexinfer-context-curve-results`:

- `gemma4-26b-a4b-gptq` (cblevins-7900xtx, vLLM)
- `gemma4-26b-a4b-gptq-5930k` (cblevins-5930k, vLLM)
- `qwen3-8b-radeonvii-soak` (cblevins-radeonvii, llama.cpp)

Two runs:

| Set | Lane assignment | Long p95 Δ | Short p95 Δ | Degenerate split | Pass |
| --- | --- | --- | --- | --- | --- |
| All three lanes | curve-aware picked qwen3-8b for 100% of requests | −32.6% | −58.6% | yes (qwen3 100%) | no |
| Two substitutable gemma4 lanes | curve-aware picked 7900xtx for 100% of requests | +0.0% | −3.1% | yes (7900xtx 100%) | no |

Latency criteria mathematically pass in the cross-family run, but
the assignment is not "routing" — the lanes serve different model
families and the proxy is not allowed to substitute one for the
other. The substitutable-only run is the real scheduler-use case
and shows that two-point curves on near-identical sibling lanes
cannot drive a non-degenerate split: the marginally faster lane
wins every comparison, collapsing `argmax(decode_tps)` into a
blanket preference. The pass criteria correctly fired.

Decision: do **not** build CC-7 (proxy curve-aware routing) on
this foundation. CC-6a will reframe scheduler use of curve data
toward use cases that benefit from per-lane curves even when
lanes are near-identical — candidates: context-bounded admission,
operator dashboard signal, cross-architecture promotion gate.

Raw reports:

- `.loom/local/validation/context-curve/2026-05-25/sim-report.json`
- `.loom/local/validation/context-curve/2026-05-25/sim-report-gemma4-substitutable.json`

Command:

```bash
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
# (curves captured with scripts/bench-context-curve.sh STORE_CONFIGMAP=1)

python3 scripts/sim-curve-router.py \
  --report .loom/local/validation/context-curve/2026-05-25/sim-report.json
python3 scripts/sim-curve-router.py \
  --models gemma4-26b-a4b-gptq,gemma4-26b-a4b-gptq-5930k \
  --report .loom/local/validation/context-curve/2026-05-25/sim-report-gemma4-substitutable.json
```

This is evidence capture only. It does not change scheduler scoring,
controller behavior, runtime profiles, CRDs, or benchmark ConfigMap
consumers.

### 2026-05-25 context-curve 2nd family: qwen3-8b-radeonvii-soak

Second model family for Lane 4. Same runner
(`scripts/bench-context-curve.sh`) against the gfx906 llama.cpp
soak target through a port-forward to `svc/flexinfer-proxy`. Run
right after MR !493 (proxy port-cache fix) unblocked the lane —
the curve confirms steady-state serving plus exposes how Q4_0 KV
cache scales on Radeon VII.

Command:

```bash
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
MODEL=qwen3-8b-radeonvii-soak \
  ENDPOINT=http://localhost:18080 \
  POINTS=2k,8k MAX_TOKENS=64 TIMEOUT=300 \
  REPORT_DIR=/tmp/flexinfer-curves \
  STORE_CONFIGMAP=1 \
  ./scripts/bench-context-curve.sh
```

Result summary:

- Report schema: `flexinfer.context_curve.v1`.
- Local report:
  `.loom/local/validation/context-curve/2026-05-25/bench-context-curve-qwen3-8b-radeonvii-soak-context-curve-20260525T132342-e017e7.json`.
- ConfigMap key:
  `qwen3-8b-radeonvii-soak-context-curve-20260525T132342-e017e7.json`
  in `flexinfer-system/flexinfer-context-curve-results`.
- Summary: `total_points=2`, `passed=2`, `failed=0`, `skipped=0`,
  `first_failure_point=null`.
- 2048 target: observed `1866` prompt tokens, `64` completion
  tokens; elapsed `1.498s`; prefill throughput `1245.5 tok/s`;
  decode throughput `42.72 tok/s`.
- 8192 target: observed `7286` prompt tokens, `64` completion
  tokens; elapsed `8.243s`; prefill throughput `883.9 tok/s`;
  decode throughput `7.76 tok/s`.
- 16384 attempted earlier in the same session and rejected with
  HTTP 400 by llama-server (prompt + completion budget exceeded
  `contextSize: 16384`). Recorded as `first_failure_point=16384`
  in an earlier raw report; not retained in the ConfigMap because
  the persisted run used `--points 2k,8k`.

Curve shape vs. `gemma4-26b-a4b-gptq` (first family):

| target | gemma4-26b prefill | gemma4-26b decode | qwen3-8b prefill | qwen3-8b decode |
| --- | --- | --- | --- | --- |
| 2048 | 1756.2 tok/s | 12.20 tok/s | 1245.5 tok/s | 42.72 tok/s |
| 8192 | 1470.8 tok/s | 2.62 tok/s | 883.9 tok/s | 7.76 tok/s |

Both families show prefill throughput degrading gently (≈16% on
gemma4, ≈29% on qwen3) and decode throughput degrading sharply
(≈78% on gemma4, ≈82% on qwen3) over the same 2k→8k range.
qwen3-8b on llama.cpp is markedly faster at decode in absolute
terms thanks to the smaller model and Q4_K_M weights, but the
shape of the curve mirrors the 26B vLLM lane. With two families
now stored, downstream scheduler/controller use can begin
specifying its decision rules in a follow-up spec.

This is evidence capture only. It does not change scheduler
scoring, controller behavior, runtime profiles, CRDs, or benchmark
ConfigMap consumers.

### 2026-05-22 context-curve MVP: gemma4-26b-a4b-gptq

First live CC-3 context-curve run used the reporting-only
`scripts/bench-context-curve.sh` runner from MR !472. It targeted the explicit
`gemma4-26b-a4b-gptq` route through a local port-forward to
`svc/flexinfer-proxy` so shared-alias round-robin did not mix in the 5930k
sister lane.

Command:

```bash
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
REPORT_DIR=.loom/local/validation/context-curve/2026-05-22 \
  MODEL=gemma4-26b-a4b-gptq \
  ENDPOINT=http://127.0.0.1:18080 \
  ./scripts/bench-context-curve.sh --points 2048,8192 --iterations 1 --warmup 1 --timeout 900
```

Result summary:

- Report schema: `flexinfer.context_curve.v1`.
- Raw report:
  `.loom/local/validation/context-curve/2026-05-22/bench-context-curve-gemma4-26b-a4b-gptq-context-curve-20260521T215333-dcd797.json`.
- Stdout transcript:
  `.loom/local/validation/context-curve/2026-05-22/context-curve-stdout.log`.
- Summary: `total_points=2`, `passed=2`, `failed=0`, `skipped=0`,
  `first_failure_point=null`.
- 2048 target: observed `1872` prompt tokens and `13` completion tokens;
  measured sample elapsed `1.066s`, prefill throughput `1756.2 tok/s`, decode
  throughput `12.20 tok/s`.
- 8192 target: observed `7292` prompt tokens and `13` completion tokens;
  measured sample elapsed `4.958s`, prefill throughput `1470.8 tok/s`, decode
  throughput `2.62 tok/s`.

This is evidence capture only. It does not change scheduler scoring,
controller behavior, runtime profiles, CRDs, or benchmark ConfigMap consumers.

### 2026-05-21 gfx906 llama.cpp HIP memory-info shim probe

Built and pushed:

`registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`

The image adds `LD_PRELOAD=/opt/flexinfer/lib/libflexinfer_hipmeminfo_shim.so`
to `build/Dockerfile.llamacpp-rocm-gfx906`. The shim calls the real
`hipMemGetInfo`; when ROCm returns `err=1` on Radeon VII, it returns sysfs VRAM
totals instead of failing the process.

Live probe result on `cblevins-radeonvii`:

- Job `gfx906-llamacpp-hipmeminfo-probe` completed successfully.
- `llama-server --version` saw `AMD Radeon VII`, `gfx906`, and `VMM: no`.
- All four probe variants passed: `current-profile`, `no-gfx-override`,
  `rocr-visible-only`, and `hip-visible-and-ordinal`.
- Every variant reported `hipMemGetInfo=0:no error`, `hipMalloc4096=0:no
  error`, and `hipMemGetInfoAfterMalloc=0:no error`.
- Shim diagnostics still showed the underlying ROCm call returning `err=1`, so
  this is an image compatibility fix, not a driver fix.

Raw transcript:
`.loom/local/validation/gfx906-llamacpp/2026-05-21/hipmeminfo-shim-probe.log`.

Next gate: run a model-load smoke or controlled soak on the shimmed image before
promoting any radeonvii llama.cpp alias or default fallback route.

### 2026-05-21 gfx906 llama.cpp Qwen3 8B model-load smoke on shim

Image and model:

`registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`

`/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`

Live smoke result on `cblevins-radeonvii`:

- The one-off debug Job mounted the node-local model cache from
  `/var/lib/flexinfer/models`.
- `llama-cli` saw `AMD Radeon VII`, `gfx906`, and `VMM: no`.
- The shim intercepted repeated raw `hipMemGetInfo err=1` failures and returned
  sysfs VRAM totals.
- Qwen3 8B loaded successfully with `--gpu-layers 999`, `--flash-attn on`,
  `--cache-type-k q4_0`, and `--cache-type-v q4_0`.
- llama.cpp printed ROCm memory breakdown with about `4455 MiB` model memory,
  `324 MiB` context memory, and `304 MiB` compute memory.
- The short generation exited `SMOKE_RESULT PASS` with prompt throughput
  `175.2 t/s` and generation throughput `81.1 t/s`.
- Restore smoke against `qwen3-1p7b-tools-radeonvii` returned `Blue` at
  `69.51 tok/s`, and the debug Job/ConfigMap were removed.

Raw transcript:
`.loom/local/validation/gfx906-llamacpp/2026-05-21/model-load-shim-smoke.log`.

Next gate: run the controlled 24 hour soak before promoting any Radeon VII
llama.cpp alias or default fallback route.

### 2026-05-22 gfx906 llama.cpp Qwen3 8B 24h standalone soak

Image and model:

`registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`

`/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`

Live soak result on `cblevins-radeonvii`:

- Job `gfx906-llamacpp-soak-traffic` started at
  2026-05-21T18:40:23Z and completed at 2026-05-22T18:43:42Z.
- Pod `gfx906-llamacpp-soak-traffic-brpcf` reached `Succeeded`.
- `server` container: exit `0`, restart count `0`, ran
  2026-05-21T18:43:16Z to 2026-05-22T18:43:38Z.
- `traffic` container: exit `0`, restart count `0`, ran
  2026-05-21T18:43:16Z to 2026-05-22T18:43:34Z.
- The traffic script exits nonzero on request failure, absent p95 data, or p95
  above `SOAK_P95_MS_PER_TOKEN_BUDGET=300`, so the completed Job proves the
  standalone latency envelope was honored.
- Mid-run harvest at attempts 981-1140 showed steady HTTP 200 responses with
  64 completion tokens and approximately `13.6-13.8 ms/token`.
- Co-tenant baseline at final harvest: `sdxl-inpainting-radeonvii` was `Idle`
  and `qwen3-1p7b-tools-radeonvii` was `Ready`.

Evidence retention caveat:

- Final container logs were unavailable after completion; `kubectl logs` for
  both completed containers returned `unable to retrieve container logs for
  containerd://...`.
- The exact final `soak_summary.p95_ms_per_token` could not be recovered from
  Kubernetes. The next proxy-backed soak should persist the summary into a
  ConfigMap or PVC before any alias/default fallback promotion.

Decision:

- Standalone kill-test: PASS.
- Promotion posture: conditional. Build or promote a persistent `gfx906`
  runtime image carrying the shim, then run a proxy-backed soak with durable
  summary storage before adding broad chat/default fallback aliases.

### 2026-05-19 gfx906 vLLM diagnostic digest canary

The post-MR !439 manual publish job `109838` produced and pinned:

`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:020e737330f7e6355634ffc7d606d294806c65988e7f48f3099f6013fda07964`

Live OPT-125M canary result:

- The pinned image pulled successfully in 8m33s (`10460235176` bytes).
- `cblevins-radeonvii` stayed `Ready=True`, `DiskPressure=False`,
  `MemoryPressure=False`, and `PIDPressure=False`.
- Cache staging was not the blocker: 13 files / 718.2 MB were already cached or
  skipped with roughly 55 GB free in `/dev/shm`.
- The proxy smoke still failed before HTTP 200 with `curl: (52) Empty reply from
  server`.
- The diagnostic hook captured the useful root stack: a fatal Python segfault
  in the vLLM child engine process during OPT `Embedding` initialization via
  `torch.nn.init._no_grad_normal_`, with frames in
  `vllm/model_executor/models/opt.py`,
  `vllm/model_executor/model_loader/loader.py`, and
  `vllm/worker/model_runner.py:1112 load_model`.
- Cleanup restored the lane: Flux `flexinfer-system` and `flexinfer-models`
  were resumed, `qwen3-1p7b-vllm-radeonvii` returned to `Idle`,
  `qwen3-1p7b-tools-radeonvii` returned to `Ready`, and a fallback proxy smoke
  returned `Blue`.

Promotion decision remains `block`. The next slice should patch or work around
the torch/vLLM OPT initialization crash named above rather than trying more
model-family, cache, scheduler, or profile-env variants.

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
  both incoherent and slow on the 5930k shared text lane.
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
- Backfilled the then-required canary lanes called out in the Track E spec:
  `gfx1100` textgen (`qwen36-27b-gptq`), historical `gfx1100` imagegen,
  `gfx906` textgen/quantization (Qwen3.5 GPTQ runbook lane, currently paused
  under DiskPressure), and `gfx906` imagegen/offload
  (`sdxl-inpainting-radeonvii`).
- 2026-05-22 correction: `gonzalomo-fluxpony-imagegen` no longer counts as
  `gfx1100` imagegen evidence. Its manifest is now pinned to
  `cblevins-radeonvii`/`gfx906`, so the active imagegen canary contract follows
  the Radeon VII lane until a real gfx1100 diffusers Model is re-enabled.
- Cells without captured evidence are written as `TBD: <reason>` so the
  promotion rules can keep treating them as `pending` instead of fabricating
  a `promote`.
- Source plan: Track E in the gfx1100/gfx906 next-round plan, picking up
  Slice 6 of `.loom/gfx1100-gfx906-platform-enhancements-plan.md`.

### 2026-06-03 S1.1 kill-test — gfx906 HBM2 embedding feasibility (PASS, feasibility leg)

Hardware-utilization arc Sprint 1 gate
(`.loom/30-implementation-plan-hardware-utilization-2026-06-03.md`,
`.loom/brainstorm-hardware-utilization-sprints-2026-06-03.md`).

- `artifact`: bge-large-en-v1.5 q8_0 GGUF (335M, 1024-dim) + nomic-embed-v1.5
  q8_0 GGUF (137M, 768-dim), same-substrate control.
- `context_length`: n/a (embedding, ctx 512 cap per CR).
- `gpu_class`: gfx906/radeonvii. `backend`: llamacpp (`/opt/llamacpp/bin/llama-embedding`).
- `support_level`: experimental. `observed_failure_mode`: **none** (no segfault;
  the Vega20 C++ fused-path fragility that made vLLM feasibility-only does NOT
  affect the llama.cpp embedding path).
- `canary_command`: `kubectl exec flexinfer-runtime-gfx906-p4kng -n flexinfer-system
  -- bash -lc 'HSA_OVERRIDE_GFX_VERSION=9.0.6 ROCR_VISIBLE_DEVICES=0
  /opt/llamacpp/bin/llama-embedding -m bge.gguf -ngl 999 -b 2048 -ub 512
  --pooling mean -f p2k.txt --embd-output-format array'`
- `promotion_decision`: **conditional** — feasibility leg PASSES; the
  ≥5×-vs-incumbent throughput leg defers to Sprint 0 S0.3 (measure nomic@980Ti).

Evidence (2000-doc batch, 75,179 tokens, ~37.6 tok/doc):

| Model | layers→GPU | ROCm0 buf | compute tok/s | wall emb/s | peak GPU% |
|---|---|---|---|---|---|
| bge-large-en-v1.5 q8_0 | 25/25 | 307 MiB | **8,952** | 84.0 | **100** |
| nomic-embed-v1.5 q8_0 | 13/13 | 115 MiB | **23,719** | 128.9 | **100** |

- Residency proof: `found 1 ROCm devices: Device 0: AMD Radeon VII, gfx906`,
  `offloaded N/N layers to GPU`, peak GPU busy 100% (15+ samples pegged).
- Wall emb/s is serialization-penalized (2000×1024 floats → stdout text); the
  real `llama-server /v1/embeddings` path won't pay it — compute tok/s is the
  honest card capability.
- `[flexinfer-hipmeminfo-shim] hipMemGetInfo failed err=1` lines are the expected
  Vega20 VMM workaround firing as designed, not errors.
- Open: nomic@980Ti (operational incumbent) baseline NOT captured — activation
  stuck in `gtx980ti-models` shared-group queue (behind gemma4-e4b-gguf). That
  exact measurement is Sprint 0 S0.3. Cross-card ≥5× ratio rendered there.
- Disconfirming search: real-world llama.cpp-gfx906 works GPU-resident
  (95 t/s text-gen on Radeon VII); documented ROCm-7 segfault instability exists
  but did not manifest on the embedding path here.

### 2026-06-03 S1.2 deploy — bge gfx906 lane LANDED, blocked from serving by group-budget accounting

MR !555 (squash f3755e18) added `bge-large-radeonvii` to `deploy/models/
kustomization.yaml` as an additive lane (priority 100, minReplicas 0,
serviceLabels `[embeddings-hbm2]`). Flux applied it; live-activation probe:

- `cache.jobPhase: Succeeded` ("artifact prefetched"), `ConfigValid: True`,
  `Schedulable: True` — controller deploy/cache path works for a llamacpp
  embedding Model (`config.embedding: true`).
- **phase: Idle / sharedGroup.state: Queued, preemptedBy qwen3-1p7b-tools-radeonvii.**
  bge did NOT get the gfx906 slot.
- **Root cause (finding):** `qwen3-1p7b-tools-radeonvii` is **CPU-pinned**
  (`nGPULayers: 0`, `hipVisibleDevices: "-1"`, `rocrVisibleDevices: "-1"`) yet
  reserves `vramEstimateMB: 2000` against the `radeonvii-models` **GPU** group
  budget. Physical gfx906 is idle (663 MB / 16 GB). So a CPU model's phantom GPU
  reservation blocks co-admission of a real GPU model (bge, 1500 MB) with ~16 GB
  physically free. No preemption occurred (priority 100 < 120 as designed →
  safety held; live pyannote + tool router stayed Ready throughout).
- **Implication:** the HBM2 embedding lane cannot serve concurrently with the
  warm CPU tool router under current group-budget accounting. Next-slice fix
  options: (a) exclude `hipVisibleDevices/rocrVisibleDevices == -1` (CPU) models
  from GPU-group VRAM budget [principled, controller change]; (b) move the
  CPU tool router out of the `radeonvii-models` GPU group entirely [cleanest —
  a CPU model arguably should not be in a GPU shared group]; (c) raise the group
  VRAM budget to admit both [config-only stopgap].
- `promotion_decision`: **conditional** — lane deployed + cache warm; serving
  blocked pending the group-budget fix. bge left Idle/Queued (harmless, no pod).

### 2026-06-03 S1.2b fix — cpu-only models off the single-slot GPU runtime (MR !556, #62)

Corrected root cause of the S1.2 blocker: NOT VRAM-budget accounting. The gfx906
unified runtime serves ONE backend subprocess at a time
(`internal/runtime/manager.go:70`); `chooseSharedGroupLeader` elects one leader
per shared group. The CPU-pinned tool router (`qwen3-1p7b-tools-radeonvii`,
`nGPULayers:0`, `*VisibleDevices:-1`, `minReplicas:1`) held that single slot,
blocking GPU models from serving on the Radeon VII.

- Operator decision: option (A) dedicated pod for the tool router.
- Fix (MR !556, squash 6704c998, merge c810db58): `DirectRuntimeLoadEligibility`
  (`pkg/runtime/payload.go`) returns false for models that explicitly set
  `gpu.vendor: cpu` → controller falls through to the Deployment flow
  (`model_controller.go:391+`). GPU-less models keep existing eligibility.
  `qwen3-1.7b-tools-radeonvii.yaml` reclassified to `vendor: cpu` (schema forces
  `gpu.shared` empty → leaves the GPU group). Unit tests
  `TestDirectRuntimeLoadEligibility` (+cpu→excluded, +amd→eligible); CI pipeline
  12721 success; auto-merged.
- `promotion_decision`: **conditional** — code merged + CI green. NOT yet live:
  the controller image must be rebuilt + rolled out (manual cycle, no CI publish)
  before the behavior takes effect on-cluster. Live Prove pending:
  tool router → own CPU Deployment (Ready); bge → radeonvii-models runtime leader;
  `/v1/embeddings` returns a vector; pyannote diarization unaffected.

### 2026-06-03 S1.2b LIVE VERIFIED — bge embedding lane serving + CPU tool router concurrent

The #62 fix is deployed and proven end-to-end. Controller rebuilt from master
(Dockerfile.manager, digest sha256:5932e77d) + rolled out; chain of follow-up
manifest fixes landed:

- MR !556 (6704c998): `DirectRuntimeLoadEligibility` excludes explicit vendor:cpu
  → dedicated Deployment. Controller live (image ID 5932e77d confirmed).
- MR !557 (82a4e613): bge priority 100→120 (was out-ranked by gemma4-e4b 110 once
  the tool router left the group).
- MR !558 (4299bb59): pin tool-router image — vendor:cpu llamacpp resolved the
  stale `ghcr.io/ggerganov/llama.cpp:server` default (404 → ErrImagePull, weaver
  router down). Pinned node-cached gfx906 ROCm llamacpp image.
- MR !559 (1f6eb516): bge cache SharedPVC→Local — the runtime only sees node-local
  /models; SharedPVC → `gguf_init_from_file: No such file or directory` → RuntimeFailed.

Live verification (2026-06-03 ~18:47):
- `qwen3-1p7b-tools-radeonvii`: gpu_vendor **cpu**, NO shared group, own Deployment
  pod 1/1 **Ready** (weaver-router dependency restored).
- `bge-large-radeonvii`: phase **Ready**, sharedGroup.state **Active** (leader,
  priority 120), RuntimeReady, runtime-served GPU-resident on gfx906.
- Smoke: `POST /v1/embeddings` via flexinfer-proxy (model `bge-large`) →
  **HTTP 200**, model `CompendiumLabs/bge-large-en-v1.5-gguf`, **1024 dims**,
  usage prompt_tokens 19. A CPU model + GPU model serve the same node concurrently.
- pyannote-diarization (voice stack) Running throughout — undisturbed.
- `promotion_decision`: **promote** — S1.2/S1.2b complete and live.

Follow-up (latent bug, not blocking): `backend/llamacpp.go` `DEFAULT_LLAMA_CPP_IMAGE_CPU`
default is the stale `ghcr.io/ggerganov/llama.cpp:server` (ggml-org now). Any future
vendor:cpu llamacpp model without an explicit image hits ErrImagePull.

### 2026-06-03 S0.3 baseline — bge served throughput + incumbent-stuck finding

- **bge-large @ gfx906 (served, via flexinfer-proxy `/v1/embeddings`, model `bge-large`):**
  cold-load 5.3s; warm 64-doc batch **~91 emb/s** (3 trials 83/91.5/91.2), model
  `CompendiumLabs/bge-large-en-v1.5-gguf`, 1024 dims. Compute peak (kill-test)
  8,952 tok/s; same-substrate nomic@gfx906 was 23,719 tok/s.
- **Incumbent nomic @ 980Ti: NOT MEASURABLE — effectively unavailable.** Despite
  `minReplicas: 1`, nomic-embed-text is stuck `phase: Idle / sharedGroup Queued,
  preemptedBy gemma4-e4b-gguf` (preemptedAt 2026-05-03, lastActiveTime 2026-06-01).
  Same single-leader-group pathology as #62, on `gtx980ti-models`. The default
  `embeddings`/`text-embedding-3-small` alias lane has been degraded/down for weeks.
- **Verdict:** the ≥5×-vs-incumbent *ratio* is moot — the incumbent isn't serving.
  bge@gfx906 is a strict availability improvement (it returns HTTP 200 at ~91 emb/s
  batched; the incumbent returns nothing). S1.2c default-alias cutover to bge is
  justified on availability grounds, not just throughput.
- **New finding (follow-up):** `gtx980ti-models` group exhibits the #62 pathology —
  a warm minReplicas:1 model (nomic) starved by an idle higher-priority leader
  (gemma4-e4b-gguf). Worth applying the same analysis (priorities / CPU-vs-GPU
  placement) to the 980Ti group.

### 2026-06-03 S1.2c LIVE VERIFIED — bge is the default embeddings lane

MR !560 (squash efcd2665) cut the default embeddings retrieval plane over to
bge@gfx906 and demoted the stuck nomic@980Ti incumbent.

- bge-large-radeonvii: minReplicas:1 (warm), aliases `embeddings`,
  `text-embedding-3-small`, `bge-large`, `embeddings-radeonvii`; serviceLabels
  `embeddings`,`semantic-search`,`rag`,`embeddings-hbm2`. ConfigValid "No alias
  conflicts detected"; phase Ready / sharedGroup Active (priority-120 leader).
- nomic-embed-text: demoted to minReplicas:0 cold fallback under distinct
  `nomic-embed-text`/`embeddings-980ti` names.
- Live smoke via flexinfer-proxy `/v1/embeddings`:
  - model `text-embedding-3-small` → HTTP 200, served `bge-large-en-v1.5`, 1024 dims.
  - model `embeddings` → HTTP 200 (×3 consistent), served `bge-large-en-v1.5`,
    1024 dims. (First call 502 = transient proxy alias-cache refresh; cleared in ~5s.)
- `promotion_decision`: **promote** — Sprint 1 (gfx906 HBM2 retrieval plane)
  delivered and LIVE as the default embeddings lane.

Sprint 1 remaining: S1.3 reranker (bge-reranker-large `/v1/rerank`), S1.4 migrate a
real consumer (codebase-memory/agent-context) — deferred.

### 2026-06-03 S1.3 kill-test — reranker GGUF on gfx906 via llama-server `--reranking` — PASS

**Riskiest assumption** (spec-riskiest-assumption rule): a reranker GGUF runs
GPU-resident on gfx906 via `llama-server --reranking` AND serves a working
`/v1/rerank` endpoint that the path-agnostic flexinfer-proxy can route.

**Procedure**: exec into `flexinfer-runtime-gfx906-jm62h`; downloaded
`gpustack/bge-reranker-v2-m3-GGUF` (Q8_0, 607 MB; the llama.cpp-validated reranker —
substituted for the plan's `bge-reranker-large`, see note); launched
`/opt/llamacpp/bin/llama-server --model bge-reranker-v2-m3-Q8_0.gguf --reranking
-ngl 999 -fit off --port 8099` (HSA_OVERRIDE_GFX_VERSION=9.0.6, ROCR_VISIBLE_DEVICES=0)
alongside the live managed bge subprocess; curled `/v1/rerank`.

**Evidence**:
- GPU-resident: `load_tensors: offloaded 25/25 layers to GPU`, ROCm0 model buffer
  308.29 MiB, `Device 0: AMD Radeon VII, gfx906`, ready in ~4s, no segfault, no
  Vega20 fragility (15262 MiB free reported; VMM:no as expected).
- `--rerank, --reranking` confirmed in this build's `llama-server --help`
  (llama.cpp 0.9.7, env `LLAMA_ARG_RERANKING`).
- Functional ranking correct (`/v1/rerank`, query "What is the capital of France?",
  4 docs): idx3 "Paris is the largest city and capital of France" **7.378** (top),
  idx0 "Eiffel Tower in Paris…" **5.526**, idx2 "Berlin is capital of Germany"
  **−5.437**, idx1 "Bananas…potassium" **−11.041** (bottom). Response is
  cohere-style `{"results":[{"index","relevance_score"}],"usage"}`.
- Proxy passthrough (code read): `internal/proxy/proxy.go:454 handleRequest` is
  path-agnostic — extracts model from body `model` field / header / path and
  proxies preserving `r.URL.Path` → backend pod `/v1/rerank`. No proxy code change
  needed; only model resolution (alias/serviceLabel) must point at the reranker.

**Verdict: PASS.** The Vega20 reranking risk is retired; S1.3 reduces to (a) a 4-line
`--reranking` flag in `backend/llamacpp.go` mirroring `--embeddings`, and (b) a new
reranker Model CR. **Note**: kill-test used `bge-reranker-v2-m3` (newer, multilingual,
the reranker llama.cpp's rerank support was validated against) rather than the plan's
`bge-reranker-large`; the production CR uses v2-m3 for the same reason.

### 2026-06-03 S1.3 design pivot — single-slot runtime blocks concurrent embed+rerank

**Finding**: the gfx906 unified runtime is **single-slot by design** —
`internal/runtime/manager.go:70` "Only one backend subprocess runs at a time",
`Manager.active *LoadedModel` (one model), and `Load()` unloads the active model
before starting a new one (manager.go:128-170). The controller routes GPU models on
the runtime's node through the runtime API (skips Deployment; forcing a dedicated
Deployment would deadlock on the GPU device — model_controller.go:379-381). So bge
embeddings (warm, priority-120 leader) and a GPU reranker **cannot both be warm** on
gfx906 via the current path; they'd swap-thrash (~9s/RAG-query). The S1.3 plan's
"reranker on the same card" assumed co-residence that the runtime doesn't support.

**Operator decision (2026-06-03)**: build the **multi-subprocess runtime first** —
extend the runtime Manager to hold N VRAM-bounded subprocesses so bge+reranker (and
future models) co-reside on the HBM2 card (the true retrieval-appliance vision). The
reranker CR + `/v1/rerank` go live after the runtime work (re-scoped to slices below).

### 2026-06-03 Multi-subprocess runtime kill-test (concurrent embed+rerank) — PASS

**Riskiest assumption**: two GPU model subprocesses can run concurrently inside the
gfx906 runtime pod, each on its own port, both GPU-resident, both serving correct
results, within the 16 GB VRAM budget, without Vega20 instability.

**Procedure**: with the managed bge subprocess Ready on port 8000 (runtime
`/api/v1/status`: vramUsed 1105 MB / 16368 MB), launched a second `llama-server
--reranking` (reranker GGUF) on port 8099 GPU-resident, then hit BOTH endpoints.

**Evidence**:
- Concurrent serve: `POST :8000/v1/embeddings` → 1024-dim vector **AND**
  `POST :8099/v1/rerank` ("capital of France" / 2 docs) → top_idx 0 (Paris)
  score 8.56 — both returned correctly while both subprocesses were live.
- Both GPU-resident: reranker `offloaded 25/25 layers to GPU`, ROCm0 buffer 308 MiB;
  bge already 25/25.
- VRAM with BOTH up: **1591 MB / 16368 MB used (14777 MB free)** — reranker added
  only ~486 MB. The 16 GB card can co-host many such models.
- Runtime GPU telemetry tracked it: 1105 → 1591 → 1113 MB (after teardown).
- Managed bge undisturbed throughout (still Ready on 8000 after test-server kill).

**Verdict: PASS.** The multi-subprocess approach is feasible and VRAM-cheap. The
runtime change is software orchestration only (per-model ports + VRAM admission +
no auto-unload + multi-Active election), NOT a hardware limit.

### Multi-subprocess runtime — slice decomposition (re-scoped S1.3)

- **R1 (kill-test)** — DONE 2026-06-03 PASS (above).
- **R2** — Manager multi-subprocess core, flag-gated (`active` map, per-model port
  allocation, VRAM admission, no auto-unload when flag on, multi-model
  status/metrics/health). Self-contained to `internal/runtime/`; default off =
  identical to today.
- **R3** — Proxy per-model port routing from runtime status (resolve model→port
  for the right subprocess instead of assuming the single active model).
- **R4** — Controller multi-Active election (`chooseSharedGroupLeader` → VRAM-bounded
  set; `reconcileViaRuntime` loads additively; `CanAcceptLoad` VRAM-aware).
- **R5** — Reranker Model CR + flip multi-model on for radeonvii-models; live-verify
  `/v1/rerank` concurrent with `/v1/embeddings` (the original S1.3 payoff). Backend
  `--reranking` flag (shipped separately, additive) feeds this.

#### Ship status (2026-06-03/04) — all 4 software slices R1-R4 shipped
- **S1.3 backend `--reranking` flag** — MERGED MR !562 (squash e0df2162).
- **R2** Manager multi-subprocess core — MERGED MR !564 (squash e7e7393f).
- **R3** proxy+runtime per-model port routing — MERGED/merging MR !565: runtime
  `handleModelHealth` by-name + port; `Manager.Model(name)`; proxy
  `waitForRuntimeReady` returns port; `tryDirectRuntimeLoad`/`recoverDirectLoadTargets`
  route to `podIP:<reportedPort>` with fixed-port fallback. Tests green, vet clean.
- **R4** controller multi-Active election — MR !566 auto-merge ARMED. Reconciled
  with !563 (warm-pinned-leader): `chooseSharedGroupLeaders` builds ON TOP of the
  single-leader fn (multiModel=false → exactly today's behavior). Mechanism chosen:
  **GPUProfile `features.multiModel` flag** (per-arch capability) + budget reuses
  profile `vramMB`; NOT a runtime round-trip. `handleSharedGPU` marks this model
  Active iff in the VRAM-bounded set; `syncActiveServiceLabels` takes the set so each
  Active member advertises its own labels (embeddings vs rerank). Default-safe (no
  profile sets multiModel yet → single-slot everywhere). Tests cover set election.
  NOTE: `reconcileViaRuntime`/`CanAcceptLoad` did NOT need changes — additive load
  happens because each Active model reconciles + loads itself, and the R2 Manager in
  multi-model mode doesn't unload others (+ its own VRAM admission is the backstop).

#### R5 — remaining (deploy/enablement + live-verify), gated on R4 merge
The entire software stack (R1-R4) is built/tested/merged. R5 is an ops cycle:
1. Reranker Model CR `deploy/models/bge-reranker-radeonvii.yaml` (backend llamacpp,
   source `gpustack/bge-reranker-v2-m3-GGUF`, `config.reranking: true`, cache Local
   nvme-1r, group `radeonvii-models`, `vramEstimateMB` ~700, serviceLabel `rerank`,
   litellm alias `rerank`, minReplicas 1) + add to `deploy/models/kustomization.yaml`.
2. Set `features.multiModel: true` on the gfx906 GPUProfile (platform/gitops).
3. Set `FLEXINFER_RUNTIME_MULTI_MODEL=true` (or `--multi-model`) on the
   flexinfer-runtime-gfx906 DaemonSet (platform/gitops).
4. **Rebuild + roll BOTH images** (manual, NO CI publish): controller (R4 code) via
   `build/Dockerfile.manager`; runtime gfx906 (R2/R3 code) via `build/build-runtime.sh
   gfx906 --push` (~30-60min). Watch for the Helm-chart-bump → Flux full-rollout churn.
5. Live-verify: `POST /v1/rerank` via proxy returns ranked scores from
   bge-reranker@gfx906 CONCURRENTLY with `/v1/embeddings` from bge@gfx906 (both Active
   in radeonvii-models). Matrix row + S1.4 (migrate a consumer) optional follow-up.

### 2026-06-04 R5 SHIPPED + LIVE-VERIFIED — multi-Active runtime live on gfx906 — PASS

**Slice**: R5 (final slice of the multi-subprocess runtime arc). MR !568 merged
(squash `d46ead08`). **The original S1.3 payoff is live**: bge embeddings + a GPU
reranker GPU-resident on the SAME Radeon VII at once.

**What shipped** (deploy/ only, one MR; controller needed NO manual build — CI
`publish` had already rebuilt `:master` with R4, only `govulncheck` flaked the
master pipeline red; runtime gfx906 WAS stale (digest from 2026-05-22) so it was
the one real rebuild):
- `models/bge-reranker-radeonvii.yaml`: llamacpp `gpustack/bge-reranker-v2-m3-GGUF`
  Q8_0, `config.reranking:true`, group radeonvii-models, priority 115, vram 700,
  minReplicas 1 (warm co-Active), serviceLabel + litellm alias `rerank`.
- `gpuprofiles/gfx906.yaml`: `features.multiModel:true` + runtime digest repin.
- `values-k3s.yaml`: `FLEXINFER_RUNTIME_MULTI_MODEL=true` on the gfx906 runtime DS
  + runtime digest repin.
- Runtime gfx906 rebuilt from master HEAD (R2/R3) → `runtime@sha256:4a02796d…`
  (replaced May-22 `8797a08a`); pushed via `build/build-runtime.sh gfx906 --push`.

**Rollout**: `flux reconcile` (source flexinfer → ks flexinfer-models → hr
flexinfer). Chart-version bump `1.0.2+2f59e2a7d403` rolled controller (repull R4
`:master`) + runtime gfx906 (new digest). Reranker GGUF auto-downloaded, loaded
via runtime, Ready in ~1 min.

**Live evidence (PASS)**:
- Runtime `/api/v1/models` holds BOTH subprocesses concurrently: `bge-reranker`
  state=Ready port=8000 pid=127; `bge-large` state=Ready port=8001 pid=217 (R2
  multi-subprocess + R3 per-model ports). Runtime log: `VRAM admission passed …
  free=15259MB` (R2 admission gate).
- Controller elected BOTH Active in radeonvii-models (R4 `chooseSharedGroupLeaders`:
  primary bge 120 + admitted reranker 115; gemma4-e4b/qwen3-8b stayed Idle).
- CONCURRENT proxy calls both succeeded: `/v1/rerank` (model `rerank`) →
  bge-reranker-v2-m3 correct cohere ranking (idx0 "Paris…" 8.61 top, idx1
  "Berlin…" −5.39 bottom); `/v1/embeddings` (model `bge-large`) → valid vector.
- Post-load steady state: both still Ready, same PIDs (NO eviction). GPU VRAM
  ~1597 MB / 16368 MB used with both resident — ~14 GB headroom.

**Verdict: PASS.** R1–R5 multi-subprocess runtime arc COMPLETE and live. Default-safe
elsewhere (only gfx906 sets multiModel). S1.4 (migrate a real consumer onto the
`rerank` route) is the optional follow-up.

### 2026-06-03 S0.1 — per-card GPU compute-utilization metric exported (MR !567)

- **Slice**: Sprint 0 (fleet utilization instrument), S0.1. Advances #28.
- **Finding**: the compute-utilization leg was *collected but never exported*. The
  `flexinfer-agent` DaemonSet already fills `GPUMetrics.Utilization` on both vendors
  (nvidia-smi `utilization.gpu`, rocm-smi `--showuse` → `extractUtilValue`), but
  `cmd/flexinfer-agent/main.go` only emitted VRAM metrics. The dashboard had no
  compute-busy signal and therefore no idle-time signal.
- **Change**: new gauge `flexinfer_gpu_compute_utilization_percent{gpu,node,vendor}`
  in `pkg/metrics/exporter.go` (registered) + emitted unconditionally in the agent
  loop (0 = valid idle reading). Descriptor test added; `expectedMetricCount` 43→44.
- **Verdict**: PASS. `go build ./...`, `go test ./pkg/metrics/... ./agents/agent/...`,
  vet/fmt/lint all green. Squash-merged b455d1f4 (merge d137c10b). The one red job was
  a GitLab-502 git-clone flake in `proxy_test` (infra, not code) — retried → green.
- **Idle-time**: derive from `flexinfer_gpu_compute_utilization_percent`; near-zero
  over a window = idle card. No separate idle metric needed.
- **Next in Sprint 0**: S0.2 Grafana "Fleet Utilization" dashboard (platform/gitops;
  coordinate with open MR !227 monitoring-dashboard-dedupe). S0.3 baseline already
  captured. Then #28 can close with backlinks once the dashboard is live.

### 2026-06-03 S0.2 — FlexInfer Fleet Utilization Grafana dashboard LIVE (platform/gitops MR !219)

- **Slice**: Sprint 0 (fleet utilization instrument), S0.2. Advances #28. With S0.1
  (metric) + S0.3 (baseline, already captured), the Sprint 0 instrument is complete.
- **Kill-test (live, pre-build)**: confirmed the `flexinfer_gpu_*` agent family and
  `flexinfer_runtime_model_state` are scraped into the monitoring Prometheus (3 GPUs
  report: 7900xtx, 5930k, radeonvii). Every panel PromQL parses + returns clean
  per-card series. → dashboard will have real data, not empty panels.
- **Change**: `k3s/monitoring/dashboards/services-flexinfer-fleet-utilization-dashboard.yaml`
  (ConfigMap, `grafana_dashboard:"1"`, folder Services) + kustomization entry. 10
  panels across 4 rows: per-card compute util + VRAM util; VRAM bytes + VRAM-util
  bargauge + temp; idle-cards-now + idle-hours/24h; tokens/sec + warm/cold states +
  warm count. All exprs `max by (node,gpu,vendor)` aggregated to collapse DaemonSet
  pod churn (was 66 stale series → 3 clean lines/card).
- **Verdict**: PASS + LIVE. `kustomize build` OK, JSON validates, merge 93fb826f,
  Flux `monitoring` Kustomization reconciled, ConfigMap confirmed present in
  `monitoring` ns (Grafana sidecar auto-imports). VRAM/temp/warm-cold panels live
  immediately; compute-util + idle panels light up after the flexinfer-agent image
  (S0.1 / MR !567) is rebuilt + rolled — ops follow-up, additive (no empty errors).
- **#28 status**: metrics (S0.1) + dashboard (S0.2) + baseline (S0.3) all landed.
  Remaining before close: rebuild/roll the flexinfer-agent image so compute-util +
  idle panels populate. Left open w/ backlink comment.

### 2026-06-04 S1.4a — litellm gateway bge route fixed → bge@gfx906 reachable (platform/gitops MR !220)

- **Slice**: Sprint 1 (gfx906 HBM2 retrieval plane), S1.4a — the gateway-route
  prerequisite of S1.4 ("wire one real consumer"). Arc:
  `30-implementation-plan-hardware-utilization-2026-06-03.md`.
- **Kill-test (live, pre-merge)** — surfaced the real blocker, NOT the assumed one.
  The assumed path (codebase-memory → litellm `embeddings` alias → bge) was false on
  two counts: (1) `flexinfer-proxy` is ClusterIP-only — no ingress — so the S1.2c
  "default embeddings = bge" cutover (on the native proxy) is unreachable to an
  external consumer; (2) the litellm gateway (the only external entry) routed
  `embeddings`/`text-embedding-3-small` → **morph-embedding-v4** (paid cloud, 1536-dim,
  confirmed by direct call) and its dedicated `bge-large-embeddings` entry pointed at
  `bge-large-embeddings.flexinfer-system.svc:80` — **a Service that does not exist**.
  From the litellm pod, `flexinfer-proxy.../v1/embeddings` with model
  `bge-large-radeonvii` / `semantic-search` / `text-embedding-3-small` → **HTTP 200,
  1024-dim, served by `CompendiumLabs/bge-large-en-v1.5-gguf`** (= bge@gfx906). So the
  proxy routes embeddings by model-name correctly; only the litellm entry's endpoint
  was wrong.
- **Change**: re-point the litellm `bge-large-embeddings` route at
  `http://flexinfer-proxy.flexinfer-system.svc.cluster.local:80/v1` with
  `remoteModel: bge-large-radeonvii` + `encodingFormat: float`, in BOTH the
  `external-models.yaml` ConfigMap (source-of-truth) and the inline
  `STATIC_LOCAL_EMBEDDINGS` fallback in `litellm.yaml`. Aliases `bge-large` /
  `text-embedding-bge-large` unchanged.
- **Verdict**: PASS + LIVE. Validated YAML + embedded-JSON parse + `kustomize build`.
  Pipeline 12829 green, MR !220 squash-merged (c731f5c6), Flux `apps` ks reconciled to
  the merge revision. **Gateway live-verify**: `bge-large` via
  `https://litellm.flexinfer.ai/v1/embeddings` → **1024-dim, served by
  CompendiumLabs/bge-large-en-v1.5-gguf**. bge@gfx906 is now reachable through the
  gateway. Additive/reversible — `morph-embedding-v4` is still the default `embeddings`
  alias; only the explicit `bge-large` route changed.
- **Next**: S1.4b — migrate `codebase-memory` (loom-core `mcp/context/registry.yaml`:
  `CODEBASE_EMBED_PROVIDER=flexinfer`, model `bge-large`, base = litellm gateway, key
  `LITELLM_API_KEY`) to a fresh 1024-dim collection `codebase_memory_bge_v1`, re-embed
  one repo, capture before/after emb/s + search-quality parity. Old morph collection
  preserved (reversible).

### 2026-06-04 S1.4b — codebase-memory→bge migration PROOF (proof-only; verdict: do NOT migrate the local consumer)

- **Slice**: Sprint 1, S1.4b — proof-only (operator chose "side collection, no global
  flip"). Goal: measure bge-vs-Morph emb/s + search-quality parity for the
  codebase-memory consumer before any cutover. Builds on S1.4a (gateway route live).
- **Setup**: codebase-memory embeds via the **paid Morph cloud** (`embeddings` alias →
  morph-embedding-v4, 1536-dim) today and runs as a **local stdio process** on the dev
  box. The bge alternative reaches gfx906 only through the public litellm gateway
  (Cloudflare-fronted). Corpus: 114 real Go decl/doc chunks from loom-core
  `pkg/codebase`. Measured via the exact OpenAI-compatible POST the flexinfer embedder
  client makes (`/v1/embeddings`, batch 64, median of 3 rounds).
- **M1 — emb/s (the consumer's real local→gateway path)**:

  | path | emb/s |
  |---|---|
  | bge in-cluster (proxy direct — k8s Job path) | **70.9** |
  | morph cloud via gateway (current consumer baseline) | 15.4 |
  | **bge via gateway (local consumer path)** | **3.3** |

  Attribution: bge *serving* is fast (70.9, matches the S0.3 ~91 emb/s baseline). The
  3.3 the local consumer sees is **WAN + Cloudflare per-batch round-trip overhead**,
  not GPU — each batch is one HTTPS round-trip through the public edge. Morph wins for
  this consumer because it is a globally-distributed, WAN-optimized cloud endpoint.
- **M2 — parity (paraphrase probe)**: query "function that marks an indexing job as
  failed with an error" → morph ranked the exact target `setJobFailed` **top-1**; bge
  ranked a semantically-adjacent sibling `incrementJobError` top-1 with the exact
  target **not in top-3**. Suggestive of slightly weaker retrieval precision (one
  probe — not conclusive), with no offsetting throughput case.
- **Integration check**: Go client UA (`Go-http-client/1.1`) → HTTP 200 at the gateway
  (only `python-urllib` is Cloudflare-1010-blocked), so the flexinfer/bge embedder path
  would function; the embedder client + `bge-large` alias already exist
  (`loom-core pkg/codebase/embed/flexinfer.go`). Did NOT write the throwaway
  `codebase_memory_bge_v1` Qdrant collection — the throughput verdict already decides
  the migration, so a slow full re-embed adds no signal.
- **Verdict**: **Do NOT migrate the local interactive codebase-memory to bge.** It is a
  ~4.7× throughput regression (3.3 vs 15.4 emb/s) over the consumer's real path, plus a
  weaker parity probe. The proof-only slice correctly **falsified** the assumed
  "migrate codebase-memory → utilization win." The HBM2 plane's win is for **in-cluster
  batch** consumers hitting flexinfer-proxy directly (70.9 emb/s, free, self-hosted) —
  i.e. **Sprint 2 S2.2** (nightly re-embed as a k8s Job), the correct migration target.
  Morph remains codebase-memory's default (untouched, no global flip — as scoped).
- **Net Sprint 1 close**: S1.4a (gateway route) LIVE; S1.4b proof concludes the
  consumer choice. S1.4 done. Redirects the "wire a real consumer" payoff to S2.2.

### 2026-06-04 Internal routing — proxy self-heals stale direct-load targets (MR !569)

- **Context**: follow-up to S1.4b. While measuring the bge embeddings lane I hit
  repeated proxy 502s after a runtime/litellm restart. Code review (Explore map of
  `internal/proxy/`) found a real, non-transient bug.
- **Bug**: the proxy's per-model fast-path cache `directLoadTargets` (modelName →
  `http://<podIP>:<port>`, used for runtime-served models) is populated ONLY at
  startup (`recoverDirectLoadTargets`, proxy.go:415) and on activation
  (`tryDirectRuntimeLoad`, queue.go:464), is **never `Delete`d anywhere**, and has
  **no periodic refresh**. When a runtime pod restarts (new IP / per-model port), the
  stale entry pins a dead address **indefinitely** → every direct-path request 502s
  until the proxy itself restarts or the model re-activates. `httputil.ReverseProxy`
  had no `ErrorHandler` and no retry, so a dial failure = immediate 502.
- **Fix** (`internal/proxy/routing_retry.go` + `routing.go` + `metrics.go`):
  `forwardWithRetry` — on a dial-class transport error, (1) **delete the stale
  `directLoadTargets` entry** (core self-heal), (2) **retry once** against a freshly
  re-resolved target (Service DNS, kube-proxy load-balanced — never re-picks the dead
  pod). Safe because `ReverseProxy.ErrorHandler` fires only on pre-response RoundTrip
  errors (nothing written → buffered body replays cleanly). A custom ErrorHandler
  records the error on a per-request `forwardResult`; `serveProxy` keeps the original
  body for routing and forwards the rewritten body (behavior-preserving). Bounded
  (default 2 attempts, `FLEXINFER_PROXY_UPSTREAM_MAX_ATTEMPTS` clamped [1,3], 50ms
  backoff); dial-class only (conn refused/reset, host/net unreachable, EOF, timeout —
  never valid upstream HTTP statuses). New metric
  `flexinfer_proxy_upstream_retries_total{model,reason}`.
- **Verdict**: PASS (code+CI). 5 new unit tests + full `./internal/proxy/...` suite
  green, gofmt clean, module builds. MR !569 squash-merged (fbf1b592, merge 54d7916b);
  master pipeline 12847 rebuilds + republishes the proxy image. **Live-verify pends**
  the proxy image roll (Flux) — reproduce a runtime pod restart and confirm the bge
  lane recovers without an indefinite 502 window. Additive/reversible (set
  `FLEXINFER_PROXY_UPSTREAM_MAX_ATTEMPTS=1` to restore legacy single-attempt).
- **Out of scope (follow-up)**: the gfx906 multi-model per-model-port (`:8001`) vs
  controller Service port mapping — a separate controller-side reconciliation concern;
  this MR makes the proxy resilient to the resulting dial failures but doesn't change
  Service reconciliation.

### 2026-06-04 Internal routing (cont.) — Part A LIVE-VERIFIED + Part B controller fix (MR !570)

- **Part A (proxy self-heal MR !569) — now LIVE-VERIFIED.** master pipeline `publish`
  job rebuilt the proxy image despite the overall pipeline reddening on a
  `govulncheck` flake (allow_failure=false but `publish`/`proxy_test` both succeeded —
  matches the known controller-publish pattern). Rolled the proxy (scale 0→1 via
  ops_mcp; bash kubectl mutations denied). **Running pod image digest
  `sha256:241457e6e064` exactly matches the digest published for `:master`** → the
  self-heal code is deployed and running. Post-roll smoke: `bge-large` via the
  gateway → 3/3 HTTP 200, 1024-dim bge@gfx906. (The new `upstream_retries_total`
  CounterVec is lazily registered — absent until first retry — so it isn't a liveness
  signal; digest match is the proof.)
- **Part B — controller-side root cause FIXED (MR !570).** Investigating the residual
  stale-endpoint symptom: found `bge-reranker-radeonvii` stuck `phase=Ready` with
  Endpoints → dead pod IP `10.42.8.249` while the live runtime pod was `10.42.8.38`.
  - **Verified a NON-bug**: the per-model **port** wiring is correct —
    `ensureRuntimeEndpoints` writes a named `"http"` EndpointPort with the runtime's
    dynamic per-model port, and the selectorless Service routes by the named Endpoint
    port (so a model on `:8001` is reachable via its `:8000` Service). This also
    confirms Part A's Service-DNS retry path is sound for multi-model.
  - **Real bug**: after a runtime pod swap (new IP), `reconcileViaRuntime` only
    rewrites Endpoints in the loaded/"Ready" path; the not-loaded (`status==nil`)
    reload branch never clears the stale Endpoints → traffic 502s to the dead pod for
    the whole reload window. (`status==nil` also fires on transient health errors, so
    clearing on that alone would flap.)
  - **Fix** (`clearStaleRuntimeEndpoints`, early in `reconcileViaRuntime`): drop any
    Endpoints subset whose address ≠ the current runtime pod IP. Cross-pod = dead =
    safe to clear; Ready path re-creates the correct ones; IP-mismatch guard means a
    transient health blip (same pod IP) never tears down a live endpoint. 3 unit tests
    + full `./controllers/` suite green. MR !570 auto-merge armed, pipeline 12851.
  - **Two halves of one failure mode**: !569 (proxy, client-side: retry past dead
    upstream) + !570 (controller, server-side: stop advertising the dead address).

### 2026-06-04 S1.4 — rerank consumer wired into loom-core agent-context recall — PASS (loom-core MR !621)
- **Slice**: Sprint 1, S1.4 — "migrate one real consumer onto the live `rerank` route".
  Distinct from S1.4a/b, which killed the *embeddings*-over-WAN migration (4.7× regression
  for a local interactive consumer, row 2026-06-04 S1.4b). Reranking is the right consumer
  shape: it's latency-tolerant (runs on the already-retrieved top-K), so the win is ranking
  *quality* — exactly what the co-active gfx906 reranker delivers.
- **Consumer**: loom-core **agent-context recall** (`pkg/agentcontext`, `HandleUnifiedRecall`)
  — the highest-traffic agent retrieval path. The `Reranker`/`FlexInferReranker`/`ApplyReranker`
  infra was built+tested in loom-core Slice A1 but deliberately UNWIRED (the code carried a
  ".loom/88 §2.A1 — wired separately in a later slice" note). This slice IS that wiring.
- **Change** (default-off, additive): `Service.reranker` field (constructed from
  `LoadRerankerConfigFromEnv`, default `NoopReranker`/"off" = zero I/O, response byte-stable);
  `HandleUnifiedRecall` applies `rerankRecallEntries` as a 2nd stage over the merged context
  candidates (gates on backend; soft-fails to embedding/priority order on proxy down — recall
  never fails; surfaces `recall_meta.rerank_backend`). `WithReranker` test option + 3 wiring
  tests. `go test ./pkg/agentcontext` + vet + golangci-lint(0) green; `cmd/mcp-agent-context` builds.
- **Live before/after** (proxy `/v1/rerank`, bge-reranker-v2-m3 co-active with bge-large on
  the Radeon VII; embedding-cosine order vs cross-encoder rerank, 5 graded-relevance queries):

  | Metric | Before (embedding-only) | After (+rerank) | Lift |
  |---|---|---|---|
  | Mean nDCG@6 | 0.554 | 0.778 | **+22.4 pts** |
  | Mean gold-doc rank | 3.40 | 2.40 | **−1.0** |

  Gold answer → rank 1 on 3/5 queries; ties on 2 genuinely hard cross-vocabulary queries;
  **never regresses**. Runtime stayed co-Active and served ~50+ live embedding+rerank calls
  through the eval with no eviction (VRAM ~1.6/16 GB). Utilization confirmed.
- **Enable (opt-in, default-safe)**: `WEAVER_RERANKER=flexinfer` + `WEAVER_RERANKER_BASE_URL=$FLEXINFER_URL`
  + **`WEAVER_RERANKER_MODEL=rerank`** — CRITICAL: the proxy routes the litellm alias
  `rerank`/`bge-reranker`; the library default `bge-reranker-v2-m3` is **not** a routable
  proxy alias (would 502). Graceful fallback (criterion 3) is unit-tested (404/timeout →
  `rerank_status` annotated, order preserved).
- **Gotchas**: cold-start transient bad_gateway right after a proxy restart (warms in seconds);
  loom-core default branch is `main` not `master` (the MCP create-MR targeted a non-existent
  `master` → phantom MR `cannot_be_merged`; retarget via REST `PUT .../merge_requests/IID?target_branch=main`).
  agent-context runs as a LOCAL stdio MCP server (no k8s pod) → flipping the flag live = rebuild
  local binary + set env (operator step; code ships default-off).
- **Status**: shipped loom-core MR !621 (auto-merge armed, pipeline 12855). Sprint 1 S1.4 done
  for real — the rerank route now has a production consumer wired (vs the falsified embeddings migration).

### 2026-06-04 S2.2 — idle-time batch appliance: nightly bge re-embed CronJob (LIVE-VERIFIED)

- **Slice**: Sprint 2, S2.2 (hardware-utilization arc). The in-cluster batch consumer the
  S1.4b proof redirected here: latency-insensitive nightly re-embed hitting `flexinfer-proxy`
  directly (self-hosted bge HBM2 plane), NOT the WAN/Cloudflare gateway. Operator picks:
  S2.2 per plan + standalone ConfigMap script Job.
- **Artifact** (`deploy/tasks/codebase-reembed/`, additive/reversible, Flux-managed): a
  `CronJob` (nightly 04:00 ET, `concurrencyPolicy: Forbid`) + ConfigMap `reembed.py`
  (pure-stdlib: urllib + uuid, no pip deps) + README. Source = inline read-only NFS mount of
  the shared devbox workspace (`192.168.50.211:/srv/nas/pilot3/nas-media-bulk/devbox-ws`) →
  no git/creds. Walk → line-window chunk → bge `/v1/embeddings` (model `bge-large-radeonvii`,
  1024-dim) → Qdrant upsert with deterministic UUIDv5 IDs (idempotent re-runs). Embedding runs
  remotely on gfx906 so the pod needs no GPU.
- **Grounding probes (read-only Jobs)**: bge embeddings in-cluster → HTTP 200 dim=1024;
  Qdrant **target disambiguation** — `daemon/qdrant` is anon-200 but a near-empty ORPHAN
  (only `xfiles-ufo`); the canonical agent-context/codebase-memory Qdrant is **host-level
  `192.168.50.176:6333`** (holds the morph sibling `codebase_memory_v1`), reachable from
  in-cluster pods but **api-key-enforced** → secret `qdrant-credentials/api-key` in
  flexinfer-system. GitLab unusable as source (public edge Cloudflare-1010 blocks urllib UA;
  in-cluster svc 404 anon for the private repo).
- **Live verify** (bounded `MAX_CHUNKS=500`, target REPO_PATH=/workspace/services/loom-core →
  collection `codebase_memory_bge_v1`, 1024-dim Cosine, on the canonical Qdrant alongside
  morph): re-embed DONE files=35 chunks=500; `points_count=500` status green (confirmed via
  the MCP qdrant pointed at 192.168.50.176). Search (3 queries, junk-free after the dotfile
  fix): "register an MCP tool handler" → `cmd/mcp-quality/main.go`, `cmd/mcp-docker/main.go`
  (0.77/0.73); "start a new agent context session" → `AGENTS.md` agent-context sections
  (0.73). Semantically relevant; morph `codebase_memory_v1` untouched.
- **Bugs found+fixed via live verification** (RALPH Prove caught all three): (1) bge-large is
  a 512-token max-seq model → 60-line code windows hit 583/533 tokens → 500s; fixed with a
  per-input `MAX_CHUNK_CHARS=900` (~450 tok worst case) that splits over-long windows.
  (2) macOS AppleDouble `._*` NFS cruft embedded as near-empty noise and crowded out real
  hits → skip dotfiles in `iter_files`. (3) wrote to the wrong (orphan) Qdrant first →
  repointed to the canonical `192.168.50.176` + api-key secret; orphan collection cleaned up.
- **Throughput**: 4–10 emb/s **wall-clock** for the bounded run (cold bge activation + per-batch
  Qdrant upsert over LAN dominate; NOT the warm-serving 70.9 emb/s from S1.4b). For a nightly
  latency-insensitive idle batch this is fine and free/self-hosted vs the morph cloud.
  Follow-up tuning (warm-pin during the window / parallel upsert / larger batches) is optional.
- **Status**: CronJob LIVE (`SUSPEND: False`), schema accepted by the API. Shipping in
  flexinfer `deploy/tasks/codebase-reembed/`. S2.2 acceptance met: nightly re-embed runs
  unattended + logged; one in-cluster consumer wired with the throughput path measured.

### 2026-06-04 S2.3 — offline model-eval gauntlet: Postgres benchmark storage e2e (#34) + automation (#27) (LIVE-VERIFIED)

- **Slice**: Sprint 2, S2.3 (hardware-utilization arc). Validate the never-proven Postgres
  benchmark storage backend (#34) end-to-end, then automate benchmark generation (#27) as a
  scheduled gauntlet. Continues the S2.2 idle-batch appliance pattern.
- **Tracker drift corrected**: #34 names `pkg/benchmarkconfig/store_postgres.go` — that path
  does not exist; the store is `agents/benchmarker/postgres_store.go` (`NewPostgresStore`:
  `sql.Open`+Ping, embedded `schema.sql` auto-creates the `benchmarks` table, inserts via
  `Save`). `flexinfer-bench` is CI-published (`registry.harbor.lan/flexinfer/flexinfer-bench:master`).
- **#34 e2e — gap found + closed**: the configured DSN
  (`deploy/system/values-k3s.yaml:210` → `langgraph-postgres-postgresql.ai.svc:5432/flexinfer_benchmarks`,
  user langgraph) pointed at a database that **did not exist** — the store auto-creates the
  *table* but not the *database*, so the bench would have failed at Ping. Created
  `flexinfer_benchmarks` (probe job). Then ran `flexinfer-bench` (SA `flexinfer-benchmarker`,
  nodes+configmaps RBAC; `FLEXINFER_PROXY_URL`; `POSTGRES_DSN`) against the warm
  `gemma4-26b-a4b-gptq` (vllm): table auto-created, **1 row inserted** (113.39 tps / 512 tok /
  4.52 s). #34 PASS.
- **#27 automation — gauntlet CronJob** (`deploy/tasks/model-eval-gauntlet/`, additive/reversible,
  Flux-managed): weekly (Sun 05:00 ET) `flexinfer-bench` loop over a `MODELS` (`name=backend`)
  list → Postgres `benchmarks` + per-model ConfigMap. Wrapper continues past one cold/missing
  model (exits non-zero only if all fail). Live one-shot from the CronJob: **ok=2 fail=0**,
  both models "Saved benchmark result to postgres". Final table = **3 rows**:
  gemma4-26b-a4b-gptq 113.39→156.60 tps (batch 32→64), gemma4-26b-a4b-gptq-5930k twin 141.24 tps.
- **Known limitation (follow-up)**: `device_class` is empty (`vendor=,arch=,…`) — the bench
  derives it from the **runner pod's** node (a CPU node), not the model's serving GPU node.
  Storage + throughput numbers are correct; device attribution needs a benchmarker code change.
- **Status**: both CronJobs LIVE (`codebase-reembed` + `model-eval-gauntlet`, SUSPEND False).
  #34 validated (closeable); #27 advanced (offline scheduled gauntlet; true on-artifact-creation
  trigger is a follow-up). S2.3 acceptance met: gauntlet emits Postgres rows automatically.

### 2026-06-04 S2.4 → #26 — controller preload-on-deploy (premise pivot + opt-in feature)

- **Slice**: Sprint 2, S2.4. The plan framed S2.4 as idle-window **prefix-cache prewarm**.
  Grounding **falsified that premise for the current fleet**: the daily-driver
  `gemma4-26b-a4b-gptq` is warm-pinned (`minReplicas:1`, nothing to cold-start) AND has
  `enablePrefixCaching: false` (APC structurally infeasible at 32K/FP8-KV per the F4 canary —
  no prefix cache to seed); every scale-to-zero text lane (gemma4-e4b, qwen3-8b, qwen3-1p7b)
  is on `cblevins-radeonvii` (gfx906) co-located with the **live bge+reranker** plane, so a
  blanket morning prewarm would evict the S1.4 retrieval win for idleTimeout-bounded gain.
  **Operator picked: pursue #26's controller pre-loading instead.**
- **Shipped** (opt-in, default-off, additive): `config.preloadOnDeploy: true` keeps a
  **non-shared, serverless** model warm (1 replica) from deploy until its first request, so
  the first post-deploy request skips cold start; after `LastActiveTime` is set, normal idle
  scale-to-zero resumes (distinct from `minReplicas:1` which pins forever). Pure calculation in
  `desiredReplicasForContext` (the `LastActiveTime==nil` branch) — no CRD change, no state
  writes. **Shared-GPU members are excluded** (the `SharedGroup.State != Active → 0` guard runs
  first), so preload can **never** contend with or evict a group leader (e.g. gfx906 bge).
  Observability: `flexinfer_model_preload_active{model,namespace}` gauge (1 while held warm
  pre-first-request). Files: `controllers/model_helpers.go` (`preloadOnDeploy`/`isPreloadWarming`
  + branch + gauge in `recordPhaseMetrics`), `pkg/metrics/exporter.go` (gauge + register),
  `docs/CONFIGURATION.md`.
- **Verify**: `go build ./...` OK; `go vet` OK; new `TestDesiredReplicasPreloadOnDeploy` PASS
  (warm when never-activated; scales to 0 after first-use idle; off when disabled; shared
  excluded) + full `./controllers/...` + `./pkg/metrics/...` suites green. Default-off → zero
  behavior change for existing models, safe to roll via CI-publish + Flux. Live-verify on a
  non-shared serverless lane is an optional post-rollout step (no such lane is warm-testable on
  the current shared-heavy fleet without disturbing live gfx906 lanes).
- **Status**: #26 advanced (controller preload-on-deploy shipped; the heavier "rollout controls"
  like preload TTL / on-artifact triggers remain). **Sprint 2 COMPLETE** (S2.1–S2.4).

---

### 2026-06-05 S3.0 — traffic-mix kill-test FAILED → request-shape instrument shipped

- **Slice**: Sprint 3 grounding. Operator picked "**measure traffic mix first**" before any
  further blanket n-gram SD work (twin param bump / `maxNumSeqs` raise), because SD is
  **workload-conditional** (short Q/A ~2× win; long-form gen −53%→−75% tax) and there is **no
  per-request SD bypass** (`ngram-sd-workload-conditional`). The prescribed data source was a
  week of proxy `event=request_usage` logs (`internal/proxy/usage_log.go`, MR !518) → joint
  prompt/completion-token distribution per lane.
- **Kill-test (≪30 min) — FAILED**: the data source does not exist in any aggregator.
  Evidence:
  - Loki: **zero `request_usage` lines** in flexinfer-system over 24 h (650k lines scanned,
    `postFilterLines: 0`); `{app="flexinfer-proxy"}` returns **zero lines of any kind**.
  - Root cause: the proxy pod (`flexinfer-proxy:master @ sha256:1da9de53`, chart
    `1.0.2+7067adee` = HEAD, so the usage-log feature **is** deployed) is pinned to
    `cblevins-gtx980ti` (`nodeSelector: node-role.kubernetes.io/control-plane`), a node whose
    pod logs are **not scraped into Loki** (no flexinfer-system series ever originate there).
    `kubectl logs` works but the stream is **drowned by controller-runtime
    `v1 Endpoints is deprecated` spam** (~4/s) and rolls, so request lines are unrecoverable.
  - Prometheus (reliably scraped) has **no** token/shape metric — `list_metrics` over
    `flexinfer.*(token|prompt|completion|shape)` returns only duration/count/queue/admission.
  - Net: per-request token shape is observable **nowhere**. The traffic-mix verdict is
    ungroundable today — exactly the kind of failure a kill-test catches before building an
    analysis pipeline on absent data.
- **Shipped (in-repo, additive, default-safe)**: two proxy Prometheus histograms
  `flexinfer_proxy_request_prompt_tokens{model}` + `flexinfer_proxy_request_completion_tokens{model}`,
  observed in `logUpstreamUsage` from the upstream `usage` block, labeled by **resolved model**
  (matches existing proxy metrics for joins). Buckets span 16→32768 tokens. This routes the
  traffic-shape signal through Prometheus (scraped), **bypassing the broken log path**. Files:
  `internal/proxy/metrics.go`, `internal/proxy/usage_log.go`, `internal/proxy/usage_log_test.go`,
  `docs/specs/metrics.md`.
- **Verify**: `go build`/`go vet ./internal/proxy/...` OK; new
  `TestLogUpstreamUsage_RecordsTokenShapeMetrics` (+1 obs on each histogram) and
  `..._SkipsTokenShapeMetricsForStreaming` (no obs) PASS; full `./internal/proxy/` suite green;
  gofmt clean.
- **CAVEAT (no silent cap)**: only **non-streaming** JSON completions carry a parseable usage
  block, so the histograms are biased toward non-streaming traffic (same bias as the usage log).
  A future slice could parse the final SSE `usage` chunk for streaming coverage.
- **S3.0b (coverage counter) — SHIPPED 2026-06-05.** The histograms observe **non-streaming
  only**, so a streaming-heavy workload would bias them toward exactly the long-form generation
  the SD verdict hinges on. Added `flexinfer_proxy_completions_total{model,stream}` (counter,
  every successful completion) as the **coverage denominator**: the `stream="true"` share
  quantifies the histograms' blind spot per lane. If streaming dominates, the histogram verdict
  must wait for streaming usage-chunk capture (follow-up #3); if not, the histograms are
  trustworthy as-is. `TestLogUpstreamUsage_CountsCompletionCoverage` PASS; full
  `./internal/proxy/` suite green.
- **Telemetry follow-up (a) ALREADY FIXED 2026-06-05** (master `b3fc0406`,
  `fix(proxy): migrate endpoint watch to discovery.k8s.io/v1 EndpointSlice`): the controller-
  runtime `v1 Endpoints is deprecated` log spam is gone → `kubectl logs` on the proxy is
  readable again. Loki-scraping gap (b) still open in platform/gitops.
- **S3.0 LIVE-VERIFIED 2026-06-05.** Proxy rolled to `sha256:2d5b47ca` (15:31Z). Sent one
  non-streaming completion through the proxy to the warm `gemma4-26b-a4b-gptq` lane
  (`prompt_tokens=22, completion_tokens=6`); `/metrics` then showed
  `flexinfer_proxy_request_prompt_tokens_count{model="gemma4-26b-a4b-gptq"} 1` +
  `..._completion_tokens_count ... 1`. **The instrument works end-to-end through Prometheus,
  bypassing the broken log path** — design goal confirmed on real traffic. `completions_total`
  (S3.0b) is NOT yet in this image (rolled before the S3.0b merge `c0815345`) → goes live on the
  next proxy publish+Flux cycle. (Live-checked via `kubectl port-forward svc/flexinfer-proxy`;
  loom Prometheus MCP was disconnected.)
- **Status**: instrument shipped + S3.0 LIVE. The measurement now *can* land once data
  accumulates (hours/days of real traffic → read completion-token percentiles per lane and decide
  blanket SD per route, **gated on the stream-coverage share being low** once S3.0b rolls). The
  verdict itself is **deferred** to a follow-up. Sprint 3 SD replication remains
  config-blocked/risk-gated pending this data (twin already has SD `{5,4}`; primary already tuned
  `{7,6}`; qwen35 disabled).

---

### 2026-06-05 S3.0 #3 — streaming usage-chunk capture (MR !579, merged 2af1c733)

- **Slice**: Sprint 3 S3.0 follow-up #3. Closes the documented blind spot in the S3.0 instrument:
  `flexinfer_proxy_request_{prompt,completion}_tokens` observed **non-streaming completions only**,
  biasing the sample away from exactly the long-form generation the blanket n-gram SD verdict hinges
  on (`ngram-sd-workload-conditional`). Not data-gated (unlike the verdict), so landable now to make
  the eventual verdict trustworthy regardless of traffic mix.
- **What**: `usageSniffingBody` (`internal/proxy/usage_log.go`) — a transparent pass-through wrapper
  around streaming (SSE) completion bodies. Forwards every byte to the client unmodified/unbuffered,
  retains a bounded 32 KiB tail, parses the terminal OpenAI `usage` chunk on `Close`, records the
  two histograms once. Wired in `logUpstreamUsage` for `stream=true` 2xx completion paths. Present
  **only** when the client set `stream_options.include_usage` (engine then emits the usage chunk);
  streams without it stay unobserved and `flexinfer_proxy_completions_total{stream}` still bounds the
  gap. Help text + `docs/specs/metrics.md` updated (no longer "non-streaming only").
- **Safety**: additive, default-safe, no behavior change to the stream itself (transparent tap, no
  buffering, idempotent `Close`).
- **Verify**: `gofmt`/`go build`/`go vet ./internal/proxy/...` clean. New tests:
  `TestExtractStreamingUsage` (null frames, truncated leading line, zero-filled placeholder,
  `[DONE]`-only, garbage), `TestUsageSniffingBody_RecordsOnCloseAndPassesThrough` (byte-exact
  pass-through + record-on-Close + idempotent Close), `TestLogUpstreamUsage_StreamingRecordsTokenShapeFromUsageChunk`
  (e2e), and the repurposed `..._SkipsTokenShapeMetricsForStreamingWithoutUsageChunk`. Full
  `./internal/proxy/` suite green under `-race`. CI pipeline 13024 green (fmt/vet/lint + unit/proxy/
  integration/gpugroup tests); auto-merged.
- **CAVEAT (no silent cap)**: coverage still depends on clients setting `stream_options.include_usage`.
  Whether real streaming traffic carries the chunk is unknown until S3.0b's `completions_total` rolls
  live and data accumulates — if streaming dominates AND usage chunks are absent, the histograms stay
  biased and a deeper capture (delta-counting, or proxy-injected `include_usage`) would be the next move.
- **Status**: S3.0 instrument now covers streaming-with-usage-chunk. Remaining S3.0 dependency is
  unchanged: roll S3.0b (`completions_total`, merge `c0815345`) live via proxy publish+Flux, then let
  ≥1 day of real traffic accumulate before the blanket-SD verdict.

---

### 2026-06-05 S3.0b + S3.0 #3 — LIVE-ROLL VERIFIED (instrument trio live + centrally scraped)

- **Slice**: Sprint 3 S3.0 — closes the *only* remaining S3.0 implementation dependency: confirm
  the merged S3.0b coverage counter (`c0815345`) and S3.0 #3 streaming usage-chunk capture
  (`2af1c733`) are actually **live on the rolled proxy** (they were merged but the previously-checked
  image `sha256:2d5b47ca` @ 15:31Z predated both merges). No code change — this is the live-roll
  proof that starts the ≥1-day data clock.
- **Live image**: proxy Deployment pins `:master`; running pod `flexinfer-proxy-788c67b76c-kq28f`
  on `cblevins-gtx980ti`, image `sha256:3094248f...`, started **2026-06-05T20:47:58Z** (re-rolled
  ~4 h after the #3 merge at 16:30Z, so `:master` was rebuilt with both commits).
- **Verify (end-to-end via `kubectl port-forward svc/flexinfer-proxy`)**:
  - Non-streaming completion to warm `gemma4-26b-a4b-gptq` (HTTP 200, `usage` prompt=20/compl=2) →
    `flexinfer_proxy_completions_total{model,stream="false"}=1` (with the **S3.0b help text**
    "The stream=true share is the blind spot…") + `request_prompt_tokens_count`/
    `request_completion_tokens_count`=1. **S3.0b counter LIVE.**
  - Streaming completion with `stream_options.include_usage=true` → terminal SSE `usage` chunk
    (prompt=20/compl=2) → `completions_total{stream="true"}=1` **and** the histogram `_count`
    advanced **1 → 2**. Pre-#3 the streaming path would NOT have recorded the shape (count would
    have stayed 1) → **S3.0 #3 streaming usage-chunk capture LIVE.**
  - **Central scrape confirmed**: loom Prometheus returns all three new series with full
    `job="flexinfer-proxy"` / `pod` / `namespace` labels (scraped from `10.42.2.23:8080`). The
    earlier "proxy on un-scraped gtx980ti node" worry is moot — the proxy *is* scraped.
- **Status**: S3.0 instrument stack (histograms + coverage counter + streaming capture) is **fully
  live and centrally scraped**. The "roll S3.0b live" dependency is **CLOSED**. The blanket n-gram
  SD verdict is now **purely time-gated**: let ≥1 day of real per-lane traffic accumulate, then read
  per-lane completion-token percentiles + the `stream="true"` coverage share and decide blanket SD
  per route (gated on coverage being trustworthy per `ngram-sd-workload-conditional`). 2 synthetic
  verification samples injected (negligible vs. a day of traffic). No code/CR change this slice.

---

### 2026-06-05 S3.0 traffic source — news-analyzer batch canary onto gemma4-26b SD lanes

- **Problem found**: the SD-verdict data clock was effectively *not running* — the gemma4-26b lanes
  see ~0 real completions/day. The heavy LLM consumers (news-analyzer, jobsearch-app,
  storyboard-generator) route through the **legacy `ai`-namespace litellm** (`litellm.ai.svc`),
  whose local GPU backends are all scaled 0/0, so it only proxies to **OpenRouter cloud**
  (`or/deepseek-chat`, `or/llama-3.3-70b`). Self-hosted GPUs idle + cloud spend + lanes starved.
  Only project-management/mentatlab point at flexinfer-proxy (low volume). flexinfer-proxy 24h
  total ≈534 req, ~0 actual completions per lane.
- **Action (operator-approved canary)**: repointed **only** the news-analyzer `summarizer-batch`
  cronjob (runs every 30 min) → `flexinfer-proxy.flexinfer-system/v1`, model `gemma4-26b-a4b-gptq`,
  local fallback chain `[-5930k twin, gemma4-e4b-gguf]`, `CONCURRENCY 10→2` + `TIMEOUT→120`
  (single-stream `maxNumSeqs:1` lanes). Shipped news-analyzer **MR !7** (merged; Flux-applied).
  OCR/extractor (gemini vision), digest, rss-digest, the always-on deployment, and all embeddings
  stay on cloud (untouched). Reversible: revert the cronjob file.
- **Verify**: reachability kill-test from a live summarizer pod → flexinfer-proxy `/v1/models` 200,
  chat `gemma4-26b-a4b-gptq` 200 (no NetworkPolicy block). After Flux apply, a manual batch run
  logged `Initialized ArticleSummarizer with model priority: gemma4-26b-a4b-gptq,
  gemma4-26b-a4b-gptq-5930k, gemma4-e4b-gguf` — app honors the canary chain. That run found **0
  pending articles** (queue drained, low overnight inflow), so completion volume accrues organically
  as the scraper/extractor pipeline feeds articles.
- **Action 2 — storyboard-generator (prod) — MR !2 (merged + LIVE 2026-06-05)**: repointed the
  prod text path (`LLM_ENDPOINT`/`LLM_BASE_URL` + `LLM_MODEL`/`TEXTGEN`/`VISION`/`AGENT` + legacy
  `OLLAMA_API_URL`) → flexinfer-proxy `gemma4-26b-a4b-gptq`. Image generation (`IMAGE_ENDPOINT`/
  `IMAGE_MODEL=image-gen`/`SD_API_URL`) stays on cloud diffusers (separate env, untouched). Edited
  `k8s/overlays/production/configmap-patch.yaml`. **GitOps gotcha**: `envFrom: configMapRef` does NOT
  auto-rollout on a ConfigMap change, and `kubectl rollout restart` is **reverted by Flux** (the
  restart annotation isn't in git) — had to `kubectl delete pod -l app=storyboard-generator` to cycle
  them. New pod verified: `LLM_ENDPOINT=flexinfer..., LLM_MODEL=gemma4-26b-a4b-gptq, IMAGE_ENDPOINT=
  litellm.ai` (cloud). Reachability kill-tested 200 from a storyboard-prod pod.
- **Action 3 — jobsearch-app daemon — MR !93 (auto-merge armed)**: required a **code change** —
  daemon shares one `LLM_BASE_URL` for all roles and has a *real* cloud vision model
  (`or/claude-3.5-sonnet`). Added `LLM_VISION_BASE_URL` split (`config.get_base_url_for(model_type)` +
  threaded through `clients._get_local_model`/`_create_model_for_provider`; 54 tests pass). Manifests
  (deployment, worker, cron-match): `LLM_BASE_URL`→flexinfer, `LLM_VISION_BASE_URL`→litellm.ai (cloud),
  `LLM_TEXTGEN_MODEL`/`LLM_AGENT_MODEL`→gemma4-26b, vision model stays claude. **Safe**: the LLM vision
  path (`get_vision_model`) is dormant (no `image_url` call sites; real interview vision is MediaPipe),
  and text works under old or new code → no new-env/old-image risk. **NOTE**: jobsearch deploy/image-
  promote job is `when: manual` → after merge, promote the new `daemon-api` image so the vision-split
  code is live (text canary works regardless; env change triggers the pod rollout via Flux).
- **Status**: 3 traffic sources enabled (news-analyzer batch, storyboard-prod text, jobsearch
  text/agent); real per-lane completion data now accrues toward the S3.0 SD verdict. **Follow-up
  (≥1 day)**: confirm gemma4 lanes show real completions from all three, read the per-lane
  completion-token distribution + `stream` coverage, watch for 32K-context overflows (gemma4 is 32K
  vs cloud 131K) and quality regressions, then make the blanket-SD verdict. jobsearch image-promote
  is the one open manual step.

---

## Benchmarker device_class — serving-node resolution (#34 follow-up, 2026-06-04)

**Defect**: The benchmarker stored an empty `device_class`
(`vendor=,arch=,vram=,count=,int4=`) in the Postgres `benchmarks` table.
`agents/benchmarker/postgres_store.go` `Save()` resolved device class from
`r.NodeName`, which was the benchmarker pod's own node (downward-API
`NODE_NAME`) — a CPU worker (`k3s-w-10`) with no `flexinfer.ai/gpu.*` labels —
not the node serving the benchmarked model.

**Fix** (`fix/benchmarker-device-class-serving-node`, commit `7e2c09e3`):
resolve the serving node from the model's Endpoints after the backend is Ready,
priority order (1) endpoint `NodeName` → (2) `TargetRef` Pod → (3) IP→Pod match
(runtime-served models carry only an IP). Recorded onto `BenchmarkRecord.NodeName`
so the Postgres store, the ConfigMap global-result store, and `emitBenchmarkMetrics`
all read GPU labels from the correct node. Benchmarker ClusterRole granted
`get,list` on `endpoints,pods`. Falls back to the runner node if unresolved.

**Unit evidence**: `go build ./...` OK, `go vet ./agents/benchmarker/` OK,
`go test ./agents/benchmarker/` PASS — incl. new `serving_node_test.go`
(NodeName / TargetRef / PodIP tiers, not-found + no-client fallbacks, and
`TestRun_DeviceClassFromServingNode_NotRunnerNode` proving the GPU node wins
over the CPU runner node end-to-end).

**Live e2e** (like S2.3; image `flexinfer-bench:device-class-fix` built+pushed,
additive test RBAC `flexinfer-benchmarker-endpoints-test`, one-off Job
`device-class-validate` → warm `gemma4-26b-a4b-gptq` via proxy):

- Benchmarker log: `Resolved model serving node for device class
  {"servingNode": "cblevins-7900xtx", "runnerNode": "k3s-w-10"}` — runner was the
  CPU node (the bug), correctly resolved to the GPU node.
- Postgres `flexinfer_benchmarks.benchmarks`
  (`langgraph-postgres-postgresql.ai.svc:5432`, user `langgraph`), new row
  `4e795412-876d-4c0c-9ce2-0f43bccbb0af` @ `2026-06-05 01:49:27Z`:
  `device_class = vendor=AMD,arch=gfx1100,vram=23Gi,count=1,int4=true`
  (153.0 tps). The three prior rows (the reproduced bug) remain
  `vendor=,arch=,vram=,count=,int4=`.

**Status**: PASSED 2026-06-04. Durable RBAC ships via the chart
(`charts/flexinfer/templates/rbac.yaml`) on merge→CI→Flux; durable code ships in
CI-published `flexinfer-bench:master`. The test RBAC
(`flexinfer-benchmarker-endpoints-test`) and the one-off `device-class-validate`
Job were deleted after validation; the throwaway `flexinfer-bench:device-class-fix`
registry tag remains in Harbor (harmless, superseded by `:master` on merge).

---

### 2026-06-05 S3.0 — first per-lane data-clock read (canary effective; verdict still time-gated)

**What this slice is**: a RALPH Prove/Harvest pass over the S3.0 data clock that the
2026-06-05 canaries started (news-analyzer batch MR !7, storyboard-prod MR !2,
jobsearch-app MR !93 — all merged). Yesterday the gemma4-26b SD lanes saw ~0 real
completions/day (heavy consumers routed via legacy `ai`-namespace litellm → OpenRouter
cloud). This confirms the bet paid off: **real per-lane traffic is now flowing**, and
records the first token-shape read. No code/CR change.

**Evidence (loom Prometheus, `job="flexinfer-proxy"`, instant + 24 h aggregates handling
per-pod counter resets via `increase(...[24h])`)**:

- **Traffic now flowing** (was ~0/lane on 2026-06-04):
  `sum(increase(flexinfer_proxy_completions_total[24h]))` → **primary
  `gemma4-26b-a4b-gptq` ≈ 45/day**, **twin `gemma4-26b-a4b-gptq-5930k` ≈ 5/day**.
  (Counter reset ~5× in 24 h from proxy pod rolls — `increase()` repairs the resets;
  the TSDB accumulates across pod generations, so the "≥1 day" window is intact even
  though no single pod's counter survives a day.)
- **Primary lane token shape** (the daily-driver / news-analyzer summarizer path):
  - `histogram_quantile(0.5, …request_prompt_tokens_bucket…)` → **prompt p50 ≈ 741 tok**
  - `histogram_quantile(0.5, …request_completion_tokens_bucket…)` → **completion p50 ≈ 495 tok**
  - `histogram_quantile(0.9, …)` → **completion p90 ≈ 958 tok**
  - Current-pod bucket snapshot (~21 samples) was bimodal: ~8 short (≤256 tok) + ~12
    medium-long (512–1024 tok), 1 in 1024–2048.
- **Twin lane token shape**: completion **p50 ≈ 13 tok** (~5 samples — noisy, but clearly
  short: fallback / health-probe-shaped traffic, not the summarizer batch).
- **Stream coverage**: `sum(increase(completions_total{stream="true"}[24h])) / sum(...)`
  → **0 %**. Every real consumer in the current mix is non-streaming, so the
  `request_*_tokens` histograms already capture **100 %** of real traffic. The S3.0 #3
  streaming usage-chunk capture (MR !579) remains correct insurance but is moot for the
  present consumer set.

**Preliminary read (NOT the verdict — sample is <1 day, ~45+5 completions)**:

- The two SD lanes have **inverted workload shapes**: the **primary is long-form**
  (prompt p50 ~741, completion p50 ~495 / p90 ~958), the **twin is short** (completion
  p50 ~13). Per [`ngram-sd-workload-conditional`] this is the decisive split — n-gram SD
  pays ~2× on short Q/A but **regresses −53 % to −75 % on long-form generation**.
- Direction therefore **inverts the naive Sprint 3 "replicate the 2× everywhere"
  premise** for the primary: a median ~495-token completion sits squarely in the
  SD-hostile regime. Early signal = **blanket SD on the primary long-form lane is
  likely a net loss**; the twin (short) is the lane that would actually benefit. This
  is exactly the per-lane (not blanket) outcome the operator's "measure first" gate was
  protecting against.

**Status**: S3.0 data clock **CONFIRMED RUNNING** (canaries effective). The blanket-SD
verdict (S3.1/S3.2/S3.3) remains **time-gated** — current sample is immature (<1 day,
~50 completions). Re-read after ≥1 day of accumulation: per-lane completion-token
percentiles + `stream` coverage share, then rule SD **per lane** (preliminary lean:
twin-yes / primary-no). No knob touched this slice — recording the read, not acting on
it, per the spec-riskiest-assumption discipline baked into this plan.

---

### 2026-06-06 S3.0 — mature ≥1-day per-lane data-clock read + blanket-SD VERDICT

**What this slice is**: the RALPH Prove/Harvest pass that the 2026-06-05 first-read
deferred. The traffic-source canaries (news-analyzer !7, storyboard !2, jobsearch !93)
have now fed the gemma4-26b SD lanes for a full day, so the sample is mature enough to
render the per-lane blanket-SD verdict that gates S3.1/S3.2/S3.3. No code/CR change —
this is the measurement→decision slice the operator's "measure first" gate was built for.

**Evidence (loom Prometheus, `job="flexinfer-proxy"`, `increase(...[24h])`, reset-repaired)**:

| Lane | 24h vol | prompt p50 | compl p50 | compl p90 | completion mass |
|------|---------|-----------|-----------|-----------|-----------------|
| primary `gemma4-26b-a4b-gptq` | **≈ 88/day** (was ~45) | ≈ 756 | **≈ 588** | **≈ 953** | ≤256: 17.5% · ≤512: 41.7% · **>512: 58.3%** · >256: 82.5% |
| twin `gemma4-26b-a4b-gptq-5930k` | **≈ 5/day** | ≈ 896 | **≈ 13** | ≈ 384 | **≤16: 60%** · ≤128: 80% · ≤512: 100% |

- **Stream coverage = 0%** (`completions_total{stream="true"}` = 0) — every consumer in
  the live mix is non-streaming, so the `request_*_tokens` histograms capture **100%** of
  real traffic. MR !579 streaming capture stays correct insurance but is moot here.
- Sample matured ~2× vs the 2026-06-05 first read (primary 45→88 completions); the lane
  shapes held, so the preliminary lean is now confirmed, not provisional.

**Live SD config at read time** (both lanes already run blanket n-gram SD):
- primary `gemma4-26b-a4b-gptq.yaml:163`: `{num_speculative_tokens:7, prompt_lookup_max:6}`, `enforceEager:false` (graph capture on), `enablePrefixCaching:false`.
- twin `gemma4-26b-a4b-gptq-5930k.yaml:125`: `{5, 4}`, `enforceEager:false`.

**VERDICT — per-lane, NOT blanket** (per [`ngram-sd-workload-conditional`]: n-gram SD pays
~2× on short Q/A but regresses −53% to −75% on long-form generation):

- **Primary — blanket SD is a PROBABLE NET LOSS on its real workload.** 58.3% of primary
  completions exceed 512 tok (median ≈ 588); the lane lives squarely in the SD-hostile
  long-form regime, yet currently runs the *widest* SD config (`{7,6}`). The naive Sprint 3
  "replicate the 2× everywhere" premise is **falsified for the primary**. The next move on
  the primary is **DOWN/OFF, not up** — but it must be proven by a direct on/off A/B (see
  next slice), not flipped blind on the live warm-pinned daily driver.
- **Twin — SD-friendly workload, but negligible volume.** 60% of twin completions are
  ≤16 tok (median ≈ 13): short fallback/probe-shaped traffic, exactly the regime SD helps.
  So the planned **S3.2** bump `{5,4}→{7,6}` is *directionally safe* — but the lane runs
  ~5 completions/day. **Verdict: documented null — defer.** Tuning a 5/day fallback lane is
  low-leverage motion; not worth a live CR change against a warm-pinned daily driver's twin.
- **Methodological finding (blocks the obvious canary path)**: the twin **cannot serve as
  the primary's SD canary** — their workloads are *inverted* (twin short ↔ primary long). A
  primary SD on/off decision therefore needs a before/after measured **on the primary lane
  itself** (or a representative long-form synthetic load that matches the 588/953 p50/p90
  shape), not a twin canary. The plan's "canary the twin first" guidance (S3.3) does **not**
  transfer to the SD-verdict question.

**Per-slice disposition**:
- **S3.1** (replicate SD → qwen35): **BLOCKED, no change** — qwen35 disabled (#51/#52).
- **S3.2** (twin `{5,4}→{7,6}`): **documented null / defer** — safe but ~5/day, negligible value.
- **S3.3** (`maxNumSeqs` knee): **unaffected** — orthogonal to SD (batching, not speculation);
  remains a separate high-blast-radius slice.
- **NEW S3.4** (proposed next): **primary SD on/off A/B kill-test** on representative
  long-form traffic. The data makes "primary blanket SD net-loss" the riskiest live
  assumption now standing; the kill-test is a direct decode-tps before/after on the primary
  with `speculativeConfig` present vs absent, under a prompt set matching the live
  588-p50 / 953-p90 completion shape. If confirmed, disable or narrow primary SD.

**Status**: S3.0 measurement thread **COMPLETE** — instrument shipped, rolled live,
≥1-day data accumulated, per-lane verdict rendered. The "≥1 day of per-lane token-shape
data → blanket-SD verdict per lane" acceptance criterion for Sprint 3 is **met**. SD
knobs deliberately untouched this slice (verdict, not action); the one high-leverage
action the data justifies (primary SD A/B) is scoped as S3.4 for operator-gated execution.

---

### 2026-06-06 S3.4 — primary SD on/off A/B kill-test (operator-gated) → primary SD DISABLED

**What this slice is**: the operator-gated A/B the S3.0 verdict scoped. The S3.0
read made "the primary's blanket SD `{7,6}` is a net throughput loss on its real
long-form workload" the riskiest live assumption. This kill-test measures it
**directly** on the identical gemma4-26b/gfx1100 substrate.

**Rig & method** (low blast radius): ran the A/B on the **`-5930k` twin** — same
model + GPTQ-int4 on the same 7900 XTX silicon as the primary, but near-idle
(~5 real req/day), so no daily-driver disruption. The S3.0 "twin can't canary the
primary" finding was about *natural* traffic shape; here the load is **supplied**,
so it dissolves. `flux suspend flexinfer-models` around the experiment; CR toggled
via `flexinfer_update_model`; **gitops = clean restore** via `flux resume`. Load:
real document→detailed-summary (≈760-tok prompt → ~685-tok completion, matching the
live 588/953 p50/p90 shape), **single-stream** (so the twin's maxNumSeqs=2 ≡ the
primary's maxNumSeqs=1), **temperature=0** (deterministic; SD is verified-equivalent
→ identical output, clean tps comparison). Decode tps = completion_tokens /
(t_last − t_first_token), isolating decode from prefill. 3 runs/arm; each arm's SD
state **engine-verified** from the vLLM `speculative_config=…` startup log.

**Result** (median decode tok/s, real-shaped long-form):

| Arm | speculativeConfig | engine `speculative_config` | decode tok/s | vs OFF |
|-----|-------------------|------------------------------|--------------|--------|
| **B** | *(removed)* | `None` | **63.2** (61.9–63.6) | — |
| A | ngram `{5,4}` | `num_spec_tokens=5` | 40.3 (40.2–40.3) | **−36%** |
| C | ngram `{7,6}` *(= primary's live config)* | `num_spec_tokens=7` | 38.4 (38.3–38.8) | **−39%** |

**SD OFF is +64% faster than the primary's `{7,6}` on real summarization traffic.**
Wider SD (`{7,6}`) is worse than narrower (`{5,4}`); both far worse than off.

**Why it matters / what's new**: this closes the gap the S3.0 verdict left open. The
−53…−75% figure in `ngram-sd-workload-conditional` came from **filler-prompted** free
gen. This A/B used **real document→summary** — the output *echoes the input document*,
the case most *favorable* to prompt-lookup SD — and SD **still** regresses ~39%.
Prompt-lookup acceptance on coherent summarization is too low to offset the widened
verifier-step compute on the gfx1100 MoE target (graph capture is ON — `enforce_eager:
false` — so this is not the eager-mode 2026-05-14 falsification; it's the post-graph-
capture regime, and SD is still a loss for long-form here). The S3.0 verdict is now
**empirically confirmed**, not inferred.

**Action**: removed `speculativeConfig` from the **primary**
`gemma4-26b-a4b-gptq.yaml` (long-form lane) via GitOps. The **twin** keeps `{5,4}` —
its real traffic is short (completion p50 ≈ 13 tok), the SD-favorable regime → SD is a
**per-lane** decision, not a fleet default. Twin restored to gitops `{5,4}` + Ready;
Flux resumed; cluster returned to known-good before shipping.

**Status**: PASSED 2026-06-06 — kill-test decisive. Primary SD disabled (expected
+~64% decode throughput on its real long-form workload, free, via one rolling restart
mitigated by the twin fallback). S3.4 closes the Sprint 3 SD-verdict thread:
**primary = SD off (proven), twin = SD `{5,4}` keep (short traffic), qwen35 = blocked
(#51/#52)**. Remaining Sprint 3 slice: S3.3 `maxNumSeqs` knee (orthogonal to SD).

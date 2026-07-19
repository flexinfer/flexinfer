# Plan: abliterated long-context Qwen3.5-9B + NSFW-RP LoRA (psyche-simulator test)

Date: 2026-07-18
Branch: claude/qwen-lora-flexinfer-d8e43b

## Goal

Serve the abliterated Qwen3.5-9B at 32K context on the free gfx1100 node
(`cblevins-7900xtx`) with the external adapter
[`mirazrafi/NSFW-RP-RolePlay-LoRA-Qwen-3.5-9B`](https://huggingface.co/mirazrafi/NSFW-RP-RolePlay-LoRA-Qwen-3.5-9B)
hot-loaded, to exercise the psyche-simulator eval.

## Riskiest assumption + kill-test

**Load-bearing assumption**: vLLM on `cblevins-7900xtx` (RDNA3 / gfx1100 / ROCm)
can serve the **abliterated GPTQ** Qwen3.5-9B (hybrid: 24 GDN linear-attention +
8 full-attention layers) with the **rank-64 rsLoRA** adapter — which targets all
7 projections (`q,k,v,o,gate,up,down`), including projections inside GDN layers —
**hot-loaded at runtime**, and produce coherent roleplay output.

**Kill test** (≤30 min once the Model is `Ready`):
1. Apply the OCI-pull cache + Model + LoRAAdapter to `cblevins-7900xtx`.
2. `kubectl -n flexinfer-system port-forward svc/qwen35-9b-ablit-rp 8000:8000`.
3. Confirm the adapter registered: `GET /v1/models` lists `nsfw-rp`.
4. `POST /v1/chat/completions` with `"model":"nsfw-rp"` and an RP opener.
5. PASS = coherent (non-degenerate, no repetition collapse) in-character RP
   text, no refusal. FAIL = load rejected, garbled/degenerate output, or refusal.

**Failure modes if wrong**:
- vLLM rejects LoRA on a GPTQ base on ROCm (INT4+LoRA kernel unsupported) →
  fall back to serving the fp16 base + LoRA (needs an fp16 cache; heavier).
- vLLM errors applying LoRA to GDN-layer `q/k/v/o` (unsupported target module on
  the hybrid arch) → may need a filtered adapter or fp16 path.
- rsLoRA (`use_rslora:true`, scale = alpha/sqrt(r)) mishandled by the runtime →
  garbled output; check vLLM version honors rslora.
- HF download 401/403 on the NFAA repo → serving pod needs
  `HUGGING_FACE_HUB_TOKEN`, or switch adapter `source.type` to `LocalPath` after
  pre-staging weights on the model volume.

**Status**: FAILED 2026-07-18 — but *before* LoRA. Two blockers surfaced live:
1. Base model crashed at config parse on the default gfx1100 image (`a9b306af`):
   `vllm/transformers_utils/configs/qwen3_5.py` `validate_rope` →
   `TypeError: unsupported operand type(s) for -=: 'set' and 'list'`. The
   abliterated Qwen3.5-9B's rope config trips the image's validator before any
   weights/LoRA load. The workhorse avoids this on image `7bc680b4` + explicit
   `hfOverrides` rope block.
2. gfx1100 serves via the shared `flexinfer-runtime-gfx1100` pod (load request),
   NOT a dedicated Deployment — the controller deleted the Deployment. So the
   merged LoRA wiring (`--enable-lora` / `VLLM_ALLOW_RUNTIME_LORA_UPDATING` added
   in `model_deployment.go`) never applies here. `dedicatedDeployment: true` is
   required to run as a real Deployment (as the workhorse does).

**Proposed fix (next iteration)**: on the Model — `dedicatedDeployment: true`
(forces the Deployment path where the LoRA wiring applies) + pin `image:` to a
qwen3_5-parsing digest (workhorse `7bc680b4` or profile MTP cert `f467e202`) +
`hfOverrides` with a sane rope config to dodge `validate_rope`. Then re-run the
kill-test — which will FINALLY exercise the actual GPTQ+rank-64-LoRA assumption.

## What's done (this branch)

Controller/runtime fixes required for any rank>16 external adapter to hot-load
(all in `backend/` + `controllers/`, unit-tested — `go test ./backend/ ./controllers/` green):

1. `LoRABaseArgs(maxAdapters, maxRank)` now emits `--max-lora-rank` (rounded up to
   vLLM's allowed tier) when an adapter exceeds rank 16 — rank-64 here.
2. `VLLM_ALLOW_RUNTIME_LORA_UPDATING=True` is injected when the model has adapters,
   without which the reconciler's `/v1/load_lora_adapter` POST is rejected.
3. Model reconciler now `Watches(&LoRAAdapter{})` → creating an adapter against a
   live model re-renders its Deployment with `--enable-lora` (was a silent gap).

## Remaining steps (checkpoint before applying to the live cluster)

- Stage artifact: `deploy/modelcaches/qwen35-9b-oci-gfx1100.yaml` pulls
  `oci://registry.harbor.lan/flexinfer/qwen35-9b:gptq-w4-g128` (confirmed present
  in Harbor via `crane ls`) onto `cblevins-7900xtx`.
- Serve: `deploy/models/qwen35-9b-ablit-rp.yaml` at `maxModelLen: 32768`,
  `litellm.enabled:false` (kept off the public fleet until the kill-test passes).
- Adapter: `deploy/loras/qwen35-9b-nsfw-rp.yaml` (`adapterName: nsfw-rp`, rank 64).
- Run the kill-test above.

### Verify-on-apply (values I could not confirm from the worktree alone)

- **Model `source` PVC path** produced by the OCI-pull cache — drafted as
  `pvc://qwen35-9b-oci-gfx1100/qwen35-9b`; confirm the PVC name/subpath once the
  cache is `Ready`.
- **gfx1100 image resolution** for the `qwen3_5_text` (non-MoE hybrid) 9B — left
  to GPUProfile default (as the historical 9B did); confirm it registers the arch
  and supports LoRA. Pin an explicit `image:` digest if the default doesn't.
- **Native max_position_embeddings** for 9B ≥ 32768 (else add
  `VLLM_ALLOW_LONG_MAX_MODEL_LEN` / `hfOverrides` rope like the 35B).

## Notes

- The Harbor artifact was published by the `qwen35-9b-gptq` ModelCache, whose spec
  abliterates full-attention layers 11/15/19 (GDN-skipped, norm-guarded) before
  GPTQ. Verify abliteration is live simply by the absence of refusals on load.
- The LoRA was trained on the *clean* base (`techwithsergiu/Qwen3.5-text-9B`);
  we apply it to the *abliterated* base. Both remove refusals — functionally fine,
  just off the exact distribution the adapter author validated.
- Placement: `cblevins-7900xtx` chosen because it has the most free VRAM (24GB,
  only a small `wan21-t2v` diffuser resident) and does not contend with the 5930k
  primary chat model or the radeonvii retrieval plane.

## Status update 2026-07-18 (runtime build fix shipped; kill-test capacity-gated)

**Blocker 1 (gfx1100 runtime image build) — ROOT-CAUSED, FIXED, SHIPPED.**
`./build/build-runtime.sh gfx1100` failed rc=1 in the `INCLUDE_QUANTIZER=true`
layer. Root cause: the extra-deps `pip install` used an **unpinned** `kernels`,
which now resolves to `kernels==0.16.0`. That release made `version`/`revision`
mandatory on `kernels.LayerRepository`; the baked transformers build
(`529504b2`, 5.3.0.dev0) constructs `LayerRepository(repo_id=..., layer_name=...)`
without one in `integrations/hub_kernels.py`, so `import transformers` — and the
layer's final `import gptqmodel` smoke test — raised
`ValueError: Either a revision or a version must be specified.`
(tokenicer/pypcre/gptqmodel wheels all built fine; only the import test failed.)
Fix: pin `kernels>=0.10.2,<0.11` in `build/Dockerfile.runtime` — the exact range
transformers `529504b2` declares in its own `setup.py`/deptable. Rebuild resolved
`kernels-0.10.5`, import test passed, image pushed. First rebuild attempt died on
a transient `proxy.golang.org` TLS handshake timeout in `go mod download`
(busy-builder network flake) — retry with warm cache succeeded.

New image: `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100` @
`sha256:62bf47e8a8918d4b338d57d83677fc9f383fabfab7d870dab963ffbd597fba8f`.
Digest pins bumped in `deploy/system/values-k3s.yaml` (runtimeImage + gfx1100 +
gfx1100-5930k runtime profiles) and `deploy/gpuprofiles/gfx1100.yaml`.

**Blocker 2 (kill-test) — BLOCKED ON CAPACITY (guardrail respected).**
`qwen35-35b-clean-gptq-workhorse-128k` is Ready and actively holding the gfx1100
card (~24.6/25.7 GB); `qwen35-9b-ablit-rp` sits Queued at position 1 behind it.
Workhorse not displaced per hard guardrail.

**NEW blocker surfaced for the kill-test (independent of LoRA):** the model's
last runtime load crashed at base-model config parse with `validate_rope` →
`TypeError: unsupported operand type(s) for -=: 'set' and 'list'` — *despite*
the Model spec carrying the intended `hfOverrides` rope block. Root cause: the
runtime-managed serving path drops `hfOverrides` — it is only consumed in
`backend/vllm.go` (dedicated-Deployment arg builder); `pkg/runtime/payload.go`
(runtime load payload builder, used by `controllers/model_runtime.go`) never
references it. Next required step before the GPTQ+rank-64-LoRA assumption can be
exercised: thread `hfOverrides` through `pkg/runtime/payload.go` (or serve this
model via `dedicatedDeployment: true`), then retry once the card frees.

## Status update 2026-07-19 (rope-validator root cause CORRECTED + fleet layout)

**Correction to the previous update:** the `validate_rope` crash is NOT fixable
by threading `hfOverrides` (dict overrides apply after `cls(**config_dict)`, the
TypeError fires inside `__init__` validation). Actual root cause: vLLM's
`transformers_utils/configs/qwen3_5.py:77` passes
`ignore_keys_at_rope_validation` as a **list**; transformers 5.3.0.dev0 moved
`_check_received_keys` (with its `received_keys -= ignore_keys` set-subtraction)
from `configuration_utils.py` to `modeling_rope_utils.py`, so patch section 0e
of `build/scripts/vllm_qwen35_patches_nodiag.py` missed it → every Qwen3.5
dense model crashed at config parse. The artifact's own rope block is clean (no
mrope poison). Fix: 0e now patches both candidate files; repro + fix validated
in the live image against the real artifact config. Also hardened
`go mod download` (3x retry + GOPROXY `|direct`) after proxy.golang.org TLS
timeouts killed 2 of 3 full builds.

Patched image: `sha256:5069e96c1b9f46e66a1fcf451bef330c331f8a02aa5a51d606d190dd8ba906b3`
(supersedes `62bf47e8`; all 4 digest pins bumped).

**Fleet layout (MR !862, merged):** operator-directed psyche layout —
qwen35-9b-ablit-rp is now the warm-pinned 7900xtx leader (priority 500),
workhorse-128k demoted to 300; wan t2v/vace rehomed to cblevins-5930k
(5930k-textgen, count 1), 5930k workhorse demoted to 300/minReplicas 0 with
forcePromotion removed. This clears the kill-test capacity gate permanently.

**Next:** DS roll to 5069e96c → 9b loads with `--enable-lora --max-lora-rank 64`
→ LoRA kill-test at 32K → context maxing (native max_position_embeddings is
262144; stepwise 65536 → 131072 → 262144 with needle checks via
scripts/context-needle-bench.py).

## Kill-test results 2026-07-19 (image 5069e96c, fleet layout live)

**Mechanics: PASS.** On the patched runtime image the base model parses, loads,
and serves via the runtime-managed path with `--enable-lora --max-lora-rank 64`
and `VLLM_ALLOW_RUNTIME_LORA_UPDATING`. The rank-64 rsLoRA adapter hot-loaded
successfully (`Loaded new LoRA adapter: name 'nsfw-rp'` → 200 OK) and appears
in `GET /v1/models`. The GPTQ-base + rank-64-LoRA + hybrid-GDN + hot-load
mechanical assumption is TRUE.

**Quality: FAIL — but the fault is BELOW the LoRA.** Both `nsfw-rp` AND the
raw base model emit deterministic multilingual token salad (greedy
"The capital of France is" → 'नुeniiske在线阅读ัส...'). Findings:

1. NOT the LoRA (base is equally degenerate without it).
2. NOT the artifact's head/embeddings: `lm_head.weight` (248320x4096,
   std 0.0155, zero-frac 0), `embed_tokens`, `model.norm` all healthy.
3. NOT (solely) the `gdn_attention_core` ROCm custom-op corruption: live
   bypass test in the serving pod (patch section 17 extended to
   `qwen3_5.py`'s own call site, process restarted, patch confirmed active)
   produced BYTE-IDENTICAL garbage — the corruption is deterministic and
   elsewhere.
4. Remaining suspects, in order: (a) the qwen35 fastpath patch layer's
   dense-arch weight-mapping/head-expansion paths (patch 3
   `stacked_params_mapping` shard IDs, patch 8 `repeat_interleave` q/k head
   expansion in `rearrange_mixed_qkv`) — engineered against the MoE flow,
   never certified for the dense 9B head geometry; (b) the generic vLLM
   `0.17.0+rocm700` wheel's numerics on gfx1100 for this arch (the base
   image's navi-built vLLM was replaced by this wheel at build);
   (c) the artifact itself (abliteration/GPTQ damage) — it has NEVER passed a
   generation gauntlet; no clean-9B GPTQ twin exists for A/B (the
   `qwen35-9b-gptq` ModelCache spec abliterates before quantizing).

**Follow-up (blocking the psyche text lane):** discriminate (a)/(b)/(c) with a
reference-implementation A/B — e.g. wrap the artifact to VL layout and run
gptqmodel/transformers greedy tokens on CPU (`build/scripts/
qwen35_wrap_to_vl_layout.py`), or serve one layer's activations against a
reference. Context maxing (32K→262144) is queued behind a coherent base.

**LoRAAdapter controller bug found:** the reconciler re-POSTs
`/v1/load_lora_adapter` after a successful load; vLLM answers 400
"already been loaded" and the CR is marked Failed despite the adapter being
live. Treat that 400 as success (or check GET /v1/models first).

## Independent root-cause confirmation + live validation 2026-07-19 (second session, converged with !866)

A parallel investigation independently reached the same root cause as !866
(patch-0c reference fallback dequantizing gptq_shuffle-permuted qweight) via a
different evidence chain, and validated the mechanism live:

1. **CPU ground truth — artifact healthy (suspect c eliminated).** In the
   serving pod: `pip install --no-deps optimum` (2.2.0) makes plain
   transformers 5.3.0.dev0 + gptqmodel 6.0.0 load the checkpoint directly —
   the VL-layout wrap is NOT needed. Recipe: `GPTQConfig(bits=4,
   group_size=128, desc_act=False, backend="gptq_torch")` (default
   `torch_fused` CPU kernel crashes building its permutation index), GPU
   masked via `HIP_VISIBLE_DEVICES=""`, and the 48 unquantized
   `in_proj_a/b` modules (24 GDN layers × 2, shipped fp16 in the checkpoint)
   swapped back to `nn.Linear` post-load (transformers' loader otherwise
   random-inits them as QuantLinear). Greedy: "The capital of France is" →
   `' Paris.\nThe capital of France is Paris.\nThe'`; "2 + 2 =" → `' ?\n4\n**2.**\n3 + 4'`.
2. **GDN kernels pass parity on gfx1100** (in-pod, alongside serving):
   `fused_gdn_gating` maxerr 1e-6; `causal_conv1d_fn` 7e-3 (bf16);
   `causal_conv1d_update` chained decode 5e-3; conv_state handoff exact.
   Cache layout gotcha: conv_states must be `(slots, state_len, dim)`
   transposed to dim-contiguous, mirroring the qwen3_next call site.
3. **Shuffle experiment (smoking gun replicated):** `ops.gptq_shuffle` on
   layer-0 `in_proj_qkv` qweight → naive disk-layout dequant differs on 75%
   of rows afterwards (maxdiff 0.40 vs weight std 0.017).
4. **Mechanism validated live:** hot-editing the pod's `gptq.py` to keep the
   reference fallback but SKIP the shuffle (`torch.version.hip and
   weight_bits == 4`), then reloading via manager DELETE (controller
   re-added), produced greedy output **byte-identical to the CPU ground
   truth** plus coherent chat output. This is the inverse experiment of
   !866's matmul-level measurement and confirms shuffle-vs-unpack mismatch
   as the sole corruption.

**Also eliminated en route** (useful for future dense-9B work): patch 3's
contiguous `[Q|K|V|Z]` split matches HF dense `in_proj_qkv` layout
(`modeling_qwen3_5.py:506,564` — dense is NOT per-head interleaved); patch 8
`repeat_interleave` matches HF (ratio 2 for 32/16 heads); vLLM builds
`in_proj_ba` unquantized via `is_layer_gptq_quantized`'s fused-mapping shard
check, so patch 16/16b "found nothing" is correct; naive FLA kernels match HF
reference math including the 1/sqrt(K) scale; g_idx is trivial everywhere
(no act_group_aware permutation mismatch despite gptqmodel 6.0.0 metadata).

**Resolution**: !866's fix (remove fallback, stock fused `gptq_gemm`) is the
durable one — this session's shuffle-skip variant was superseded and dropped.
NOTE: the live pod currently runs the hot-edited venv (reference+skip-shuffle,
serving coherently); a pod restart before the rebuilt image rolls reverts to
salad. Rollout: rebuild `./build/build-runtime.sh gfx1100 --push` from master
(carries !866), repin digest at 4 sites, roll DaemonSet, rerun the greedy
canary (expect `' Paris.…'`), then LoRA quality re-test + context-max 262K.

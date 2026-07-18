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

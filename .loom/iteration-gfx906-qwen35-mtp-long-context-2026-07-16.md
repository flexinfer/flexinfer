# gfx906 Qwen3.5 native-MTP + long-context certificate

Date: 2026-07-16

## Goal

Certify a modern vLLM path on the 16 GiB Radeon VII (`gfx906`) for a
Qwen3.5-9B GPTQ target with its native one-layer MTP head, then measure an
eager baseline/MTP A/B at increasing context lengths without weakening the
required Vega20 environment contract.

## Riskiest assumption and kill test

Load-bearing assumption: the immutable Qwen3.5-9B W4G128 target plus its BF16
MTP head fits in 16 GiB, and the modern native-gfx906 vLLM/Triton stack can load
that hybrid GDN model with `HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, no GFX
override, and no AOTriton path.

Kill test, in order:

1. Prove the target artifact contains a complete 15-tensor MTP contract.
2. Prove the runtime imports and allocates on the physical gfx906 card.
3. Start an 8K eager baseline server and complete one deterministic request.
4. Start the same server with MTP-1, prove accepted-draft metrics and exact
   greedy parity.
5. Increase the context window only after the 8K gate passes; abort on OOM,
   HSA fault, aperture violation, SIGSEGV, or output divergence.

Failure means no GPUProfile promotion. The fallback remains the already
certified llama.cpp stateful/ngram lane while the failing layer is isolated.

## Negative search

- The existing `qwen35-9b:gptq-w4-g128` artifact was hydrated and inspected.
  It had zero `mtp.*` tensors and no `mtp_num_hidden_layers`; this killed the
  initial assumption before any runtime or GPU mutation.
- The production gfx906 vLLM image is v0.6.3 and does not register Qwen3.5 or
  native MTP. It is not reused for this certificate.
- Upstream vLLM documents AMD Qwen3.5 MTP as under development, so the lane
  remains canary-only until hardware evidence exists.

## Recovery artifact

The official target checkpoint contains 15 MTP tensors, but they span three
5+ GiB source shards. The pinned standalone head
`mlx-community/Qwen3.5-9B-MTP-bf16@22de6695` contains the same 15 tensors in a
single 486 MiB safetensors file. `build/scripts/graft_qwen35_mtp.py` prefixes
those keys with `mtp.`, streams the tensor payload bit-exact, hard-links the
unchanged GPTQ target shards, restores the MTP config contract, and atomically
publishes the sibling directory.

Live graft result:

- target OCI digest: `sha256:d3bf817a6d213c0f127734b6ef8902994a8c6eaae14a0014dce26fd83b1f7acc`
- target index digest: `sha256:bc46f3a8a137b728dd60d84530b20c56283b451bacdcdb82d5f94ab7885479ef`
- MTP source digest: `sha256:c97f1cbac2bef846a2f689108f70ca88bf0d91c4482c46621a86a3ca55dea208`
- MTP payload: 486,581,248 bytes / 15 tensors / BF16
- graft contract digest: `sha256:64189493708ff203f65a08e0ebde92cf9998271212b69cb390173a694f453134`
- live path: `gfx906-qwen35-9b-oci-preflight:qwen35-9b-mtp`

## Runtime candidate

Base:
`mixa3607/vllm-gfx906:0.20.1-rocm-6.3.3-aiinfos@sha256:a5a54ee0494ef0a9a351cf7123620f8fe941492906ecb2afec88b3333dd7a6f0`

The base is built from `ai-infos/vllm-gfx906-mobydick@eb450bdf`,
`triton-gfx906` 3.6, native gfx906 PyTorch, and ROCm 6.3.3. The fork ranks
`TRITON_ATTN` first on gfx906. FlexInfer's thin overlay installs the text-only
Qwen3.5 dense/MoE registration plugin, the established Vega20 memory/init
compatibility hooks, and a consumer-ROCm FLA policy that collapses all 11
loaded GDN autotuners to their conservative minimum-stage/minimum-warp config.
The unbounded autotuners otherwise remained inside `make_amdgcn`; a live
`py-spy` sample identified `chunk_delta_h` after the first KKT-only fix.

Qualified overlay:
`registry.harbor.lan/flexinfer/vllm@sha256:034f081861278a680fe54ddeb71db6446ce65f0a9c37ce9aecc061a99b1d40fc`

## Hardware evidence

### Physical runtime smoke

- device: `gfx906:sramecc+:xnack-`, Radeon VII, 17,163,091,968 bytes
- vLLM: `0.1.dev1+geb450bdfb` (0.20.1 lineage)
- PyTorch: `2.11.0a0+git70d99e9`, HIP 6.3
- patched `torch.cuda.mem_get_info()`: `(17163091968, 17163091968)`
- native FP16 HIP allocation/mean: `1.0`
- dense + MoE plugin registration: pass

### 8K baseline and native MTP

The text-only target requires the same non-MRoPE `hfOverrides` contract as the
existing gfx1100 Qwen3.5 lane. Without it, the first request correctly failed
at `supports_mrope(model)` because the raw artifact retained multimodal
`mrope_section` keys.

With the override and `maxNumBatchedTokens=256`:

- baseline model residency: 7.29 GiB
- baseline KV cache: 6.86 GiB / 56,032 physical tokens
- two greedy baseline requests returned exactly
  ` Paris.\n\nThe capital of France is Paris.`
- MTP model residency: 7.64 GiB; vLLM resolved `Qwen3_5MTP` and shared target
  embedding/lm-head weights with the draft layer
- MTP KV cache: 6.50 GiB / 47,056 physical tokens at 90% utilization
- two MTP requests preserved exact greedy output; warm request: 0.973 seconds
- MTP metrics after two requests: 12 drafts, 8 accepted (66.7%)

### Long-context pressure boundary

At 90% utilization, a 9,001-token request failed on its first 256-token chunk
inside `gptq_gemm` with `hipErrorInvalidValue`. The same shape passed during
startup profiling, before KV allocation. This matches the existing gfx906
GPTQ certificate: Vega20 needs roughly a 1.5–1.8 GiB physical workspace floor
and reports `invalid argument`, not OOM, when KV pages consume that floor.

Reducing only `gpuMemoryUtilization` to 0.80 retained 4.90 GiB / 35,632 KV
tokens and cleared the fault:

- 16K server + 9,001-token real prefill: HTTP 200 in 22.573 seconds
- 32K server + 18,001-token real prefill: HTTP 200 in 43.816 seconds
- both returned four completion tokens and exact usage accounting
- final log scan: no HSA status error, aperture violation, SIGSEGV,
  `AcceleratorError`, OOM, or fatal engine error

The 32K eager/MTP configuration is the certified correctness recipe. Graph
mode and higher throughput remain separate optimizations; this slice does not
promote the modern image as the gfx906 default vLLM runtime.

## Reproduction and restoration

- suspended kill test:
  `deploy/debug/gfx906-qwen35-mtp-long-context-kill-test.yaml`
- image build:
  `build/Dockerfile.vllm-rocm-gfx906-qwen35-mtp`
- graft implementation and unit contract:
  `build/scripts/graft_qwen35_mtp.py`,
  `build/scripts/test_graft_qwen35_mtp.py`
- all live server/smoke/diagnostic Pods were direct debug resources and are
  deleted after evidence capture
- the temporary `ModelCache` parent is deleted after the final manifest and
  repository validation, allowing its controller-owned PVC to clean up

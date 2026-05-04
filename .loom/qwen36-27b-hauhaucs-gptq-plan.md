# Qwen3.6-27B HauhauCS-Aggressive → GPTQ INT4 → vLLM/gfx1100

Status: SCOPING DOC (no commits, no MR). Phase 1 discovery complete. Phase 2 prototype scaffolding written.
Date: 2026-05-03
Worktree: `services/flexinfer/.claude/worktrees/agitated-moore-88b88e`

---

## TL;DR

Two findings reframe the task:

1. **"Qwen3.6" is just a marketing alias for `model_type: qwen3_5`.** The upstream
   `Qwen/Qwen3.6-27B` config.json has `architectures: ["Qwen3_5ForConditionalGeneration"]`
   and `model_type: "qwen3_5"` (text_config.model_type: `qwen3_5_text`). transformers
   has no `qwen3_6` module and never will — Qwen3.6 IS Qwen3.5 modeling code with a
   wider vocab (248320 vs 151936) and head_dim=256.
2. **HauhauCS publishes GGUF only, but `Qwen/Qwen3.6-27B` upstream ships full
   BF16 safetensors (~55.6 GB).** A GGUF→safetensors dequant detour is *only*
   needed if we insist on HauhauCS's specific abliterated weights. Quantizing
   the upstream BF16 with our own abliteration is far cheaper.

Verdict: **GO with caveat — recommend re-routing the work to abliterate-then-quantize
the upstream Qwen3.6-27B, NOT dequant HauhauCS's GGUF.** Project unblocks immediately;
no upstream waits; reuses existing Qwen3.5 patch stack.

If the user *specifically wants HauhauCS's abliteration profile*, we can still build
the GGUF→BF16 path as a follow-up, but it's a strictly larger / harder lane.

---

## 1. Discovery findings

### 1.1 What HauhauCS publishes

Repo: `HauhauCS/Qwen3.6-27B-Uncensored-HauhauCS-Aggressive`
([HF tree](https://huggingface.co/HauhauCS/Qwen3.6-27B-Uncensored-HauhauCS-Aggressive/tree/main))

**Files (all GGUF):**

| File | Size | Notes |
|---|---|---|
| `*-IQ2_M.gguf` | 10 GB | gguf-py does NOT support IQ2_M dequant |
| `*-IQ3_M.gguf` | 12.6 GB | gguf-py does NOT support IQ3_M dequant |
| `*-IQ3_XS.gguf` | 12 GB | IQ3_XXS supported, IQ3_XS not in main quants.py |
| `*-IQ4_XS.gguf` | 15.1 GB | IQ4_XS supported by gguf-py |
| `*-Q2_K_P.gguf` | 11.5 GB | Q2_K supported |
| `*-Q3_K_P.gguf` | 14.3 GB | Q3_K supported |
| `*-Q4_K_P.gguf` | 17.5 GB | Q4_K supported |
| `*-Q5_K_P.gguf` | 20.8 GB | Q5_K supported |
| `*-Q6_K_P.gguf` | 23.2 GB | Q6_K supported |
| `*-Q8_K_P.gguf` | 32 GB | Q8_K supported |
| `mmproj-*-f16.gguf` | 928 MB | Vision projection (multimodal) |

NO `config.json`, NO `tokenizer*`, NO `*.safetensors`. The GGUF metadata embeds
the config + tokenizer (standard llama.cpp practice), but our pipeline expects
a HF-shaped directory layout.

**K_P verification**: HauhauCS readme states *"Fully compatible with llama.cpp,
LM Studio, and any GGUF-compatible runtime — no special builds needed."* and
the imatrix-driven approach + `--tensor-type` overrides in llama-quantize
([llama.cpp quantize docs](https://github.com/ggml-org/llama.cpp/blob/master/tools/quantize/README.md))
strongly suggest K_P files contain only standard llama.cpp tensor types (Q8_K,
Q6_K, Q5_K, …) per-tensor with imatrix weighting. Q8_K_P should be fully
readable by `gguf-py`'s `dequantize()`.

### 1.2 Upstream `Qwen/Qwen3.6-27B` source posture

**Critical reframe**:
[Qwen/Qwen3.6-27B/tree/main](https://huggingface.co/Qwen/Qwen3.6-27B/tree/main)
ships the full base in standard format:

- 15 × `model-NNNNN-of-00015.safetensors` (~55.6 GB total, BF16)
- `config.json` (4.31 kB)
- `tokenizer.json`, `tokenizer_config.json`, `vocab.json`, `merges.txt`
- `chat_template.jinja`, `generation_config.json`
- `preprocessor_config.json`, `video_preprocessor_config.json`

`config.json` (verified content):

```json
{
  "architectures": ["Qwen3_5ForConditionalGeneration"],
  "model_type": "qwen3_5",
  "text_config": {
    "model_type": "qwen3_5_text",
    "num_hidden_layers": 64,
    "hidden_size": 5120,
    "intermediate_size": 17408,
    "head_dim": 256,
    "num_attention_heads": 24,
    "num_key_value_heads": 4,
    "vocab_size": 248320,
    "max_position_embeddings": 262144,
    "rope_parameters": {"rope_theta": 10000000, ...},
    "layer_types": ["linear_attention", "linear_attention", "linear_attention",
                    "full_attention", ...],   // 16× repeat = 64 layers
    "full_attention_interval": 4,
    "tie_word_embeddings": false
  },
  "vision_config": { "model_type": "qwen3_5", "depth": 27, ... }
}
```

**Architecture identity**: `Qwen3_5ForConditionalGeneration` (VLM). Same string,
same module, as Qwen3.5-VL. Our existing Qwen3.5 VLM handling
(commit `5870a6b`, memory note: *"GPTQModel does NOT fully handle Qwen3.5 VLMs
natively — text_config.vocab_size nesting workaround"*) applies verbatim.

### 1.3 transformers version compatibility

Pinned at `build/scripts/quantize_gptq.py:54`:

```
git+https://github.com/huggingface/transformers.git@529504b2fa98970c6c44d3fafaeb07a39c40e7ea
```

Commit date: 2026-03-19. Verified via GitHub API:

```bash
$ curl -s "https://api.github.com/repos/huggingface/transformers/contents/src/transformers/models/qwen3_5?ref=529504b2fa98970c6c44d3fafaeb07a39c40e7ea"
['__init__.py', 'configuration_qwen3_5.py', 'modeling_qwen3_5.py',
 'modular_qwen3_5.py', 'tokenization_qwen3_5.py']
```

Full `qwen3_5` module exists in our pinned commit. `transformers` main also
exposes `qwen3_5` and `qwen3_5_moe` (verified 2026-05-03 via GitHub API).
**No `qwen3_6` module exists in transformers, on main or any tag** — and based
on the upstream config.json saying `model_type: qwen3_5`, no such module is
expected.

`transformers_version` field in upstream config.json: `"4.57.1"` — this is a
hint of the minimum transformers version Qwen used. Our pinned commit is
post-5.0, well above the floor.

### 1.4 GPTQModel support

Pinned at `gptqmodel>=6.0.3` (`build/Dockerfile.quantizer-gptq-rocm:32`).

Verified ([ModelCloud/GPTQModel/tree/main/gptqmodel/models/definitions](https://github.com/ModelCloud/GPTQModel/tree/main/gptqmodel/models/definitions)):

- `qwen3.py`, `qwen3_moe.py`, `qwen3_5.py`, `qwen3_5_moe.py`,
  `qwen3_next.py`, `qwen3_omni_moe.py`, `qwen3_vl.py`
- **No `qwen3_6.py`** — confirmed via web search 2026-05-03.

Existing pipeline behavior (`build/scripts/quantize_gptq.py:2820-2830`):

```python
getattr(config, "model_type", "") == "qwen3_5_text"
# GPTQModel currently maps qwen3_5_text to the multimodal loader even
# when text_config has been promoted...
# Override loader to text-only via gptqmodel.models.definitions.qwen3_5
```

This override is exactly what we need for Qwen3.6-27B: text_config promotion +
text loader.

### 1.5 Pipeline routing — abliteration-skip path

`controllers/modelcache_controller.go:173` — pipeline routing iterates
specs and skips phases where the pointer is nil.
`controllers/modelcache_abliteration.go:116` skips abliteration if
`Spec.Abliteration == nil` and dispatches to quantization (or download-only
Ready).

Reference for download-only modelcache:
`deploy/modelcaches/qwen3-30b-a3b-gptq-int4.yaml` (lines 78–90 in repo).

Reference for download → abliterate → GPTQ → publish:
`deploy/modelcaches/qwen35-9b-gptq-gfx1100.yaml`.

**For the HauhauCS lane (use HauhauCS GGUF directly)**: the spec needs a
`download` step that fetches a SINGLE GGUF (no abliteration), then a NEW
"dequant" preprocess step that emits BF16 safetensors, then quantization.

**For the Qwen3.6-27B-base lane (recommended)**: existing
`Download → Abliterate → Quantize → Publish` shape works unchanged. Only the
`source` field changes. ModelCache CR is structurally identical to
`qwen35-9b-gptq-gfx1100.yaml`.

### 1.6 GGUF → BF16 dequant tool inventory

| Tool | License | Q8_K | Q6_K | IQ4_XS | IQ3_M | IQ2_M |
|---|---|---|---|---|---|---|
| `gguf-py` (llama.cpp official) | MIT | ✓ | ✓ | ✓ | ✗ | ✗ |
| `city96/ComfyUI-GGUF/dequant.py` | Apache-2.0 | ✗ | ✓ | ✓ | ✗ | ✗ |
| `99991/pygguf` | MIT (unmaintained) | partial | partial | ? | ? | ? |

For Q8_K_P specifically: **gguf-py's built-in `dequantize(data, qtype)`
function in `gguf-py/gguf/quants.py` covers Q8_K natively.** No new tool to
build — we'd just wrap it.

For HauhauCS's IQ2_M / IQ3_M variants: out of luck without porting llama.cpp
C dequant kernels to NumPy. We would have to call `llama-quantize --allow-requantize`
CLI to convert HauhauCS Q8_K_P → BF16, which means adding a llama.cpp build
into the dequant container. Larger surface area than reusing existing
Python-only quantizer container.

---

## 2. Risk register + go/no-go

### Path A — Recommended: abliterate-then-GPTQ from Qwen/Qwen3.6-27B BF16

| Risk | Severity | Verdict | Notes |
|---|---|---|---|
| transformers lacks Qwen3.6 model code | **NONE** | GO | Qwen3.6 = Qwen3.5; existing module covers it |
| GPTQModel lacks Qwen3.6 def | **NONE** | GO | Same reason; `qwen3_5.py` definition applies |
| VLM architecture string `Qwen3_5ForConditionalGeneration` | **LOW** | GO | Already handled in our pipeline (commit `5870a6b`, text_config promotion) |
| vocab_size 248320 vs 151936 | **LOW** | GO | embed_tokens / lm_head shapes differ — automatic; no patch surface affected |
| head_dim=256 (vs 128 for many qwen3 variants) | **LOW** | GO | Read from config; no code changes |
| `attn_output_gate: true` (Gated-Attention) | **LOW** | GO | Already in `qwen3_5` modeling; vLLM patches in `vllm_qwen35_patches.py` cover |
| Native context 262144 — too large for our 24 GiB card | **MEDIUM** | GO with cap | Cap maxModelLen=8192 initially (mirrors qwen3-30b-a3b canary) |
| 27B BF16 source = 55.6 GB to download/store | **MEDIUM** | GO with sizing | storageSize=120Gi (BF16 + GPTQ-out + headroom). Same envelope as our 27B opus distill flow |
| 27B abliteration timing on gfx1100 24GiB | **HIGH** | GO with offload | Memory note: "ABLITERATION_GPU_MAX_MEMORY_GB=12" + ~56Gi CPU. Same playbook as Qwen3.5-27B opus distill abliteration |
| `ablitateLmHead: true` corrupts streaming save | **MEDIUM** | GO | Memory note + CRD field already wired to `false` by default. Keep disabled |
| GPU contention on cblevins-7900xtx (gemma4-31b warm primary) | **HIGH** | NEEDS DECISION | See open questions §5 |

**Path A verdict: GO.** Best-case: 1.5 dev days + 12 GPU-hours. Worst-case: 4 dev days
+ 24 GPU-hours.

### Path B — HauhauCS GGUF dequant route (if user insists on HauhauCS's abliteration)

| Risk | Severity | Verdict | Notes |
|---|---|---|---|
| Extracting config.json + tokenizer from GGUF metadata | **MEDIUM** | GO | gguf-py `GGUFReader.fields` exposes metadata key-value pairs; tokenizer can be reconstructed from `tokenizer.ggml.tokens` + `tokenizer.ggml.model` keys, OR copied from `Qwen/Qwen3.6-27B` upstream |
| Dequant numerical error (Q8_K_P → BF16 loses ~0.5–1.0% PPL) | **MEDIUM** | GO with eyes-open | Then GPTQ INT4 on top adds another ~1% PPL loss. Compound error: 1.5–2% vs upstream |
| Q8_K dequant performance (32 GB GGUF, all tensors) | **LOW** | GO | NumPy vectorized, ~5–15 min per shard on a 5930k-class CPU |
| Q8_K_P → BF16 disk footprint = ~108 GB | **HIGH** | NEEDS storage decision | 32 GB GGUF + 54 GB BF16 + 14 GB GPTQ output ≈ 100 GB. PVC must be 120+ Gi |
| llama.cpp build inside dequant container | **LOW** if we stick to gguf-py-only | GO | Avoid by limiting to Q8_K_P (NOT IQ2_M / IQ3_M) |
| Dequant tool maintenance (test parity vs upstream) | **MEDIUM** | OK | New `--test-roundtrip` mode + a unit fixture |

**Path B verdict: GO but slower.** Best-case: 4 dev days + 18 GPU-hours.
Worst-case: 8 dev days + 36 GPU-hours, plus the 1.5–2% PPL hit.

### Path C — Use HauhauCS GGUF directly via llama.cpp backend (no GPTQ)

Out of scope for the prompt, but worth flagging as the cheapest possible
delivery: pin the Q4_K_P (17.5 GB) or Q5_K_P (20.8 GB) GGUF on a llamacpp-rocm
backend Model CR. ~30 min of work, zero pipeline changes. Gives HauhauCS
abliteration directly. **Trades**: no FP8 KV, no gptq exllama kernel speedup,
slower decode (~25–35 tok/s expected vs 60–70 for GPTQ on this card).

---

## 3. Implementation plan

### Path A (recommended) work breakdown

| File | Size estimate | Type | Depends on |
|---|---|---|---|
| `deploy/modelcaches/qwen36-27b-gptq-gfx1100.yaml` | ~80 lines | NEW | none |
| `deploy/models/qwen36-27b-gptq.yaml` | ~120 lines | NEW | modelcache yaml above |
| `deploy/modelcaches/kustomization.yaml` | +1 line | EDIT | yaml above |
| `deploy/models/kustomization.yaml` | +1 line | EDIT | yaml above |
| `build/scripts/quantize_gptq.py` | +0 lines | unchanged | Qwen3.5 path covers Qwen3.6 |
| `build/scripts/abliterate.py` | +0 lines (likely) | unchanged | text_config / VLM handling identical |
| `build/scripts/vllm_qwen35_patches.py` | maybe +5 lines | possible EDIT | rope_theta=10000000 vs 1000000 may need branch |

The only patches we *might* need beyond config:

1. **vLLM `rope_theta` selection**: `vllm_qwen35_patches.py` may hardcode the
   Qwen3.5 default rope_theta. Qwen3.6 uses `rope_theta=10000000` (10M, vs
   Qwen3.5's 1M). Verify by grep before quantizing. If hardcoded, parameterize.
2. **chat_template.jinja**: copy upstream verbatim into the GPTQ output dir
   so vLLM tool-calling template lookup works. Existing pipeline already
   preserves it (`abliterate.py` save section + `quantize_gptq.py` does not
   touch tokenizer/template files).

### Path B work breakdown (if requested)

| File | Size | Type | Depends on |
|---|---|---|---|
| `build/scripts/gguf_to_bf16_safetensors.py` | ~250 lines | NEW | gguf-py |
| `build/Dockerfile.gguf-dequant` | ~30 lines | NEW | base: python:3.10-slim or rocm/pytorch |
| `pkg/quantization/gguf_dequant.go` | ~150 lines | NEW | controller integration |
| `controllers/modelcache_gguf_dequant.go` | ~250 lines | NEW | new pipeline phase |
| `api/v1alpha1/modelcache_types.go` GGUFDequantSpec | +30 lines | EDIT | CRD regen |
| `charts/flexinfer/crds/*.yaml` | regen | EDIT | `make manifests` |
| `deploy/modelcaches/qwen36-27b-hauhaucs-aggressive.yaml` | ~100 lines | NEW | dequant phase |
| `deploy/models/qwen36-27b-hauhaucs-aggressive.yaml` | ~120 lines | NEW | modelcache |
| `build/scripts/quantize_gptq.py` | +0 lines | unchanged | as Path A |
| `build/scripts/vllm_qwen35_patches.py` | maybe +5 lines | possible EDIT | as Path A |

Critical sub-task: tokenizer extraction from GGUF metadata. The GGUF format
encodes `tokenizer.ggml.model="gpt2"` (BPE) plus `tokenizer.ggml.tokens` (list)
and `tokenizer.ggml.merges`. Reconstructing the HF `tokenizer.json` is doable
but fragile — easier to copy tokenizer files from `Qwen/Qwen3.6-27B` upstream
(only ~25 MB) and trust they match (HauhauCS only modified weights, not
vocab).

### Time estimate

**Path A:**
- Plan + scaffolding (this doc): done — 1 hour
- ModelCache + Model CR draft: 30 min
- Smoke deploy (download → abliterate → GPTQ → vllm load): single-cycle 8–14 hours of GPU time on cblevins-7900xtx
- Validation / cosine / coherence smoke: 1 hour
- **Best case**: 12 GPU-hours total + 1 dev day
- **Worst case**: 24 GPU-hours (one re-quant cycle for an abliteration norm-threshold hit) + 4 dev days

**Path B:**
- Add to Path A: dequant tool, dequant container build, controller plumbing,
  CRD regen, integration test
- **Best case**: 18 GPU-hours + 4 dev days
- **Worst case**: 36 GPU-hours + 8 dev days

---

## 4. Open decisions for the user (top of mind)

1. **Path A vs Path B.** Is the value here HauhauCS's *specific* abliteration
   profile (path B) or a working Qwen3.6-27B GPTQ on gfx1100 (path A)? If the
   user has not actually compared HauhauCS Aggressive output to upstream
   Qwen3.6 + our standard abliteration recipe, A is the right default.

2. **Storage class + node placement for the quantize job.** cblevins-7900xtx
   already runs gemma4-31b-gptq (priority 250) and qwen3-30b-a3b-gptq-int4
   (priority 200). Putting a 12-hour quantize job on this card preempts both.
   Options:
   - (a) Run quantize on cblevins-5930k (gfx1100, no warm primary right now if
     FluxPony is idle). Same arch, same image, no patch changes. Then publish
     to OCI and pull onto 7900xtx.
   - (b) Run quantize on cblevins-7900xtx during a planned maintenance window
     (suspend gemma4, suspend qwen3-30b-a3b, run job, resume).
   - (c) Run quantize on radeonvii (gfx906). Slower, has its own image, but
     doesn't compete for the 7900 XTX cards.
   Recommend (a) for speed and zero impact to warm models.

3. **Variant scope.** Build the "balanced" HauhauCS variant too, or just
   "aggressive"? If yes, identical pipeline x2 — only the source URL differs.
   This doubles GPU time for marginal benefit unless A/B comparison is the
   actual ask.

---

## 5. Files written by this scoping pass

All written as untracked files in this worktree. NOT added to any
kustomization.yaml. NOT committed.

- `.loom/qwen36-27b-hauhaucs-gptq-plan.md` — this doc
- `build/scripts/gguf_to_bf16_safetensors.py` — Phase 2.1 prototype (Path B)
- `build/scripts/vllm_qwen36_patches.py` — Phase 2.2 stub (TODO-marked,
  enumerates Qwen3.5 patch sites; ports are mostly no-ops since model_type
  IS qwen3_5)
- `deploy/modelcaches/qwen36-27b-gptq-gfx1100.yaml.draft` — Path A modelcache
  draft
- `deploy/models/qwen36-27b-gptq.yaml.draft` — Path A model serving draft
- `deploy/modelcaches/qwen36-27b-hauhaucs-aggressive.yaml.draft` — Path B
  modelcache draft (HauhauCS dequant flow)
- `deploy/models/qwen36-27b-hauhaucs-aggressive.yaml.draft` — Path B model
  serving draft

`.draft` suffix is intentional — these will not be picked up by Flux even if
someone accidentally runs `kubectl apply -k deploy/`.

---

## 6. Sources / evidence

- HauhauCS GGUF tree:
  https://huggingface.co/HauhauCS/Qwen3.6-27B-Uncensored-HauhauCS-Aggressive/tree/main
- Qwen/Qwen3.6-27B config.json (raw, fetched 2026-05-03):
  https://huggingface.co/Qwen/Qwen3.6-27B/resolve/main/config.json
- Qwen/Qwen3.6-27B file tree:
  https://huggingface.co/Qwen/Qwen3.6-27B/tree/main
- transformers main `qwen3_5` module:
  https://github.com/huggingface/transformers/tree/main/src/transformers/models/qwen3_5
- transformers pinned commit `529504b2…` model files:
  https://api.github.com/repos/huggingface/transformers/contents/src/transformers/models/qwen3_5?ref=529504b2fa98970c6c44d3fafaeb07a39c40e7ea
- GPTQModel definitions inventory:
  https://github.com/ModelCloud/GPTQModel/tree/main/gptqmodel/models/definitions
- gguf-py `quants.py` dequant coverage:
  https://github.com/ggml-org/llama.cpp/blob/master/gguf-py/gguf/quants.py
- city96/ComfyUI-GGUF dequant.py (reference for IQ4 family):
  https://github.com/city96/ComfyUI-GGUF/blob/main/dequant.py
- llama.cpp quantize tool / `--tensor-type` (basis for K_P claim):
  https://github.com/ggml-org/llama.cpp/blob/master/tools/quantize/README.md
- Workspace memory anchors used (CLAUDE.md MEMORY.md):
  - `Qwen3.5 VLM quantization … extract text_config to top level …` (commit
    `5870a6b`)
  - `ablitateLmHead must be false` (2026-04-01) — corruption via streaming
    safetensors save
  - `GPTQ with sym=true → ExllamaLinearKernel = 72-73 tok/s decode on gfx1100`
  - `cblevins-7900xtx: 1 discrete 7900 XTX + 1 iGPU; gpu.count=2 + hipVisibleDevices=0`

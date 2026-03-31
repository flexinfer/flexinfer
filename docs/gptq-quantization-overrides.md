# GPTQ Quantization Pipeline: Runtime Overrides Reference

This document catalogs all runtime overrides, monkey-patches, and non-obvious
configuration in the flexinfer GPTQ quantization pipeline. These exist because
GPTQModel + ROCm + Qwen3.5 hybrid architecture require extensive workarounds.

## Quick Reference

| Override | Where | Why |
|----------|-------|-----|
| torchao removal | `gptq.go:615-624` | SIGABRT on torch dev builds |
| gptqmodel pip install | `gptq.go:654-660` | Image may lack gptqmodel |
| pip-show dep guard | `gptq.go:662-669` | Python import guard SIGABRT |
| CPU device map (gfx906+gfx1100) | `gptq.go:153-164` | Meta tensor crash |
| writer.py ZeroDivisionError | `gptq.go:300-305` | Cleanup race on save |
| loader.py direct CPU path | `gptq.go:312-350` | GPTQModel re-injects device_map |
| VLM text_config extraction | `gptq.go:352-390` | Qwen3.5 VLM nested vocab_size |
| Meta-device conditional loading | `gptq.go:401-513` | `from_config()` for CPU path |
| HSA_OVERRIDE_GFX_VERSION | `gptq.go:516-532` | Radeon VII reports as gfx900 |
| MAGMA/LAPACK/scipy fallback | `gptq.go:660-805` | ROCm images lack MAGMA |
| Triton cache lock patch | `gptq.go:662-698` | FLA + GPTQModel race |
| torchao skip (gfx906) | `gptq.go:640-651` | SIGILL on Broadwell |
| Hessian repair config | `gptq.go:142-149` | Singular Hessian matrices |
| pypcre stdlib shim (gfx906) | `gptq.go:646-651` | SIGILL on Broadwell |

## Dependency Bootstrap Flow

```
1. Remove torchao (SIGABRT on torch dev builds)
2. gfx906: replace pypcre with stdlib re shim
3. pip show gptqmodel → install if missing (--no-build-isolation --no-deps)
4. pip show deps → install if missing (tokenicer, kernels, accelerate, etc.)
5. MAGMA/LAPACK fallback script → patches torch.linalg
6. Triton cache lock patches → fixes FLA/GPTQModel interaction
7. Run quantize_gptq.py via _magma_fallback.py wrapper
```

## Image Resolution Precedence

When selecting the quantizer image, the controller uses this chain (highest wins):

1. `GPUProfile.spec.quantization.images.gptq` (per-GPU-arch override)
2. `FLEXINFER_USE_RUNTIME_FOR_QUANTIZE=true` + `FLEXINFER_RUNTIME_IMAGE`
3. Arch-specific env: `FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE`
4. Generic vendor env: `FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE`
5. Hardcoded default: `DefaultGPTQROCmGFX906Image`

Source: `pkg/quantization/image.go:22-44`

## Abliteration Overrides (abliteration.go)

### gfx906-Specific (Radeon VII)

| Override | Lines | Purpose |
|----------|-------|---------|
| `torch.cuda.mem_get_info` shim | 494-517 | VMM unsupported on Vega20 |
| `caching_allocator_warmup` no-op | 518-523 | Warmup exceeds 16GB VRAM |
| Safe sharded load (full from_pretrained replacement) | 524-664 | Avoids meta tensors entirely |
| VLM key remapping (`model.language_model.` → `model.`) | 579-593 | Checkpoint uses VLM prefix |
| `no_split_module_classes` for GDN layers | 604-610 | Prevents orphan param dispatch |
| Post-dispatch param fixup | 642-661 | Moves orphaned `dt_bias`/`A_log` |

### Environment Variables

| Env Var | Default (gfx906) | Purpose |
|---------|-------------------|---------|
| `ABLITERATION_GPU_MAX_MEMORY_GB` | 14 | Headroom for 16GB VRAM |
| `ABLITERATION_CPU_MAX_MEMORY_GB` | 56 | Avoid disk offload stall |
| `ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP` | true | Skip GPU warmup crash |
| `ABLITERATION_SAFE_SHARDED_LOAD` | true | Use custom loader |
| `SAFETENSORS_FAST_GPU` | 0 | No GPU-direct safetensors |
| `HF_SAFETENSORS_MMAP` | 0 | No NFS mmap |

## Qwen3.5 Model Policy

The `gptqModelPolicies` in `values-k3s.yaml` defines special handling for
Qwen3.5 models. Key fields:

| Field | Value | Why |
|-------|-------|-----|
| `extract_text_config` | true | VLM nests `vocab_size` in `text_config` |
| `remap_model_type` | `qwen3_5_text` | Avoid VLM loader (`ForConditionalGeneration`) |
| `architectures` | `[Qwen3_5ForCausalLM]` | Text-only, not VLM |
| `loader` | `manual_sharded_state_dict` | Default path crashes on meta tensors |
| `python_packages` | transformers@529504b | Pin for Qwen3.5 support |
| `disable_qwen35_fla` | true | FLA Triton kernels crash on ROCm |
| `attn_implementation` | eager | No flash attention on gfx906 |

## vLLM Runtime Patches (vllm_qwen35_patches.py)

16 patches for Qwen3.5 on vLLM/ROCm. Highlights:

- **Triton 3.4.0 pin**: 3.2.0 breaks unified attention, 3.5.1 crashes FLA
- **FLA fp32 cast wrappers**: ROCm Triton lacks bf16 math ops
- **Register `qwen3_5_text` config**: GPTQ remaps model_type
- **Naive PyTorch FLA kernels**: Triton FLA produces near-zero on gfx1100
- **RMSNormGated native fallback**: Triton gated norm incorrect on ROCm
- **M-RoPE removal**: Triggers SupportsMRoPE assertion crash
- **decoder_sparse_step fix**: Must be 4 (every 4th = full attention)

Full list: `build/scripts/vllm_qwen35_patches.py`

## CRD Fields (ModelCache)

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `skipGDNLayers` | *bool | true | Only abliterate full-attention layers |
| `normThreshold` | *string | "100" | Abort if refusal norm exceeds |
| `ablitateLmHead` | *bool | true | Abliterate output projection |
| `targetLayers` | *string | "auto" | Layer selection (e.g. "27,31,35") |
| `dynamicExclusion` | *string | "auto" | GPTQ exclusion pattern |

## Known Constraints

- **gfx906**: VMM unsupported → no `torch.cuda.mem_get_info`, no GPU allocations > VRAM
- **gfx1100**: torchao crashes on torch dev builds → removed at script start
- **NFS PVC**: Save is slow (~30 min for 27GB), mmap unreliable
- **Qwen3.5 GDN**: 48 linear-attention + 16 full-attention layers; abliterate only full-attention
- **GPTQModel on ROCm**: Pure Python (no native extensions), needs `--no-build-isolation --no-deps`

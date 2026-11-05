Follow-ups to the vLLM render currency change. The `vllm-omni` backend now
honours `spec.config.vllmVersion` like `vllm`: from 0.32 it drops
`VLLM_USE_TRITON_FLASH_ATTN`, which vLLM no longer defines, and the controller
refuses an unreadable `vllmVersion` on a `vllm-omni` Model with `ConfigInvalid`.
`enableAiter` now renders `VLLM_ROCM_USE_AITER=1` only on CDNA 3 or newer
(gfx942, gfx950) on both backends; on gfx1100 it was a no-op and now renders
nothing (`rocmUseAiter` and `disableAiter` stay literal overrides). The D4
structured-output gate never certifies LM-Format-Enforcer, which vLLM 0.33
deprecates, or any backend other than `auto`; the renderer drops such a backend,
and `make certify-matrix` shows a vLLM certificate naming LM-Format-Enforcer as
`unsupported`. No lane changes: none sets `enableAiter`, the one `vllm-omni` lane
sets neither `enableFlashAttention` nor `vllmVersion`, and the vLLM render golden
is byte-identical.

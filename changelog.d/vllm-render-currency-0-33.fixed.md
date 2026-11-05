The vLLM backend now renders for the vLLM release a lane declares. The new
`spec.config.vllmVersion` key (e.g. `"0.33.0"`) is controller metadata, like
`llamacppBuild`: it never reaches the server. With it set, the backend drops
`VLLM_*` variables that release's `envs.py` no longer defines
(`VLLM_ROCM_USE_AITER_PAGED_ATTN` and `VLLM_USE_TRITON_FLASH_ATTN` from 0.32,
`VLLM_ROCM_USE_AITER_FP4_ASM_GEMM` from 0.33), which `--fail-on-environ-validation`
turns into a hard start failure, and stops rendering `--mamba-cache-mode` from
0.33, where it is a deprecated no-op that 0.34 removes. The controller now
refuses `maxNumPartialPrefills` and `maxLongPartialPrefills` with
`ConfigInvalid`: their flags exist in no vLLM release, so a lane that set them
would abort in argument parsing. New pass-through keys, rendered only when set:
`prefixMatchUnit`, `enableMambaSharedPrefixCheckpoint`, `kvTransferConfig` and
`sleepPreserveParameterNames`. No lane changes: none sets the new or refused
keys, and a golden test (`backend/testdata/vllm-render-golden.json`) pins the
command line and env of all 56 vLLM lanes in `deploy/models`, `deploy/debug`
and `deploy/experiments` byte for byte.

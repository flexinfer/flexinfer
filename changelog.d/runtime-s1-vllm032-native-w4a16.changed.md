The gfx1100 `gfx1100-native-w4a16` runtime profile, its Dockerfile and the
`publish_serving_rocm_gfx1100_native_w4a16` CI job now track vLLM 0.32.0
(base `vllm/vllm-openai-rocm:v0.32.0`, tag `rocm-gfx1100-v032-native-w4a16`),
slice S1 of `.loom/10-research-gfx1100-next-feature-2026-10-23.md`. The vLLM
plugins were re-ported for 0.32: `flexinfer_vllm_common` 0.2.0 only copies
hybrid-state helpers a class does not already resolve, and
`flexinfer_vllm_qwen35_text` 0.9.0 skips the Qwen3.5 text architectures when
vLLM registers them itself and gains `FLEXINFER_QWEN35_MTP_HEAD=checkpoint` to
leave the MTP head's quantization to the checkpoint, and
`flexinfer_vllm_qwen35_seqcls` 0.2.0 keeps the upstream M-RoPE implementation
when the generative base already provides one. The vLLM backend no longer
emits `--no-calculate-kv-scales` for `calculateKvScales: false`: vLLM 0.28
removed the option, false is the default on every runtime, and the negative
flag would have failed argument parsing on 0.28+. The debug canary
`deploy/debug/qwen38-27b-v032-canary.yaml` passed the MTP-off qualification
on the 5930k on 2026-10-24 (matrix row `gfx1100-native-w4a16-v032-2026-10-24`:
identical probes, 41.5 tok/s, pool 2.15x at 64K, 0 restarts); production lanes
stay on the 0.26 digest until the promotion slice.

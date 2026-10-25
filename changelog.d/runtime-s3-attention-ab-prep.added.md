`scripts/verify-vllm-mtp.py` gains a context sweep (`--contexts`, e.g.
`512,2048,4096,16384,28672`): one single-stream request per prompt size with
a unique prefix, reporting cold-prefill TTFT, prefill and decode tok/s and
that request's own draft acceptance from the spec-decode counter deltas, plus
a `--phase measure` that gates coherence only for A/B arms. The debug canary
`deploy/debug/qwen38-27b-v032-s3-attn-canary.yaml` stages slice S3 of the
gfx1100 program: ROCM_SEGMENTED_ATTN (fp16 KV, autotune on) against
TRITON_ATTN (fp8 KV), each with MTP K=2 and off, at 32K on the 5930k, with
the sweep points and the decision rule taken from the workhorse's 7-day prompt
histogram (p50 466, p90 4,260 tokens).

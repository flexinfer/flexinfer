The runtime image entrypoint no longer rewrites every Qwen3.5 / Gemma4 `config.json` under
`/models` when it starts the per-node runtime manager (`flexinfer-runtime`). In that mode
`/models` is the node-wide hostPath cache, and the 2026-09-13 S1 roll on the 7900 XTX
rewrote the co-located dedicated Qwen3.8 lane's `architectures` to the text-only class, which
vLLM 0.23 then refused on its first request ("M-RoPE support is not implemented"). Dedicated
model pods (where `/models` is the pod's own cache) keep the normalization unchanged; runtime
profiles can opt back in with `FLEXINFER_RUNTIME_NORMALIZE_MODEL_CONFIGS=1`.

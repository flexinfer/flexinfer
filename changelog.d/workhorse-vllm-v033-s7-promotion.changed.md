The gfx1100 workhorse (`qwen38-27b-autoround-workhorse`) moves to vLLM 0.33.0-rocm72 on
`runtime@51afd0d3` (`rocm-gfx1100-serving-v033-3e8ae3c2`), with `vllmVersion: "0.33.0"`. It also serves its
2.37 GiB input-embedding table from mapped host memory (`FLEXINFER_EMBED_HOST`, S7), which grows the KV pool, and
opts in to certified structured outputs. The envelope stays 32K × 2. Rollback is `b06b06fe` with the S4 config.

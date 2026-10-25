The three demand-only 5930k lanes that lost their vLLM 0.23 runtime to Harbor retention,
`qwen36-27b-autoround-workhorse`, `qwen36-27b-fable-gptq` and `qwen35-9b-ablit-rp-5930k`, start
again: they run the CI-built vLLM 0.32 native-W4A16 image (`runtime@sha256:b06b06fe…`, !1166)
without `calculateKvScales`, and each passed a boot and probe on 2026-10-25. The Qwen3.6 lane now
uses 0.96 GPU memory (!1167) because vLLM 0.32 counts CUDA graph memory and left one 64K sequence
0.01 GiB short at 0.94. The Fable lane no longer uses the flash loader (!1168), whose tmpfs copy
of its 17 GB of shards could not fit the 5930k's 7.7 GB budget.

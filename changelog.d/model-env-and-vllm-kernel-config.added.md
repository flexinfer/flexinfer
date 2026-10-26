Models can set environment variables for their backend process with
`spec.config.env`, a map of names to string, number or boolean values. It
applies on both the dedicated-Deployment and runtime-pod launch paths and
overrides the backend's defaults and the GPUProfile env, which replaces the
`bash -lc 'export …; exec …'` command wrappers used to pass, for example,
`FLEXINFER_QWEN35_MTP_HEAD=checkpoint` to the vLLM text plugin. Device
visibility, `PORT` and the compilation and HF cache paths stay controller-owned:
a Model that names one fails validation with the new `ConfigInvalid` reason. The
vLLM backend also renders `kernelConfig` as `--kernel-config` (for example
`{enable_rocm_segmented_attn_autotune: true}` with `ROCM_SEGMENTED_ATTN`).

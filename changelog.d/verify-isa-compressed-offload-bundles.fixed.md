The build-time ISA check (`build/scripts/verify_vllm_gfx1100_native_w4a16.py`) reads the target
list of every offload bundle in each vLLM extension's `.hip_fatbin` instead of byte-scanning the
file for `amdgcn-amd-amdhsa--gfx1100`. vLLM 0.33's `_rocm_C` ships zstd-compressed (`CCOB`) bundles,
which hid the triple and failed the check on a wheel that does carry gfx1100. Plain bundles are
parsed in Python; compressed ones are sliced by their header size and listed with
`clang-offload-bundler --list`. Every bundle must now name the target, so a wheel with one
translation unit built without gfx1100 also fails. Checked on the 0.32.0 (`527e2714`) and
0.33.0-rocm72 images: both pass for gfx1100 and fail for gfx1102.

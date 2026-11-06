"""Build the RDNA3 W4A16 GEMM op library against the image's own torch.

Install with ``pip install --no-deps --no-build-isolation .`` inside the vLLM
ROCm image (it needs that image's torch, hipcc and ROCm headers; there is no
build isolation to fetch them). torch's cpp_extension hipifies the .cu
sources the same way vLLM's CMake does.

The device flags repeat vLLM's for _rocm_C (cmake/utils.cmake
get_torch_gpu_compiler_flags, CMAKE_BUILD_TYPE=Release) so the vendored kernel
compiles to the same code as vLLM's; the macros rename the vendored symbols
(see csrc/bindings.cpp). Only gfx1100 is built.
"""

import os

from setuptools import setup
from torch.utils.cpp_extension import BuildExtension, CUDAExtension

ARCH = os.environ.get("FLEXINFER_RDNA3_W4A16_ARCH", "gfx1100")
# cpp_extension reads the offload targets from PYTORCH_ROCM_ARCH.
os.environ["PYTORCH_ROCM_ARCH"] = ARCH

RENAMES = [
    "-Dvllm=flexinfer_rdna3_vllm",
    "-Dgptq_gemm_rdna3=flexinfer_gptq_gemm_rdna3",
    "-Dgptq_gemm_rdna3_wmma=flexinfer_gptq_gemm_rdna3_wmma",
]

# vLLM builds _rocm_C as CMake RelWithDebInfo (setup.py default_cfg), i.e.
# -O2 -g -DNDEBUG, with CMAKE_HIP_STANDARD 20. -O3 compiles the same source to
# different code (scheduling, unrolling at M_COUNT = 8), so match it exactly:
# the persistent-ticket kernel must produce vLLM's bits.
DEVICE_FLAGS = [
    "-O2",
    "-g",
    "-DNDEBUG",
    "-std=c++20",
    "-DTORCH_EXTENSION_NAME=_rocm_C",
    "-DVLLM_ROCM_GFX1100",
    "-DUSE_ROCM",
    "-DENABLE_FP8",
    "-U__HIP_NO_HALF_CONVERSIONS__",
    "-U__HIP_NO_HALF_OPERATORS__",
    "-Werror=unused-variable",
    "-fno-gpu-rdc",
] + RENAMES

setup(
    ext_modules=[
        CUDAExtension(
            name="flexinfer_vllm_rdna3_w4a16._C",
            sources=[
                "csrc/bindings.cpp",
                "csrc/q_gemm_rdna3.cu",
                "csrc/q_gemm_rdna3_dot2.cu",
            ],
            extra_compile_args={
                "cxx": ["-O3", "-DUSE_ROCM"],
                "nvcc": DEVICE_FLAGS,
            },
            # Bind the library's references to its own definitions, never to a
            # same-named symbol another library exported first.
            extra_link_args=["-Wl,-Bsymbolic"],
        )
    ],
    cmdclass={"build_ext": BuildExtension},
)

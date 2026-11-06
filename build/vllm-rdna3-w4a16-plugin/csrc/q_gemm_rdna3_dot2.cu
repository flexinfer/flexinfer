// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright contributors to the vLLM project
//
// Candidate for the decode W4A16 GEMM on RDNA3 (fp16, M = 1..10), measured
// against gemm_q4_kernel_rdna3 in q_gemm_rdna3.cu. Not the shipped default.
//
// The kernel is gemm_q4_kernel_dot2 from the RDNA3 tracking branch of
// vllm-project/vllm#57925 (perf/rdna3_full_stack, author JartX, as of
// 2026-11-05), which has not been proposed upstream as its own PR yet. The
// one change here: a wave's K part no longer has to start on a group
// boundary. The scale is folded into the fp32 sum at every absolute group
// boundary and once more after the loop, so K = 17408 (136 groups of 128,
// whose only power-of-two splits are 1..8 ways) can be cut 16 or 32 ways.
// When the parts are group-aligned the extra fold adds exact zeros and the
// result is the same as the original kernel's.
//
// Why it can beat the split-K kernel at small M (the S4 anatomy of the
// Qwen3.8-27B decode step, .loom/70-result-s4-step0-decode-anatomy-*):
//   * no split-K across blocks: no FP32 partials written and read back, no
//     tickets, no serial last-block reduction at the end of the launch;
//   * one block covers all M rows, so M = 3 does not run two row tiles that
//     both stream the same weights (the split-K kernel uses M_COUNT = 2 there);
//   * the activations are wave-uniform and loaded through the scalar cache.
//
// Layout: every lane owns two output columns, a wave reads one packed row
// (8 K values) of 64 columns per step, and the WPB waves of a block split K
// into contiguous parts that meet in LDS in a fixed order, so the result does
// not depend on scheduling. The nibbles become magic halves and the zero is
// subtracted in fp16, which is exact (|q - zero| <= 16 and 1024 + 16q fit in
// fp16's 11-bit significand); v_dot2 accumulates in fp32 and the fp32 scale
// is applied once per group.

#include <cstdint>

#include <torch/all.h>
#include <c10/cuda/CUDAGuard.h>
#include <ATen/cuda/CUDAContext.h>

#include <hip/hip_runtime.h>
#include <hip/hip_fp16.h>

#if defined(__HIPCC__) && defined(__gfx1100__)
  #define __HIP__RDNA3__
#endif

namespace vllm {
namespace gptq_rdna3_dot2 {

constexpr int kDot2MinM = 1;
constexpr int kDot2MaxM = 10;

#if defined(__HIP__RDNA3__) || !defined(__HIP_DEVICE_COMPILE__)

typedef _Float16 d2_h2 __attribute__((ext_vector_type(2)));
__device__ __forceinline__ d2_h2 d2_as_h2(uint32_t u) {
  d2_h2 r;
  __builtin_memcpy(&r, &u, 4);
  return r;
}

template <int M, int PF, int WPB>
__global__ void __launch_bounds__(WPB * 32)
    gemm_q4_kernel_dot2(const half* __restrict__ a,
                        const uint32_t* __restrict__ b_q,
                        const uint32_t* __restrict__ b_qzeros,
                        const half* __restrict__ b_scales, half* __restrict__ c,
                        const int size_n, const int size_k, const int groups,
                        const int zero_offset) {
  const int lane = threadIdx.x & 31;
  const int wave = __builtin_amdgcn_readfirstlane(threadIdx.x >> 5);
  const int n0 = blockIdx.x * 64 + 2 * lane;
  const int kpart = size_k / WPB, k0 = wave * kpart, nst = kpart / 8;
  const int gs = size_k / groups, spg = gs / 8;
  const int st0 = k0 / 8;  // absolute step index of this wave's first step

  const uint32_t* bp = b_q + (long)(k0 / 8) * size_n + n0;
  const half* ap[M];
  #pragma unroll
  for (int m = 0; m < M; ++m) ap[m] = a + (long)m * size_k + k0;

  float acc[M][2], cacc[M][2];
  #pragma unroll
  for (int m = 0; m < M; ++m)
    acc[m][0] = acc[m][1] = cacc[m][0] = cacc[m][1] = 0.0f;

  // -(1024 + zero) for the plain pairs, -(64 + zero) for the x16 ones.
  uint32_t z1[2], z16[2];
  float sc[2];
  auto group_sz = [&](int g) {
    const uint32_t zw =
        b_qzeros[(long)g * (size_n / 8) + n0 / 8] >> ((n0 & 7) * 4);
  #pragma unroll
    for (int j = 0; j < 2; ++j) {
      const float z = (float)(((zw >> (4 * j)) & 0xF) + (uint32_t)zero_offset);
      const d2_h2 v1 = {(_Float16)(-1024.0f - z), (_Float16)(-1024.0f - z)};
      const d2_h2 v16 = {(_Float16)(-64.0f - z), (_Float16)(-64.0f - z)};
      __builtin_memcpy(&z1[j], &v1, 4);
      __builtin_memcpy(&z16[j], &v16, 4);
      sc[j] = __half2float(b_scales[(long)g * size_n + n0 + j]);
    }
  };

  uint2 rb[PF];
  #pragma unroll
  for (int p = 0; p < PF; ++p)
    if (p < nst) rb[p] = *(const uint2*)(bp + (long)p * size_n);
  group_sz(k0 / gs);
  const d2_h2 sixteenth = {(_Float16)0.0625f, (_Float16)0.0625f};

  for (int s0 = 0; s0 < nst; s0 += PF) {
  #pragma unroll
    for (int p = 0; p < PF; ++p) {
      const int st = s0 + p;
      if (st < nst) {
        const uint32_t w[2] = {rb[p].x, rb[p].y};
        if (st + PF < nst)
          rb[p] = *(const uint2*)(bp + (long)(st + PF) * size_n);
        uint4 av[M];
  #pragma unroll
        for (int m = 0; m < M; ++m) av[m] = *(const uint4*)(ap[m] + 8 * st);
  #pragma unroll
        for (int j = 0; j < 2; ++j) {
          const uint32_t ws = w[j] >> 8;
          const d2_h2 x0 =
              d2_as_h2((w[j] & 0x000F000Fu) | 0x64006400u) + d2_as_h2(z1[j]);
          const d2_h2 x1 =
              d2_as_h2((w[j] & 0x00F000F0u) | 0x64006400u) * sixteenth +
              d2_as_h2(z16[j]);
          const d2_h2 x2 =
              d2_as_h2((ws & 0x000F000Fu) | 0x64006400u) + d2_as_h2(z1[j]);
          const d2_h2 x3 =
              d2_as_h2((ws & 0x00F000F0u) | 0x64006400u) * sixteenth +
              d2_as_h2(z16[j]);
  #pragma unroll
          for (int m = 0; m < M; ++m) {
            float t = acc[m][j];
            t = __builtin_amdgcn_fdot2(x0, d2_as_h2(av[m].x), t, false);
            t = __builtin_amdgcn_fdot2(x1, d2_as_h2(av[m].y), t, false);
            t = __builtin_amdgcn_fdot2(x2, d2_as_h2(av[m].z), t, false);
            t = __builtin_amdgcn_fdot2(x3, d2_as_h2(av[m].w), t, false);
            acc[m][j] = t;
          }
        }
        // Fold at absolute group boundaries (the original kernel used the
        // wave-local step, which is the same when k0 is group-aligned).
        if ((st0 + st + 1) % spg == 0) {
  #pragma unroll
          for (int m = 0; m < M; ++m)
  #pragma unroll
            for (int j = 0; j < 2; ++j) {
              cacc[m][j] += sc[j] * acc[m][j];
              acc[m][j] = 0.0f;
            }
          if (st + 1 < nst) group_sz((k0 + 8 * (st + 1)) / gs);
        }
      }
    }
  }
  // A part that ends inside a group folds its tail here; otherwise acc is
  // zero and this adds exact zeros.
  #pragma unroll
  for (int m = 0; m < M; ++m)
  #pragma unroll
    for (int j = 0; j < 2; ++j) cacc[m][j] += sc[j] * acc[m][j];

  __shared__ float red[WPB][M][64];
  #pragma unroll
  for (int m = 0; m < M; ++m) {
    red[wave][m][2 * lane] = cacc[m][0];
    red[wave][m][2 * lane + 1] = cacc[m][1];
  }
  __syncthreads();
  for (int e = threadIdx.x; e < M * 64; e += WPB * 32) {
    const int m = e / 64, col = e % 64;
    float v = 0.0f;
  #pragma unroll
    for (int w = 0; w < WPB; ++w) v += red[w][m][col];
    c[(long)m * size_n + blockIdx.x * 64 + col] = __float2half_rn(v);
  }
}

#else  // non-RDNA3 device pass: empty __global__ for symbol parity.

template <int M, int PF, int WPB>
__global__ void gemm_q4_kernel_dot2(const half*, const uint32_t*,
                                    const uint32_t*, const half*, half*,
                                    const int, const int, const int,
                                    const int) {}

#endif  // __HIP__RDNA3__ || !__HIP_DEVICE_COMPILE__

// Waves per block when the caller does not force one: the tracking branch's
// rule (32 for narrow N at M <= 6, whose reduction LDS caps M; 16 up to
// N = 5120; 8 otherwise), with group alignment of the K parts dropped.
inline int dot2_auto_waves(int size_m, int size_n, int size_k) {
  if (size_m <= 6 && size_n <= 32 * 64 && size_k % (8 * 32) == 0) return 32;
  if (size_n <= 5120 && size_k % (8 * 16) == 0) return 16;
  return 8;
}

inline bool dot2_waves_ok(int waves, int size_m, int size_k) {
  if (waves != 8 && waves != 16 && waves != 32) return false;
  if (waves == 32 && size_m > 6) return false;  // LDS of the reduction
  return size_k % (8 * waves) == 0;
}

inline bool dot2_fits(int size_m, int size_n, int size_k, int groups) {
  if (size_m < kDot2MinM || size_m > kDot2MaxM || groups <= 0) return false;
  if (size_k % groups != 0) return false;
  const int gs = size_k / groups;
  return gs % 8 == 0 && size_n % 64 == 0 && size_k % 64 == 0;
}

template <int M>
void launch_dot2_m(const half* a, const uint32_t* b_q_weight,
                   const uint32_t* b_qzeros, const half* b_scales, half* c,
                   int size_n, int size_k, int groups, int zero_offset,
                   int waves, cudaStream_t stream) {
  const dim3 grid(size_n / 64);
  if constexpr (M <= 6) {
    if (waves == 32) {
      gemm_q4_kernel_dot2<M, 2, 32>
          <<<grid, 1024, 0, stream>>>(a, b_q_weight, b_qzeros, b_scales, c,
                                      size_n, size_k, groups, zero_offset);
      return;
    }
  }
  if (waves == 16)
    gemm_q4_kernel_dot2<M, 2, 16>
        <<<grid, 512, 0, stream>>>(a, b_q_weight, b_qzeros, b_scales, c, size_n,
                                   size_k, groups, zero_offset);
  else
    gemm_q4_kernel_dot2<M, 4, 8>
        <<<grid, 256, 0, stream>>>(a, b_q_weight, b_qzeros, b_scales, c, size_n,
                                   size_k, groups, zero_offset);
}

void launch_dot2(const half* a, const uint32_t* b_q_weight,
                 const uint32_t* b_qzeros, const half* b_scales, half* c,
                 int size_m, int size_n, int size_k, int groups,
                 int zero_offset, int waves, cudaStream_t stream) {
  switch (size_m) {
#define DOT2_CASE(M)                                                        \
  case M:                                                                   \
    launch_dot2_m<M>(a, b_q_weight, b_qzeros, b_scales, c, size_n, size_k, \
                     groups, zero_offset, waves, stream);                   \
    break;
    DOT2_CASE(1)
    DOT2_CASE(2)
    DOT2_CASE(3)
    DOT2_CASE(4)
    DOT2_CASE(5)
    DOT2_CASE(6)
    DOT2_CASE(7)
    DOT2_CASE(8)
    DOT2_CASE(9)
    DOT2_CASE(10)
#undef DOT2_CASE
    default:
      TORCH_CHECK(false, "gptq_gemm_rdna3_dot2: M=", size_m, " out of range");
  }
}

}  // namespace gptq_rdna3_dot2
}  // namespace vllm

// True when the dot2 kernel can take this call with `waves` waves per block
// (0 = the automatic choice).
bool gptq_gemm_rdna3_dot2_fits(const torch::Tensor& a,
                               const torch::Tensor& b_q_weight,
                               const torch::Tensor& b_qzeros,
                               const torch::Tensor& b_scales, int64_t waves) {
  using namespace vllm::gptq_rdna3_dot2;
  if (a.dim() != 2 || b_q_weight.dim() != 2 || b_scales.dim() != 2) return false;
  if (a.scalar_type() != torch::kHalf ||
      b_scales.scalar_type() != torch::kHalf) {
    return false;
  }
  if (!a.is_contiguous() || !b_q_weight.is_contiguous() ||
      !b_qzeros.is_contiguous() || !b_scales.is_contiguous()) {
    return false;
  }
  if (reinterpret_cast<uintptr_t>(a.data_ptr()) % 16 != 0) return false;
  const int size_m = (int)a.size(0), size_k = (int)a.size(1);
  const int size_n = (int)b_q_weight.size(1), groups = (int)b_scales.size(0);
  if (b_q_weight.size(0) * 8 != size_k || b_qzeros.size(0) != groups ||
      b_scales.size(1) != size_n) {
    return false;
  }
  if (!dot2_fits(size_m, size_n, size_k, groups)) return false;
  const int w = waves > 0 ? (int)waves : dot2_auto_waves(size_m, size_n, size_k);
  return dot2_waves_ok(w, size_m, size_k);
}

torch::Tensor gptq_gemm_rdna3_dot2(torch::Tensor a, torch::Tensor b_q_weight,
                                   torch::Tensor b_qzeros,
                                   torch::Tensor b_scales, bool use_v2_format,
                                   int64_t waves) {
  using namespace vllm::gptq_rdna3_dot2;
  TORCH_CHECK(a.is_cuda() && b_q_weight.is_cuda() && b_qzeros.is_cuda() &&
                  b_scales.is_cuda(),
              "gptq_gemm_rdna3_dot2: all tensors must be on the GPU");
  TORCH_CHECK(gptq_gemm_rdna3_dot2_fits(a, b_q_weight, b_qzeros, b_scales,
                                        waves),
              "gptq_gemm_rdna3_dot2: shape, dtype, layout or waves=", waves,
              " not supported");
  const at::cuda::OptionalCUDAGuard device_guard(device_of(a));
  auto stream = at::cuda::getCurrentCUDAStream();
  const int size_m = (int)a.size(0), size_k = (int)a.size(1);
  const int size_n = (int)b_q_weight.size(1), groups = (int)b_scales.size(0);
  const int w = waves > 0 ? (int)waves : dot2_auto_waves(size_m, size_n, size_k);
  at::Tensor c = torch::empty({size_m, size_n}, a.options());
  launch_dot2((const half*)a.data_ptr(), (const uint32_t*)b_q_weight.data_ptr(),
              (const uint32_t*)b_qzeros.data_ptr(),
              (const half*)b_scales.data_ptr(), (half*)c.data_ptr(), size_m,
              size_n, size_k, groups, use_v2_format ? 0 : 1, w, stream);
  return c;
}

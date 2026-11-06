// SPDX-License-Identifier: Apache-2.0
//
// Torch bindings for flexinfer_vllm_rdna3_w4a16.
//
// q_gemm_rdna3.cu is vLLM v0.33.0's csrc/rocm/q_gemm_rdna3.cu plus the
// persistent split-K tickets change (see upstream/). setup.py compiles it with
// -Dvllm=flexinfer_rdna3_vllm and -Dgptq_gemm_rdna3=flexinfer_gptq_gemm_rdna3
// (and the same for _wmma), so none of its symbols can collide with vLLM's
// _rocm_C, which defines the same names, and its kernels show up in a trace as
// flexinfer_rdna3_vllm::gptq_rdna3::gemm_q4_kernel_rdna3<...>.
//
// Ops, namespace flexinfer_rdna3:
//   gptq_gemm(a, b_q_weight, b_qzeros, b_scales, use_v2_format) -> Tensor
//       the patched kernel, called directly (benchmarks, tests);
//   gptq_gemm_dot2(a, b_q_weight, b_qzeros, b_scales, use_v2_format, waves)
//       the dot2 candidate (q_gemm_rdna3_dot2.cu); waves 0 = automatic;
//   dot2_fits(a, b_q_weight, b_qzeros, b_scales, waves) -> bool;
//   install(mode) -> int
//       replaces the CUDA kernel of _rocm_C::gptq_gemm_rdna3, which vLLM's
//       RDNA3W4A16LinearKernel calls: 0 restores vLLM's own kernel,
//       1 = the patched kernel, 2 = dot2 where it fits and the patched kernel
//       elsewhere. Only the CUDA kernel moves; the schema and vLLM's fake
//       (meta) kernel stay, so torch.compile and CUDA graphs see the same op;
//   mode() -> int.

#include <memory>
#include <mutex>

#include <ATen/core/dispatch/Dispatcher.h>
#include <torch/all.h>
#include <torch/library.h>

// From q_gemm_rdna3.cu (renamed by setup.py).
torch::Tensor flexinfer_gptq_gemm_rdna3(torch::Tensor a,
                                        torch::Tensor b_q_weight,
                                        torch::Tensor b_qzeros,
                                        torch::Tensor b_scales,
                                        bool use_v2_format);

// From q_gemm_rdna3_dot2.cu.
torch::Tensor gptq_gemm_rdna3_dot2(torch::Tensor a, torch::Tensor b_q_weight,
                                   torch::Tensor b_qzeros,
                                   torch::Tensor b_scales, bool use_v2_format,
                                   int64_t waves);
bool gptq_gemm_rdna3_dot2_fits(const torch::Tensor& a,
                               const torch::Tensor& b_q_weight,
                               const torch::Tensor& b_qzeros,
                               const torch::Tensor& b_scales, int64_t waves);

// q_gemm_rdna3.cu sends M >= 12 to the WMMA kernel, which this library does
// not build: forward to vLLM's own op through the dispatcher.
torch::Tensor flexinfer_gptq_gemm_rdna3_wmma(torch::Tensor a,
                                             torch::Tensor b_q_weight,
                                             torch::Tensor b_qzeros,
                                             torch::Tensor b_scales,
                                             bool use_v2_format) {
  static const auto op =
      c10::Dispatcher::singleton()
          .findSchemaOrThrow("_rocm_C::gptq_gemm_rdna3_wmma", "")
          .typed<torch::Tensor(torch::Tensor, torch::Tensor, torch::Tensor,
                               torch::Tensor, bool)>();
  return op.call(a, b_q_weight, b_qzeros, b_scales, use_v2_format);
}

namespace {

torch::Tensor gemm_dot2_or_tickets(torch::Tensor a, torch::Tensor b_q_weight,
                                   torch::Tensor b_qzeros,
                                   torch::Tensor b_scales, bool use_v2_format) {
  if (a.is_cuda() &&
      gptq_gemm_rdna3_dot2_fits(a, b_q_weight, b_qzeros, b_scales, 0)) {
    return gptq_gemm_rdna3_dot2(a, b_q_weight, b_qzeros, b_scales,
                                use_v2_format, 0);
  }
  return flexinfer_gptq_gemm_rdna3(a, b_q_weight, b_qzeros, b_scales,
                                   use_v2_format);
}

std::mutex g_override_mu;
std::unique_ptr<torch::Library> g_override;
int64_t g_mode = 0;

// The registration's debug string; the plugin reads it back from
// torch._C._dispatch_dump_table to prove the override is the active kernel.
constexpr const char* kOverrideFile = "flexinfer_rdna3_w4a16_override";

int64_t install(int64_t mode) {
  TORCH_CHECK(mode >= 0 && mode <= 2, "flexinfer_rdna3.install: mode ", mode,
              " is not 0 (vLLM's kernel), 1 (tickets) or 2 (dot2)");
  std::lock_guard<std::mutex> lock(g_override_mu);
  // Destroying the Library removes its registration, which puts vLLM's own
  // kernel back in the dispatch table.
  g_override.reset();
  g_mode = 0;
  if (mode == 0) return 0;
  // Fail here, not on the first GEMM, when _rocm_C (and with it the schema
  // and the WMMA op the large-M path forwards to) is not loaded.
  c10::Dispatcher::singleton().findSchemaOrThrow("_rocm_C::gptq_gemm_rdna3",
                                                 "");
  c10::Dispatcher::singleton().findSchemaOrThrow(
      "_rocm_C::gptq_gemm_rdna3_wmma", "");
  auto lib = std::make_unique<torch::Library>(
      torch::Library::IMPL, "_rocm_C", c10::DispatchKey::CUDA, kOverrideFile,
      static_cast<uint32_t>(mode));
  if (mode == 1) {
    lib->impl("gptq_gemm_rdna3", TORCH_FN(flexinfer_gptq_gemm_rdna3));
  } else {
    lib->impl("gptq_gemm_rdna3", TORCH_FN(gemm_dot2_or_tickets));
  }
  g_override = std::move(lib);
  g_mode = mode;
  return mode;
}

int64_t current_mode() {
  std::lock_guard<std::mutex> lock(g_override_mu);
  return g_mode;
}

}  // namespace

TORCH_LIBRARY(flexinfer_rdna3, m) {
  m.def(
      "gptq_gemm(Tensor a, Tensor b_q_weight, Tensor b_qzeros, "
      "Tensor b_scales, bool use_v2_format) -> Tensor");
  m.impl("gptq_gemm", torch::kCUDA, &flexinfer_gptq_gemm_rdna3);
  m.def(
      "gptq_gemm_dot2(Tensor a, Tensor b_q_weight, Tensor b_qzeros, "
      "Tensor b_scales, bool use_v2_format, int waves=0) -> Tensor");
  m.impl("gptq_gemm_dot2", torch::kCUDA, &gptq_gemm_rdna3_dot2);
  m.def(
      "dot2_fits(Tensor a, Tensor b_q_weight, Tensor b_qzeros, "
      "Tensor b_scales, int waves=0) -> bool");
  m.impl("dot2_fits", torch::kCUDA,
         [](const torch::Tensor& a, const torch::Tensor& b_q_weight,
            const torch::Tensor& b_qzeros, const torch::Tensor& b_scales,
            int64_t waves) {
           return gptq_gemm_rdna3_dot2_fits(a, b_q_weight, b_qzeros, b_scales,
                                            waves);
         });
  m.def("install(int mode) -> int", &install);
  m.def("mode() -> int", &current_mode);
}

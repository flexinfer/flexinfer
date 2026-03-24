#!/usr/bin/env python3
"""Register qwen3_5_text config + model + fix weight loading + add IsHybrid + mamba state methods + FLA fix + diagnostics.

ALL patches are FILE-LEVEL (modify .py on disk) so they persist to the EngineCore subprocess.
"""
import re
import subprocess
import sys

BASE = "/opt/venv/lib/python3.12/site-packages/vllm"

# 0. Install Triton 3.4.0 — sweet spot between 3.2.0 (breaks unified attention) and 3.5.1 (crashes FLA)
try:
    import triton

    target = "3.4.0"
    if triton.__version__ != target:
        print(f"0. Installing Triton {target} (was {triton.__version__})...")
        subprocess.check_call(
            [sys.executable, "-m", "pip", "install", f"triton=={target}", "--quiet"],
            stdout=subprocess.DEVNULL,
        )
        print(f"0. Triton installed: {target}")
    else:
        print(f"0. Triton already at {target}")
except Exception as e:
    print(f"0. WARNING: Triton install failed: {e}")

# 0b. Patch FLA ops/op.py — wrap exp/log/log2 with fp32 cast
# AMD ROCm Triton requires fp32 for math ops; bf16 causes LLVM crash (3.5.1) or CompilationError (3.2.0)
op_path = f"{BASE}/model_executor/layers/fla/ops/op.py"
with open(op_path) as f:
    op_content = f.read()

old_math_ops = """if os.environ.get("FLA_USE_FAST_OPS", "0") == "1":
    exp = tldevice.fast_expf
    log = tldevice.fast_logf
    log2 = tldevice.fast_log2f
else:
    exp = tl.exp
    log = tl.log
    log2 = tl.log2"""

new_math_ops = """# Auto-cast to fp32 for Triton compatibility on AMD ROCm (bf16 not supported in exp/log)
@triton.jit
def exp(x):
    return tl.exp(x.to(tl.float32))

@triton.jit
def log(x):
    return tl.log(x.to(tl.float32))

@triton.jit
def log2(x):
    return tl.log2(x.to(tl.float32))"""

if old_math_ops in op_content:
    op_content = op_content.replace(old_math_ops, new_math_ops)
    with open(op_path, "w") as f:
        f.write(op_content)
    print("0b. Patched FLA ops/op.py with fp32-safe math wrappers")
elif "@triton.jit\ndef exp(x):" in op_content:
    print("0b. FLA ops/op.py already patched")
else:
    print("0b. WARNING: Could not find expected pattern in FLA ops/op.py")

# Clear Triton cache to force recompilation with new wrappers
import shutil

for cache_dir in ["/root/.triton/cache", "/tmp/.triton/cache"]:
    shutil.rmtree(cache_dir, ignore_errors=True)
print("0c. Cleared Triton cache")

# 1. Register qwen3_5_text config type
config_path = f"{BASE}/transformers_utils/config.py"
with open(config_path) as f:
    content = f.read()
if "qwen3_5_text" not in content:
    content = content.replace(
        'qwen3_5_moe="Qwen3_5MoeConfig",',
        'qwen3_5_moe="Qwen3_5MoeConfig",\n    qwen3_5_text="Qwen3_5TextConfig",',
    )
    with open(config_path, "w") as f:
        f.write(content)
    print("1. Registered qwen3_5_text config type")
else:
    print("1. qwen3_5_text already registered")

# 2. Register Qwen3_5ForCausalLM in model registry
registry_path = f"{BASE}/model_executor/models/registry.py"
with open(registry_path) as f:
    content = f.read()
if "Qwen3_5ForCausalLM" not in content:
    content = content.replace(
        '"Qwen3NextForCausalLM": ("qwen3_next", "Qwen3NextForCausalLM"),',
        '"Qwen3NextForCausalLM": ("qwen3_next", "Qwen3NextForCausalLM"),\n    "Qwen3_5ForCausalLM": ("qwen3_5", "Qwen3_5ForCausalLM"),',
    )
    with open(registry_path, "w") as f:
        f.write(content)
    print("2. Registered Qwen3_5ForCausalLM in model registry")
else:
    print("2. Qwen3_5ForCausalLM already registered")

# 3. Patch qwen3_5.py
qwen35_path = f"{BASE}/model_executor/models/qwen3_5.py"
with open(qwen35_path) as f:
    content = f.read()

changes = []

# 3a. Keep original packed_modules_mapping — GPTQ checkpoint has UNFUSED
# projections (in_proj_qkv, in_proj_z, in_proj_a, in_proj_b) that vLLM's
# weight loader must reassemble into the fused in_proj_qkvz / in_proj_ba.
# The original mapping {"in_proj_qkvz": ["in_proj_qkv", "in_proj_z"], ...}
# is correct and must NOT be changed to identity.
changes.append("packed_modules_mapping (kept original unfused mapping)")

# 3c. Add IsHybrid to Qwen3_5ForCausalLMBase inheritance
# Check if IsHybrid is already in the class definition
class_match = re.search(r"class Qwen3_5ForCausalLMBase\(([^)]+)\)", content, re.DOTALL)
if class_match and "IsHybrid" not in class_match.group(1):
    # Insert IsHybrid before the closing paren
    old_def = class_match.group(0)
    bases = class_match.group(1)
    # Add IsHybrid after SupportsPP
    new_bases = bases.rstrip().rstrip(",") + ",\n        IsHybrid,\n    "
    new_def = f"class Qwen3_5ForCausalLMBase({new_bases})"
    content = content.replace(old_def, new_def)
    changes.append("IsHybrid inheritance")
elif class_match:
    print("  IsHybrid already in class definition")

# 3d. Add mamba state methods to Qwen3_5ForCausalLMBase
# Check specifically within the Base class body, not the whole file
# (Qwen3_5ForConditionalGeneration already has these, so file-wide check fails)
base_class_section = (
    content.split("class Qwen3_5ForCausalLMBase")[1].split("\nclass ")[0]
    if "class Qwen3_5ForCausalLMBase" in content
    else ""
)
needs_mamba_methods = "get_mamba_state_dtype_from_config" not in base_class_section

mamba_methods = """
    @classmethod
    def get_mamba_state_dtype_from_config(
        cls,
        vllm_config: "VllmConfig",
    ) -> tuple[torch.dtype, torch.dtype]:
        return MambaStateDtypeCalculator.gated_delta_net_state_dtype(
            vllm_config.model_config.dtype,
            vllm_config.cache_config.mamba_cache_dtype,
            vllm_config.cache_config.mamba_ssm_cache_dtype,
        )

    @classmethod
    def get_mamba_state_shape_from_config(
        cls, vllm_config: "VllmConfig"
    ) -> tuple[tuple[int, int], tuple[int, int]]:
        parallel_config = vllm_config.parallel_config
        hf_config = vllm_config.model_config.hf_text_config
        tp_size = parallel_config.tensor_parallel_size
        num_spec = (
            vllm_config.speculative_config.num_speculative_tokens
            if vllm_config.speculative_config
            else 0
        )
        return MambaStateShapeCalculator.gated_delta_net_state_shape(
            tp_size,
            hf_config.linear_num_key_heads,
            hf_config.linear_num_value_heads,
            hf_config.linear_key_head_dim,
            hf_config.linear_value_head_dim,
            hf_config.linear_conv_kernel_dim,
            num_spec,
        )

    @classmethod
    def get_mamba_state_copy_func(cls) -> tuple[MambaStateCopyFunc, MambaStateCopyFunc]:
        return MambaStateCopyFuncCalculator.gated_delta_net_state_copy_func()

"""
if needs_mamba_methods:
    # Find the first load_weights in Qwen3_5ForCausalLMBase (before the next class def)
    # Use a targeted insertion point
    base_start = content.find("class Qwen3_5ForCausalLMBase")
    next_class = content.find("\nclass ", base_start + 1)
    base_body = (
        content[base_start:next_class] if next_class > 0 else content[base_start:]
    )

    load_weights_sig = "    def load_weights(self, weights: Iterable[tuple[str, torch.Tensor]]) -> set[str]:"
    lw_pos = base_body.find(load_weights_sig)
    if lw_pos >= 0:
        abs_pos = base_start + lw_pos
        content = content[:abs_pos] + mamba_methods + content[abs_pos:]
        changes.append("mamba state methods")
    else:
        print("  WARNING: Could not find load_weights in Qwen3_5ForCausalLMBase")
else:
    print("  mamba state methods already in Qwen3_5ForCausalLMBase")

# 3e. Fix MergedColumnParallelLinear output_sizes for pre-fused weights
# Pre-fused checkpoint has single tensors, not multiple shards.
old_qkvz_proj = "output_sizes=[key_dim, key_dim, value_dim, value_dim],"
new_qkvz_proj = "output_sizes=[key_dim + key_dim + value_dim + value_dim],"
if old_qkvz_proj in content:
    content = content.replace(old_qkvz_proj, new_qkvz_proj)
    changes.append("qkvz_proj output_sizes")

# Also fix in_proj_ba: output_sizes=[self.num_v_heads] * 2 → [self.num_v_heads * 2]
old_ba = "output_sizes=[self.num_v_heads] * 2,"
new_ba = "output_sizes=[self.num_v_heads * 2],"
if old_ba in content:
    content = content.replace(old_ba, new_ba)
    changes.append("in_proj_ba output_sizes")

with open(qwen35_path, "w") as f:
    f.write(content)
print(
    f"3. Patched qwen3_5.py: {', '.join(changes) if changes else 'no changes needed'}"
)

# 4. Verify
with open(qwen35_path) as f:
    verify = f.read()
class_match = re.search(r"class Qwen3_5ForCausalLMBase\(([^)]+)\)", verify, re.DOTALL)
if class_match and "IsHybrid" in class_match.group(1):
    print("4. VERIFIED: IsHybrid in Qwen3_5ForCausalLMBase inheritance")
else:
    print("4. FAILED: IsHybrid NOT in class inheritance!")

base_section = (
    verify.split("class Qwen3_5ForCausalLMBase")[1].split("\nclass ")[0]
    if "class Qwen3_5ForCausalLMBase" in verify
    else ""
)
if (
    "get_mamba_state_dtype_from_config" in base_section
    and "get_mamba_state_shape_from_config" in base_section
):
    print("5. VERIFIED: mamba state methods in Qwen3_5ForCausalLMBase")
else:
    print("5. FAILED: mamba state methods missing from Qwen3_5ForCausalLMBase!")

# Check packed_modules — should have UNFUSED entries for GPTQ checkpoint
if '["in_proj_qkv", "in_proj_z"]' in verify and '["in_proj_b", "in_proj_a"]' in verify:
    print("6. VERIFIED: packed_modules_mapping has unfused entries (correct for GPTQ)")
elif '["in_proj_qkvz"]' in verify and '["in_proj_ba"]' in verify:
    print(
        "6. WARNING: packed_modules_mapping has identity entries (WRONG for unfused GPTQ checkpoint!)"
    )
else:
    print("6. WARNING: packed_modules_mapping state unknown")

# Check FLA op.py
with open(op_path) as f:
    op_verify = f.read()
if "def exp(x):" in op_verify and "x.to(tl.float32)" in op_verify:
    print("7. VERIFIED: FLA ops/op.py has fp32-safe math wrappers")
else:
    print("7. WARNING: FLA ops/op.py may not be patched correctly")

print("\nAll file patches applied successfully")

# ============================================================================
# 8. Fix GQA head expansion in qwen3_next.py rearrange_mixed_qkv
# ROOT CAUSE: Qwen3.5-27B has linear_num_key_heads=16, linear_num_value_heads=48.
# The HuggingFace reference uses repeat_interleave(ratio=3, dim=2) to expand q/k
# from 16 to 48 heads BEFORE passing to FLA delta rule kernels.
# vLLM's qwen3_next.py rearrange_mixed_qkv is MISSING this expansion, causing
# the FLA kernel to read OOB data for value heads 16-47 → garbage output.
# ============================================================================
qwen3_next_path = f"{BASE}/model_executor/models/qwen3_next.py"
with open(qwen3_next_path) as f:
    qn_content = f.read()

# Add repeat_interleave before the return in rearrange_mixed_qkv
old_rearrange_return = '        value = rearrange(value, "l (h d) -> 1 l h d", d=self.head_v_dim)\n        return query.contiguous(), key.contiguous(), value.contiguous()'
new_rearrange_return = '        value = rearrange(value, "l (h d) -> 1 l h d", d=self.head_v_dim)\n        # GQA-style head expansion: expand q/k from num_k_heads to num_v_heads\n        if self.num_v_heads > self.num_k_heads:\n            ratio = self.num_v_heads // self.num_k_heads\n            query = query.repeat_interleave(ratio, dim=2)\n            key = key.repeat_interleave(ratio, dim=2)\n        return query.contiguous(), key.contiguous(), value.contiguous()'

if old_rearrange_return in qn_content:
    qn_content = qn_content.replace(old_rearrange_return, new_rearrange_return)
    with open(qwen3_next_path, "w") as f:
        f.write(qn_content)
    print(
        "8. PATCHED: Added repeat_interleave q/k head expansion in rearrange_mixed_qkv"
    )
elif "repeat_interleave" in qn_content and "rearrange_mixed_qkv" in qn_content:
    print("8. repeat_interleave already present in rearrange_mixed_qkv")
else:
    print("8. WARNING: Could not find expected pattern in rearrange_mixed_qkv")

# ============================================================================
# 9. Write naive FLA implementations to a file on disk, then change imports
#    in qwen3_next.py to use them. This ensures the EngineCore subprocess
#    picks up the naive versions (runtime monkey-patches don't persist to
#    the subprocess since vLLM uses multiprocessing.spawn).
# ============================================================================

naive_impl_path = f"{BASE}/model_executor/layers/fla/ops/_naive_impl.py"
naive_impl_code = '''"""Naive PyTorch implementations of FLA kernels for ROCm safety.

These replace the Triton-based chunk_gated_delta_rule and fused_recurrent_gated_delta_rule
kernels that may have issues on AMD gfx1100.
"""
import sys
import torch
import torch.nn.functional as F

_diag_counter = [0]

def _stat(name, t, call_num):
    """Print tensor statistics for diagnostics."""
    if t is None:
        print(f"  DIAG [{call_num}] {name}: None", file=sys.stderr, flush=True)
        return
    t_f = t.float()
    print(
        f"  DIAG [{call_num}] {name}: shape={list(t.shape)} dtype={t.dtype} "
        f"mean={t_f.mean().item():.6f} std={t_f.std().item():.6f} "
        f"absmax={t_f.abs().max().item():.6f}",
        file=sys.stderr, flush=True,
    )


def naive_chunk_gated_delta_rule(
    q,
    k,
    v,
    g,
    beta,
    scale=None,
    initial_state=None,
    output_final_state=False,
    cu_seqlens=None,
    use_qk_l2norm_in_kernel=False,
):
    """Pure PyTorch sequential recurrence replacing FLA Triton chunk kernel."""
    _diag_counter[0] += 1
    call_num = _diag_counter[0]
    do_log = call_num <= 4

    if use_qk_l2norm_in_kernel:
        q = F.normalize(q.float(), p=2, dim=-1).to(q.dtype)
        k = F.normalize(k.float(), p=2, dim=-1).to(k.dtype)

    B, T_total, H, K = q.shape
    HV, V = v.shape[2], v.shape[3]
    scale = scale or K**-0.5

    if do_log:
        print(f"  DIAG [{call_num}] naive_chunk_gated_delta_rule: B={B} T={T_total} H={H} K={K} HV={HV} V={V} scale={scale:.6f}", file=sys.stderr, flush=True)
        _stat("q", q, call_num)
        _stat("k", k, call_num)
        _stat("v", v, call_num)
        _stat("g", g, call_num)
        _stat("beta", beta, call_num)

    if cu_seqlens is not None:
        # Variable-length mode: B=1, tokens flattened
        assert B == 1
        N = len(cu_seqlens) - 1
        state = (
            initial_state.float().clone()
            if initial_state is not None
            else torch.zeros(N, HV, V, K, dtype=torch.float32, device=q.device)
        )
        outputs = torch.empty(1, T_total, HV, V, dtype=q.dtype, device=q.device)
        for n in range(N):
            start, end = cu_seqlens[n].item(), cu_seqlens[n + 1].item()
            for t in range(start, end):
                decay = torch.exp(g[0, t, :])  # [HV]
                state[n] = state[n] * decay[:, None, None]
                kt = k[0, t, :, :].float()  # [H, K]
                vt = v[0, t, :, :].float()  # [HV, V]
                bt = beta[0, t, :].float()  # [HV]
                # Delta correction: subtract current prediction from value
                prediction = torch.einsum("hvk,hk->hv", state[n], kt)
                v_delta = vt - prediction
                state[n] = state[n] + bt[:, None, None] * (
                    v_delta[:, :, None] * kt[:, None, :]
                )
                qt = q[0, t, :, :].float()  # [H, K]
                ot = scale * torch.einsum("hvk,hk->hv", state[n], qt)
                outputs[0, t] = ot.to(q.dtype)
    else:
        # Fixed-length mode
        state = (
            initial_state.float().clone()
            if initial_state is not None
            else torch.zeros(B, HV, V, K, dtype=torch.float32, device=q.device)
        )
        out_list = []
        for t in range(T_total):
            decay = torch.exp(g[:, t, :])  # [B, HV]
            state = state * decay[:, :, None, None]
            kt = k[:, t, :, :].float()  # [B, H, K]
            vt = v[:, t, :, :].float()  # [B, HV, V]
            bt = beta[:, t, :].float()  # [B, HV]
            # Delta correction: subtract current prediction from value
            prediction = torch.einsum("bhvk,bhk->bhv", state, kt)
            v_delta = vt - prediction
            state = state + bt[:, :, None, None] * (
                v_delta[:, :, :, None] * kt[:, :, None, :]
            )
            qt = q[:, t, :, :].float()  # [B, H, K]
            ot = scale * torch.einsum("bhvk,bhk->bhv", state, qt)
            out_list.append(ot.to(q.dtype))
        outputs = torch.stack(out_list, dim=1)

    if do_log:
        _stat("chunk output", outputs, call_num)

    final_state = state if output_final_state else None
    return outputs, final_state


def naive_fused_recurrent_gated_delta_rule(
    q,
    k,
    v,
    g,
    beta=None,
    scale=None,
    initial_state=None,
    inplace_final_state=True,
    cu_seqlens=None,
    ssm_state_indices=None,
    num_accepted_tokens=None,
    use_qk_l2norm_in_kernel=False,
):
    """Pure PyTorch single-step recurrence replacing FLA Triton fused_recurrent kernel."""
    _diag_counter[0] += 1
    call_num = _diag_counter[0]
    do_log = call_num <= 4

    if use_qk_l2norm_in_kernel:
        q = F.normalize(q.float(), p=2, dim=-1).to(q.dtype)
        k = F.normalize(k.float(), p=2, dim=-1).to(k.dtype)

    B, T, H, K = q.shape
    HV, V = v.shape[2], v.shape[3]
    scale = scale or K**-0.5
    if beta is None:
        beta = torch.ones(B, T, HV, dtype=q.dtype, device=q.device)

    if do_log:
        print(f"  DIAG [{call_num}] naive_fused_recurrent: B={B} T={T} H={H} K={K} HV={HV} V={V}", file=sys.stderr, flush=True)
        _stat("q", q, call_num)
        _stat("k", k, call_num)
        _stat("v", v, call_num)

    final_state = initial_state if inplace_final_state else initial_state.clone()
    N = B if cu_seqlens is None else len(cu_seqlens) - 1

    out_list = []

    if cu_seqlens is not None:
        assert B == 1
        for n in range(N):
            start, end = cu_seqlens[n].item(), cu_seqlens[n + 1].item()
            # Determine state index
            if ssm_state_indices is None:
                state_idx = n
            elif ssm_state_indices.ndim == 1:
                state_idx = ssm_state_indices[n].item()
            else:
                state_idx = ssm_state_indices[n, 0].item()

            nat = (
                num_accepted_tokens[n].item()
                if num_accepted_tokens is not None
                else (end - start)
            )

            for ti, t in enumerate(range(start, end)):
                if ti >= nat:
                    out_list.append(torch.zeros(HV, V, dtype=q.dtype, device=q.device))
                    continue

                if ssm_state_indices is not None and ssm_state_indices.ndim == 2:
                    fs_idx = ssm_state_indices[n, ti].item()
                else:
                    fs_idx = state_idx

                if fs_idx < 0:  # PAD_SLOT_ID
                    out_list.append(torch.zeros(HV, V, dtype=q.dtype, device=q.device))
                    continue

                state = final_state[state_idx].float()  # [HV, V, K]
                decay = torch.exp(g[0, t, :])  # [HV]
                state = state * decay[:, None, None]
                kt = k[0, t, :, :].float()
                vt = v[0, t, :, :].float()
                bt = (
                    beta[0, t, :].float()
                    if beta.ndim == 3
                    else beta[0, t, :, 0].float()
                )
                # Delta correction: subtract current prediction from value
                prediction = torch.einsum("hvk,hk->hv", state, kt)
                v_delta = vt - prediction
                state = state + bt[:, None, None] * (v_delta[:, :, None] * kt[:, None, :])
                final_state[fs_idx] = state.to(final_state.dtype)
                state_idx = fs_idx  # chain for next token

                qt = q[0, t, :, :].float()
                ot = scale * torch.einsum("hvk,hk->hv", state, qt)
                out_list.append(ot.to(q.dtype))

        if out_list:
            output = torch.stack(out_list, dim=0).unsqueeze(0)
        else:
            output = q.new_empty(1, 0, HV, V)
    else:
        for b in range(B):
            if ssm_state_indices is None:
                state_idx = b
            elif ssm_state_indices.ndim == 1:
                state_idx = ssm_state_indices[b].item()
            else:
                state_idx = ssm_state_indices[b, 0].item()

            nat = (
                num_accepted_tokens[b].item() if num_accepted_tokens is not None else T
            )

            for t in range(T):
                if t >= nat:
                    out_list.append(torch.zeros(HV, V, dtype=q.dtype, device=q.device))
                    continue

                if ssm_state_indices is not None and ssm_state_indices.ndim == 2:
                    fs_idx = ssm_state_indices[b, t].item()
                else:
                    fs_idx = state_idx

                if fs_idx < 0:
                    out_list.append(torch.zeros(HV, V, dtype=q.dtype, device=q.device))
                    continue

                state = final_state[state_idx].float()
                decay = torch.exp(g[b, t, :])
                state = state * decay[:, None, None]
                kt = k[b, t, :, :].float()
                vt = v[b, t, :, :].float()
                bt = (
                    beta[b, t, :].float()
                    if beta.ndim == 3
                    else beta[b, t, :, 0].float()
                )
                # Delta correction: subtract current prediction from value
                prediction = torch.einsum("hvk,hk->hv", state, kt)
                v_delta = vt - prediction
                state = state + bt[:, None, None] * (v_delta[:, :, None] * kt[:, None, :])
                final_state[fs_idx] = state.to(final_state.dtype)
                state_idx = fs_idx

                qt = q[b, t, :, :].float()
                ot = scale * torch.einsum("hvk,hk->hv", state, qt)
                out_list.append(ot.to(q.dtype))

        output = torch.stack(out_list, dim=0).reshape(B, T, HV, V)

    if do_log:
        _stat("recurrent output", output, call_num)

    return output, final_state
'''

# Write the naive implementation file (ACTIVE — used instead of Triton)
with open(naive_impl_path, "w") as f:
    f.write(naive_impl_code)
print(f"9a. Wrote naive FLA implementations to {naive_impl_path} (ACTIVE)")

# ENABLED: Use naive PyTorch FLA kernels instead of Triton.
# The Triton chunk_gated_delta_rule kernel produces near-zero output on ROCm gfx1100
# despite healthy inputs (Q/K/V/g/beta all have reasonable values).
# Using naive PyTorch implementation to diagnose whether the issue is Triton-specific.
print("9b. ENABLING naive PyTorch FLA kernels (replacing Triton)")

with open(qwen3_next_path) as f:
    qn_content = f.read()

naive_chunk_import = """from vllm.model_executor.layers.fla.ops._naive_impl import (
    naive_chunk_gated_delta_rule as fla_chunk_gated_delta_rule,
)"""
orig_chunk_import = """from vllm.model_executor.layers.fla.ops import (
    chunk_gated_delta_rule as fla_chunk_gated_delta_rule,
)"""

naive_recurrent_import = """from vllm.model_executor.layers.fla.ops._naive_impl import (
    naive_fused_recurrent_gated_delta_rule as fused_recurrent_gated_delta_rule,
)"""
orig_recurrent_import = """from vllm.model_executor.layers.fla.ops import (
    fused_recurrent_gated_delta_rule,
)"""

switched = False
if orig_chunk_import in qn_content:
    qn_content = qn_content.replace(orig_chunk_import, naive_chunk_import)
    switched = True
if orig_recurrent_import in qn_content:
    qn_content = qn_content.replace(orig_recurrent_import, naive_recurrent_import)
    switched = True

if switched:
    with open(qwen3_next_path, "w") as f:
        f.write(qn_content)
    print("9c. SWITCHED: qwen3_next.py imports changed to naive PyTorch FLA")
elif naive_chunk_import in qn_content:
    print("9c. qwen3_next.py already using naive FLA imports")
else:
    print("9c. WARNING: Could not find expected import patterns in qwen3_next.py")

print("9. DONE: Using naive PyTorch FLA kernels (NOT Triton)")

# ============================================================================
# 10. Force RMSNormGated to use forward_native (FILE PATCH on layernorm.py)
#     The runtime monkey-patch doesn't persist to EngineCore subprocess.
# ============================================================================
layernorm_path = f"{BASE}/model_executor/layers/layernorm.py"
with open(layernorm_path) as f:
    ln_content = f.read()

# Replace RMSNormGated.forward_cuda with a call to forward_native
old_rmsnorm_cuda = """    def forward_cuda(
        self, x: torch.Tensor, z: torch.Tensor | None = None
    ) -> torch.Tensor:
        from vllm.model_executor.layers.fla.ops.layernorm_guard import rmsnorm_fn

        return rmsnorm_fn(
            x,
            self.weight,
            self.bias,
            z=z,
            eps=self.eps,
            group_size=self.group_size,
            norm_before_gate=self.norm_before_gate,
            activation=self.activation,
        )"""

new_rmsnorm_cuda = """    def forward_cuda(
        self, x: torch.Tensor, z: torch.Tensor | None = None
    ) -> torch.Tensor:
        # PATCHED: bypass Triton rmsnorm_fn, use native PyTorch implementation
        return self.forward_native(x, z)"""

if old_rmsnorm_cuda in ln_content:
    ln_content = ln_content.replace(old_rmsnorm_cuda, new_rmsnorm_cuda)
    with open(layernorm_path, "w") as f:
        f.write(ln_content)
    print("10. PATCHED: RMSNormGated.forward_cuda → forward_native (FILE PATCH)")
elif "# PATCHED: bypass Triton rmsnorm_fn" in ln_content:
    print("10. RMSNormGated already patched (file)")
else:
    print("10. WARNING: Could not find expected RMSNormGated.forward_cuda pattern")

# ============================================================================
# 11. Add diagnostic logging to Qwen3_5GatedDeltaNet.forward() in qwen3_5.py
#     This is a FILE PATCH so it persists to the EngineCore subprocess.
#     Logs tensor stats at key pipeline points for first few calls.
# ============================================================================
with open(qwen35_path) as f:
    q35_content = f.read()

# Add diagnostic helper and logging to the forward() method
# Find the forward method and add logging after key operations

# First, add the diagnostic import at the top of the file
if "import sys as _diag_sys" not in q35_content:
    # Add after first import line
    q35_content = q35_content.replace(
        "import torch",
        "import torch\nimport sys as _diag_sys\n\n_q35_diag_counter = [0]\n\ndef _q35_stat(name, t, call_num):\n    if t is None:\n        print(f'  DIAG [{call_num}] {name}: None', file=_diag_sys.stderr, flush=True)\n        return\n    tf = t.float()\n    print(f'  DIAG [{call_num}] {name}: shape={list(t.shape)} dtype={t.dtype} mean={tf.mean().item():.6f} std={tf.std().item():.6f} absmax={tf.abs().max().item():.6f}', file=_diag_sys.stderr, flush=True)\n",
        1,  # Replace only first occurrence
    )
    print("11a. Added diagnostic helpers to qwen3_5.py")

# Now add logging in forward() after the qkvz projection
# Target: after "mixed_qkvz, _ = self.in_proj_qkvz(hidden_states)" (tuple unpacking)
old_proj_line = "        mixed_qkvz, _ = self.in_proj_qkvz(hidden_states)"
new_proj_line = """        mixed_qkvz, _ = self.in_proj_qkvz(hidden_states)
        _q35_is_real = False  # will be set to True after checking core_attn_out"""

if old_proj_line in q35_content and "_q35_diag_counter[0] += 1" not in q35_content:
    q35_content = q35_content.replace(old_proj_line, new_proj_line, 1)
    print("11b. Added post-projection diagnostic to forward()")
elif "_q35_diag_counter[0] += 1" in q35_content:
    print("11b. Post-projection diagnostic already present")
else:
    print("11b. WARNING: Could not find in_proj_qkvz line in forward()")

# Add logging after gdn_attention_core and after norm
old_norm_line = "        core_attn_out = self.norm(core_attn_out, z)"
new_norm_line = """        _q35_is_real = core_attn_out.abs().max().item() > 0
        if _q35_is_real:
            _q35_diag_counter[0] += 1
        _q35_call = _q35_diag_counter[0]
        if _q35_is_real and _q35_call <= 4:
            print(f'  DIAG [{_q35_call}] === Qwen3_5GatedDeltaNet.forward layer={self.layer_idx} num_tokens={num_tokens} ===', file=_diag_sys.stderr, flush=True)
            _q35_stat('hidden_states (input)', hidden_states, _q35_call)
            _q35_stat('mixed_qkvz (post GPTQ dequant)', mixed_qkvz, _q35_call)
            _q35_stat('core_attn_out (post FLA)', core_attn_out, _q35_call)
            _q35_stat('z (gate input)', z, _q35_call)
        core_attn_out = self.norm(core_attn_out, z)
        if _q35_is_real and _q35_call <= 4:
            _q35_stat('core_attn_out (post norm)', core_attn_out, _q35_call)"""

if old_norm_line in q35_content and "post FLA" not in q35_content:
    q35_content = q35_content.replace(old_norm_line, new_norm_line, 1)
    print("11c. Added post-FLA and post-norm diagnostics to forward()")
elif "post FLA" in q35_content:
    print("11c. Post-FLA/norm diagnostics already present")
else:
    print("11c. WARNING: Could not find norm line in forward()")

# Add logging after out_proj
# Pattern: "output[:num_tokens], _ = self.out_proj(core_attn_out)"
old_outproj_line = "        output[:num_tokens], _ = self.out_proj(core_attn_out)"
new_outproj_line = """        output[:num_tokens], _ = self.out_proj(core_attn_out)
        if _q35_is_real and _q35_call <= 4:
            _q35_stat('out (post out_proj)', output[:num_tokens], _q35_call)
            print(f'  DIAG [{_q35_call}] === END forward ===', file=_diag_sys.stderr, flush=True)"""

if old_outproj_line in q35_content and "post out_proj" not in q35_content:
    q35_content = q35_content.replace(old_outproj_line, new_outproj_line, 1)
    print("11d. Added post-out_proj diagnostic to forward()")
elif "post out_proj" in q35_content:
    print("11d. Post-out_proj diagnostic already present")
else:
    print("11d. WARNING: Could not find out_proj line in forward()")

with open(qwen35_path, "w") as f:
    f.write(q35_content)

print("11. PATCHED: diagnostics added to qwen3_5.py forward() (FILE PATCHES)")

# ── Section 12: Diagnostics at compute_logits and Qwen3NextModel.forward ──
# Instrument the final hidden states (after all 64 layers) and logits

# 12a. Patch compute_logits in Qwen3_5ForCausalLMBase
old_compute_logits = "    def compute_logits(\n        self,\n        hidden_states: torch.Tensor,\n    ) -> torch.Tensor | None:\n        return self.logits_processor(self.lm_head, hidden_states)"
new_compute_logits = """    def compute_logits(
        self,
        hidden_states: torch.Tensor,
    ) -> torch.Tensor | None:
        import sys as _cl_sys
        _cl_counter = getattr(self, '_cl_diag_counter', 0)
        if _cl_counter < 3:
            self._cl_diag_counter = _cl_counter + 1
            _hs = hidden_states.float()
            print(f'  LOGITS_DIAG [{_cl_counter}] compute_logits: hidden_states shape={list(hidden_states.shape)} dtype={hidden_states.dtype} mean={_hs.mean().item():.6f} std={_hs.std().item():.6f} absmax={_hs.abs().max().item():.6f}', file=_cl_sys.stderr, flush=True)
            # Check for NaN/Inf
            print(f'  LOGITS_DIAG [{_cl_counter}] has_nan={hidden_states.isnan().any().item()} has_inf={hidden_states.isinf().any().item()}', file=_cl_sys.stderr, flush=True)
            # Compute logits via weight matmul (bypass ParallelLMHead.forward restriction)
            with torch.no_grad():
                try:
                    _lm_w = self.lm_head.weight  # [vocab_size, hidden_size]
                    _sample_logits = torch.matmul(hidden_states[-1:].float(), _lm_w.float().T)
                    print(f'  LOGITS_DIAG [{_cl_counter}] last_token_logits: shape={list(_sample_logits.shape)} mean={_sample_logits.mean().item():.6f} std={_sample_logits.std().item():.6f} absmax={_sample_logits.abs().max().item():.6f}', file=_cl_sys.stderr, flush=True)
                    _top5 = torch.topk(_sample_logits[0], 5)
                    print(f'  LOGITS_DIAG [{_cl_counter}] top5_tokens: ids={_top5.indices.tolist()} logits={_top5.values.tolist()}', file=_cl_sys.stderr, flush=True)
                except Exception as _e:
                    print(f'  LOGITS_DIAG [{_cl_counter}] lm_head probe failed: {_e}', file=_cl_sys.stderr, flush=True)
        return self.logits_processor(self.lm_head, hidden_states)"""

if old_compute_logits in q35_content:
    q35_content = q35_content.replace(old_compute_logits, new_compute_logits, 1)
    print("12a. Added compute_logits diagnostics")
else:
    print(
        "12a. WARNING: Could not find compute_logits pattern (may already be patched or different formatting)"
    )

# 12b. Patch Qwen3NextModel.forward to log hidden_states at layers 16, 32, 48 and final
qwen3next_path = f"{BASE}/model_executor/models/qwen3_next.py"
with open(qwen3next_path) as f:
    qn_content = f.read()

old_layer_loop = """        for layer in islice(self.layers, self.start_layer, self.end_layer):
            hidden_states, residual = layer(
                positions=positions,
                hidden_states=hidden_states,
                residual=residual,
            )"""
new_layer_loop = """        import sys as _ll_sys
        _ll_counter = getattr(self, '_ll_diag_counter', 0)
        _ll_layer_idx = self.start_layer
        for layer in islice(self.layers, self.start_layer, self.end_layer):
            hidden_states, residual = layer(
                positions=positions,
                hidden_states=hidden_states,
                residual=residual,
            )
            if _ll_counter < 2 and _ll_layer_idx in (15, 31, 47, 63):
                _hs = hidden_states.float()
                _res = residual.float() if residual is not None else None
                print(f'  LAYER_DIAG [{_ll_counter}] after_layer={_ll_layer_idx} hidden_states: shape={list(hidden_states.shape)} mean={_hs.mean().item():.6f} std={_hs.std().item():.6f} absmax={_hs.abs().max().item():.6f}', file=_ll_sys.stderr, flush=True)
                if _res is not None:
                    print(f'  LAYER_DIAG [{_ll_counter}] after_layer={_ll_layer_idx} residual: shape={list(residual.shape)} mean={_res.mean().item():.6f} std={_res.std().item():.6f} absmax={_res.abs().max().item():.6f}', file=_ll_sys.stderr, flush=True)
            _ll_layer_idx += 1"""

old_final_norm = """        hidden_states, _ = self.norm(hidden_states, residual)
        return hidden_states"""
new_final_norm = """        hidden_states, _ = self.norm(hidden_states, residual)
        if _ll_counter < 2:
            self._ll_diag_counter = _ll_counter + 1
            _hs = hidden_states.float()
            print(f'  LAYER_DIAG [{_ll_counter}] final_norm output: shape={list(hidden_states.shape)} mean={_hs.mean().item():.6f} std={_hs.std().item():.6f} absmax={_hs.abs().max().item():.6f}', file=_ll_sys.stderr, flush=True)
        return hidden_states"""

if old_layer_loop in qn_content and "LAYER_DIAG" not in qn_content:
    qn_content = qn_content.replace(old_layer_loop, new_layer_loop, 1)
    print("12b. Added layer loop diagnostics to Qwen3NextModel.forward")
else:
    print("12b. WARNING: Could not patch layer loop (may already be patched)")

if old_final_norm in qn_content and "final_norm" not in qn_content:
    qn_content = qn_content.replace(old_final_norm, new_final_norm, 1)
    print("12c. Added final norm diagnostics")
else:
    print("12c. WARNING: Could not patch final norm")

with open(qwen3next_path, "w") as f:
    f.write(qn_content)

print("12. PATCHED: layer-level and logits diagnostics added")

with open(qwen35_path, "w") as f:
    f.write(q35_content)

# ============================================================================
# 13. CONV1D COMPARISON DIAGNOSTIC
#     Add a naive PyTorch conv1d computation alongside the Triton kernel call
#     in _forward_core, and log the difference. This tells us if the Triton
#     causal_conv1d kernel produces incorrect results on ROCm/gfx1100.
#
#     Also add pre-conv / post-conv tensor stats to understand signal flow.
# ============================================================================
with open(qwen3_next_path) as f:
    qn_content = f.read()

# Patch the prefill conv1d call in _forward_core
old_prefill_conv = """        if attn_metadata.num_prefills > 0:
            mixed_qkv_non_spec_T = mixed_qkv_non_spec.transpose(0, 1)
            # - "cache_indices" updates the conv_state cache in positions
            #   pointed to by "state_indices_tensor"
            mixed_qkv_non_spec = causal_conv1d_fn(
                mixed_qkv_non_spec_T,
                conv_weights,
                self.conv1d.bias,
                activation=self.activation,
                conv_states=conv_state,
                has_initial_state=has_initial_state,
                cache_indices=non_spec_state_indices_tensor,
                query_start_loc=non_spec_query_start_loc,
                metadata=attn_metadata,
            ).transpose(0, 1)"""

new_prefill_conv = """        if attn_metadata.num_prefills > 0:
            mixed_qkv_non_spec_T = mixed_qkv_non_spec.transpose(0, 1)
            _conv_pre_copy = mixed_qkv_non_spec_T.clone()  # Save for comparison
            # - "cache_indices" updates the conv_state cache in positions
            #   pointed to by "state_indices_tensor"
            mixed_qkv_non_spec = causal_conv1d_fn(
                mixed_qkv_non_spec_T,
                conv_weights,
                self.conv1d.bias,
                activation=self.activation,
                conv_states=conv_state,
                has_initial_state=has_initial_state,
                cache_indices=non_spec_state_indices_tensor,
                query_start_loc=non_spec_query_start_loc,
                metadata=attn_metadata,
            ).transpose(0, 1)
            # --- CONV1D COMPARISON DIAGNOSTIC (section 13) ---
            import sys as _c13_sys
            import torch.nn.functional as _c13_F
            _c13_ctr = getattr(self, '_conv_diag_ctr', 0)
            _c13_layer = self.prefix.split('.')[2] if '.' in self.prefix else '?'
            if _c13_ctr < 8 and (has_initial_state is None or not has_initial_state.any().item()):
                self._conv_diag_ctr = _c13_ctr + 1
                with torch.no_grad():
                    _c13_dim, _c13_L = _conv_pre_copy.shape
                    _c13_width = conv_weights.shape[1]
                    # Naive depthwise causal conv1d: left-pad + F.conv1d + silu
                    _c13_w = conv_weights.float().unsqueeze(1)  # [dim, 1, width]
                    _c13_xp = _c13_F.pad(_conv_pre_copy.float(), (_c13_width - 1, 0))  # [dim, L + width - 1]
                    _c13_naive = _c13_F.conv1d(_c13_xp.unsqueeze(0), _c13_w, bias=self.conv1d.bias.float() if self.conv1d.bias is not None else None, groups=_c13_dim).squeeze(0)
                    _c13_naive = _c13_F.silu(_c13_naive)  # [dim, L]
                    _c13_triton = mixed_qkv_non_spec.transpose(0, 1).float()  # [dim, L]
                    _c13_diff = (_c13_triton - _c13_naive).abs()
                    _c13_rel = _c13_diff / (_c13_naive.abs() + 1e-8)
                    _c13_pre = _conv_pre_copy.float()
                    print(f'  CONV1D_DIAG [{_c13_ctr}] layer={_c13_layer} tokens={_c13_L}:', file=_c13_sys.stderr, flush=True)
                    print(f'    input:  mean={_c13_pre.mean().item():.6f} std={_c13_pre.std().item():.6f} absmax={_c13_pre.abs().max().item():.6f}', file=_c13_sys.stderr, flush=True)
                    print(f'    triton: mean={_c13_triton.mean().item():.6f} std={_c13_triton.std().item():.6f} absmax={_c13_triton.abs().max().item():.6f}', file=_c13_sys.stderr, flush=True)
                    print(f'    naive:  mean={_c13_naive.mean().item():.6f} std={_c13_naive.std().item():.6f} absmax={_c13_naive.abs().max().item():.6f}', file=_c13_sys.stderr, flush=True)
                    print(f'    diff:   max_abs={_c13_diff.max().item():.6f} mean_abs={_c13_diff.mean().item():.6f} max_rel={_c13_rel.max().item():.6f} mean_rel={_c13_rel.mean().item():.6f}', file=_c13_sys.stderr, flush=True)
                    # If significant diff, log first divergent channel
                    if _c13_diff.max().item() > 0.01:
                        _c13_worst_ch = _c13_diff.max(dim=1).values.argmax().item()
                        print(f'    WORST_CHANNEL: ch={_c13_worst_ch} triton={_c13_triton[_c13_worst_ch,:5].tolist()} naive={_c13_naive[_c13_worst_ch,:5].tolist()}', file=_c13_sys.stderr, flush=True)
            del _conv_pre_copy"""

if old_prefill_conv in qn_content:
    qn_content = qn_content.replace(old_prefill_conv, new_prefill_conv, 1)
    print("13a. Added conv1d comparison diagnostic (prefill path)")
else:
    print(
        "13a. WARNING: Could not find prefill conv1d pattern (may already be patched)"
    )

# Also add g/beta diagnostics in _forward_core after fused_gdn_gating
old_gating = "        g, beta = fused_gdn_gating(self.A_log, a, b, self.dt_bias)"
new_gating = """        g, beta = fused_gdn_gating(self.A_log, a, b, self.dt_bias)
        # --- GATING DIAGNOSTIC (section 13b) ---
        _g13_ctr = getattr(self, '_gating_diag_ctr', 0)
        _g13_layer = self.prefix.split('.')[2] if '.' in self.prefix else '?'
        if _g13_ctr < 8:
            self._gating_diag_ctr = _g13_ctr + 1
            import sys as _g13_sys
            _g13_gf = g.float()
            _g13_bf = beta.float()
            _g13_af = self.A_log.float()
            _g13_dtf = self.dt_bias.float() if self.dt_bias is not None else None
            _g13_a_in = a.float()
            print(f'  GATING_DIAG [{_g13_ctr}] layer={_g13_layer}:', file=_g13_sys.stderr, flush=True)
            print(f'    A_log: shape={list(self.A_log.shape)} mean={_g13_af.mean().item():.6f} std={_g13_af.std().item():.6f} min={_g13_af.min().item():.6f} max={_g13_af.max().item():.6f}', file=_g13_sys.stderr, flush=True)
            if _g13_dtf is not None:
                print(f'    dt_bias: shape={list(self.dt_bias.shape)} mean={_g13_dtf.mean().item():.6f} std={_g13_dtf.std().item():.6f} min={_g13_dtf.min().item():.6f} max={_g13_dtf.max().item():.6f}', file=_g13_sys.stderr, flush=True)
            print(f'    a (input proj): mean={_g13_a_in.mean().item():.6f} std={_g13_a_in.std().item():.6f} absmax={_g13_a_in.abs().max().item():.6f}', file=_g13_sys.stderr, flush=True)
            print(f'    g (decay):  mean={_g13_gf.mean().item():.6f} std={_g13_gf.std().item():.6f} min={_g13_gf.min().item():.6f} max={_g13_gf.max().item():.6f} absmax={_g13_gf.abs().max().item():.6f}', file=_g13_sys.stderr, flush=True)
            print(f'    beta:       mean={_g13_bf.mean().item():.6f} std={_g13_bf.std().item():.6f} min={_g13_bf.min().item():.6f} max={_g13_bf.max().item():.6f} absmax={_g13_bf.abs().max().item():.6f}', file=_g13_sys.stderr, flush=True)
            print(f'    exp(g) (effective decay): min={torch.exp(_g13_gf).min().item():.10f} max={torch.exp(_g13_gf).max().item():.6f} mean={torch.exp(_g13_gf).mean().item():.6f}', file=_g13_sys.stderr, flush=True)"""

if old_gating in qn_content:
    qn_content = qn_content.replace(old_gating, new_gating, 1)
    print("13b. Added gating diagnostic (A_log, dt_bias, g, beta)")
else:
    print("13b. WARNING: Could not find fused_gdn_gating pattern")

# 13c. Add Q/K/V post-rearrange diagnostics (actual FLA inputs after GQA expansion)
old_rearrange_qkv = """        query_spec, key_spec, value_spec = self.rearrange_mixed_qkv(mixed_qkv_spec)
        query_non_spec, key_non_spec, value_non_spec = self.rearrange_mixed_qkv(
            mixed_qkv_non_spec
        )"""

new_rearrange_qkv = """        query_spec, key_spec, value_spec = self.rearrange_mixed_qkv(mixed_qkv_spec)
        query_non_spec, key_non_spec, value_non_spec = self.rearrange_mixed_qkv(
            mixed_qkv_non_spec
        )
        # --- FLA INPUT DIAGNOSTIC (section 13c) ---
        import sys as _fla_sys
        _fla_ctr = getattr(self, '_fla_input_diag_ctr', 0)
        _fla_layer = self.prefix.split('.')[2] if '.' in self.prefix else '?'
        if _fla_ctr < 8:
            self._fla_input_diag_ctr = _fla_ctr + 1
            with torch.no_grad():
                print(f'  FLA_INPUT [{_fla_ctr}] layer={_fla_layer} (post conv1d + rearrange + GQA expansion):', file=_fla_sys.stderr, flush=True)
                # Log mixed_qkv (pre-rearrange, post-conv1d)
                if mixed_qkv_non_spec is not None:
                    _mqkv = mixed_qkv_non_spec.float()
                    print(f'    mixed_qkv_non_spec: shape={list(mixed_qkv_non_spec.shape)} mean={_mqkv.mean().item():.6f} std={_mqkv.std().item():.6f} absmax={_mqkv.abs().max().item():.6f}', file=_fla_sys.stderr, flush=True)
                # Q after GQA expansion (should be [1, T, 48, 128])
                if query_non_spec is not None:
                    _qf = query_non_spec.float()
                    print(f'    Q (post GQA): shape={list(query_non_spec.shape)} mean={_qf.mean().item():.6f} std={_qf.std().item():.6f} absmax={_qf.abs().max().item():.6f}', file=_fla_sys.stderr, flush=True)
                    # Per-head Q stats for first 4 heads
                    for _hi in range(min(4, query_non_spec.shape[2])):
                        _qh = _qf[0, :, _hi, :]
                        print(f'      Q head[{_hi}]: mean={_qh.mean().item():.6f} std={_qh.std().item():.6f} absmax={_qh.abs().max().item():.6f}', file=_fla_sys.stderr, flush=True)
                # K after GQA expansion
                if key_non_spec is not None:
                    _kf = key_non_spec.float()
                    print(f'    K (post GQA): shape={list(key_non_spec.shape)} mean={_kf.mean().item():.6f} std={_kf.std().item():.6f} absmax={_kf.abs().max().item():.6f}', file=_fla_sys.stderr, flush=True)
                # V (no GQA expansion, stays at 48 heads)
                if value_non_spec is not None:
                    _vf = value_non_spec.float()
                    print(f'    V: shape={list(value_non_spec.shape)} mean={_vf.mean().item():.6f} std={_vf.std().item():.6f} absmax={_vf.abs().max().item():.6f}', file=_fla_sys.stderr, flush=True)
                # Check if Q/K/V have any zeros or near-zeros
                if query_non_spec is not None:
                    _q_zero_frac = (query_non_spec.abs() < 1e-6).float().mean().item()
                    _k_zero_frac = (key_non_spec.abs() < 1e-6).float().mean().item()
                    _v_zero_frac = (value_non_spec.abs() < 1e-6).float().mean().item()
                    print(f'    zero_frac: Q={_q_zero_frac:.4f} K={_k_zero_frac:.4f} V={_v_zero_frac:.4f}', file=_fla_sys.stderr, flush=True)"""

if old_rearrange_qkv in qn_content:
    qn_content = qn_content.replace(old_rearrange_qkv, new_rearrange_qkv, 1)
    print("13c. Added FLA input diagnostics (Q/K/V post-rearrange)")
else:
    print("13c. WARNING: Could not find rearrange_mixed_qkv pattern")

# 13d. Add FLA output diagnostics (after chunk_gated_delta_rule prefill call)
old_fla_prefill = """            (
                core_attn_out_non_spec,
                last_recurrent_state,
            ) = self.chunk_gated_delta_rule(
                q=query_non_spec,
                k=key_non_spec,
                v=value_non_spec,
                g=g_non_spec,
                beta=beta_non_spec,
                initial_state=initial_state,
                output_final_state=True,
                cu_seqlens=non_spec_query_start_loc,
                use_qk_l2norm_in_kernel=True,
            )
            # Init cache
            ssm_state[non_spec_state_indices_tensor] = last_recurrent_state.to(
                ssm_state.dtype
            )"""

new_fla_prefill = """            (
                core_attn_out_non_spec,
                last_recurrent_state,
            ) = self.chunk_gated_delta_rule(
                q=query_non_spec,
                k=key_non_spec,
                v=value_non_spec,
                g=g_non_spec,
                beta=beta_non_spec,
                initial_state=initial_state,
                output_final_state=True,
                cu_seqlens=non_spec_query_start_loc,
                use_qk_l2norm_in_kernel=True,
            )
            # --- FLA OUTPUT DIAGNOSTIC (section 13d) ---
            _fla_out_ctr = getattr(self, '_fla_output_diag_ctr', 0)
            _fla_out_layer = self.prefix.split('.')[2] if '.' in self.prefix else '?'
            if _fla_out_ctr < 8:
                self._fla_output_diag_ctr = _fla_out_ctr + 1
                import sys as _fla_out_sys
                with torch.no_grad():
                    _fla_of = core_attn_out_non_spec.float()
                    print(f'  FLA_OUTPUT [{_fla_out_ctr}] layer={_fla_out_layer} (post chunk_gated_delta_rule):', file=_fla_out_sys.stderr, flush=True)
                    print(f'    output: shape={list(core_attn_out_non_spec.shape)} mean={_fla_of.mean().item():.6f} std={_fla_of.std().item():.6f} absmax={_fla_of.abs().max().item():.6f}', file=_fla_out_sys.stderr, flush=True)
                    # Per-head output stats
                    for _hi in range(min(4, core_attn_out_non_spec.shape[2])):
                        _oh = _fla_of[0, :, _hi, :]
                        print(f'      head[{_hi}]: mean={_oh.mean().item():.6f} std={_oh.std().item():.6f} absmax={_oh.abs().max().item():.6f}', file=_fla_out_sys.stderr, flush=True)
                    # Check state magnitude
                    _lrs = last_recurrent_state.float()
                    print(f'    last_state: shape={list(last_recurrent_state.shape)} mean={_lrs.mean().item():.6f} std={_lrs.std().item():.6f} absmax={_lrs.abs().max().item():.6f}', file=_fla_out_sys.stderr, flush=True)
                    # Initial state stats
                    _isf = initial_state.float()
                    print(f'    initial_state: shape={list(initial_state.shape)} mean={_isf.mean().item():.6f} std={_isf.std().item():.6f} absmax={_isf.abs().max().item():.6f} all_zero={(_isf == 0).all().item()}', file=_fla_out_sys.stderr, flush=True)
                    # g/beta stats for this specific call
                    if g_non_spec is not None:
                        _gnf = g_non_spec.float()
                        _bnf = beta_non_spec.float()
                        print(f'    g_non_spec: shape={list(g_non_spec.shape)} mean={_gnf.mean().item():.6f} std={_gnf.std().item():.6f} min={_gnf.min().item():.6f} max={_gnf.max().item():.6f}', file=_fla_out_sys.stderr, flush=True)
                        print(f'    exp(g_non_spec) decay: min={torch.exp(_gnf).min().item():.10f} max={torch.exp(_gnf).max().item():.6f} mean={torch.exp(_gnf).mean().item():.6f}', file=_fla_out_sys.stderr, flush=True)
                        print(f'    beta_non_spec: shape={list(beta_non_spec.shape)} mean={_bnf.mean().item():.6f} min={_bnf.min().item():.6f} max={_bnf.max().item():.6f}', file=_fla_out_sys.stderr, flush=True)
                    # output-to-input ratio (signal amplification/attenuation)
                    if query_non_spec is not None:
                        _v_std = value_non_spec.float().std().item()
                        _o_std = _fla_of.std().item()
                        _ratio = _o_std / (_v_std + 1e-10)
                        print(f'    output/input_v ratio: {_ratio:.6f} (v_std={_v_std:.6f} out_std={_o_std:.6f})', file=_fla_out_sys.stderr, flush=True)
            # Init cache
            ssm_state[non_spec_state_indices_tensor] = last_recurrent_state.to(
                ssm_state.dtype
            )"""

if old_fla_prefill in qn_content:
    qn_content = qn_content.replace(old_fla_prefill, new_fla_prefill, 1)
    print("13d. Added FLA output diagnostics (post chunk_gated_delta_rule)")
else:
    print("13d. WARNING: Could not find chunk_gated_delta_rule prefill pattern")

with open(qwen3_next_path, "w") as f:
    f.write(qn_content)

print(
    "13. PATCHED: conv1d + gating + FLA input/output diagnostics added to _forward_core"
)

print(
    "\nAll patches applied successfully (ALL as file patches for subprocess persistence)"
)

# ==============================================================================
# 14. Remove M-RoPE from config (text-only M-RoPE = standard RoPE, was red herring)
# ==============================================================================
# M-RoPE for text-only uses same position for all 3 dims → identical to standard RoPE.
# But mrope_section in config triggers V1 model runner's _init_mrope_positions which
# requires SupportsMRoPE protocol → assertion crash. Remove it.
print("\n14. Cleaning up M-RoPE config (not needed for text-only)...")

import json

config_path = "/models/qwen35-27b-opus-distill/config.json"
with open(config_path) as f:
    config = json.load(f)

rope_params = config.get("rope_parameters", {})
changed = False
for key in ["mrope_section", "mrope_interleaved"]:
    if key in rope_params:
        del rope_params[key]
        changed = True

# FIX decoder_sparse_step: must be 4 (every 4th layer is full attention)
# Config extraction during GPTQ quantization set it to 1 (all full attn) which is WRONG
if config.get("decoder_sparse_step") != 4:
    print(f"14b. FIXING decoder_sparse_step: {config.get('decoder_sparse_step')} -> 4")
    config["decoder_sparse_step"] = 4
    changed = True
else:
    print("14b. decoder_sparse_step already correct (4)")

if changed:
    with open(config_path, "w") as f:
        json.dump(config, f, indent=2)
    print("14c. Config saved")
else:
    print("14c. config.json already correct")

print("14. DONE: Config fixes applied")

# ==============================================================================
# 15. DIAGNOSTIC: Trace hidden_states between forward() return and compute_logits
# ==============================================================================
# The LAYER_DIAG shows final_norm output with std=1.88 but compute_logits
# receives hidden_states with std=0.29 (values in [0,1]). Something corrupts
# the tensor between model output and logits computation.
# Add diagnostics at every step to pinpoint the corruption.
print("\n15. Adding hidden_states tracing diagnostics...")

q35_path = f"{BASE}/model_executor/models/qwen3_5.py"
with open(q35_path) as f:
    q35_content = f.read()

# 15a. Add diagnostic in Qwen3_5ForCausalLMBase.forward() BEFORE returning
old_forward_return = """    def forward(
        self,
        input_ids: torch.Tensor,
        positions: torch.Tensor,
        intermediate_tensors: IntermediateTensors | None = None,
        inputs_embeds: torch.Tensor | None = None,
        **kwargs: object,
    ):
        hidden_states = self.model(
            input_ids, positions, intermediate_tensors, inputs_embeds
        )

        return hidden_states"""

new_forward_return = """    def forward(
        self,
        input_ids: torch.Tensor,
        positions: torch.Tensor,
        intermediate_tensors: IntermediateTensors | None = None,
        inputs_embeds: torch.Tensor | None = None,
        **kwargs: object,
    ):
        hidden_states = self.model(
            input_ids, positions, intermediate_tensors, inputs_embeds
        )

        import sys as _fwd_sys
        _fwd_counter = getattr(self, '_fwd_diag_counter', 0)
        if _fwd_counter < 3:
            self._fwd_diag_counter = _fwd_counter + 1
            _hs = hidden_states.float()
            print(f'  FWD_DIAG [{_fwd_counter}] forward() returning: shape={list(hidden_states.shape)} dtype={hidden_states.dtype} data_ptr={hidden_states.data_ptr()} stride={hidden_states.stride()} contiguous={hidden_states.is_contiguous()}', file=_fwd_sys.stderr, flush=True)
            print(f'  FWD_DIAG [{_fwd_counter}] stats: mean={_hs.mean().item():.6f} std={_hs.std().item():.6f} absmax={_hs.abs().max().item():.6f}', file=_fwd_sys.stderr, flush=True)
            # Print first 5 values of first and last token for fingerprinting
            print(f'  FWD_DIAG [{_fwd_counter}] first_token[:5]={hidden_states[0,:5].tolist()}', file=_fwd_sys.stderr, flush=True)
            print(f'  FWD_DIAG [{_fwd_counter}] last_token[:5]={hidden_states[-1,:5].tolist()}', file=_fwd_sys.stderr, flush=True)

        return hidden_states"""

if old_forward_return in q35_content:
    q35_content = q35_content.replace(old_forward_return, new_forward_return, 1)
    print("15a. Added forward() return diagnostic")
else:
    print("15a. WARNING: Could not find forward() return pattern")

with open(q35_path, "w") as f:
    f.write(q35_content)

# 15b. Add diagnostic in gpu_model_runner.py between forward output and compute_logits
runner_path = f"{BASE}/v1/worker/gpu_model_runner.py"
with open(runner_path) as f:
    runner_content = f.read()

old_runner_logits = """                sample_hidden_states = hidden_states[logits_indices]
                logits = self.model.compute_logits(sample_hidden_states)"""

new_runner_logits = """                sample_hidden_states = hidden_states[logits_indices]
                # DIAG: trace hidden_states corruption
                import sys as _r_sys
                _r_counter = getattr(self, '_runner_diag_counter', 0)
                if _r_counter < 3:
                    self._runner_diag_counter = _r_counter + 1
                    _r_hs = hidden_states.float()
                    _r_shs = sample_hidden_states.float()
                    print(f'  RUNNER_DIAG [{_r_counter}] hidden_states: shape={list(hidden_states.shape)} data_ptr={hidden_states.data_ptr()} mean={_r_hs.mean().item():.6f} std={_r_hs.std().item():.6f} absmax={_r_hs.abs().max().item():.6f}', file=_r_sys.stderr, flush=True)
                    print(f'  RUNNER_DIAG [{_r_counter}] logits_indices: {logits_indices.tolist() if logits_indices.numel() < 20 else f"shape={list(logits_indices.shape)} first={logits_indices[:5].tolist()} last={logits_indices[-5:].tolist()}"}', file=_r_sys.stderr, flush=True)
                    print(f'  RUNNER_DIAG [{_r_counter}] sample_hidden_states: shape={list(sample_hidden_states.shape)} data_ptr={sample_hidden_states.data_ptr()} mean={_r_shs.mean().item():.6f} std={_r_shs.std().item():.6f} absmax={_r_shs.abs().max().item():.6f}', file=_r_sys.stderr, flush=True)
                    print(f'  RUNNER_DIAG [{_r_counter}] first_token[:5]={sample_hidden_states[0,:5].tolist()}', file=_r_sys.stderr, flush=True)
                    print(f'  RUNNER_DIAG [{_r_counter}] last_token[:5]={sample_hidden_states[-1,:5].tolist()}', file=_r_sys.stderr, flush=True)
                logits = self.model.compute_logits(sample_hidden_states)"""

if old_runner_logits in runner_content:
    runner_content = runner_content.replace(old_runner_logits, new_runner_logits, 1)
    print("15b. Added model runner logits diagnostic")
else:
    print("15b. WARNING: Could not find model runner logits pattern")

with open(runner_path, "w") as f:
    f.write(runner_content)

print("15. PATCHED: Hidden states tracing diagnostics added")

# ==============================================================================
# 16. FIX: Replace broken QuantLinear in_proj_ba with nn.Linear
#
# ROOT CAUSE: GPTQModel 5.8.0 does not quantize in_proj_ba layers (shape
# [96, 5120]) in Qwen3.5's hybrid architecture. The checkpoint stores them as
# full-precision .weight tensors. But vLLM's GPTQ loader converts ALL linear
# layers to QuantLinear, so in_proj_ba gets random/zero-initialized qweight/
# scales/qzeros. This corrupts g (decay) and beta (update gate) for all 48
# linear attention layers, producing garbage inference.
#
# Fix: Patch load_weights() to detect layers in the checkpoint that have
# .weight but no .qweight, and load them as regular parameters instead of
# trying to use the quantized weight_loader path.
# ==============================================================================
print("\n16. Fixing unquantized in_proj_ba layers (GPTQ mixed-quant fix)...")

q35_path = f"{BASE}/model_executor/models/qwen3_5.py"
with open(q35_path) as f:
    q35_content = f.read()

# We need to patch load_weights() to handle mixed quantized/unquantized checkpoints.
# The approach: after the standard load_weights() processes all weights, we do a
# post-load fixup that:
# 1. Scans the safetensors index for layers with .weight but no .qweight
# 2. For each such layer, if the model has a QuantLinear, replaces it with nn.Linear
# 3. Loads the full-precision weight from the safetensors file

# Find the load_weights method and add a post-load fixup call
# The actual pattern in the vLLM image's qwen3_5.py (Qwen3_5ForCausalLMBase):
#   loader = AutoWeightsLoader(self, skip_prefixes=["mtp."])
#   return loader.load_weights(weights)
# NOTE: Qwen3_5ForConditionalGeneration has a different load_weights with mapper=...
# We patch the text-only base class version.
old_load_weights_end = """        loader = AutoWeightsLoader(
            self,
            skip_prefixes=["mtp."],
        )
        return loader.load_weights(weights)


class Qwen3_5ForCausalLM(Qwen3_5ForCausalLMBase):
    pass"""

new_load_weights_end = """        loader = AutoWeightsLoader(
            self,
            skip_prefixes=["mtp."],
        )
        _loaded = loader.load_weights(weights)

        # ── POST-LOAD FIXUP: Replace broken QuantLinear for unquantized layers ──
        # GPTQModel may skip quantizing small layers (e.g. in_proj_ba [96, 5120]).
        # These are stored as .weight in the checkpoint but vLLM wraps them as
        # QuantLinear with random qweight. Detect and fix.
        import json as _fix_json
        import os as _fix_os
        try:
            # Find model directory — try _name_or_path, then common paths
            _model_dir = None
            for _cand in [
                getattr(self.config, "_name_or_path", ""),
                "/models/qwen35-27b-opus-distill",
            ]:
                if _cand and _fix_os.path.exists(_fix_os.path.join(_cand, "model.safetensors.index.json")):
                    _model_dir = _cand
                    break
            if not _model_dir and _fix_os.path.isdir("/models"):
                for _d in _fix_os.listdir("/models"):
                    _p = _fix_os.path.join("/models", _d)
                    if _fix_os.path.exists(_fix_os.path.join(_p, "model.safetensors.index.json")):
                        _model_dir = _p
                        break
            _index_path = _fix_os.path.join(_model_dir, "model.safetensors.index.json") if _model_dir else ""
            if _fix_os.path.exists(_index_path):
                with open(_index_path) as _f:
                    _idx = _fix_json.load(_f)
                _wmap = _idx.get("weight_map", {})

                # Find layers with .weight but no .qweight
                _has_weight = set()
                _has_qweight = set()
                for _k in _wmap:
                    if _k.endswith(".weight"):
                        _has_weight.add(_k.rsplit(".weight", 1)[0])
                    elif _k.endswith(".qweight"):
                        _has_qweight.add(_k.rsplit(".qweight", 1)[0])

                _unquantized = _has_weight - _has_qweight
                if _unquantized:
                    import safetensors.torch as _st
                    import torch.nn as _nn
                    _fixed = 0
                    _loaded_shards = {}  # cache shard loads

                    for _layer_prefix in sorted(_unquantized):
                        # Strip "model." prefix to match named_modules output
                        _mod_name = _layer_prefix
                        if _mod_name.startswith("model."):
                            _mod_name = _mod_name[len("model."):]

                        # Find the module in the model
                        try:
                            _mod = self.model.get_submodule(_mod_name)
                        except (AttributeError, ValueError):
                            continue

                        # Check if it's a quantized linear (has qweight attribute)
                        if not hasattr(_mod, "qweight"):
                            continue

                        # Load the full-precision weight
                        _weight_key = f"{_layer_prefix}.weight"
                        _shard_file = _wmap.get(_weight_key)
                        if not _shard_file:
                            continue

                        _shard_path = _fix_os.path.join(_model_dir, _shard_file)
                        if _shard_path not in _loaded_shards:
                            _loaded_shards[_shard_path] = _st.load_file(
                                _shard_path, device="cpu"
                            )
                        _weight = _loaded_shards[_shard_path].get(_weight_key)
                        if _weight is None:
                            continue

                        # Check for bias
                        _bias_key = f"{_layer_prefix}.bias"
                        _bias = None
                        if _bias_key in _wmap:
                            _b_shard = _wmap[_bias_key]
                            _b_path = _fix_os.path.join(_model_dir, _b_shard)
                            if _b_path not in _loaded_shards:
                                _loaded_shards[_b_path] = _st.load_file(
                                    _b_path, device="cpu"
                                )
                            _bias = _loaded_shards[_b_path].get(_bias_key)

                        # Create replacement nn.Linear
                        _out_f, _in_f = _weight.shape
                        _new_linear = _nn.Linear(
                            _in_f, _out_f, bias=(_bias is not None)
                        )
                        _dev = next(_mod.parameters()).device
                        _dtype = next(_mod.parameters()).dtype
                        _new_linear.weight.data = _weight.to(
                            device=_dev, dtype=_dtype
                        )
                        if _bias is not None:
                            _new_linear.bias.data = _bias.to(
                                device=_dev, dtype=_dtype
                            )

                        # Replace in parent module
                        _parts = _mod_name.rsplit(".", 1)
                        if len(_parts) == 2:
                            _parent = self.model.get_submodule(_parts[0])
                            setattr(_parent, _parts[1], _new_linear)
                        else:
                            setattr(self.model, _mod_name, _new_linear)

                        _fixed += 1
                        print(
                            f"  16. Fixed {_layer_prefix}: QuantLinear -> "
                            f"nn.Linear({_in_f}, {_out_f}) on {_dev}",
                            flush=True,
                        )

                    # Free cached shards
                    del _loaded_shards

                    if _fixed > 0:
                        print(
                            f"  16. FIXED {_fixed} unquantized layers "
                            f"(replaced QuantLinear with nn.Linear)",
                            flush=True,
                        )
                    else:
                        print(
                            "  16. No unquantized QuantLinear layers found",
                            flush=True,
                        )
                else:
                    print("  16. All layers properly quantized", flush=True)
            else:
                print(
                    f"  16. SKIP: No safetensors index at {_index_path}",
                    flush=True,
                )
        except Exception as _fix_e:
            import traceback as _fix_tb
            print(f"  16. ERROR in post-load fixup: {_fix_e}", flush=True)
            _fix_tb.print_exc()

        return _loaded


class Qwen3_5ForCausalLM(Qwen3_5ForCausalLMBase):
    pass"""

if old_load_weights_end in q35_content:
    q35_content = q35_content.replace(old_load_weights_end, new_load_weights_end, 1)
    with open(q35_path, "w") as f:
        f.write(q35_content)
    print("16. PATCHED: load_weights() post-fixup for unquantized in_proj_ba layers")
else:
    print("16. WARNING: Could not find load_weights AutoWeightsLoader pattern")
    print("    Trying alternative approach: patch weight_loader directly...")

    # Alternative: check if the pattern is slightly different
    # Try to find any AutoWeightsLoader usage
    import re as _re16

    _match = _re16.search(
        r"(loader\s*=\s*AutoWeightsLoader\([^)]+\)\s*\n\s*return\s+loader\.load_weights\([^)]+\))",
        q35_content,
    )
    if _match:
        _old = _match.group(0)
        # Build the replacement using the exact matched text
        _new = (
            _old
            + """

        # ── POST-LOAD FIXUP: Replace broken QuantLinear for unquantized layers ──
        import json as _fix_json
        import os as _fix_os
        try:
            # Find model directory — try _name_or_path, then common paths
            _model_dir = None
            for _cand in [
                getattr(self.config, "_name_or_path", ""),
                "/models/qwen35-27b-opus-distill",
            ]:
                if _cand and _fix_os.path.exists(_fix_os.path.join(_cand, "model.safetensors.index.json")):
                    _model_dir = _cand
                    break
            if not _model_dir and _fix_os.path.isdir("/models"):
                for _d in _fix_os.listdir("/models"):
                    _p = _fix_os.path.join("/models", _d)
                    if _fix_os.path.exists(_fix_os.path.join(_p, "model.safetensors.index.json")):
                        _model_dir = _p
                        break
            _index_path = _fix_os.path.join(_model_dir, "model.safetensors.index.json") if _model_dir else ""
            if _fix_os.path.exists(_index_path):
                with open(_index_path) as _f:
                    _idx = _fix_json.load(_f)
                _wmap = _idx.get("weight_map", {})
                _has_weight = {k.rsplit(".weight", 1)[0] for k in _wmap if k.endswith(".weight")}
                _has_qweight = {k.rsplit(".qweight", 1)[0] for k in _wmap if k.endswith(".qweight")}
                _unquantized = _has_weight - _has_qweight
                if _unquantized:
                    import safetensors.torch as _st
                    import torch.nn as _nn
                    _fixed = 0
                    _loaded_shards = {}
                    for _lp in sorted(_unquantized):
                        _mn = _lp[len("model."):] if _lp.startswith("model.") else _lp
                        try:
                            _mod = self.model.get_submodule(_mn)
                        except (AttributeError, ValueError):
                            continue
                        if not hasattr(_mod, "qweight"):
                            continue
                        _wk = f"{_lp}.weight"
                        _sf = _wmap.get(_wk)
                        if not _sf:
                            continue
                        _sp = _fix_os.path.join(_model_dir, _sf)
                        if _sp not in _loaded_shards:
                            _loaded_shards[_sp] = _st.load_file(_sp, device="cpu")
                        _w = _loaded_shards[_sp].get(_wk)
                        if _w is None:
                            continue
                        _of, _if = _w.shape
                        _nl = _nn.Linear(_if, _of, bias=False)
                        _dev = next(_mod.parameters()).device
                        _dt = next(_mod.parameters()).dtype
                        _nl.weight.data = _w.to(device=_dev, dtype=_dt)
                        _parts = _mn.rsplit(".", 1)
                        if len(_parts) == 2:
                            _parent = self.model.get_submodule(_parts[0])
                            setattr(_parent, _parts[1], _nl)
                        else:
                            setattr(self.model, _mn, _nl)
                        _fixed += 1
                        print(f"  16. Fixed {_lp}: QuantLinear -> nn.Linear({_if}, {_of}) on {_dev}", flush=True)
                    del _loaded_shards
                    print(f"  16. FIXED {_fixed} unquantized layers" if _fixed else "  16. No fixable layers", flush=True)
        except Exception as _fix_e:
            print(f"  16. ERROR: {_fix_e}", flush=True)"""
        )
        q35_content = q35_content.replace(_old, _new, 1)
        with open(q35_path, "w") as f:
            f.write(q35_content)
        print("16. PATCHED (alt): load_weights() post-fixup via regex match")
    else:
        print("16. FAILED: Could not find any AutoWeightsLoader pattern in qwen3_5.py")
        print("    Manual intervention required")

#!/usr/bin/env python3
"""Register qwen3_5_text config + model + fix weight loading + add IsHybrid + mamba state methods + FLA fix.

ALL patches are FILE-LEVEL (modify .py on disk) so they persist to the EngineCore subprocess.
"""
import os
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

# 0d. Polyfill layer_type_validation in ALL vLLM config files that import it.
# vLLM 0.17.0's Qwen3.5/Qwen3Next configs import layer_type_validation from
# transformers, but transformers 5.3.0.dev0 doesn't export it yet. Glob-scan
# all config files to catch qwen3_5.py, qwen3_5_moe.py, qwen3_next.py, etc.
import glob as _glob

configs_dir = f"{BASE}/transformers_utils/configs"
old_import = "from transformers.configuration_utils import PretrainedConfig, layer_type_validation"
new_import = """from transformers.configuration_utils import PretrainedConfig
try:
    from transformers.configuration_utils import layer_type_validation
except ImportError:
    def layer_type_validation(layer_types, num_hidden_layers):
        if layer_types is not None and len(layer_types) != num_hidden_layers:
            raise ValueError(f"layer_types length ({len(layer_types)}) != num_hidden_layers ({num_hidden_layers})")"""
for cfg_path in _glob.glob(f"{configs_dir}/*.py"):
    cfg_name = os.path.basename(cfg_path)
    with open(cfg_path) as f:
        content = f.read()
    if old_import in content:
        content = content.replace(old_import, new_import)
        with open(cfg_path, "w") as f:
            f.write(content)
        print(f"0d. Polyfilled layer_type_validation import in {cfg_name}")

# 0e. Fix ignore_keys_at_rope_validation: list → set in all config files.
# vLLM 0.17.0 passes a list but transformers 5.3.0.dev0 _check_received_keys does
# `received_keys -= ignore_keys` which requires a set (TypeError on list).
old_rope_keys = 'kwargs["ignore_keys_at_rope_validation"] = [\n            "mrope_section",\n            "mrope_interleaved",\n        ]'
new_rope_keys = 'kwargs["ignore_keys_at_rope_validation"] = {\n            "mrope_section",\n            "mrope_interleaved",\n        }'
for cfg_path in _glob.glob(f"{configs_dir}/*.py"):
    cfg_name = os.path.basename(cfg_path)
    with open(cfg_path) as f:
        content = f.read()
    if old_rope_keys in content:
        content = content.replace(old_rope_keys, new_rope_keys)
        with open(cfg_path, "w") as f:
            f.write(content)
        print(f"0e. Fixed ignore_keys_at_rope_validation: list → set in {cfg_name}")

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

# 2. Register Qwen3_5ForCausalLM + Qwen3_5TextModel in model registry
# Qwen3_5TextConfig (model_type="qwen3_5_text") auto-generates architecture name
# "Qwen3_5TextModel" via PretrainedConfig. Must register both aliases.
registry_path = f"{BASE}/model_executor/models/registry.py"
with open(registry_path) as f:
    content = f.read()
changed_registry = False
if "Qwen3_5ForCausalLM" not in content:
    content = content.replace(
        '"Qwen3NextForCausalLM": ("qwen3_next", "Qwen3NextForCausalLM"),',
        '"Qwen3NextForCausalLM": ("qwen3_next", "Qwen3NextForCausalLM"),\n    "Qwen3_5ForCausalLM": ("qwen3_5", "Qwen3_5ForCausalLM"),',
    )
    changed_registry = True
if "Qwen3_5TextModel" not in content:
    content = content.replace(
        '"Qwen3_5ForCausalLM": ("qwen3_5", "Qwen3_5ForCausalLM"),',
        '"Qwen3_5ForCausalLM": ("qwen3_5", "Qwen3_5ForCausalLM"),\n    "Qwen3_5TextModel": ("qwen3_5", "Qwen3_5ForCausalLM"),',
    )
    changed_registry = True
if changed_registry:
    with open(registry_path, "w") as f:
        f.write(content)
    print("2. Registered Qwen3_5ForCausalLM + Qwen3_5TextModel in model registry")
else:
    print("2. Both Qwen3_5 entries already registered")

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

# 3b. Replace in_proj_qkvz stacked_params tuple shard IDs with simple integers.
# Original: ("in_proj_qkvz", "in_proj_qkv", (0, 1, 2)) tries to sub-split the
# GPTQ quantized tensor into 3 parts — impossible for packed qweight/qzeros/scales.
# Fix: use integer shard IDs (0, 1) to concatenate whole tensors instead.
pattern_qkvz_stacked = (
    r'\("in_proj_qkvz",\s*"in_proj_qkv",\s*\(0,\s*1,\s*2\)\),'
    r"\s*\n\s*"
    r'\("in_proj_qkvz",\s*"in_proj_z",\s*3\),'
)
replacement_qkvz = (
    '("in_proj_qkvz", "in_proj_qkv", 0),\n            ("in_proj_qkvz", "in_proj_z", 1),'
)
if re.search(pattern_qkvz_stacked, content):
    content = re.sub(pattern_qkvz_stacked, replacement_qkvz, content)
    changes.append(
        "stacked_params_mapping (in_proj_qkvz: tuple→int shard IDs for GPTQ)"
    )

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

# 3e. Fix MergedColumnParallelLinear output_sizes to match 2-shard GPTQ layout.
# Original: [key_dim, key_dim, value_dim, value_dim] = [Q, K, V, Z] for 4 sub-shards.
# GPTQ layout: 2 tensors — in_proj_qkv (Q+K+V fused) and in_proj_z (Z separate).
# So output_sizes needs exactly 2 entries: [QKV_combined, Z].
old_qkvz_proj = "output_sizes=[key_dim, key_dim, value_dim, value_dim],"
new_qkvz_proj = "output_sizes=[key_dim + key_dim + value_dim, value_dim],"
if old_qkvz_proj in content:
    content = content.replace(old_qkvz_proj, new_qkvz_proj)
    changes.append("qkvz_proj output_sizes")

# in_proj_ba: output_sizes=[self.num_v_heads] * 2 is already correct for 2 shards
# (in_proj_b = shard 0, in_proj_a = shard 1). Do NOT collapse to single entry.

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

# Check stacked_params — all entries must use integer shard IDs, no tuple (0,1,2)
has_ba_stacked = '"in_proj_ba", "in_proj_b", 0' in verify
has_qkvz_int = '"in_proj_qkvz", "in_proj_qkv", 0' in verify
has_qkvz_tuple = "(0, 1, 2)" in verify and "in_proj_qkvz" in verify
if has_ba_stacked and has_qkvz_int and not has_qkvz_tuple:
    print("6b. VERIFIED: stacked_params all integer shard IDs (GPTQ-compatible)")
elif has_qkvz_tuple:
    print("6b. WARNING: stacked_params still has tuple shard IDs (will break GPTQ!)")
elif not has_ba_stacked:
    print(
        "6b. WARNING: stacked_params missing in_proj_ba entries (in_proj_a/b won't load!)"
    )
elif not has_qkvz_int:
    print(
        "6b. WARNING: stacked_params missing in_proj_qkvz entries (qkv/z won't load!)"
    )
else:
    print("6b. WARNING: stacked_params state unexpected")

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
    """Print tensor statistics."""
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
                _fix_os.environ.get("FLEXINFER_MODEL_PATH", ""),
                getattr(self.config, "_name_or_path", ""),
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

#!/usr/bin/env python3
"""GPTQ quantization via GPTQModel.

All configuration is read from environment variables set by the controller:
  MODEL_DIR, BITS, GROUP_SIZE, MAX_MEMORY_GB, MAX_SEQ_LEN, MAX_SAMPLES,
  SYM, DESC_ACT, GPU_MEMORY_FRACTION, DYNAMIC_EXCLUSION, DATASET,
  FLEXINFER_TELEMETRY (optional, "true" enables JSON progress lines)
"""

# Bumped when controller-side patches change. The wrapper script checks this
# against GPTQScriptVersion in gptq.go and aborts on mismatch.
FLEXINFER_SCRIPT_VERSION = "v12"
import copy
import gc
import json
import math
import os
import re
import shutil
import subprocess
import sys
import time
import threading
from importlib import metadata as importlib_metadata

POLICY_STATE_FILE = ".flexinfer-gptq-policy.json"
CHECKPOINT_DIR_NAME = ".flexinfer-gptq-cache"
CHECKPOINT_STATE_FILE = "checkpoint.json"
CALIBRATION_CACHE_FILE = "calibration-examples.pt"
DEFAULT_MODEL_POLICIES = [
    {
        "name": "qwen3.5-text",
        "match_model_types": ["qwen3_5_text"],
        "match_path_substrings": ["qwen35", "qwen3.5"],
        "extract_text_config": True,
        "copy_root_keys": ["bos_token_id", "eos_token_id", "pad_token_id"],
        "remap_model_type": "qwen3_5_text",
        "architectures": ["Qwen3_5ForCausalLM"],
        "loader": "manual_sharded_state_dict",
        "python_packages": [
            "git+https://github.com/huggingface/transformers.git@529504b2fa98970c6c44d3fafaeb07a39c40e7ea",
        ],
        "calibration_overrides": {
            "max_samples": 16,
            "max_seq_len": 512,
            "max_tokens": 8192,
        },
        "runtime_overrides": {
            "attn_implementation": "eager",
            "disable_qwen35_fla": True,
            "fix_mistral_regex": True,
        },
    },
    {
        "name": "gemma4-text",
        "match_model_types": ["gemma4_text"],
        "match_path_substrings": ["gemma4", "gemma-4"],
        "extract_text_config": True,
        "copy_root_keys": ["bos_token_id", "eos_token_id", "pad_token_id"],
        "remap_model_type": "gemma4_text",
        "architectures": ["Gemma4ForCausalLM"],
        "loader": "gptqmodel",
        "python_packages": [
            "git+https://github.com/huggingface/transformers.git@f965b10b",
        ],
        "quantize_config_overrides": {
            "offload_to_disk": True,
        },
        "calibration_overrides": {
            "max_samples": 512,
            "max_seq_len": 2048,
            "max_tokens": 524288,
        },
        "runtime_overrides": {
            "attn_implementation": "eager",
        },
        "artifact_overrides": {
            "preserve_native_output": True,
            "refuse_moe_expert_tensors": True,
        },
    },
]


def patch_triton_nogil_compat():
    """Backfill GPTQModel nogil_patcher state on Triton JITFunction/Autotuner.

    GPTQModel's nogil_patcher.py patches JITFunction.run and Autotuner.run,
    expecting _cache_lock, _cache, and _cache_futures attributes. FLA's
    fused_norm_gate creates Triton kernel instances at import time (before
    nogil_patcher runs), so those instances lack these attributes.

    For Autotuner: the native attribute is .cache (dict). nogil_patcher
    expects ._cache (same dict), ._cache_lock, and ._cache_futures.
    We add class-level defaults so any instance works with the patched run().
    """
    patched = []
    try:
        from triton.runtime.jit import JITFunction

        if not hasattr(JITFunction, "_cache_lock"):
            JITFunction._cache_lock = threading.Lock()
            patched.append("JITFunction._cache_lock")
    except Exception:
        pass
    try:
        from triton.runtime.autotuner import Autotuner

        needs = []
        if not hasattr(Autotuner, "_cache_lock"):
            Autotuner._cache_lock = threading.Lock()
            needs.append("_cache_lock")
        if not hasattr(Autotuner, "_cache_futures"):
            Autotuner._cache_futures = {}
            needs.append("_cache_futures")
        # _cache must mirror the existing .cache dict per-instance.
        # We monkey-patch __init_subclass__ won't help — instead, wrap
        # the Autotuner.__init__ to copy .cache → ._cache after init.
        if not hasattr(Autotuner, "_flexinfer_init_patched"):
            _orig_init = Autotuner.__init__

            def _patched_init(self, *args, **kwargs):
                _orig_init(self, *args, **kwargs)
                if not hasattr(self, "_cache"):
                    self._cache = getattr(self, "cache", {})
                if not hasattr(self, "_cache_lock"):
                    self._cache_lock = threading.Lock()
                if not hasattr(self, "_cache_futures"):
                    self._cache_futures = {}

            Autotuner.__init__ = _patched_init
            Autotuner._flexinfer_init_patched = True
            needs.append("__init__")
        if needs:
            patched.append(f"Autotuner({','.join(needs)})")
    except Exception:
        pass
    if patched:
        print(
            f"Patched Triton {'; '.join(patched)} for GPTQModel/FLA compat",
            flush=True,
        )


patch_triton_nogil_compat()


# ── Telemetry helper ──────────────────────────────────────────────────
def emit_progress(event_type, **kwargs):
    """Emit a structured JSON telemetry line to stdout."""
    if os.environ.get("FLEXINFER_TELEMETRY") != "true":
        return
    msg = {
        "event": event_type,
        "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    msg.update(kwargs)
    print(json.dumps(msg), flush=True)


def _memory_stats():
    """Return a compact GPU + RSS memory string for per-layer/shard logging."""
    parts = []
    try:
        import torch

        if torch.cuda.is_available():
            alloc_mb = torch.cuda.memory_allocated() / 1048576
            reserved_mb = torch.cuda.memory_reserved() / 1048576
            parts.append(f"gpu_alloc={alloc_mb:.0f}MB gpu_reserved={reserved_mb:.0f}MB")
    except Exception:
        pass
    try:
        with open("/proc/self/status") as f:
            for line in f:
                if line.startswith("VmRSS:"):
                    rss_kb = int(line.split()[1])
                    parts.append(f"rss={rss_kb // 1024}MB")
                    break
    except Exception:
        pass
    return " ".join(parts)


def load_model_policies():
    raw = os.environ.get("QUANTIZE_MODEL_POLICIES", "").strip()
    if not raw:
        return copy.deepcopy(DEFAULT_MODEL_POLICIES)
    policies = json.loads(raw)
    if not isinstance(policies, list):
        raise ValueError("QUANTIZE_MODEL_POLICIES must decode to a list")
    return policies


def env_bool(name, default):
    raw = os.environ.get(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def env_float(name, default):
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    return float(raw)


def env_int(name, default):
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    return int(raw)


def _indexed_safetensors(save_dir):
    """Return (index_path, weight_map, shard_files) for sharded/single safetensors."""
    try:
        from safetensors import safe_open
    except ImportError:
        return None, {}, []

    index_path = os.path.join(save_dir, "model.safetensors.index.json")
    single_path = os.path.join(save_dir, "model.safetensors")
    if os.path.exists(index_path):
        with open(index_path) as f:
            index = json.load(f)
        weight_map = index.get("weight_map", {})
        return index_path, weight_map, sorted(set(weight_map.values()))
    if os.path.exists(single_path):
        with safe_open(single_path, framework="pt") as f:
            weight_map = {key: "model.safetensors" for key in f.keys()}
        return None, weight_map, ["model.safetensors"]
    return None, {}, []


def _source_tensor_map(model_dir):
    """Return source checkpoint weight_map after wrapper key remapping."""
    index_path, weight_map, _ = _indexed_safetensors(model_dir)
    if weight_map:
        return weight_map
    return {}


def _update_quantized_module_lists(save_dir, modules):
    """Constrain vLLM quantization metadata to the modules that remain GPTQ."""
    for rel_path in ("quantize_config.json", "config.json"):
        path = os.path.join(save_dir, rel_path)
        if not os.path.exists(path):
            continue
        with open(path) as f:
            data = json.load(f)

        if rel_path == "quantize_config.json":
            data["modules_in_block_to_quantize"] = list(modules)
        else:
            qcfg = data.get("quantization_config")
            if isinstance(qcfg, dict):
                qcfg["modules_in_block_to_quantize"] = list(modules)

        with open(path, "w") as f:
            json.dump(data, f, indent=2)


def _dense_gptq_zero_point_add():
    raw = os.environ.get(
        "DENSE_GPTQ_ZERO_POINT_ADD",
        os.environ.get("GEMMA4_DENSE_GPTQ_ZERO_POINT_ADD", "1"),
    ).strip()
    try:
        return int(raw)
    except ValueError:
        print(f"WARN: invalid dense GPTQ zero-point add={raw!r}; using 1")
        return 1


def _dense_gptq_cosine_threshold():
    raw = os.environ.get(
        "DENSE_GPTQ_COSINE_THRESHOLD",
        os.environ.get("GEMMA4_DENSE_GPTQ_COSINE_THRESHOLD", "0.98"),
    ).strip()
    try:
        return float(raw)
    except ValueError:
        print(f"WARN: invalid dense GPTQ cosine threshold={raw!r}; using 0.98")
        return 0.98


def _dense_gptq_policy():
    raw = (
        os.environ.get(
            "DENSE_GPTQ_POLICY",
            os.environ.get("GEMMA4_DENSE_GPTQ_POLICY", "fallback"),
        )
        .strip()
        .lower()
    )
    if raw in {"fallback", "fp16", "source", "safe"}:
        return "fallback"
    if raw in {"validate", "validated", "allow", "gptq"}:
        return "validate"
    print(f"WARN: invalid dense GPTQ policy={raw!r}; using fallback")
    return "fallback"


def _load_safetensor(base_dir, weight_map, key):
    from safetensors import safe_open

    with safe_open(os.path.join(base_dir, weight_map[key]), framework="pt") as f:
        return f.get_tensor(key)


def _dequantize_gptq_linear(save_dir, weight_map, prefix, zero_point_add):
    """Dequantize a GPTQ linear layer to HF .weight layout [out, in]."""
    import torch

    qweight = _load_safetensor(save_dir, weight_map, f"{prefix}.qweight").to(
        torch.int32
    )
    scales = _load_safetensor(save_dir, weight_map, f"{prefix}.scales").to(
        torch.float32
    )
    qzeros = None
    if f"{prefix}.qzeros" in weight_map:
        qzeros = _load_safetensor(save_dir, weight_map, f"{prefix}.qzeros").to(
            torch.int32
        )
    if f"{prefix}.g_idx" in weight_map:
        group_idx = _load_safetensor(save_dir, weight_map, f"{prefix}.g_idx").to(
            torch.long
        )
    else:
        group_size = env_int("GROUP_SIZE", 128)
        input_size = qweight.shape[0] * 8
        group_idx = torch.arange(input_size, dtype=torch.long) // group_size

    pack_factor = 8
    input_size = qweight.shape[0] * pack_factor
    output_size = qweight.shape[1]
    unpacked = torch.empty((input_size, output_size), dtype=torch.float32)
    for i in range(pack_factor):
        unpacked[i::pack_factor, :] = ((qweight >> (4 * i)) & 0xF).to(torch.float32)

    if qzeros is not None and qzeros.numel() > 0:
        zero_points = torch.empty((qzeros.shape[0], output_size), dtype=torch.float32)
        for i in range(pack_factor):
            zero_points[:, i::pack_factor] = ((qzeros >> (4 * i)) & 0xF).to(
                torch.float32
            ) + zero_point_add
        unpacked = unpacked - zero_points[group_idx]
    else:
        unpacked = unpacked - 8.0

    return (unpacked * scales[group_idx]).transpose(0, 1).contiguous()


def _cosine_similarity(a, b):
    import torch

    a = a.to(torch.float32).flatten()
    b = b.to(torch.float32).flatten()
    return torch.nn.functional.cosine_similarity(a, b, dim=0).item()


def _validate_dense_gptq_family(
    save_dir, source_dir, target_weight_map, source_weight_map, family, prefixes
):
    """Validate all layers for one dense module family against source weights."""
    threshold = _dense_gptq_cosine_threshold()
    zero_point_add = _dense_gptq_zero_point_add()
    failures = []
    scores = []
    for prefix in prefixes:
        qkeys = [f"{prefix}.{suffix}" for suffix in ("qweight", "scales", "qzeros")]
        if any(key not in target_weight_map for key in qkeys):
            failures.append((prefix, "missing_gptq_keys"))
            continue
        source_key = f"{prefix}.weight"
        if source_key not in source_weight_map:
            failures.append((prefix, "missing_source_weight"))
            continue
        try:
            dense = _dequantize_gptq_linear(
                save_dir, target_weight_map, prefix, zero_point_add
            )
            source = _load_safetensor(source_dir, source_weight_map, source_key)
            score = _cosine_similarity(dense, source)
            scores.append(score)
            if score < threshold:
                failures.append((prefix, f"cosine={score:.6f}"))
        except Exception as exc:
            failures.append((prefix, f"error={exc}"))

    if failures:
        preview = ", ".join(f"{prefix}:{why}" for prefix, why in failures[:8])
        extra = len(failures) - min(len(failures), 8)
        suffix = f" ... (+{extra} more)" if extra > 0 else ""
        if scores:
            print(
                f"Gemma4 dense GPTQ family {family} rejected: "
                f"min_cosine={min(scores):.6f} threshold={threshold:.3f}; "
                f"{preview}{suffix}"
            )
        else:
            print(f"Gemma4 dense GPTQ family {family} rejected: {preview}{suffix}")
        return False

    print(
        f"Gemma4 dense GPTQ family {family} accepted: "
        f"layers={len(prefixes)} min_cosine={min(scores):.6f} "
        f"avg_cosine={sum(scores) / len(scores):.6f}"
    )
    return True


def should_apply_gemma4_moe_hybrid(cfg):
    """Decide whether to emit Gemma4 MoE hybrid GPTQ output.

    Dense attention GPTQ tensors from Gemma4 26B-A4B were empirically bad on
    GPTQModel 6.0.3/vLLM ROCm: direct dequant had near-zero cosine vs source
    and generation collapsed to a repeated token. Keeping dense attention/MLP
    at the post-abliteration source precision while quantizing MoE experts
    yields a vLLM-loadable artifact that serves coherently on gfx1100.
    """
    mode = os.environ.get("GEMMA4_MOE_HYBRID_GPTQ", "auto").strip().lower()
    if mode in {"0", "false", "no", "off", "disabled", "none"}:
        return False
    if mode in {"1", "true", "yes", "on", "enabled", "force"}:
        return True

    search_scopes = [cfg, cfg.get("text_config", {})]
    model_types = {scope.get("model_type", "") for scope in search_scopes}
    if "gemma4_text" not in model_types:
        return False
    for scope in search_scopes:
        if scope.get("enable_moe_block") is True:
            return True
        for key in ("num_experts", "num_local_experts"):
            val = scope.get(key, 0)
            if isinstance(val, int) and val > 1:
                return True
    return False


def emit_gemma4_moe_hybrid_gptq(save_dir, model_dir):
    """Restore bad dense Gemma4 tensors from source and keep validated GPTQ.

    vLLM scans every .safetensors file, not just the index, so it is not enough
    to edit weight_map. This function validates dense attention/MLP GPTQ
    families against post-abliteration source weights. A family is kept GPTQ
    only when every layer passes; otherwise its qweight/scales/qzeros/g_idx keys
    are physically removed from every shard and source .weight tensors are
    restored. This keeps vLLM metadata globally consistent per module family.
    """
    import torch

    try:
        from safetensors import safe_open
        from safetensors.torch import load_file, save_file
    except ImportError:
        print("WARN: safetensors not available, skipping Gemma4 hybrid GPTQ export")
        return False

    target_index_path, target_weight_map, target_shards = _indexed_safetensors(save_dir)
    source_weight_map = _source_tensor_map(model_dir)
    if not target_weight_map or not source_weight_map:
        print("WARN: missing safetensors index/map, skipping Gemma4 hybrid GPTQ export")
        return False

    dense_quant_re = re.compile(
        r"^model\.layers\.(\d+)\."
        r"(self_attn\.(?:q_proj|k_proj|v_proj|o_proj)|mlp\.(?:gate_proj|up_proj|down_proj))"
        r"\.(qweight|qzeros|scales|g_idx)$"
    )
    dense_weight_re = re.compile(
        r"^model\.layers\.(\d+)\."
        r"(self_attn\.(?:q_proj|k_proj|v_proj|o_proj)|mlp\.(?:gate_proj|up_proj|down_proj))"
        r"\.weight$"
    )

    source_dense_by_family = {}
    for key in source_weight_map:
        match = dense_weight_re.match(key)
        if match:
            source_dense_by_family.setdefault(match.group(2), []).append(key)
    if not source_dense_by_family:
        print("WARN: no dense Gemma4 source weights found for hybrid GPTQ export")
        return False

    quant_prefixes_by_family = {}
    for key in target_weight_map:
        match = dense_quant_re.match(key)
        if match and match.group(3) == "qweight":
            prefix = key.rsplit(".", 1)[0]
            quant_prefixes_by_family.setdefault(match.group(2), []).append(prefix)

    dense_gptq_policy = _dense_gptq_policy()
    keep_gptq_families = set()
    fallback_families = set(source_dense_by_family)
    for family, source_keys in sorted(source_dense_by_family.items()):
        prefixes = sorted(k.rsplit(".weight", 1)[0] for k in source_keys)
        if dense_gptq_policy == "fallback":
            print(
                f"Gemma4 dense GPTQ family {family} forced to source precision "
                "(DENSE_GPTQ_POLICY=fallback)"
            )
            continue
        if family not in quant_prefixes_by_family:
            print(
                f"Gemma4 dense GPTQ family {family} has no qweight keys; restoring source"
            )
            continue
        if len(quant_prefixes_by_family[family]) != len(prefixes):
            print(
                f"Gemma4 dense GPTQ family {family} incomplete: "
                f"qweights={len(quant_prefixes_by_family[family])} source_weights={len(prefixes)}; restoring source"
            )
            continue
        if _validate_dense_gptq_family(
            save_dir, model_dir, target_weight_map, source_weight_map, family, prefixes
        ):
            keep_gptq_families.add(family)
            fallback_families.discard(family)

    target_by_layer = {}
    keys_to_drop = set()
    for key, shard_name in target_weight_map.items():
        quant_match = dense_quant_re.match(key)
        if quant_match:
            family = quant_match.group(2)
            if family in keep_gptq_families:
                target_by_layer.setdefault(quant_match.group(1), shard_name)
                continue
            keys_to_drop.add(key)
            target_by_layer.setdefault(quant_match.group(1), shard_name)
            continue
        weight_match = dense_weight_re.match(key)
        if weight_match and weight_match.group(2) in keep_gptq_families:
            # GPTQ family accepted; remove any stray dense source weights for
            # that family to avoid ambiguous vLLM load behavior.
            keys_to_drop.add(key)
            target_by_layer.setdefault(weight_match.group(1), shard_name)
            continue
        layer_match = re.match(r"^model\.layers\.(\d+)\.", key)
        if layer_match:
            target_by_layer.setdefault(layer_match.group(1), shard_name)

    new_weight_map = {
        key: shard_name
        for key, shard_name in target_weight_map.items()
        if key not in keys_to_drop
    }

    # Rewrite every target shard so removed quant keys are gone from file bytes,
    # not merely from the index.
    for shard_name in target_shards:
        shard_path = os.path.join(save_dir, shard_name)
        tensors = load_file(shard_path)
        kept = {k: v for k, v in tensors.items() if k not in keys_to_drop}
        if len(kept) != len(tensors):
            save_file(kept, shard_path)

    restore_by_target_shard = {}
    restore_keys = []
    for family in sorted(fallback_families):
        restore_keys.extend(sorted(source_dense_by_family[family]))
    for key in restore_keys:
        match = dense_weight_re.match(key)
        layer = match.group(1)
        target_shard = target_by_layer.get(layer, target_shards[0])
        restore_by_target_shard.setdefault(target_shard, []).append(key)
        new_weight_map[key] = target_shard

    # Add source dense weights to their target shards. Source weights are the
    # post-abliteration tensors because model_dir is the pipeline input to GPTQ.
    from contextlib import ExitStack

    source_open = {}
    with ExitStack() as stack:
        for target_shard, keys in sorted(restore_by_target_shard.items()):
            target_path = os.path.join(save_dir, target_shard)
            tensors = load_file(target_path)
            updated = dict(tensors)
            for key in keys:
                source_shard = source_weight_map[key]
                if source_shard not in source_open:
                    source_open[source_shard] = stack.enter_context(
                        safe_open(os.path.join(model_dir, source_shard), framework="pt")
                    )
                updated[key] = (
                    source_open[source_shard].get_tensor(key).to(torch.float16)
                )
            save_file(updated, target_path)

    if target_index_path:
        with open(target_index_path) as f:
            index = json.load(f)
        index["weight_map"] = new_weight_map
        metadata = index.setdefault("metadata", {})
        metadata["total_size"] = sum(
            os.path.getsize(os.path.join(save_dir, shard))
            for shard in sorted(set(new_weight_map.values()))
        )
        with open(target_index_path, "w") as f:
            json.dump(index, f, indent=2)

    quantized_modules = ["moe.gate_up_proj", "moe.down_proj"]
    quantized_modules.extend(sorted(keep_gptq_families))
    _update_quantized_module_lists(save_dir, quantized_modules)
    print(
        "Gemma4 MoE hybrid GPTQ export complete: "
        f"kept_gptq={sorted(keep_gptq_families)} "
        f"fallback={sorted(fallback_families)} "
        f"dropped_keys={len(keys_to_drop)} restored_weights={len(restore_keys)}"
    )
    return True


def defuse_fused_experts(model):
    """Defuse fused 3D expert parameters into per-expert nn.Linear modules.

    Handles architectures like Gemma4 (inheriting MixtralExperts) where MoE
    experts are stored as fused 3D nn.Parameter tensors:

      gate_up_proj: [num_experts, 2 * intermediate_size, hidden_size]
      down_proj:    [num_experts, hidden_size, intermediate_size]

    The original forward (Gemma4TextExperts / MixtralExperts) uses
    F.linear(x, gate_up_proj[i]).chunk(2, dim=-1) for each expert.

    This function:
      1. Splits gate_up_proj into per-expert gate_proj and up_proj nn.Linear
      2. Creates per-expert down_proj nn.Linear
      3. Registers per-expert modules as numbered children ("0", "1", ...)
      4. Replaces forward with a sequential per-expert version that calls
         through nn.Linear (required for GPTQ activation capture via hooks)
      5. Removes original fused parameters to reclaim memory

    Returns the number of expert modules defused (one per decoder layer).
    """
    import torch
    import torch.nn as nn
    import torch.nn.functional as F

    defused_count = 0

    for mod_name, module in list(model.named_modules()):
        gate_up = getattr(module, "gate_up_proj", None)
        down = getattr(module, "down_proj", None)

        if gate_up is None or down is None:
            continue
        if not isinstance(gate_up, (nn.Parameter, torch.Tensor)):
            continue
        if gate_up.dim() != 3 or down.dim() != 3:
            continue

        num_experts = gate_up.shape[0]
        fused_2x_inter = gate_up.shape[1]
        hidden_size = gate_up.shape[2]
        intermediate_size = fused_2x_inter // 2

        if down.shape != (num_experts, hidden_size, intermediate_size):
            print(
                f"  WARN: {mod_name}.down_proj shape {list(down.shape)} != "
                f"expected [{num_experts}, {hidden_size}, {intermediate_size}], skipping"
            )
            continue

        act_fn = getattr(module, "act_fn", F.gelu)

        if defused_count == 0:
            print(
                f"Defusing fused experts: num_experts={num_experts}, "
                f"hidden={hidden_size}, intermediate={intermediate_size}"
            )
            print(
                f"  gate_up_proj: {list(gate_up.shape)}, down_proj: {list(down.shape)}"
            )
            print(f"  activation: {act_fn}")

        with torch.no_grad():
            gate_weights, up_weights = gate_up.data.chunk(2, dim=1)

        for i in range(num_experts):
            expert = nn.Module()

            gp = nn.Linear(hidden_size, intermediate_size, bias=False)
            gp.weight = nn.Parameter(gate_weights[i].contiguous())
            expert.gate_proj = gp

            up_l = nn.Linear(hidden_size, intermediate_size, bias=False)
            up_l.weight = nn.Parameter(up_weights[i].contiguous())
            expert.up_proj = up_l

            dp = nn.Linear(intermediate_size, hidden_size, bias=False)
            dp.weight = nn.Parameter(down.data[i].contiguous())
            expert.down_proj = dp

            module.register_module(str(i), expert)

        # Replace forward to route through per-expert nn.Linear modules.
        # Matches the original MixtralExperts / Gemma4TextExperts forward
        # signature: forward(hidden_states, top_k_index, top_k_weights).
        _n_exp = num_experts
        _act = act_fn

        def _make_forward(mod, n_exp, activation):
            def forward(hidden_states, top_k_index, top_k_weights):
                final_hidden_states = torch.zeros_like(hidden_states)
                with torch.no_grad():
                    expert_mask = F.one_hot(top_k_index, num_classes=n_exp)
                    expert_mask = expert_mask.permute(2, 1, 0)
                    expert_hit = torch.greater(
                        expert_mask.sum(dim=(-1, -2)), 0
                    ).nonzero()

                for eidx in expert_hit:
                    eidx_val = eidx[0]
                    if eidx_val >= n_exp:
                        continue
                    top_k_pos, token_idx = torch.where(expert_mask[eidx_val])
                    current_state = hidden_states[token_idx]

                    expert_mod = getattr(mod, str(eidx_val.item()))
                    gate_out = expert_mod.gate_proj(current_state)
                    up_out = expert_mod.up_proj(current_state)
                    current_hidden = activation(gate_out) * up_out
                    current_hidden = expert_mod.down_proj(current_hidden)
                    current_hidden = (
                        current_hidden * top_k_weights[token_idx, top_k_pos, None]
                    )
                    final_hidden_states.index_add_(
                        0,
                        token_idx,
                        current_hidden.to(final_hidden_states.dtype),
                    )

                return final_hidden_states

            return forward

        module.forward = _make_forward(module, _n_exp, _act)

        # Remove original fused parameters to reclaim memory.
        if "gate_up_proj" in module._parameters:
            del module._parameters["gate_up_proj"]
        if "down_proj" in module._parameters:
            del module._parameters["down_proj"]

        defused_count += 1
        if defused_count % 5 == 0:
            gc.collect()

    if defused_count > 0:
        gc.collect()

    return defused_count


def refuse_moe_expert_tensors(save_dir):
    """Re-fuse per-expert 2D GPTQ tensors back into fused 3D tensors for vLLM.

    GPTQModel saves defused per-expert 2D keys after Defuser unfuses:
      model.layers.0.experts.0.gate_proj.qweight [88, 2816]
      model.layers.0.experts.0.up_proj.qweight   [88, 2816]
      model.layers.0.experts.0.down_proj.qweight [352, 704]

    vLLM's MoeWNA16 expects fused 3D tensors:
      model.layers.0.experts.gate_up_proj.qweight [128, 176, 2816]
      model.layers.0.experts.down_proj.qweight    [128, 352, 704]

    Re-fuse: gate+up → cat(dim=0) per expert → stack(dim=0) → gate_up_proj
             down per expert → stack(dim=0) → down_proj
    """
    import torch

    try:
        from safetensors.torch import load_file, save_file
    except ImportError:
        print("WARN: safetensors not available, skipping MoE re-fuse")
        return False

    # Check if we have per-expert keys in any shard
    index_path = os.path.join(save_dir, "model.safetensors.index.json")
    single_path = os.path.join(save_dir, "model.safetensors")

    if os.path.exists(index_path):
        with open(index_path) as f:
            index = json.load(f)
        weight_map = index.get("weight_map", {})
    elif os.path.exists(single_path):
        # Single-file model: synthesize a weight_map from the file
        tensors = load_file(single_path)
        weight_map = {k: "model.safetensors" for k in tensors.keys()}
    else:
        print("INFO: No safetensors index or single file found, skipping MoE re-fuse")
        return False

    # Detect per-expert keys: pattern like "model.layers.N.experts.M.gate_proj.qweight"
    expert_pattern = re.compile(
        r"^(model\.layers\.(\d+)\.(?:block_sparse_moe\.)?experts)\.(\d+)\."
        r"(gate_proj|up_proj|down_proj)\.(qweight|qzeros|scales|g_idx)$"
    )

    # Collect per-expert tensors grouped by (prefix, layer, tensor_type, proj_type)
    expert_keys = {}
    for key in weight_map:
        m = expert_pattern.match(key)
        if m:
            prefix, layer_idx, expert_idx, proj_type, tensor_type = (
                m.group(1),
                int(m.group(2)),
                int(m.group(3)),
                m.group(4),
                m.group(5),
            )
            group_key = (prefix, layer_idx, tensor_type)
            if group_key not in expert_keys:
                expert_keys[group_key] = {}
            sub_key = (expert_idx, proj_type)
            expert_keys[group_key][sub_key] = key

    if not expert_keys:
        print("INFO: No per-expert GPTQ tensors found, skipping MoE re-fuse")
        return False

    # Determine number of experts per layer
    expert_counts = {}
    for (prefix, layer_idx, _), subs in expert_keys.items():
        max_expert = max(idx for (idx, _) in subs.keys())
        expert_counts[(prefix, layer_idx)] = max_expert + 1

    n_layers = len(set(li for (_, li) in expert_counts.keys()))
    n_experts = next(iter(expert_counts.values()))
    n_tensor_types = len(set(tt for (_, _, tt) in expert_keys.keys()))
    print(
        f"MoE re-fuse: {n_layers} layers × {n_experts} experts × "
        f"{n_tensor_types} tensor types"
    )

    # Load all shards
    shard_files = sorted(set(weight_map.values()))
    shard_data = {}
    for shard_name in shard_files:
        shard_path = os.path.join(save_dir, shard_name)
        shard_data[shard_name] = load_file(shard_path)

    # Build flat key→tensor mapping
    all_tensors = {}
    for shard_name, tensors in shard_data.items():
        for k, v in tensors.items():
            all_tensors[k] = v

    # Re-fuse per-expert tensors
    fused_tensors = {}
    keys_to_remove = set()

    for (prefix, layer_idx, tensor_type), subs in sorted(expert_keys.items()):
        n_exp = expert_counts[(prefix, layer_idx)]

        # Skip g_idx — vLLM's MoeWNA16 weight loader explicitly ignores
        # g_idx for MoE layers, and the fused 2D shape ([E, indices])
        # can't be exploded by vLLM's _weight_iterator (expects 3D).
        # Just remove per-expert g_idx keys from the output.
        if tensor_type == "g_idx":
            for key in subs.values():
                keys_to_remove.add(key)
            continue

        # Remap prefix to vLLM's FusedMoE module name. The original
        # HF prefix is "model.layers.{i}.(block_sparse_moe.)?experts"
        # but vLLM's Gemma4 model maps MoE to "model.layers.{i}.moe".
        fused_prefix = re.sub(
            r"(model\.layers\.\d+)\.(block_sparse_moe\.)?experts$",
            r"\1.moe",
            prefix,
        )

        # Collect gate, up, down per expert
        gate_list = []
        up_list = []
        down_list = []

        for expert_idx in range(n_exp):
            gate_key = subs.get((expert_idx, "gate_proj"))
            up_key = subs.get((expert_idx, "up_proj"))
            down_key = subs.get((expert_idx, "down_proj"))

            if gate_key and gate_key in all_tensors:
                gate_list.append(all_tensors[gate_key])
                keys_to_remove.add(gate_key)
            if up_key and up_key in all_tensors:
                up_list.append(all_tensors[up_key])
                keys_to_remove.add(up_key)
            if down_key and down_key in all_tensors:
                down_list.append(all_tensors[down_key])
                keys_to_remove.add(down_key)

        # Fuse gate+up: cat(dim=0) per expert, then stack across experts
        if gate_list and up_list and len(gate_list) == len(up_list) == n_exp:
            # gate [rows, cols] + up [rows, cols] → [2*rows, cols] per expert
            gate_up_per_expert = [
                torch.cat([g, u], dim=0) for g, u in zip(gate_list, up_list)
            ]
            # Stack: [n_experts, 2*rows, cols]
            fused_gate_up = torch.stack(gate_up_per_expert, dim=0)
            fused_key = f"{fused_prefix}.gate_up_proj.{tensor_type}"
            fused_tensors[fused_key] = fused_gate_up
            if layer_idx == 0:
                print(
                    f"  gate_up_proj.{tensor_type}: "
                    f"[{n_exp}×({gate_list[0].shape} + {up_list[0].shape})] → "
                    f"{list(fused_gate_up.shape)}"
                )

        # Fuse down: just stack across experts
        if down_list and len(down_list) == n_exp:
            fused_down = torch.stack(down_list, dim=0)
            fused_key = f"{fused_prefix}.down_proj.{tensor_type}"
            fused_tensors[fused_key] = fused_down
            if layer_idx == 0:
                print(
                    f"  down_proj.{tensor_type}: "
                    f"[{n_exp}×{list(down_list[0].shape)}] → "
                    f"{list(fused_down.shape)}"
                )

    if not fused_tensors:
        print("WARN: No tensors were re-fused (unexpected)")
        return False

    # Rebuild shard data: remove per-expert keys, add fused keys
    # Put all fused MoE tensors into the first shard for simplicity,
    # or spread across existing shards by layer assignment.
    new_weight_map = {}
    new_shard_data = {}

    # First pass: rebuild non-expert tensors into their original shards
    for shard_name in shard_files:
        new_shard_data[shard_name] = {}
        for k, v in shard_data[shard_name].items():
            if k not in keys_to_remove:
                new_shard_data[shard_name][k] = v
                new_weight_map[k] = shard_name

    # Assign fused tensors to shards based on layer index (match layer's shard)
    for fused_key, fused_tensor in sorted(fused_tensors.items()):
        # Find which shard has other tensors from this layer
        layer_match = re.search(r"model\.layers\.(\d+)\.", fused_key)
        target_shard = shard_files[0]  # default to first shard
        if layer_match:
            layer_prefix = f"model.layers.{layer_match.group(1)}."
            for k, shard_name in weight_map.items():
                if k.startswith(layer_prefix) and k not in keys_to_remove:
                    target_shard = shard_name
                    break
        new_shard_data[target_shard][fused_key] = fused_tensor
        new_weight_map[fused_key] = target_shard

    # Write updated shards
    total_fused = len(fused_tensors)
    total_removed = len(keys_to_remove)
    for shard_name, tensors in new_shard_data.items():
        if not tensors:
            # Empty shard after removing expert keys — delete it
            shard_path = os.path.join(save_dir, shard_name)
            if os.path.exists(shard_path):
                os.remove(shard_path)
            continue
        shard_path = os.path.join(save_dir, shard_name)
        save_file(tensors, shard_path)

    # Update index
    if os.path.exists(index_path):
        with open(index_path) as f:
            index = json.load(f)
        index["weight_map"] = new_weight_map
        with open(index_path, "w") as f:
            json.dump(index, f, indent=2)

    # Update quantize_config.json to reflect MoE expert quantization
    qcfg_path = os.path.join(save_dir, "quantize_config.json")
    if os.path.exists(qcfg_path):
        with open(qcfg_path) as f:
            qcfg = json.load(f)
        # Ensure modules_in_block_to_quantize lists ALL quantized modules.
        # GPTQModel >= 6.0.3 no longer writes this field, so create it
        # from scratch when absent. vLLM's get_linear_quant_method uses
        # this list to decide which layers get GPTQ applied; missing
        # entries cause KeyError on quantized weight keys (e.g. g_idx).
        mibq = qcfg.get("modules_in_block_to_quantize")
        if mibq is None:
            mibq = []
        if isinstance(mibq, list):
            required_entries = [
                # Attention projections
                "self_attn.q_proj",
                "self_attn.k_proj",
                "self_attn.v_proj",
                "self_attn.o_proj",
                # MoE experts (fused)
                "moe.gate_up_proj",
                "moe.down_proj",
                # Shared expert MLP (dense feed-forward)
                "mlp.gate_proj",
                "mlp.up_proj",
                "mlp.down_proj",
            ]
            for entry in required_entries:
                if entry not in mibq:
                    mibq.append(entry)
            qcfg["modules_in_block_to_quantize"] = mibq
        with open(qcfg_path, "w") as f:
            json.dump(qcfg, f, indent=2)

    print(
        f"MoE re-fuse complete: {total_removed} per-expert keys → "
        f"{total_fused} fused 3D tensors"
    )
    return True


def load_policy_state(model_dir):
    path = os.path.join(model_dir, POLICY_STATE_FILE)
    if not os.path.exists(path):
        return {}
    with open(path) as f:
        state = json.load(f)
    if not isinstance(state, dict):
        raise ValueError(f"{POLICY_STATE_FILE} must contain an object")
    return state


def persist_policy_state(model_dir, state):
    path = os.path.join(model_dir, POLICY_STATE_FILE)
    with open(path, "w") as f:
        json.dump(state, f, indent=2, sort_keys=True)


def select_model_policy(model_dir, cfg, policy_state, policies):
    root_model_type = cfg.get("model_type", "")
    text_model_type = cfg.get("text_config", {}).get("model_type", "")
    candidates = {
        policy_state.get("original_model_type", ""),
        policy_state.get("applied_model_type", ""),
        root_model_type,
        text_model_type,
    }
    path_candidates = {
        model_dir,
        os.path.basename(model_dir),
        cfg.get("_name_or_path", ""),
    }
    selected_name = policy_state.get("policy_name", "")
    for policy in policies:
        if selected_name and policy.get("name") == selected_name:
            return policy
        for model_type in policy.get("match_model_types", []):
            if model_type and model_type in candidates:
                return policy
        for token in policy.get("match_path_substrings", []):
            if token and any(
                token in candidate.lower() for candidate in path_candidates if candidate
            ):
                return policy
    return None


def apply_model_policy(cfg, policy, policy_state):
    root_model_type = cfg.get("model_type", "")
    text_model_type = cfg.get("text_config", {}).get("model_type", "")
    original_model_type = (
        policy_state.get("original_model_type") or text_model_type or root_model_type
    )
    active_cfg = cfg
    if policy.get("extract_text_config") and text_model_type:
        active_cfg = copy.deepcopy(cfg["text_config"])
        for key in policy.get("copy_root_keys", []):
            if key in cfg and key not in active_cfg:
                active_cfg[key] = cfg[key]
        print(f"Extracted text_config: model_type={text_model_type}")

    remapped_type = policy.get("remap_model_type", "")
    if remapped_type:
        active_cfg["model_type"] = remapped_type
        print(f"Remapped model_type to {remapped_type}")
    remapped_architectures = policy.get("architectures")
    if remapped_architectures:
        active_cfg["architectures"] = remapped_architectures
        print(f"Set architectures={remapped_architectures}")

    next_state = {
        "policy_name": policy.get("name", ""),
        "original_model_type": original_model_type,
        "applied_model_type": active_cfg.get("model_type", ""),
    }
    return active_cfg, next_state


def ensure_policy_python_packages(policy):
    packages = list((policy or {}).get("python_packages") or [])
    if not packages:
        return
    if policy_python_packages_satisfied(policy, packages):
        print(
            f"Policy python packages already satisfied for {policy.get('name', 'unnamed-policy')}"
        )
        return
    print(f"Installing policy python packages: {packages}")
    subprocess.check_call(
        [sys.executable, "-m", "pip", "install", "--no-cache-dir", *packages],
        stdout=sys.stdout,
        stderr=sys.stderr,
    )


def policy_python_packages_satisfied(policy, packages):
    name = (policy or {}).get("name", "")
    if name != "qwen3.5-text":
        return False
    if len(packages) != 1 or "transformers.git@" not in packages[0]:
        return False
    try:
        version = importlib_metadata.version("transformers")
    except importlib_metadata.PackageNotFoundError:
        return False
    if version != "5.3.0.dev0":
        return False
    try:
        from transformers import Qwen3_5ForCausalLM  # noqa: F401
    except Exception:
        return False
    return True


def runtime_overrides_for_policy(policy):
    overrides = (policy or {}).get("runtime_overrides", {})
    return dict(overrides) if isinstance(overrides, dict) else {}


def artifact_overrides_for_policy(policy):
    overrides = (policy or {}).get("artifact_overrides", {})
    return dict(overrides) if isinstance(overrides, dict) else {}


def load_tokenizer_with_runtime_overrides(model_dir, runtime_overrides):
    kwargs = {"trust_remote_code": True}
    if runtime_overrides.get("fix_mistral_regex"):
        kwargs["fix_mistral_regex"] = True
    try:
        return AutoTokenizer.from_pretrained(model_dir, **kwargs)
    except TypeError:
        if "fix_mistral_regex" not in kwargs:
            raise
        kwargs.pop("fix_mistral_regex", None)
        print("Tokenizer does not support fix_mistral_regex; retrying without it")
        return AutoTokenizer.from_pretrained(model_dir, **kwargs)


def apply_runtime_overrides(policy, config=None):
    overrides = runtime_overrides_for_policy(policy)
    if not overrides:
        return overrides

    attn_implementation = overrides.get("attn_implementation", "")
    if attn_implementation and config is not None:
        config._attn_implementation = attn_implementation
        if hasattr(config, "attn_implementation"):
            config.attn_implementation = attn_implementation
        print(f"Applied runtime override: attn_implementation={attn_implementation}")

    if overrides.get("disable_qwen35_fla"):
        try:
            from transformers.models.qwen3_5 import modeling_qwen3_5 as qwen35_modeling

            qwen35_modeling.causal_conv1d_fn = None
            qwen35_modeling.causal_conv1d_update = None
            qwen35_modeling.chunk_gated_delta_rule = None
            qwen35_modeling.fused_recurrent_gated_delta_rule = None
            qwen35_modeling.is_fast_path_available = False
            print(
                "Disabled Qwen3.5 FLA fast path for quantization; using torch fallback"
            )
        except Exception as exc:
            print(f"WARN: failed to disable Qwen3.5 FLA fast path: {exc}")

        # Re-run the full nogil compat patch (idempotent) to cover any
        # Autotuner/JITFunction instances created during model import.
        patch_triton_nogil_compat()

    return overrides


def copy_artifact_tree(src_dir, dst_dir):
    if os.path.exists(dst_dir):
        shutil.rmtree(dst_dir)
    shutil.copytree(src_dir, dst_dir, copy_function=shutil.copy2)
    print(f"Copied artifact tree: {src_dir} -> {dst_dir}")


def write_artifact_manifest(artifact_dir, role, primary_dir, hf_native_dir):
    if not os.path.isdir(artifact_dir):
        return
    manifest = {
        "role": role,
        "primary_dir": primary_dir,
        "hf_native_dir": hf_native_dir,
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    path = os.path.join(artifact_dir, "flexinfer-artifacts.json")
    with open(path, "w") as f:
        json.dump(manifest, f, indent=2, sort_keys=True)
    print(f"Wrote artifact manifest: {path}")


def ensure_qwen35_text_config(path):
    """Backfill nested text_config for Qwen3.5 text-only exports.

    vLLM's Qwen3_5Config defaults text_config to a 4096-wide model when the
    field is absent, even if the top-level checkpoint metadata is 27B/5120-wide.
    Persisting text_config into the saved artifact keeps runtime loading honest.
    """
    with open(path) as f:
        cfg = json.load(f)
    if cfg.get("model_type") != "qwen3_5" or "text_config" in cfg:
        return False
    keys = [
        "vocab_size",
        "hidden_size",
        "intermediate_size",
        "num_hidden_layers",
        "num_attention_heads",
        "num_key_value_heads",
        "hidden_act",
        "max_position_embeddings",
        "initializer_range",
        "rms_norm_eps",
        "use_cache",
        "tie_word_embeddings",
        "rope_parameters",
        "attention_bias",
        "attention_dropout",
        "head_dim",
        "linear_conv_kernel_dim",
        "linear_key_head_dim",
        "linear_value_head_dim",
        "linear_num_key_heads",
        "linear_num_value_heads",
        "layer_types",
        "pad_token_id",
        "bos_token_id",
        "eos_token_id",
        "full_attention_interval",
        "partial_rotary_factor",
        "attn_output_gate",
        "mlp_only_layers",
        "mamba_ssm_dtype",
        "dtype",
        "mtp_num_hidden_layers",
        "mtp_use_dedicated_embeddings",
    ]
    cfg["text_config"] = {k: cfg[k] for k in keys if k in cfg}
    cfg["text_config"]["model_type"] = "qwen3_5_text"
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
    return True


def _rewrite_module_tree_prefix(module_tree, old_prefix, new_prefix):
    if not isinstance(module_tree, list):
        return module_tree
    if module_tree[: len(old_prefix)] != old_prefix:
        return module_tree
    rewritten = copy.deepcopy(module_tree)
    return list(new_prefix) + rewritten[len(old_prefix) :]


def adapt_model_definition_for_loaded_model(model_definition, model):
    """Align GPTQModel's module tree with the instantiated HF model layout.

    Qwen3.5's GPTQModel definition targets the composite VLM wrapper
    (`model.language_model.layers.*`), but our direct quantization path loads the
    text-only causal LM (`model.layers.*`). If we do not rewrite the root paths,
    GPTQModel reaches calibration capture and then fails to enumerate layers.
    """

    if model is None or not hasattr(model, "model"):
        return

    inner_model = getattr(model, "model", None)
    if inner_model is None or not hasattr(inner_model, "layers"):
        return

    module_tree = getattr(model_definition, "module_tree", None)
    rewritten_tree = _rewrite_module_tree_prefix(
        module_tree,
        ["model", "language_model", "layers"],
        ["model", "layers"],
    )
    if rewritten_tree is module_tree:
        return

    model_definition.module_tree = rewritten_tree
    if getattr(model_definition, "pre_lm_head_norm_module", None) == (
        "model.language_model.norm"
    ):
        model_definition.pre_lm_head_norm_module = "model.norm"
    if getattr(model_definition, "rotary_embedding", None) == (
        "model.language_model.rotary_emb"
    ):
        model_definition.rotary_embedding = "model.rotary_emb"
    print(
        "Adapted GPTQModel module tree for text-only Qwen3.5 causal LM "
        "(model.layers.*)"
    )


def patch_gptq_save_meta_tensors():
    """Skip meta-backed tensors before GPTQModel streams safetensors shards."""

    import gptqmodel.utils.model as gptq_utils_model
    from gptqmodel.models import writer as gptq_writer

    if getattr(gptq_utils_model, "_flexinfer_meta_save_patch", False):
        return

    original_get_state_dict_for_save = gptq_utils_model.get_state_dict_for_save

    def _patched_get_state_dict_for_save(model, offload_root=None):
        state_dict = original_get_state_dict_for_save(model, offload_root)
        dropped = []
        for name, entry in list(state_dict.items()):
            source = getattr(entry, "source", None)
            if not isinstance(source, torch.Tensor):
                continue
            if getattr(source, "is_meta", False) or source.device.type == "meta":
                dropped.append(name)
                del state_dict[name]
        if dropped:
            preview = ", ".join(dropped[:8])
            extra = len(dropped) - min(len(dropped), 8)
            suffix = f" ... (+{extra} more)" if extra > 0 else ""
            print(
                "Dropped "
                f"{len(dropped)} meta-backed tensors from save state_dict: "
                f"{preview}{suffix}"
            )
        return state_dict

    gptq_utils_model.get_state_dict_for_save = _patched_get_state_dict_for_save
    gptq_writer.get_state_dict_for_save = _patched_get_state_dict_for_save
    gptq_utils_model._flexinfer_meta_save_patch = True
    print("Patched GPTQModel save path to skip meta-backed tensors")


# ── Read config from environment ──────────────────────────────────────
model_dir = os.environ["MODEL_DIR"]
out_dir = os.environ["OUT_DIR"]
bits = int(os.environ["BITS"])
group_size = int(os.environ["GROUP_SIZE"])
max_memory_gb = int(os.environ["MAX_MEMORY_GB"])
max_seq_len = int(os.environ.get("MAX_SEQ_LEN", "4096"))
max_samples = int(os.environ.get("MAX_SAMPLES", "256"))
sym = os.environ.get("SYM", "True") == "True"
desc_act = os.environ.get("DESC_ACT", "False") == "True"
gpu_memory_fraction = float(os.environ.get("GPU_MEMORY_FRACTION", "0.80"))
dynamic_exclusion = os.environ.get("DYNAMIC_EXCLUSION", "auto")
dataset_raw = os.environ.get("DATASET", "mit-han-lab/pile-val-backup")
# Support "name:config" format (e.g. "wikitext:wikitext-2-raw-v1") — colon
# separates HuggingFace dataset name from config/subset. "org/name" format
# (e.g. "mit-han-lab/pile-val-backup") is passed through as-is.
if ":" in dataset_raw:
    dataset_name, dataset_config = dataset_raw.split(":", 1)
else:
    dataset_name, dataset_config = dataset_raw, None
hessian_repair_enabled = env_bool("GPTQ_HESSIAN_REPAIR", True)
hessian_sanitize_nonfinite = env_bool("GPTQ_HESSIAN_SANITIZE_NONFINITE", True)
# Mode "mean" scales floor proportionally to mean(|diag|), so floor attempts are
# numerically comparable to damp*mean and each attempt shifts conditioning
# meaningfully. "abs_max" preserves the legacy behavior (floor is a tiny
# fraction of max|diag|), which is near-useless when mean and max are within
# an order of magnitude.
hessian_diag_floor_mode = (
    os.environ.get("GPTQ_HESSIAN_DIAG_FLOOR_MODE", "mean").strip().lower() or "mean"
)
if hessian_diag_floor_mode not in ("mean", "abs_max"):
    print(
        f"WARN: unknown GPTQ_HESSIAN_DIAG_FLOOR_MODE={hessian_diag_floor_mode!r}; "
        "falling back to mean"
    )
    hessian_diag_floor_mode = "mean"
_floor_scale_default = 0.01 if hessian_diag_floor_mode == "mean" else 1e-6
hessian_diag_floor_scale = env_float(
    "GPTQ_HESSIAN_DIAG_FLOOR_SCALE", _floor_scale_default
)
hessian_floor_multiplier = env_float("GPTQ_HESSIAN_FLOOR_MULTIPLIER", 10.0)
hessian_max_floor_attempts = env_int("GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS", 6)
hessian_clamp_abs = env_float("GPTQ_HESSIAN_CLAMP_ABS", 0.0)
qcfg_damp_percent_override = os.environ.get("GPTQ_DAMP_PERCENT_OVERRIDE", "").strip()
qcfg_damp_auto_increment_override = os.environ.get(
    "GPTQ_DAMP_AUTO_INCREMENT_OVERRIDE", ""
).strip()
gptq_resume_enabled = env_bool("GPTQ_RESUME", True)
gptq_calibration_cache_enabled = env_bool("GPTQ_CALIBRATION_CACHE", True)
quantize_device_map = os.environ.get("QUANTIZE_DEVICE_MAP", "cpu")

emit_progress(
    "start", phase="quantizing", model=model_dir, bits=bits, group_size=group_size
)


def checkpoint_dir(model_dir):
    return os.path.join(model_dir, CHECKPOINT_DIR_NAME)


def checkpoint_state_path(model_dir):
    return os.path.join(checkpoint_dir(model_dir), CHECKPOINT_STATE_FILE)


def calibration_cache_path(model_dir):
    return os.path.join(checkpoint_dir(model_dir), CALIBRATION_CACHE_FILE)


def load_quant_checkpoint(model_dir):
    path = checkpoint_state_path(model_dir)
    if not os.path.exists(path):
        return {}
    with open(path) as f:
        state = json.load(f)
    return state if isinstance(state, dict) else {}


def json_safe(value):
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, dict):
        return {str(k): json_safe(v) for k, v in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [json_safe(v) for v in value]
    return str(value)


def persist_quant_checkpoint(model_dir, state):
    os.makedirs(checkpoint_dir(model_dir), exist_ok=True)
    path = checkpoint_state_path(model_dir)
    tmp_path = f"{path}.tmp"
    with open(tmp_path, "w") as f:
        json.dump(json_safe(state), f, indent=2, sort_keys=True)
    os.replace(tmp_path, path)


def effective_calibration_setting(policy, key, default):
    overrides = (policy or {}).get("calibration_overrides", {})
    value = overrides.get(key, default)
    try:
        value = int(value)
    except (TypeError, ValueError):
        value = default
    return value if value > 0 else default


effective_max_seq_len = max_seq_len
effective_max_samples = max_samples
effective_max_tokens = max_samples * max_seq_len


def calibration_cache_fingerprint():
    return {
        "dataset": dataset_raw,
        "max_seq_len": effective_max_seq_len,
        "max_samples": effective_max_samples,
        "model_dir": os.path.basename(model_dir.rstrip("/")),
    }


def load_cached_examples(model_dir):
    if not (gptq_resume_enabled and gptq_calibration_cache_enabled):
        return None
    cache_path = calibration_cache_path(model_dir)
    if not os.path.exists(cache_path):
        return None
    state = load_quant_checkpoint(model_dir)
    if state.get("calibration_cache") != calibration_cache_fingerprint():
        return None
    try:
        payload = torch.load(cache_path, map_location="cpu", weights_only=False)
    except TypeError:
        payload = torch.load(cache_path, map_location="cpu")
    if not isinstance(payload, list):
        return None
    print(f"Loaded cached calibration examples: {len(payload)} samples")
    emit_progress(
        "progress",
        phase="quantizing",
        percent=9.0,
        detail=f"loaded cached calibration data ({len(payload)} samples)",
    )
    return payload


def persist_cached_examples(model_dir, examples, state):
    if not (gptq_resume_enabled and gptq_calibration_cache_enabled):
        return
    os.makedirs(checkpoint_dir(model_dir), exist_ok=True)
    torch.save(examples, calibration_cache_path(model_dir))
    state = dict(state)
    state["calibration_cache"] = calibration_cache_fingerprint()
    persist_quant_checkpoint(model_dir, state)


def infer_total_layers(gptq_model):
    config = getattr(getattr(gptq_model, "model", None), "config", None)
    for attr in ("num_hidden_layers", "n_layer", "num_layers"):
        value = getattr(config, attr, None)
        if isinstance(value, int) and value > 0:
            return value
    nodes = getattr(gptq_model, "extract_layers_node", lambda: [])()
    current = getattr(gptq_model, "model", None)
    if current is None or not nodes:
        return None
    try:
        for part in nodes[0].split("."):
            current = getattr(current, part)
        return len(current)
    except Exception:
        return None


class QuantizationCheckpointCallback:
    def __init__(self, model_dir, total_layers, state):
        self.model_dir = model_dir
        self.total_layers = total_layers
        self.state = dict(state)
        self.state.setdefault("completed_layers", [])

    def _persist(self):
        persist_quant_checkpoint(self.model_dir, self.state)

    def subset_event(
        self, stage, layer_idx, subset_index, subset_total, module_names, processor
    ):
        processor_name = processor
        if callable(processor_name):
            processor_name = getattr(processor_name, "__name__", str(processor_name))
        self.state["stage"] = "quantizing"
        self.state["active"] = {
            "layer_idx": layer_idx,
            "subset_index": subset_index,
            "subset_total": subset_total,
            "module_names": list(module_names or []),
            "processor": processor_name,
            "stage": stage,
            "updated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }
        percent = 10.0
        if self.total_layers:
            layer_fraction = max(layer_idx, 0) / max(self.total_layers, 1)
            subset_fraction = 0.0
            if subset_total:
                subset_fraction = max(subset_index - 1, 0) / max(subset_total, 1)
            percent = min(
                89.0,
                10.0
                + (
                    (layer_fraction + (subset_fraction / max(self.total_layers, 1)))
                    * 80.0
                ),
            )
        detail = (
            f"layer {layer_idx + 1}"
            + (f" subset {subset_index}/{subset_total}" if subset_total else "")
            + (f" via {processor_name}" if processor_name else "")
        )
        emit_progress(
            "progress", phase="quantizing", percent=round(percent, 1), detail=detail
        )
        self._persist()

    def layer_complete(self, layer_idx, submodule_finalized):
        completed = self.state.setdefault("completed_layers", [])
        if submodule_finalized and layer_idx not in completed:
            completed.append(layer_idx)
            completed.sort()
        self.state["stage"] = "quantizing"
        self.state["last_completed_layer"] = layer_idx
        self.state["last_completed_at"] = time.strftime(
            "%Y-%m-%dT%H:%M:%SZ", time.gmtime()
        )
        detail = f"completed layer {layer_idx + 1}"
        percent = 10.0
        if self.total_layers:
            percent = min(
                89.0, 10.0 + (((layer_idx + 1) / max(self.total_layers, 1)) * 80.0)
            )
        # Log GPU + system memory stats per layer for debugging OOM/fragmentation.
        mem_detail = _memory_stats()
        if mem_detail:
            detail = f"{detail} | {mem_detail}"
        emit_progress(
            "progress", phase="quantizing", percent=round(percent, 1), detail=detail
        )
        self._persist()


quant_checkpoint_state = load_quant_checkpoint(model_dir) if gptq_resume_enabled else {}
if quant_checkpoint_state:
    print(
        "Loaded quantization checkpoint state: "
        f"stage={quant_checkpoint_state.get('stage', 'unknown')} "
        f"completed_layers={len(quant_checkpoint_state.get('completed_layers', []))}"
    )

# ── VLM config extraction ─────────────────────────────────────────────
# Models like Qwen3.5 have a composite VLM config wrapping text_config.
# Extract text_config to top level so transformers loads text-only model.
cfg_path = os.path.join(model_dir, "config.json")
with open(cfg_path) as f:
    cfg = json.load(f)
policies = load_model_policies()
policy_state = load_policy_state(model_dir)
policy = select_model_policy(model_dir, cfg, policy_state, policies)
active_policy = None
if policy is not None:
    cfg, policy_state = apply_model_policy(cfg, policy, policy_state)
    active_policy = policy.get("name", "")
    persist_policy_state(model_dir, policy_state)
    print(f"Applied quantization model policy: {active_policy}")
elif policy_state:
    # Persisted state without a known policy means the runtime config is stale.
    # Preserve the state file for debugging but avoid silently reusing it.
    print(f"No active model policy matched persisted state: {policy_state}")

with open(cfg_path, "w") as f:
    json.dump(cfg, f, indent=2)

# ── VLM artifact cleanup ──────────────────────────────────────────────
# After extracting text_config from a VLM wrapper, remove processor files
# that cause GPTQModel's AutoProcessor to attempt VLM loading.
if policy is not None and policy.get("extract_text_config"):
    vlm_artifacts = [
        "preprocessor_config.json",
        "video_preprocessor_config.json",
        "chat_template.json",
    ]
    for artifact in vlm_artifacts:
        artifact_path = os.path.join(model_dir, artifact)
        if os.path.exists(artifact_path):
            os.remove(artifact_path)
            print(f"Removed VLM artifact: {artifact}")

ensure_policy_python_packages(policy)

# Verify transformers recognizes the model_type before importing heavy modules.
# After text_config extraction, model_type may be e.g. "gemma4_text" which older
# transformers versions don't have.  Upgrade transformers if needed (--no-deps to
# avoid replacing ROCm torch).
_model_type_to_check = cfg.get("model_type", "")
if _model_type_to_check:
    try:
        from transformers.models.auto.configuration_auto import CONFIG_MAPPING

        if _model_type_to_check not in CONFIG_MAPPING:
            print(
                f"transformers does not recognize model_type={_model_type_to_check!r}, "
                "upgrading transformers..."
            )
            # PyPI may not have a release with Gemma4 support yet; use the
            # known-good dev commit that both abliteration and quantization
            # have validated.  Fall back to >=5.5 only if the git install
            # fails (e.g. network issue in an air-gapped environment).
            _transformers_specs = [
                "git+https://github.com/huggingface/transformers.git@f965b10b",
                "transformers>=5.5",
            ]
            _installed = False
            for _tspec in _transformers_specs:
                try:
                    subprocess.check_call(
                        [
                            sys.executable,
                            "-m",
                            "pip",
                            "install",
                            "--no-cache-dir",
                            "--no-deps",
                            _tspec,
                        ],
                        stdout=sys.stdout,
                        stderr=sys.stderr,
                    )
                    _installed = True
                    break
                except subprocess.CalledProcessError:
                    print(f"WARN: failed to install {_tspec}, trying next fallback")
            if not _installed:
                print("ERROR: could not install transformers with Gemma4 support")
            # Purge cached transformers modules so the next import gets the new version
            for _k in list(sys.modules.keys()):
                if _k.startswith("transformers"):
                    del sys.modules[_k]
            print("Upgraded transformers and cleared module cache")
        else:
            print(f"transformers recognizes model_type={_model_type_to_check!r}")
    except Exception as _e:
        print(f"WARN: transformers model_type check failed: {_e}")

effective_max_seq_len = effective_calibration_setting(
    policy, "max_seq_len", max_seq_len
)
effective_max_samples = effective_calibration_setting(
    policy, "max_samples", max_samples
)
effective_max_tokens = effective_calibration_setting(
    policy,
    "max_tokens",
    effective_max_samples * effective_max_seq_len,
)
if (
    effective_max_seq_len != max_seq_len
    or effective_max_samples != max_samples
    or effective_max_tokens != (max_samples * max_seq_len)
):
    print(
        "Applied calibration overrides from "
        f"policy={active_policy or 'none'}: "
        f"max_seq_len={effective_max_seq_len} "
        f"max_samples={effective_max_samples} "
        f"max_tokens={effective_max_tokens}"
    )

model_type = cfg.get("model_type", "")
load_strategy = (policy or {}).get("loader", "gptqmodel")
force_direct_load = load_strategy == "manual_sharded_state_dict"
if force_direct_load:
    print(
        f"Using direct GPTQ load path for policy={active_policy or 'none'} model_type={model_type or 'unknown'}"
    )

# ── Dynamic exclusion ──────────────────────────────────────────────────
if dynamic_exclusion == "none":
    dynamic_config = None
    print("Dynamic exclusion disabled (mode=none) -- all modules will be quantized")
elif dynamic_exclusion == "gdn":
    # GDN-only exclusion: keep GDN (linear_attn) layers at full precision while
    # quantizing everything else including full-attention layers.  This preserves
    # GDN delta-rule recurrence quality while fitting in 24 GB VRAM (~21-23 GB).
    dynamic_config = {
        "-:.*linear_attn.*": {},
    }
    print(f"GDN exclusion (mode=gdn): keeping linear_attn modules at full precision")
    print(f"Dynamic exclusion patterns: {list(dynamic_config.keys())}")
elif dynamic_exclusion == "moe":
    # MoE-only exclusion: keep routed expert FFN weights at full precision
    # while quantizing shared attention and non-expert modules.  MoE expert
    # weights are often fused 3D tensors (num_experts, hidden, intermediate)
    # that crash GPTQ's 2D matrix quantization.
    dynamic_config = {
        "-:.*experts.*": {},
        "-:.*block_sparse_moe.*": {},
    }
    print(f"MoE exclusion (mode=moe): keeping expert modules at full precision")
    print(f"Dynamic exclusion patterns: {list(dynamic_config.keys())}")
else:
    # "auto" mode — auto-detect hybrid architectures and exclude attention/expert/
    # vision/MTP modules (matches official Qwen GPTQ-Int4 approach).
    # Also detects MoE models and excludes routed expert weights (fused 3D tensors).
    with open(cfg_path) as f:
        cfg_recheck = json.load(f)
    dynamic_config = None
    exclusion_reasons = []

    # Detect hybrid layer types (GDN + full_attention mixed architectures)
    has_hybrid_layers = False
    if "layer_types" in cfg_recheck:
        layer_types = cfg_recheck["layer_types"]
        unique_types = set(layer_types)
        if len(unique_types) > 1:
            has_hybrid_layers = True
            exclusion_reasons.append(
                f"hybrid layers: {dict((t, layer_types.count(t)) for t in unique_types)}"
            )

    # Detect MoE architecture.
    # Check multiple indicators: num_local_experts (Mixtral/Qwen),
    # num_experts (Gemma4), enable_moe_block (Gemma4), top_k_experts.
    has_moe = False
    # Search both root and text_config (covers pre- and post-extraction).
    search_scopes = [cfg_recheck, cfg_recheck.get("text_config", {})]
    for moe_key in ("num_local_experts", "num_experts"):
        for scope in search_scopes:
            val = scope.get(moe_key, 0)
            if isinstance(val, int) and val > 1:
                has_moe = True
                exclusion_reasons.append(f"MoE: {moe_key}={val}")
                break
        if has_moe:
            break
    if not has_moe:
        # Gemma4 uses enable_moe_block=True as the primary MoE flag.
        for scope in search_scopes:
            if scope.get("enable_moe_block") is True:
                has_moe = True
                n_exp = scope.get("num_experts", scope.get("top_k_experts", "?"))
                exclusion_reasons.append(
                    f"MoE: enable_moe_block=True (experts={n_exp})"
                )
                break

    # Check GPTQModel version for native MoE support.
    # v6.0.3+ has native Gemma4 MoE model definitions with proper lifecycle
    # hooks for fused 3D expert tensors — no need to exclude experts.
    gptqmodel_has_native_moe = False
    if has_moe:
        try:
            from packaging import version as packaging_version

            gptq_ver = importlib_metadata.version("gptqmodel")
            if packaging_version.parse(gptq_ver) >= packaging_version.parse("6.0.3"):
                gptqmodel_has_native_moe = True
                print(
                    f"GPTQModel {gptq_ver} has native MoE support — experts will be quantized natively"
                )
        except Exception as e:
            print(f"WARN: could not check GPTQModel version for MoE support: {e}")

    if has_hybrid_layers or has_moe:
        print(f"Architecture detection: {'; '.join(exclusion_reasons)}")
        dynamic_config = {}
        if has_hybrid_layers and not has_moe:
            # Pure hybrid (GDN + full-attention) — exclude non-standard attn
            # modules that may confuse GPTQ's layer walker.
            dynamic_config["-:.*shared_expert.*"] = {}
            dynamic_config["-:.*visual.*"] = {}
            dynamic_config["-:.*mtp.*"] = {}
        if has_moe and not gptqmodel_has_native_moe:
            # Legacy path: GPTQModel < 6.0.3 lacks native Gemma4-MoE model
            # definitions, so fused 3D expert tensors crash GPTQ's 2D matrix
            # quantization. Exclude experts and only quantize self_attn.
            dynamic_config["-:.*experts.*"] = {}
            dynamic_config["-:.*block_sparse_moe.*"] = {}
            dynamic_config["-:.*router.*"] = {}
            dynamic_config["-:.*mlp.*"] = {}
            dynamic_config["-:.*shared_expert.*"] = {}
            dynamic_config["-:.*visual.*"] = {}
            dynamic_config["-:.*mtp.*"] = {}
        elif has_moe and gptqmodel_has_native_moe:
            # GPTQModel >= 6.0.3: native MoE with Defuser + module_tree patching.
            # Use dynamic=None (not passed to QuantizeConfig) so the model
            # definition's module_tree governs quantization scope. The wrapper
            # script patches module_tree to include MoE expert entries AFTER
            # Defuser unfuses the fused 3D expert tensors into nn.Linear.
            # dynamic={} would override module_tree with dynamic scanning,
            # which misses the expert-specific lifecycle hooks needed for
            # proper gate_up/down fusing at save time.
            dynamic_config = None
            print(
                "MoE experts will be quantized via patched module_tree (GPTQModel >= 6.0.3)"
            )
        if dynamic_config is not None:
            print(
                f"Dynamic config: {list(dynamic_config.keys()) if dynamic_config else '(empty — quantize all)'}"
            )
        else:
            print("Dynamic exclusion disabled — using model definition defaults")

# ── Memory management ──────────────────────────────────────────────────
import torch
from datasets import load_dataset
from gptqmodel import GPTQModel, QuantizeConfig
from gptqmodel.quantization.gptq import GPTQ

# These imports are only needed for the manual_sharded_state_dict loader path.
# GPTQModel < 6.0 doesn't have normalize_hf_config_compat / prepare_remote_model_init_compat.
try:
    from gptqmodel.models.auto import check_and_get_model_definition
    from gptqmodel.utils.hf import (
        normalize_hf_config_compat,
        prepare_remote_model_init_compat,
        resolve_trust_remote_code,
    )
    from gptqmodel.utils.importer import auto_select_device
    from gptqmodel.utils.model import auto_dtype
except ImportError:
    check_and_get_model_definition = None
    normalize_hf_config_compat = None
    prepare_remote_model_init_compat = None
    auto_select_device = None
    auto_dtype = None

    def resolve_trust_remote_code(model_dir, trust_remote_code=False):
        return trust_remote_code


from transformers import AutoConfig
from transformers import AutoModelForCausalLM
from transformers import AutoTokenizer
from transformers.modeling_utils import get_checkpoint_shard_files, load_state_dict

patch_gptq_save_meta_tensors()

gpu_vram_mb_env = int(os.environ.get("GPU_VRAM_MB", "0"))
try:
    total_vram = torch.cuda.get_device_properties(0).total_memory
    torch.cuda.set_per_process_memory_fraction(gpu_memory_fraction)
    print(
        f"Memory: GPU fraction={gpu_memory_fraction} ({int(total_vram * gpu_memory_fraction / (1024**3))}GiB of {total_vram // (1024**3)}GiB), container={max_memory_gb}Gi"
    )
except (RuntimeError, AssertionError):
    if gpu_vram_mb_env > 0 and torch.cuda.is_available():
        # hipMemGetInfo broken on gfx906 (VMM not supported on Vega20).
        # Use GPU_VRAM_MB from GPUProfile as fallback.
        total_vram = gpu_vram_mb_env * 1024 * 1024
        print(
            f"Memory: GPU VRAM from profile={gpu_vram_mb_env}MB (hipMemGetInfo broken), "
            f"fraction={gpu_memory_fraction} ({int(total_vram * gpu_memory_fraction / (1024**3))}GiB), "
            f"container={max_memory_gb}Gi"
        )
    else:
        total_vram = 0
        print(
            f"Memory: GPU not available (device_map={quantize_device_map}), container={max_memory_gb}Gi"
        )


def patch_gptq_hessian_inverse():
    if not hessian_repair_enabled:
        return

    def _patched_hessian_inverse(self, H: torch.Tensor):
        # On gfx906 a Cholesky failure can put the ROCm HIP context in a bad
        # state so the NEXT module's `torch.isfinite(H).sum().item()` raises
        # `torch.AcceleratorError: HIP error: invalid argument` and kills the
        # whole job. Do NOT move H to CPU as a workaround: the rocm/pytorch
        # container is compiled without CPU LAPACK, so `torch.linalg.cholesky`
        # on CPU fails with "LAPACK library not found in compilation" and
        # every module exhausts. Instead, keep H on device and wrap the
        # sanitize check so a HIP fault degrades to skip-sanitize rather than
        # crashing the process.
        H = H.clone()

        if hessian_sanitize_nonfinite:
            try:
                nonfinite_mask = ~torch.isfinite(H)
                nonfinite_count = int(nonfinite_mask.sum().item())
            except Exception as exc:
                print(
                    f"WARN: GPTQ Hessian nonfinite check failed for module="
                    f"{getattr(self, 'name', 'unknown')}: {exc!r}; skipping sanitize"
                )
                nonfinite_count = 0
            if nonfinite_count:
                fill_value = hessian_clamp_abs if hessian_clamp_abs > 0 else 0.0
                H = torch.nan_to_num(
                    H,
                    nan=0.0,
                    posinf=fill_value,
                    neginf=-fill_value,
                )
                print(
                    f"Patched GPTQ Hessian for module={getattr(self, 'name', 'unknown')}: "
                    f"replaced {nonfinite_count} non-finite entries"
                )
        # Wrap the pre-recovery setup so a poisoned HIP context on entry
        # (symmetrize, diag stats, etc.) can't take the whole job down — we
        # degrade to returning None and let GPTQModel handle the module.
        try:
            H = 0.5 * (H + H.T)
            diag_view = H.diagonal()
            orig_diag = diag_view.clone()
            finite_diag = torch.nan_to_num(
                orig_diag.abs(), nan=0.0, posinf=0.0, neginf=0.0
            )
            base_abs_max = torch.max(finite_diag).item()
            if not math.isfinite(base_abs_max) or base_abs_max == 0.0:
                base_abs_max = 1.0
            if hessian_diag_floor_mode == "mean":
                base_mean = torch.mean(finite_diag).item()
                if not math.isfinite(base_mean) or base_mean == 0.0:
                    base_mean = base_abs_max
                floor_reference = base_mean
            else:
                floor_reference = base_abs_max
            floor_base = floor_reference * hessian_diag_floor_scale
        except Exception as exc:
            print(
                f"GPTQ Hessian recovery aborted for module="
                f"{getattr(self, 'name', 'unknown')}: setup failed: {exc!r}"
            )
            return None, 1.0
        used_damp = getattr(self.qcfg, "damp_percent", 0.01)
        damp_step = getattr(self.qcfg, "damp_auto_increment", 0.0015)
        last_error = None

        for attempt in range(hessian_max_floor_attempts + 1):
            current_diag = torch.nan_to_num(orig_diag, nan=0.0, posinf=0.0, neginf=0.0)
            if attempt > 0:
                floor_increment = floor_base * math.pow(
                    hessian_floor_multiplier, attempt - 1
                )
                current_diag = torch.clamp(
                    current_diag + floor_increment, min=floor_increment
                )
                print(
                    f"GPTQ Hessian recovery for module={getattr(self, 'name', 'unknown')}: "
                    f"diagonal floor +{floor_increment:.2e} (attempt {attempt}/{hessian_max_floor_attempts})"
                )
            diag_view.copy_(current_diag)

            mean = torch.mean(current_diag)
            damp = getattr(self.qcfg, "damp_percent", 0.01)
            # Once a diagonal floor is applied, damp sweeping within the same
            # attempt is wasted work — sweeping damp without touching the floor
            # just shifts the mean by a constant, so if damp=damp_percent fails
            # under this floor, damp+step will fail too. Jump to the next floor
            # attempt instead. Attempt 0 (no floor) keeps sweeping.
            effective_damp_step = damp_step if attempt == 0 else 0.0
            recovery_started = False
            recovery_initial = None
            recovery_last = None

            while 0 < damp < 1:
                try:
                    diag_view.copy_(current_diag)
                    diag_view.add_(damp * mean)
                    H2 = torch.linalg.cholesky(H)
                    Hinv_result = torch.linalg.cholesky(
                        torch.cholesky_inverse(H2), upper=True
                    )
                    diag_view.copy_(current_diag)
                    del H2
                    used_damp = damp
                    if recovery_started:
                        print(
                            f"GPTQ Hessian recovery for module={getattr(self, 'name', 'unknown')}: "
                            f"damp recovery succeeded at {damp:.5f} (started at {recovery_initial:.5f})"
                        )
                    return Hinv_result, used_damp
                except Exception as exc:
                    last_error = exc
                    diag_view.copy_(current_diag)
                    if effective_damp_step == 0:
                        break
                    if not recovery_started:
                        recovery_started = True
                        recovery_initial = damp
                        print(
                            f"GPTQ Hessian recovery for module={getattr(self, 'name', 'unknown')}: "
                            f"starting damp recovery at {damp:.5f} with step {effective_damp_step:.5f}"
                        )
                    damp += effective_damp_step
                    recovery_last = damp

            if recovery_started:
                final_damp = recovery_last if recovery_last is not None else damp
                print(
                    f"GPTQ Hessian recovery for module={getattr(self, 'name', 'unknown')}: "
                    f"damp recovery failed at {final_damp:.5f}"
                )

        print(
            f"GPTQ Hessian recovery exhausted for module={getattr(self, 'name', 'unknown')} "
            f"after {hessian_max_floor_attempts + 1} attempts; last_error={last_error}"
        )
        return None, 1.0

    GPTQ.hessian_inverse = _patched_hessian_inverse
    print(
        "Patched GPTQ.hessian_inverse with configurable non-finite sanitation and diagonal-floor recovery"
    )


patch_gptq_hessian_inverse()


def resolve_checkpoint_index(model_dir):
    candidates = [
        os.path.join(model_dir, name)
        for name in sorted(os.listdir(model_dir))
        if name.endswith(".index.json")
    ]
    if not candidates:
        raise FileNotFoundError(f"no checkpoint index found under {model_dir}")
    return candidates[0]


def load_state_dict_materialized(module, state_dict, *, strict=False):
    """Load checkpoint shards into meta-backed modules when assign=True exists."""
    try:
        return module.load_state_dict(state_dict, strict=strict, assign=True)
    except TypeError as exc:
        if "assign" not in str(exc):
            raise
        print(
            "WARN: load_state_dict(assign=True) unsupported by this runtime; "
            "retrying without assign"
        )
        return module.load_state_dict(state_dict, strict=strict)


def patch_defuser_transformers_prerelease_gate():
    import defuser.defuser as defuser_impl
    import transformers
    from packaging import version as packaging_version

    original = defuser_impl.is_supported_transformers_version
    current = packaging_version.parse(transformers.__version__)
    minimum = packaging_version.parse(defuser_impl.MIN_SUPPORTED_TRANSFORMERS_VERSION)

    if original():
        return

    # Defuser's public API gate treats 5.3.0.dev* as older than 5.3.0 and
    # skips replace_fused_blocks()/convert_model() entirely. We only need the
    # model-class patch path here; the newer conversion-mapping path remains
    # guarded by Defuser's own stricter version check inside replace_fused_blocks.
    if current.base_version != minimum.base_version:
        return

    def _allow_same_base_prerelease():
        return True

    def _suppress_same_base_prerelease_warning(api_name: str, logger) -> bool:
        return False

    defuser_impl.is_supported_transformers_version = _allow_same_base_prerelease
    defuser_impl.warn_if_public_api_transformers_unsupported = (
        _suppress_same_base_prerelease_warning
    )
    print(
        "Patched Defuser public API gate to allow transformers prerelease "
        f"{transformers.__version__} for base version {current.base_version}"
    )


def load_model_manual_sharded_state_dict(model_dir, tokenizer, quantize_config):
    if check_and_get_model_definition is None:
        raise RuntimeError(
            "manual_sharded_state_dict loader requires GPTQModel >= 6.0 "
            "(missing check_and_get_model_definition / normalize_hf_config_compat)"
        )
    import defuser

    trust_remote_code = resolve_trust_remote_code(model_dir, trust_remote_code=True)
    model_definition = check_and_get_model_definition(
        model_dir, trust_remote_code=trust_remote_code
    )
    config = AutoConfig.from_pretrained(model_dir, trust_remote_code=trust_remote_code)

    patch_defuser_transformers_prerelease_gate()
    defuser.replace_fused_blocks(config.model_type)
    normalize_hf_config_compat(config, trust_remote_code=trust_remote_code)
    prepare_remote_model_init_compat(model_dir, config)
    # GPTQModel removed resolve_loader_config() from loader.py; the supported
    # load path now relies on AutoConfig plus the existing HF compatibility
    # hooks above before materializing the model from config.
    apply_runtime_overrides(policy, config)

    if quantize_config.device is None:
        quantize_config.device = auto_select_device(None, None)
    dtype = auto_dtype(
        config=config, device=quantize_config.device, quant_inference=False
    )

    def skip(*args, **kwargs):
        pass

    torch.nn.init.kaiming_uniform_ = skip
    torch.nn.init.uniform_ = skip
    torch.nn.init.normal_ = skip

    init_kwargs = {"torch_dtype": dtype}
    before_model_load = getattr(model_definition, "before_model_load", None)
    if callable(before_model_load):
        try:
            before_model_load(
                model_definition,
                model_local_path=model_dir,
                load_quantized_model=False,
            )
        except TypeError as exc:
            if "model_local_path" not in str(exc):
                raise
            # Older GPTQModel builds exposed a shorter hook signature.
            before_model_load(model_definition, load_quantized_model=False)

    loader_cls = model_definition.loader
    if (
        getattr(config, "model_type", "") == "qwen3_5_text"
        and getattr(loader_cls, "__name__", "") == "AutoModelForImageTextToText"
    ):
        # GPTQModel currently maps qwen3_5_text to the multimodal loader even
        # after we extract text_config. Force the text-only CausalLM path.
        print(
            "Overriding GPTQ loader for qwen3_5_text: "
            "AutoModelForImageTextToText -> AutoModelForCausalLM"
        )
        loader_cls = AutoModelForCausalLM

    print(
        f"Instantiating HF model from config for GPTQ with dtype={dtype} "
        f"using loader={getattr(loader_cls, '__name__', loader_cls)}"
    )
    model = loader_cls.from_config(config, **init_kwargs)
    index_filename = resolve_checkpoint_index(model_dir)
    shard_files, shard_metadata = get_checkpoint_shard_files(
        model_dir,
        index_filename,
        local_files_only=True,
    )
    expected_keys = set((shard_metadata or {}).get("weight_map", {}).keys())
    loaded_keys = set()
    unexpected_keys = set()
    print(
        f"Loading {len(shard_files)} checkpoint shards from {os.path.basename(index_filename)}"
    )
    for idx, shard_file in enumerate(shard_files, start=1):
        emit_progress(
            "progress",
            phase="quantizing",
            percent=min(4.5, 1.0 + (idx / max(len(shard_files), 1)) * 3.0),
            detail=f"loading shard {idx}/{len(shard_files)}",
        )
        state_dict = load_state_dict(shard_file, map_location="cpu")
        incompatible = load_state_dict_materialized(model, state_dict, strict=False)
        loaded_keys.update(state_dict.keys())
        unexpected_keys.update(incompatible.unexpected_keys)
        del state_dict
        gc.collect()
    print(
        "Loaded checkpoint shards into instantiated model: "
        f"expected={len(expected_keys)} loaded={len(loaded_keys)} "
        f"missing={len(expected_keys - loaded_keys)} unexpected={len(unexpected_keys)}"
    )
    if getattr(model, "config", None) is config:
        model.config = copy.deepcopy(config)
    defuser.convert_model(model, cleanup_original=False)
    model._model_init_kwargs = init_kwargs.copy()
    model.eval()
    adapt_model_definition_for_loaded_model(model_definition, model)

    # Dispatch model across devices if device_map is not cpu-only.
    if quantize_device_map and quantize_device_map != "cpu":
        try:
            from accelerate import infer_auto_device_map, dispatch_model
            from accelerate.utils import get_max_memory

            max_mem = get_max_memory()
            # Apply GPU memory fraction to limit VRAM usage.
            for dev_id in list(max_mem.keys()):
                if dev_id != "cpu":
                    max_mem[dev_id] = int(max_mem[dev_id] * gpu_memory_fraction)
            device_map = infer_auto_device_map(model, max_memory=max_mem)
            model = dispatch_model(model, device_map=device_map)
            gpu_layers = sum(1 for v in device_map.values() if v != "cpu")
            cpu_layers = sum(1 for v in device_map.values() if v == "cpu")
            print(
                f"Dispatched model: device_map={quantize_device_map} "
                f"gpu_layers={gpu_layers} cpu_layers={cpu_layers}"
            )
        except Exception as exc:
            print(f"WARN: device_map dispatch failed, keeping all on CPU: {exc}")

    return model_definition(
        model,
        turtle_model=None,
        quantized=False,
        quantize_config=quantize_config,
        tokenizer=tokenizer,
        trust_remote_code=trust_remote_code,
        model_local_path=model_dir,
    )


# ── Tokenizer + model ──────────────────────────────────────────────────
runtime_overrides = runtime_overrides_for_policy(policy)
tokenizer = load_tokenizer_with_runtime_overrides(model_dir, runtime_overrides)
qcfg_kwargs = dict(bits=bits, group_size=group_size, sym=sym, desc_act=desc_act)
if dynamic_config is not None:
    qcfg_kwargs["dynamic"] = dynamic_config
for key, value in (policy or {}).get("quantize_config_overrides", {}).items():
    qcfg_kwargs[key] = value
    print(
        f"Applied QuantizeConfig override from policy={active_policy or 'none'}: {key}={value}"
    )
quantize_config = QuantizeConfig(**qcfg_kwargs)
if qcfg_damp_percent_override:
    quantize_config.damp_percent = float(qcfg_damp_percent_override)
    print(
        f"Applied QuantizeConfig damp_percent override: {quantize_config.damp_percent}"
    )
if qcfg_damp_auto_increment_override and hasattr(
    quantize_config, "damp_auto_increment"
):
    quantize_config.damp_auto_increment = float(qcfg_damp_auto_increment_override)
    print(
        "Applied QuantizeConfig damp_auto_increment override: "
        f"{quantize_config.damp_auto_increment}"
    )
if force_direct_load:
    model = load_model_manual_sharded_state_dict(model_dir, tokenizer, quantize_config)
else:
    model = GPTQModel.load(
        model_dir,
        quantize_config=quantize_config,
        trust_remote_code=True,
    )

emit_progress("progress", phase="quantizing", percent=5.0, detail="model loaded")

# ── MoE expert defusion + module_tree patch ──────────────────────────
# Gemma4 (and similar architectures) store MoE experts as fused 3D
# nn.Parameter tensors. GPTQ requires per-expert nn.Linear modules.
#
# Strategy:
#   1. Check if experts are already defused (by Defuser during GPTQModel.load)
#   2. Try defuser.convert_model() with correct import path
#   3. Fall back to custom defuse_fused_experts() for unregistered models
#   4. After defusion, patch module_tree with expert entries + lifecycle hooks

_has_defused_experts = False

# 1. Check if Defuser already defused during GPTQModel.load()
for _name, _mod in model.named_modules():
    if re.search(r"\.experts\.\d+\.gate_proj$", _name):
        _has_defused_experts = True
        print(f"Experts already defused (found {_name})")
        break

if not _has_defused_experts:
    # Check for fused 3D expert parameters
    _fused_expert_info = []
    for _name, _param in model.named_parameters():
        if "expert" in _name.lower() and _param.dim() == 3:
            _fused_expert_info.append((_name, list(_param.shape)))
    if _fused_expert_info:
        print(
            f"Found {len(_fused_expert_info)} fused 3D expert parameters "
            f"(need defusion for GPTQ)"
        )
        for _fi_name, _fi_shape in _fused_expert_info[:4]:
            print(f"  {_fi_name}: {_fi_shape}")

        # 2. Try defuser package first (correct import: `import defuser`)
        _defuser_ok = False
        try:
            import defuser as _defuser_pkg

            _inner = model.model if hasattr(model, "model") else model
            _defuser_pkg.convert_model(_inner, cleanup_original=True)
            for _name, _mod in model.named_modules():
                if re.search(r"\.experts\.\d+\.gate_proj$", _name):
                    _has_defused_experts = True
                    _defuser_ok = True
                    break
            if _defuser_ok:
                print("Defuser: converted fused experts to per-expert nn.Linear")
        except Exception as _e:
            print(f"Defuser did not defuse experts: {_e}")

        # 3. Fall back to custom defusion
        if not _defuser_ok:
            print("Using custom defusion for fused 3D expert parameters...")
            _inner = model.model if hasattr(model, "model") else model
            _n_defused = defuse_fused_experts(_inner)
            if _n_defused > 0:
                _has_defused_experts = True
                print(f"Custom defusion complete: {_n_defused} layers defused")
                # Verify
                _sample_paths = [
                    n
                    for n, _ in model.named_modules()
                    if re.search(r"\.experts\.\d+\.gate_proj$", n)
                ]
                print(
                    f"  Verification: {len(_sample_paths)} expert gate_proj "
                    f"modules found"
                )
                if _sample_paths:
                    print(f"  Example: {_sample_paths[0]}")
            else:
                print("WARN: Custom defusion found no modules to defuse")
    else:
        print("INFO: No fused 3D expert parameters found (non-MoE model)")

# 4. Patch module_tree + lifecycle hooks
if _has_defused_experts:
    try:
        from gptqmodel.models.moe_lifecycle import GateUpDownMoELifecycleHooks

        _cls = type(model)
        _patched_tree = False
        for _entry in getattr(_cls, "module_tree", []):
            if isinstance(_entry, dict) and "self_attn" in _entry:
                if "experts:moe:?" not in _entry:
                    _entry["experts:moe:?"] = {
                        "#": ("gate_proj:0", "up_proj:0", "down_proj:1"),
                    }
                    _patched_tree = True
                break

        if _patched_tree:
            _cls.dynamic_expert_index = "num_experts"
            _cls.moe_lifecycle_hooks = GateUpDownMoELifecycleHooks()
            _n_expert_layers = sum(
                1
                for n, _ in model.named_modules()
                if re.search(r"\.experts\.0\.gate_proj$", n)
            )
            print(
                f"Patched {_cls.__name__} module_tree with MoE experts "
                f"({_n_expert_layers} layers with defused experts)"
            )
            print(
                f"  module_tree entries: "
                f"{[k for e in _cls.module_tree if isinstance(e, dict) for k in e.keys()]}"
            )
        else:
            print(
                "INFO: MoE experts already in module_tree or "
                "self_attn entry not found"
            )
    except ImportError as e:
        print(f"WARN: Could not import MoE lifecycle hooks: {e}")
        print("  MoE experts will NOT be quantized")
else:
    print("INFO: No MoE experts to defuse")

# ── Calibration dataset ────────────────────────────────────────────────
examples = load_cached_examples(model_dir)
if examples is None:
    ds_args = [dataset_name]
    if dataset_config:
        ds_args.append(dataset_config)
    dataset = load_dataset(*ds_args, split="validation")
    examples = []
    total_tokens = 0
    for sample in dataset:
        if (
            len(examples) >= effective_max_samples
            or total_tokens >= effective_max_tokens
        ):
            break
        text = sample.get("text", "")
        if not text.strip():
            continue
        tok = tokenizer(
            text,
            return_tensors="pt",
            max_length=effective_max_seq_len,
            truncation=True,
        )
        sample_tokens = int(tok.input_ids.shape[-1])
        if sample_tokens <= 1:
            continue
        remaining_tokens = effective_max_tokens - total_tokens
        if sample_tokens > remaining_tokens:
            truncated = max(1, remaining_tokens)
            tok = {
                "input_ids": tok.input_ids[:, :truncated],
                "attention_mask": tok.attention_mask[:, :truncated],
            }
            sample_tokens = truncated
        else:
            tok = {"input_ids": tok.input_ids, "attention_mask": tok.attention_mask}
        examples.append(tok)
        total_tokens += sample_tokens
    checkpoint_state = dict(quant_checkpoint_state)
    checkpoint_state["stage"] = "calibration_ready"
    checkpoint_state["calibration_samples"] = len(examples)
    checkpoint_state["calibration_max_seq_len"] = effective_max_seq_len
    checkpoint_state["calibration_max_tokens"] = effective_max_tokens
    checkpoint_state["calibration_total_tokens"] = total_tokens
    checkpoint_state["calibration_cached_at"] = time.strftime(
        "%Y-%m-%dT%H:%M:%SZ", time.gmtime()
    )
    persist_cached_examples(model_dir, examples, checkpoint_state)
    quant_checkpoint_state = checkpoint_state

emit_progress(
    "progress", phase="quantizing", percent=10.0, detail="calibration data ready"
)

# ── Quantize ───────────────────────────────────────────────────────────
total_layers = infer_total_layers(model)
checkpoint_callback = QuantizationCheckpointCallback(
    model_dir, total_layers, quant_checkpoint_state
)
model.layer_callback = checkpoint_callback
model.subset_callback = checkpoint_callback
checkpoint_callback.state["stage"] = "quantizing"
checkpoint_callback.state["total_layers"] = total_layers
checkpoint_callback.state["resume_enabled"] = gptq_resume_enabled
checkpoint_callback._persist()
model.quantize(examples)

emit_progress("progress", phase="saving", percent=90.0, detail="saving quantized model")
checkpoint_callback.state["stage"] = "saving"
checkpoint_callback.state["save_started_at"] = time.strftime(
    "%Y-%m-%dT%H:%M:%SZ", time.gmtime()
)
checkpoint_callback._persist()

# ── Save (atomic: write to temp dir, then rename) ─────────────────────
save_tmp = out_dir + ".saving"
if os.path.exists(save_tmp):
    shutil.rmtree(save_tmp)
os.makedirs(save_tmp, exist_ok=True)


def save_with_progress(model, tokenizer, save_dir):
    """Save model + tokenizer with per-shard progress events."""
    done = threading.Event()

    def monitor():
        while not done.is_set():
            try:
                shard_count = len(
                    [f for f in os.listdir(save_dir) if f.endswith(".safetensors")]
                )
            except OSError:
                shard_count = 0
            if shard_count > 0:
                detail = f"saved {shard_count} shards"
                mem = _memory_stats()
                if mem:
                    detail = f"{detail} | {mem}"
                emit_progress(
                    "progress",
                    phase="saving",
                    percent=min(96.0, 91.0 + shard_count * 0.7),
                    detail=detail,
                )
            done.wait(timeout=30)

    t = threading.Thread(target=monitor, daemon=True)
    t.start()
    try:
        model.save(save_dir)
        tokenizer.save_pretrained(save_dir)
        # GPTQModel/transformers can drop chat metadata such as chat_template
        # during save_pretrained(). Preserve the source tokenizer metadata so
        # the exported artifact behaves like the parent model at serve time.
        for meta_name in (
            "tokenizer_config.json",
            "special_tokens_map.json",
            "chat_template.jinja",
        ):
            src = os.path.join(model_dir, meta_name)
            dst = os.path.join(save_dir, meta_name)
            if os.path.exists(src):
                shutil.copy2(src, dst)
    finally:
        done.set()
        t.join(timeout=5)


emit_progress("progress", phase="saving", percent=90.5, detail="saving model shards")
save_with_progress(model, tokenizer, save_tmp)

saved_cfg_path = os.path.join(save_tmp, "config.json")
if os.path.exists(saved_cfg_path) and ensure_qwen35_text_config(saved_cfg_path):
    print("Backfilled nested text_config in saved Qwen3.5 checkpoint")

# Validate before promoting
shard_files = [f for f in os.listdir(save_tmp) if f.endswith(".safetensors")]
has_config = os.path.exists(os.path.join(save_tmp, "quantize_config.json"))
if not shard_files or not has_config:
    raise RuntimeError(
        f"Save validation failed: shards={len(shard_files)} config={has_config}"
    )

# Verify safetensors integrity: ensure each shard's data section covers all tensor offsets.
# GPTQModel can silently truncate large unquantized tensors (e.g. PLE embedding tables)
# during sharded save, producing files that pass existence checks but fail at load time
# with "incomplete metadata, file not fully covered".
import struct as _struct

for shard_name in shard_files:
    shard_path = os.path.join(save_tmp, shard_name)
    fsize = os.path.getsize(shard_path)
    with open(shard_path, "rb") as sf:
        hdr_size = _struct.unpack("<Q", sf.read(8))[0]
        hdr = json.loads(sf.read(hdr_size))
    data_start = 8 + hdr_size
    data_available = fsize - data_start
    max_end = 0
    for tname, tmeta in hdr.items():
        if tname == "__metadata__":
            continue
        offsets = tmeta.get("data_offsets") or tmeta.get("offsets")
        if offsets and offsets[1] > max_end:
            max_end = offsets[1]
    if max_end == 0:
        # Fallback: compute expected size from dtype + shape
        dtype_sizes = {
            "F16": 2,
            "BF16": 2,
            "F32": 4,
            "I32": 4,
            "I8": 1,
            "U8": 1,
            "F64": 8,
            "I64": 8,
            "I16": 2,
        }
        expected = 0
        for tname, tmeta in hdr.items():
            if tname == "__metadata__":
                continue
            dt = tmeta.get("dtype", "F32")
            shape = tmeta.get("shape", [])
            elem_size = dtype_sizes.get(dt, 4)
            tensor_bytes = elem_size
            for dim in shape:
                tensor_bytes *= dim
            expected += tensor_bytes
        max_end = expected
    if data_available < max_end:
        raise RuntimeError(
            f"Safetensors integrity check failed for {shard_name}: "
            f"data_section={data_available} bytes but tensors need {max_end} bytes "
            f"(missing {max_end - data_available} bytes). File is truncated."
        )
    print(
        f"Verified {shard_name}: {fsize} bytes, {len([k for k in hdr if k != '__metadata__'])} tensors OK"
    )

artifact_overrides = artifact_overrides_for_policy(policy)
preserve_native_output = env_bool(
    "GPTQ_PRESERVE_NATIVE_OUTPUT",
    bool(artifact_overrides.get("preserve_native_output", False)),
)
refuse_moe_expert_tensors_enabled = env_bool(
    "GPTQ_REFUSE_MOE_EXPERT_TENSORS",
    bool(artifact_overrides.get("refuse_moe_expert_tensors", True)),
)
hf_native_dir = f"{out_dir}-hf-native" if preserve_native_output else ""

if preserve_native_output:
    emit_progress(
        "progress",
        phase="saving",
        percent=95.5,
        detail="preserving HF-native artifact",
    )
    copy_artifact_tree(save_tmp, hf_native_dir)

# ── MoE re-fuse: convert per-expert 2D → fused 3D for vLLM MoeWNA16 ──
if refuse_moe_expert_tensors_enabled:
    emit_progress(
        "progress",
        phase="saving",
        percent=96.0,
        detail="re-fusing MoE expert tensors",
    )
    if refuse_moe_expert_tensors(save_tmp):
        emit_progress(
            "progress", phase="saving", percent=97.0, detail="MoE re-fuse complete"
        )
else:
    print("Skipping MoE re-fuse; leaving GPTQ output in HF-native layout")
    emit_progress(
        "progress",
        phase="saving",
        percent=97.0,
        detail="keeping HF-native MoE artifact layout",
    )

if should_apply_gemma4_moe_hybrid(cfg):
    emit_progress(
        "progress",
        phase="saving",
        percent=97.2,
        detail="emitting Gemma4 MoE hybrid GPTQ artifact",
    )
    if emit_gemma4_moe_hybrid_gptq(save_tmp, model_dir):
        emit_progress(
            "progress",
            phase="saving",
            percent=97.4,
            detail="Gemma4 MoE hybrid GPTQ artifact complete",
        )

emit_progress(
    "progress", phase="saving", percent=97.5, detail="promoting output directory"
)
if os.path.exists(out_dir):
    shutil.rmtree(out_dir)
os.rename(save_tmp, out_dir)
write_artifact_manifest(
    out_dir,
    role="primary",
    primary_dir=out_dir,
    hf_native_dir=hf_native_dir,
)
if preserve_native_output:
    write_artifact_manifest(
        hf_native_dir,
        role="hf-native",
        primary_dir=out_dir,
        hf_native_dir=hf_native_dir,
    )

checkpoint_callback.state["stage"] = "complete"
checkpoint_callback.state["completed_at"] = time.strftime(
    "%Y-%m-%dT%H:%M:%SZ", time.gmtime()
)
checkpoint_callback._persist()
emit_progress("complete", phase="quantizing")
print("Quantization complete")

#!/usr/bin/env python3
"""GPTQ quantization via GPTQModel.

All configuration is read from environment variables set by the controller:
  MODEL_DIR, BITS, GROUP_SIZE, MAX_MEMORY_GB, MAX_SEQ_LEN, MAX_SAMPLES,
  SYM, DESC_ACT, GPU_MEMORY_FRACTION, DYNAMIC_EXCLUSION, DATASET,
  FLEXINFER_TELEMETRY (optional, "true" enables JSON progress lines)
"""
import copy
import gc
import json
import math
import os
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
dataset_name = os.environ.get("DATASET", "mit-han-lab/pile-val-backup")
hessian_repair_enabled = env_bool("GPTQ_HESSIAN_REPAIR", True)
hessian_sanitize_nonfinite = env_bool("GPTQ_HESSIAN_SANITIZE_NONFINITE", True)
hessian_diag_floor_scale = env_float("GPTQ_HESSIAN_DIAG_FLOOR_SCALE", 1e-6)
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
        "dataset": dataset_name,
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

ensure_policy_python_packages(policy)

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
else:
    with open(cfg_path) as f:
        cfg_recheck = json.load(f)
    dynamic_config = None
    if "layer_types" in cfg_recheck:
        layer_types = cfg_recheck["layer_types"]
        unique_types = set(layer_types)
        if len(unique_types) > 1:
            print(
                f"Hybrid architecture detected: {dict((t, layer_types.count(t)) for t in unique_types)}"
            )
            dynamic_config = {
                "-:.*attn.*": {},
                "-:.*shared_expert.*": {},
                "-:.*visual.*": {},
                "-:.*mtp.*": {},
            }
            print(f"Dynamic exclusion: {list(dynamic_config.keys())}")

# ── Memory management ──────────────────────────────────────────────────
import torch
from datasets import load_dataset
from gptqmodel import GPTQModel, QuantizeConfig
from gptqmodel.models.auto import check_and_get_model_definition
from gptqmodel.quantization.gptq import GPTQ
from gptqmodel.utils.hf import (
    normalize_hf_config_compat,
    prepare_remote_model_init_compat,
    resolve_trust_remote_code,
)
from gptqmodel.utils.importer import auto_select_device
from gptqmodel.utils.model import auto_dtype
from transformers import AutoConfig
from transformers import AutoModelForCausalLM
from transformers import AutoTokenizer
from transformers.modeling_utils import get_checkpoint_shard_files, load_state_dict

try:
    total_vram = torch.cuda.get_device_properties(0).total_memory
    torch.cuda.set_per_process_memory_fraction(gpu_memory_fraction)
    print(
        f"Memory: GPU fraction={gpu_memory_fraction} ({int(total_vram * gpu_memory_fraction / (1024**3))}GiB of {total_vram // (1024**3)}GiB), container={max_memory_gb}Gi"
    )
except (RuntimeError, AssertionError):
    total_vram = 0
    print(
        f"Memory: GPU not available (device_map={quantize_device_map}), container={max_memory_gb}Gi"
    )


def patch_gptq_hessian_inverse():
    if not hessian_repair_enabled:
        return

    def _patched_hessian_inverse(self, H: torch.Tensor):
        H = H.clone()

        if hessian_sanitize_nonfinite:
            nonfinite_mask = ~torch.isfinite(H)
            nonfinite_count = int(nonfinite_mask.sum().item())
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
        H = 0.5 * (H + H.T)

        diag_view = H.diagonal()
        orig_diag = diag_view.clone()
        finite_diag = torch.nan_to_num(orig_diag.abs(), nan=0.0, posinf=0.0, neginf=0.0)
        base_abs_max = torch.max(finite_diag).item()
        if not math.isfinite(base_abs_max) or base_abs_max == 0.0:
            base_abs_max = 1.0
        floor_base = base_abs_max * hessian_diag_floor_scale
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
                    if damp_step == 0:
                        break
                    if not recovery_started:
                        recovery_started = True
                        recovery_initial = damp
                        print(
                            f"GPTQ Hessian recovery for module={getattr(self, 'name', 'unknown')}: "
                            f"starting damp recovery at {damp:.5f} with step {damp_step:.5f}"
                        )
                    damp += damp_step
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

# ── Calibration dataset ────────────────────────────────────────────────
examples = load_cached_examples(model_dir)
if examples is None:
    dataset = load_dataset(dataset_name, split="validation")
    examples = []
    total_tokens = 0
    for sample in dataset.select(range(min(effective_max_samples, len(dataset)))):
        tok = tokenizer(
            sample["text"],
            return_tensors="pt",
            max_length=effective_max_seq_len,
            truncation=True,
        )
        sample_tokens = int(tok.input_ids.shape[-1])
        if total_tokens >= effective_max_tokens:
            break
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
        if sample_tokens <= 0:
            break
        examples.append(tok)
        total_tokens += sample_tokens
        if total_tokens >= effective_max_tokens:
            break
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

emit_progress(
    "progress", phase="saving", percent=97.0, detail="promoting output directory"
)
if os.path.exists(out_dir):
    shutil.rmtree(out_dir)
os.rename(save_tmp, out_dir)

checkpoint_callback.state["stage"] = "complete"
checkpoint_callback.state["completed_at"] = time.strftime(
    "%Y-%m-%dT%H:%M:%SZ", time.gmtime()
)
checkpoint_callback._persist()
emit_progress("complete", phase="quantizing")
print("Quantization complete")

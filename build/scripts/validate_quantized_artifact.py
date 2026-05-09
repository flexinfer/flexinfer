#!/usr/bin/env python3
"""Offline validator for quantized model artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from collections import Counter, defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Sequence

try:
    from safetensors import safe_open as _safe_open
except Exception:  # noqa: BLE001 - optional dependency.
    _safe_open = None


LAYOUT_CHOICES = ("hf-native", "vllm-gptq", "compressed-tensors", "auto")
FAMILY_ID_RE = re.compile(r"^[a-z0-9][a-z0-9._-]*$")
LAYER_QWEIGHT_RE = re.compile(
    r"^model\.layers\.(?P<layer>\d+)\."
    r"(?P<module>"
    r"self_attn\.(?:q_proj|k_proj|v_proj|o_proj)"
    r"|mlp\.(?:gate_proj|up_proj|down_proj)"
    r"|moe\.(?:gate_up_proj|down_proj)"
    r")\.qweight$"
)


@dataclass(frozen=True)
class FamilyProfile:
    name: str
    aliases: tuple[str, ...]
    known_vllm_flat_modules: frozenset[str]
    gdn_fp_module_prefixes: tuple[str, ...] = ()
    variant_hints_required: bool = False
    # variant_hints must ALL be present in the metadata hint blob for the
    # profile to be considered. Used to disambiguate variants (e.g. 26B vs 31B)
    # whose model_type/architectures strings alone are identical.
    variant_hints: tuple[str, ...] = ()


_GEMMA4_VLLM_FLAT_MODULES = frozenset(
    (
        "moe.gate_up_proj",
        "moe.down_proj",
        "self_attn.q_proj",
        "self_attn.k_proj",
        "self_attn.v_proj",
        "self_attn.o_proj",
        "mlp.gate_proj",
        "mlp.up_proj",
        "mlp.down_proj",
    )
)

_QWEN35_VLLM_FLAT_MODULES = frozenset(
    (
        "self_attn.q_proj",
        "self_attn.k_proj",
        "self_attn.v_proj",
        "self_attn.o_proj",
        "mlp.gate_proj",
        "mlp.up_proj",
        "mlp.down_proj",
        "linear_attn.in_proj_qkv",
        "linear_attn.in_proj_qkvz",
        "linear_attn.in_proj_ba",
        "linear_attn.in_proj_z",
        "linear_attn.out_proj",
        "linear_attn.conv1d",
    )
)


FAMILY_PROFILES: dict[str, FamilyProfile] = {
    "gemma4-26b-a4b": FamilyProfile(
        name="gemma4-26b-a4b",
        aliases=(
            "gemma-4-26b-a4b",
            "gemma4-26b-a4b",
            "26b-a4b",
            "gemma4_text",
            "gemma4forcausallm",
        ),
        variant_hints=("num_hidden_layers=30",),
        known_vllm_flat_modules=_GEMMA4_VLLM_FLAT_MODULES,
    ),
    "gemma4-31b": FamilyProfile(
        name="gemma4-31b",
        aliases=(
            "gemma-4-31b",
            "gemma4-31b",
            "31b",
            "gemma4_text",
            "gemma4forcausallm",
        ),
        variant_hints=("num_hidden_layers=42",),
        known_vllm_flat_modules=_GEMMA4_VLLM_FLAT_MODULES,
    ),
    "qwen36-27b": FamilyProfile(
        name="qwen36-27b",
        aliases=(
            "qwen/qwen3.6-27b",
            "qwen3.6-27b",
            "qwen36-27b",
            "qwen3_5",
            "qwen3_5forconditionalgeneration",
            "qwen3_5_text",
        ),
        known_vllm_flat_modules=_QWEN35_VLLM_FLAT_MODULES,
        gdn_fp_module_prefixes=("linear_attn.",),
        variant_hints_required=True,
        variant_hints=("num_hidden_layers=64", "vocab_size=248320"),
    ),
}


def detect_repeated_token_runs(
    tokens: Sequence[str] | str, min_run: int = 3
) -> dict[str, Any]:
    if isinstance(tokens, str):
        sequence = tokens.split()
    else:
        sequence = [str(token) for token in tokens]
    if not sequence:
        return {"has_repetition": False, "max_run": 0, "token": None, "runs": []}

    runs: list[dict[str, Any]] = []
    current_token = sequence[0]
    current_start = 0
    current_length = 1

    for index in range(1, len(sequence)):
        if sequence[index] == current_token:
            current_length += 1
            continue
        if current_length >= min_run:
            runs.append(
                {
                    "token": current_token,
                    "start": current_start,
                    "end": index - 1,
                    "length": current_length,
                }
            )
        current_token = sequence[index]
        current_start = index
        current_length = 1

    if current_length >= min_run:
        runs.append(
            {
                "token": current_token,
                "start": current_start,
                "end": len(sequence) - 1,
                "length": current_length,
            }
        )

    max_run = max((run["length"] for run in runs), default=0)
    top_token = None
    for run in runs:
        if run["length"] == max_run:
            top_token = run["token"]
            break
    return {
        "has_repetition": bool(runs),
        "max_run": max_run,
        "token": top_token,
        "runs": runs,
    }


def _read_json(path: Path) -> tuple[dict[str, Any] | None, str | None]:
    try:
        data = json.loads(path.read_text())
    except FileNotFoundError:
        return None, f"missing file: {path.name}"
    except Exception as exc:  # noqa: BLE001 - JSON parse errors surface directly.
        return None, f"failed to parse {path.name}: {exc}"
    if not isinstance(data, dict):
        return None, f"invalid {path.name}: expected JSON object"
    return data, None


def _load_weight_map(
    artifact_path: Path, warnings: list[str], errors: list[str], checks: dict[str, Any]
) -> tuple[dict[str, str], list[str], str | None]:
    index_path = artifact_path / "model.safetensors.index.json"
    single_safetensors = artifact_path / "model.safetensors"

    weight_map: dict[str, str] = {}
    tensor_keys: list[str] = []
    mode: str | None = None

    if index_path.exists():
        mode = "index"
        index, error = _read_json(index_path)
        if error:
            errors.append(error)
            return weight_map, tensor_keys, mode
        raw_weight_map = index.get("weight_map")
        if not isinstance(raw_weight_map, dict) or not raw_weight_map:
            errors.append("model.safetensors.index.json missing non-empty weight_map")
            return weight_map, tensor_keys, mode
        bad_entries = [
            key
            for key, value in raw_weight_map.items()
            if not isinstance(key, str) or not isinstance(value, str)
        ]
        if bad_entries:
            errors.append(
                "model.safetensors.index.json weight_map contains non-string entries"
            )
            return weight_map, tensor_keys, mode
        weight_map = dict(raw_weight_map)
        tensor_keys = sorted(weight_map)
        shard_files = sorted(set(weight_map.values()))
        missing_shards = [
            name for name in shard_files if not (artifact_path / name).exists()
        ]
        if missing_shards:
            errors.append(
                "missing shard files from weight_map: " + ", ".join(missing_shards[:12])
            )
        checks["shard_files"] = shard_files
        checks["shard_file_count"] = len(shard_files)
        checks["tensor_key_count"] = len(tensor_keys)
        return weight_map, tensor_keys, mode

    if single_safetensors.exists():
        mode = "single"
        checks["shard_files"] = ["model.safetensors"]
        checks["shard_file_count"] = 1
        if _safe_open is None:
            warnings.append(
                "safetensors not installed; skipping tensor-key inspection for model.safetensors"
            )
            return weight_map, tensor_keys, mode
        try:
            with _safe_open(str(single_safetensors), framework="pt") as handle:
                tensor_keys = sorted(handle.keys())
            checks["tensor_key_count"] = len(tensor_keys)
            checks["single_file_tensor_key_source"] = "safetensors"
        except Exception as exc:  # noqa: BLE001 - keep metadata-only validation alive.
            warnings.append(f"unable to inspect model.safetensors keys: {exc}")
        return weight_map, tensor_keys, mode

    errors.append(
        "missing model.safetensors.index.json and model.safetensors; cannot validate shards"
    )
    return weight_map, tensor_keys, mode


def _infer_layout(
    artifact_path: Path,
    tensor_keys: Sequence[str],
    quantize_config: dict[str, Any] | None,
    config_qcfg: dict[str, Any] | None,
) -> tuple[str | None, str]:
    metadata_candidates = [quantize_config or {}, config_qcfg or {}]
    format_values: set[str] = set()
    method_values: set[str] = set()
    for candidate in metadata_candidates:
        if not isinstance(candidate, dict):
            continue
        format_value = candidate.get("format")
        if isinstance(format_value, str):
            format_values.add(format_value.lower())
        method_value = candidate.get("quant_method")
        if isinstance(method_value, str):
            method_values.add(method_value.lower())

    has_compressed_metadata = (
        "compressed-tensors" in format_values
        or "compressed-tensors" in method_values
        or (artifact_path / "compression_config.json").exists()
    )
    if has_compressed_metadata:
        return "compressed-tensors", "compressed-tensors metadata marker"

    has_qweight = any(key.endswith(".qweight") for key in tensor_keys)
    has_qzeros = any(key.endswith(".qzeros") for key in tensor_keys)
    has_fused_moe = any(
        ".moe.gate_up_proj." in key or ".moe.down_proj." in key for key in tensor_keys
    )
    has_dense_weights = any(key.endswith(".weight") for key in tensor_keys)

    if has_qweight and (has_fused_moe or has_qzeros):
        return "vllm-gptq", "qweight + fused-MoE/qzeros tensor patterns"
    if has_qweight:
        return "hf-native", "qweight tensors without fused-MoE markers"
    if has_dense_weights:
        return "hf-native", "dense .weight tensors"
    return None, "insufficient tensor keys to infer layout"


def _collect_family_hints(
    config: dict[str, Any] | None,
    quantize_config: dict[str, Any] | None,
    config_qcfg: dict[str, Any] | None,
) -> list[str]:
    hints: list[str] = []
    config_candidates: list[dict[str, Any]] = []
    if isinstance(config, dict):
        config_candidates.append(config)
        text_config = config.get("text_config")
        if isinstance(text_config, dict):
            config_candidates.append(text_config)
    for candidate_config in config_candidates:
        for field in ("_name_or_path", "model_type", "architectures", "torch_dtype"):
            value = candidate_config.get(field)
            if isinstance(value, str):
                hints.append(value.lower())
            elif isinstance(value, list):
                hints.extend(str(item).lower() for item in value)
        # Numeric config fields emitted as "field=N" tokens so variant_hints can
        # substring-match them. Lets us disambiguate same-architecture families
        # (e.g. gemma4-26b-a4b vs gemma4-31b) by layer count when
        # _name_or_path has been stripped from config.json.
        for numeric_field in (
            "num_hidden_layers",
            "num_experts",
            "moe_intermediate_size",
            "vocab_size",
        ):
            value = candidate_config.get(numeric_field)
            if isinstance(value, int):
                hints.append(f"{numeric_field}={value}")
        layer_types = candidate_config.get("layer_types")
        if isinstance(layer_types, list):
            counts = Counter(str(item).lower() for item in layer_types)
            hints.extend(
                f"layer_types.{layer_type}={count}"
                for layer_type, count in sorted(counts.items())
            )
    for candidate in (quantize_config, config_qcfg):
        if not isinstance(candidate, dict):
            continue
        for field in ("model_name_or_path", "base_model_name_or_path", "model_type"):
            value = candidate.get(field)
            if isinstance(value, str):
                hints.append(value.lower())
    return hints


def _detect_family(
    config: dict[str, Any] | None,
    quantize_config: dict[str, Any] | None,
    config_qcfg: dict[str, Any] | None,
) -> tuple[str | None, str]:
    hints = _collect_family_hints(config, quantize_config, config_qcfg)
    if not hints:
        return None, "no family hints found in config metadata"

    hint_blob = " ".join(hints)
    scores: list[tuple[int, str]] = []
    for profile in FAMILY_PROFILES.values():
        if profile.variant_hints_required and not all(
            hint in hint_blob for hint in profile.variant_hints
        ):
            continue
        score = sum(1 for alias in profile.aliases if alias in hint_blob)
        if score > 0:
            scores.append((score, profile.name))
    if not scores:
        return None, "no profile markers matched metadata hints"

    scores.sort(reverse=True)
    top_score = scores[0][0]
    winners = sorted(name for score, name in scores if score == top_score)
    if len(winners) == 1:
        return winners[0], f"profile markers matched metadata ({winners[0]})"

    # Alias score tie — try to disambiguate via variant_hints. A variant matches
    # only if ALL of its variant_hints are present in the hint blob.
    variant_matches = [
        name
        for name in winners
        if FAMILY_PROFILES[name].variant_hints
        and all(hint in hint_blob for hint in FAMILY_PROFILES[name].variant_hints)
    ]
    if len(variant_matches) == 1:
        return variant_matches[0], (
            f"profile aliases tied; variant hints selected {variant_matches[0]}"
        )
    return None, f"ambiguous profile markers: {', '.join(winners)}"


def _validate_modules_shape(
    modules: Any, resolved_layout: str, family: str | None
) -> tuple[bool, str, str | None]:
    if not isinstance(modules, list) or not modules:
        return False, "invalid", "modules_in_block_to_quantize must be a non-empty list"

    if all(
        isinstance(item, list) and all(isinstance(name, str) for name in item)
        for item in modules
    ):
        return True, "nested", None

    if all(isinstance(item, str) for item in modules):
        known_flat: set[str] = set()
        if family and family in FAMILY_PROFILES:
            known_flat.update(FAMILY_PROFILES[family].known_vllm_flat_modules)
        else:
            for profile in FAMILY_PROFILES.values():
                known_flat.update(profile.known_vllm_flat_modules)
        unknown = sorted(name for name in modules if name not in known_flat)
        if resolved_layout != "vllm-gptq":
            return (
                False,
                "flat",
                "flat module list is only accepted for layout=vllm-gptq",
            )
        if unknown:
            return (
                False,
                "flat",
                "flat module list contains unknown modules: " + ", ".join(unknown[:12]),
            )
        return True, "flat", None

    return (
        False,
        "invalid",
        "modules_in_block_to_quantize must be nested lists or flat strings",
    )


def _flatten_declared_modules(modules: Any) -> list[str]:
    if not isinstance(modules, list):
        return []
    if all(isinstance(item, str) for item in modules):
        return sorted(set(modules))
    flattened: set[str] = set()
    for item in modules:
        if isinstance(item, list):
            flattened.update(name for name in item if isinstance(name, str))
    return sorted(flattened)


def _quantized_module_from_key(key: str) -> str | None:
    if not key.endswith(".qweight"):
        return None
    parts = key.rsplit(".qweight", 1)[0].split(".")
    for index in range(len(parts) - 2):
        if parts[index] == "layers" and parts[index + 1].isdigit():
            module_parts = parts[index + 2 :]
            if module_parts:
                return ".".join(module_parts)
    return None


def _collect_quantized_module_counts(tensor_keys: Sequence[str]) -> dict[str, int]:
    counts: Counter[str] = Counter()
    for key in tensor_keys:
        module = _quantized_module_from_key(key)
        if module:
            counts[module] += 1
    return dict(sorted(counts.items()))


def _validate_gdn_fp_policy(
    resolved_layout: str,
    resolved_family: str,
    quantized_module_counts: dict[str, int],
) -> tuple[dict[str, Any] | None, str | None]:
    if resolved_layout != "vllm-gptq" or resolved_family not in FAMILY_PROFILES:
        return None, None

    profile = FAMILY_PROFILES[resolved_family]
    prefixes = profile.gdn_fp_module_prefixes
    if not prefixes:
        return None, None

    quantized_gdn_modules = {
        module: count
        for module, count in sorted(quantized_module_counts.items())
        if any(module.startswith(prefix) for prefix in prefixes)
    }
    non_gdn_modules = {
        module: count
        for module, count in sorted(quantized_module_counts.items())
        if module not in quantized_gdn_modules
    }
    check = {
        "enabled": True,
        "policy": "gdn-linear-attention-must-remain-fp",
        "severity": "warning",
        "fp_module_prefixes": list(prefixes),
        "quantized_gdn_modules": quantized_gdn_modules,
        "non_gdn_quantized_module_count": len(non_gdn_modules),
    }
    if not quantized_gdn_modules:
        return check, None

    modules = ", ".join(
        f"{module}={count}" for module, count in list(quantized_gdn_modules.items())[:8]
    )
    return (
        check,
        "GDN GPTQ policy warning: linear-attention modules should remain FP "
        f"for family={resolved_family}, but qweight tensors were found for {modules}",
    )


def _tensor_signature(tensor: Any) -> str:
    if hasattr(tensor, "detach"):
        tensor = tensor.detach().cpu().contiguous()
        dtype = str(tensor.dtype)
        shape = tuple(int(dim) for dim in tensor.shape)
        data = tensor.numpy()
    else:
        import numpy as np

        data = np.ascontiguousarray(tensor)
        dtype = str(data.dtype)
        shape = tuple(int(dim) for dim in data.shape)

    digest = hashlib.sha256()
    digest.update(dtype.encode())
    digest.update(str(shape).encode())
    digest.update(memoryview(data))
    return digest.hexdigest()


def _detect_repeated_layer_qweights(
    artifact_path: Path,
    weight_map: dict[str, str],
    tensor_keys: Sequence[str],
) -> tuple[dict[str, Any], str | None]:
    check: dict[str, Any] = {
        "enabled": True,
        "candidate_count": 0,
        "duplicate_groups": [],
    }

    effective_weight_map = dict(weight_map)
    if not effective_weight_map and (artifact_path / "model.safetensors").exists():
        effective_weight_map = {key: "model.safetensors" for key in tensor_keys}

    candidates: list[tuple[int, str, str, str]] = []
    for key in tensor_keys:
        match = LAYER_QWEIGHT_RE.match(key)
        if not match:
            continue
        shard = effective_weight_map.get(key)
        if not shard:
            continue
        candidates.append(
            (
                int(match.group("layer")),
                match.group("module"),
                key,
                shard,
            )
        )

    check["candidate_count"] = len(candidates)
    if len(candidates) < 2:
        return check, None
    if _safe_open is None:
        return check, "safetensors not installed; cannot run repeated tensor guard"

    by_shard: dict[str, list[tuple[int, str, str]]] = defaultdict(list)
    for layer, module, key, shard in candidates:
        by_shard[shard].append((layer, module, key))

    by_module_digest: dict[tuple[str, str], list[dict[str, Any]]] = defaultdict(list)
    for shard, entries in sorted(by_shard.items()):
        shard_path = artifact_path / shard
        try:
            with _safe_open(str(shard_path), framework="pt") as handle:
                for layer, module, key in entries:
                    signature = _tensor_signature(handle.get_tensor(key))
                    by_module_digest[(module, signature)].append(
                        {"layer": layer, "tensor": key, "shard": shard}
                    )
        except Exception as exc:  # noqa: BLE001 - report exact shard/key failure.
            return check, f"repeated tensor guard failed while reading {shard}: {exc}"

    duplicate_groups: list[dict[str, Any]] = []
    for (module, signature), entries in sorted(by_module_digest.items()):
        unique_layers = sorted({int(entry["layer"]) for entry in entries})
        if len(unique_layers) < 2:
            continue
        duplicate_groups.append(
            {
                "module": module,
                "sha256": signature,
                "layers": unique_layers,
                "tensors": [entry["tensor"] for entry in entries[:8]],
            }
        )

    check["duplicate_groups"] = duplicate_groups[:20]
    if duplicate_groups:
        summary = "; ".join(
            f"{group['module']} layers={group['layers'][:8]}"
            for group in duplicate_groups[:6]
        )
        return (
            check,
            "repeated qweight tensors across different layers: " + summary,
        )
    return check, None


def _run_generation_probe(artifact_path: Path) -> tuple[dict[str, Any], str | None]:
    try:
        import torch
        from transformers import AutoModelForCausalLM, AutoTokenizer
    except Exception as exc:  # noqa: BLE001 - expose import details for diagnostics.
        return {
            "ok": False
        }, f"--run-generation unavailable: dependency import failed: {exc}"

    try:
        tokenizer = AutoTokenizer.from_pretrained(
            str(artifact_path), local_files_only=True, trust_remote_code=True
        )
        model = AutoModelForCausalLM.from_pretrained(
            str(artifact_path), local_files_only=True, trust_remote_code=True
        )
        encoded = tokenizer(
            "List two short words:", return_tensors="pt", add_special_tokens=True
        )
        with torch.no_grad():
            generated = model.generate(
                **encoded,
                max_new_tokens=16,
                do_sample=False,
            )
        decoded = tokenizer.decode(generated[0], skip_special_tokens=True)
        repetition = detect_repeated_token_runs(decoded.split())
        probe = {
            "ok": True,
            "output_preview": decoded[:200],
            "repetition": repetition,
        }
        return probe, None
    except Exception as exc:  # noqa: BLE001 - generation probe is optional.
        return {"ok": False}, f"--run-generation failed: {exc}"


def validate_artifact(
    artifact_path: Path,
    requested_layout: str = "auto",
    requested_family: str = "auto",
    run_generation: bool = False,
    required_quantized_modules: Sequence[str] | None = None,
    forbidden_quantized_modules: Sequence[str] | None = None,
    min_quantized_modules: int | None = None,
) -> dict[str, Any]:
    errors: list[str] = []
    warnings: list[str] = []
    checks: dict[str, Any] = {"artifact_path": str(artifact_path)}
    result = {
        "ok": False,
        "layout": requested_layout,
        "family": requested_family,
        "errors": errors,
        "warnings": warnings,
        "checks": checks,
    }

    if not artifact_path.exists():
        errors.append(f"artifact path does not exist: {artifact_path}")
        return result
    if not artifact_path.is_dir():
        errors.append(f"artifact path is not a directory: {artifact_path}")
        return result

    config_path = artifact_path / "config.json"
    config: dict[str, Any] | None = None
    config_qcfg: dict[str, Any] | None = None
    quantize_config: dict[str, Any] | None = None

    if config_path.exists():
        config, error = _read_json(config_path)
        if error:
            errors.append(error)
        elif isinstance(config, dict):
            raw_qcfg = config.get("quantization_config")
            if raw_qcfg is None:
                config_qcfg = None
            elif isinstance(raw_qcfg, dict):
                config_qcfg = raw_qcfg
            else:
                errors.append("config.json quantization_config must be an object")
    else:
        errors.append("missing file: config.json")

    quantize_path = artifact_path / "quantize_config.json"
    if quantize_path.exists():
        quantize_config, error = _read_json(quantize_path)
        if error:
            errors.append(error)

    checks["has_quantize_config_json"] = quantize_config is not None
    checks["has_config_quantization_config"] = config_qcfg is not None
    if quantize_config is None and config_qcfg is None:
        errors.append(
            "missing quantization metadata: quantize_config.json or config.quantization_config"
        )

    weight_map, tensor_keys, shard_mode = _load_weight_map(
        artifact_path, warnings, errors, checks
    )
    checks["shard_mode"] = shard_mode or "missing"
    checks["weight_map_entries"] = len(weight_map)
    if not checks.get("tensor_key_count"):
        checks["tensor_key_count"] = len(tensor_keys)

    detected_layout, layout_reason = _infer_layout(
        artifact_path, tensor_keys, quantize_config, config_qcfg
    )
    checks["layout_detection_reason"] = layout_reason
    checks["detected_layout"] = detected_layout

    if requested_layout == "auto":
        resolved_layout = detected_layout or "auto"
        if detected_layout is None:
            warnings.append("unable to infer layout automatically; using layout=auto")
    else:
        resolved_layout = requested_layout
        if detected_layout and detected_layout != requested_layout:
            errors.append(
                f"requested layout={requested_layout} but metadata suggests {detected_layout}"
            )
    result["layout"] = resolved_layout

    detected_family, family_reason = _detect_family(
        config, quantize_config, config_qcfg
    )
    checks["family_detection_reason"] = family_reason
    checks["detected_family"] = detected_family
    if requested_family == "auto":
        resolved_family = detected_family or "auto"
        if detected_family is None:
            warnings.append("unable to infer family automatically from metadata")
    else:
        resolved_family = requested_family
        if detected_family and detected_family != requested_family:
            warnings.append(
                f"requested family={requested_family} but metadata suggests {detected_family}"
            )
    result["family"] = resolved_family

    modules = None
    if isinstance(quantize_config, dict):
        modules = quantize_config.get("modules_in_block_to_quantize")
    if modules is None and isinstance(config_qcfg, dict):
        modules = config_qcfg.get("modules_in_block_to_quantize")

    if modules is None:
        errors.append("missing modules_in_block_to_quantize in quantization metadata")
    else:
        is_valid_modules, shape, module_error = _validate_modules_shape(
            modules,
            resolved_layout,
            resolved_family if resolved_family != "auto" else None,
        )
        checks["modules_in_block_to_quantize_shape"] = shape
        # Flat is the expected shape for layout=vllm-gptq; only emit a warning
        # when the shape is flat outside that layout, where _validate_modules_shape
        # has already promoted it to an error via module_error.
        if shape == "flat" and resolved_layout != "vllm-gptq":
            warnings.append(
                "modules_in_block_to_quantize uses flat string list outside vllm-gptq layout"
            )
        if not is_valid_modules and module_error:
            errors.append(module_error)

    declared_modules = _flatten_declared_modules(modules)
    quantized_module_counts = _collect_quantized_module_counts(tensor_keys)
    quantized_modules = sorted(quantized_module_counts)
    checks["declared_quantized_modules"] = declared_modules
    checks["quantized_modules"] = quantized_modules
    checks["quantized_module_counts"] = quantized_module_counts

    gdn_policy_check, gdn_policy_warning = _validate_gdn_fp_policy(
        resolved_layout, resolved_family, quantized_module_counts
    )
    if gdn_policy_check is not None:
        checks["gdn_gptq_policy"] = gdn_policy_check
    if gdn_policy_warning:
        warnings.append(gdn_policy_warning)

    if declared_modules and quantized_modules:
        missing_declared = sorted(set(declared_modules) - set(quantized_modules))
        checks["declared_modules_without_qweight"] = missing_declared
        if missing_declared:
            warnings.append(
                "declared quantized modules have no qweight tensors: "
                + ", ".join(missing_declared[:12])
            )

    required = sorted(set(required_quantized_modules or ()))
    forbidden = sorted(set(forbidden_quantized_modules or ()))
    for module in required:
        if module not in quantized_module_counts:
            errors.append(
                f"required quantized module missing qweight tensors: {module}"
            )
    for module in forbidden:
        if module in quantized_module_counts:
            errors.append(f"forbidden quantized module has qweight tensors: {module}")
    if (
        min_quantized_modules is not None
        and len(quantized_module_counts) < min_quantized_modules
    ):
        errors.append(
            f"only {len(quantized_module_counts)} quantized module families found; "
            f"want at least {min_quantized_modules}"
        )

    if resolved_family == "gemma4-31b" and resolved_layout == "vllm-gptq":
        repeated_check, repeated_error = _detect_repeated_layer_qweights(
            artifact_path, weight_map, tensor_keys
        )
        checks["repeated_tensor_guard"] = repeated_check
        if repeated_error:
            errors.append(repeated_error)

    if run_generation:
        generation_probe, generation_error = _run_generation_probe(artifact_path)
        checks["generation_probe"] = generation_probe
        if generation_error:
            errors.append(generation_error)
        elif generation_probe.get("ok"):
            repetition = generation_probe.get("repetition", {})
            if repetition.get("has_repetition") and repetition.get("max_run", 0) >= 12:
                warnings.append(
                    "generation probe produced long repeated-token run (max_run >= 12)"
                )

    result["ok"] = len(errors) == 0
    return result


def _print_text_result(result: dict[str, Any]) -> None:
    status = "PASS" if result["ok"] else "FAIL"
    print(f"{status}: layout={result['layout']} family={result['family']}")
    if result["warnings"]:
        print("warnings:")
        for warning in result["warnings"]:
            print(f"  - {warning}")
    if result["errors"]:
        print("errors:")
        for error in result["errors"]:
            print(f"  - {error}")
    print("checks:")
    for key in sorted(result["checks"]):
        print(f"  - {key}: {result['checks'][key]}")


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Validate quantized model artifact metadata and shard completeness."
    )
    parser.add_argument("--artifact-path", required=True, type=Path)
    parser.add_argument("--layout", choices=LAYOUT_CHOICES, default="auto")
    parser.add_argument(
        "--family",
        default="auto",
        help="Model family id such as auto, gemma4-26b-a4b, gemma4-31b, or a future profile id.",
    )
    parser.add_argument("--json", action="store_true", dest="as_json")
    parser.add_argument("--run-generation", action="store_true")
    parser.add_argument(
        "--require-quantized-module",
        action="append",
        default=[],
        help="Require at least one .qweight tensor for this module family; may be repeated.",
    )
    parser.add_argument(
        "--forbid-quantized-module",
        action="append",
        default=[],
        help="Fail if this module family has any .qweight tensors; may be repeated.",
    )
    parser.add_argument(
        "--min-quantized-modules",
        type=int,
        default=None,
        help="Require at least this many quantized module families.",
    )
    args = parser.parse_args(argv)
    args.family = args.family.strip().lower()
    if not FAMILY_ID_RE.match(args.family):
        parser.error(
            "--family must be auto or a lowercase family id using letters, digits, '.', '_' or '-'"
        )
    for field_name in ("require_quantized_module", "forbid_quantized_module"):
        values = getattr(args, field_name)
        cleaned = [value.strip() for value in values if value.strip()]
        setattr(args, field_name, cleaned)
    if args.min_quantized_modules is not None and args.min_quantized_modules < 0:
        parser.error("--min-quantized-modules must be >= 0")
    return args


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(list(argv) if argv is not None else sys.argv[1:])
    result = validate_artifact(
        artifact_path=args.artifact_path,
        requested_layout=args.layout,
        requested_family=args.family,
        run_generation=args.run_generation,
        required_quantized_modules=args.require_quantized_module,
        forbidden_quantized_modules=args.forbid_quantized_module,
        min_quantized_modules=args.min_quantized_modules,
    )
    if args.as_json:
        print(json.dumps(result, indent=2, sort_keys=True))
    else:
        _print_text_result(result)
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

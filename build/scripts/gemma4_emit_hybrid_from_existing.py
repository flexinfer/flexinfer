#!/usr/bin/env python3
"""Build a Gemma4 MoE hybrid GPTQ artifact from existing checkpoints.

This is a recovery/canary utility for the 26B-A4B pipeline. It avoids re-running
GPTQ calibration when we already have:

* a full GPTQ candidate directory, and
* a known-good hybrid directory containing source-precision dense weights.

The safe default restores all dense attention/MLP families from the source
artifact. A validation mode can keep dense GPTQ families for offline experiments,
but vLLM/ROCm serving should use the fallback path because dense GPTQ can pass
offline checks and still trip the runtime GPTQ kernel.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import time
from contextlib import ExitStack
from pathlib import Path

import torch
from safetensors import safe_open
from safetensors.torch import load_file, save_file


DENSE_QUANT_RE = re.compile(
    r"^model\.layers\.(\d+)\."
    r"(self_attn\.(?:q_proj|k_proj|v_proj|o_proj)|mlp\.(?:gate_proj|up_proj|down_proj))"
    r"\.(qweight|qzeros|scales|g_idx)$"
)
DENSE_WEIGHT_RE = re.compile(
    r"^model\.layers\.(\d+)\."
    r"(self_attn\.(?:q_proj|k_proj|v_proj|o_proj)|mlp\.(?:gate_proj|up_proj|down_proj))"
    r"\.weight$"
)


def indexed_safetensors(model_dir: Path) -> tuple[Path | None, dict[str, str], list[str]]:
    index_path = model_dir / "model.safetensors.index.json"
    single_path = model_dir / "model.safetensors"
    if index_path.exists():
        index = json.loads(index_path.read_text())
        weight_map = index.get("weight_map", {})
        return index_path, weight_map, sorted(set(weight_map.values()))
    if single_path.exists():
        with safe_open(str(single_path), framework="pt") as f:
            weight_map = {key: "model.safetensors" for key in f.keys()}
        return None, weight_map, ["model.safetensors"]
    return None, {}, []


def load_tensor(base_dir: Path, weight_map: dict[str, str], key: str) -> torch.Tensor:
    with safe_open(str(base_dir / weight_map[key]), framework="pt") as f:
        return f.get_tensor(key)


def dequantize_gptq_linear(
    target_dir: Path,
    weight_map: dict[str, str],
    prefix: str,
    group_size: int,
    zero_point_add: int,
) -> torch.Tensor:
    """Dequantize GPTQ linear layer to HF `.weight` layout `[out, in]`."""
    qweight = load_tensor(target_dir, weight_map, f"{prefix}.qweight").to(torch.int32)
    scales = load_tensor(target_dir, weight_map, f"{prefix}.scales").to(torch.float32)
    qzeros = None
    if f"{prefix}.qzeros" in weight_map:
        qzeros = load_tensor(target_dir, weight_map, f"{prefix}.qzeros").to(torch.int32)
    if f"{prefix}.g_idx" in weight_map:
        group_idx = load_tensor(target_dir, weight_map, f"{prefix}.g_idx").to(torch.long)
    else:
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
            zero_points[:, i::pack_factor] = (
                ((qzeros >> (4 * i)) & 0xF).to(torch.float32) + zero_point_add
            )
        unpacked = unpacked - zero_points[group_idx]
    else:
        unpacked = unpacked - 8.0

    return (unpacked * scales[group_idx]).transpose(0, 1).contiguous()


def cosine_similarity(a: torch.Tensor, b: torch.Tensor) -> float:
    a = a.to(torch.float32).flatten()
    b = b.to(torch.float32).flatten()
    return torch.nn.functional.cosine_similarity(a, b, dim=0).item()


def validate_family(
    target_dir: Path,
    source_dir: Path,
    target_weight_map: dict[str, str],
    source_weight_map: dict[str, str],
    family: str,
    prefixes: list[str],
    group_size: int,
    zero_point_add: int,
    threshold: float,
) -> bool:
    failures: list[tuple[str, str]] = []
    scores: list[float] = []
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
            dense = dequantize_gptq_linear(
                target_dir, target_weight_map, prefix, group_size, zero_point_add
            )
            source = load_tensor(source_dir, source_weight_map, source_key)
            score = cosine_similarity(dense, source)
            scores.append(score)
            if score < threshold:
                failures.append((prefix, f"cosine={score:.6f}"))
        except Exception as exc:  # noqa: BLE001 - report all tensor failures.
            failures.append((prefix, f"error={exc}"))

    if failures:
        preview = ", ".join(f"{prefix}:{why}" for prefix, why in failures[:8])
        extra = len(failures) - min(len(failures), 8)
        suffix = f" ... (+{extra} more)" if extra > 0 else ""
        if scores:
            print(
                f"REJECT {family}: min_cosine={min(scores):.6f} "
                f"threshold={threshold:.3f}; {preview}{suffix}",
                flush=True,
            )
        else:
            print(f"REJECT {family}: {preview}{suffix}", flush=True)
        return False

    print(
        f"ACCEPT {family}: layers={len(prefixes)} min_cosine={min(scores):.6f} "
        f"avg_cosine={sum(scores) / len(scores):.6f}",
        flush=True,
    )
    return True


def update_quantized_module_lists(output_dir: Path, modules: list[str]) -> None:
    for rel_path in ("quantize_config.json", "config.json"):
        path = output_dir / rel_path
        if not path.exists():
            continue
        data = json.loads(path.read_text())
        if rel_path == "quantize_config.json":
            data["modules_in_block_to_quantize"] = modules
        else:
            qcfg = data.get("quantization_config")
            if isinstance(qcfg, dict):
                qcfg["modules_in_block_to_quantize"] = modules
        path.write_text(json.dumps(data, indent=2))


def write_artifact_manifest(output_dir: Path, source_dir: Path, target_dir: Path) -> None:
    manifest = {
        "role": "primary",
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "source_dir": str(source_dir),
        "target_dir": str(target_dir),
        "output_dir": str(output_dir),
    }
    (output_dir / ".flexinfer-artifact.json").write_text(json.dumps(manifest, indent=2))


def emit_hybrid(
    target_dir: Path,
    source_dir: Path,
    output_dir: Path,
    group_size: int,
    zero_point_add: int,
    threshold: float,
    dense_gptq_policy: str,
) -> None:
    if output_dir.exists():
        shutil.rmtree(output_dir)
    tmp_dir = output_dir.with_name(output_dir.name + ".saving")
    if tmp_dir.exists():
        shutil.rmtree(tmp_dir)
    print(f"Copying candidate GPTQ artifact: {target_dir} -> {tmp_dir}", flush=True)
    shutil.copytree(target_dir, tmp_dir, symlinks=True)

    target_index_path, target_weight_map, target_shards = indexed_safetensors(tmp_dir)
    _, source_weight_map, _ = indexed_safetensors(source_dir)
    if not target_weight_map:
        raise RuntimeError(f"missing target safetensors index under {tmp_dir}")
    if not source_weight_map:
        raise RuntimeError(f"missing source safetensors index under {source_dir}")

    source_dense_by_family: dict[str, list[str]] = {}
    for key in source_weight_map:
        match = DENSE_WEIGHT_RE.match(key)
        if match:
            source_dense_by_family.setdefault(match.group(2), []).append(key)
    if not source_dense_by_family:
        raise RuntimeError(f"no dense source weights found under {source_dir}")

    quant_prefixes_by_family: dict[str, list[str]] = {}
    for key in target_weight_map:
        match = DENSE_QUANT_RE.match(key)
        if match and match.group(3) == "qweight":
            quant_prefixes_by_family.setdefault(match.group(2), []).append(
                key.rsplit(".", 1)[0]
            )

    keep_gptq_families: set[str] = set()
    fallback_families = set(source_dense_by_family)
    for family, source_keys in sorted(source_dense_by_family.items()):
        prefixes = sorted(k.rsplit(".weight", 1)[0] for k in source_keys)
        if dense_gptq_policy == "fallback":
            print(f"FALLBACK {family}: dense GPTQ disabled by policy", flush=True)
            continue
        qprefixes = quant_prefixes_by_family.get(family, [])
        if len(qprefixes) != len(prefixes):
            print(
                f"REJECT {family}: qweights={len(qprefixes)} "
                f"source_weights={len(prefixes)}",
                flush=True,
            )
            continue
        if validate_family(
            tmp_dir,
            source_dir,
            target_weight_map,
            source_weight_map,
            family,
            prefixes,
            group_size,
            zero_point_add,
            threshold,
        ):
            keep_gptq_families.add(family)
            fallback_families.discard(family)

    target_by_layer: dict[str, str] = {}
    keys_to_drop: set[str] = set()
    for key, shard_name in target_weight_map.items():
        quant_match = DENSE_QUANT_RE.match(key)
        if quant_match:
            family = quant_match.group(2)
            if family in keep_gptq_families:
                target_by_layer.setdefault(quant_match.group(1), shard_name)
                continue
            keys_to_drop.add(key)
            target_by_layer.setdefault(quant_match.group(1), shard_name)
            continue
        weight_match = DENSE_WEIGHT_RE.match(key)
        if weight_match and weight_match.group(2) in keep_gptq_families:
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

    for shard_name in target_shards:
        shard_path = tmp_dir / shard_name
        tensors = load_file(str(shard_path))
        kept = {k: v for k, v in tensors.items() if k not in keys_to_drop}
        if len(kept) != len(tensors):
            save_file(kept, str(shard_path))

    restore_by_target_shard: dict[str, list[str]] = {}
    restore_keys: list[str] = []
    for family in sorted(fallback_families):
        restore_keys.extend(sorted(source_dense_by_family[family]))
    for key in restore_keys:
        match = DENSE_WEIGHT_RE.match(key)
        if not match:
            continue
        target_shard = target_by_layer.get(match.group(1), target_shards[0])
        restore_by_target_shard.setdefault(target_shard, []).append(key)
        new_weight_map[key] = target_shard

    source_open = {}
    with ExitStack() as stack:
        for target_shard, keys in sorted(restore_by_target_shard.items()):
            target_path = tmp_dir / target_shard
            tensors = load_file(str(target_path))
            updated = dict(tensors)
            for key in keys:
                source_shard = source_weight_map[key]
                if source_shard not in source_open:
                    source_open[source_shard] = stack.enter_context(
                        safe_open(str(source_dir / source_shard), framework="pt")
                    )
                updated[key] = source_open[source_shard].get_tensor(key).to(torch.float16)
            save_file(updated, str(target_path))

    if target_index_path:
        index = json.loads(target_index_path.read_text())
        index["weight_map"] = new_weight_map
        metadata = index.setdefault("metadata", {})
        metadata["total_size"] = sum(
            os.path.getsize(tmp_dir / shard)
            for shard in sorted(set(new_weight_map.values()))
        )
        target_index_path.write_text(json.dumps(index, indent=2))

    modules = ["moe.gate_up_proj", "moe.down_proj"]
    modules.extend(sorted(keep_gptq_families))
    update_quantized_module_lists(tmp_dir, modules)
    write_artifact_manifest(tmp_dir, source_dir, target_dir)
    os.rename(tmp_dir, output_dir)
    print(
        "Hybrid export complete: "
        f"kept_gptq={sorted(keep_gptq_families)} "
        f"fallback={sorted(fallback_families)} "
        f"dropped_keys={len(keys_to_drop)} restored_weights={len(restore_keys)}",
        flush=True,
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", required=True, type=Path)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--group-size", type=int, default=128)
    parser.add_argument("--zero-point-add", type=int, default=1)
    parser.add_argument("--cosine-threshold", type=float, default=0.98)
    parser.add_argument(
        "--dense-gptq-policy",
        choices=("fallback", "validate"),
        default="fallback",
        help="Use 'validate' only for offline experiments; ROCm vLLM serving defaults to fallback.",
    )
    args = parser.parse_args()
    emit_hybrid(
        target_dir=args.target,
        source_dir=args.source,
        output_dir=args.out,
        group_size=args.group_size,
        zero_point_add=args.zero_point_add,
        threshold=args.cosine_threshold,
        dense_gptq_policy=args.dense_gptq_policy,
    )


if __name__ == "__main__":
    main()

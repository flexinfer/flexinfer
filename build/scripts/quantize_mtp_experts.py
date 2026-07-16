#!/usr/bin/env python3
"""Surgically RTN-quantize only a Qwen3.5 native MTP expert layer.

The source artifact remains immutable. A hard-linked sibling staging tree is
rewritten shard-by-shard, verified, and atomically renamed into place only after
all 256 x 3 plain expert matrices have become four fused GPTQ W4G128 tensors.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import uuid
from collections import Counter
from contextlib import ExitStack
from pathlib import Path

import numpy as np


EXPERT_WEIGHT_RE = re.compile(
    r"^(?P<prefix>(?:.*\.)?mtp\.layers\.(?P<layer>\d+)\.mlp\.experts)\."
    r"(?P<expert>\d+)\.(?P<projection>gate_proj|up_proj|down_proj)\.weight$"
)
PROJECTIONS = ("gate_proj", "up_proj", "down_proj")
MARKER_NAME = ".flexinfer-mtp-experts-gptq.json"


def quantize_symmetric_rtn(
    weight: np.ndarray, group_size: int = 128
) -> tuple[np.ndarray, np.ndarray, dict[str, float]]:
    """Return GPTQ int32 qweight, fp16 scales, and round-trip metrics."""
    if weight.ndim != 2:
        raise ValueError(f"expected [out,in] weight, got shape={weight.shape}")
    out_features, in_features = weight.shape
    if group_size <= 0 or in_features % group_size:
        raise ValueError(
            f"input width {in_features} must be divisible by group_size {group_size}"
        )
    if in_features % 8:
        raise ValueError(f"input width {in_features} must be divisible by pack factor 8")

    source = np.asarray(weight, dtype=np.float32)
    groups = source.reshape(out_features, in_features // group_size, group_size)
    scales = np.max(np.abs(groups), axis=2, keepdims=True) / 7.0
    scales = np.where(scales > 0, scales, np.ones_like(scales))
    signed = np.clip(np.rint(groups / scales), -8, 7).astype(np.int32)
    unsigned = (signed + 8).reshape(out_features, in_features).T

    packed = np.zeros((in_features // 8, out_features), dtype=np.uint32)
    for nibble in range(8):
        packed |= unsigned[nibble::8].astype(np.uint32) << np.uint32(4 * nibble)
    qweight = packed.view(np.int32)
    scale_matrix = scales.squeeze(2).T.astype(np.float16)

    restored = dequantize_symmetric_rtn(qweight, scale_matrix, group_size)
    delta = restored.astype(np.float32) - source
    mean_abs_weight = float(np.mean(np.abs(source)))
    mean_abs_error = float(np.mean(np.abs(delta)))
    source_flat = source.reshape(-1).astype(np.float64)
    restored_flat = restored.reshape(-1).astype(np.float64)
    denominator = float(np.linalg.norm(source_flat) * np.linalg.norm(restored_flat))
    cosine = float(np.dot(source_flat, restored_flat) / denominator) if denominator else 1.0
    return qweight, scale_matrix, {
        "elements": float(source.size),
        "mean_abs_weight": mean_abs_weight,
        "mean_abs_error": mean_abs_error,
        "relative_l1_error": mean_abs_error / mean_abs_weight if mean_abs_weight else 0.0,
        "cosine_similarity": cosine,
    }


def dequantize_symmetric_rtn(
    qweight: np.ndarray, scales: np.ndarray, group_size: int
) -> np.ndarray:
    """Decode the symmetric GPTQ layout used by vLLM's WNA16 path."""
    packed = np.asarray(qweight, dtype=np.int32).view(np.uint32)
    in_features = packed.shape[0] * 8
    out_features = packed.shape[1]
    if scales.shape != (in_features // group_size, out_features):
        raise ValueError(
            f"scale shape {scales.shape} does not match packed weight {packed.shape}"
        )
    unsigned = np.empty((in_features, out_features), dtype=np.int16)
    for nibble in range(8):
        unsigned[nibble::8] = ((packed >> np.uint32(4 * nibble)) & 0xF).astype(
            np.int16
        )
    group_index = np.arange(in_features) // group_size
    return ((unsigned - 8).astype(np.float32) * scales[group_index].astype(np.float32)).T


def discover_expert_weights(
    weight_map: dict[str, str], expected_experts: int
) -> tuple[str, dict[tuple[int, str], str]]:
    matches: dict[tuple[int, str], str] = {}
    prefixes: set[str] = set()
    layers: set[int] = set()
    for key in weight_map:
        match = EXPERT_WEIGHT_RE.match(key)
        if not match:
            continue
        expert = int(match.group("expert"))
        projection = match.group("projection")
        pair = (expert, projection)
        if pair in matches:
            raise ValueError(f"duplicate MTP expert matrix: {pair}")
        matches[pair] = key
        prefixes.add(match.group("prefix"))
        layers.add(int(match.group("layer")))

    if len(prefixes) != 1 or len(layers) != 1:
        raise ValueError(
            f"expected exactly one MTP expert layer, prefixes={sorted(prefixes)}, "
            f"layers={sorted(layers)}"
        )
    expected = {
        (expert, projection)
        for expert in range(expected_experts)
        for projection in PROJECTIONS
    }
    missing = sorted(expected - set(matches))
    extra = sorted(set(matches) - expected)
    if missing or extra:
        raise ValueError(
            f"MTP expert matrix contract mismatch: missing={missing[:8]} "
            f"extra={extra[:8]} count={len(matches)}/{len(expected)}"
        )
    return next(iter(prefixes)), matches


def fuse_expert_tensors(
    tensors: dict[tuple[int, str], tuple[np.ndarray, np.ndarray]],
    expected_experts: int,
) -> dict[str, np.ndarray]:
    gate_up_qweight = []
    gate_up_scales = []
    down_qweight = []
    down_scales = []
    for expert in range(expected_experts):
        gate_q, gate_s = tensors[(expert, "gate_proj")]
        up_q, up_s = tensors[(expert, "up_proj")]
        down_q, down_s = tensors[(expert, "down_proj")]
        gate_up_qweight.append(np.concatenate((gate_q, up_q), axis=0))
        gate_up_scales.append(np.concatenate((gate_s, up_s), axis=0))
        down_qweight.append(down_q)
        down_scales.append(down_s)
    return {
        "gate_up_proj.qweight": np.stack(gate_up_qweight),
        "gate_up_proj.scales": np.stack(gate_up_scales),
        "down_proj.qweight": np.stack(down_qweight),
        "down_proj.scales": np.stack(down_scales),
    }


def _atomic_json(path: Path, value: dict) -> None:
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    os.replace(temporary, path)


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _aggregate_metrics(metrics: list[dict[str, float]]) -> dict[str, float]:
    elements = sum(item["elements"] for item in metrics)
    if not elements:
        raise ValueError("no quantization metrics were collected")
    mean_abs_weight = sum(
        item["mean_abs_weight"] * item["elements"] for item in metrics
    ) / elements
    mean_abs_error = sum(
        item["mean_abs_error"] * item["elements"] for item in metrics
    ) / elements
    return {
        "elements": int(elements),
        "mean_abs_weight": mean_abs_weight,
        "mean_abs_error": mean_abs_error,
        "relative_l1_error": mean_abs_error / mean_abs_weight,
        "minimum_matrix_cosine": min(item["cosine_similarity"] for item in metrics),
    }


def run(args: argparse.Namespace) -> dict:
    import torch
    from safetensors import safe_open
    from safetensors.torch import save_file

    source = args.source.resolve()
    output = args.output.resolve()
    if not source.is_dir():
        raise ValueError(f"source artifact does not exist: {source}")
    if output.exists():
        raise ValueError(f"output artifact already exists: {output}")
    if source.parent != output.parent:
        raise ValueError("source and output must be siblings for atomic NFS publication")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", args.source_artifact_digest):
        raise ValueError("source artifact digest must be a lowercase sha256 digest")

    index_path = source / "model.safetensors.index.json"
    quantize_path = source / "quantize_config.json"
    if not index_path.is_file() or not quantize_path.is_file():
        raise ValueError("source artifact is missing index or quantize_config.json")
    source_index_sha = _sha256(index_path)
    source_quantize_sha = _sha256(quantize_path)
    index = json.loads(index_path.read_text())
    weight_map = index.get("weight_map") or {}
    prefix, expert_keys = discover_expert_weights(weight_map, args.expected_experts)

    source_shards = [weight_map[key] for key in expert_keys.values()]
    destination_shard = Counter(source_shards).most_common(1)[0][0]
    removed_keys = set(expert_keys.values())
    touched_shards = set(source_shards) | {destination_shard}
    source_bytes = 0
    quantized: dict[tuple[int, str], tuple[np.ndarray, np.ndarray]] = {}
    metrics = []

    handles = {}
    with ExitStack() as stack:
        for shard in sorted(set(source_shards)):
            handles[shard] = stack.enter_context(
                safe_open(source / shard, framework="pt", device="cpu")
            )
        for expert in range(args.expected_experts):
            for projection in PROJECTIONS:
                key = expert_keys[(expert, projection)]
                tensor = handles[weight_map[key]].get_tensor(key)
                source_bytes += tensor.numel() * tensor.element_size()
                qweight, scales, quality = quantize_symmetric_rtn(
                    tensor.float().numpy(), args.group_size
                )
                quantized[(expert, projection)] = (qweight, scales)
                metrics.append(quality)

    fused = fuse_expert_tensors(quantized, args.expected_experts)
    del quantized
    output_tensors = {f"{prefix}.{suffix}": value for suffix, value in fused.items()}
    quantized_bytes = sum(value.nbytes for value in output_tensors.values())
    bytes_freed = source_bytes - quantized_bytes
    if bytes_freed < args.min_bytes_freed:
        raise ValueError(
            f"expert quantization frees {bytes_freed} bytes; "
            f"at least {args.min_bytes_freed} are required"
        )
    quality = _aggregate_metrics(metrics)
    if quality["relative_l1_error"] > args.max_relative_l1:
        raise ValueError(
            f"RTN relative L1 {quality['relative_l1_error']:.6f} exceeds "
            f"{args.max_relative_l1:.6f}"
        )
    if quality["minimum_matrix_cosine"] < args.min_matrix_cosine:
        raise ValueError(
            f"RTN minimum cosine {quality['minimum_matrix_cosine']:.6f} below "
            f"{args.min_matrix_cosine:.6f}"
        )

    staging = output.with_name(f".{output.name}.staging-{uuid.uuid4().hex}")
    try:
        shutil.copytree(source, staging, copy_function=os.link)
        for shard in sorted(touched_shards):
            shard_path = staging / shard
            with safe_open(shard_path, framework="pt", device="cpu") as handle:
                rewritten = {
                    key: handle.get_tensor(key)
                    for key in handle.keys()
                    if key not in removed_keys
                }
            if shard == destination_shard:
                rewritten.update(
                    {key: torch.from_numpy(value) for key, value in output_tensors.items()}
                )
            temporary = shard_path.with_name(f".{shard_path.name}.rewrite.tmp")
            save_file(rewritten, temporary)
            os.replace(temporary, shard_path)
            del rewritten

        new_weight_map = {
            key: shard for key, shard in weight_map.items() if key not in removed_keys
        }
        new_weight_map.update({key: destination_shard for key in output_tensors})
        index["weight_map"] = new_weight_map
        index.setdefault("metadata", {})["total_size"] = sum(
            (staging / shard).stat().st_size for shard in set(new_weight_map.values())
        )
        _atomic_json(staging / index_path.name, index)

        quantize_config = json.loads((staging / quantize_path.name).read_text())
        quantize_config["flexinfer_mtp_expert_quantization"] = {
            "format": "gptq",
            "bits": 4,
            "group_size": args.group_size,
            "sym": True,
            "desc_act": False,
            "algorithm": "symmetric-rtn",
            "prefix": prefix,
        }
        _atomic_json(staging / quantize_path.name, quantize_config)

        marker = {
            "schema_version": 1,
            "source_artifact_digest": args.source_artifact_digest,
            "source_index_sha256": source_index_sha,
            "source_quantize_config_sha256": source_quantize_sha,
            "expert_count": args.expected_experts,
            "removed_plain_expert_tensor_count": len(removed_keys),
            "output_quantized_tensor_count": len(output_tensors),
            "output_quantized_keys": sorted(output_tensors),
            "output_shapes": {
                key: list(value.shape) for key, value in sorted(output_tensors.items())
            },
            "group_size": args.group_size,
            "source_expert_bytes": source_bytes,
            "quantized_expert_bytes": quantized_bytes,
            "bytes_freed": bytes_freed,
            "compression_ratio": source_bytes / quantized_bytes,
            "quality": quality,
        }
        fingerprint_payload = json.dumps(marker, sort_keys=True).encode()
        marker["contract_digest"] = "sha256:" + hashlib.sha256(
            fingerprint_payload
        ).hexdigest()
        _atomic_json(staging / MARKER_NAME, marker)

        published_index = json.loads((staging / index_path.name).read_text())[
            "weight_map"
        ]
        if removed_keys & set(published_index):
            raise ValueError("plain MTP expert tensors survived the staging rewrite")
        if set(output_tensors) - set(published_index):
            raise ValueError("quantized MTP expert tensors are missing from staged index")
        if _sha256(index_path) != source_index_sha or _sha256(quantize_path) != source_quantize_sha:
            raise RuntimeError("source artifact metadata changed during staging")
        os.replace(staging, output)
        print("MTP_EXPERT_QUANTIZATION PASS " + json.dumps(marker, sort_keys=True))
        return marker
    except Exception:
        shutil.rmtree(staging, ignore_errors=True)
        raise


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--source-artifact-digest", required=True)
    parser.add_argument("--expected-experts", type=int, default=256)
    parser.add_argument("--group-size", type=int, default=128)
    parser.add_argument("--min-bytes-freed", type=int, default=1_138_166_333)
    parser.add_argument("--max-relative-l1", type=float, default=0.18)
    parser.add_argument("--min-matrix-cosine", type=float, default=0.97)
    return parser.parse_args()


if __name__ == "__main__":
    run(parse_args())

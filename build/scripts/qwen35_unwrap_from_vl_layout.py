#!/usr/bin/env python3
"""Unwrap a GPTQ-quantized Qwen3-VL-layout checkpoint back into the flat causal-LM layout.

This is the post-quantization counterpart to ``qwen35_wrap_to_vl_layout.py``. After
GPTQModel saves its quantized output against the wrapped (multimodal-shaped) layout, we
rewrite the safetensors keys back to the flat ``model.layers.*`` namespace that vLLM's
text-only Qwen3.5 loader expects.

Output policy: write the unwrapped result to a new directory by default, so the caller
can validate the new dir before swapping. With ``--in-place``, rewrite the source dir.

Streaming, idempotent. Drops any ``model.visual.*`` keys defensively (should not appear
when the wrap script used vision-strategy=none, but we strip them anyway).

Usage:
    python qwen35_unwrap_from_vl_layout.py --src DIR --dst DIR
    python qwen35_unwrap_from_vl_layout.py --src DIR --in-place
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Dict, Iterable, List, Optional, Tuple

try:
    from safetensors import safe_open
    from safetensors.torch import save_file
except ImportError:  # pragma: no cover
    sys.stderr.write("qwen35_unwrap_from_vl_layout: safetensors is required\n")
    raise

UNWRAP_MARKER_FILE = ".flexinfer-vl-unwrap-complete"
LAYOUT_VERSION = "1"
WRAPPED_PREFIXES_TO_UNWRAP: List[Tuple[str, str]] = [
    # Reverse of the wrap mapping. Longer prefixes first.
    ("model.language_model.embed_tokens.", "model.embed_tokens."),
    ("model.language_model.norm.", "model.norm."),
    ("model.language_model.rotary_emb.", "model.rotary_emb."),
    ("model.language_model.layers.", "model.layers."),
    ("model.language_model.", "model."),  # catch-all for any other lang_model.* key
]
DROP_PREFIXES = (
    "model.visual.",
    "model.vision_tower.",
    "model.audio_tower.",
    "model.multi_modal_projector.",
)
PASSTHROUGH_PREFIXES = ["lm_head."]


def emit(event: str, **fields: object) -> None:
    payload = {
        "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "component": "qwen35-vl-unwrap",
        "event": event,
    }
    payload.update(fields)
    print(json.dumps(payload), flush=True)


def is_already_unwrapped(dst: Path) -> bool:
    return (dst / UNWRAP_MARKER_FILE).exists()


def map_key(key: str) -> Optional[str]:
    """Return the unwrapped key, or None to drop the key."""

    for drop in DROP_PREFIXES:
        if key.startswith(drop):
            return None
    for prefix in PASSTHROUGH_PREFIXES:
        if key.startswith(prefix):
            return key
    for src_prefix, dst_prefix in WRAPPED_PREFIXES_TO_UNWRAP:
        if key.startswith(src_prefix):
            return dst_prefix + key[len(src_prefix) :]
    return key


def load_index(src: Path) -> Tuple[Optional[dict], List[str]]:
    index_path = src / "model.safetensors.index.json"
    single_path = src / "model.safetensors"
    if index_path.exists():
        with index_path.open("r") as fh:
            index = json.load(fh)
        shards = sorted(set(index.get("weight_map", {}).values()))
        return index, shards
    if single_path.exists():
        return None, ["model.safetensors"]
    raise FileNotFoundError(f"no safetensors found in {src}")


def rewrite_index(index: dict) -> Tuple[dict, int, int]:
    """Return (new_index, renamed_count, dropped_count)."""

    new_index = dict(index)
    new_weight_map: Dict[str, str] = {}
    renamed = 0
    dropped = 0
    for key, shard in index.get("weight_map", {}).items():
        new_key = map_key(key)
        if new_key is None:
            dropped += 1
            continue
        if new_key != key:
            renamed += 1
        new_weight_map[new_key] = shard
    new_index["weight_map"] = new_weight_map
    metadata = dict(new_index.get("metadata", {}))
    metadata.pop("_flexinfer_layout", None)
    metadata.pop("_flexinfer_layout_version", None)
    metadata["_flexinfer_unwrap_version"] = LAYOUT_VERSION
    new_index["metadata"] = metadata
    return new_index, renamed, dropped


def rewrite_shard(src_shard: Path, dst_shard: Path) -> Tuple[int, int, int]:
    """Returns (renamed_count, passthrough_count, dropped_count)."""

    tmp_path = dst_shard.with_suffix(dst_shard.suffix + ".tmp")
    tensors: Dict[str, "torch.Tensor"] = {}
    metadata: Dict[str, str] = {}
    renamed = 0
    passthrough = 0
    dropped = 0
    with safe_open(str(src_shard), framework="pt") as reader:
        meta = reader.metadata()
        if meta:
            metadata = dict(meta)
        for key in reader.keys():
            new_key = map_key(key)
            if new_key is None:
                dropped += 1
                continue
            tensors[new_key] = reader.get_tensor(key)
            if new_key == key:
                passthrough += 1
            else:
                renamed += 1
    metadata.pop("_flexinfer_layout", None)
    metadata.pop("_flexinfer_layout_version", None)
    metadata["_flexinfer_unwrap_version"] = LAYOUT_VERSION
    save_file(tensors, str(tmp_path), metadata=metadata)
    os.replace(tmp_path, dst_shard)
    return renamed, passthrough, dropped


def rewrite_config(src: Path, dst: Path) -> None:
    src_cfg = src / "config.json"
    if not src_cfg.exists():
        emit("config_missing", path=str(src_cfg))
        return
    with src_cfg.open("r") as fh:
        cfg = json.load(fh)
    cfg.pop("_flexinfer_layout", None)
    cfg.pop("_flexinfer_layout_version", None)
    # Defensive: GPTQModel may write a multimodal architecture during save. Force back.
    arches = cfg.get("architectures") or []
    if any("ConditionalGeneration" in a or "ImageTextToText" in a for a in arches):
        cfg["architectures"] = ["Qwen3_5ForCausalLM"]
    # Strip vision_config / text_config wrapping if present (we want flat causal-LM).
    if "text_config" in cfg and isinstance(cfg["text_config"], dict):
        # Promote text_config fields to top level if not already there.
        for k, v in cfg["text_config"].items():
            cfg.setdefault(k, v)
        cfg.pop("text_config", None)
    cfg.pop("vision_config", None)
    if "model_type" in cfg and cfg["model_type"].startswith("qwen3_vl"):
        cfg["model_type"] = "qwen3_5_text"
    with (dst / "config.json").open("w") as fh:
        json.dump(cfg, fh, indent=2)


def rewrite_quantize_config(src: Path, dst: Path) -> None:
    """Rewrite GPTQModel's quantize_config.json so per-module references match flat layout."""

    src_qc = src / "quantize_config.json"
    if not src_qc.exists():
        return
    with src_qc.open("r") as fh:
        qc = json.load(fh)

    def _rewrite_str(s: str) -> str:
        new = s
        for src_prefix, dst_prefix in WRAPPED_PREFIXES_TO_UNWRAP:
            if src_prefix in new:
                new = new.replace(src_prefix, dst_prefix)
        return new

    def _rewrite_obj(obj: object) -> object:
        if isinstance(obj, str):
            return _rewrite_str(obj)
        if isinstance(obj, list):
            return [_rewrite_obj(x) for x in obj]
        if isinstance(obj, dict):
            # Both keys and values may carry wrapped prefixes (e.g. dynamic regex
            # exclusion patterns are stored as dict keys in some GPTQModel versions).
            return {
                _rewrite_str(k) if isinstance(k, str) else k: _rewrite_obj(v)
                for k, v in obj.items()
            }
        return obj

    new_qc = _rewrite_obj(qc)
    with (dst / "quantize_config.json").open("w") as fh:
        json.dump(new_qc, fh, indent=2)


def copy_aux_files(src: Path, dst: Path) -> List[str]:
    copied: List[str] = []
    for name in os.listdir(src):
        if name.endswith(".safetensors"):
            continue
        if name in (
            "model.safetensors.index.json",
            "config.json",
            "quantize_config.json",
        ):
            continue
        if name.startswith("."):
            continue
        if name == UNWRAP_MARKER_FILE:
            continue
        sp = src / name
        if not sp.is_file():
            continue
        dp = dst / name
        shutil.copy2(sp, dp)
        copied.append(name)
    return copied


def unwrap(src: Path, dst: Path, in_place: bool) -> int:
    if not src.exists():
        emit("source_missing", src=str(src))
        return 3

    if in_place:
        dst = src
    dst.mkdir(parents=True, exist_ok=True)

    if is_already_unwrapped(dst) and not in_place:
        emit("idempotent_skip", dst=str(dst), marker=UNWRAP_MARKER_FILE)
        return 0

    emit("unwrap_start", src=str(src), dst=str(dst), in_place=in_place)

    index, shards = load_index(src)
    emit(
        "index_loaded",
        shards=len(shards),
        keys=(len(index["weight_map"]) if index is not None else "single-file"),
    )

    total_renamed = 0
    total_passthrough = 0
    total_dropped = 0
    for shard_name in shards:
        sp = src / shard_name
        if in_place:
            # Stream-rewrite in place via temp file.
            renamed, passthrough, dropped = rewrite_shard(sp, sp)
        else:
            dp = dst / shard_name
            renamed, passthrough, dropped = rewrite_shard(sp, dp)
        total_renamed += renamed
        total_passthrough += passthrough
        total_dropped += dropped
        emit(
            "shard_complete",
            shard=shard_name,
            renamed=renamed,
            passthrough=passthrough,
            dropped=dropped,
        )

    if index is not None:
        new_index, idx_renamed, idx_dropped = rewrite_index(index)
        target_index = dst / "model.safetensors.index.json"
        with target_index.open("w") as fh:
            json.dump(new_index, fh, indent=2)
        emit(
            "index_written",
            keys=len(new_index["weight_map"]),
            renamed=idx_renamed,
            dropped=idx_dropped,
            dst=str(target_index),
        )

    rewrite_config(src, dst)
    rewrite_quantize_config(src, dst)
    if not in_place:
        aux = copy_aux_files(src, dst)
        emit("aux_copied", count=len(aux), files=",".join(aux[:8]))

    (dst / UNWRAP_MARKER_FILE).write_text(
        json.dumps(
            {
                "version": LAYOUT_VERSION,
                "src": str(src),
                "renamed": total_renamed,
                "passthrough": total_passthrough,
                "dropped": total_dropped,
                "in_place": in_place,
                "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            }
        )
        + "\n"
    )
    emit(
        "unwrap_complete",
        renamed=total_renamed,
        passthrough=total_passthrough,
        dropped=total_dropped,
        shards=len(shards),
    )
    return 0


def parse_args(argv: Iterable[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--src", required=True, help="Source (wrapped/quantized) checkpoint directory"
    )
    parser.add_argument(
        "--dst",
        default=None,
        help="Destination unwrapped directory (mutually exclusive " "with --in-place)",
    )
    parser.add_argument(
        "--in-place", action="store_true", help="Rewrite the source directory in place"
    )
    return parser.parse_args(list(argv))


def main(argv: Optional[List[str]] = None) -> int:
    ns = parse_args(argv if argv is not None else sys.argv[1:])
    if ns.in_place and ns.dst:
        sys.stderr.write("--in-place and --dst are mutually exclusive\n")
        return 2
    if not ns.in_place and not ns.dst:
        sys.stderr.write("either --dst or --in-place is required\n")
        return 2
    src = Path(ns.src)
    dst = Path(ns.dst) if ns.dst else src
    return unwrap(src, dst, ns.in_place)


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Wrap a text-only Qwen3.5 causal-LM checkpoint into the Qwen3-VL multimodal layout.

GPTQModel's hardcoded ``Qwen3_5*QModel`` definitions target the multimodal Qwen3-VL
schema, in which the text decoder lives under ``model.language_model.*``. Our pipeline
ships text-only abliterated checkpoints with the flat causal-LM layout
(``model.layers.*``, ``model.norm``, ``model.embed_tokens`` plus top-level ``lm_head``).

This script renames safetensors keys so the wrapped layout matches what GPTQModel walks,
without modifying GPTQModel itself. The architecture name in ``config.json`` stays
``Qwen3_5ForCausalLM`` — we keep the text-only model class but expose the wrapped key
namespace it reads from.

Streaming, idempotent, no real model load. Mirrors source shard structure 1:1.

Usage:
    python qwen35_wrap_to_vl_layout.py --src DIR --dst DIR \\
        [--vision-strategy zero|real|none]

The default vision strategy is ``none``: we do not synthesize or copy any vision_tower
weights. GPTQModel's text-only flow walks ``model.language_model.layers.*`` and stops at
``model.language_model.norm``; the visual prefix is unreferenced.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Dict, Iterable, List, Optional, Tuple

try:
    from safetensors import safe_open
    from safetensors.torch import save_file
except ImportError as exc:  # pragma: no cover - the quantizer image always has it
    sys.stderr.write(
        "qwen35_wrap_to_vl_layout: safetensors is required (pip install safetensors)\n"
    )
    raise

LAYOUT_MARKER_FILE = ".flexinfer-vl-wrap-complete"
LAYOUT_VERSION = "1"
SOURCE_PREFIXES_TO_WRAP: List[Tuple[str, str]] = [
    # Order matters: longer-prefix matches must come first so we do not double-rewrite.
    ("model.embed_tokens.", "model.language_model.embed_tokens."),
    ("model.norm.", "model.language_model.norm."),
    ("model.rotary_emb.", "model.language_model.rotary_emb."),
    ("model.layers.", "model.language_model.layers."),
]
PASSTHROUGH_PREFIXES: List[str] = ["lm_head."]
VALID_VISION_STRATEGIES = ("zero", "real", "none")


def emit(event: str, **fields: object) -> None:
    """Emit one JSON-line event to stdout for log scrapers (Loki, Promtail)."""

    payload = {
        "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "component": "qwen35-vl-wrap",
        "event": event,
    }
    payload.update(fields)
    print(json.dumps(payload), flush=True)


def is_already_wrapped(dst: Path) -> bool:
    """A wrap is idempotent: if the marker exists, do nothing."""

    return (dst / LAYOUT_MARKER_FILE).exists()


def map_key(key: str) -> Optional[str]:
    """Return the wrapped key, or None to drop the key."""

    for prefix in PASSTHROUGH_PREFIXES:
        if key.startswith(prefix):
            return key
    for src_prefix, dst_prefix in SOURCE_PREFIXES_TO_WRAP:
        if key.startswith(src_prefix):
            return dst_prefix + key[len(src_prefix) :]
    # Unknown top-level key. Pass through verbatim — let GPTQModel decide.
    return key


def load_index(src: Path) -> Tuple[Optional[dict], List[str]]:
    """Return (index_dict_or_None, list_of_shard_filenames)."""

    index_path = src / "model.safetensors.index.json"
    single_path = src / "model.safetensors"
    if index_path.exists():
        with index_path.open("r") as fh:
            index = json.load(fh)
        shards = sorted(set(index.get("weight_map", {}).values()))
        return index, shards
    if single_path.exists():
        return None, ["model.safetensors"]
    raise FileNotFoundError(
        f"no safetensors found in {src} (expected model.safetensors[.index.json])"
    )


def rewrite_index(index: dict) -> dict:
    """Rebuild weight_map with renamed keys."""

    new_index = dict(index)  # shallow copy
    new_weight_map: Dict[str, str] = {}
    for key, shard in index.get("weight_map", {}).items():
        new_key = map_key(key)
        if new_key is None:
            continue
        new_weight_map[new_key] = shard
    new_index["weight_map"] = new_weight_map
    metadata = dict(new_index.get("metadata", {}))
    metadata["_flexinfer_layout"] = "qwen3_5_vl_wrapped"
    metadata["_flexinfer_layout_version"] = LAYOUT_VERSION
    new_index["metadata"] = metadata
    return new_index


def rewrite_shard(src_shard: Path, dst_shard: Path) -> Tuple[int, int]:
    """Stream-rewrite one safetensors shard with renamed keys.

    Returns (renamed_count, passthrough_count). Tensors are written to a temp file
    then renamed on top of dst_shard (atomic on POSIX).
    """

    tmp_path = dst_shard.with_suffix(dst_shard.suffix + ".tmp")
    tensors: Dict[str, "torch.Tensor"] = {}
    metadata: Dict[str, str] = {}
    renamed = 0
    passthrough = 0
    with safe_open(str(src_shard), framework="pt") as reader:
        meta = reader.metadata()
        if meta:
            metadata = dict(meta)
        for key in reader.keys():
            new_key = map_key(key)
            if new_key is None:
                continue
            tensors[new_key] = reader.get_tensor(key)
            if new_key == key:
                passthrough += 1
            else:
                renamed += 1
    metadata["_flexinfer_layout"] = "qwen3_5_vl_wrapped"
    metadata["_flexinfer_layout_version"] = LAYOUT_VERSION
    save_file(tensors, str(tmp_path), metadata=metadata)
    os.replace(tmp_path, dst_shard)
    return renamed, passthrough


def rewrite_config(src: Path, dst: Path) -> None:
    """Copy and lightly annotate config.json."""

    src_cfg = src / "config.json"
    if not src_cfg.exists():
        emit("config_missing", path=str(src_cfg))
        return
    with src_cfg.open("r") as fh:
        cfg = json.load(fh)
    # Annotate with a marker so the unwrap step can detect the wrapped state.
    cfg["_flexinfer_layout"] = "qwen3_5_vl_wrapped"
    cfg["_flexinfer_layout_version"] = LAYOUT_VERSION
    # Preserve architectures and model_type as-is. The wrapped layout is only a
    # safetensors key namespace change — the architecture name still tells transformers
    # to instantiate Qwen3_5ForCausalLM (text-only). See design doc §5 strategy (c).
    with (dst / "config.json").open("w") as fh:
        json.dump(cfg, fh, indent=2)


def copy_aux_files(src: Path, dst: Path) -> List[str]:
    """Copy small non-weight files (tokenizer, generation config, etc.) verbatim."""

    copied: List[str] = []
    for name in os.listdir(src):
        if name.endswith(".safetensors"):
            continue
        if name in ("model.safetensors.index.json", "config.json"):
            continue
        if name.startswith("."):
            continue
        if name == LAYOUT_MARKER_FILE:
            continue
        sp = src / name
        if not sp.is_file():
            continue
        dp = dst / name
        # Cheap copy via shutil — these are small (KB-MB).
        import shutil

        shutil.copy2(sp, dp)
        copied.append(name)
    return copied


def wrap(src: Path, dst: Path, vision_strategy: str) -> int:
    """Wrap the source layout into the Qwen3-VL multimodal namespace.

    Returns 0 on success, non-zero on error.
    """

    if vision_strategy not in VALID_VISION_STRATEGIES:
        emit("invalid_vision_strategy", got=vision_strategy)
        return 2
    if vision_strategy != "none":
        # Strategies (a) and (b) considered and rejected in the design doc; emit a
        # warning but keep the surface so future variants can experiment.
        emit(
            "vision_strategy_warning",
            strategy=vision_strategy,
            note="design currently recommends 'none'",
        )

    if is_already_wrapped(dst):
        emit("idempotent_skip", dst=str(dst), marker=LAYOUT_MARKER_FILE)
        return 0

    if not src.exists():
        emit("source_missing", src=str(src))
        return 3

    dst.mkdir(parents=True, exist_ok=True)

    emit("wrap_start", src=str(src), dst=str(dst), strategy=vision_strategy)

    index, shards = load_index(src)
    emit(
        "index_loaded",
        shards=len(shards),
        keys=(len(index["weight_map"]) if index is not None else "single-file"),
    )

    total_renamed = 0
    total_passthrough = 0
    for shard_name in shards:
        sp = src / shard_name
        dp = dst / shard_name
        renamed, passthrough = rewrite_shard(sp, dp)
        total_renamed += renamed
        total_passthrough += passthrough
        emit(
            "shard_complete", shard=shard_name, renamed=renamed, passthrough=passthrough
        )

    if index is not None:
        new_index = rewrite_index(index)
        with (dst / "model.safetensors.index.json").open("w") as fh:
            json.dump(new_index, fh, indent=2)
        emit(
            "index_written",
            keys=len(new_index["weight_map"]),
            dst=str(dst / "model.safetensors.index.json"),
        )

    rewrite_config(src, dst)
    aux = copy_aux_files(src, dst)
    emit("aux_copied", count=len(aux), files=",".join(aux[:8]))

    (dst / LAYOUT_MARKER_FILE).write_text(
        json.dumps(
            {
                "version": LAYOUT_VERSION,
                "src": str(src),
                "renamed": total_renamed,
                "passthrough": total_passthrough,
                "vision_strategy": vision_strategy,
                "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            }
        )
        + "\n"
    )
    emit(
        "wrap_complete",
        renamed=total_renamed,
        passthrough=total_passthrough,
        shards=len(shards),
    )
    return 0


def parse_args(argv: Iterable[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--src", required=True, help="Source checkpoint directory")
    parser.add_argument("--dst", required=True, help="Destination wrapped directory")
    parser.add_argument(
        "--vision-strategy",
        choices=VALID_VISION_STRATEGIES,
        default="none",
        help="How to satisfy the vision_tower placeholder (default: none)",
    )
    return parser.parse_args(list(argv))


def main(argv: Optional[List[str]] = None) -> int:
    ns = parse_args(argv if argv is not None else sys.argv[1:])
    return wrap(Path(ns.src), Path(ns.dst), ns.vision_strategy)


if __name__ == "__main__":
    sys.exit(main())

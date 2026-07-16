#!/usr/bin/env python3
"""Atomically graft a standalone Qwen3.5 MTP head onto a GPTQ checkpoint."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import struct
import sys
import uuid
from pathlib import Path
from typing import BinaryIO


EXPECTED_MTP_KEYS = frozenset(
    {
        "fc.weight",
        "layers.0.input_layernorm.weight",
        "layers.0.mlp.down_proj.weight",
        "layers.0.mlp.gate_proj.weight",
        "layers.0.mlp.up_proj.weight",
        "layers.0.post_attention_layernorm.weight",
        "layers.0.self_attn.k_norm.weight",
        "layers.0.self_attn.k_proj.weight",
        "layers.0.self_attn.o_proj.weight",
        "layers.0.self_attn.q_norm.weight",
        "layers.0.self_attn.q_proj.weight",
        "layers.0.self_attn.v_proj.weight",
        "norm.weight",
        "pre_fc_norm_embedding.weight",
        "pre_fc_norm_hidden.weight",
    }
)
OUTPUT_SHARD = "mtp.safetensors"
MARKER_NAME = ".flexinfer-qwen35-mtp-graft.json"
MAX_HEADER_BYTES = 16 * 1024 * 1024


def read_safetensors_header(path: Path) -> tuple[dict, int]:
    """Read and validate a safetensors header without loading tensor data."""
    with path.open("rb") as handle:
        raw_length = handle.read(8)
        if len(raw_length) != 8:
            raise ValueError(f"invalid safetensors length prefix: {path}")
        header_length = struct.unpack("<Q", raw_length)[0]
        if not 0 < header_length <= MAX_HEADER_BYTES:
            raise ValueError(f"invalid safetensors header size {header_length}")
        raw_header = handle.read(header_length)
    if len(raw_header) != header_length:
        raise ValueError(f"truncated safetensors header: {path}")
    return json.loads(raw_header), 8 + header_length


def _validate_mtp_header(header: dict, path: Path, data_start: int) -> int:
    keys = {key for key in header if key != "__metadata__"}
    if keys != EXPECTED_MTP_KEYS:
        missing = sorted(EXPECTED_MTP_KEYS - keys)
        extra = sorted(keys - EXPECTED_MTP_KEYS)
        raise ValueError(
            f"MTP tensor contract mismatch: missing={missing}, extra={extra}"
        )
    ends = []
    for key in keys:
        start, end = header[key]["data_offsets"]
        if start < 0 or end <= start:
            raise ValueError(f"invalid data offsets for {key}: {start}, {end}")
        ends.append(end)
    data_bytes = max(ends)
    if path.stat().st_size != data_start + data_bytes:
        raise ValueError("MTP safetensors payload length does not match its header")
    return data_bytes


def _encode_header(header: dict) -> bytes:
    encoded = json.dumps(header, separators=(",", ":")).encode()
    return encoded + b" " * ((-len(encoded)) % 8)


def _copy_payload(source: BinaryIO, destination: BinaryIO) -> None:
    for chunk in iter(lambda: source.read(8 * 1024 * 1024), b""):
        destination.write(chunk)


def write_prefixed_mtp_shard(source: Path, destination: Path) -> int:
    """Rewrite only the safetensors header; tensor bytes stay bit-exact."""
    header, data_start = read_safetensors_header(source)
    data_bytes = _validate_mtp_header(header, source, data_start)
    metadata = dict(header.get("__metadata__") or {})
    metadata["format"] = "pt"
    prefixed = {"__metadata__": metadata}
    prefixed.update({f"mtp.{key}": header[key] for key in sorted(EXPECTED_MTP_KEYS)})
    encoded = _encode_header(prefixed)
    with source.open("rb") as source_handle, destination.open("wb") as output:
        source_handle.seek(data_start)
        output.write(struct.pack("<Q", len(encoded)))
        output.write(encoded)
        _copy_payload(source_handle, output)
    return data_bytes


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(8 * 1024 * 1024), b""):
            digest.update(chunk)
    return "sha256:" + digest.hexdigest()


def _write_json(path: Path, value: dict) -> None:
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    os.replace(temporary, path)


def _load_target_contract(source: Path) -> tuple[dict, dict, dict]:
    index_path = source / "model.safetensors.index.json"
    config_path = source / "config.json"
    quantize_path = source / "quantize_config.json"
    for path in (index_path, config_path, quantize_path):
        if not path.is_file():
            raise ValueError(f"target checkpoint is missing {path.name}")
    if (source / OUTPUT_SHARD).exists():
        raise ValueError(f"target checkpoint already contains {OUTPUT_SHARD}")
    index = json.loads(index_path.read_text())
    config = json.loads(config_path.read_text())
    quantize = json.loads(quantize_path.read_text())
    if any(key.startswith("mtp.") for key in index.get("weight_map", {})):
        raise ValueError("target checkpoint already contains MTP tensors")
    if config.get("model_type") not in {"qwen3_5_text", "qwen3_5_moe_text"}:
        raise ValueError(f"unsupported target model_type {config.get('model_type')!r}")
    return index, config, quantize


def _update_output_contract(
    staging: Path, index: dict, config: dict, quantize: dict, data_bytes: int
) -> None:
    index["weight_map"].update(
        {f"mtp.{key}": OUTPUT_SHARD for key in sorted(EXPECTED_MTP_KEYS)}
    )
    metadata = index.setdefault("metadata", {})
    metadata["total_size"] = int(metadata.get("total_size", 0)) + data_bytes
    metadata["_flexinfer_mtp_graft"] = "qwen3_5-bf16-v1"
    config["mtp_num_hidden_layers"] = 1
    config["mtp_use_dedicated_embeddings"] = False
    config["_flexinfer_mtp_graft"] = "qwen3_5-bf16-v1"
    quantize["flexinfer_mtp_graft"] = {
        "format": "bf16",
        "tensor_count": len(EXPECTED_MTP_KEYS),
    }
    _write_json(staging / "model.safetensors.index.json", index)
    _write_json(staging / "config.json", config)
    _write_json(staging / "quantize_config.json", quantize)


def _build_marker(
    source: Path,
    mtp_file: Path,
    source_artifact_digest: str,
    data_bytes: int,
) -> dict:
    marker = {
        "schema_version": 1,
        "source_artifact_digest": source_artifact_digest,
        "source_index_sha256": _sha256(source / "model.safetensors.index.json"),
        "source_quantize_config_sha256": _sha256(source / "quantize_config.json"),
        "mtp_source_sha256": _sha256(mtp_file),
        "mtp_format": "bf16",
        "mtp_tensor_count": len(EXPECTED_MTP_KEYS),
        "mtp_tensor_bytes": data_bytes,
        "output_shard": OUTPUT_SHARD,
    }
    payload = json.dumps(marker, sort_keys=True).encode()
    marker["contract_digest"] = "sha256:" + hashlib.sha256(payload).hexdigest()
    return marker


def graft(
    source: Path,
    mtp_file: Path,
    output: Path,
    *,
    source_artifact_digest: str,
) -> dict:
    """Publish a hardlink-preserving target copy with one BF16 MTP shard."""
    source, mtp_file, output = source.resolve(), mtp_file.resolve(), output.resolve()
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", source_artifact_digest):
        raise ValueError("source artifact digest must be a lowercase sha256 digest")
    if source.parent != output.parent:
        raise ValueError("source and output must be siblings for atomic publication")
    if output.exists():
        raise ValueError(f"output already exists: {output}")
    if not mtp_file.is_file():
        raise ValueError(f"MTP safetensors file does not exist: {mtp_file}")
    index, config, quantize = _load_target_contract(source)
    staging = output.with_name(f".{output.name}.staging-{uuid.uuid4().hex}")
    try:
        shutil.copytree(source, staging, copy_function=os.link)
        data_bytes = write_prefixed_mtp_shard(mtp_file, staging / OUTPUT_SHARD)
        _update_output_contract(staging, index, config, quantize, data_bytes)
        marker = _build_marker(source, mtp_file, source_artifact_digest, data_bytes)
        _write_json(staging / MARKER_NAME, marker)
        os.replace(staging, output)
    except Exception:
        shutil.rmtree(staging, ignore_errors=True)
        raise
    print("QWEN35_MTP_GRAFT PASS " + json.dumps(marker, sort_keys=True))
    return marker


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--mtp-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--source-artifact-digest", required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    graft(
        args.source,
        args.mtp_file,
        args.output,
        source_artifact_digest=args.source_artifact_digest,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())

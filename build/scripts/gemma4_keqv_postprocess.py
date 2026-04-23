#!/usr/bin/env python3
"""Post-process a Gemma4 dense GPTQ artifact to populate v_proj shards on
heterogeneous (k_eq_v) layers.

Background
----------

Gemma4-31B (and similar dense Gemma4 variants) has heterogeneous attention:
every Nth layer is a "full_attention" layer with NO ``self_attn.v_proj``
in the source weights. These layers reuse k_proj as v_proj at inference time
when ``config.attention_k_eq_v: true`` is set.

vLLM's Gemma4 model code DOES handle this — ``Gemma4DecoderLayer`` consults
``config.attention_k_eq_v`` and ``Gemma4Model._weight_iterator`` replicates
k_proj weights into both K and V slots at load time. But for GPTQ-quantized
artifacts, vLLM's ``is_layer_gptq_quantized`` quant-state check runs DURING
``QKVParallelLinear.__init__`` — BEFORE the weight iterator gets to do its
replication. The check sees q + k shards with ``qweight`` and v shards
entirely absent, classifies the layer as "some but not all shards
quantized", and raises ``ValueError`` from
``vllm/model_executor/layers/quantization/utils/gptq_utils.py``.

The fix is to make the on-disk artifact look uniformly quantized to the
quant-state check: copy every k_proj shard tensor (qweight, qzeros, scales,
g_idx, optional bias) into a v_proj-named tensor at the same shape. K and
V have identical shapes on these layers (same num_key_value_heads × head_dim
on the V output dim), so the copy is shape-compatible. Numerically v == k,
which is the same end state vLLM's k_eq_v weight iterator would have
produced, just materialized on disk instead of in memory.

What this script does
---------------------

1. Reads ``<src>/config.json`` and asserts the model is Gemma4 with
   ``attention_k_eq_v: true``. Identifies heterogeneous layers via
   ``layer_types[idx] == "full_attention"`` (the layer-type entries that
   require k_eq_v handling).
2. Reads ``<src>/model.safetensors.index.json`` and walks every shard.
3. For each heterogeneous layer, finds the k_proj.* tensors and clones
   them into v_proj.* names. Writes those clones into a single extra
   shard ``model-keqv-vproj.safetensors`` to keep the original shards
   untouched.
4. Hardlinks (falls back to copy on cross-FS) every existing shard +
   non-shard file from ``<src>/`` into ``<dst>/``, then writes a new
   ``model.safetensors.index.json`` that references the original shards
   PLUS the v_proj entries in the new shard.
5. Validates that the resulting tensor set covers q+k+v on every layer
   that the source had q+k for.

Inputs (env vars):
    SRC_DIR — source artifact dir, e.g. /cache/gptq-w4-g128
    DST_DIR — output dir (must not exist or must be empty), e.g.
              /cache/gptq-w4-g128-keqv
    DRY_RUN — if "1", inspect + log only; do not write anything

Exit codes:
    0 = success (or dry-run inspection complete)
    1 = config invalid (not Gemma4, no attention_k_eq_v, etc.)
    2 = source artifact missing required files
    3 = transform failed (shape mismatch, missing k_proj on a layer that
        needs v_proj, etc.)
    4 = validation failed after write

Reproducibility
---------------

The script is purely deterministic: copying packed INT4 tensors yields
byte-identical output across runs. No quantization re-derivation. No
floating-point math.

Author: services/flexinfer (2026-04-22)
"""

from __future__ import annotations

import json
import os
import re
import shutil
import sys
from collections import defaultdict
from pathlib import Path
from typing import Iterable

# ── Tiny utility ──────────────────────────────────────────────────────


def log(msg: str) -> None:
    print(f"[gemma4-keqv] {msg}", flush=True)


def fail(code: int, msg: str) -> None:
    log(f"FATAL: {msg}")
    sys.exit(code)


# ── Config inspection ─────────────────────────────────────────────────


def load_config(src: Path) -> dict:
    cfg_path = src / "config.json"
    if not cfg_path.exists():
        fail(2, f"missing config.json at {cfg_path}")
    with cfg_path.open() as fh:
        return json.load(fh)


def heterogeneous_layer_indices(cfg: dict) -> list[int]:
    """Layers that need v_proj duplication.

    Gemma4 marks them ``layer_type == "full_attention"`` (vs.
    ``"sliding_attention"``). Only full-attention layers reuse K as V when
    ``attention_k_eq_v`` is set, per Gemma4DecoderLayer in vLLM:

        use_k_eq_v = self.is_full_attention and getattr(
            config, "attention_k_eq_v", False
        )
    """
    layer_types = cfg.get("layer_types") or []
    if not layer_types:
        fail(1, "config.json has no layer_types — cannot identify heterogeneous layers")
    return [i for i, t in enumerate(layer_types) if t == "full_attention"]


def assert_config_supports_keqv(cfg: dict) -> None:
    model_type = cfg.get("model_type", "")
    if not model_type.startswith("gemma4"):
        fail(1, f"model_type={model_type!r}, expected gemma4* — wrong model family")
    if not cfg.get("attention_k_eq_v"):
        fail(
            1,
            "config.attention_k_eq_v is not true — vLLM won't apply k_eq_v "
            "weight routing even with v_proj present. Set it to true in "
            "config.json before running this transform, OR accept that v_proj "
            "is now genuinely present (no k_eq_v needed; vLLM will load v as "
            "regular weight). The duplicated v IS numerically equal to k, so "
            "either path produces the same forward output.",
        )
    num_kv_shared = cfg.get("num_kv_shared_layers", 0)
    if num_kv_shared:
        log(
            f"NOTE: config.num_kv_shared_layers={num_kv_shared}. The last "
            f"{num_kv_shared} layers share KV cache with earlier layers of "
            f"the same type. v_proj duplication on those layers is harmless "
            f"because the KV-sharing path takes precedence at runtime."
        )


# ── Index walking ─────────────────────────────────────────────────────


def load_index(src: Path) -> dict:
    idx_path = src / "model.safetensors.index.json"
    if not idx_path.exists():
        fail(2, f"missing index at {idx_path}")
    with idx_path.open() as fh:
        return json.load(fh)


KEYV_TENSOR_NAMES = ("qweight", "qzeros", "scales", "g_idx", "bias")
LAYER_TENSOR_RE = re.compile(
    r"^(?P<prefix>model\.layers\.(?P<idx>\d+)\.self_attn\.)"
    r"(?P<proj>k_proj|v_proj)\.(?P<suffix>[A-Za-z_0-9]+)$"
)


def collect_attention_keys(
    weight_map: dict[str, str]
) -> dict[int, dict[str, dict[str, str]]]:
    """layer_idx → { 'k_proj' | 'v_proj' : { tensor_suffix → shard_filename } }."""
    out: dict[int, dict[str, dict[str, str]]] = defaultdict(lambda: defaultdict(dict))
    for tname, shard in weight_map.items():
        m = LAYER_TENSOR_RE.match(tname)
        if not m:
            continue
        layer_idx = int(m.group("idx"))
        proj = m.group("proj")
        suffix = m.group("suffix")
        out[layer_idx][proj][suffix] = shard
    return out


def plan_duplications(
    heterogeneous: list[int],
    attn_keys: dict[int, dict[str, dict[str, str]]],
) -> list[tuple[int, str, str]]:
    """Return list of (layer_idx, suffix, src_shard) to duplicate.

    Each entry says "for this layer, take k_proj.<suffix> from <src_shard>
    and emit it as v_proj.<suffix> in the new shard."
    """
    plan: list[tuple[int, str, str]] = []
    for layer_idx in heterogeneous:
        present = attn_keys.get(layer_idx, {})
        k = present.get("k_proj", {})
        v = present.get("v_proj", {})
        if not k:
            fail(
                3,
                f"layer {layer_idx} is heterogeneous (full_attention, k_eq_v) "
                f"but has no k_proj.* tensors in the index — cannot duplicate "
                f"non-existent k into v.",
            )
        if v:
            log(
                f"layer {layer_idx}: v_proj already present ({sorted(v)}); "
                f"skipping (idempotent)."
            )
            continue
        for suffix in KEYV_TENSOR_NAMES:
            if suffix not in k:
                # bias is optional — only error on the always-required ones
                if suffix in ("qweight", "qzeros", "scales", "g_idx"):
                    log(
                        f"WARN: layer {layer_idx} k_proj missing required tensor "
                        f"{suffix!r}; skipping that suffix. The artifact may not "
                        f"load — investigate before promoting."
                    )
                continue
            plan.append((layer_idx, suffix, k[suffix]))
    return plan


# ── Tensor I/O ────────────────────────────────────────────────────────


def safetensors_lazy_open(path: Path):
    """Return a safetensors file handle for lazy tensor reads."""
    from safetensors import safe_open

    return safe_open(str(path), framework="pt")


def write_keqv_extras_shard(
    src: Path,
    dst: Path,
    plan: list[tuple[int, str, str]],
) -> tuple[str, dict[str, dict]]:
    """Write the new shard containing duplicated v_proj tensors.

    Returns:
        shard_filename (relative to dst), header_metadata for index
    """
    import torch
    from safetensors.torch import save_file

    if not plan:
        log("plan is empty — no duplications needed; skipping shard write")
        return "", {}

    # Group plan entries by source shard so we open each shard once.
    by_shard: dict[str, list[tuple[int, str]]] = defaultdict(list)
    for layer_idx, suffix, src_shard in plan:
        by_shard[src_shard].append((layer_idx, suffix))

    new_tensors: dict[str, "torch.Tensor"] = {}
    for src_shard, entries in by_shard.items():
        shard_path = src / src_shard
        if not shard_path.exists():
            fail(3, f"index references {src_shard} but file missing at {shard_path}")
        with safetensors_lazy_open(shard_path) as fh:
            for layer_idx, suffix in entries:
                k_name = f"model.layers.{layer_idx}.self_attn.k_proj.{suffix}"
                v_name = f"model.layers.{layer_idx}.self_attn.v_proj.{suffix}"
                if k_name not in fh.keys():
                    fail(
                        3,
                        f"plan said {k_name} lives in {src_shard} but the file "
                        f"doesn't contain that key.",
                    )
                tensor = fh.get_tensor(k_name)
                # Clone into a contiguous CPU tensor so save_file is happy
                new_tensors[v_name] = tensor.detach().clone().contiguous()
        log(f"  read {len(entries)} k_proj tensors from {src_shard}")

    shard_name = "model-keqv-vproj.safetensors"
    shard_path = dst / shard_name
    save_file(new_tensors, str(shard_path), metadata={"format": "pt"})
    log(
        f"wrote {len(new_tensors)} v_proj tensors → {shard_name} ({shard_path.stat().st_size:,} bytes)"
    )
    weight_map = {name: shard_name for name in new_tensors}
    return shard_name, weight_map


# ── Linking + index rewrite ───────────────────────────────────────────


def link_or_copy_files(src: Path, dst: Path, exclude: Iterable[str] = ()) -> None:
    """Hardlink every regular file under src to dst (recursive). Falls back
    to copy on cross-filesystem (EXDEV)."""
    excl = set(exclude)
    n_link = 0
    n_copy = 0
    for root, dirs, files in os.walk(src):
        rel_root = Path(root).relative_to(src)
        (dst / rel_root).mkdir(parents=True, exist_ok=True)
        for fname in files:
            if fname in excl:
                continue
            srcf = Path(root) / fname
            dstf = dst / rel_root / fname
            if dstf.exists():
                continue
            try:
                os.link(srcf, dstf)
                n_link += 1
            except OSError:
                shutil.copy2(srcf, dstf)
                n_copy += 1
    log(f"linked {n_link} files, copied {n_copy} files")


def rewrite_index(src_index: dict, extra_weight_map: dict[str, str]) -> dict:
    new_weight_map = dict(src_index.get("weight_map", {}))
    overlap = set(new_weight_map) & set(extra_weight_map)
    if overlap:
        fail(
            3,
            f"refused to overwrite existing keys: {sorted(overlap)[:5]}... "
            f"(re-run on a clean SRC, or this script is being applied twice).",
        )
    new_weight_map.update(extra_weight_map)
    new_index = dict(src_index)
    new_index["weight_map"] = new_weight_map
    # Recompute total_size if the index has it
    return new_index


# ── Validation ────────────────────────────────────────────────────────


def validate_qkv_complete(
    weight_map: dict[str, str],
    expected_layers: list[int],
) -> None:
    """For every full_attention layer, assert q_proj + k_proj + v_proj are
    each represented by qweight + qzeros + scales (the load-bearing tensors)."""
    fails: list[str] = []
    for layer_idx in expected_layers:
        for proj in ("q_proj", "k_proj", "v_proj"):
            for suffix in ("qweight", "qzeros", "scales"):
                key = f"model.layers.{layer_idx}.self_attn.{proj}.{suffix}"
                if key not in weight_map:
                    fails.append(key)
    if fails:
        fail(
            4,
            f"validation: {len(fails)} expected qkv tensors missing from new "
            f"index (first 5: {fails[:5]})",
        )
    log(
        f"validation: q+k+v complete on all {len(expected_layers)} heterogeneous layers"
    )


# ── Main ──────────────────────────────────────────────────────────────


def main() -> None:
    src = Path(os.environ.get("SRC_DIR", "")).resolve()
    dst = Path(os.environ.get("DST_DIR", "")).resolve()
    dry_run = os.environ.get("DRY_RUN", "0") == "1"

    if not src.is_dir():
        fail(2, f"SRC_DIR not a directory: {src}")
    if not dry_run:
        if dst.exists() and any(dst.iterdir()):
            fail(2, f"DST_DIR not empty: {dst} (refusing to clobber)")
        dst.mkdir(parents=True, exist_ok=True)

    log(f"src = {src}")
    log(f"dst = {dst}")
    log(f"dry_run = {dry_run}")

    # 1. Inspect config
    cfg = load_config(src)
    log(f"model_type = {cfg.get('model_type')!r}")
    log(f"num_hidden_layers = {cfg.get('num_hidden_layers')}")
    log(f"attention_k_eq_v = {cfg.get('attention_k_eq_v')!r}")
    log(f"num_kv_shared_layers = {cfg.get('num_kv_shared_layers', 0)}")
    layer_types = cfg.get("layer_types") or []
    full_count = sum(1 for t in layer_types if t == "full_attention")
    sliding_count = sum(1 for t in layer_types if t == "sliding_attention")
    log(
        f"layer_types: {len(layer_types)} total = "
        f"{full_count} full_attention + {sliding_count} sliding_attention"
    )
    assert_config_supports_keqv(cfg)
    heterogeneous = heterogeneous_layer_indices(cfg)
    log(
        f"heterogeneous layers ({len(heterogeneous)}): "
        f"{heterogeneous[:6]}{'...' if len(heterogeneous) > 6 else ''}"
    )

    # 2. Walk index
    index = load_index(src)
    weight_map = index.get("weight_map", {})
    log(f"index has {len(weight_map)} tensor entries")
    attn_keys = collect_attention_keys(weight_map)

    # Show pre-state for the first few heterogeneous layers
    for layer_idx in heterogeneous[:3]:
        present = attn_keys.get(layer_idx, {})
        log(
            f"  layer {layer_idx} attention shards: "
            f"k_proj={sorted(present.get('k_proj', {}))} "
            f"v_proj={sorted(present.get('v_proj', {}))}"
        )

    plan = plan_duplications(heterogeneous, attn_keys)
    log(
        f"duplication plan: {len(plan)} tensor copies across {len(set(p[0] for p in plan))} layers"
    )

    if dry_run:
        log("DRY_RUN=1 — exiting before any writes")
        return

    # 3. Write extras shard
    extras_shard, extras_weight_map = write_keqv_extras_shard(src, dst, plan)

    # 4. Link other files
    # Exclude the index — we rewrite it
    link_or_copy_files(src, dst, exclude={"model.safetensors.index.json"})

    # 5. Rewrite index
    new_index = rewrite_index(index, extras_weight_map)
    new_index_path = dst / "model.safetensors.index.json"
    with new_index_path.open("w") as fh:
        json.dump(new_index, fh, indent=2)
    log(f"wrote {new_index_path} ({len(new_index['weight_map'])} entries)")

    # 6. Validate
    validate_qkv_complete(new_index["weight_map"], heterogeneous)

    log("done. promote by updating Model CR `source` to point at the new dir.")


if __name__ == "__main__":
    main()

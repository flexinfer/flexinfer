"""Tests for the Qwen3.5 GPTQ layout adapter (wrap + unwrap).

Builds a synthetic 1-layer fake checkpoint (no real weights) and round-trips it through
``qwen35_wrap_to_vl_layout.py`` and ``qwen35_unwrap_from_vl_layout.py``. Validates:

1. Round-trip key equality: wrap → unwrap reproduces the source key set exactly.
2. Wrapped layout schema: matches the Qwen3-VL sibling structure (lm_head at top level,
   model.language_model.* prefix for text decoder).
3. Idempotency: re-running wrap on already-wrapped output is a no-op.
4. Vision-key dropping: synthetic ``model.visual.*`` tensors injected into the wrapped
   index are dropped on unwrap (defensive).
5. config.json round-trip: wrap annotates, unwrap strips.

No GPU. No real model. <5s on a laptop.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

# safetensors is required for both adapter scripts; if it's missing in the test env,
# the whole adapter is unusable, so a hard import error is appropriate.
import torch
from safetensors import safe_open
from safetensors.torch import save_file

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

import qwen35_unwrap_from_vl_layout as unwrap_mod  # noqa: E402
import qwen35_wrap_to_vl_layout as wrap_mod  # noqa: E402


HIDDEN = 64
NUM_LAYERS = 1
VOCAB = 256


def _t(*shape: int) -> torch.Tensor:
    return torch.randn(*shape, dtype=torch.float16)


def _build_source_checkpoint(root: Path) -> dict:
    """Create a fake 1-layer Qwen3.5 text-only checkpoint at ``root``.

    Returns the source state_dict (key -> tensor) for later equality checks.
    """

    root.mkdir(parents=True, exist_ok=True)
    state = {
        "model.embed_tokens.weight": _t(VOCAB, HIDDEN),
        "model.norm.weight": _t(HIDDEN),
        "lm_head.weight": _t(VOCAB, HIDDEN),
    }
    for i in range(NUM_LAYERS):
        prefix = f"model.layers.{i}"
        state[f"{prefix}.self_attn.q_proj.weight"] = _t(HIDDEN, HIDDEN)
        state[f"{prefix}.self_attn.k_proj.weight"] = _t(HIDDEN, HIDDEN)
        state[f"{prefix}.self_attn.v_proj.weight"] = _t(HIDDEN, HIDDEN)
        state[f"{prefix}.self_attn.o_proj.weight"] = _t(HIDDEN, HIDDEN)
        state[f"{prefix}.mlp.gate_proj.weight"] = _t(HIDDEN * 2, HIDDEN)
        state[f"{prefix}.mlp.up_proj.weight"] = _t(HIDDEN * 2, HIDDEN)
        state[f"{prefix}.mlp.down_proj.weight"] = _t(HIDDEN, HIDDEN * 2)
        state[f"{prefix}.input_layernorm.weight"] = _t(HIDDEN)
        state[f"{prefix}.post_attention_layernorm.weight"] = _t(HIDDEN)

    # Single-shard layout for simplicity. The adapter handles multi-shard via index.
    save_file(state, str(root / "model.safetensors"))

    # Sharded index variant: also write a synthetic index referencing the single shard
    # so we exercise both code paths at runtime.
    index = {
        "metadata": {
            "total_size": sum(v.numel() * v.element_size() for v in state.values())
        },
        "weight_map": {key: "model.safetensors" for key in state.keys()},
    }
    with (root / "model.safetensors.index.json").open("w") as fh:
        json.dump(index, fh, indent=2)

    cfg = {
        "architectures": ["Qwen3_5ForCausalLM"],
        "model_type": "qwen3_5_text",
        "hidden_size": HIDDEN,
        "num_hidden_layers": NUM_LAYERS,
        "vocab_size": VOCAB,
    }
    with (root / "config.json").open("w") as fh:
        json.dump(cfg, fh, indent=2)

    # Aux files that should pass through.
    (root / "tokenizer.json").write_text("{}")
    (root / "generation_config.json").write_text("{}")

    return state


def _read_state(path: Path) -> dict:
    state: dict = {}
    with safe_open(str(path / "model.safetensors"), framework="pt") as reader:
        for key in reader.keys():
            state[key] = reader.get_tensor(key)
    return state


def test_wrap_creates_vl_layout_keys(tmp_path):
    src = tmp_path / "src"
    dst = tmp_path / "wrapped"
    _build_source_checkpoint(src)

    rc = wrap_mod.wrap(src, dst, vision_strategy="none")
    assert rc == 0

    wrapped_state = _read_state(dst)
    keys = set(wrapped_state.keys())
    # Top-level passthrough preserved.
    assert "lm_head.weight" in keys
    # Decoder layers wrapped.
    for i in range(NUM_LAYERS):
        assert f"model.language_model.layers.{i}.self_attn.q_proj.weight" in keys
        assert f"model.language_model.layers.{i}.mlp.down_proj.weight" in keys
        # The unwrapped path must NOT exist anymore.
        assert f"model.layers.{i}.self_attn.q_proj.weight" not in keys
    # Embed + norm wrapped.
    assert "model.language_model.embed_tokens.weight" in keys
    assert "model.language_model.norm.weight" in keys
    assert "model.embed_tokens.weight" not in keys
    assert "model.norm.weight" not in keys

    # Index reflects the wrapped namespace.
    with (dst / "model.safetensors.index.json").open("r") as fh:
        idx = json.load(fh)
    assert "lm_head.weight" in idx["weight_map"]
    assert "model.language_model.layers.0.mlp.up_proj.weight" in idx["weight_map"]
    assert idx["metadata"]["_flexinfer_layout"] == "qwen3_5_vl_wrapped"

    # Config annotated but architecture preserved (strategy (c) — text-only class).
    with (dst / "config.json").open("r") as fh:
        cfg = json.load(fh)
    assert cfg["_flexinfer_layout"] == "qwen3_5_vl_wrapped"
    assert cfg["architectures"] == ["Qwen3_5ForCausalLM"]
    assert cfg["model_type"] == "qwen3_5_text"


def test_round_trip_keys_match_source(tmp_path):
    src = tmp_path / "src"
    wrapped = tmp_path / "wrapped"
    unwrapped = tmp_path / "unwrapped"
    source_state = _build_source_checkpoint(src)

    assert wrap_mod.wrap(src, wrapped, vision_strategy="none") == 0
    assert unwrap_mod.unwrap(wrapped, unwrapped, in_place=False) == 0

    final_state = _read_state(unwrapped)
    assert set(final_state.keys()) == set(source_state.keys()), (
        "round-trip key sets differ\n"
        f"missing: {set(source_state.keys()) - set(final_state.keys())}\n"
        f"extra:   {set(final_state.keys()) - set(source_state.keys())}"
    )
    # Tensor values preserved bit-exact.
    for key, src_t in source_state.items():
        torch.testing.assert_close(src_t, final_state[key], atol=0, rtol=0)


def test_wrap_is_idempotent(tmp_path):
    src = tmp_path / "src"
    dst = tmp_path / "wrapped"
    _build_source_checkpoint(src)
    assert wrap_mod.wrap(src, dst, vision_strategy="none") == 0

    # Snapshot file mtimes.
    snapshot = {p.name: p.stat().st_mtime_ns for p in dst.iterdir() if p.is_file()}
    # Re-run; should no-op because the marker is present.
    rc = wrap_mod.wrap(src, dst, vision_strategy="none")
    assert rc == 0
    after = {p.name: p.stat().st_mtime_ns for p in dst.iterdir() if p.is_file()}
    assert snapshot == after, "idempotent re-run should not modify any file"


def test_unwrap_drops_vision_keys(tmp_path):
    src = tmp_path / "wrapped"
    dst = tmp_path / "unwrapped"
    src.mkdir()

    # Build a fake wrapped checkpoint that includes an injected visual tensor (we want
    # to confirm the unwrap step drops it even though our wrap step does not emit it).
    wrapped_state = {
        "lm_head.weight": _t(VOCAB, HIDDEN),
        "model.language_model.embed_tokens.weight": _t(VOCAB, HIDDEN),
        "model.language_model.norm.weight": _t(HIDDEN),
        "model.language_model.layers.0.self_attn.q_proj.weight": _t(HIDDEN, HIDDEN),
        "model.visual.patch_embed.weight": _t(8, 3, 16, 16),  # synthetic vision junk
    }
    save_file(wrapped_state, str(src / "model.safetensors"))
    index = {
        "metadata": {"_flexinfer_layout": "qwen3_5_vl_wrapped"},
        "weight_map": {key: "model.safetensors" for key in wrapped_state.keys()},
    }
    with (src / "model.safetensors.index.json").open("w") as fh:
        json.dump(index, fh, indent=2)
    cfg = {
        "architectures": ["Qwen3_5ForCausalLM"],
        "model_type": "qwen3_5_text",
        "_flexinfer_layout": "qwen3_5_vl_wrapped",
    }
    with (src / "config.json").open("w") as fh:
        json.dump(cfg, fh, indent=2)

    rc = unwrap_mod.unwrap(src, dst, in_place=False)
    assert rc == 0

    final = _read_state(dst)
    assert "model.visual.patch_embed.weight" not in final
    assert "lm_head.weight" in final
    assert "model.embed_tokens.weight" in final
    assert "model.layers.0.self_attn.q_proj.weight" in final


def test_unwrap_strips_layout_marker_from_config(tmp_path):
    src = tmp_path / "wrapped"
    dst = tmp_path / "unwrapped"
    src.mkdir()
    save_file({"lm_head.weight": _t(VOCAB, HIDDEN)}, str(src / "model.safetensors"))
    cfg = {
        "architectures": ["Qwen3_5ForCausalLM"],
        "model_type": "qwen3_5_text",
        "_flexinfer_layout": "qwen3_5_vl_wrapped",
        "_flexinfer_layout_version": "1",
    }
    (src / "config.json").write_text(json.dumps(cfg))
    assert unwrap_mod.unwrap(src, dst, in_place=False) == 0
    with (dst / "config.json").open("r") as fh:
        out_cfg = json.load(fh)
    assert "_flexinfer_layout" not in out_cfg
    assert "_flexinfer_layout_version" not in out_cfg
    assert out_cfg["architectures"] == ["Qwen3_5ForCausalLM"]


def test_unwrap_rewrites_quantize_config_keys(tmp_path):
    src = tmp_path / "wrapped"
    dst = tmp_path / "unwrapped"
    src.mkdir()
    save_file({"lm_head.weight": _t(VOCAB, HIDDEN)}, str(src / "model.safetensors"))
    qc = {
        "bits": 4,
        "group_size": 128,
        "dynamic": {
            "+:model.language_model.layers.\\d+.self_attn.*": {"bits": 4},
            "-:model.language_model.layers.\\d+.mlp.*": True,
        },
    }
    (src / "quantize_config.json").write_text(json.dumps(qc))
    assert unwrap_mod.unwrap(src, dst, in_place=False) == 0
    with (dst / "quantize_config.json").open("r") as fh:
        out = json.load(fh)
    new_keys = list(out["dynamic"].keys())
    assert any("model.layers.\\d+.self_attn" in k for k in new_keys)
    assert all("model.language_model" not in k for k in new_keys)


def test_wrap_rejects_invalid_vision_strategy(tmp_path):
    src = tmp_path / "src"
    dst = tmp_path / "wrapped"
    _build_source_checkpoint(src)
    rc = wrap_mod.wrap(src, dst, vision_strategy="bogus")
    assert rc != 0


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-v"]))

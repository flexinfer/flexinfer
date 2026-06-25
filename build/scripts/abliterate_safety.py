#!/usr/bin/env python3
"""Pure, dependency-free safety helpers for abliteration (issue #52).

These functions encode the safeguards that prevent abliteration from
catastrophically corrupting a model — the failure that produced garbage
inference on Qwen3.5-27B (see docs/dev/qwen35-gptq-root-cause.md). They are
extracted out of ``abliterate.py`` (which imports torch and executes a job at
module load, so it cannot be unit-tested directly) into this importable,
stdlib-only module so the logic can carry regression tests in CI.

Safeguards covered here:
  * ``select_full_attention_layers`` — GDN-skip detection. Qwen3.5 and similar
    hybrid models interleave GatedDeltaNet (linear-attention) layers with full
    self-attention layers. Abliterating GDN layers destroys their recurrence
    mechanics, so only full-attention layers must be targeted. Detection mirrors
    transformers config conventions: ``layer_types`` first, then
    ``full_attention_interval`` / legacy ``decoder_sparse_step``.
  * ``refusal_norm_exceeds`` — norm guard. A refusal-direction norm far above the
    typical 20-50 range means the direction captured model capability rather than
    just refusal behavior; abliterating along it corrupts the model.
  * ``is_degenerate_generation`` — the coherence fallback used when perplexity is
    inconclusive (NaN under CPU device_map). Flags empty output, repetition
    loops, and runaway single-character loops.
"""
from __future__ import annotations


def select_full_attention_layers(cfg, total_layers, candidate_layers):
    """Filter ``candidate_layers`` down to full-attention layers only.

    Args:
        cfg: parsed ``config.json`` dict (may contain a nested ``text_config``).
        total_layers: total decoder layer count.
        candidate_layers: layer indices that abliteration would otherwise target.

    Returns:
        ``(kept_layers, source_detail, filtered)`` where ``kept_layers`` is the
        subset of ``candidate_layers`` that are full-attention layers,
        ``source_detail`` names the config signal used, and ``filtered`` is True
        only when a full-attention signal was found and applied. When no signal
        is present the inputs are returned unchanged with ``filtered=False`` so
        the caller can decide whether to proceed or abort.
    """
    text_cfg = cfg.get("text_config") or {}
    layer_types = text_cfg.get("layer_types") or cfg.get("layer_types") or []

    full_attn_indices = set()
    source_detail = ""

    if isinstance(layer_types, list) and layer_types:
        full_attn_indices = {
            i
            for i, layer_type in enumerate(layer_types[:total_layers])
            if layer_type == "full_attention"
        }
        if full_attn_indices:
            source_detail = "layer_types"

    if not full_attn_indices:
        full_attention_interval = (
            text_cfg.get("full_attention_interval")
            or cfg.get("full_attention_interval")
            or text_cfg.get("decoder_sparse_step")
            or cfg.get("decoder_sparse_step")
            or 0
        )
        if full_attention_interval and int(full_attention_interval) > 0:
            full_attn_indices = {
                i
                for i in range(total_layers)
                if (i + 1) % int(full_attention_interval) == 0
            }
            source_detail = f"full_attention_interval={full_attention_interval}"

    if not full_attn_indices:
        return list(candidate_layers), "", False

    kept = [i for i in candidate_layers if i in full_attn_indices]
    return kept, source_detail, True


def refusal_norm_exceeds(max_norm, threshold):
    """Return True when the max refusal-direction norm exceeds the guard threshold."""
    return float(max_norm) > float(threshold)


def is_degenerate_generation(text):
    """Heuristic coherence check used when perplexity validation is inconclusive.

    Flags output that is empty, a repetition loop (<=2 distinct tokens over a
    non-trivial length), or a runaway single-character loop.
    """
    if not text.strip():
        return True
    if len(set(text.split())) <= 2 and len(text) > 10:
        return True
    if any(c * 5 in text for c in "!?.#*"):
        return True
    return False

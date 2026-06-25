#!/usr/bin/env python3
"""Regression tests for abliteration safeguards (issue #52).

These pin the logic that prevents abliteration from corrupting hybrid/GDN
models — the failure mode that produced garbage Qwen3.5-27B inference
(docs/dev/qwen35-gptq-root-cause.md). Run standalone like the other
build/scripts tests: ``python3 build/scripts/test_abliterate_safety.py``.
"""
from __future__ import annotations

import unittest

import abliterate_safety as safety


class SelectFullAttentionLayersTests(unittest.TestCase):
    def test_layer_types_signal(self):
        cfg = {
            "layer_types": [
                "linear_attention",
                "full_attention",
                "linear_attention",
                "full_attention",
            ]
        }
        kept, source, filtered = safety.select_full_attention_layers(
            cfg, total_layers=4, candidate_layers=[0, 1, 2, 3]
        )
        self.assertEqual(kept, [1, 3])
        self.assertEqual(source, "layer_types")
        self.assertTrue(filtered)

    def test_layer_types_nested_in_text_config(self):
        cfg = {"text_config": {"layer_types": ["full_attention", "linear_attention"]}}
        kept, source, filtered = safety.select_full_attention_layers(
            cfg, total_layers=2, candidate_layers=[0, 1]
        )
        self.assertEqual(kept, [0])
        self.assertEqual(source, "layer_types")
        self.assertTrue(filtered)

    def test_full_attention_interval_signal(self):
        cfg = {"full_attention_interval": 4}
        kept, source, filtered = safety.select_full_attention_layers(
            cfg, total_layers=8, candidate_layers=list(range(8))
        )
        # (i + 1) % 4 == 0 -> layers 3 and 7.
        self.assertEqual(kept, [3, 7])
        self.assertEqual(source, "full_attention_interval=4")
        self.assertTrue(filtered)

    def test_legacy_decoder_sparse_step_signal(self):
        # The original Qwen3.5-27B config: 64 layers, every 4th is full attention.
        cfg = {"decoder_sparse_step": 4}
        total = 64
        kept, source, filtered = safety.select_full_attention_layers(
            cfg, total_layers=total, candidate_layers=list(range(total))
        )
        self.assertTrue(filtered)
        self.assertEqual(source, "full_attention_interval=4")
        # Exactly 16 full-attention layers kept; 48 GDN layers skipped.
        self.assertEqual(len(kept), 16)
        self.assertEqual(kept, [i for i in range(total) if (i + 1) % 4 == 0])

    def test_no_signal_returns_unchanged_unfiltered(self):
        cfg = {"hidden_size": 5120}
        candidate = [0, 1, 2, 3]
        kept, source, filtered = safety.select_full_attention_layers(
            cfg, total_layers=4, candidate_layers=candidate
        )
        self.assertEqual(kept, candidate)
        self.assertEqual(source, "")
        self.assertFalse(filtered)

    def test_respects_candidate_subset(self):
        # Only the intersection of candidate layers and full-attention layers
        # is kept (a targeted retry must not re-expand to all full-attn layers).
        cfg = {"decoder_sparse_step": 4}
        kept, _source, filtered = safety.select_full_attention_layers(
            cfg, total_layers=16, candidate_layers=[3, 5, 7]
        )
        self.assertTrue(filtered)
        self.assertEqual(kept, [3, 7])  # 5 is a GDN layer, dropped

    def test_zero_interval_is_not_a_signal(self):
        cfg = {"decoder_sparse_step": 0}
        kept, _source, filtered = safety.select_full_attention_layers(
            cfg, total_layers=4, candidate_layers=[0, 1, 2, 3]
        )
        self.assertFalse(filtered)
        self.assertEqual(kept, [0, 1, 2, 3])


class RefusalNormGuardTests(unittest.TestCase):
    def test_above_threshold(self):
        self.assertTrue(safety.refusal_norm_exceeds(152.96, 100))

    def test_strict_inequality_at_threshold(self):
        self.assertFalse(safety.refusal_norm_exceeds(100.0, 100.0))

    def test_below_threshold(self):
        self.assertFalse(safety.refusal_norm_exceeds(42.0, 100))


class DegenerateGenerationTests(unittest.TestCase):
    def test_empty_or_whitespace(self):
        self.assertTrue(safety.is_degenerate_generation(""))
        self.assertTrue(safety.is_degenerate_generation("   \n\t "))

    def test_repetition_loop(self):
        self.assertTrue(safety.is_degenerate_generation("the the the the the the"))

    def test_character_loop(self):
        self.assertTrue(safety.is_degenerate_generation("answer!!!!!!"))

    def test_coherent_text_is_not_degenerate(self):
        self.assertFalse(
            safety.is_degenerate_generation("Paris, the capital of France.")
        )

    def test_short_low_diversity_text_is_not_flagged(self):
        # <=2 distinct tokens but short (len <= 10) should not trip the loop guard.
        self.assertFalse(safety.is_degenerate_generation("4"))


if __name__ == "__main__":
    unittest.main()

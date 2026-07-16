#!/usr/bin/env python3
"""Unit contract for surgical Qwen3.5 MTP expert RTN quantization."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path

import numpy as np


SCRIPT = Path(__file__).with_name("quantize_mtp_experts.py")
SPEC = importlib.util.spec_from_file_location("quantize_mtp_experts", SCRIPT)
assert SPEC and SPEC.loader
mtpq = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(mtpq)


class QuantizeMTPExpertsTest(unittest.TestCase):
    def test_symmetric_rtn_round_trip_and_packing(self) -> None:
        rng = np.random.default_rng(42)
        weight = rng.normal(0, 0.3, size=(24, 32)).astype(np.float32)
        qweight, scales, quality = mtpq.quantize_symmetric_rtn(weight, group_size=16)
        self.assertEqual(qweight.shape, (4, 24))
        self.assertEqual(qweight.dtype, np.int32)
        self.assertEqual(scales.shape, (2, 24))
        self.assertEqual(scales.dtype, np.float16)
        restored = mtpq.dequantize_symmetric_rtn(qweight, scales, 16)
        self.assertEqual(restored.shape, weight.shape)
        self.assertLess(quality["relative_l1_error"], 0.15)
        self.assertGreater(quality["cosine_similarity"], 0.98)

    def test_discovery_requires_complete_single_layer(self) -> None:
        weight_map = {}
        for expert in range(2):
            for projection in mtpq.PROJECTIONS:
                key = f"mtp.layers.0.mlp.experts.{expert}.{projection}.weight"
                weight_map[key] = "model.safetensors"
        prefix, found = mtpq.discover_expert_weights(weight_map, 2)
        self.assertEqual(prefix, "mtp.layers.0.mlp.experts")
        self.assertEqual(len(found), 6)
        del weight_map["mtp.layers.0.mlp.experts.1.down_proj.weight"]
        with self.assertRaisesRegex(ValueError, "contract mismatch"):
            mtpq.discover_expert_weights(weight_map, 2)

    def test_fusion_matches_vllm_gptq_expert_layout(self) -> None:
        tensors = {}
        for expert in range(2):
            for offset, projection in enumerate(mtpq.PROJECTIONS):
                qweight = np.full((2, 3), expert * 10 + offset, dtype=np.int32)
                scales = np.full((1, 3), expert * 10 + offset, dtype=np.float16)
                tensors[(expert, projection)] = (qweight, scales)
        fused = mtpq.fuse_expert_tensors(tensors, 2)
        self.assertEqual(fused["gate_up_proj.qweight"].shape, (2, 4, 3))
        self.assertEqual(fused["gate_up_proj.scales"].shape, (2, 2, 3))
        self.assertEqual(fused["down_proj.qweight"].shape, (2, 2, 3))
        np.testing.assert_array_equal(
            fused["gate_up_proj.qweight"][1, :, 0],
            np.array([10, 10, 11, 11], dtype=np.int32),
        )


if __name__ == "__main__":
    unittest.main()

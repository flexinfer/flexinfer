"""Regression tests for Qwen/Gemma GPTQ runtime patch composition."""

from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).parent / "vllm_gemma4_moe_gptq_patch.py"


def _load_patch_module():
    spec = importlib.util.spec_from_file_location("vllm_gemma4_moe_gptq_patch", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load patch module from {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class GPTQPatchCompositionTests(unittest.TestCase):
    def test_gemma_patch_preserves_qwen_stock_fused_gptq_path(self) -> None:
        """Gemma compatibility must not undo Qwen's fused-GEMM contract."""

        patch_module = _load_patch_module()
        with tempfile.TemporaryDirectory() as tmp:
            vllm_root = Path(tmp)
            gptq_path = (
                vllm_root
                / "model_executor"
                / "layers"
                / "quantization"
                / "gptq.py"
            )
            gptq_path.parent.mkdir(parents=True)
            original = (
                "# FLEXINFER_QWEN35_GPTQ_ROCM_SHUFFLE_SKIP\n"
                "# stock ops.gptq_gemm remains active\n"
            )
            gptq_path.write_text(original)

            self.assertTrue(
                patch_module.patch_gptq_rocm_reference_fallback(vllm_root)
            )
            self.assertEqual(gptq_path.read_text(), original)
            self.assertNotIn(
                "GEMMA4_GPTQ_ROCM_REFERENCE_PATCH", gptq_path.read_text()
            )


if __name__ == "__main__":
    unittest.main()

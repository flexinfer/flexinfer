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
    def test_gemma_patch_replaces_legacy_qwen_fused_path_on_rocm(self) -> None:
        """Production-shape gfx1100 GPTQ must retain the coherent fallback."""

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
                "class GPTQLinearMethod:\n"
                "    def apply(\n"
                "        self,\n"
                "        layer: torch.nn.Module,\n"
                "        x: torch.Tensor,\n"
                "        bias: torch.Tensor | None = None,\n"
                "    ) -> torch.Tensor:\n"
                "        out_shape = x.shape[:-1] + (layer.qweight.shape[-1],)\n"
                "        reshaped_x = x.reshape(-1, x.shape[-1])\n"
                "\n"
                "        # GPTQ v1 and v2 format checkpoints deals with zero points differently,\n"
                "        # and require different gemm kernels.\n"
                "        output = ops.gptq_gemm(\n"
                "            reshaped_x,\n"
                "            layer.qweight,\n"
                "            layer.qzeros,\n"
                "            layer.scales,\n"
                "            layer.g_idx,\n"
                "            layer.exllama_state == ExllamaState.READY,\n"
                "            self.use_v2_format,\n"
                "            self.quant_config.weight_bits,\n"
                "        )\n"
                "        if bias is not None:\n"
                "            output.add_(bias)\n"
                "        return output.reshape(out_shape)\n"
            )
            gptq_path.write_text(original)

            self.assertTrue(
                patch_module.patch_gptq_rocm_reference_fallback(vllm_root)
            )
            patched = gptq_path.read_text()
            self.assertNotEqual(patched, original)
            self.assertIn("GEMMA4_GPTQ_ROCM_REFERENCE_PATCH", patched)
            self.assertIn("unpacked_qweight", patched)


if __name__ == "__main__":
    unittest.main()

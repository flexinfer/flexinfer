"""Tests for the native RDNA3 W4A16 runtime verifier."""

from __future__ import annotations

import importlib.util
import tempfile
import types
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).parent / "verify_vllm_gfx1100_native_w4a16.py"


def _load_verifier():
    spec = importlib.util.spec_from_file_location(
        "verify_vllm_gfx1100_native_w4a16", SCRIPT_PATH
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load verifier from {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class NativeW4A16VerifierTests(unittest.TestCase):
    def test_accepts_v023_rdna3_kernel_contract(self) -> None:
        verifier = _load_verifier()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            kernel = (
                root
                / "model_executor"
                / "kernels"
                / "linear"
                / "mixed_precision"
                / "rdna3_w4a16.py"
            )
            kernel.parent.mkdir(parents=True)
            kernel.write_text(
                '"""W4A16 GPTQ kernel for AMD RDNA3 (gfx1100)."""\n'
                "torch.ops._rocm_C.gptq_gemm_rdna3\n"
            )

            verifier.verify_source_contract(root, "0.23.0")

    def test_rejects_legacy_vllm(self) -> None:
        verifier = _load_verifier()
        with self.assertRaisesRegex(RuntimeError, "vLLM >= 0.23.0"):
            verifier.verify_source_contract(Path("/nonexistent"), "0.17.0+rocm700")

    def test_rejects_missing_native_op_dispatch(self) -> None:
        verifier = _load_verifier()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            kernel = (
                root
                / "model_executor"
                / "kernels"
                / "linear"
                / "mixed_precision"
                / "rdna3_w4a16.py"
            )
            kernel.parent.mkdir(parents=True)
            kernel.write_text("# incomplete RDNA3 kernel wrapper\n")

            with self.assertRaisesRegex(RuntimeError, "gptq_gemm_rdna3"):
                verifier.verify_source_contract(root, "0.23.0")

    def test_loads_rocm_extension_before_checking_operator(self) -> None:
        verifier = _load_verifier()
        torch_module = types.SimpleNamespace(ops=types.SimpleNamespace())

        def register_operator(module_name: str) -> object:
            torch_module.ops._rocm_C = types.SimpleNamespace(
                gptq_gemm_rdna3=object()
            )
            return object()

        importer = mock.Mock(side_effect=register_operator)
        verifier.verify_compiled_contract(torch_module, importer)

        importer.assert_called_once_with("vllm._rocm_C")

    def test_rejects_rocm_extension_without_native_operator(self) -> None:
        verifier = _load_verifier()
        torch_module = types.SimpleNamespace(
            ops=types.SimpleNamespace(_rocm_C=types.SimpleNamespace())
        )

        with self.assertRaisesRegex(RuntimeError, "does not register"):
            verifier.verify_compiled_contract(torch_module, mock.Mock())

    def test_reports_rocm_extension_load_failure(self) -> None:
        verifier = _load_verifier()
        torch_module = types.SimpleNamespace(ops=types.SimpleNamespace())
        importer = mock.Mock(side_effect=ImportError("missing shared object"))

        with self.assertRaisesRegex(RuntimeError, "failed to load"):
            verifier.verify_compiled_contract(torch_module, importer)


if __name__ == "__main__":
    unittest.main()

"""Tests for the native RDNA3 W4A16 runtime verifier."""

from __future__ import annotations

import importlib.util
import tempfile
import sys
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
            torch_module.ops._rocm_C = types.SimpleNamespace(gptq_gemm_rdna3=object())
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

    def test_accepts_qwen35_text_plugin_entry_point(self) -> None:
        verifier = _load_verifier()
        entry_points = mock.Mock(
            return_value=[types.SimpleNamespace(name="flexinfer_qwen35_text")]
        )

        verifier.verify_text_plugin_contract(entry_points)

        entry_points.assert_called_once_with(group="vllm.general_plugins")

    def test_text_plugin_registers_clears_rocm_arch_and_checks_registry(self) -> None:
        verifier = _load_verifier()
        calls = []

        def register():
            calls.append("register")

        registry = mock.Mock()
        registry.get_supported_archs.return_value = [
            "Qwen3_5ForCausalLM",
            "Qwen3_5MoeForCausalLM",
        ]
        fake_vllm = types.ModuleType("vllm")
        fake_vllm.ModelRegistry = registry
        entry = mock.Mock()
        entry.name = verifier.TEXT_PLUGIN_ENTRY_POINT
        entry.load.return_value = register
        environ = {"PYTORCH_ROCM_ARCH": "gfx1100"}
        with mock.patch.dict(sys.modules, {"vllm": fake_vllm}):
            verifier.verify_text_plugin_registers(
                mock.Mock(return_value=[entry]), environ
            )
        self.assertEqual(calls, ["register"])
        self.assertNotIn("PYTORCH_ROCM_ARCH", environ)

    def test_text_plugin_registers_rejects_unregistered_architecture(self) -> None:
        verifier = _load_verifier()
        registry = mock.Mock()
        registry.get_supported_archs.return_value = ["Qwen3_5ForConditionalGeneration"]
        fake_vllm = types.ModuleType("vllm")
        fake_vllm.ModelRegistry = registry
        entry = mock.Mock()
        entry.name = verifier.TEXT_PLUGIN_ENTRY_POINT
        entry.load.return_value = lambda: None
        with mock.patch.dict(sys.modules, {"vllm": fake_vllm}):
            with self.assertRaises(RuntimeError) as ctx:
                verifier.verify_text_plugin_registers(
                    mock.Mock(return_value=[entry]), {}
                )
        self.assertIn("Qwen3_5ForCausalLM", str(ctx.exception))

    def test_rejects_missing_qwen35_text_plugin_entry_point(self) -> None:
        verifier = _load_verifier()

        with self.assertRaisesRegex(RuntimeError, "missing flexinfer_qwen35_text"):
            verifier.verify_text_plugin_contract(mock.Mock(return_value=[]))

    def test_isa_contract_accepts_extensions_with_target_code_objects(self) -> None:
        """Every compiled extension embeds the HIP target triple for gfx1100."""
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name in ("_C.abi3.so", "_moe_C.abi3.so", "_rocm_C.abi3.so"):
                (root / name).write_bytes(
                    b"\x7fELF amdgcn-amd-amdhsa--gfx942 amdgcn-amd-amdhsa--gfx1100 "
                )
            checked = verifier.verify_isa_contract(root, "gfx1100")

        self.assertEqual(checked, ["_C.abi3.so", "_moe_C.abi3.so", "_rocm_C.abi3.so"])

    def test_isa_contract_rejects_extensions_without_target_code_objects(self) -> None:
        """A CDNA-only wheel imports fine but must fail the build, not the engine."""
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "_C.abi3.so").write_bytes(b"amdgcn-amd-amdhsa--gfx1100")
            (root / "_rocm_C.abi3.so").write_bytes(b"amdgcn-amd-amdhsa--gfx942")
            with self.assertRaisesRegex(
                RuntimeError, r"lack gfx1100 device code: \['_rocm_C.abi3.so'\]"
            ):
                verifier.verify_isa_contract(root, "gfx1100")

    def test_isa_contract_rejects_missing_extensions(self) -> None:
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaisesRegex(RuntimeError, "no compiled vLLM extensions"):
                verifier.verify_isa_contract(Path(tmp), "gfx1100")


if __name__ == "__main__":
    unittest.main()

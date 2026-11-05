"""Tests for the native RDNA3 W4A16 runtime verifier."""

from __future__ import annotations

import importlib.util
import struct
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

    def _register_case(self, verifier, register, archs=None):
        registry = mock.Mock()
        registry.get_supported_archs.return_value = archs or [
            "Qwen3_5ForCausalLM",
            "Qwen3_5MoeForCausalLM",
        ]
        fake_vllm = types.ModuleType("vllm")
        fake_vllm.ModelRegistry = registry
        warmup = types.ModuleType(verifier.SEGMENTED_WARMUP_MODULE)
        entry = mock.Mock()
        entry.name = verifier.TEXT_PLUGIN_ENTRY_POINT
        entry.load.return_value = lambda: register(warmup)
        modules = {"vllm": fake_vllm, warmup.__name__: warmup}
        return mock.Mock(return_value=[entry]), modules

    def test_text_plugin_registers_clears_rocm_arch_and_checks_registry(self) -> None:
        verifier = _load_verifier()
        calls = []
        environ = {"PYTORCH_ROCM_ARCH": "gfx1100"}

        def register(warmup):
            calls.append(environ.get(verifier.SEGMENTED_RUNTIME_TABLES_ENV))
            setattr(warmup, verifier.SEGMENTED_PATCH_MARKER, True)

        entry_points, modules = self._register_case(verifier, register)
        with mock.patch.dict(sys.modules, modules):
            verifier.verify_text_plugin_registers(entry_points, environ)
        # register() ran once, with the segmented flag on.
        self.assertEqual(calls, ["1"])
        self.assertNotIn("PYTORCH_ROCM_ARCH", environ)

    def test_text_plugin_registers_rejects_unregistered_architecture(self) -> None:
        verifier = _load_verifier()

        def register(warmup):
            setattr(warmup, verifier.SEGMENTED_PATCH_MARKER, True)

        entry_points, modules = self._register_case(
            verifier, register, archs=["Qwen3_5ForConditionalGeneration"]
        )
        with mock.patch.dict(sys.modules, modules):
            with self.assertRaises(RuntimeError) as ctx:
                verifier.verify_text_plugin_registers(entry_points, {})
        self.assertIn("Qwen3_5ForCausalLM", str(ctx.exception))

    def test_text_plugin_registers_requires_the_segmented_patch(self) -> None:
        """A plugin that ignores the flag (or a vLLM it cannot patch) fails the build."""
        verifier = _load_verifier()
        entry_points, modules = self._register_case(verifier, lambda warmup: None)
        with mock.patch.dict(sys.modules, modules):
            with self.assertRaises(RuntimeError) as ctx:
                verifier.verify_text_plugin_registers(entry_points, {})
        self.assertIn(verifier.SEGMENTED_WARMUP_MODULE, str(ctx.exception))

    def test_rejects_missing_qwen35_text_plugin_entry_point(self) -> None:
        verifier = _load_verifier()

        with self.assertRaisesRegex(RuntimeError, "missing flexinfer_qwen35_text"):
            verifier.verify_text_plugin_contract(mock.Mock(return_value=[]))

    def test_isa_contract_accepts_plain_bundles_with_target_code_objects(self) -> None:
        """0.32 layout: every extension's plain bundles name gfx1100."""
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name in ("_C.abi3.so", "_moe_C.abi3.so", "_rocm_C.abi3.so"):
                _write_extension(
                    root / name, _plain_bundle(ALL_TARGETS) + _plain_bundle(ALL_TARGETS)
                )
            checked = verifier.verify_isa_contract(
                root, "gfx1100", list_compressed=_unexpected_lister
            )

        self.assertEqual(checked, ["_C.abi3.so", "_moe_C.abi3.so", "_rocm_C.abi3.so"])

    def test_isa_contract_lists_compressed_bundles(self) -> None:
        """0.33 layout: _rocm_C's zstd CCOB bundles hide the triple from a byte scan."""
        verifier = _load_verifier()
        listed: list[bytes] = []

        def lister(bundle: bytes) -> list[str]:
            listed.append(bundle)
            return _fake_list(bundle)

        first = _compressed_bundle(ALL_TARGETS, version=3)
        second = _compressed_bundle(ALL_TARGETS, version=2)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_extension(root / "_C.abi3.so", _plain_bundle(ALL_TARGETS))
            _write_extension(root / "_rocm_C.abi3.so", first + second + b"\0" * 64)
            checked = verifier.verify_isa_contract(
                root, "gfx1100", list_compressed=lister
            )

        self.assertEqual(checked, ["_C.abi3.so", "_rocm_C.abi3.so"])
        self.assertEqual(listed, [first, second])

    def test_isa_contract_rejects_compressed_bundle_without_target(self) -> None:
        """A wheel that really lacks gfx1100 still fails when its bundles are compressed."""
        verifier = _load_verifier()
        cdna = ["host-x86_64-unknown-linux-gnu-", "hipv4-amdgcn-amd-amdhsa--gfx942"]

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_extension(root / "_C.abi3.so", _plain_bundle(ALL_TARGETS))
            _write_extension(
                root / "_rocm_C.abi3.so",
                _compressed_bundle(ALL_TARGETS) + _compressed_bundle(cdna),
            )
            with self.assertRaisesRegex(
                RuntimeError,
                r"lack gfx1100 device code: \['_rocm_C.abi3.so \(bundles \[1\] of 2\)'\]",
            ):
                verifier.verify_isa_contract(
                    root, "gfx1100", list_compressed=_fake_list
                )

    def test_isa_contract_rejects_extensions_without_target_code_objects(self) -> None:
        """A CDNA-only wheel imports fine but must fail the build, not the engine."""
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_extension(root / "_C.abi3.so", _plain_bundle(ALL_TARGETS))
            _write_extension(
                root / "_rocm_C.abi3.so",
                _plain_bundle(["hipv4-amdgcn-amd-amdhsa--gfx942"]),
            )
            with self.assertRaisesRegex(
                RuntimeError,
                r"lack gfx1100 device code: \['_rocm_C.abi3.so \(all 1 bundles\)'\]",
            ):
                verifier.verify_isa_contract(
                    root, "gfx1100", list_compressed=_unexpected_lister
                )

    def test_isa_contract_matches_the_processor_not_a_prefix(self) -> None:
        """gfx11000 or a gfx110 target is not gfx1100; feature suffixes are."""
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_extension(
                root / "_C.abi3.so",
                _plain_bundle(["hipv4-amdgcn-amd-amdhsa--gfx1100:xnack-"]),
            )
            _write_extension(
                root / "_rocm_C.abi3.so",
                _plain_bundle(["hipv4-amdgcn-amd-amdhsa--gfx11000"]),
            )
            with self.assertRaisesRegex(RuntimeError, r"\['_rocm_C.abi3.so "):
                verifier.verify_isa_contract(
                    root, "gfx1100", list_compressed=_unexpected_lister
                )

    def test_isa_contract_ignores_magic_bytes_inside_a_code_object(self) -> None:
        """Bundles are walked by their headers, not by searching for magic."""
        verifier = _load_verifier()
        payload = b"\x7fELF device code with CCOB and __CLANG_OFFLOAD_BUNDLE__ in it"

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_extension(
                root / "_rocm_C.abi3.so", _plain_bundle(ALL_TARGETS, payload=payload)
            )
            checked = verifier.verify_isa_contract(
                root, "gfx1100", list_compressed=_unexpected_lister
            )

        self.assertEqual(checked, ["_rocm_C.abi3.so"])

    def test_isa_contract_rejects_extension_without_fat_binary(self) -> None:
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "_rocm_C.abi3.so").write_bytes(_elf([(b".text", b"\x90" * 16)]))
            with self.assertRaisesRegex(RuntimeError, r"no offload bundles"):
                verifier.verify_isa_contract(
                    root, "gfx1100", list_compressed=_unexpected_lister
                )

    def test_isa_contract_rejects_unknown_compressed_bundle_version(self) -> None:
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_extension(
                root / "_rocm_C.abi3.so", _compressed_bundle(ALL_TARGETS, version=4)
            )
            with self.assertRaisesRegex(RuntimeError, "unsupported version 4"):
                verifier.verify_isa_contract(
                    root, "gfx1100", list_compressed=_fake_list
                )

    def test_isa_contract_rejects_truncated_compressed_bundle(self) -> None:
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_extension(
                root / "_rocm_C.abi3.so", _compressed_bundle(ALL_TARGETS)[:-8]
            )
            with self.assertRaisesRegex(RuntimeError, "past the fat binary"):
                verifier.verify_isa_contract(
                    root, "gfx1100", list_compressed=_fake_list
                )

    def test_isa_contract_rejects_missing_extensions(self) -> None:
        verifier = _load_verifier()

        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaisesRegex(RuntimeError, "no compiled vLLM extensions"):
                verifier.verify_isa_contract(Path(tmp), "gfx1100")

    def test_bundler_list_fails_closed_without_the_tool(self) -> None:
        verifier = _load_verifier()

        with mock.patch.object(verifier.shutil, "which", return_value=None):
            with self.assertRaisesRegex(RuntimeError, "need clang-offload-bundler"):
                verifier.clang_offload_bundler_list(b"CCOB")

    def test_bundler_list_runs_list_on_the_slice(self) -> None:
        verifier = _load_verifier()
        seen: list[bytes] = []

        def run(argv, **_kwargs):
            seen.append(Path(argv[-1].split("=", 1)[1]).read_bytes())
            self.assertEqual(argv[:3], ["/bundler", "--list", "--type=o"])
            return types.SimpleNamespace(
                returncode=0,
                stdout="host-x86_64-unknown-linux-gnu-\nhipv4-amdgcn-amd-amdhsa--gfx1100\n",
                stderr="",
            )

        with mock.patch.object(verifier.shutil, "which", return_value="/bundler"):
            with mock.patch.object(verifier.subprocess, "run", side_effect=run):
                triples = verifier.clang_offload_bundler_list(b"CCOB slice")

        self.assertEqual(seen, [b"CCOB slice"])
        self.assertEqual(
            triples,
            ["host-x86_64-unknown-linux-gnu-", "hipv4-amdgcn-amd-amdhsa--gfx1100"],
        )


# The nine device targets plus host that every bundle of the vLLM 0.32.0 and
# 0.33.0-rocm72 wheels lists (read on both images, 2026-11-04).
ALL_TARGETS = ["host-x86_64-unknown-linux-gnu-"] + [
    f"hipv4-amdgcn-amd-amdhsa--{isa}"
    for isa in (
        "gfx1100",
        "gfx1101",
        "gfx1150",
        "gfx1151",
        "gfx1200",
        "gfx1201",
        "gfx90a",
        "gfx942",
        "gfx950",
    )
]


def _plain_bundle(triples: list[str], payload: bytes = b"\x7fELF code") -> bytes:
    """clang's plain offload bundle: magic, count, offset/size/triple, payloads."""
    header_size = 24 + 8 + sum(24 + len(triple) for triple in triples)
    entries = b""
    for index, triple in enumerate(triples):
        offset = header_size + index * len(payload)
        entries += (
            struct.pack("<QQQ", offset, len(payload), len(triple)) + triple.encode()
        )
    return (
        b"__CLANG_OFFLOAD_BUNDLE__"
        + struct.pack("<Q", len(triples))
        + entries
        + payload * len(triples)
    )


def _compressed_bundle(triples: list[str], version: int = 3) -> bytes:
    """A CCOB header whose 'stream' is just the triples, for _fake_list."""
    stream = ("\n".join(triples)).encode()
    if version == 2:
        header_size = 4 + 2 + 2 + 4 + 4 + 8
        sizes = struct.pack("<II", header_size + len(stream), len(stream))
    else:
        header_size = 4 + 2 + 2 + 8 + 8 + 8
        sizes = struct.pack("<QQ", header_size + len(stream), len(stream))
    header = b"CCOB" + struct.pack("<HH", version, 1) + sizes + b"\0" * 8
    return header + stream


def _fake_list(bundle: bytes) -> list[str]:
    (version,) = struct.unpack_from("<H", bundle, 4)
    return bundle[24 if version == 2 else 32 :].decode().split()


def _unexpected_lister(bundle: bytes) -> list[str]:
    raise AssertionError("plain bundles must not need clang-offload-bundler")


def _elf(sections: list[tuple[bytes, bytes]]) -> bytes:
    """Minimal 64-bit little-endian ELF with the given sections and a shstrtab."""
    names = b"\0"
    entries = [(0, b"")]
    for name, data in sections + [(b".shstrtab", b"")]:
        entries.append((len(names), data))
        names += name + b"\0"
    entries[-1] = (entries[-1][0], names)
    body = b""
    offsets = []
    for _, data in entries:
        offsets.append(64 + len(body))
        body += data
    shoff = 64 + len(body)
    header = bytearray(64)
    header[:6] = b"\x7fELF\x02\x01"
    struct.pack_into("<Q", header, 0x28, shoff)
    struct.pack_into("<HHH", header, 0x3A, 64, len(entries), len(entries) - 1)
    table = b"".join(
        struct.pack("<IIQQQQIIQQ", name, 1, 0, 0, offset, len(data), 0, 0, 1, 0)
        for (name, data), offset in zip(entries, offsets)
    )
    return bytes(header) + body + table


def _write_extension(path: Path, fatbin: bytes) -> None:
    path.write_bytes(_elf([(b".text", b"\x90" * 16), (b".hip_fatbin", fatbin)]))


if __name__ == "__main__":
    unittest.main()

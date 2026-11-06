#!/usr/bin/env python3
"""CPU-only tests for build/vllm-rdna3-w4a16-plugin and its build verifier.

No torch, vLLM or GPU needed: the plugin's import-time surface is stdlib, and
the parts that touch torch take it as an argument, so fakes stand in. The GPU
behaviour (bit equality with vLLM's kernel, graph and stream safety, speed)
is scripts/bench-gfx1100-gptq-rdna3.py's job.

    python3 build/scripts/test_vllm_rdna3_w4a16_plugin.py
"""

from __future__ import annotations

import pathlib
import struct
import sys
import types
import unittest
from unittest import mock

ROOT = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "build" / "vllm-rdna3-w4a16-plugin" / "src"))
sys.path.insert(0, str(ROOT / "build" / "scripts"))

import flexinfer_vllm_rdna3_w4a16 as plugin  # noqa: E402
import verify_vllm_rdna3_w4a16_plugin as verify  # noqa: E402

STOCK_LINE = "CUDA: registered at /app/vllm/csrc/rocm/torch_bindings.cpp:14 [kernel]"


def fake_torch(active=STOCK_LINE):
    """A torch stand-in whose dispatch table follows flexinfer_rdna3.install."""
    state = {"mode": 0, "line": active, "loads": 0}

    def install(mode):
        state["mode"] = mode
        state["line"] = (
            STOCK_LINE
            if mode == 0
            else f"CUDA: registered at {plugin.OVERRIDE_MARKER}:{mode} [kernel]"
        )
        return mode

    flexinfer = types.SimpleNamespace(install=install, mode=lambda: state["mode"])

    def load_library(path):
        state["loads"] += 1

    def dump_table(op):
        assert op == plugin.OP
        return f"Meta: registered at x [backend fallback]\n{state['line']}\nPython: y\n"

    torch = types.SimpleNamespace(
        ops=types.SimpleNamespace(flexinfer_rdna3=flexinfer, load_library=load_library),
        _C=types.SimpleNamespace(_dispatch_dump_table=dump_table),
    )
    return torch, state


class RequestedModeTest(unittest.TestCase):
    def test_unset_and_off(self):
        for env in (
            {},
            {plugin.ENV: ""},
            {plugin.ENV: "off"},
            {plugin.ENV: " OFF "},
            {plugin.ENV: "0"},
        ):
            self.assertEqual(plugin.requested_mode(env), 0, env)

    def test_modes(self):
        self.assertEqual(plugin.requested_mode({plugin.ENV: "tickets"}), 1)
        self.assertEqual(plugin.requested_mode({plugin.ENV: "Tickets"}), 1)
        self.assertEqual(plugin.requested_mode({plugin.ENV: "1"}), 1)
        self.assertEqual(plugin.requested_mode({plugin.ENV: "dot2"}), 2)
        self.assertEqual(plugin.requested_mode({plugin.ENV: "2"}), 2)

    def test_typo_fails_loudly(self):
        for value in ("ticket", "on", "true", "3"):
            with self.assertRaises(ValueError):
                plugin.requested_mode({plugin.ENV: value})


class HelpersTest(unittest.TestCase):
    def test_vllm_release(self):
        self.assertEqual(plugin.vllm_release("0.33.0+rocm723"), "0.33.0")
        self.assertEqual(plugin.vllm_release("0.32.0"), "0.32.0")
        self.assertIn("0.33.0", plugin.SUPPORTED_VLLM)
        self.assertIn("0.32.0", plugin.SUPPORTED_VLLM)

    def test_active_cuda_kernel(self):
        torch, _ = fake_torch()
        self.assertEqual(plugin.active_cuda_kernel(torch), STOCK_LINE)


class InstallTest(unittest.TestCase):
    def test_install_and_restore(self):
        torch, state = fake_torch()
        line = plugin.install(1, torch)
        self.assertIn(plugin.OVERRIDE_MARKER, line)
        self.assertEqual(state["loads"], 0)  # op already visible: no reload
        self.assertEqual(plugin.install(0, torch), STOCK_LINE)

    def test_install_detects_kernel_not_swapped(self):
        torch, state = fake_torch()
        torch.ops.flexinfer_rdna3.install = lambda mode: mode  # table never moves
        with self.assertRaises(RuntimeError):
            plugin.install(1, torch)

    def test_install_loads_library_when_ops_missing(self):
        torch, state = fake_torch()
        ops = torch.ops.flexinfer_rdna3
        torch.ops.flexinfer_rdna3 = types.SimpleNamespace()

        def load_library(path):
            state["loads"] += 1
            torch.ops.flexinfer_rdna3 = ops

        torch.ops.load_library = load_library
        with mock.patch.object(plugin, "library_path", return_value="/x/_C.so"):
            plugin.install(1, torch)
        self.assertEqual(state["loads"], 1)


class RegisterTest(unittest.TestCase):
    def test_off_imports_nothing(self):
        with mock.patch.dict("os.environ", {}, clear=True), mock.patch.dict(
            sys.modules, {"torch": None}
        ):
            plugin.register()  # would raise ImportError if it touched torch

    def test_typo_raises(self):
        with mock.patch.dict("os.environ", {plugin.ENV: "tikets"}):
            with self.assertRaises(ValueError):
                plugin.register()


def objdump(symbols):
    out = ["", "Disassembly of section .text:", ""]
    for name, body in symbols.items():
        out.append(f"<{name}>:")
        out.extend(body)
        out.append("")
    return "\n".join(out)


class VerifierHelpersTest(unittest.TestCase):
    def test_kernel_key(self):
        self.assertEqual(
            verify.kernel_key(
                "void flexinfer_rdna3_vllm::gptq_rdna3::gemm_q4_kernel_rdna3<__half, 2>(__half const*, int)"
            ),
            "gptq_rdna3::gemm_q4_kernel_rdna3<__half, 2>",
        )
        self.assertEqual(
            verify.kernel_key(
                "void vllm::gptq_rdna3::reduce_partials_rdna3<__hip_bfloat16>(float const*)"
            ),
            "gptq_rdna3::reduce_partials_rdna3<__hip_bfloat16>",
        )
        self.assertIsNone(
            verify.kernel_key(
                "void vllm::gptq_rdna3_dot2::gemm_q4_kernel_dot2<3, 4, 8>()"
            )
        )
        self.assertIsNone(
            verify.kernel_key(
                "void vllm::moe_gptq_rdna3::moe_gemm_q4_kernel_rdna3<__half, 2>()"
            )
        )

    def test_normalize_ignores_names_and_comments(self):
        a = [
            "\ts_cbranch_execz 12 <_ZN4vllm10gptq_rdna3X+0x30>  // 000000001234",
            "\tv_dot2acc_f32_f16 v1, v2, v3",
        ]
        b = [
            "  s_cbranch_execz 12 <_ZN20flexinfer_rdna3_vllmX+0x30>",
            "v_dot2acc_f32_f16   v1, v2, v3 // c",
        ]
        self.assertEqual(
            verify.normalize_disassembly(a), verify.normalize_disassembly(b)
        )
        c = ["\tv_dot2acc_f32_f16 v1, v2, v4"]
        self.assertNotEqual(
            verify.normalize_disassembly(a[1:]), verify.normalize_disassembly(c)
        )

    def test_split_functions(self):
        text = objdump({"k1": ["\ts_nop 0", "\ts_endpgm"], "k2": ["\ts_endpgm"]})
        funcs = verify.split_functions(text)
        self.assertEqual(sorted(funcs), ["k1", "k2"])
        self.assertEqual(len(funcs["k1"]), 2)

    def test_compare_kernels(self):
        all_same = {k: "h" for k in verify.MATCHED_KERNELS}
        self.assertEqual(verify.compare_kernels(all_same, dict(all_same)), [])
        ours = dict(all_same)
        ours[verify.MATCHED_KERNELS[1]] = "other"
        problems = verify.compare_kernels(all_same, ours)
        self.assertEqual(len(problems), 1)
        self.assertIn("differs", problems[0])
        missing = dict(all_same)
        del missing[verify.MATCHED_KERNELS[0]]
        self.assertIn(
            "not found in the plugin", verify.compare_kernels(all_same, missing)[0]
        )

    def test_offload_bundles_plain(self):
        triple = b"hipv4-amdgcn-amd-amdhsa--gfx1100"
        payload = b"\x7fELF" + b"\0" * 12
        header_len = len(verify.PLAIN_BUNDLE_MAGIC) + 8 + 24 + len(triple)
        bundle = (
            verify.PLAIN_BUNDLE_MAGIC
            + struct.pack("<Q", 1)
            + struct.pack("<QQQ", header_len, len(payload), len(triple))
            + triple
            + payload
        )
        fatbin = bundle + b"\0" * 7 + bundle
        bundles = verify.offload_bundles(fatbin)
        self.assertEqual(len(bundles), 2)
        self.assertEqual(bundles[0], bundle)

    def test_offload_bundles_compressed_v3(self):
        body = b"x" * 40
        blob = (
            verify.COMPRESSED_BUNDLE_MAGIC
            + struct.pack("<HH", 3, 1)
            + struct.pack("<Q", 16 + len(body))
            + body
        )
        self.assertEqual(verify.offload_bundles(b"\0\0" + blob + b"\0"), [blob])


if __name__ == "__main__":
    unittest.main()

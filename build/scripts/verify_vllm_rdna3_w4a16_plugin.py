#!/usr/bin/env python3
"""Build-time check of build/vllm-rdna3-w4a16-plugin inside the serving image.

Runs with no GPU, after the plugin is installed, and fails the build when:

1. the vLLM general-plugin entry point is missing, or this vLLM release is not
   one the vendored kernel source matches (the plugin would stay off forever);
2. the compiled library has no gfx1100 device code;
3. the library cannot replace, and then restore, the CUDA kernel of
   _rocm_C::gptq_gemm_rdna3 next to the real _rocm_C (both directions are
   read back from the dispatcher);
4. register() with FLEXINFER_RDNA3_W4A16=tickets raises on a GPU-less host
   (vLLM loads every general plugin in every process, the API server's too);
5. the vendored scalar GEMM kernels (gemm_q4_kernel_rdna3<T, M_COUNT> and the
   bf16 reduce) are not instruction-for-instruction the kernels in this
   image's _rocm_C. The persistent-ticket change touches only host code, so
   identical device code is what makes its outputs bit-identical to vLLM's;
   a new base whose kernel moved fails here instead of shipping a stale copy.
"""

from __future__ import annotations

import hashlib
import importlib
import os
import pathlib
import re
import shutil
import struct
import subprocess
import sys
import tempfile
from typing import Callable, Iterable

ENTRY_POINT = "flexinfer_rdna3_w4a16"
PLUGIN_MODULE = "flexinfer_vllm_rdna3_w4a16"
ISA = "gfx1100"
LLVM_BIN = pathlib.Path("/opt/rocm/llvm/bin")
PLAIN_BUNDLE_MAGIC = b"__CLANG_OFFLOAD_BUNDLE__"
COMPRESSED_BUNDLE_MAGIC = b"CCOB"
# The kernels whose device code must match vLLM's, by demangled-name suffix
# after the namespace (the plugin's build renames vllm:: to
# flexinfer_rdna3_vllm::).
MATCHED_KERNELS = tuple(
    f"gptq_rdna3::gemm_q4_kernel_rdna3<{t}, {m}>"
    for t in ("__half", "__hip_bfloat16")
    for m in (1, 2, 4, 8)
) + ("gptq_rdna3::reduce_partials_rdna3<__hip_bfloat16>",)


def fail(message: str) -> None:
    raise SystemExit(f"verify_vllm_rdna3_w4a16_plugin: {message}")


# --------------------------------------------------------------------------
# Pure helpers (unit-tested in build/scripts/test_vllm_rdna3_w4a16_plugin.py)
# --------------------------------------------------------------------------


def kernel_key(demangled: str) -> str | None:
    """``void ns::gptq_rdna3::gemm_q4_kernel_rdna3<__half, 2>(...)`` -> key."""
    head = demangled.split("(", 1)[0]
    for key in MATCHED_KERNELS:
        if head.endswith(key):
            return key
    return None


def normalize_disassembly(lines: Iterable[str]) -> list[str]:
    """Drop comments, symbol names and whitespace runs: code, not names."""
    out = []
    for line in lines:
        line = re.sub(r"//.*$", "", line)
        line = re.sub(r"<[^>]*>", "<L>", line)
        line = " ".join(line.split())
        if line:
            out.append(line)
    return out


def split_functions(objdump_text: str) -> dict[str, list[str]]:
    """``llvm-objdump -d --no-show-raw-insn --no-leading-addr`` -> per symbol."""
    funcs: dict[str, list[str]] = {}
    current = None
    for line in objdump_text.splitlines():
        match = re.match(r"^(?:\S+ )?<(.+)>:$", line)
        if match:
            current = match.group(1)
            funcs[current] = []
            continue
        if current is not None and line.strip() and not line.startswith("Disassembly"):
            funcs[current].append(line)
    return funcs


def compare_kernels(stock: dict[str, str], ours: dict[str, str]) -> list[str]:
    """Problems between {key: hash} maps; every matched kernel must agree."""
    problems = []
    for key in MATCHED_KERNELS:
        if key not in stock:
            problems.append(f"{key}: not found in vLLM's _rocm_C")
        elif key not in ours:
            problems.append(f"{key}: not found in the plugin library")
        elif stock[key] != ours[key]:
            problems.append(
                f"{key}: device code differs (vLLM {stock[key]}, plugin {ours[key]})"
            )
    return problems


def elf_section(data: bytes, name: bytes) -> bytes | None:
    if data[:4] != b"\x7fELF" or data[4] != 2 or data[5] != 1:
        raise RuntimeError("not a 64-bit little-endian ELF file")
    (shoff,) = struct.unpack_from("<Q", data, 0x28)
    shentsize, shnum, shstrndx = struct.unpack_from("<HHH", data, 0x3A)

    def header(index: int) -> tuple[int, int, int]:
        fields = struct.unpack_from("<IIQQQQ", data, shoff + index * shentsize)
        return fields[0], fields[4], fields[5]

    _, strtab, _ = header(shstrndx)
    for index in range(shnum):
        name_offset, offset, size = header(index)
        start = strtab + name_offset
        if data[start : data.index(b"\0", start)] == name:
            return data[offset : offset + size]
    return None


def offload_bundles(fatbin: bytes) -> list[bytes]:
    """Slice a .hip_fatbin into its plain and compressed offload bundles."""
    bundles = []
    cursor = 0
    while True:
        starts = [
            (index, magic)
            for magic in (PLAIN_BUNDLE_MAGIC, COMPRESSED_BUNDLE_MAGIC)
            if (index := fatbin.find(magic, cursor)) >= 0
        ]
        if not starts:
            return bundles
        start, magic = min(starts)
        if magic == PLAIN_BUNDLE_MAGIC:
            (count,) = struct.unpack_from("<Q", fatbin, start + len(magic))
            entry = start + len(magic) + 8
            end = entry
            for _ in range(count):
                offset, size, triple_size = struct.unpack_from("<QQQ", fatbin, entry)
                entry += 24 + triple_size
                end = max(end, entry, start + offset + size)
        else:
            (version,) = struct.unpack_from("<H", fatbin, start + 4)
            if version == 3:
                (total,) = struct.unpack_from("<Q", fatbin, start + 8)
            elif version == 2:
                (total,) = struct.unpack_from("<I", fatbin, start + 8)
            else:
                following = [
                    i
                    for i in (
                        fatbin.find(PLAIN_BUNDLE_MAGIC, start + 4),
                        fatbin.find(COMPRESSED_BUNDLE_MAGIC, start + 4),
                    )
                    if i >= 0
                ]
                total = (min(following) if following else len(fatbin)) - start
            end = start + total
        bundles.append(fatbin[start:end])
        cursor = end


# --------------------------------------------------------------------------
# Image checks
# --------------------------------------------------------------------------


def _tool(name: str) -> str:
    path = LLVM_BIN / name
    if path.is_file():
        return str(path)
    found = shutil.which(name)
    if found is None:
        fail(f"{name} not found (looked in {LLVM_BIN} and PATH)")
    return found


def _run(*args: str) -> str:
    return subprocess.run(args, capture_output=True, text=True, check=True).stdout


def _demangle(symbol: str) -> str:
    for tool in (str(LLVM_BIN / "llvm-cxxfilt"), "c++filt"):
        try:
            return _run(tool, symbol).strip()
        except (OSError, subprocess.CalledProcessError):
            continue
    return symbol


def kernel_hashes(library: pathlib.Path) -> dict[str, str]:
    """{matched kernel key: hash of its normalized gfx1100 disassembly}."""
    fatbin = elf_section(library.read_bytes(), b".hip_fatbin")
    if fatbin is None:
        fail(f"{library} has no .hip_fatbin section")
    bundler, objdump, readelf = (
        _tool("clang-offload-bundler"),
        _tool("llvm-objdump"),
        _tool("llvm-readelf"),
    )
    hashes: dict[str, str] = {}
    with tempfile.TemporaryDirectory() as tmp:
        for index, bundle in enumerate(offload_bundles(fatbin)):
            path = pathlib.Path(tmp) / f"b{index}.o"
            path.write_bytes(bundle)
            triples = _run(bundler, "--list", "--type=o", f"--input={path}").split()
            target = next(
                (t for t in triples if t.endswith(f"--{ISA}") or f"--{ISA}:" in t), None
            )
            if target is None:
                continue
            code = pathlib.Path(tmp) / f"co{index}.o"
            _run(
                bundler,
                "--unbundle",
                "--type=o",
                f"--targets={target}",
                f"--input={path}",
                f"--output={code}",
            )
            symbols = _run(readelf, "-s", "--wide", str(code))
            if (
                "gemm_q4_kernel_rdna3" not in symbols
                and "reduce_partials_rdna3" not in symbols
            ):
                continue
            text = _run(
                objdump, "-d", "--no-show-raw-insn", "--no-leading-addr", str(code)
            )
            for symbol, lines in split_functions(text).items():
                key = kernel_key(_demangle(symbol))
                if key is None:
                    continue
                digest = hashlib.sha256(
                    "\n".join(normalize_disassembly(lines)).encode()
                ).hexdigest()[:16]
                hashes[key] = digest
    return hashes


def verify_entry_point(entry_points: Callable) -> None:
    names = [ep.name for ep in entry_points(group="vllm.general_plugins")]
    if ENTRY_POINT not in names:
        fail(f"vllm.general_plugins has no {ENTRY_POINT!r} entry point: {names}")


def verify_override(torch, plugin) -> None:
    import vllm._custom_ops  # noqa: F401
    import vllm._rocm_C  # noqa: F401

    if not hasattr(torch.ops._rocm_C, "gptq_gemm_rdna3"):
        fail("this vLLM's _rocm_C does not register gptq_gemm_rdna3")
    stock = plugin.active_cuda_kernel(torch)
    if plugin.OVERRIDE_MARKER in stock or not stock:
        fail(f"unexpected CUDA kernel before install: {stock!r}")
    for mode in (1, 2, 0, 1, 0):
        line = plugin.install(mode, torch)
        if (plugin.OVERRIDE_MARKER in line) != (mode != 0):
            fail(f"install({mode}) left {line!r} active")
        if torch.ops.flexinfer_rdna3.mode() != mode:
            fail(f"install({mode}) but mode() is {torch.ops.flexinfer_rdna3.mode()}")
    if plugin.active_cuda_kernel(torch) != stock:
        fail("install(0) did not restore vLLM's kernel")


def verify_register_gpu_less(torch, plugin) -> None:
    before = plugin.active_cuda_kernel(torch)
    saved = os.environ.get(plugin.ENV)
    os.environ[plugin.ENV] = "tickets"
    try:
        plugin.register()
    finally:
        if saved is None:
            os.environ.pop(plugin.ENV, None)
        else:
            os.environ[plugin.ENV] = saved
    after = plugin.active_cuda_kernel(torch)
    if not torch.cuda.is_available() and after != before:
        fail(f"register() changed the kernel on a host with no GPU: {after!r}")


def main() -> int:
    # Same reason as verify_vllm_gfx1100_native_w4a16.py: with the arch set
    # and no device, vLLM's platform probe goes down a GPU path.
    os.environ.pop("PYTORCH_ROCM_ARCH", None)
    from importlib.metadata import entry_points

    import torch
    import vllm

    plugin = importlib.import_module(PLUGIN_MODULE)
    verify_entry_point(entry_points)
    release = plugin.vllm_release(vllm.__version__)
    if release not in plugin.SUPPORTED_VLLM:
        fail(
            f"vLLM {vllm.__version__} is not in SUPPORTED_VLLM {plugin.SUPPORTED_VLLM}"
        )
    library = pathlib.Path(plugin.library_path())
    if f"amdgcn-amd-amdhsa--{ISA}".encode() not in library.read_bytes():
        fail(f"{library} carries no {ISA} device code")
    verify_override(torch, plugin)
    verify_register_gpu_less(torch, plugin)
    rocm_c = pathlib.Path(vllm.__file__).parent / "_rocm_C.abi3.so"
    stock = kernel_hashes(rocm_c)
    ours = kernel_hashes(library)
    problems = compare_kernels(stock, ours)
    if problems:
        fail("vendored kernels do not match vLLM's: " + "; ".join(problems))
    print(
        f"verify_vllm_rdna3_w4a16_plugin: ok (vLLM {vllm.__version__}, "
        f"{len(MATCHED_KERNELS)} kernels identical to _rocm_C, override "
        f"installs and restores, {library.name})"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())

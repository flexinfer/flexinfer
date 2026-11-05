#!/usr/bin/env python3
"""Fail a runtime build that lacks vLLM's native RDNA3 W4A16 GPTQ kernel."""

from __future__ import annotations

import ast
import importlib
import importlib.metadata
import pathlib
import re
import shutil
import struct
import subprocess
import sys
import tempfile
from typing import Any, Callable


MINIMUM_VLLM_VERSION = (0, 23, 0)
KERNEL_RELATIVE_PATH = pathlib.Path(
    "model_executor/kernels/linear/mixed_precision/rdna3_w4a16.py"
)
TEXT_PLUGIN_ENTRY_POINT = "flexinfer_qwen35_text"
# The text plugin's opt-in segmented-attention autotune patch
# (flexinfer_vllm_common.segmented): the flag, the module it patches and the
# marker it leaves there.
SEGMENTED_RUNTIME_TABLES_ENV = "FLEXINFER_SEGMENTED_RUNTIME_TABLES"
SEGMENTED_WARMUP_MODULE = (
    "vllm.model_executor.warmup.rocm_segmented_attn_autotune_warmup"
)
SEGMENTED_PATCH_MARKER = "_flexinfer_runtime_tables"
# The opt-in input-embedding host offload (flexinfer_vllm_common.embed_host):
# it wraps BaseModelLoader.load_model and marks the class. Its imports need no
# GPU, so register() installs it here like the segmented patch.
EMBED_HOST_ENV = "FLEXINFER_EMBED_HOST"
EMBED_HOST_LOADER_MODULE = "vllm.model_executor.model_loader.base_loader"
EMBED_HOST_PATCH_MARKER = "_flexinfer_embed_host"
# The opt-in fp8 segmented KV gate lift for gfx11
# (flexinfer_vllm_common.segmented_fp8) imports vllm.platforms.rocm, which
# queries the GPU at import, so a driverless build checks the names it wraps
# from source instead: file -> (top-level name, parameter it reads or None).
SEGMENTED_FP8_SOURCE_API = {
    "v1/attention/backends/rocm_segmented_attn.py": (
        ("RocmSegmentedAttentionBackend.supports_combination", "kv_cache_dtype"),
        ("RocmSegmentedAttentionImpl.__init__", "kv_cache_dtype"),
        ("segmented_attention", None),
    ),
    "v1/attention/ops/segmented_attention.py": (
        ("segmented_attention", "kv_cache_dtype"),
        ("run_segmented_attention", None),
    ),
    "platforms/rocm.py": (
        ("on_gfx11", None),
        ("on_gfx1x", None),
        ("on_gfx12x", None),
    ),
}
SEGMENTED_FP8_GATED_FILES = (
    "v1/attention/backends/rocm_segmented_attn.py",
    "v1/attention/ops/segmented_attention.py",
)
# Compiled extensions that must carry device code for the target ISA. Through
# 0.26 the published ROCm wheel lacked gfx1100 objects and this image rebuilt
# vLLM from source; since the 0.32 wheel embeds them the build relies on the
# upstream wheel and this check is what keeps a future base image honest.
ISA_EXTENSION_GLOBS = ("_C*.so", "_moe_C*.so", "_rocm_C*.so")
# HIP keeps device code in the ELF section .hip_fatbin as a run of clang
# offload bundles, one per translation unit. A plain bundle names its targets
# in a header (magic, count, then offset/size/triple per entry). 0.33's
# _rocm_C ships compressed bundles instead ("CCOB": magic, version, method,
# sizes, hash, then a zstd or zlib stream of a plain bundle), whose triples
# are only visible after decompression, so those go to clang-offload-bundler.
HIP_FATBIN_SECTION = b".hip_fatbin"
PLAIN_BUNDLE_MAGIC = b"__CLANG_OFFLOAD_BUNDLE__"
COMPRESSED_BUNDLE_MAGIC = b"CCOB"
DEVICE_TRIPLE_MARKER = "amdgcn-amd-amdhsa--"
CLANG_OFFLOAD_BUNDLER_CANDIDATES = (
    "/opt/rocm/llvm/bin/clang-offload-bundler",
    "clang-offload-bundler",
)


def _parse_version(raw: str) -> tuple[int, int, int]:
    match = re.match(r"^(\d+)\.(\d+)(?:\.(\d+))?", raw)
    if match is None:
        raise RuntimeError(f"cannot parse vLLM version: {raw!r}")
    return tuple(int(part or 0) for part in match.groups())


def verify_source_contract(vllm_root: pathlib.Path, version: str) -> None:
    """Verify the package contains the gfx1100-native GPTQ dispatch wrapper."""
    if _parse_version(version) < MINIMUM_VLLM_VERSION:
        raise RuntimeError(
            f"native gfx1100 W4A16 requires vLLM >= 0.23.0; found {version}"
        )

    kernel_path = vllm_root / KERNEL_RELATIVE_PATH
    if not kernel_path.is_file():
        raise RuntimeError(f"missing RDNA3 W4A16 kernel wrapper: {kernel_path}")

    source = kernel_path.read_text()
    if "gptq_gemm_rdna3" not in source:
        raise RuntimeError(
            f"{kernel_path} does not dispatch torch.ops._rocm_C.gptq_gemm_rdna3"
        )


def elf_section(data: bytes, name: bytes) -> bytes | None:
    """Return the bytes of section ``name`` of a 64-bit little-endian ELF."""
    if data[:4] != b"\x7fELF" or data[4] != 2 or data[5] != 1:
        raise RuntimeError("not a 64-bit little-endian ELF file")
    (shoff,) = struct.unpack_from("<Q", data, 0x28)
    shentsize, shnum, shstrndx = struct.unpack_from("<HHH", data, 0x3A)

    def header(index: int) -> tuple[int, int, int]:
        fields = struct.unpack_from("<IIQQQQ", data, shoff + index * shentsize)
        return fields[0], fields[4], fields[5]  # sh_name, sh_offset, sh_size

    _, strtab, _ = header(shstrndx)
    for index in range(shnum):
        name_offset, offset, size = header(index)
        start = strtab + name_offset
        if data[start : data.index(b"\0", start)] == name:
            return data[offset : offset + size]
    return None


def _plain_bundle_triples(blob: bytes, start: int) -> tuple[list[str], int]:
    """Parse one plain offload bundle header; return its triples and end."""
    (count,) = struct.unpack_from("<Q", blob, start + len(PLAIN_BUNDLE_MAGIC))
    cursor = start + len(PLAIN_BUNDLE_MAGIC) + 8
    end = cursor
    triples: list[str] = []
    for _ in range(count):
        offset, size, triple_size = struct.unpack_from("<QQQ", blob, cursor)
        cursor += 24
        triples.append(blob[cursor : cursor + triple_size].decode("ascii"))
        cursor += triple_size
        end = max(end, cursor, start + offset + size)
    if end > len(blob):
        raise RuntimeError(f"offload bundle at {start:#x} runs past the fat binary")
    return triples, end


def _compressed_bundle_end(blob: bytes, start: int) -> int:
    """End of the compressed bundle at ``start``, from its header.

    clang's CompressedOffloadBundle header: magic, uint16 version, uint16
    method, then v2 a uint32 and v3 a uint64 total size (header included).
    v1 has no total size, so it runs to the next bundle or the section end.
    """
    (version,) = struct.unpack_from("<H", blob, start + 4)
    if version == 3:
        (total,) = struct.unpack_from("<Q", blob, start + 8)
    elif version == 2:
        (total,) = struct.unpack_from("<I", blob, start + 8)
    elif version == 1:
        following = [
            index
            for index in (
                blob.find(PLAIN_BUNDLE_MAGIC, start + 4),
                blob.find(COMPRESSED_BUNDLE_MAGIC, start + 4),
            )
            if index >= 0
        ]
        return min(following, default=len(blob))
    else:
        raise RuntimeError(
            f"compressed offload bundle at {start:#x} has unsupported version {version}"
        )
    if total <= 0 or start + total > len(blob):
        raise RuntimeError(
            f"compressed offload bundle at {start:#x} declares size {total} "
            f"past the fat binary ({len(blob)} bytes)"
        )
    return start + total


def offload_bundle_triples(
    fatbin: bytes, list_compressed: Callable[[bytes], list[str]]
) -> list[list[str]]:
    """Return the target triples of every offload bundle in a .hip_fatbin.

    Bundles are walked structurally (each header gives its length), so magic
    bytes inside a code object are never mistaken for a bundle. Compressed
    bundles are sliced out whole and handed to ``list_compressed``.
    """
    bundles: list[list[str]] = []
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
            triples, end = _plain_bundle_triples(fatbin, start)
        else:
            end = _compressed_bundle_end(fatbin, start)
            triples = list_compressed(fatbin[start:end])
        bundles.append(triples)
        cursor = end


def clang_offload_bundler_list(bundle: bytes) -> list[str]:
    """List a compressed bundle's targets with ROCm's clang-offload-bundler."""
    bundler = next(
        (
            found
            for candidate in CLANG_OFFLOAD_BUNDLER_CANDIDATES
            if (found := shutil.which(candidate))
        ),
        None,
    )
    if bundler is None:
        raise RuntimeError(
            "compressed offload bundles need clang-offload-bundler to list their "
            f"targets; none of {CLANG_OFFLOAD_BUNDLER_CANDIDATES} is executable"
        )
    with tempfile.TemporaryDirectory() as tmp:
        path = pathlib.Path(tmp) / "bundle.o"
        path.write_bytes(bundle)
        result = subprocess.run(
            [bundler, "--list", "--type=o", f"--input={path}"],
            capture_output=True,
            text=True,
            check=False,
        )
    if result.returncode != 0:
        raise RuntimeError(
            f"clang-offload-bundler --list failed ({result.returncode}): "
            f"{result.stderr.strip()}"
        )
    return result.stdout.split()


def _triple_isa(triple: str) -> str | None:
    """``hipv4-amdgcn-amd-amdhsa--gfx1100[:feature±]`` -> ``gfx1100``."""
    if DEVICE_TRIPLE_MARKER not in triple:
        return None
    return triple.split(DEVICE_TRIPLE_MARKER, 1)[1].split(":", 1)[0]


def verify_isa_contract(
    vllm_root: pathlib.Path,
    isa: str,
    list_compressed: Callable[[bytes], list[str]] = clang_offload_bundler_list,
) -> list[str]:
    """Verify every device bundle of every compiled vLLM extension targets ``isa``.

    Reads each extension's .hip_fatbin and the target list of every offload
    bundle in it, plain or compressed, and requires ``isa`` in each one: a
    wheel that only carries CDNA targets (or one translation unit built
    without gfx1100) imports fine and then fails kernel launches on an RDNA3
    card with "no kernel image is available". A byte scan for the triple was
    enough through 0.32, but 0.33 compresses _rocm_C's bundles and hides it.
    Returns the extension file names that were checked.
    """
    checked: list[str] = []
    missing: list[str] = []
    for pattern in ISA_EXTENSION_GLOBS:
        for path in sorted(vllm_root.glob(pattern)):
            checked.append(path.name)
            fatbin = elf_section(path.read_bytes(), HIP_FATBIN_SECTION)
            bundles = offload_bundle_triples(fatbin or b"", list_compressed)
            if not bundles:
                missing.append(f"{path.name} (no offload bundles)")
                continue
            lacking = [
                index
                for index, triples in enumerate(bundles)
                if isa not in {_triple_isa(triple) for triple in triples}
            ]
            if len(lacking) == len(bundles):
                missing.append(f"{path.name} (all {len(bundles)} bundles)")
            elif lacking:
                missing.append(f"{path.name} (bundles {lacking} of {len(bundles)})")
    if not checked:
        raise RuntimeError(
            f"no compiled vLLM extensions matching {ISA_EXTENSION_GLOBS} under {vllm_root}"
        )
    if missing:
        raise RuntimeError(
            f"compiled vLLM extensions lack {isa} device code: {missing}; "
            "the base wheel was not built for this GPU"
        )
    return checked


def verify_compiled_contract(
    torch_module: Any,
    importer: Callable[[str], Any] = importlib.import_module,
) -> None:
    """Load the ROCm extension directly and verify its gfx1100 operator."""
    try:
        importer("vllm._rocm_C")
    except (ImportError, OSError) as exc:
        raise RuntimeError(
            "failed to load vLLM's compiled ROCm extension vllm._rocm_C"
        ) from exc

    rocm_ops = getattr(torch_module.ops, "_rocm_C", None)
    if rocm_ops is None or not hasattr(rocm_ops, "gptq_gemm_rdna3"):
        raise RuntimeError(
            "vLLM's compiled _rocm_C extension loaded but does not register "
            "gptq_gemm_rdna3"
        )


def verify_text_plugin_contract(entry_points: Any) -> None:
    """Verify the text-only Qwen3.5 adapter will load in every vLLM process."""
    plugins = entry_points(group="vllm.general_plugins")
    if not any(plugin.name == TEXT_PLUGIN_ENTRY_POINT for plugin in plugins):
        raise RuntimeError(
            "missing flexinfer_qwen35_text vLLM general plugin; text-only "
            "Qwen3.5 configs would fall through to the multimodal wrapper"
        )


def verify_text_plugin_registers(entry_points: Any, environ: dict) -> None:
    """Run the plugin's register() the way vLLM will, on this exact vLLM.

    The entry-point check above only proves the plugin is installed. The
    2026-09-13 S2 canary (issue #74) shipped an image whose plugin imported a
    vLLM 0.23 kernel path that 0.26 no longer has; every engine start died in
    load_general_plugins() before the model loaded. Executing register() at
    image-build time catches that class of drift with no GPU: it exercises
    every vLLM import the plugin relies on and the ModelRegistry hooks.
    PYTORCH_ROCM_ARCH is cleared for the call because the consumer-ROCm FLA
    autotuner pruning needs Triton's kernels, which a driverless build
    container does not load; that path is verified by the runtime log line
    (fla_autotuners > 0) on the first GPU start instead.

    FLEXINFER_SEGMENTED_RUNTIME_TABLES and FLEXINFER_EMBED_HOST are set for
    the call, so both opt-in patches must install on this vLLM: their imports
    and every upstream name they wrap are checked here, not on the first lane
    that turns them on. The flags affect only this build-time process.
    """
    environ.pop("PYTORCH_ROCM_ARCH", None)
    environ[SEGMENTED_RUNTIME_TABLES_ENV] = "1"
    environ[EMBED_HOST_ENV] = "1"
    (plugin,) = [
        ep
        for ep in entry_points(group="vllm.general_plugins")
        if ep.name == TEXT_PLUGIN_ENTRY_POINT
    ]
    plugin.load()()
    from vllm import ModelRegistry

    supported = set(ModelRegistry.get_supported_archs())
    missing = {"Qwen3_5ForCausalLM", "Qwen3_5MoeForCausalLM"} - supported
    if missing:
        raise RuntimeError(
            f"flexinfer_qwen35_text register() ran but did not register {sorted(missing)}"
        )
    warmup = sys.modules.get(SEGMENTED_WARMUP_MODULE)
    if not getattr(warmup, SEGMENTED_PATCH_MARKER, False):
        raise RuntimeError(
            f"flexinfer_qwen35_text register() ran with {SEGMENTED_RUNTIME_TABLES_ENV}=1 "
            f"but did not patch {SEGMENTED_WARMUP_MODULE}"
        )
    base_loader = sys.modules.get(EMBED_HOST_LOADER_MODULE)
    loader_cls = getattr(base_loader, "BaseModelLoader", None)
    if not getattr(loader_cls, EMBED_HOST_PATCH_MARKER, False):
        raise RuntimeError(
            f"flexinfer_qwen35_text register() ran with {EMBED_HOST_ENV}=1 "
            f"but did not patch {EMBED_HOST_LOADER_MODULE}.BaseModelLoader"
        )


def _top_level_definitions(tree: ast.Module) -> dict[str, ast.AST]:
    """Map top-level functions, classes, class methods and imported names."""
    found: dict[str, ast.AST] = {}
    for node in tree.body:
        if isinstance(node, (ast.FunctionDef, ast.ClassDef)):
            found[node.name] = node
            if isinstance(node, ast.ClassDef):
                for item in node.body:
                    if isinstance(item, ast.FunctionDef):
                        found[f"{node.name}.{item.name}"] = item
        elif isinstance(node, ast.ImportFrom):
            for alias in node.names:
                found[alias.asname or alias.name] = node
    return found


def verify_segmented_fp8_contract(vllm_root: pathlib.Path) -> bool:
    """Check the names the fp8 segmented gate lift wraps; return whether gated.

    Fails the build when a wrapped name or the parameter it reads is gone.
    Returns whether upstream still gates fp8 to gfx12 (an ``on_gfx12x`` call
    in the backend or the dispatcher); once it does not, the patch is a no-op
    and can be retired.
    """
    for relative, names in SEGMENTED_FP8_SOURCE_API.items():
        path = vllm_root / relative
        if not path.is_file():
            raise RuntimeError(f"segmented fp8 contract: {path} is missing")
        found = _top_level_definitions(ast.parse(path.read_text(encoding="utf-8")))
        for name, parameter in names:
            node = found.get(name)
            if node is None:
                raise RuntimeError(f"segmented fp8 contract: {relative} lacks {name}")
            if parameter is not None:
                arguments = node.args
                params = [a.arg for a in arguments.args + arguments.kwonlyargs]
                if parameter not in params:
                    raise RuntimeError(
                        f"segmented fp8 contract: {relative} {name} has no "
                        f"{parameter!r} parameter"
                    )
    return any(
        "on_gfx12x()" in (vllm_root / relative).read_text(encoding="utf-8")
        for relative in SEGMENTED_FP8_GATED_FILES
    )


def main() -> None:
    import os
    import torch
    import vllm

    version = importlib.metadata.version("vllm")
    vllm_root = pathlib.Path(vllm.__file__).resolve().parent
    isa = os.environ.get("GPU_ARCH", "gfx1100")
    verify_source_contract(vllm_root, version)
    checked = verify_isa_contract(vllm_root, isa)
    verify_text_plugin_contract(importlib.metadata.entry_points)
    verify_text_plugin_registers(importlib.metadata.entry_points, os.environ)
    fp8_gated = verify_segmented_fp8_contract(vllm_root)

    # Image assembly has no GPU, so vLLM cannot detect ROCm and its generic
    # platform loader does not import this extension. Load it explicitly: the
    # Torch operator registration itself does not require a visible device.
    verify_compiled_contract(torch)

    print(
        "verified vLLM",
        version,
        f"native {isa} W4A16 op: torch.ops._rocm_C.gptq_gemm_rdna3;",
        f"{isa} device code in {', '.join(checked)};",
        "flexinfer_qwen35_text register() OK (segmented runtime-tables and "
        "embed-host patches install);",
        "segmented fp8 gate-lift names present"
        + ("" if fp8_gated else " (upstream no longer gates fp8 to gfx12)"),
    )


if __name__ == "__main__":
    main()

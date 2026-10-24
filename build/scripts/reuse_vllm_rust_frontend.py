#!/usr/bin/env python3
"""Copy the base image's precompiled vLLM Rust frontend into a source checkout.

vLLM's ``setup.py`` skips the local Rust build (``precompiled_build_rust``)
whenever ``vllm/vllm-rs`` is present in the source tree, and likewise reuses
any ``vllm/_rust_*.so`` PyO3 extension modules it finds there. Copying both
from the already-installed vLLM in the base image means a from-source rebuild
of the native C++/HIP extensions needs no Rust toolchain.

Usage: reuse_vllm_rust_frontend.py <vllm-source-root>
"""

from __future__ import annotations

import pathlib
import shutil
import sys


def main(source_root: str) -> None:
    import vllm

    installed = pathlib.Path(vllm.__file__).resolve().parent
    frontend = installed / "vllm-rs"
    if not frontend.is_file():
        raise SystemExit(
            f"base image is missing the precompiled Rust frontend: {frontend}"
        )
    target_dir = pathlib.Path(source_root) / "vllm"
    if not target_dir.is_dir():
        raise SystemExit(f"not a vLLM source checkout: {source_root}")

    target = target_dir / "vllm-rs"
    shutil.copy2(frontend, target)
    target.chmod(0o755)
    copied = [target.name]
    for extension in sorted(installed.glob("_rust_*.so")):
        shutil.copy2(extension, target_dir / extension.name)
        copied.append(extension.name)
    print(f"reusing precompiled Rust artifacts from {installed}: {', '.join(copied)}")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    main(sys.argv[1])

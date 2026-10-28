#!/usr/bin/env python3
"""Build-time import checks for the diffusers overlay of the gfx1100 serving image.

Build/Dockerfile.runtime-serving pins diffusers and transformers in a late layer
(Qwen-Image 2.1 needs diffusers >= 0.41) and runs this right after it. It fails
the build when the pinned stack cannot serve what server-diffusers.py loads:

- the pinned diffusers/transformers versions are the ones importable, and
  huggingface-hub sits inside the range both of them import against (the
  unpinned early layer resolves to 2.x, which they refuse);
- QwenImage21Pipeline / QwenImage21Transformer2DModel, the GGUF and pipeline
  quantization configs, and the GGUF classes the server's dequant shim uses;
- Qwen3VLForConditionalGeneration (the Qwen-Image 2.1 text encoder) and the
  transformers bitsandbytes config for the NF4 text encoder;
- gguf >= 0.10, bitsandbytes with a ROCm library, vLLM still importable beside
  the overlay;
- QwenImage21Pipeline.__call__ still takes true_cfg_scale and no guidance_scale,
  the contract server-diffusers.py's call adapter is written against;
- server-diffusers.py itself imports (module scope only; no GPU, no model).

No GPU is needed: BuildKit runs this without devices.
"""

import argparse
import glob
import importlib.metadata as md
import importlib.util
import inspect
import os
import sys


def version_tuple(value: str) -> tuple:
    parts = []
    for piece in value.split("+")[0].split("."):
        digits = "".join(ch for ch in piece if ch.isdigit())
        if not digits:
            break
        parts.append(int(digits))
    return tuple(parts)


def satisfies(version: str, constraint: str) -> bool:
    """Report whether version meets a pip-style constraint such as ">=1.32,<2.0".

    Only the comparison operators pip's pins use are understood (==, !=, >=,
    <=, >, <); the image is not guaranteed to ship `packaging`, and the check
    must not fail on its own dependency.
    """
    got = version_tuple(version)
    for clause in constraint.split(","):
        clause = clause.strip()
        if not clause:
            continue
        for op in (">=", "<=", "==", "!=", ">", "<"):
            if clause.startswith(op):
                want = version_tuple(clause[len(op) :].strip())
                break
        else:
            raise ValueError(f"unsupported constraint clause {clause!r}")
        width = max(len(got), len(want))
        a = got + (0,) * (width - len(got))
        b = want + (0,) * (width - len(want))
        ok = {
            ">=": a >= b,
            "<=": a <= b,
            "==": a == b,
            "!=": a != b,
            ">": a > b,
            "<": a < b,
        }[op]
        if not ok:
            return False
    return True


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--diffusers", required=True, help="pinned diffusers version")
    parser.add_argument(
        "--transformers", required=True, help="pinned transformers version"
    )
    parser.add_argument(
        "--huggingface-hub",
        default=">=1.32,<2.0",
        help="pip constraint the installed huggingface-hub must satisfy "
        "(transformers 5.17 needs >=1.5,<2.0; diffusers 0.41 needs >=1.32,<2.0)",
    )
    parser.add_argument(
        "--vllm", default="true", help="'true' when the image bundles vLLM"
    )
    parser.add_argument(
        "--bitsandbytes",
        default="true",
        help="'true' when the image bundles bitsandbytes",
    )
    parser.add_argument("--server", default="/opt/flexinfer/server-diffusers.py")
    args = parser.parse_args()
    failures = []

    def check(label, fn):
        try:
            detail = fn()
        except Exception as exc:  # report every failing check, then fail once
            failures.append(f"{label}: {type(exc).__name__}: {exc}")
            print(f"FAIL {label}: {type(exc).__name__}: {exc}", flush=True)
        else:
            print(f"ok   {label}{': ' + detail if detail else ''}", flush=True)

    def pinned(package, want):
        def run():
            got = md.version(package)
            if got != want:
                raise AssertionError(f"{package} {got} installed, pin is {want}")
            return got

        return run

    check("diffusers pin", pinned("diffusers", args.diffusers))
    check("transformers pin", pinned("transformers", args.transformers))

    def hub_range():
        got = md.version("huggingface-hub")
        if not satisfies(got, args.huggingface_hub):
            raise AssertionError(
                f"huggingface-hub {got} installed, overlay needs {args.huggingface_hub}; "
                "the unpinned early layer resolves to 2.x, which transformers "
                f"{args.transformers} and diffusers {args.diffusers} refuse to import against"
            )
        return f"{got} within {args.huggingface_hub}"

    check("huggingface-hub range", hub_range)

    def qwen_image21():
        from diffusers import QwenImage21Pipeline, QwenImage21Transformer2DModel

        params = inspect.signature(QwenImage21Pipeline.__call__).parameters
        if "guidance_scale" in params:
            raise AssertionError(
                "QwenImage21Pipeline.__call__ takes guidance_scale again"
            )
        missing = [
            name
            for name in (
                "true_cfg_scale",
                "negative_prompt",
                "image",
                "output_resolution",
            )
            if name not in params
        ]
        if missing:
            raise AssertionError(f"QwenImage21Pipeline.__call__ lacks {missing}")
        if not hasattr(QwenImage21Transformer2DModel, "from_single_file"):
            raise AssertionError(
                "QwenImage21Transformer2DModel has no from_single_file"
            )
        return "pipeline + single-file transformer"

    check("Qwen-Image 2.1 classes", qwen_image21)

    def quantizers():
        from diffusers import BitsAndBytesConfig, GGUFQuantizationConfig  # noqa: F401
        from diffusers.quantizers import PipelineQuantizationConfig  # noqa: F401
        from diffusers.quantizers.gguf.utils import (  # noqa: F401
            GGUFLinear,
            GGUFParameter,
            dequantize_gguf_tensor,
        )

        return "GGUF, bitsandbytes, pipeline mapping, shim classes"

    check("diffusers quantizers", quantizers)

    def text_encoder():
        from transformers import (
            BitsAndBytesConfig,
            Qwen3VLForConditionalGeneration,
        )  # noqa: F401

        return "Qwen3VLForConditionalGeneration"

    check("transformers text encoder", text_encoder)

    def gguf_package():
        import gguf  # noqa: F401

        got = md.version("gguf")
        if version_tuple(got) < (0, 10):
            raise AssertionError(f"gguf {got} < 0.10")
        return got

    check("gguf", gguf_package)

    if args.bitsandbytes == "true":

        def bitsandbytes():
            spec = importlib.util.find_spec("bitsandbytes")
            if spec is None or not spec.submodule_search_locations:
                raise AssertionError("bitsandbytes is not installed")
            root = list(spec.submodule_search_locations)[0]
            libs = sorted(
                os.path.basename(p)
                for p in glob.glob(os.path.join(root, "libbitsandbytes_*"))
            )
            if not any("rocm" in lib or "hip" in lib for lib in libs):
                raise AssertionError(f"no ROCm library in {root}: {libs}")
            return f"{md.version('bitsandbytes')} ({', '.join(libs)})"

        check("bitsandbytes ROCm", bitsandbytes)

    if args.vllm == "true":

        def vllm():
            import vllm

            return vllm.__version__

        check("vllm import", vllm)

    def server():
        spec = importlib.util.spec_from_file_location("server_diffusers", args.server)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        for name in (
            "_adapt_call_kwargs",
            "_quantized_component_kwargs",
            "_dequantize_non_linear_gguf",
        ):
            if not hasattr(module, name):
                raise AssertionError(f"server lacks {name}")
        return args.server

    check("server-diffusers.py imports", server)

    if failures:
        print(f"{len(failures)} diffusers overlay check(s) failed", flush=True)
        return 1
    print("diffusers overlay checks passed", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())

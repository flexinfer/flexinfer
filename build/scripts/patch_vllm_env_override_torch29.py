#!/usr/bin/env python3
"""Patch vLLM env_override.py for torch 2.9 CaptureOutput compatibility."""

from __future__ import annotations

import pathlib
import sys


OLD = """    from torch._dynamo.convert_frame import GraphCaptureOutput

    _original_get_runtime_env = GraphCaptureOutput.get_runtime_env

    def _safe_builtins_dict(builtins_dict: dict) -> dict:
        \"\"\"Filter a builtins dict to only picklable entries for serialization.\"\"\"
        result = {}
        for k, v in builtins_dict.items():
            try:
                pickle.dumps(v)
                result[k] = v
            except Exception:
                pass
        return result

    def _patched_get_runtime_env(self):  # type: ignore[no-untyped-def]
        runtime_env = _original_get_runtime_env(self)
        for ref in runtime_env.external_refs:
            if ref not in runtime_env.used_globals:
                if ref.startswith(\"__builtins_dict__\") and ref in self.f_globals:
                    runtime_env.used_globals[ref] = _safe_builtins_dict(
                        self.f_globals[ref]
                    )
                elif hasattr(_builtins, ref):
                    runtime_env.used_globals[ref] = getattr(_builtins, ref)
        return runtime_env

    GraphCaptureOutput.get_runtime_env = _patched_get_runtime_env
"""

NEW = """    try:
        from torch._dynamo.convert_frame import GraphCaptureOutput as _CaptureOutput
    except ImportError:
        # torch 2.9 exposes CaptureOutput instead of GraphCaptureOutput.
        from torch._dynamo.convert_frame import CaptureOutput as _CaptureOutput

    if hasattr(_CaptureOutput, \"get_runtime_env\"):
        _original_get_runtime_env = _CaptureOutput.get_runtime_env

        def _safe_builtins_dict(builtins_dict: dict) -> dict:
            \"\"\"Filter a builtins dict to only picklable entries for serialization.\"\"\"
            result = {}
            for k, v in builtins_dict.items():
                try:
                    pickle.dumps(v)
                    result[k] = v
                except Exception:
                    pass
            return result

        def _patched_get_runtime_env(self):  # type: ignore[no-untyped-def]
            runtime_env = _original_get_runtime_env(self)
            for ref in runtime_env.external_refs:
                if ref not in runtime_env.used_globals:
                    if ref.startswith(\"__builtins_dict__\") and ref in self.f_globals:
                        runtime_env.used_globals[ref] = _safe_builtins_dict(
                            self.f_globals[ref]
                        )
                    elif hasattr(_builtins, ref):
                        runtime_env.used_globals[ref] = getattr(_builtins, ref)
            return runtime_env

        _CaptureOutput.get_runtime_env = _patched_get_runtime_env
    else:
        print(
            \"skip GraphCaptureOutput runtime-env patch: torch CaptureOutput \"
            \"has no get_runtime_env\"
        )
"""


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: patch_vllm_env_override_torch29.py <vllm-source-root>", file=sys.stderr)
        return 2

    root = pathlib.Path(sys.argv[1])
    target = root / "vllm" / "env_override.py"
    text = target.read_text()

    if NEW in text:
        print(f"already patched: {target}")
        return 0

    if OLD not in text:
        print(f"unexpected env_override.py contents: {target}", file=sys.stderr)
        return 1

    text = text.replace(OLD, NEW, 1)
    target.write_text(text)
    print(f"patched: {target}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

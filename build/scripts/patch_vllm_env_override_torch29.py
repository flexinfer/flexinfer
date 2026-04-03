#!/usr/bin/env python3
"""Patch vLLM source for FlexInfer's Gemma4 ROCm runtime needs.

This currently applies two upstream source patches:
1. env_override.py Torch 2.9 CaptureOutput compatibility.
2. KV-sharing helper fix so shared layers added back to
   UniformTypeKVCacheSpecs inherit their target layer spec entry.
"""

from __future__ import annotations

import pathlib
import sys


ENV_OVERRIDE_OLD = """    from torch._dynamo.convert_frame import GraphCaptureOutput

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

ENV_OVERRIDE_NEW = """    try:
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

KV_SHARING_OLD = """    for layer_name, target_layer_name in shared_kv_cache_layers.items():
        tgt_kv_cache_group = layer_to_kv_cache_group[target_layer_name]
        tgt_kv_cache_group.layer_names.append(layer_name)

        if runner_only_attn_layers is not None:
            runner_only_attn_layers.add(layer_name)
"""

KV_SHARING_NEW = """    for layer_name, target_layer_name in shared_kv_cache_layers.items():
        tgt_kv_cache_group = layer_to_kv_cache_group[target_layer_name]
        tgt_kv_cache_group.layer_names.append(layer_name)
        if isinstance(tgt_kv_cache_group.kv_cache_spec, UniformTypeKVCacheSpecs):
            tgt_kv_cache_group.kv_cache_spec.kv_cache_specs[layer_name] = (
                tgt_kv_cache_group.kv_cache_spec.kv_cache_specs[target_layer_name]
            )

        if runner_only_attn_layers is not None:
            runner_only_attn_layers.add(layer_name)
"""


def _replace_once(
    path: pathlib.Path,
    old: str,
    new: str,
) -> None:
    text = path.read_text()
    if new in text:
        print(f"already patched: {path}")
        return
    if old not in text:
        print(f"unexpected file contents: {path}", file=sys.stderr)
        raise SystemExit(1)
    path.write_text(text.replace(old, new, 1))
    print(f"patched: {path}")


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: patch_vllm_env_override_torch29.py <vllm-source-root>", file=sys.stderr)
        return 2

    root = pathlib.Path(sys.argv[1])
    env_override = root / "vllm" / "env_override.py"
    kv_sharing = root / "vllm" / "v1" / "worker" / "utils.py"

    _replace_once(env_override, ENV_OVERRIDE_OLD, ENV_OVERRIDE_NEW)
    _replace_once(kv_sharing, KV_SHARING_OLD, KV_SHARING_NEW)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

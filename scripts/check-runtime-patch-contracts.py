#!/usr/bin/env python3
"""Fast static checks for runtime patch wiring.

The unified runtime image can spend tens of minutes in BuildKit before it
reaches the vLLM patch layers. Keep these checks cheap and dependency-free so
CI can fail in the lint stage when patch scripts or runtime wiring drift.
"""

from __future__ import annotations

import ast
import py_compile
import re
import sys
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]

PATCH_SCRIPTS = (
    "build/scripts/vllm_qwen35_patches_nodiag.py",
    "build/scripts/vllm_gemma4_moe_gptq_patch.py",
    "build/scripts/patch_vllm_env_override_torch29.py",
    "build/scripts/patch_turboquant_quantizer_gpu_qr.py",
)

PATCH_TESTS = (
    "build/scripts/test_modules_in_block_to_quantize.py",
    "build/scripts/test_validate_quantized_artifact.py",
)

COMPILE_ONLY = PATCH_SCRIPTS + (
    "build/scripts/quantize_gptq.py",
    "build/scripts/validate_quantized_artifact.py",
)

CI_CHANGE_RULE_FILES = PATCH_SCRIPTS + (
    "scripts/check-runtime-patch-contracts.py",
    "build/Dockerfile.runtime",
    "build/runtime.yaml",
    ".gitlab/ci/runtime-publish.yml",
)


def fail(message: str) -> None:
    raise SystemExit(f"ERROR: {message}")


def read(path: str) -> str:
    target = REPO_ROOT / path
    if not target.exists():
        fail(f"missing required file: {path}")
    return target.read_text()


def strip_yaml_value(raw: str) -> str:
    value = raw.split("#", 1)[0].strip()
    if (value.startswith('"') and value.endswith('"')) or (
        value.startswith("'") and value.endswith("'")
    ):
        value = value[1:-1]
    return value


def referenced_source_patch_scripts(runtime_yaml: str) -> set[str]:
    scripts: set[str] = set()
    for line in runtime_yaml.splitlines():
        match = re.match(
            r"\s+(?:vllm_source_patch_script|turboquant_source_patch_script):\s*(.+)$",
            line,
        )
        if not match:
            continue
        value = strip_yaml_value(match.group(1))
        if value:
            scripts.add(value)
    return scripts


def assert_patch_scripts_exist_and_parse() -> None:
    for relative in COMPILE_ONLY:
        target = REPO_ROOT / relative
        if not target.exists():
            fail(f"missing patch/check script: {relative}")
        try:
            py_compile.compile(str(target), doraise=True)
        except py_compile.PyCompileError as exc:
            fail(f"{relative} does not compile: {exc.msg}")

        try:
            ast.parse(target.read_text(), filename=str(target))
        except SyntaxError as exc:
            fail(f"{relative} does not parse: {exc}")


def assert_runtime_yaml_patch_refs(runtime_yaml: str) -> None:
    referenced = referenced_source_patch_scripts(runtime_yaml)
    for relative in referenced:
        if not relative.startswith("build/scripts/"):
            fail(f"runtime.yaml patch reference must stay under build/scripts/: {relative}")
        if not (REPO_ROOT / relative).exists():
            fail(f"runtime.yaml references missing patch script: {relative}")

    required_refs = {
        "build/scripts/patch_vllm_env_override_torch29.py",
        "build/scripts/patch_turboquant_quantizer_gpu_qr.py",
    }
    missing = sorted(required_refs - referenced)
    if missing:
        fail(f"runtime.yaml no longer references expected source patch scripts: {missing}")


def assert_dockerfile_patch_order(dockerfile: str) -> None:
    scripts_copy = dockerfile.find("COPY build/scripts/ /opt/flexinfer/scripts/")
    qwen_patch = dockerfile.find("python3 /opt/flexinfer/scripts/vllm_qwen35_patches_nodiag.py")
    gemma_patch = dockerfile.find("python3 /opt/flexinfer/scripts/vllm_gemma4_moe_gptq_patch.py")
    hip_check = dockerfile.find("CUDA torch detected")

    if min(scripts_copy, qwen_patch, gemma_patch, hip_check) == -1:
        fail("Dockerfile.runtime is missing runtime patch copy/apply/HIP-check wiring")
    if not (scripts_copy < qwen_patch < gemma_patch < hip_check):
        fail("Dockerfile.runtime patch order changed; expected copy -> qwen -> gemma -> HIP check")

    for token in ("VLLM_SOURCE_PATCH_SCRIPT", "TURBOQUANT_SOURCE_PATCH_SCRIPT"):
        if token not in dockerfile:
            fail(f"Dockerfile.runtime no longer applies {token}")


def assert_ci_fast_check_wiring(ci_yaml: str) -> None:
    fast_job = ci_yaml.find("runtime_patch_contracts:")
    publish_job = ci_yaml.find("publish_unified_rocm_gfx1100:")
    if fast_job == -1:
        fail("runtime-publish CI is missing runtime_patch_contracts")
    if publish_job == -1:
        fail("runtime-publish CI is missing publish_unified_rocm_gfx1100")
    if fast_job > publish_job:
        fail("runtime_patch_contracts must appear before publish_unified_rocm_gfx1100")

    fast_job_body = ci_yaml[fast_job:publish_job]
    required_snippets = (
        "stage: lint",
        "needs: []",
        "python3 scripts/check-runtime-patch-contracts.py",
        "python3 build/scripts/test_modules_in_block_to_quantize.py",
        "python3 build/scripts/test_validate_quantized_artifact.py",
    )
    for snippet in required_snippets:
        if snippet not in fast_job_body:
            fail(f"runtime_patch_contracts missing {snippet!r}")

    missing_rules = [path for path in CI_CHANGE_RULE_FILES if path not in fast_job_body]
    if missing_rules:
        fail(f"runtime_patch_contracts rules do not include: {missing_rules}")


def run_python_tests() -> None:
    loader = unittest.TestLoader()
    suite = unittest.TestSuite()
    for relative in PATCH_TESTS:
        test_path = REPO_ROOT / relative
        suite.addTests(loader.discover(str(test_path.parent), pattern=test_path.name))
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    if not result.wasSuccessful():
        raise SystemExit(1)


def main(argv: list[str]) -> int:
    run_tests = "--run-script-tests" in argv

    runtime_yaml = read("build/runtime.yaml")
    dockerfile = read("build/Dockerfile.runtime")
    ci_yaml = read(".gitlab/ci/runtime-publish.yml")

    assert_patch_scripts_exist_and_parse()
    assert_runtime_yaml_patch_refs(runtime_yaml)
    assert_dockerfile_patch_order(dockerfile)
    assert_ci_fast_check_wiring(ci_yaml)

    if run_tests:
        run_python_tests()

    print("runtime patch contract checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

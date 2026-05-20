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
    "build/scripts/install_vllm_gfx906_compat.py",
    "build/scripts/quantize_gptq.py",
    "build/scripts/validate_quantized_artifact.py",
)

CI_CHANGE_RULE_FILES = PATCH_SCRIPTS + (
    "scripts/check-runtime-patch-contracts.py",
    "build/build-runtime.sh",
    "build/Dockerfile.runtime",
    "build/Dockerfile.runtime-serving",
    "build/Dockerfile.vllm-rocm-gfx906",
    "build/runtime-entrypoint.sh",
    "build/runtime.yaml",
    "build/scripts/install_vllm_gfx906_compat.py",
    ".gitlab/ci/runtime-publish.yml",
)

SERVING_CHANGE_RULE_FILES = (
    ".gitlab/ci/runtime-publish.yml",
    "build/Dockerfile.runtime-serving",
    "build/runtime.yaml",
    "build/runtime-entrypoint.sh",
    "build/server-diffusers.py",
    "build/scripts/vllm_gemma4_moe_gptq_patch.py",
    "build/scripts/vllm_qwen35_patches_nodiag.py",
)

UNIFIED_SERVING_ONLY_RULE_FILES = (
    "build/server-diffusers.py",
    "build/scripts/vllm_gemma4_moe_gptq_patch.py",
    "build/scripts/vllm_qwen35_patches_nodiag.py",
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
            fail(
                f"runtime.yaml patch reference must stay under build/scripts/: {relative}"
            )
        if not (REPO_ROOT / relative).exists():
            fail(f"runtime.yaml references missing patch script: {relative}")

    required_refs = {
        "build/scripts/patch_vllm_env_override_torch29.py",
        "build/scripts/patch_turboquant_quantizer_gpu_qr.py",
    }
    missing = sorted(required_refs - referenced)
    if missing:
        fail(
            f"runtime.yaml no longer references expected source patch scripts: {missing}"
        )


def assert_dockerfile_patch_order(dockerfile: str) -> None:
    scripts_copy = dockerfile.find("COPY build/scripts/ /opt/flexinfer/scripts/")
    qwen_patch = dockerfile.find(
        "python3 /opt/flexinfer/scripts/vllm_qwen35_patches_nodiag.py"
    )
    gemma_patch = dockerfile.find(
        "python3 /opt/flexinfer/scripts/vllm_gemma4_moe_gptq_patch.py"
    )
    hip_check = dockerfile.find("CUDA torch detected")

    if min(scripts_copy, qwen_patch, gemma_patch, hip_check) == -1:
        fail("Dockerfile.runtime is missing runtime patch copy/apply/HIP-check wiring")
    if not (scripts_copy < qwen_patch < gemma_patch < hip_check):
        fail(
            "Dockerfile.runtime patch order changed; expected copy -> qwen -> gemma -> HIP check"
        )

    for token in ("VLLM_SOURCE_PATCH_SCRIPT", "TURBOQUANT_SOURCE_PATCH_SCRIPT"):
        if token not in dockerfile:
            fail(f"Dockerfile.runtime no longer applies {token}")

    if "ARG SKIP_GEMMA4_MOE_PATCH=false" not in dockerfile:
        fail("Dockerfile.runtime missing SKIP_GEMMA4_MOE_PATCH build arg")
    if 'if [ "${SKIP_GEMMA4_MOE_PATCH}" = "true" ]; then' not in dockerfile:
        fail("Dockerfile.runtime no longer gates the Gemma4 MoE patch")
    if "FLEXINFER_GEMMA4_MOE_NATIVE_COMPAT_ONLY=1" not in dockerfile:
        fail("Dockerfile.runtime no longer applies native Gemma4 MoE compat")


def assert_runtime_entrypoint_contract(entrypoint: str, build_script: str) -> None:
    if "SKIP_GEMMA4_MOE_PATCH=${skip_gemma4_moe_patch}" not in build_script:
        fail("build-runtime.sh no longer bakes SKIP_GEMMA4_MOE_PATCH into runtime.env")
    if "SKIP_GEMMA4_MOE_PATCH:-false" not in entrypoint:
        fail("runtime-entrypoint.sh no longer respects SKIP_GEMMA4_MOE_PATCH")
    if "vllm_gemma4_moe_gptq_patch.py" not in entrypoint:
        fail("runtime-entrypoint.sh no longer wires the Gemma4 MoE patch script")


def assert_serving_dockerfile_contract(dockerfile: str) -> None:
    required = (
        "Serving-focused flexinfer-runtime Dockerfile.",
        "FROM golang:${GO_VERSION} AS go-builder",
        "FROM ${BASE_IMAGE} AS runtime",
        "COPY build/scripts/ /opt/flexinfer/scripts/",
        "python3 /opt/flexinfer/scripts/vllm_qwen35_patches_nodiag.py",
        "python3 /opt/flexinfer/scripts/vllm_gemma4_moe_gptq_patch.py",
        "COPY build/server-diffusers.py /opt/flexinfer/server-diffusers.py",
        "COPY --from=go-builder /runtime /usr/local/bin/flexinfer-runtime",
    )
    for snippet in required:
        if snippet not in dockerfile:
            fail(f"Dockerfile.runtime-serving missing {snippet!r}")

    forbidden = (
        "AS llamacpp-builder",
        "AS ollama-builder",
        "INCLUDE_OLLAMA",
        "INCLUDE_LLAMACPP",
        "INCLUDE_STEAM",
        "INCLUDE_QUANTIZER",
        "COPY --from=llamacpp-builder",
        "COPY --from=ollama-builder",
        "steamcmd",
        "gptqmodel @",
        "oras/releases",
    )
    for snippet in forbidden:
        if snippet in dockerfile:
            fail(
                f"Dockerfile.runtime-serving reintroduced utility payload: {snippet!r}"
            )


def assert_gfx906_vllm_diagnostics_contract(dockerfile: str) -> None:
    required = (
        "PYTHONFAULTHANDLER=1",
        "TORCH_SHOW_CPP_STACKTRACES=1",
        "FLEXINFER_GFX906_VLLM_DIAGNOSTICS=1",
        "COPY build/scripts/install_vllm_gfx906_compat.py",
        "python3 /tmp/install_vllm_gfx906_compat.py",
    )
    for snippet in required:
        if snippet not in dockerfile:
            fail(f"Dockerfile.vllm-rocm-gfx906 missing diagnostics wiring: {snippet!r}")


def assert_gfx906_vllm_compat_hooks_contract(install_script: str) -> None:
    required_hooks = (
        "flexinfer_vllm_transformers_compat.py",
        "flexinfer_vllm_triton_compat.py",
        "flexinfer_vllm_torch_rocm_compat.py",
        "flexinfer_vllm_torch_init_compat.py",
        "flexinfer_vllm_worker_diagnostics.py",
    )
    for hook in required_hooks:
        if hook not in install_script:
            fail(
                "install_vllm_gfx906_compat.py no longer installs required hook: "
                f"{hook!r}"
            )

    init_contract = (
        '"_no_grad_normal_"',
        '"_no_grad_uniform_"',
    )
    for token in init_contract:
        if token not in install_script:
            fail(
                "install_vllm_gfx906_compat.py no longer patches torch.nn.init "
                f"target {token}"
            )


def assert_ci_fast_check_wiring(ci_yaml: str) -> None:
    fast_job = ci_yaml.find("runtime_patch_contracts:")
    serving_job = ci_yaml.find("publish_serving_rocm_gfx1100:")
    publish_job = ci_yaml.find("publish_unified_rocm_gfx1100:")
    if fast_job == -1:
        fail("runtime-publish CI is missing runtime_patch_contracts")
    if serving_job == -1:
        fail("runtime-publish CI is missing publish_serving_rocm_gfx1100")
    if publish_job == -1:
        fail("runtime-publish CI is missing publish_unified_rocm_gfx1100")
    if not (fast_job < serving_job < publish_job):
        fail(
            "runtime-publish CI order changed; expected runtime_patch_contracts "
            "-> publish_serving_rocm_gfx1100 -> publish_unified_rocm_gfx1100"
        )

    fast_job_body = ci_yaml[fast_job:serving_job]
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

    serving_job_body = ci_yaml[serving_job:publish_job]
    serving_required_snippets = (
        'filename="Dockerfile.runtime-serving"',
        "${REGISTRY}/runtime:rocm-gfx1100-serving",
        '--opt build-arg:INCLUDE_VLLM="true"',
        '--opt build-arg:INCLUDE_DIFFUSERS="true"',
        "build/server-diffusers.py",
        "build/scripts/vllm_qwen35_patches_nodiag.py",
        "build/scripts/vllm_gemma4_moe_gptq_patch.py",
    )
    for snippet in serving_required_snippets:
        if snippet not in serving_job_body:
            fail(f"publish_serving_rocm_gfx1100 missing {snippet!r}")

    missing_serving_rules = [
        path for path in SERVING_CHANGE_RULE_FILES if path not in serving_job_body
    ]
    if missing_serving_rules:
        fail(
            "publish_serving_rocm_gfx1100 rules do not include: "
            f"{missing_serving_rules}"
        )

    unified_job_body = ci_yaml[publish_job:]
    for path in UNIFIED_SERVING_ONLY_RULE_FILES:
        if path in unified_job_body:
            fail(f"serving-only file still auto-triggers unified runtime: {path}")


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
    build_script = read("build/build-runtime.sh")
    entrypoint = read("build/runtime-entrypoint.sh")
    serving_dockerfile = read("build/Dockerfile.runtime-serving")
    gfx906_vllm_dockerfile = read("build/Dockerfile.vllm-rocm-gfx906")
    gfx906_vllm_install_script = read("build/scripts/install_vllm_gfx906_compat.py")
    ci_yaml = read(".gitlab/ci/runtime-publish.yml")

    assert_patch_scripts_exist_and_parse()
    assert_runtime_yaml_patch_refs(runtime_yaml)
    assert_dockerfile_patch_order(dockerfile)
    assert_runtime_entrypoint_contract(entrypoint, build_script)
    assert_serving_dockerfile_contract(serving_dockerfile)
    assert_gfx906_vllm_diagnostics_contract(gfx906_vllm_dockerfile)
    assert_gfx906_vllm_compat_hooks_contract(gfx906_vllm_install_script)
    assert_ci_fast_check_wiring(ci_yaml)

    if run_tests:
        run_python_tests()

    print("runtime patch contract checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

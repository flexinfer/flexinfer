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
    "build/sunshine-headless.sh",
    "build/Dockerfile.runtime-serving",
    "build/Dockerfile.vllm-rocm-gfx906",
    "build/runtime-entrypoint.sh",
    "build/runtime.yaml",
    "build/scripts/install_vllm_gfx906_compat.py",
    "deploy/system/values-k3s.yaml",
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


def assert_optional_runtime_component_contract(dockerfile: str) -> None:
    required = (
        "FROM ${BASE_IMAGE} AS optional-components-disabled",
        "FROM llamacpp-${INCLUDE_LLAMACPP} AS llamacpp-output",
        "FROM ollama-${INCLUDE_OLLAMA} AS ollama-output",
        "COPY --from=llamacpp-output /opt/llamacpp/bin/ /opt/llamacpp/bin/",
        "COPY --from=ollama-output /opt/ollama/bin/ /usr/local/bin/",
    )
    for snippet in required:
        if snippet not in dockerfile:
            fail(f"Dockerfile.runtime optional component graph missing {snippet!r}")

    forbidden = (
        "COPY --from=llamacpp-builder",
        "COPY --from=ollama-builder",
    )
    for snippet in forbidden:
        if snippet in dockerfile:
            fail(
                "Dockerfile.runtime still forces a disabled component builder: "
                f"{snippet!r}"
            )


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
        "flexinfer_vllm_torch_tensor_compat.py",
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
        '"_no_grad_fill_"',
        '"_no_grad_zero_"',
    )
    for token in init_contract:
        if token not in install_script:
            fail(
                "install_vllm_gfx906_compat.py no longer patches torch.nn.init "
                f"target {token}"
            )

    tensor_contract = (
        '_patch_tensor_method("fill_")',
        '_patch_tensor_method("zero_")',
    )
    for token in tensor_contract:
        if token not in install_script:
            fail(
                "install_vllm_gfx906_compat.py no longer patches torch.Tensor "
                f"target {token}"
            )


def assert_sunshine_input_contract(launcher: str, values_yaml: str) -> None:
    required_launcher = (
        'export WLR_BACKENDS="${WLR_BACKENDS:-headless,libinput}"',
        "for dev in /dev/input/event* /dev/input/js*; do",
        'SESSION_SUPPLEMENTARY_GIDS="$GAMING_GID"',
        'append_session_gid "$gid"',
        '--groups "$SESSION_SUPPLEMENTARY_GIDS"',
    )
    for snippet in required_launcher:
        if snippet not in launcher:
            fail(f"sunshine-headless.sh missing input contract: {snippet!r}")
    if "export WLR_LIBINPUT_NO_DEVICES" in launcher:
        fail("sunshine-headless.sh must not set WLR_LIBINPUT_NO_DEVICES; it hides Sunshine uinput devices from Sway")
    for line in launcher.splitlines():
        stripped = line.strip()
        for forbidden in ("usermod ", "groupadd "):
            if stripped.startswith(forbidden):
                fail(
                    "sunshine-headless.sh must not mutate account databases at "
                    f"backend startup: {stripped!r}"
                )

    # An inference-only deployment intentionally omits privileged host input
    # mounts. Require the full input plumbing whenever a gaming profile is
    # enabled, without making a valid lease rollback fail this static gate.
    if re.search(r"^\s+gaming:\s*true\s*$", values_yaml, re.MULTILINE):
        required_mounts = (
            "mountPath: /dev/input",
            "mountPath: /dev/uinput",
            "mountPath: /run/udev",
            "path: /dev/input",
            "path: /dev/uinput",
            "path: /run/udev",
        )
        for snippet in required_mounts:
            if snippet not in values_yaml:
                fail(f"values-k3s.yaml missing gaming input mount: {snippet!r}")


def assert_validator_image_contract(values_yaml: str, qwen35_cache_yaml: str) -> None:
    image = (
        "registry.harbor.lan/flexinfer/model-tools@sha256:"
        "d1515f57e5e92ad62a8f3820eb428fc32436cf979fa130817fbe3f038edd14d1"
    )
    if f'validatorImage: "{image}"' not in values_yaml:
        fail("values-k3s.yaml must override stale validator images with model-tools")
    if f'image: "{image}"' not in qwen35_cache_yaml:
        fail("qwen35 cache must revalidate with the model-tools image")


def assert_long_context_gauntlet_contract(
    cronjob_yaml: str, experiment_yaml: str
) -> None:
    required_cronjob = (
        "name: qwen35-long-context-gauntlet",
        "kubernetes.io/hostname: cblevins-7900xtx",
        "key: node-role.kubernetes.io/control-plane",
        "key: dedicated",
        "operator: Equal",
        "value: gpu",
        "FLEXINFER_DIRECT_URL_TEMPLATE",
        "http://{model}.flexinfer-system.svc.cluster.local:8000",
        "http.client.RemoteDisconnected",
        "TRANSPORT_RETRIES",
        "results.append(proxy_smoke())",
    )
    for snippet in required_cronjob:
        if snippet not in cronjob_yaml:
            fail(f"Qwen long-context gauntlet missing scheduling fence: {snippet!r}")

    if "templateRef: qwen35-long-context-gauntlet" not in experiment_yaml:
        fail("Qwen 128K experiment no longer references its context gauntlet")


def assert_qwen128_production_contract(
    primary_yaml: str, sister_yaml: str
) -> None:
    required_sister = (
        "name: qwen35-35b-clean-gptq-workhorse-128k",
        "maxModelLen: 131072",
        'gpuMemoryUtilization: "0.94"',
        'hipVisibleDevices: "0"',
        "count: 2",
        "shared: 7900xtx-textgen",
        "priority: 700",
        "kubernetes.io/hostname: cblevins-7900xtx",
        "warmPolicy: ondemand",
        "minReplicas: 0",
        "coldStartTimeout: 25m",
        "- qwen35-128k",
        "- workhorse-128k",
        "- mills-council-128k",
    )
    for snippet in required_sister:
        if snippet not in sister_yaml:
            fail(f"Qwen 128K sister model contract regressed: {snippet!r}")
    if "forcePromotion: true" in sister_yaml:
        fail("Qwen 128K sister must yield the idle 7900 XTX to the warm video lane")

    required_primary = (
        "name: qwen35-35b-clean-gptq-workhorse",
        "maxModelLen: 131072",
        'gpuMemoryUtilization: "0.94"',
        "count: 1",
        "shared: 5930k-textgen",
        "kubernetes.io/hostname: cblevins-5930k",
        "minReplicas: 1",
        "coldStartTimeout: 25m",
        "- qwen35-128k",
        "- workhorse-128k",
        "- mills-council-128k",
    )
    for snippet in required_primary:
        if snippet not in primary_yaml:
            fail(f"Qwen 128K primary model contract regressed: {snippet!r}")


def assert_wan_video_warm_contract(wan_yaml: str) -> None:
    required = (
        "name: wan21-t2v-1p3b-gfx1100",
        "shared: 7900xtx-textgen",
        "priority: 500",
        "warmPolicy: primary",
        "minReplicas: 1",
    )
    for snippet in required:
        if snippet not in wan_yaml:
            fail(f"Wan warm video contract regressed: {snippet!r}")


def assert_ci_fast_check_wiring(ci_yaml: str) -> None:
    fast_job = ci_yaml.find("runtime_patch_contracts:")
    serving_job = ci_yaml.find("publish_serving_rocm_gfx1100:")
    publish_job = ci_yaml.find("publish_unified_rocm_gfx1100:")
    gaming_job = ci_yaml.find("publish_gaming_rocm_gfx1100:")
    if fast_job == -1:
        fail("runtime-publish CI is missing runtime_patch_contracts")
    if serving_job == -1:
        fail("runtime-publish CI is missing publish_serving_rocm_gfx1100")
    if publish_job == -1:
        fail("runtime-publish CI is missing publish_unified_rocm_gfx1100")
    if gaming_job == -1:
        fail("runtime-publish CI is missing publish_gaming_rocm_gfx1100")
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

    unified_job_body = ci_yaml[publish_job:gaming_job]
    for path in UNIFIED_SERVING_ONLY_RULE_FILES:
        if path in unified_job_body:
            fail(f"serving-only file still auto-triggers unified runtime: {path}")

    gaming_job_body = ci_yaml[gaming_job:]
    gaming_required_snippets = (
        'filename="Dockerfile.runtime"',
        "${REGISTRY}/runtime:rocm-gfx1100-gaming",
        '--opt build-arg:INCLUDE_GAMING="true"',
        '--opt build-arg:INCLUDE_VLLM="false"',
        '--opt build-arg:INCLUDE_LLAMACPP="false"',
        '--opt build-arg:INCLUDE_QUANTIZER="false"',
        "build/sunshine-headless.sh",
    )
    for snippet in gaming_required_snippets:
        if snippet not in gaming_job_body:
            fail(f"publish_gaming_rocm_gfx1100 missing {snippet!r}")


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
    sunshine_launcher = read("build/sunshine-headless.sh")
    values_yaml = read("deploy/system/values-k3s.yaml")
    qwen35_cache_yaml = read(
        "deploy/modelcaches/qwen35-35b-a3b-clean-gptq.yaml"
    )
    long_context_cronjob_yaml = read(
        "deploy/tasks/model-eval-gauntlet/qwen35-long-context-cronjob.yaml"
    )
    long_context_experiment_yaml = read(
        "deploy/experiments/qwen35-35b-clean-gptq-128k.yaml"
    )
    qwen128_production_yaml = read(
        "deploy/models/qwen35-35b-clean-gptq-workhorse-128k.yaml"
    )
    qwen128_primary_yaml = read(
        "deploy/models/qwen35-35b-clean-gptq-workhorse.yaml"
    )
    wan_video_yaml = read("deploy/models/wan21-t2v-1p3b-gfx1100.yaml")
    ci_yaml = read(".gitlab/ci/runtime-publish.yml")

    assert_patch_scripts_exist_and_parse()
    assert_runtime_yaml_patch_refs(runtime_yaml)
    assert_dockerfile_patch_order(dockerfile)
    assert_runtime_entrypoint_contract(entrypoint, build_script)
    assert_optional_runtime_component_contract(dockerfile)
    assert_serving_dockerfile_contract(serving_dockerfile)
    assert_gfx906_vllm_diagnostics_contract(gfx906_vllm_dockerfile)
    assert_gfx906_vllm_compat_hooks_contract(gfx906_vllm_install_script)
    assert_sunshine_input_contract(sunshine_launcher, values_yaml)
    assert_validator_image_contract(values_yaml, qwen35_cache_yaml)
    assert_long_context_gauntlet_contract(
        long_context_cronjob_yaml, long_context_experiment_yaml
    )
    assert_qwen128_production_contract(
        qwen128_primary_yaml, qwen128_production_yaml
    )
    assert_wan_video_warm_contract(wan_video_yaml)
    assert_ci_fast_check_wiring(ci_yaml)

    if run_tests:
        run_python_tests()

    print("runtime patch contract checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

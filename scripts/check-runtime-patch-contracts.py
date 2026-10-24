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
    "build/scripts/test_runtime_gptq_patch_composition.py",
    "build/scripts/test_verify_vllm_gfx1100_native_w4a16.py",
)

COMPILE_ONLY = PATCH_SCRIPTS + (
    "build/scripts/install_vllm_gfx906_compat.py",
    "build/scripts/quantize_gptq.py",
    "build/scripts/validate_quantized_artifact.py",
    "build/scripts/verify_vllm_gfx1100_native_w4a16.py",
)

CI_CHANGE_RULE_FILES = PATCH_SCRIPTS + (
    "build/scripts/test_runtime_gptq_patch_composition.py",
    "scripts/check-runtime-patch-contracts.py",
    "scripts/test_check_runtime_patch_contracts.py",
    # publish_runtime_rocm_gfx906 lives in the root CI file.
    ".gitlab-ci.yml",
    "build/build-runtime.sh",
    "build/Dockerfile.runtime",
    "build/sunshine-headless.sh",
    "build/Dockerfile.runtime-serving",
    "build/Dockerfile.runtime-serving-native-w4a16",
    "build/Dockerfile.vllm-rocm-gfx906",
    "build/runtime-entrypoint.sh",
    "build/runtime.yaml",
    "build/scripts/install_vllm_gfx906_compat.py",
    "build/scripts/verify_vllm_gfx1100_native_w4a16.py",
    "build/scripts/test_verify_vllm_gfx1100_native_w4a16.py",
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

# Runtime publish jobs that hardcode their buildctl args and runtime.env
# heredoc instead of running build/build-runtime.sh: (job, CI file, profile).
# 2026-10-12: publish_runtime_rocm_gfx906 still passed LLAMACPP_VERSION=b8173
# while the live gfx906 lane needed a newer llama.cpp, so the floating tag
# moved to a build without it and nothing noticed.
PUBLISH_JOB_PROFILES = (
    ("publish_runtime_rocm_gfx906", ".gitlab-ci.yml", "gfx906"),
    ("publish_unified_rocm_gfx1100", ".gitlab/ci/runtime-publish.yml", "gfx1100"),
    (
        "publish_serving_rocm_gfx1100",
        ".gitlab/ci/runtime-publish.yml",
        "gfx1100-serving",
    ),
    ("publish_gaming_rocm_gfx1100", ".gitlab/ci/runtime-publish.yml", "gfx1100-gaming"),
    (
        "publish_serving_rocm_gfx1100_native_w4a16",
        ".gitlab/ci/runtime-publish.yml",
        "gfx1100-native-w4a16",
    ),
)

# Top-level runtime.yaml versions; a profile may override each with the same key.
RUNTIME_YAML_VERSION_KEYS = {
    "GO_VERSION": "go_version",
    "LLAMACPP_VERSION": "llamacpp_version",
    "OLLAMA_VERSION": "ollama_version",
    "OLLAMA_GO_VERSION": "ollama_go_version",
}

# build arg -> (profile field, build-runtime.sh default). Mirrors the `pcfg`
# calls in build/build-runtime.sh; assert_build_script_defaults() keeps the
# two in step.
PROFILE_BUILD_ARGS = {
    "BASE_IMAGE": ("base_image", ""),
    "GPU_VENDOR": ("gpu_vendor", ""),
    "GPU_ARCH": ("gpu_arch", ""),
    "INCLUDE_VLLM": ("backends.vllm", "false"),
    "INCLUDE_LLAMACPP": ("backends.llamacpp", "true"),
    "INCLUDE_OLLAMA": ("backends.ollama", "true"),
    "INCLUDE_DIFFUSERS": ("backends.diffusers", "false"),
    "INCLUDE_STEAM": ("backends.steam", "false"),
    "INCLUDE_GAMING": ("backends.gaming", "false"),
    "INCLUDE_QUANTIZER": ("include_quantizer", "false"),
    "INCLUDE_TURBOQUANT": ("include_turboquant", "false"),
    "INCLUDE_BITSANDBYTES": ("include_bitsandbytes", "false"),
    "TRANSFORMERS_CONSTRAINT": ("transformers_constraint", ">=5.0"),
    "TRANSFORMERS_INSTALL_MODE": ("transformers_install_mode", "constraint"),
    "TRANSFORMERS_REPO": (
        "transformers_repo",
        "https://github.com/huggingface/transformers.git",
    ),
    "TRANSFORMERS_REF": ("transformers_ref", "main"),
    "VLLM_INSTALL_MODE": ("vllm_install_mode", "wheel"),
    "VLLM_VERSION": ("vllm_version", "0.17.0+rocm700"),
    "VLLM_EXTRA_INDEX_URL": (
        "vllm_extra_index_url",
        "https://wheels.vllm.ai/rocm/0.17.0/rocm700",
    ),
    "VLLM_REPO": ("vllm_repo", "https://github.com/vllm-project/vllm.git"),
    "VLLM_REF": ("vllm_ref", "main"),
    "VLLM_SOURCE_PATCH_SCRIPT": ("vllm_source_patch_script", ""),
    "VLLM_EXTRA_DEPS_PROFILE": ("vllm_extra_deps_profile", "full"),
    "SKIP_GEMMA4_MOE_PATCH": ("skip_gemma4_moe_patch", "false"),
    "INSTALL_QWEN35_FASTPATH": ("install_qwen35_fastpath", "true"),
    "TURBOQUANT_INSTALL_MODE": ("turboquant_install_mode", "none"),
    "TURBOQUANT_VERSION": ("turboquant_version", "1.4.0"),
    "TURBOQUANT_REPO": (
        "turboquant_repo",
        "https://github.com/Alberto-Codes/turboquant-vllm.git",
    ),
    "TURBOQUANT_REF": ("turboquant_ref", "main"),
    "TURBOQUANT_SOURCE_PATCH_SCRIPT": ("turboquant_source_patch_script", ""),
    "CUDA_ARCHITECTURES": ("cuda_architectures", "52"),
}

# Args for a component the profile leaves out are never read by its build
# stages (Dockerfile.runtime-gfx906 declares VLLM_INSTALL_MODE but no stage
# consumes it), so they are only compared when the component is in.
GATED_BUILD_ARG_PREFIXES = (
    ("VLLM_", "INCLUDE_VLLM"),
    ("TURBOQUANT_", "INCLUDE_TURBOQUANT"),
)

# runtime.env keys build-runtime.sh derives rather than copying from the
# profile's env map. A CI heredoc may omit them (the entrypoint has defaults);
# when it writes one, the value must match.
DERIVED_RUNTIME_ENV_KEYS = {
    "FLEXINFER_TRANSFORMERS_PACKAGE": "TRANSFORMERS_PACKAGE",
    "FLEXINFER_TRANSFORMERS_RUNTIME_INSTALL": "TRANSFORMERS_RUNTIME_INSTALL",
    "SKIP_GEMMA4_MOE_PATCH": "SKIP_GEMMA4_MOE_PATCH",
}

# {(job, kind, key): reason} for a divergence that must ship before the two
# files can be reconciled. Each is printed on every run, and an entry that no
# longer matches a live divergence fails the check, so whoever reconciles the
# two files must also delete the waiver. The seven found when this check
# landed (b26dcef56) were reconciled on 2026-10-12.
KNOWN_PUBLISH_JOB_DIVERGENCES: dict[tuple[str, str, str], str] = {}


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


def assert_qwen35_gptq_checkpoint_order_path(patch_script: str) -> None:
    # The old Qwen-local reference fallback could unpack qweight after an
    # ExLlama shuffle and was therefore unsafe. This stage must first remove
    # that stale fallback and skip the shuffle. The later Gemma compatibility
    # stage then installs the coherent fallback against checkpoint-order data.
    forbidden = (
        "unpacked_qweight",
        "torch.version.hip is not None and self.quant_config.weight_bits == 4",
    )
    for snippet in forbidden:
        if snippet in patch_script:
            fail(
                "vllm_qwen35_patches_nodiag.py reintroduced the ROCm GPTQ "
                f"reference fallback ({snippet!r}) before the shuffle-skip "
                "stage; that ordering can corrupt all quantized projections"
            )

    if "FLEXINFER_QWEN35_GPTQ_ROCM_REFERENCE_PATCH" not in patch_script:
        fail(
            "vllm_qwen35_patches_nodiag.py lost the stale-image guard that "
            "fails the build when gptq.py still carries the removed ROCm "
            "reference fallback"
        )

    # Removing the stale fallback is necessary but not sufficient:
    # ops.gptq_shuffle() permutes qweight into an ExLlama layout that the ROCm
    # reference implementation cannot unpack. Keep checkpoint-order qweight so
    # the following compatibility stage can dequantize it coherently.
    if "FLEXINFER_QWEN35_GPTQ_ROCM_SHUFFLE_SKIP" not in patch_script:
        fail(
            "vllm_qwen35_patches_nodiag.py lost the ROCm gptq_shuffle skip "
            "guard (FLEXINFER_QWEN35_GPTQ_ROCM_SHUFFLE_SKIP); without it every "
            "4-bit GPTQ projection is served with ExLlama-permuted rows and "
            "generation degenerates into token salad"
        )


def assert_legacy_gptq_uses_reference_after_qwen_shuffle_skip(
    patch_script: str,
) -> None:
    if 'if "FLEXINFER_QWEN35_GPTQ_ROCM_SHUFFLE_SKIP" in src:' in patch_script:
        fail(
            "vllm_gemma4_moe_gptq_patch.py bypasses the coherent ROCm GPTQ "
            "reference fallback when the Qwen shuffle-skip marker is present; "
            "full 27B tensor shapes corrupt on vLLM 0.17 stock gptq_gemm"
        )
    for snippet in (
        "GEMMA4_GPTQ_ROCM_REFERENCE_PATCH",
        "unpacked_qweight",
        "src = src.replace(old_apply, new_apply, 1)",
    ):
        if snippet not in patch_script:
            fail(f"legacy ROCm GPTQ reference fallback lost {snippet!r}")


def assert_native_w4a16_dockerfile_contract(dockerfile: str) -> None:
    required = (
        "vllm/vllm-openai-rocm:v0.32.0",
        "PYTORCH_ROCM_ARCH=${GPU_ARCH}",
        "verify_vllm_gfx1100_native_w4a16.py",
        'ENTRYPOINT ["vllm", "serve"]',
    )
    for snippet in required:
        if snippet not in dockerfile:
            fail(f"native gfx1100 W4A16 Dockerfile missing {snippet!r}")
    # 2026-10-24: the 0.32 base wheel ships gfx1100 code objects, so the image
    # must not rebuild vLLM from source any more (that path cost ~1 h per build
    # and pinned a second copy of the version string). The ISA guarantee moved
    # into the verify script.
    for snippet in ("git clone", "pip wheel", "VLLM_VERSION_OVERRIDE"):
        if snippet in dockerfile:
            fail(
                "native gfx1100 W4A16 Dockerfile still rebuilds vLLM from source "
                f"({snippet!r}); the 0.32 base wheel already carries gfx1100"
            )

    forbidden = (
        "vllm_qwen35_patches_nodiag.py",
        "vllm_gemma4_moe_gptq_patch.py",
    )
    for snippet in forbidden:
        if snippet in dockerfile:
            fail(
                "native gfx1100 W4A16 image must bypass legacy source patches: "
                f"{snippet}"
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
    # 2026-09-13 (issue #74 S1 incident): in runtime-manager mode /models is
    # the node-wide cache, and the in-place config normalizers rewrote a
    # co-located dedicated Qwen3.5 lane's architectures. The pass must stay
    # opt-in there and unconditional only for dedicated model pods.
    if "FLEXINFER_RUNTIME_NORMALIZE_MODEL_CONFIGS:-0" not in entrypoint:
        fail(
            "runtime-entrypoint.sh no longer gates node-wide config normalization in runtime-manager mode"
        )
    if "Skipping model config normalization in runtime-manager mode" not in entrypoint:
        fail(
            "runtime-entrypoint.sh no longer logs the skipped node-wide config normalization"
        )


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
        fail(
            "sunshine-headless.sh must not set WLR_LIBINPUT_NO_DEVICES; it hides Sunshine uinput devices from Sway"
        )
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
    promoted_image = (
        "registry.harbor.lan/flexinfer/model-tools@sha256:"
        "41f948bafa42c154a17ac567a0ade1f49fd3e17e6241c7e8bb44dc7a48265f30"
    )
    qwen35_revalidation_image = (
        "registry.harbor.lan/flexinfer/model-tools@sha256:"
        "d1515f57e5e92ad62a8f3820eb428fc32436cf979fa130817fbe3f038edd14d1"
    )
    for key in ("modelToolsImage", "validatorImage"):
        if f'{key}: "{promoted_image}"' not in values_yaml:
            fail(f"values-k3s.yaml must pin {key} to the promoted model-tools image")
    if f'image: "{qwen35_revalidation_image}"' not in qwen35_cache_yaml:
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


def assert_qwen128_production_contract(primary_yaml: str, sister_yaml: str) -> None:
    required_sister = (
        "name: qwen35-35b-clean-gptq-workhorse-128k",
        # 2026-08-16 context push (!976): both 35B lanes raised to the
        # qualified 184320 envelope (needle-proven 5/5 @180,930).
        "maxModelLen: 184320",
        'gpuMemoryUtilization: "0.94"',
        'hipVisibleDevices: "0"',
        "count: 2",
        "shared: 7900xtx-textgen",
        # 2026-07-18 psyche fleet layout (MR !862): demoted below the warm
        # qwen35-9b-ablit-rp text-lane leader (500); on-demand only.
        "priority: 300",
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
        # 2026-08-16 context push (!976): 184320 on the 5930k lane too.
        "maxModelLen: 184320",
        'gpuMemoryUtilization: "0.94"',
        "count: 1",
        "shared: 5930k-textgen",
        "kubernetes.io/hostname: cblevins-5930k",
        # 2026-07-18 psyche fleet layout (MR !862): scale-to-zero; the 5930k
        # card belongs to the rehomed wan video lanes now.
        "minReplicas: 0",
        "coldStartTimeout: 25m",
        "- qwen35-128k",
        "- workhorse-128k",
        "- mills-council-128k",
    )
    for snippet in required_primary:
        if snippet not in primary_yaml:
            fail(f"Qwen 128K primary model contract regressed: {snippet!r}")


def assert_wan_video_parked_contract(wan_yaml: str) -> None:
    required = (
        "name: wan21-t2v-1p3b-gfx1100",
        # 2026-07-24 twin canary window: WAN remains on cblevins-5930k but is
        # parked so higher-priority members can acquire the shared GPU.
        "shared: 5930k-textgen",
        "priority: 500",
        "minReplicas: 0",
        "warmPolicy: ondemand",
    )
    for snippet in required:
        if snippet not in wan_yaml:
            fail(f"Wan parked video contract regressed: {snippet!r}")


def assert_flash_loader_image_contract(
    controller_source: str, deployment_source: str, values_yaml: str
) -> None:
    published_ref = "registry.harbor.lan/flexinfer/flexinfer-flash-loader:master"
    if published_ref not in controller_source:
        fail(
            "controller flash-loader default must use the always-published :master tag"
        )
    if published_ref not in values_yaml:
        fail("chart flash-loader default must use the always-published :master tag")
    if "ImagePullPolicy: corev1.PullAlways" not in deployment_source:
        fail("flash-loader init container must refresh its mutable :master tag")


def assert_ci_fast_check_wiring(ci_yaml: str) -> None:
    fast_job = ci_yaml.find("runtime_patch_contracts:")
    serving_job = ci_yaml.find("publish_serving_rocm_gfx1100:")
    native_w4a16_job = ci_yaml.find("publish_serving_rocm_gfx1100_native_w4a16:")
    publish_job = ci_yaml.find("publish_unified_rocm_gfx1100:")
    gaming_job = ci_yaml.find("publish_gaming_rocm_gfx1100:")
    if fast_job == -1:
        fail("runtime-publish CI is missing runtime_patch_contracts")
    if serving_job == -1:
        fail("runtime-publish CI is missing publish_serving_rocm_gfx1100")
    if native_w4a16_job == -1:
        fail(
            "runtime-publish CI is missing " "publish_serving_rocm_gfx1100_native_w4a16"
        )
    if publish_job == -1:
        fail("runtime-publish CI is missing publish_unified_rocm_gfx1100")
    if gaming_job == -1:
        fail("runtime-publish CI is missing publish_gaming_rocm_gfx1100")
    if not (fast_job < serving_job < native_w4a16_job < publish_job):
        fail(
            "runtime-publish CI order changed; expected runtime_patch_contracts "
            "-> publish_serving_rocm_gfx1100 -> native W4A16 canary "
            "-> publish_unified_rocm_gfx1100"
        )

    fast_job_body = ci_yaml[fast_job:serving_job]
    required_snippets = (
        "stage: lint",
        "needs: []",
        "python3 scripts/check-runtime-patch-contracts.py",
        "python3 scripts/test_check_runtime_patch_contracts.py",
        "python3 build/scripts/test_modules_in_block_to_quantize.py",
        "python3 build/scripts/test_validate_quantized_artifact.py",
        "python3 build/scripts/test_runtime_gptq_patch_composition.py",
        "python3 build/scripts/test_verify_vllm_gfx1100_native_w4a16.py",
    )
    for snippet in required_snippets:
        if snippet not in fast_job_body:
            fail(f"runtime_patch_contracts missing {snippet!r}")

    missing_rules = [path for path in CI_CHANGE_RULE_FILES if path not in fast_job_body]
    if missing_rules:
        fail(f"runtime_patch_contracts rules do not include: {missing_rules}")

    # The pin-drift tests skip without PyYAML, which only runtime_pin_drift installs.
    drift_job_body = ci_job_body(ci_yaml, "runtime_pin_drift") or ""
    for snippet in (
        "python3 scripts/test_check_runtime_pin_drift.py",
        "- scripts/test_check_runtime_pin_drift.py",
    ):
        if snippet not in drift_job_body:
            fail(f"runtime_pin_drift missing {snippet!r}")

    serving_job_body = ci_yaml[serving_job:native_w4a16_job]
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

    native_w4a16_job_body = ci_yaml[native_w4a16_job:publish_job]
    native_required_snippets = (
        'filename="Dockerfile.runtime-serving-native-w4a16"',
        'build-arg:BASE_IMAGE="vllm/vllm-openai-rocm:v0.32.0"',
        "${REGISTRY}/runtime:rocm-gfx1100-v032-native-w4a16",
        "build/scripts/verify_vllm_gfx1100_native_w4a16.py",
        "build/scripts/test_verify_vllm_gfx1100_native_w4a16.py",
    )
    for snippet in native_required_snippets:
        if snippet not in native_w4a16_job_body:
            fail("publish_serving_rocm_gfx1100_native_w4a16 missing " f"{snippet!r}")

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


# ---------------------------------------------------------------------------
# CI publish jobs vs build/runtime.yaml
# ---------------------------------------------------------------------------
# runtime_patch_contracts runs on python:3.12-alpine with no pip install, so
# runtime.yaml is read with the small parser below rather than PyYAML.

_YAML_KEY_RE = re.compile(
    r"""^(?P<key>"(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^\s'"#\-][^:]*?)"""
    r"""\s*:(?:\s+(?P<value>.*))?$"""
)


class SimpleYAMLError(ValueError):
    pass


def _strip_yaml_comment(line: str) -> str:
    """Drop a `#` comment that is outside quotes and starts a token."""
    quote = None
    i = 0
    while i < len(line):
        ch = line[i]
        if quote == '"':
            if ch == "\\":
                i += 2
                continue
            if ch == '"':
                quote = None
        elif quote == "'":
            if ch == "'":
                quote = None
        elif ch in "\"'" and (i == 0 or line[i - 1] in " \t[{,:-"):
            quote = ch
        elif ch == "#" and (i == 0 or line[i - 1] in " \t"):
            return line[:i].rstrip()
        i += 1
    return line.rstrip()


def _yaml_scalar(raw: str, lineno: int):
    raw = raw.strip()
    if raw in ("", "~", "null", "Null", "NULL"):
        return None
    if raw == "[]":
        return []
    if raw == "{}":
        return {}
    if raw[0] == '"':
        if len(raw) < 2 or raw[-1] != '"':
            raise SimpleYAMLError(f"line {lineno}: unterminated double-quoted scalar")
        body = raw[1:-1]
        escapes = {'"': '"', "\\": "\\", "/": "/", "n": "\n", "t": "\t"}
        out = []
        i = 0
        while i < len(body):
            if body[i] == "\\":
                if i + 1 >= len(body) or body[i + 1] not in escapes:
                    raise SimpleYAMLError(f"line {lineno}: unsupported escape in {raw}")
                out.append(escapes[body[i + 1]])
                i += 2
                continue
            out.append(body[i])
            i += 1
        return "".join(out)
    if raw[0] == "'":
        if len(raw) < 2 or raw[-1] != "'":
            raise SimpleYAMLError(f"line {lineno}: unterminated single-quoted scalar")
        return raw[1:-1].replace("''", "'")
    if raw[0] in "&*!|>[{%@`":
        raise SimpleYAMLError(f"line {lineno}: unsupported YAML construct {raw!r}")
    return raw


def parse_simple_yaml(text: str) -> dict:
    """Parse the block-style YAML subset build/runtime.yaml is written in.

    Supports nested mappings by indentation, plain and quoted scalars,
    full-line and trailing comments, block sequences of scalars, and the
    empty flow collections `[]` / `{}`. Scalars stay strings, matching
    `yq -r` (`true` -> "true", `"1"` -> "1"); `null`, `~` and empty values
    become None. Anything else (anchors, block scalars, flow collections with
    content, tabs, multiple documents) raises SimpleYAMLError rather than
    being misread.
    """
    lines: list[tuple[int, str, int]] = []
    for lineno, raw in enumerate(text.splitlines(), 1):
        stripped = _strip_yaml_comment(raw)
        if not stripped.strip():
            continue
        body = stripped.lstrip(" ")
        if body.startswith("\t"):
            raise SimpleYAMLError(f"line {lineno}: tab indentation is not supported")
        if body in ("---", "..."):
            raise SimpleYAMLError(f"line {lineno}: document markers are not supported")
        lines.append((len(stripped) - len(body), body, lineno))
    if not lines:
        return {}
    if lines[0][0] != 0:
        raise SimpleYAMLError(f"line {lines[0][2]}: document must start at column 0")

    def is_item(body: str) -> bool:
        return body == "-" or body.startswith("- ")

    def parse_sequence(i: int, indent: int):
        items = []
        while i < len(lines) and lines[i][0] == indent and is_item(lines[i][1]):
            _, body, lineno = lines[i]
            value = body[1:].strip()
            if not value or _YAML_KEY_RE.match(value):
                raise SimpleYAMLError(
                    f"line {lineno}: only sequences of scalars are supported"
                )
            items.append(_yaml_scalar(value, lineno))
            i += 1
        return items, i

    def parse_mapping(i: int, indent: int):
        result: dict = {}
        while i < len(lines) and lines[i][0] == indent and not is_item(lines[i][1]):
            _, body, lineno = lines[i]
            match = _YAML_KEY_RE.match(body)
            if not match:
                raise SimpleYAMLError(
                    f"line {lineno}: expected 'key: value', got {body!r}"
                )
            key = _yaml_scalar(match.group("key"), lineno)
            if key in result:
                raise SimpleYAMLError(f"line {lineno}: duplicate key {key!r}")
            value = match.group("value")
            i += 1
            if value is not None and value.strip():
                result[key] = _yaml_scalar(value, lineno)
            elif i < len(lines) and lines[i][0] > indent:
                result[key], i = parse_node(i, lines[i][0])
            elif i < len(lines) and lines[i][0] == indent and is_item(lines[i][1]):
                result[key], i = parse_sequence(i, indent)
            else:
                result[key] = None
        return result, i

    def parse_node(i: int, indent: int):
        if is_item(lines[i][1]):
            node, i = parse_sequence(i, indent)
        else:
            node, i = parse_mapping(i, indent)
        if i < len(lines) and lines[i][0] > indent:
            raise SimpleYAMLError(f"line {lines[i][2]}: unexpected indentation")
        return node, i

    node, i = parse_node(0, 0)
    if i != len(lines):
        raise SimpleYAMLError(f"line {lines[i][2]}: could not parse from here")
    if not isinstance(node, dict):
        raise SimpleYAMLError("runtime.yaml must be a mapping at the top level")
    return node


def _lookup(mapping: dict, dotted: str):
    node = mapping
    for part in dotted.split("."):
        if not isinstance(node, dict) or part not in node:
            return None
        node = node[part]
    return node


def _as_yq_string(value) -> str:
    # yq -r prints a YAML null as "null"; build-runtime.sh writes it verbatim.
    return "null" if value is None else str(value)


def expected_build_args(runtime_cfg: dict, profile: str) -> dict[str, tuple[str, str]]:
    """What build/build-runtime.sh would pass for `profile`.

    Returns {build arg: (value, where the value came from)}.
    """
    profiles = runtime_cfg.get("profiles") or {}
    spec = profiles.get(profile)
    if not isinstance(spec, dict):
        raise SimpleYAMLError(f"build/runtime.yaml has no profile {profile!r}")

    def pcfg(field: str, default: str) -> tuple[str, str]:
        # build-runtime.sh: an unset, null or empty profile value takes the default.
        value = _lookup(spec, field)
        if value is None or value == "" or value == "null":
            return default, f"build-runtime.sh default for profiles.{profile}.{field}"
        return str(value), f"profiles.{profile}.{field}"

    args: dict[str, tuple[str, str]] = {}
    for arg, key in RUNTIME_YAML_VERSION_KEYS.items():
        value = spec.get(key)
        if value is not None and value != "":
            args[arg] = (str(value), f"profiles.{profile}.{key}")
        else:
            args[arg] = (_as_yq_string(runtime_cfg.get(key)), key)
    for arg, (field, default) in PROFILE_BUILD_ARGS.items():
        args[arg] = pcfg(field, default)
    args["AMDGPU_TARGETS"] = pcfg("amdgpu_targets", args["GPU_ARCH"][0])
    dev_image = "rocm_dev_image" if args["GPU_VENDOR"][0] == "amd" else "cuda_dev_image"
    args["LLAMACPP_BUILD_IMAGE"] = pcfg(dev_image, "")

    mode, mode_src = args["TRANSFORMERS_INSTALL_MODE"]
    if mode == "source":
        package = args["TRANSFORMERS_REPO"][0]
        if not package.startswith("git+"):
            package = f"git+{package}"
        args["TRANSFORMERS_PACKAGE"] = (
            f"{package}@{args['TRANSFORMERS_REF'][0]}",
            mode_src,
        )
        args["TRANSFORMERS_RUNTIME_INSTALL"] = ("fallback", mode_src)
    elif mode == "constraint":
        constraint = args["TRANSFORMERS_CONSTRAINT"][0]
        args["TRANSFORMERS_PACKAGE"] = (f"transformers{constraint}", mode_src)
        args["TRANSFORMERS_RUNTIME_INSTALL"] = ("fallback", mode_src)
    elif mode in ("none", ""):
        args["TRANSFORMERS_PACKAGE"] = ("", mode_src)
        args["TRANSFORMERS_RUNTIME_INSTALL"] = ("disabled", mode_src)
    return args


def expected_runtime_env(runtime_cfg: dict, profile: str) -> dict[str, str]:
    """The keys build-runtime.sh always writes to build/.runtime-env."""
    spec = (runtime_cfg.get("profiles") or {}).get(profile) or {}
    env = {
        "GPU_VENDOR": _as_yq_string(spec.get("gpu_vendor")),
        "GPU_ARCH": _as_yq_string(spec.get("gpu_arch")),
    }
    for key, value in (spec.get("env") or {}).items():
        env[key] = _as_yq_string(value)
    return env


def ci_job_body(ci_text: str, job: str) -> str | None:
    """Return a top-level CI job's text, from its key to the next column-0 line."""
    match = re.search(rf"^{re.escape(job)}:[ \t]*(?:#.*)?$", ci_text, re.MULTILINE)
    if not match:
        return None
    end = re.search(r"^\S", ci_text[match.end() :], re.MULTILINE)
    return ci_text[match.start() : match.end() + (end.start() if end else len(ci_text))]


_BUILD_ARG_RE = re.compile(
    r"""build-arg:(?P<key>[A-Za-z0-9_]+)=(?:"(?P<dq>(?:[^"\\]|\\.)*)"|'(?P<sq>[^']*)'|(?P<bare>[^\s"'\\]*))"""
)


def ci_build_args(job_body: str) -> tuple[dict[str, str], list[str]]:
    """Return ({build arg: literal value}, problems) for a job's buildctl call."""
    args: dict[str, str] = {}
    problems: list[str] = []
    found = 0
    for match in _BUILD_ARG_RE.finditer(job_body):
        found += 1
        key = match.group("key")
        value = next(
            (
                match.group(g)
                for g in ("dq", "sq", "bare")
                if match.group(g) is not None
            ),
            "",
        )
        if key in args:
            problems.append(f"passes build arg {key} twice")
        args[key] = value
    if found != job_body.count("build-arg:"):
        problems.append(
            "has a build-arg the contract check cannot parse "
            '(expected --opt build-arg:KEY="value")'
        )
    return args, problems


_RUNTIME_ENV_HEREDOC_RE = re.compile(
    r"""cat\s*>\s*build/\.runtime-env\s*<<-?\s*(?P<q>['"]?)(?P<tag>\w+)(?P=q)[ \t]*\n"""
    r"""(?P<body>.*?)\n[ \t]*(?P=tag)[ \t]*$""",
    re.DOTALL | re.MULTILINE,
)


def ci_runtime_env(job_body: str) -> tuple[dict[str, str] | None, list[str]]:
    """Return ({key: value}, problems) from the job's build/.runtime-env heredoc."""
    match = _RUNTIME_ENV_HEREDOC_RE.search(job_body)
    if not match:
        return None, []
    env: dict[str, str] = {}
    problems: list[str] = []
    for line in match.group("body").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, value = line.partition("=")
        if not sep or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            problems.append(
                f"runtime.env heredoc has a line that is not KEY=VALUE: {line!r}"
            )
            continue
        if key in env:
            problems.append(f"runtime.env heredoc sets {key} twice")
        env[key] = value
    return env, problems


def ci_output_names(job_body: str) -> set[str]:
    names: set[str] = set()
    for match in re.finditer(r"name=([^\"\\\s]+)", job_body):
        names.update(n for n in match.group(1).split(",") if n)
    return names


def publish_job_divergences(
    job: str, ci_file: str, job_body: str, runtime_cfg: dict, profile: str
) -> tuple[list[tuple[str, str, str]], list[str]]:
    """Compare one publish job with its runtime.yaml profile.

    Returns (divergences, problems). Divergences are (kind, key, message)
    and may be waived in KNOWN_PUBLISH_JOB_DIVERGENCES; problems mean the job
    could not be checked at all and are never waived.
    """
    where = f"{job} ({ci_file})"
    spec = (runtime_cfg.get("profiles") or {}).get(profile) or {}
    expected = expected_build_args(runtime_cfg, profile)
    divergences: list[tuple[str, str, str]] = []

    passed, problems = ci_build_args(job_body)
    problems = [f"{where} {p}" for p in problems]
    for required in ("BASE_IMAGE", "GPU_ARCH"):
        if required not in passed:
            problems.append(
                f"{where} no longer passes build arg {required}; the contract check "
                "cannot tell which profile it builds"
            )
    for key, value in sorted(passed.items()):
        if key not in expected or "$" in value:
            # Not derived from runtime.yaml, or computed when the job runs.
            continue
        gate = next((g for p, g in GATED_BUILD_ARG_PREFIXES if key.startswith(p)), None)
        if gate and expected[gate][0] != "true":
            continue
        want, source = expected[key]
        if value != want:
            divergences.append(
                (
                    "build-arg",
                    key,
                    f'{where} passes {key}="{value}" but build/runtime.yaml '
                    f'resolves it to "{want}" ({source})',
                )
            )

    dockerfile = str(spec.get("dockerfile") or "build/Dockerfile.runtime")
    match = re.search(r"""--opt\s+filename=["']?([^"'\s\\]+)""", job_body)
    if not match:
        problems.append(
            f"{where} has no --opt filename=...; cannot check its Dockerfile"
        )
    elif f"build/{match.group(1)}" != dockerfile:
        divergences.append(
            (
                "dockerfile",
                "filename",
                f"{where} builds build/{match.group(1)} but build/runtime.yaml "
                f"profiles.{profile}.dockerfile is {dockerfile}",
            )
        )

    tag = str(spec.get("tag") or "")
    names = ci_output_names(job_body)
    for name in (
        f"${{REGISTRY}}/{tag}",
        f"${{REGISTRY}}/{tag}-${{CI_COMMIT_SHORT_SHA}}",
    ):
        if name not in names:
            divergences.append(
                (
                    "image-tag",
                    name,
                    f"{where} does not push {name}; build/runtime.yaml profiles.{profile}.tag "
                    f"is {tag} and the pin-drift gate recognizes a profile build only by "
                    "its <tag>-<commit sha> tag",
                )
            )

    if "build/.runtime-env" in job_body:
        env, env_problems = ci_runtime_env(job_body)
        problems.extend(f"{where} {p}" for p in env_problems)
        if env is None:
            problems.append(
                f"{where} writes build/.runtime-env but no heredoc was parsed"
            )
        else:
            want_env = expected_runtime_env(runtime_cfg, profile)
            for key in sorted(set(want_env) | set(env)):
                field = {"GPU_VENDOR": "gpu_vendor", "GPU_ARCH": "gpu_arch"}.get(
                    key, f"env.{key}"
                )
                if key in want_env and key not in env:
                    msg = (
                        f"{where} runtime.env heredoc is missing {key}="
                        f'"{want_env[key]}" that build/runtime.yaml profiles.{profile}.{field} sets'
                    )
                elif key in DERIVED_RUNTIME_ENV_KEYS and key not in want_env:
                    want, source = expected[DERIVED_RUNTIME_ENV_KEYS[key]]
                    if env[key] == want:
                        continue
                    msg = (
                        f'{where} runtime.env heredoc sets {key}="{env[key]}" but '
                        f'build-runtime.sh derives "{want}" from build/runtime.yaml ({source})'
                    )
                elif key not in want_env:
                    msg = (
                        f'{where} runtime.env heredoc sets {key}="{env[key]}" which '
                        f"build/runtime.yaml profiles.{profile}.env does not declare"
                    )
                elif env[key] != want_env[key]:
                    msg = (
                        f'{where} runtime.env heredoc sets {key}="{env[key]}" but '
                        f'build/runtime.yaml profiles.{profile}.{field} is "{want_env[key]}"'
                    )
                else:
                    continue
                divergences.append(("runtime.env", key, msg))
    return divergences, problems


def assert_build_script_defaults(build_script: str) -> None:
    """PROFILE_BUILD_ARGS must carry build-runtime.sh's literal pcfg defaults."""
    defaults = {
        m.group(1): m.group(2)
        for m in re.finditer(
            r'pcfg "\$\{profile\}" "([\w.]+)"(?: "([^"$]*)")?\)', build_script
        )
    }
    stale = []
    for arg, (field, default) in PROFILE_BUILD_ARGS.items():
        if field not in defaults:
            stale.append(f"{arg}: build-runtime.sh no longer reads {field} via pcfg")
        elif (defaults[field] or "") != default:
            stale.append(
                f"{arg}: build-runtime.sh defaults {field} to {defaults[field]!r}, "
                f"PROFILE_BUILD_ARGS says {default!r}"
            )
    if stale:
        fail(
            "scripts/check-runtime-patch-contracts.py PROFILE_BUILD_ARGS is out of "
            "step with build/build-runtime.sh: " + "; ".join(stale)
        )


def assert_publish_jobs_match_runtime_yaml(
    runtime_yaml: str, ci_files: dict[str, str]
) -> list[str]:
    """Fail when a hardcoded publish job diverges from build/runtime.yaml.

    Returns the waived divergences so main() can print them.
    """
    try:
        runtime_cfg = parse_simple_yaml(runtime_yaml)
    except SimpleYAMLError as exc:
        fail(f"build/runtime.yaml: {exc}")
    errors: list[str] = []
    waived: list[str] = []
    seen: set[tuple[str, str, str]] = set()
    for job, ci_file, profile in PUBLISH_JOB_PROFILES:
        body = ci_job_body(ci_files[ci_file], job)
        if body is None:
            errors.append(f"{ci_file} no longer defines {job}")
            continue
        if profile not in (runtime_cfg.get("profiles") or {}):
            errors.append(
                f"build/runtime.yaml no longer defines profile {profile} ({job})"
            )
            continue
        divergences, problems = publish_job_divergences(
            job, ci_file, body, runtime_cfg, profile
        )
        errors.extend(problems)
        for kind, key, message in divergences:
            waiver = KNOWN_PUBLISH_JOB_DIVERGENCES.get((job, kind, key))
            if waiver:
                seen.add((job, kind, key))
                waived.append(f"{message} [known divergence: {waiver}]")
            else:
                errors.append(message)
    for job, kind, key in sorted(set(KNOWN_PUBLISH_JOB_DIVERGENCES) - seen):
        errors.append(
            f"KNOWN_PUBLISH_JOB_DIVERGENCES entry ({job}, {kind}, {key}) no longer "
            "matches a divergence; delete it from scripts/check-runtime-patch-contracts.py"
        )
    if errors:
        fail(
            "runtime publish jobs diverge from build/runtime.yaml:\n  - "
            + "\n  - ".join(errors)
        )
    return waived


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
    native_w4a16_dockerfile = read("build/Dockerfile.runtime-serving-native-w4a16")
    gfx906_vllm_dockerfile = read("build/Dockerfile.vllm-rocm-gfx906")
    gfx906_vllm_install_script = read("build/scripts/install_vllm_gfx906_compat.py")
    sunshine_launcher = read("build/sunshine-headless.sh")
    values_yaml = read("deploy/system/values-k3s.yaml")
    chart_values_yaml = read("charts/flexinfer/values.yaml")
    qwen35_cache_yaml = read("deploy/modelcaches/qwen35-35b-a3b-clean-gptq.yaml")
    long_context_cronjob_yaml = read(
        "deploy/tasks/model-eval-gauntlet/qwen35-long-context-cronjob.yaml"
    )
    long_context_experiment_yaml = read(
        "deploy/experiments/qwen35-35b-clean-gptq-128k.yaml"
    )
    qwen128_production_yaml = read(
        "deploy/models/qwen35-35b-clean-gptq-workhorse-128k.yaml"
    )
    qwen128_primary_yaml = read("deploy/models/qwen35-35b-clean-gptq-workhorse.yaml")
    wan_video_yaml = read("deploy/models/wan21-t2v-1p3b-gfx1100.yaml")
    flash_loader_source = read("controllers/flash_loader.go")
    model_deployment_source = read("controllers/model_deployment.go")
    ci_yaml = read(".gitlab/ci/runtime-publish.yml")
    root_ci_yaml = read(".gitlab-ci.yml")

    qwen35_patch_script = read("build/scripts/vllm_qwen35_patches_nodiag.py")
    gemma4_patch_script = read("build/scripts/vllm_gemma4_moe_gptq_patch.py")

    assert_patch_scripts_exist_and_parse()
    assert_qwen35_gptq_checkpoint_order_path(qwen35_patch_script)
    assert_legacy_gptq_uses_reference_after_qwen_shuffle_skip(gemma4_patch_script)
    assert_runtime_yaml_patch_refs(runtime_yaml)
    assert_dockerfile_patch_order(dockerfile)
    assert_runtime_entrypoint_contract(entrypoint, build_script)
    assert_optional_runtime_component_contract(dockerfile)
    assert_serving_dockerfile_contract(serving_dockerfile)
    assert_native_w4a16_dockerfile_contract(native_w4a16_dockerfile)
    assert_gfx906_vllm_diagnostics_contract(gfx906_vllm_dockerfile)
    assert_gfx906_vllm_compat_hooks_contract(gfx906_vllm_install_script)
    assert_sunshine_input_contract(sunshine_launcher, values_yaml)
    assert_validator_image_contract(values_yaml, qwen35_cache_yaml)
    assert_long_context_gauntlet_contract(
        long_context_cronjob_yaml, long_context_experiment_yaml
    )
    assert_qwen128_production_contract(qwen128_primary_yaml, qwen128_production_yaml)
    assert_wan_video_parked_contract(wan_video_yaml)
    assert_flash_loader_image_contract(
        flash_loader_source, model_deployment_source, chart_values_yaml
    )
    assert_ci_fast_check_wiring(ci_yaml)
    assert_build_script_defaults(build_script)
    waived = assert_publish_jobs_match_runtime_yaml(
        runtime_yaml,
        {".gitlab-ci.yml": root_ci_yaml, ".gitlab/ci/runtime-publish.yml": ci_yaml},
    )

    if run_tests:
        run_python_tests()

    for message in waived:
        print(f"WARNING: {message}")
    print("runtime patch contract checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

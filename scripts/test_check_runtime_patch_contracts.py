#!/usr/bin/env python3
"""Tests for the CI-vs-runtime.yaml contract in check-runtime-patch-contracts.py.

Stdlib only, like the script (its CI job has no pip install). The PyYAML
parity test runs only where PyYAML happens to be installed. Run with
``python3 scripts/test_check_runtime_patch_contracts.py``.
"""

from __future__ import annotations

import importlib.util
import pathlib
import textwrap
import unittest

HERE = pathlib.Path(__file__).resolve().parent
REPO_ROOT = HERE.parent
SCRIPT = HERE / "check-runtime-patch-contracts.py"
HAVE_YAML = importlib.util.find_spec("yaml") is not None

ROOT_CI = ".gitlab-ci.yml"
PUBLISH_CI = ".gitlab/ci/runtime-publish.yml"


def _load():
    spec = importlib.util.spec_from_file_location(
        "check_runtime_patch_contracts", SCRIPT
    )
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def _read(rel: str) -> str:
    return (REPO_ROOT / rel).read_text(encoding="utf-8")


class SimpleYAMLTests(unittest.TestCase):
    def setUp(self) -> None:
        self.m = _load()

    def parse(self, text: str):
        return self.m.parse_simple_yaml(textwrap.dedent(text))

    def test_nested_mappings_scalars_and_comments(self) -> None:
        doc = self.parse(
            """\
            # leading comment
            registry: registry.harbor.lan/flexinfer
            llamacpp_version: "b8173"   # trailing comment
            profiles:
              gfx906:
                tag: "runtime:rocm-gfx906"
                base_image: docker.io/mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3
                transformers_constraint: ">=5.0"
                quoted_hash: "a # not a comment"
                url_fragment: https://example.com/x#frag
                single: 'it''s'
                flag: true
                empty_string: ""
                nothing:
                tilde: ~
                backends:
                  vllm: false        # no gfx906 wheels
                env:
                  HSA_OVERRIDE_GFX_VERSION: "9.0.6"
                  PYTORCH_HIP_ALLOC_CONF: "garbage_collection_threshold:0.9,max_split_size_mb:512"
                promotion:
                  model_manifests: []
                  extra:
                    - deploy/models/a.yaml
                    - "deploy/models/b.yaml"
            """
        )
        self.assertEqual(doc["llamacpp_version"], "b8173")
        gfx906 = doc["profiles"]["gfx906"]
        self.assertEqual(gfx906["tag"], "runtime:rocm-gfx906")
        self.assertEqual(
            gfx906["base_image"], "docker.io/mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3"
        )
        self.assertEqual(gfx906["transformers_constraint"], ">=5.0")
        self.assertEqual(gfx906["quoted_hash"], "a # not a comment")
        self.assertEqual(gfx906["url_fragment"], "https://example.com/x#frag")
        self.assertEqual(gfx906["single"], "it's")
        self.assertEqual(
            gfx906["flag"], "true", "scalars stay strings, as yq -r prints them"
        )
        self.assertEqual(gfx906["empty_string"], "")
        self.assertIsNone(gfx906["nothing"])
        self.assertIsNone(gfx906["tilde"])
        self.assertEqual(gfx906["backends"], {"vllm": "false"})
        self.assertEqual(
            gfx906["env"],
            {
                "HSA_OVERRIDE_GFX_VERSION": "9.0.6",
                "PYTORCH_HIP_ALLOC_CONF": "garbage_collection_threshold:0.9,max_split_size_mb:512",
            },
        )
        self.assertEqual(gfx906["promotion"]["model_manifests"], [])
        self.assertEqual(
            gfx906["promotion"]["extra"],
            ["deploy/models/a.yaml", "deploy/models/b.yaml"],
        )

    def test_sequence_at_parent_indent(self) -> None:
        doc = self.parse(
            """\
            model_manifests:
            - a.yaml
            - b.yaml
            next: x
            """
        )
        self.assertEqual(doc, {"model_manifests": ["a.yaml", "b.yaml"], "next": "x"})

    def test_rejects_what_it_cannot_read(self) -> None:
        bad = {
            "tab": "a:\n\tb: c\n",
            "anchor": "a: &x 1\n",
            "alias": "a: *x\n",
            "block scalar": "a: |\n  text\n",
            "flow content": "a: [1, 2]\n",
            "duplicate": "a: 1\na: 2\n",
            "bad indent": "a:\n    b: 1\n  c: 2\n",
            "document marker": "---\na: 1\n",
            "mapping in sequence": "a:\n  - b: 1\n",
            "unterminated": 'a: "open\n',
            "not a mapping": "just text\n",
        }
        for name, text in bad.items():
            with self.subTest(name), self.assertRaises(self.m.SimpleYAMLError):
                self.m.parse_simple_yaml(text)

    @unittest.skipUnless(HAVE_YAML, "PyYAML not installed")
    def test_matches_pyyaml_on_runtime_yaml(self) -> None:
        import yaml

        def norm(value):
            if isinstance(value, bool):
                return "true" if value else "false"
            if isinstance(value, dict):
                return {k: norm(v) for k, v in value.items()}
            if isinstance(value, list):
                return [norm(v) for v in value]
            return None if value is None else str(value)

        text = _read("build/runtime.yaml")
        self.assertEqual(self.m.parse_simple_yaml(text), norm(yaml.safe_load(text)))


class ExpectedBuildArgsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.m = _load()
        self.cfg = self.m.parse_simple_yaml(
            textwrap.dedent(
                """\
                go_version: "1.26.0"
                llamacpp_version: "b8173"
                ollama_version: "v0.17.4"
                ollama_go_version: "1.25.7"
                profiles:
                  amd:
                    gpu_vendor: amd
                    gpu_arch: gfx906
                    rocm_dev_image: rocm/dev-ubuntu-22.04:6.2
                    cuda_dev_image: should-not-be-used
                    transformers_constraint: ""
                  pinned:
                    gpu_vendor: amd
                    gpu_arch: gfx906
                    llamacpp_version: "b11457"
                    amdgpu_targets: gfx906;gfx1100
                    transformers_install_mode: source
                    transformers_ref: abc123
                  nvidia:
                    gpu_vendor: nvidia
                    gpu_arch: sm_52
                    cuda_dev_image: nvidia/cuda:11.8.0-devel-ubuntu22.04
                    transformers_install_mode: none
                """
            )
        )

    def test_global_versions_apply_without_a_profile_key(self) -> None:
        args = self.m.expected_build_args(self.cfg, "amd")
        self.assertEqual(args["LLAMACPP_VERSION"], ("b8173", "llamacpp_version"))
        self.assertEqual(args["GO_VERSION"], ("1.26.0", "go_version"))

    def test_profile_version_overrides_global(self) -> None:
        args = self.m.expected_build_args(self.cfg, "pinned")
        self.assertEqual(
            args["LLAMACPP_VERSION"], ("b11457", "profiles.pinned.llamacpp_version")
        )
        self.assertEqual(args["OLLAMA_VERSION"], ("v0.17.4", "ollama_version"))

    def test_build_script_defaults_and_derivations(self) -> None:
        amd = self.m.expected_build_args(self.cfg, "amd")
        # build-runtime.sh: an empty profile value takes the default.
        self.assertEqual(amd["TRANSFORMERS_CONSTRAINT"][0], ">=5.0")
        self.assertEqual(amd["TRANSFORMERS_PACKAGE"][0], "transformers>=5.0")
        self.assertEqual(amd["TRANSFORMERS_RUNTIME_INSTALL"][0], "fallback")
        self.assertEqual(amd["AMDGPU_TARGETS"][0], "gfx906")
        self.assertEqual(amd["LLAMACPP_BUILD_IMAGE"][0], "rocm/dev-ubuntu-22.04:6.2")
        self.assertEqual(amd["INCLUDE_LLAMACPP"][0], "true")
        self.assertEqual(amd["VLLM_VERSION"][0], "0.17.0+rocm700")

        pinned = self.m.expected_build_args(self.cfg, "pinned")
        self.assertEqual(pinned["AMDGPU_TARGETS"][0], "gfx906;gfx1100")
        self.assertEqual(
            pinned["TRANSFORMERS_PACKAGE"][0],
            "git+https://github.com/huggingface/transformers.git@abc123",
        )

        nvidia = self.m.expected_build_args(self.cfg, "nvidia")
        self.assertEqual(
            nvidia["LLAMACPP_BUILD_IMAGE"][0], "nvidia/cuda:11.8.0-devel-ubuntu22.04"
        )
        self.assertEqual(nvidia["TRANSFORMERS_PACKAGE"][0], "")
        self.assertEqual(nvidia["TRANSFORMERS_RUNTIME_INSTALL"][0], "disabled")


class PublishJobContractTests(unittest.TestCase):
    """Run the contract against the repo's real files, mutated in memory."""

    def setUp(self) -> None:
        self.m = _load()
        self.runtime_yaml = _read("build/runtime.yaml")
        self.ci = {ROOT_CI: _read(ROOT_CI), PUBLISH_CI: _read(PUBLISH_CI)}

    def edit_job(self, ci_file: str, job: str, old: str, new: str) -> None:
        text = self.ci[ci_file]
        body = self.m.ci_job_body(text, job)
        self.assertIsNotNone(body, job)
        self.assertIn(old, body, f"{job} no longer contains {old!r}; update the test")
        self.ci[ci_file] = text.replace(body, body.replace(old, new, 1), 1)

    def edit_profile(self, profile: str, after: str, insert: str) -> None:
        marker = f"\n  {profile}:\n"
        start = self.runtime_yaml.index(marker)
        anchor = self.runtime_yaml.index(after, start)
        pos = anchor + len(after)
        self.runtime_yaml = self.runtime_yaml[:pos] + insert + self.runtime_yaml[pos:]

    def check(self) -> list[str]:
        return self.m.assert_publish_jobs_match_runtime_yaml(self.runtime_yaml, self.ci)

    def failure(self) -> str:
        with self.assertRaises(SystemExit) as cm:
            self.check()
        return str(cm.exception)

    def test_master_passes_with_only_the_known_divergences(self) -> None:
        waived = self.check()
        self.assertEqual(len(waived), len(self.m.KNOWN_PUBLISH_JOB_DIVERGENCES))
        self.assertTrue(all("[known divergence:" in w for w in waived))

    def test_llamacpp_version_changed_only_in_ci_fails(self) -> None:
        self.edit_job(
            ROOT_CI,
            "publish_runtime_rocm_gfx906",
            'LLAMACPP_VERSION="b8173"',
            'LLAMACPP_VERSION="b11457"',
        )
        msg = self.failure()
        self.assertIn(
            'publish_runtime_rocm_gfx906 (.gitlab-ci.yml) passes LLAMACPP_VERSION="b11457" but '
            'build/runtime.yaml resolves it to "b8173" (llamacpp_version)',
            msg,
        )
        self.assertNotIn("publish_unified_rocm_gfx1100", msg)

    def test_llamacpp_version_changed_only_in_runtime_yaml_fails_every_consumer(
        self,
    ) -> None:
        self.runtime_yaml = self.runtime_yaml.replace(
            'llamacpp_version: "b8173"', 'llamacpp_version: "b11457"', 1
        )
        msg = self.failure()
        for job, ci_file in (
            ("publish_runtime_rocm_gfx906", ROOT_CI),
            ("publish_unified_rocm_gfx1100", PUBLISH_CI),
        ):
            self.assertIn(
                f'{job} ({ci_file}) passes LLAMACPP_VERSION="b8173" but build/runtime.yaml '
                'resolves it to "b11457" (llamacpp_version)',
                msg,
            )

    def test_profile_level_llamacpp_version(self) -> None:
        self.edit_profile(
            "gfx906", "gpu_arch: gfx906\n", '    llamacpp_version: "b11457"\n'
        )
        msg = self.failure()
        self.assertIn(
            'publish_runtime_rocm_gfx906 (.gitlab-ci.yml) passes LLAMACPP_VERSION="b8173" but '
            'build/runtime.yaml resolves it to "b11457" (profiles.gfx906.llamacpp_version)',
            msg,
        )
        self.assertNotIn(
            "publish_unified_rocm_gfx1100",
            msg,
            "other profiles keep the global version",
        )

        self.edit_job(
            ROOT_CI,
            "publish_runtime_rocm_gfx906",
            'LLAMACPP_VERSION="b8173"',
            'LLAMACPP_VERSION="b11457"',
        )
        self.check()

    def test_runtime_env_value_change_fails(self) -> None:
        self.edit_job(
            PUBLISH_CI,
            "publish_serving_rocm_gfx1100",
            "MIOPEN_FIND_MODE=2",
            "MIOPEN_FIND_MODE=3",
        )
        self.assertIn(
            "publish_serving_rocm_gfx1100 (.gitlab/ci/runtime-publish.yml) runtime.env heredoc sets "
            'MIOPEN_FIND_MODE="3" but build/runtime.yaml profiles.gfx1100-serving.env.MIOPEN_FIND_MODE is "2"',
            self.failure(),
        )

    def test_runtime_env_extra_and_missing_keys_fail(self) -> None:
        self.edit_job(
            PUBLISH_CI,
            "publish_serving_rocm_gfx1100",
            "MIOPEN_FIND_MODE=2\n",
            "EXTRA_KEY=1\n",
        )
        msg = self.failure()
        self.assertIn(
            'runtime.env heredoc sets EXTRA_KEY="1" which build/runtime.yaml profiles.gfx1100-serving.env does not declare',
            msg,
        )
        self.assertIn(
            'runtime.env heredoc is missing MIOPEN_FIND_MODE="2" that build/runtime.yaml '
            "profiles.gfx1100-serving.env.MIOPEN_FIND_MODE sets",
            msg,
        )

    def test_gpu_identity_keys_are_required(self) -> None:
        self.edit_job(
            PUBLISH_CI, "publish_serving_rocm_gfx1100", "GPU_ARCH=gfx1100\n", ""
        )
        self.assertIn(
            'runtime.env heredoc is missing GPU_ARCH="gfx1100"', self.failure()
        )

    def test_derived_runtime_env_keys_are_checked_when_present(self) -> None:
        self.edit_job(
            ROOT_CI,
            "publish_runtime_rocm_gfx906",
            "FLEXINFER_TRANSFORMERS_PACKAGE=transformers>=5.0",
            "FLEXINFER_TRANSFORMERS_PACKAGE=transformers>=4.0",
        )
        self.assertIn(
            'runtime.env heredoc sets FLEXINFER_TRANSFORMERS_PACKAGE="transformers>=4.0" but build-runtime.sh '
            'derives "transformers>=5.0"',
            self.failure(),
        )

    def test_stale_waiver_fails(self) -> None:
        self.edit_job(
            ROOT_CI,
            "publish_runtime_rocm_gfx906",
            "GPU_ARCH=gfx906\n",
            "GPU_ARCH=gfx906\n      ROCM_PATH=/opt/rocm\n",
        )
        msg = self.failure()
        self.assertIn(
            "KNOWN_PUBLISH_JOB_DIVERGENCES entry (publish_runtime_rocm_gfx906, runtime.env, ROCM_PATH) "
            "no longer matches a divergence",
            msg,
        )
        self.assertNotIn("HIP_PATH) no longer", msg)

    def test_vllm_args_are_ignored_when_the_profile_has_no_vllm(self) -> None:
        # gfx906 passes VLLM_INSTALL_MODE="none" while build-runtime.sh would
        # pass "wheel"; Dockerfile.runtime-gfx906 never reads it.
        self.assertIn(
            'VLLM_INSTALL_MODE="none"',
            self.m.ci_job_body(self.ci[ROOT_CI], "publish_runtime_rocm_gfx906"),
        )
        self.check()
        self.edit_job(
            PUBLISH_CI,
            "publish_serving_rocm_gfx1100",
            'VLLM_VERSION="0.17.0+rocm700"',
            'VLLM_VERSION="0.19.1+rocm700"',
        )
        self.assertIn(
            'passes VLLM_VERSION="0.19.1+rocm700" but build/runtime.yaml resolves it to "0.17.0+rocm700"',
            self.failure(),
        )

    def test_image_tag_must_match_the_profile(self) -> None:
        self.edit_job(
            ROOT_CI,
            "publish_runtime_rocm_gfx906",
            "runtime:rocm-gfx906-${CI_COMMIT_SHORT_SHA}",
            "runtime:rocm-gfx906-humanizer-${CI_COMMIT_SHORT_SHA}",
        )
        self.assertIn(
            "publish_runtime_rocm_gfx906 (.gitlab-ci.yml) does not push ${REGISTRY}/runtime:rocm-gfx906-${CI_COMMIT_SHORT_SHA}",
            self.failure(),
        )

    def test_dockerfile_must_match_the_profile(self) -> None:
        self.edit_job(
            PUBLISH_CI,
            "publish_gaming_rocm_gfx1100",
            'filename="Dockerfile.runtime"',
            'filename="Dockerfile.runtime-serving"',
        )
        self.assertIn(
            "publish_gaming_rocm_gfx1100 (.gitlab/ci/runtime-publish.yml) builds build/Dockerfile.runtime-serving but "
            "build/runtime.yaml profiles.gfx1100-gaming.dockerfile is build/Dockerfile.runtime",
            self.failure(),
        )

    def test_unparseable_build_arg_fails_instead_of_passing_vacuously(self) -> None:
        self.edit_job(
            ROOT_CI,
            "publish_runtime_rocm_gfx906",
            '--opt build-arg:GO_VERSION="1.26.0"',
            "--opt build-arg:GO_VERSION",
        )
        self.assertIn("has a build-arg the contract check cannot parse", self.failure())

    def test_non_literal_build_arg_is_skipped(self) -> None:
        self.edit_job(
            ROOT_CI,
            "publish_runtime_rocm_gfx906",
            'LLAMACPP_VERSION="b8173"',
            'LLAMACPP_VERSION="${LLAMACPP_VERSION}"',
        )
        self.check()

    def test_missing_job_fails(self) -> None:
        self.ci[PUBLISH_CI] = self.ci[PUBLISH_CI].replace(
            "publish_gaming_rocm_gfx1100:", "publish_gaming_renamed:", 1
        )
        self.assertIn(
            ".gitlab/ci/runtime-publish.yml no longer defines publish_gaming_rocm_gfx1100",
            self.failure(),
        )

    def test_job_body_ends_at_the_next_top_level_line(self) -> None:
        body = self.m.ci_job_body(self.ci[ROOT_CI], "publish_runtime_rocm_gfx906")
        self.assertTrue(body.startswith("publish_runtime_rocm_gfx906:"))
        self.assertNotIn("publish_unified_rocm_gfx906:", body)
        self.assertNotIn("# Unified gfx906 runtime", body)
        self.assertIsNone(self.m.ci_job_body(self.ci[ROOT_CI], "no_such_job"))


class BuildScriptDefaultsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.m = _load()
        self.script = _read("build/build-runtime.sh")

    def test_real_build_script_matches(self) -> None:
        self.m.assert_build_script_defaults(self.script)

    def test_changed_default_fails(self) -> None:
        mutated = self.script.replace(
            '"vllm_version" "0.17.0+rocm700"', '"vllm_version" "0.19.1+rocm700"', 1
        )
        self.assertNotEqual(mutated, self.script)
        with self.assertRaises(SystemExit) as cm:
            self.m.assert_build_script_defaults(mutated)
        self.assertIn(
            "VLLM_VERSION: build-runtime.sh defaults vllm_version to '0.19.1+rocm700'",
            str(cm.exception),
        )

    def test_dynamic_profile_version_default_is_tolerated(self) -> None:
        # The shape a per-profile llamacpp_version override is likely to take.
        extra = '\n    llamacpp_version=$(pcfg "${profile}" "llamacpp_version" "$(cfg \'.llamacpp_version\')")\n'
        self.m.assert_build_script_defaults(self.script + extra)


class RealRepoTests(unittest.TestCase):
    def test_full_contract_check_passes(self) -> None:
        self.assertEqual(_load().main([]), 0)


if __name__ == "__main__":
    unittest.main()

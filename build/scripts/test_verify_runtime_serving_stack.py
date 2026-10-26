"""Tests for the stacked serving-runtime verifier."""

from __future__ import annotations

import importlib.util
import tempfile
import types
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).parent / "verify_runtime_serving_stack.py"
REPO_ROOT = Path(__file__).resolve().parents[2]


def _load_verifier():
    spec = importlib.util.spec_from_file_location(
        "verify_runtime_serving_stack", SCRIPT_PATH
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load verifier from {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _dist(name: str, version: str):
    return types.SimpleNamespace(metadata={"Name": name}, version=version)


class FrozenStackTests(unittest.TestCase):
    def test_snapshot_names_are_normalized(self) -> None:
        verifier = _load_verifier()
        snapshot = verifier.read_snapshot(
            "# pip list --format=freeze\nTorch==2.13.0+rocm7.2.3\nhuggingface_hub==1.2.0\n"
        )
        self.assertEqual(
            snapshot, {"torch": "2.13.0+rocm7.2.3", "huggingface-hub": "1.2.0"}
        )

    def test_snapshot_rejects_url_requirements(self) -> None:
        verifier = _load_verifier()
        with self.assertRaises(ValueError):
            verifier.read_snapshot("vllm @ file:///wheels/vllm.whl\n")

    def test_added_packages_pass(self) -> None:
        verifier = _load_verifier()
        installed = verifier.installed_packages(
            [_dist("torch", "2.13.0"), _dist("diffusers", "0.41.0")]
        )
        self.assertEqual(
            verifier.frozen_stack_errors({"torch": "2.13.0"}, installed), []
        )

    def test_shadowed_duplicate_does_not_count_as_a_change(self) -> None:
        verifier = _load_verifier()
        # pip's six in /usr/local first on sys.path, Ubuntu's behind it.
        installed = verifier.installed_packages(
            [_dist("six", "1.17.0"), _dist("six", "1.16.0")]
        )
        self.assertEqual(installed, {"six": "1.17.0"})
        self.assertEqual(verifier.frozen_stack_errors({"six": "1.17.0"}, installed), [])

    def test_changed_and_removed_packages_fail(self) -> None:
        verifier = _load_verifier()
        errors = verifier.frozen_stack_errors(
            {"numpy": "2.2.6", "transformers": "4.57.1"},
            {"numpy": "1.26.4"},
        )
        self.assertEqual(
            errors,
            [
                "numpy changed from 2.2.6 to 1.26.4 by the overlay",
                "transformers 4.57.1 was removed by the overlay",
            ],
        )


class PipCheckTests(unittest.TestCase):
    BASE = "pygobject 3.42.1 requires pycairo, which is not installed.\n"

    def test_inherited_problems_pass(self) -> None:
        verifier = _load_verifier()
        self.assertEqual(verifier.new_pip_check_problems(self.BASE, self.BASE), [])

    def test_clean_base_and_clean_result_pass(self) -> None:
        verifier = _load_verifier()
        clean = "No broken requirements found.\n"
        self.assertEqual(verifier.new_pip_check_problems(clean, clean), [])

    def test_new_problem_fails(self) -> None:
        verifier = _load_verifier()
        current = (
            self.BASE
            + "vllm 0.32.0 has requirement huggingface-hub>=2.0, but you have huggingface-hub 1.40.0.\n"
        )
        self.assertEqual(
            verifier.new_pip_check_problems(self.BASE, current),
            [
                "vllm 0.32.0 has requirement huggingface-hub>=2.0, but you have huggingface-hub 1.40.0."
            ],
        )


class SpawnContractTests(unittest.TestCase):
    def test_python_must_exist(self) -> None:
        verifier = _load_verifier()
        error = verifier.interpreter_error(lambda name: None, "/usr/bin/python3")
        self.assertIn("not on PATH", error)

    def test_python_must_be_the_vllm_interpreter(self) -> None:
        verifier = _load_verifier()
        error = verifier.interpreter_error(
            lambda name: "/usr/local/bin/python",
            "/usr/bin/python3",
            realpath=lambda path: {
                "/usr/local/bin/python": "/opt/conda/bin/python3.12"
            }.get(path, path),
        )
        self.assertIn("/opt/conda/bin/python3.12", error)

    def test_python_symlink_to_the_interpreter_passes(self) -> None:
        verifier = _load_verifier()
        error = verifier.interpreter_error(
            lambda name: "/usr/local/bin/python",
            "/usr/bin/python3",
            realpath=lambda path: "/usr/bin/python3.12",
        )
        self.assertIsNone(error)

    def test_server_module_needs_a_main_guard(self) -> None:
        verifier = _load_verifier()
        with tempfile.TemporaryDirectory() as tmp:
            good = Path(tmp) / "good.py"
            good.write_text(
                "def run():\n    pass\n\nif __name__ == '__main__':\n    run()\n"
            )
            bad = Path(tmp) / "bad.py"
            bad.write_text("def run():\n    pass\n")
            specs = {
                "good": types.SimpleNamespace(origin=str(good)),
                "bad": types.SimpleNamespace(origin=str(bad)),
            }
            self.assertIsNone(verifier.module_main_error("good", specs.get))
            self.assertIn(
                "no __main__ guard", verifier.module_main_error("bad", specs.get)
            )
            self.assertIn(
                "not importable", verifier.module_main_error("gone", specs.get)
            )

    def test_executables(self) -> None:
        verifier = _load_verifier()
        allowed = {"/usr/bin/tini"}
        self.assertEqual(
            verifier.non_executable(
                ["/usr/bin/tini", "/usr/local/bin/flexinfer-runtime"],
                access=lambda path, mode: path in allowed,
            ),
            ["/usr/local/bin/flexinfer-runtime"],
        )


class ServerImportTests(unittest.TestCase):
    SOURCE = """
import os
import torch
from fastapi import FastAPI
from diffusers import (
    FluxPipeline,
    WanPipeline,
)
from . import sibling

try:
    from accelerate.utils import compile_regions
except ImportError:
    compile_regions = None


def load():
    from diffusers import StableAudioPipeline
    from diffusers.utils import export_to_video
    import uvicorn
    try:
        from diffusers import BitsAndBytesConfig
    except ImportError:
        BitsAndBytesConfig = None
"""

    def test_required_imports_skip_stdlib_relative_and_try_blocks(self) -> None:
        verifier = _load_verifier()
        modules, names = verifier.server_imports(self.SOURCE)
        self.assertEqual(modules, {"torch", "fastapi", "diffusers", "uvicorn"})
        self.assertEqual(
            names,
            {
                "diffusers": {"FluxPipeline", "WanPipeline", "StableAudioPipeline"},
                "diffusers.utils": {"export_to_video"},
            },
        )

    def test_missing_modules(self) -> None:
        verifier = _load_verifier()
        present = {"torch", "fastapi"}

        def find_spec(name):
            if name == "broken":
                raise ValueError("broken.__spec__ is None")
            return object() if name in present else None

        self.assertEqual(
            verifier.missing_modules(
                ["torch", "uvicorn", "fastapi", "broken"], find_spec
            ),
            ["broken", "uvicorn"],
        )

    def test_missing_names_reports_absent_and_unimportable(self) -> None:
        verifier = _load_verifier()

        class LazyModule:
            FluxPipeline = object()

            def __getattr__(self, name):
                raise AttributeError(name)

        def import_module(name):
            if name == "diffusers":
                return LazyModule()
            raise ImportError(f"No module named {name!r}")

        self.assertEqual(
            verifier.missing_names(
                {
                    "diffusers": {"FluxPipeline", "QwenImage21Pipeline"},
                    "diffusers.gone": {"x"},
                },
                import_module,
            ),
            [
                "diffusers.QwenImage21Pipeline",
                "diffusers.gone (ImportError: No module named 'diffusers.gone')",
            ],
        )

    def test_repo_servers_parse(self) -> None:
        verifier = _load_verifier()
        diffusers_modules, diffusers_names = verifier.server_imports(
            (REPO_ROOT / "build/server-diffusers.py").read_text()
        )
        self.assertIn("diffusers", diffusers_modules)
        self.assertIn("WanPipeline", diffusers_names["diffusers"])
        snac_modules, _ = verifier.server_imports(
            (REPO_ROOT / "build/server-snac.py").read_text()
        )
        self.assertIn("snac", snac_modules)


if __name__ == "__main__":
    unittest.main()

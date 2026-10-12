#!/usr/bin/env python3
"""Tests for scripts/check-runtime-pin-drift.py.

No registry: a fake Harbor client serves canned artifacts so the tests can
prove how each consumer is classified. Run with
``python3 scripts/test_check_runtime_pin_drift.py`` (needs PyYAML, like the
script itself).
"""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import pathlib
import sys
import tempfile
import textwrap
import unittest
from unittest import mock

HERE = pathlib.Path(__file__).resolve().parent
SCRIPT = HERE / "check-runtime-pin-drift.py"
HAVE_YAML = importlib.util.find_spec("yaml") is not None

OVERLAY = "sha256:" + "8d" * 32
GFX906_OLD = "sha256:" + "17" * 32
GFX906_NEW = "sha256:" + "3d" * 32
SERVING_OLD = "sha256:" + "f7" * 32
SERVING_NEW = "sha256:" + "da" * 32
UNIFIED_NEW = "sha256:" + "e1" * 32
REF = "registry.harbor.lan/flexinfer/runtime@"


def _load():
    spec = importlib.util.spec_from_file_location("check_runtime_pin_drift", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def _artifact(digest: str, tags: list[str], pushed: str) -> dict:
    return {"digest": digest, "tags": [{"name": t} for t in tags], "push_time": pushed}


ARTIFACTS = {
    "rocm-gfx906": _artifact(
        GFX906_NEW, ["rocm-gfx906", "rocm-gfx906-b26dcef5"], "2026-10-12T16:55:26Z"
    ),
    GFX906_NEW: _artifact(
        GFX906_NEW, ["rocm-gfx906", "rocm-gfx906-b26dcef5"], "2026-10-12T16:55:26Z"
    ),
    GFX906_OLD: _artifact(GFX906_OLD, ["rocm-gfx906-882d81ba"], "2026-09-13T20:59:16Z"),
    OVERLAY: _artifact(
        OVERLAY,
        ["rocm-gfx906-humanizer-6c3db0947", "rocm-gfx906-humanizer-b11335"],
        "2026-10-12T12:00:00Z",
    ),
    "rocm-gfx1100-serving": _artifact(
        SERVING_NEW,
        ["rocm-gfx1100-serving", "rocm-gfx1100-serving-b26dcef5"],
        "2026-10-12T18:00:00Z",
    ),
    SERVING_OLD: _artifact(
        SERVING_OLD, ["rocm-gfx1100-serving-e4ad4d10"], "2026-09-14T00:00:00Z"
    ),
    "rocm-gfx1100": _artifact(
        UNIFIED_NEW, ["rocm-gfx1100", "rocm-gfx1100-b26dcef5"], "2026-10-12T19:00:00Z"
    ),
}


class FakeHarbor:
    def __init__(self, api, ca_bundle, allow_insecure):
        self.insecure_used = False

    def artifact(self, project, repo, reference):
        return ARTIFACTS.get(reference)


class _Repo:
    """A throwaway repo root shaped like flexinfer's runtime pin surface."""

    def __init__(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.tmp.name)
        self.write(
            "build/runtime.yaml",
            """\
            registry: registry.harbor.lan/flexinfer
            profiles:
              gfx1100:
                tag: "runtime:rocm-gfx1100"
                promotion:
                  values_runtime_profiles: false
                gpu_arch: gfx1100
              gfx1100-serving:
                tag: "runtime:rocm-gfx1100-serving"
                promotion:
                  gpuprofile: false
                  values_runtime_profiles: true
                gpu_arch: gfx1100
              gfx906:
                tag: "runtime:rocm-gfx906"
                gpu_arch: gfx906
            """,
        )
        self.write(
            "deploy/system/values-k3s.yaml",
            f"""\
            runtime:
              profiles:
                - name: gfx1100
                  gpuArch: gfx1100
                  image: "{REF}{SERVING_OLD}"
                - name: gfx906
                  gpuArch: gfx906
                  image: "{REF}{OVERLAY}"
            """,
        )
        self.write(
            "deploy/gpuprofiles/kustomization.yaml",
            """\
            resources:
              - gfx1100.yaml
              - gfx906.yaml
            """,
        )
        # The gfx1100 GPUProfile pins the *serving* persona: a sibling
        # profile's CI build, which is a persona mismatch for gfx1100.
        self.write(
            "deploy/gpuprofiles/gfx1100.yaml",
            f"""\
            metadata:
              name: gfx1100
            spec:
              runtime:
                image: "{REF}{SERVING_OLD}"
            """,
        )
        self.write(
            "deploy/gpuprofiles/gfx906.yaml",
            f"""\
            metadata:
              name: gfx906
            spec:
              runtime:
                image: "{REF}{GFX906_OLD}"
            """,
        )

    def write(self, rel: str, text: str) -> None:
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(text), encoding="utf-8")

    def cleanup(self) -> None:
        self.tmp.cleanup()

    def run(self, module, *extra: str) -> tuple[int, str, dict]:
        md = self.root / "drift.md"
        js = self.root / "drift.json"
        argv = [
            "check-runtime-pin-drift.py",
            "--repo-root",
            str(self.root),
            "--markdown",
            str(md),
            "--json",
            str(js),
            *extra,
        ]
        out = io.StringIO()
        with mock.patch.object(sys, "argv", argv), mock.patch.object(
            module, "Harbor", FakeHarbor
        ), contextlib.redirect_stdout(out):
            rc = module.main()
        return (
            rc,
            md.read_text(encoding="utf-8"),
            json.loads(js.read_text(encoding="utf-8")),
        )


@unittest.skipUnless(HAVE_YAML, "check-runtime-pin-drift.py needs PyYAML")
class ClassifyPersonaTests(unittest.TestCase):
    def setUp(self) -> None:
        self.m = _load()
        self.all = {"rocm-gfx906", "rocm-gfx1100", "rocm-gfx1100-serving"}

    def classify(self, tags, floating):
        return self.m.classify_persona(tags, floating, self.all)

    def test_ci_commit_tag_is_a_profile_build(self) -> None:
        self.assertEqual(
            self.classify(["rocm-gfx906-882d81ba"], "rocm-gfx906"), self.m.PROFILE_BUILD
        )
        self.assertEqual(
            self.classify(["rocm-gfx906-b26dcef5ab12"], "rocm-gfx906"),
            self.m.PROFILE_BUILD,
        )

    def test_floating_tag_alone_is_a_profile_build(self) -> None:
        self.assertEqual(
            self.classify(["rocm-gfx906"], "rocm-gfx906"), self.m.PROFILE_BUILD
        )

    def test_overlay_tags_are_an_off_profile_build(self) -> None:
        tags = ["rocm-gfx906-humanizer-6c3db0947", "rocm-gfx906-humanizer-b11335"]
        self.assertEqual(self.classify(tags, "rocm-gfx906"), self.m.OFF_PROFILE_BUILD)

    def test_word_suffix_is_off_profile_even_when_short_hex(self) -> None:
        # b11335 is hex but only six characters: not a CI commit tag.
        self.assertEqual(
            self.classify(["rocm-gfx906-b11335"], "rocm-gfx906"),
            self.m.OFF_PROFILE_BUILD,
        )

    def test_any_ci_tag_wins_over_overlay_tags(self) -> None:
        tags = ["rocm-gfx906-humanizer-b11335", "rocm-gfx906-882d81ba"]
        self.assertEqual(self.classify(tags, "rocm-gfx906"), self.m.PROFILE_BUILD)

    def test_sibling_persona_build_is_a_persona_mismatch(self) -> None:
        # Starts with "rocm-gfx1100-" but belongs to rocm-gfx1100-serving.
        self.assertEqual(
            self.classify(["rocm-gfx1100-serving-e4ad4d10"], "rocm-gfx1100"),
            self.m.PERSONA_MISMATCH,
        )
        self.assertEqual(
            self.classify(["rocm-gfx1100-serving-humanizer-1"], "rocm-gfx1100"),
            self.m.PERSONA_MISMATCH,
        )

    def test_unrelated_tags_are_a_persona_mismatch(self) -> None:
        self.assertEqual(
            self.classify(["unified-gfx906-v4"], "rocm-gfx906"), self.m.PERSONA_MISMATCH
        )
        self.assertEqual(self.classify([], "rocm-gfx906"), self.m.PERSONA_MISMATCH)


@unittest.skipUnless(HAVE_YAML, "check-runtime-pin-drift.py needs PyYAML")
class ReportTests(unittest.TestCase):
    def setUp(self) -> None:
        self.m = _load()
        self.repo = _Repo()
        self.addCleanup(self.repo.cleanup)

    def rows(self, report: dict) -> dict:
        return {(r["profile"], r["kind"], r["consumer"]): r for r in report["profiles"]}

    def test_overlay_consumer_is_reported_as_off_profile_build(self) -> None:
        rc, md, report = self.repo.run(self.m)
        self.assertEqual(rc, 0, "off-profile builds are advisory without --strict")
        row = self.rows(report)[("gfx906", "runtime DaemonSet", "gfx906")]
        self.assertEqual(row["status"], "off-profile build")
        self.assertEqual(
            report["off_profile_builds"],
            [
                {
                    "profile": "gfx906",
                    "tag": "runtime:rocm-gfx906",
                    "consumer": "gfx906",
                    "kind": "runtime DaemonSet",
                    "file": "deploy/system/values-k3s.yaml",
                    "pinned": OVERLAY,
                    "pinned_tags": [
                        "rocm-gfx906-humanizer-6c3db0947",
                        "rocm-gfx906-humanizer-b11335",
                    ],
                    "current": GFX906_NEW,
                }
            ],
        )
        finding = next(f for f in report["findings"] if "off-profile build" in f)
        self.assertIn(
            "rocm-gfx906-humanizer-6c3db0947, rocm-gfx906-humanizer-b11335", finding
        )
        self.assertIn("not produced by the gfx906 profile's CI job", finding)
        self.assertIn("| runtime DaemonSet `gfx906` |", md)
        self.assertIn("| off-profile build |", md)

    def test_strict_fails_on_off_profile_build(self) -> None:
        rc, _, report = self.repo.run(self.m, "--strict")
        self.assertEqual(rc, 1)
        self.assertEqual(report["hard"], [])

    def test_profile_builds_and_sibling_personas_keep_their_statuses(self) -> None:
        _, _, report = self.repo.run(self.m)
        rows = self.rows(report)
        # The day count depends on when the test runs; the status kind does not.
        self.assertRegex(
            rows[("gfx906", "GPUProfile runtime.image", "gfx906")]["status"],
            r"^behind by \d+d$",
        )
        self.assertRegex(
            rows[("gfx1100-serving", "runtime DaemonSet", "gfx1100")]["status"],
            r"^behind by \d+d$",
        )
        self.assertEqual(
            rows[("gfx1100", "GPUProfile runtime.image", "gfx1100")]["status"],
            "persona mismatch (expects rocm-gfx1100)",
        )
        self.assertEqual(len(report["off_profile_builds"]), 1)

    def test_split_pin_is_reported_but_never_fails(self) -> None:
        rc, md, report = self.repo.run(self.m, "--offline", "--strict")
        self.assertEqual(
            report["split_pins"],
            [
                {
                    "gpu_arch": "gfx906",
                    "gpuprofile": "gfx906",
                    "gpuprofile_file": "deploy/gpuprofiles/gfx906.yaml",
                    "gpuprofile_image": GFX906_OLD,
                    "daemonset_profile": "gfx906",
                    "daemonset_image": OVERLAY,
                }
            ],
            "gfx1100 GPUProfile and DaemonSet pin the same digest: no split",
        )
        self.assertIn(
            f"- WARNING: split pin: GPUProfile gfx906 runtime.image {GFX906_OLD[:19]} ≠ DaemonSet profile gfx906 {OVERLAY[:19]}",
            md,
        )
        self.assertFalse(any("split pin" in f for f in report["findings"]))
        self.assertEqual(
            rc, 0, "--offline --strict has no findings besides the split pin"
        )

    def test_report_header_says_gpuprofile_image_is_documentation(self) -> None:
        _, md, _ = self.repo.run(self.m, "--offline")
        self.assertTrue(md.startswith("# Runtime pin drift report\n"))
        self.assertIn("`spec.runtime.image` is not read by any Go code today", md)
        self.assertIn("RuntimeProfileBundlesBackend", md)


if __name__ == "__main__":
    unittest.main()

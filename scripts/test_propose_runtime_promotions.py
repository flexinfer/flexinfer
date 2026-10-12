#!/usr/bin/env python3
"""Tests for scripts/propose-runtime-promotions.py.

No registry and no real promote script: a stub `promote-runtime-digest.sh`
records its arguments so the tests can prove what the proposal driver would
run. Run with ``python3 scripts/test_propose_runtime_promotions.py``.
"""

from __future__ import annotations

import datetime as dt
import importlib.util
import json
import os
import pathlib
import stat
import tempfile
import textwrap
import unittest

HERE = pathlib.Path(__file__).resolve().parent
SCRIPT = HERE / "propose-runtime-promotions.py"

NEW_906 = "sha256:" + "11" * 32
OLD_906 = "sha256:" + "22" * 32
NEW_1100 = "sha256:" + "33" * 32
OLD_1100 = "sha256:" + "44" * 32
CURRENT_NATIVE = "sha256:" + "55" * 32
OVERLAY_906 = "sha256:" + "66" * 32


def _load():
    spec = importlib.util.spec_from_file_location("propose_runtime_promotions", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def _report() -> dict:
    return {
        "generated": "2026-09-14T00:00:00Z",
        "offline": False,
        "profiles": [
            {
                "profile": "gfx906",
                "tag": "runtime:rocm-gfx906",
                "current": NEW_906,
                "current_push_time": "2026-09-13T20:59:16.000Z",
                "consumer": "gfx906",
                "kind": "runtime DaemonSet",
                "file": "deploy/system/values-k3s.yaml",
                "pinned": OLD_906,
                "pinned_tags": ["rocm-gfx906-a01effd0"],
                "pinned_push_time": "2026-09-12T10:00:00.000Z",
                "status": "behind by 1d",
            },
            {
                "profile": "gfx906",
                "tag": "runtime:rocm-gfx906",
                "current": NEW_906,
                "current_push_time": "2026-09-13T20:59:16.000Z",
                "consumer": "gfx906",
                "kind": "GPUProfile runtime.image",
                "file": "deploy/gpuprofiles/gfx906.yaml",
                "pinned": OLD_906,
                "pinned_tags": "rocm-gfx906-a01effd0",
                "pinned_push_time": "2026-09-12T10:00:00.000Z",
                "status": "behind by 1d",
            },
            {
                # Already on the newest build: never a candidate.
                "profile": "gfx1100",
                "tag": "runtime:rocm-gfx1100",
                "current": NEW_1100,
                "consumer": "gfx1100",
                "kind": "GPUProfile runtime.image",
                "file": "deploy/gpuprofiles/gfx1100.yaml",
                "pinned": NEW_1100,
                "status": "current",
            },
            {
                # Persona mismatch on a prewarm-gated profile: candidate, proposal only.
                "profile": "gfx1100-native-w4a16",
                "tag": "runtime:rocm-gfx1100-v026-native-w4a16",
                "current": CURRENT_NATIVE,
                "consumer": "deploy/models/qwen38-27b-autoround-workhorse.yaml",
                "kind": "Model spec.image",
                "file": "deploy/models/qwen38-27b-autoround-workhorse.yaml",
                "pinned": OLD_1100,
                "status": "persona mismatch (expects rocm-gfx1100-v026-native-w4a16)",
            },
            {
                # Nothing built yet: never a candidate.
                "profile": "gfx906-vllm",
                "tag": "runtime:rocm-gfx906-vllm",
                "current": None,
                "consumer": "gfx906-vllm",
                "kind": "GPUProfile backends.vllm.image",
                "file": "deploy/gpuprofiles/gfx906.yaml",
                "pinned": OLD_906,
                "status": "MISSING",
            },
        ],
        "findings": [],
    }


class _Repo:
    """A throwaway repo root with a stub promote script and a runtime.yaml."""

    def __init__(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.tmp.name)
        (self.root / "scripts").mkdir()
        (self.root / "build").mkdir()
        self.calls = self.root / "promote-calls.log"
        stub = self.root / "scripts" / "promote-runtime-digest.sh"
        stub.write_text(
            textwrap.dedent(
                f"""\
                #!/usr/bin/env bash
                printf '%s\\n' "$*" >> "{self.calls}"
                echo "Runtime digest promotion"
                echo "  profile: $1"
                echo "  mode:    dry-run"
                echo "Dry-run diff:"
                echo "-  image: old"
                echo "+  image: new"
                """
            ),
            encoding="utf-8",
        )
        stub.chmod(stub.stat().st_mode | stat.S_IEXEC)
        (self.root / "build" / "runtime.yaml").write_text(
            textwrap.dedent(
                """\
                registry: registry.harbor.lan/flexinfer
                profiles:
                  gfx906:
                    tag: runtime:rocm-gfx906
                    promotion:
                      gpuprofile: true
                      values_runtime_profiles: true
                  gfx1100-native-w4a16:
                    tag: runtime:rocm-gfx1100-v026-native-w4a16
                    promotion:
                      gpuprofile: false
                      values_runtime_profiles: false
                      require_prewarm: true
                      model_manifests:
                        - deploy/models/qwen38-27b-autoround-workhorse.yaml
                """
            ),
            encoding="utf-8",
        )
        self.report = self.root / "runtime-pin-drift.json"
        self.report.write_text(json.dumps(_report()), encoding="utf-8")

    def cleanup(self) -> None:
        self.tmp.cleanup()

    def run(self, module, *extra: str) -> int:
        return module.main(
            [
                "--report",
                str(self.report),
                "--repo-root",
                str(self.root),
                "--out-dir",
                "promotion-proposals",
                *extra,
            ]
        )

    def proposal(self, name: str) -> str:
        return (self.root / "promotion-proposals" / name).read_text(encoding="utf-8")


class SelectCandidatesTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = _load()

    def test_groups_consumers_and_skips_current_and_missing(self) -> None:
        candidates = self.module.select_candidates(_report())
        self.assertEqual(
            [c["profile"] for c in candidates], ["gfx1100-native-w4a16", "gfx906"]
        )
        gfx906 = candidates[1]
        self.assertEqual(gfx906["current"], NEW_906)
        self.assertEqual(gfx906["rollback"], OLD_906)
        self.assertEqual(len(gfx906["consumers"]), 2)
        self.assertEqual(
            {c["kind"] for c in gfx906["consumers"]},
            {"runtime DaemonSet", "GPUProfile runtime.image"},
        )

    def test_profile_filter(self) -> None:
        candidates = self.module.select_candidates(_report(), {"gfx906"})
        self.assertEqual([c["profile"] for c in candidates], ["gfx906"])

    def test_consumer_already_on_current_is_not_stale(self) -> None:
        report = _report()
        for row in report["profiles"]:
            if row["profile"] == "gfx906":
                row["pinned"] = NEW_906
                row["status"] = "current"
        candidates = self.module.select_candidates(report)
        self.assertEqual([c["profile"] for c in candidates], ["gfx1100-native-w4a16"])


class MainTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = _load()
        self.repo = _Repo()
        self.addCleanup(self.repo.cleanup)

    def test_writes_one_proposal_per_candidate_plus_index(self) -> None:
        rc = self.repo.run(self.module)
        self.assertEqual(rc, 0)
        out = self.repo.root / "promotion-proposals"
        self.assertEqual(
            sorted(p.name for p in out.iterdir()),
            ["README.md", "gfx1100-native-w4a16.md", "gfx906.md"],
        )
        calls = self.repo.calls.read_text(encoding="utf-8").splitlines()
        self.assertEqual(
            calls,
            [
                f"gfx1100-native-w4a16 --digest {CURRENT_NATIVE}",
                f"gfx906 --digest {NEW_906}",
            ],
            "dry-run only: no --apply may reach the promote script",
        )

    def test_gfx906_proposal_carries_apply_command_and_dry_run(self) -> None:
        self.repo.run(self.module)
        md = self.repo.proposal("gfx906.md")
        row = f"gfx906-{dt.date.today().isoformat()}"
        self.assertIn(f"# Promotion proposal: `gfx906` → `{NEW_906[7:19]}`", md)
        self.assertIn(f"--digest {NEW_906}", md)
        self.assertIn(f"--rollback-digest {OLD_906}", md)
        self.assertIn(f"--validation-row {row}", md)
        self.assertIn("--apply", md)
        self.assertIn("+  image: new", md)
        self.assertIn("exit code: 0", md)
        self.assertIn("| runtime DaemonSet | `gfx906` | `deploy/system/values-k3s.yaml` |", md)
        self.assertIn("| GPUProfile runtime.image | `gfx906` | `deploy/gpuprofiles/gfx906.yaml` |", md)
        self.assertNotIn("--prewarm-profile", md)

    def test_prewarm_gated_profile_is_proposal_only(self) -> None:
        if self.module.yaml is None:
            self.skipTest("PyYAML not installed; promotion contracts are read from build/runtime.yaml")
        self.repo.run(self.module)
        md = self.repo.proposal("gfx1100-native-w4a16.md")
        self.assertIn("--prewarm-profile", md)
        self.assertIn("require_prewarm", md)
        self.assertIn("deploy/models/qwen38-27b-autoround-workhorse.yaml", md)
        self.assertIn("persona mismatch", md)

    def test_index_lists_every_candidate(self) -> None:
        self.repo.run(self.module)
        index = self.repo.proposal("README.md")
        self.assertIn("[`gfx906`](gfx906.md)", index)
        self.assertIn("[`gfx1100-native-w4a16`](gfx1100-native-w4a16.md)", index)
        self.assertIn(f"`{OLD_906[7:19]}` → `{NEW_906[7:19]}`", index)
        self.assertIn("proposal only", index)

    def test_no_candidates_writes_index_only(self) -> None:
        report = _report()
        report["profiles"] = [r for r in report["profiles"] if r["status"] in ("current", "MISSING")]
        self.repo.report.write_text(json.dumps(report), encoding="utf-8")
        rc = self.repo.run(self.module, "--fail-on-candidates")
        self.assertEqual(rc, 0)
        out = self.repo.root / "promotion-proposals"
        self.assertEqual([p.name for p in out.iterdir()], ["README.md"])
        self.assertIn("Nothing to promote", self.repo.proposal("README.md"))
        self.assertFalse(self.repo.calls.exists())

    def test_fail_on_candidates_exits_2(self) -> None:
        self.assertEqual(self.repo.run(self.module, "--fail-on-candidates"), 2)

    def test_profile_flag_limits_proposals(self) -> None:
        self.repo.run(self.module, "--profile", "gfx906")
        out = self.repo.root / "promotion-proposals"
        self.assertEqual(sorted(p.name for p in out.iterdir()), ["README.md", "gfx906.md"])

    def test_create_mrs_without_token_is_a_noop(self) -> None:
        env_backup = {
            k: os.environ.pop(k)
            for k in list(os.environ)
            if k.startswith(("FLEXINFER_PROMOTE_MR_TOKEN", "CI_"))
        }
        self.addCleanup(os.environ.update, env_backup)
        rc = self.repo.run(self.module, "--create-mrs")
        self.assertEqual(rc, 0)
        index = self.repo.proposal("README.md")
        self.assertIn("skipped: FLEXINFER_PROMOTE_MR_TOKEN", index)
        calls = self.repo.calls.read_text(encoding="utf-8")
        self.assertNotIn("--apply", calls, "no token → no branch, no --apply")


def _off_profile_report() -> dict:
    """The gfx906 DaemonSet runs a hand-pushed overlay, not a CI build."""
    report = _report()
    for row in report["profiles"]:
        if row["profile"] == "gfx906" and row["kind"] == "runtime DaemonSet":
            row["pinned"] = OVERLAY_906
            row["pinned_tags"] = ["rocm-gfx906-humanizer-6c3db0947", "rocm-gfx906-humanizer-b11335"]
            row["status"] = "off-profile build"
    return report


class OffProfileBuildTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = _load()
        self.repo = _Repo()
        self.addCleanup(self.repo.cleanup)
        self.repo.report.write_text(json.dumps(_off_profile_report()), encoding="utf-8")

    def test_candidate_carries_the_off_profile_consumer(self) -> None:
        gfx906 = next(c for c in self.module.select_candidates(_off_profile_report()) if c["profile"] == "gfx906")
        self.assertEqual([r["kind"] for r in gfx906["off_profile"]], ["runtime DaemonSet"])
        self.assertEqual(gfx906["rollback"], OVERLAY_906)
        self.assertEqual(self.module.select_candidates(_report())[1]["off_profile"], [])

    def test_proposal_leads_with_a_warning_block(self) -> None:
        self.repo.run(self.module)
        md = self.repo.proposal("gfx906.md")
        warning = md.index("> **WARNING: off-profile build in service.**")
        self.assertLess(warning, md.index("- Profile tag:"), "the warning must come before the details")
        self.assertIn(
            "runtime DaemonSet `gfx906` (`deploy/system/values-k3s.yaml`) currently runs an off-profile build "
            f"`{OVERLAY_906[7:19]}` (`rocm-gfx906-humanizer-6c3db0947`, `rocm-gfx906-humanizer-b11335`); "
            f"promoting `{NEW_906[7:19]}` replaces it",
            md,
        )
        self.assertIn("confirm the profile build carries the same components, e.g. llama.cpp version", md)
        self.assertNotIn("WARNING", self.repo.proposal("gfx1100-native-w4a16.md"))

    def test_promote_command_is_unchanged(self) -> None:
        self.repo.run(self.module)
        md = self.repo.proposal("gfx906.md")
        self.assertIn(f"--digest {NEW_906}", md)
        self.assertIn(f"--rollback-digest {OVERLAY_906}", md)
        self.assertIn("--apply", md)
        calls = self.repo.calls.read_text(encoding="utf-8").splitlines()
        self.assertIn(f"gfx906 --digest {NEW_906}", calls)

    def test_index_marks_the_profile(self) -> None:
        self.repo.run(self.module)
        index = self.repo.proposal("README.md")
        row = next(line for line in index.splitlines() if line.startswith("| [`gfx906`]"))
        self.assertIn(f"**off-profile build in service**: `gfx906` on `{OVERLAY_906[7:19]}`", row)
        native = next(line for line in index.splitlines() if line.startswith("| [`gfx1100-native-w4a16`]"))
        self.assertNotIn("off-profile", native)
        self.assertIn("read the warning in its proposal first", index)

    def test_index_without_off_profile_builds_has_no_note(self) -> None:
        self.repo.report.write_text(json.dumps(_report()), encoding="utf-8")
        self.repo.run(self.module)
        self.assertNotIn("off-profile", self.repo.proposal("README.md"))


if __name__ == "__main__":
    unittest.main()

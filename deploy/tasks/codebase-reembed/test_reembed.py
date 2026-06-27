#!/usr/bin/env python3
"""Unit tests for the nightly re-embed script's multi-repo logic (F3 Slice 4).

The canonical script lives inline in ``configmap.yaml`` (the operator's
"standalone ConfigMap script" choice), so this test extracts that literal block,
sets placeholder env, imports it as a module, and exercises the *pure* functions
(``parse_repos`` / ``default_collection`` / ``point_id`` / ``chunk_file``) with no
network. The HTTP/qdrant paths are intentionally not exercised here -- they need
the in-cluster proxy + qdrant.

Run standalone like the other repo unittest scripts (no pip deps)::

    python3 deploy/tasks/codebase-reembed/test_reembed.py
"""
from __future__ import annotations

import importlib.util
import os
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
CONFIGMAP = os.path.join(HERE, "configmap.yaml")


def _load_script():
    """Extract ``reembed.py`` from the ConfigMap literal block and import it.

    Keeps the ConfigMap as the single source of truth (no separate copy to drift)
    while still giving the pure logic offline test coverage + a CI gate.
    """
    with open(CONFIGMAP, "r", encoding="utf-8") as fh:
        lines = fh.readlines()
    start = None
    for i, ln in enumerate(lines):
        if ln.rstrip().endswith("reembed.py: |"):
            start = i + 1
            break
    if start is None:
        raise AssertionError("reembed.py block not found in configmap.yaml")
    block = []
    for ln in lines[start:]:
        if ln.strip() == "":
            block.append("\n")
            continue
        # The block ends at the first non-blank line indented less than the body.
        if not ln.startswith("    "):
            break
        block.append(ln)
    src = textwrap.dedent("".join(block))
    # Module-level config reads a few required env vars at import; set
    # placeholders so the import is side-effect-free (the functions under test
    # do no I/O).
    os.environ.setdefault("EMBEDDINGS_URL", "http://localhost:0")
    os.environ.setdefault("EMBED_MODEL", "bge-test")
    os.environ.setdefault("QDRANT_URL", "http://localhost:0")
    path = os.path.join(tempfile.mkdtemp(), "reembed_under_test.py")
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(src)
    spec = importlib.util.spec_from_file_location("reembed_under_test", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


reembed = _load_script()


class DefaultCollection(unittest.TestCase):
    def test_sanitizes_repo_name(self):
        self.assertEqual(
            reembed.default_collection("loom-core"),
            "codebase_memory_bge_loom_core_v1",
        )

    def test_lowercases_and_strips(self):
        self.assertEqual(
            reembed.default_collection("My.Repo"),
            "codebase_memory_bge_my_repo_v1",
        )

    def test_empty_name_falls_back(self):
        self.assertEqual(
            reembed.default_collection("---"),
            "codebase_memory_bge_repo_v1",
        )


class ParseReposSingleFallback(unittest.TestCase):
    """Empty REPOS must reproduce the legacy single-repo behaviour."""

    def test_legacy_explicit_collection(self):
        self.assertEqual(
            reembed.parse_repos(
                "", "/ws/services/loom-core", "loom-core", "codebase_memory_bge_v1"
            ),
            [("loom-core", "/ws/services/loom-core", "codebase_memory_bge_v1")],
        )

    def test_legacy_name_from_basename(self):
        self.assertEqual(
            reembed.parse_repos("", "/ws/services/foo/", "", ""),
            [("foo", "/ws/services/foo", "codebase_memory_bge_foo_v1")],
        )

    def test_no_repo_path_is_empty(self):
        self.assertEqual(reembed.parse_repos("", "", "", ""), [])


class ParseReposMulti(unittest.TestCase):
    def test_two_repos_explicit_collections(self):
        env = (
            "loom-core=/ws/services/loom-core=codebase_memory_bge_v1;"
            "loom=/ws/services/loom=codebase_memory_bge_loom_v1"
        )
        self.assertEqual(
            reembed.parse_repos(env),
            [
                ("loom-core", "/ws/services/loom-core", "codebase_memory_bge_v1"),
                ("loom", "/ws/services/loom", "codebase_memory_bge_loom_v1"),
            ],
        )

    def test_collection_defaults_when_omitted(self):
        self.assertEqual(
            reembed.parse_repos("loom=/ws/services/loom"),
            [("loom", "/ws/services/loom", "codebase_memory_bge_loom_v1")],
        )

    def test_repos_overrides_single_repo_fallback(self):
        # When REPOS is set, the legacy REPO_PATH/REPO_NAME args are ignored.
        out = reembed.parse_repos("a=/p/a", "/ignored", "ignored", "ignored_coll")
        self.assertEqual(out, [("a", "/p/a", "codebase_memory_bge_a_v1")])

    def test_trailing_slash_and_whitespace_stripped(self):
        out = reembed.parse_repos("  loom = /ws/services/loom/ ; ")
        self.assertEqual(
            out, [("loom", "/ws/services/loom", "codebase_memory_bge_loom_v1")]
        )

    def test_blank_records_skipped(self):
        out = reembed.parse_repos(";a=/p/a;;")
        self.assertEqual(out, [("a", "/p/a", "codebase_memory_bge_a_v1")])

    def test_malformed_record_raises(self):
        with self.assertRaises(ValueError):
            reembed.parse_repos("justaname")
        with self.assertRaises(ValueError):
            reembed.parse_repos("=/p/only-path")


class PointIdRepoIsolation(unittest.TestCase):
    def test_deterministic(self):
        self.assertEqual(
            reembed.point_id("loom", "cmd/main.go", 3),
            reembed.point_id("loom", "cmd/main.go", 3),
        )

    def test_different_repo_same_path_does_not_collide(self):
        # The regression guard for multi-repo: identical path+index in two repos
        # must produce distinct point IDs (else one repo overwrites the other).
        self.assertNotEqual(
            reembed.point_id("loom", "cmd/main.go", 0),
            reembed.point_id("loom-core", "cmd/main.go", 0),
        )


class ChunkFileMultiRepo(unittest.TestCase):
    def test_prefix_uses_repo_name_and_relpath(self):
        d = tempfile.mkdtemp()
        repo_path = os.path.join(d, "loom")
        os.makedirs(os.path.join(repo_path, "cmd"))
        fpath = os.path.join(repo_path, "cmd", "main.go")
        with open(fpath, "w", encoding="utf-8") as fh:
            fh.write("package main\n\nfunc main() {}\n")
        chunks = list(reembed.chunk_file(fpath, "loom", repo_path))
        self.assertTrue(chunks)
        self.assertEqual(chunks[0]["rel"], "cmd/main.go")
        self.assertTrue(chunks[0]["text"].startswith("# loom/cmd/main.go\n"))


if __name__ == "__main__":
    unittest.main(verbosity=2)

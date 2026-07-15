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
import io
import json
import os
import tempfile
import textwrap
import unittest
import urllib.error
import urllib.request

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

    def test_iter_files_excludes_generated_coverage_trees(self):
        root = tempfile.mkdtemp()
        kept = os.path.join(root, "src", "main.js")
        generated = os.path.join(root, "coverage", "prettify.js")
        os.makedirs(os.path.dirname(kept))
        os.makedirs(os.path.dirname(generated))
        for path in (kept, generated):
            with open(path, "w", encoding="utf-8") as fh:
                fh.write("const value = 1;\n")

        files = list(reembed.iter_files(root))

        self.assertEqual(files, [kept])


class EmbeddingOverflowRecovery(unittest.TestCase):
    """A dense-code chunk can exceed BGE's 512-token physical batch even when
    it fits the character budget. The batch must isolate and shrink that one
    record without dropping its neighbors or embedding text that differs from
    the Qdrant payload."""

    def setUp(self):
        self._orig_embed_once = getattr(reembed, "_embed_once", None)
        self._orig_log = reembed.log
        self.logs = []
        reembed.log = self.logs.append

    def tearDown(self):
        if self._orig_embed_once is None:
            reembed.__dict__.pop("_embed_once", None)
        else:
            reembed._embed_once = self._orig_embed_once
        reembed.log = self._orig_log

    def test_isolates_and_shrinks_only_the_oversized_record(self):
        records = [
            {
                "repo": "loom",
                "rel": "src/dense.ts",
                "chunk_index": 7,
                "text": "# loom/src/dense.ts\n" + "x=>y??z::" * 80,
            },
            {
                "repo": "loom",
                "rel": "README.md",
                "chunk_index": 2,
                "text": "# loom/README.md\nshort prose",
            },
        ]
        original_short = records[1]["text"]

        def fake_embed_once(texts):
            if any(len(text) > 550 for text in texts):
                raise reembed.EmbeddingInputTooLarge(564, 512)
            return [[float(len(text))] for text in texts]

        reembed._embed_once = fake_embed_once
        embedded = reembed.embed_records(records)

        self.assertEqual(len(embedded), 2)
        long_record, long_vector = embedded[0]
        short_record, short_vector = embedded[1]
        self.assertLessEqual(len(long_record["text"]), 550)
        self.assertTrue(long_record["text"].startswith("# loom/src/dense.ts\n"))
        self.assertEqual(long_vector, [float(len(long_record["text"]))])
        self.assertEqual(short_record["text"], original_short)
        self.assertEqual(short_vector, [float(len(original_short))])
        self.assertTrue(any("src/dense.ts" in line for line in self.logs))
        self.assertTrue(any("564" in line and "512" in line for line in self.logs))

    def test_parses_llamacpp_physical_batch_error(self):
        error = RuntimeError(
            'HTTP 500: {"message":"input (564 tokens) is too large to process. '
            'increase the physical batch size (current batch size: 512)"}'
        )
        parsed = reembed.embedding_limit_from_error(error)
        self.assertEqual(parsed, (564, 512))


class QdrantCollectionTuning(unittest.TestCase):
    def setUp(self):
        self._orig_do = reembed._do
        self.requests = []

    def tearDown(self):
        reembed._do = self._orig_do

    def test_patches_indexing_threshold_when_collection_is_unindexed(self):
        def fake_do(req, **_kwargs):
            self.requests.append(req)
            if len(self.requests) == 1:
                return 200, {
                    "result": {
                        "config": {"optimizer_config": {"indexing_threshold": 20000}}
                    }
                }
            return 200, {"result": True}

        reembed._do = fake_do
        reembed.tune_collection("codebase_memory_bge_v1")
        self.assertEqual(len(self.requests), 2)
        self.assertEqual(self.requests[1].get_method(), "PATCH")
        payload = json.loads(self.requests[1].data)
        self.assertEqual(
            payload["optimizers_config"]["indexing_threshold"],
            reembed.QDRANT_INDEXING_THRESHOLD_KB,
        )

    def test_skips_patch_when_threshold_is_already_correct(self):
        def fake_do(req, **_kwargs):
            self.requests.append(req)
            return 200, {
                "result": {
                    "config": {
                        "optimizer_config": {
                            "indexing_threshold": reembed.QDRANT_INDEXING_THRESHOLD_KB
                        }
                    }
                }
            }

        reembed._do = fake_do
        reembed.tune_collection("codebase_memory_bge_v1")
        self.assertEqual(len(self.requests), 1)


class QdrantStalePointPruning(unittest.TestCase):
    def setUp(self):
        self._orig_do = reembed._do
        self._orig_log = reembed.log
        self.requests = []
        self.logs = []
        reembed.log = self.logs.append

    def tearDown(self):
        reembed._do = self._orig_do
        reembed.log = self._orig_log

    def test_prunes_only_obsolete_repo_points_after_paginated_scroll(self):
        active = {"keep-a", "keep-b"}

        def fake_do(req, **_kwargs):
            self.requests.append(req)
            if req.full_url.endswith("/points/scroll"):
                payload = json.loads(req.data)
                if "offset" not in payload:
                    return 200, {
                        "result": {
                            "points": [{"id": "keep-a"}, {"id": "stale-a"}],
                            "next_page_offset": "cursor-2",
                        }
                    }
                self.assertEqual(payload["offset"], "cursor-2")
                return 200, {
                    "result": {
                        "points": [{"id": "keep-b"}, {"id": "stale-b"}],
                        "next_page_offset": None,
                    }
                }
            return 200, {"result": True}

        reembed._do = fake_do
        removed = reembed.prune_stale_points("collection", "loom", active)

        self.assertEqual(removed, 2)
        delete_requests = [r for r in self.requests if "/points/delete" in r.full_url]
        self.assertEqual(len(delete_requests), 1)
        self.assertEqual(delete_requests[0].get_method(), "POST")
        self.assertEqual(
            set(json.loads(delete_requests[0].data)["points"]),
            {"stale-a", "stale-b"},
        )
        scroll_payload = json.loads(self.requests[0].data)
        self.assertEqual(
            scroll_payload["filter"]["must"][0],
            {"key": "repo", "match": {"value": "loom"}},
        )
        self.assertTrue(any("pruned 2 stale" in line for line in self.logs))


class _FakeResp:
    """Minimal context-manager stand-in for an http.client.HTTPResponse."""

    def __init__(self, status, body):
        self.status = status
        self._body = body

    def read(self):
        return self._body

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def _http_error(code, body=b"upstream_unavailable"):
    return urllib.error.HTTPError(
        "http://proxy/v1/embeddings", code, "err", None, io.BytesIO(body)
    )


class _ScriptedUrlopen:
    """Replays a scripted list of urlopen outcomes (raise if Exception, else
    return), counting calls. Outcomes past the end repeat the last entry."""

    def __init__(self, outcomes):
        self.outcomes = list(outcomes)
        self.calls = 0

    def __call__(self, req, timeout=None):
        outcome = self.outcomes[min(self.calls, len(self.outcomes) - 1)]
        self.calls += 1
        if isinstance(outcome, Exception):
            raise outcome
        return outcome


class DoRetry(unittest.TestCase):
    """`_do` rides out transient 502/503/504 + connection errors but fails fast on
    deterministic ones. Regression for the 2026-06-27 run that aborted on a single
    bge-proxy ``502 upstream_unavailable`` when the model reloaded mid-batch."""

    def setUp(self):
        self._orig_urlopen = reembed.urllib.request.urlopen
        self._orig_sleep = reembed.time.sleep
        self.sleeps = []
        reembed.time.sleep = lambda d: self.sleeps.append(d)

    def tearDown(self):
        reembed.urllib.request.urlopen = self._orig_urlopen
        reembed.time.sleep = self._orig_sleep

    def _run(self, outcomes, **kw):
        fake = _ScriptedUrlopen(outcomes)
        reembed.urllib.request.urlopen = fake
        req = reembed._req("http://proxy/v1/embeddings", data={"x": 1})
        return reembed._do(req, **kw), fake

    def test_retries_502_then_succeeds(self):
        ok = _FakeResp(200, b'{"ok": true}')
        (status, body), fake = self._run([_http_error(502), _http_error(502), ok])
        self.assertEqual(status, 200)
        self.assertEqual(body, {"ok": True})
        self.assertEqual(fake.calls, 3)  # two failures + one success
        self.assertEqual(len(self.sleeps), 2)  # one backoff per retry

    def test_retries_connection_error_then_succeeds(self):
        ok = _FakeResp(200, b"{}")
        err = urllib.error.URLError("connection refused")
        (status, _body), fake = self._run([err, ok])
        self.assertEqual(status, 200)
        self.assertEqual(fake.calls, 2)
        self.assertEqual(len(self.sleeps), 1)

    def test_non_transient_status_fails_fast(self):
        # A 400 is deterministic; retrying just burns time on a 10-minute batch.
        with self.assertRaises(RuntimeError):
            self._run([_http_error(400)])
        self.assertEqual(self.sleeps, [])

    def test_expected_status_short_circuits(self):
        # ensure_collection probes with expect=(200, 404); a 404 must not retry.
        (status, body), fake = self._run([_http_error(404)], expect=(200, 404))
        self.assertEqual(status, 404)
        self.assertIsNone(body)
        self.assertEqual(fake.calls, 1)
        self.assertEqual(self.sleeps, [])

    def test_exhausts_retries_then_raises(self):
        orig_retries = reembed.HTTP_RETRIES
        reembed.HTTP_RETRIES = 3
        try:
            with self.assertRaises(RuntimeError) as ctx:
                self._run([_http_error(503) for _ in range(3)])
        finally:
            reembed.HTTP_RETRIES = orig_retries
        self.assertIn("503", str(ctx.exception))
        self.assertEqual(len(self.sleeps), 2)  # 3 attempts -> 2 backoffs


class SleepBackoff(unittest.TestCase):
    def test_exponential_with_jitter_within_bounds(self):
        # Equal jitter: each wait lands in [d/2, d] for d = base * 2**attempt,
        # capped. Verify monotonic growth of the lower bound and the cap.
        orig_sleep = reembed.time.sleep
        recorded = []
        reembed.time.sleep = lambda d: recorded.append(d)
        try:
            for attempt in range(6):
                reembed._sleep_backoff(attempt)
        finally:
            reembed.time.sleep = orig_sleep
        for attempt, waited in enumerate(recorded):
            d = min(reembed.RETRY_BACKOFF_CAP, reembed.RETRY_BACKOFF * (2**attempt))
            self.assertGreaterEqual(waited, d / 2)
            self.assertLessEqual(waited, d)


if __name__ == "__main__":
    unittest.main(verbosity=2)

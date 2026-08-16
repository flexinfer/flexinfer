#!/usr/bin/env python3
"""Offline regression tests for codebase-answer readiness and routing config."""
from __future__ import annotations

import importlib.util
import json
import os
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
CONFIGMAP = os.path.join(HERE, "configmap.yaml")
DEPLOYMENT = os.path.join(HERE, "deployment.yaml")
VALUES = os.path.join(HERE, "..", "values-k3s.yaml")
PROMETHEUS_RULE = os.path.join(HERE, "prometheusrule.yaml")


def _load_script():
    with open(CONFIGMAP, "r", encoding="utf-8") as fh:
        lines = fh.readlines()
    start = next(
        (
            i + 1
            for i, line in enumerate(lines)
            if line.rstrip().endswith("rag_answer.py: |")
        ),
        None,
    )
    if start is None:
        raise AssertionError("rag_answer.py block not found")
    block = []
    for line in lines[start:]:
        if line.strip() == "":
            block.append("\n")
            continue
        if not line.startswith("    "):
            break
        block.append(line)
    src = textwrap.dedent("".join(block))
    os.environ.setdefault("PROXY_URL", "http://proxy.test")
    os.environ.setdefault("EMBED_MODEL", "bge-large-radeonvii")
    os.environ.setdefault("RERANK_MODEL", "bge-reranker-radeonvii")
    os.environ.setdefault("CHAT_MODEL", "loom-workhorse")
    os.environ.setdefault("QDRANT_URL", "http://qdrant.test")
    path = os.path.join(tempfile.mkdtemp(), "rag_answer_under_test.py")
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(src)
    spec = importlib.util.spec_from_file_location("rag_answer_under_test", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


rag = _load_script()


def _catalog(*, workhorse_ready=True, include_workhorse=True, embed_ready=True):
    data = [
        {
            "id": "bge-large-radeonvii",
            "metadata": {
                "ready": embed_ready,
                "service_labels": ["embeddings"],
            },
        },
        {
            "id": "bge-reranker-radeonvii",
            "metadata": {"ready": True, "service_labels": ["rerank"]},
        },
    ]
    if include_workhorse:
        data.append(
            {
                "id": "qwen35-workhorse-a",
                "metadata": {
                    "ready": workhorse_ready,
                    "phase": "Ready" if workhorse_ready else "Idle",
                    "service_labels": ["loom-workhorse", "workhorse-128k"],
                },
            }
        )
    return {"data": data}


class Readiness(unittest.TestCase):
    def setUp(self):
        self._orig_fetch = getattr(rag, "_fetch_json", None)

    def tearDown(self):
        if self._orig_fetch is None:
            rag.__dict__.pop("_fetch_json", None)
        else:
            rag._fetch_json = self._orig_fetch

    def _install_fetch(
        self,
        *,
        workhorse_ready=True,
        include_workhorse=True,
        embed_ready=True,
        qdrant_status="green",
    ):
        def fake_fetch(url, headers=None, timeout=None):
            if url.endswith("/v1/models"):
                return _catalog(
                    workhorse_ready=workhorse_ready,
                    include_workhorse=include_workhorse,
                    embed_ready=embed_ready,
                )
            if "/collections/" in url:
                return {"result": {"status": qdrant_status}}
            raise AssertionError(f"unexpected readiness URL: {url}")

        rag._fetch_json = fake_fetch

    def test_ready_requires_all_three_model_routes_and_green_qdrant(self):
        self._install_fetch()
        ready, checks = rag.readiness_status()
        self.assertTrue(ready)
        self.assertEqual(checks["models"], "ok")
        self.assertEqual(checks["chat"], "ready")
        self.assertEqual(checks["qdrant"], "green")

    def test_serverless_workhorse_route_keeps_readiness_ready(self):
        self._install_fetch(workhorse_ready=False)
        ready, checks = rag.readiness_status()
        self.assertTrue(ready)
        self.assertEqual(checks["models"], "ok")
        self.assertEqual(checks["chat"], "cold-startable")

    def test_missing_workhorse_route_makes_readiness_fail(self):
        self._install_fetch(include_workhorse=False)
        ready, checks = rag.readiness_status()
        self.assertFalse(ready)
        self.assertIn("loom-workhorse", checks["models"])
        self.assertEqual(checks["chat"], "not-advertised")

    def test_serverless_embedding_route_makes_readiness_fail(self):
        self._install_fetch(embed_ready=False)
        ready, checks = rag.readiness_status()
        self.assertFalse(ready)
        self.assertIn("bge-large-radeonvii", checks["models"])

    def test_yellow_collection_remains_ready_during_optimization(self):
        self._install_fetch(qdrant_status="yellow")
        ready, checks = rag.readiness_status()
        self.assertTrue(ready)
        self.assertEqual(checks["qdrant"], "yellow")

    def test_red_collection_makes_readiness_fail(self):
        self._install_fetch(qdrant_status="red")
        ready, checks = rag.readiness_status()
        self.assertFalse(ready)
        self.assertEqual(checks["qdrant"], "red")


class DeclarativeRouting(unittest.TestCase):
    def test_deployment_uses_stable_workhorse_label(self):
        with open(DEPLOYMENT, "r", encoding="utf-8") as fh:
            deployment = fh.read()
        self.assertIn('value: "loom-workhorse"', deployment)
        self.assertNotIn('value: "qwen36-35b-mtp-uncensored-5930k"', deployment)
        self.assertIn('value: "gemma4-e4b-radeonvii"', deployment)
        self.assertIn(
            'flexinfer.ai/config-rev: "2026-08-16-retrieve-only-and-chat-fallback"',
            deployment,
        )

    def test_cluster_values_explicitly_wire_rag_upstream(self):
        with open(os.path.normpath(VALUES), "r", encoding="utf-8") as fh:
            values = fh.read()
        self.assertIn("codebaseAnswerUpstream:", values)

    def test_stale_alert_accepts_manual_job_success(self):
        with open(PROMETHEUS_RULE, "r", encoding="utf-8") as fh:
            rule = fh.read()
        self.assertIn("kube_cronjob_status_last_successful_time", rule)
        self.assertIn("kube_job_status_completion_time", rule)
        self.assertIn('job_name=~"codebase-reembed.*"', rule)


class GenerationMode(unittest.TestCase):
    def setUp(self):
        self._orig_do = rag._do
        self._orig_fallback = rag.CHAT_FALLBACK_MODEL

    def tearDown(self):
        rag._do = self._orig_do
        rag.CHAT_FALLBACK_MODEL = self._orig_fallback

    def test_chat_disables_reasoning_trace_in_answer_content(self):
        captured = {}

        def fake_do(req, retries=3, timeout=None):
            captured.update(json.loads(req.data))
            return {"choices": [{"message": {"content": "final answer"}}]}

        rag._do = fake_do
        answer, model_used = rag.chat("system", "question", 80)

        self.assertEqual(answer, "final answer")
        self.assertEqual(model_used, rag.CHAT_MODEL)
        self.assertEqual(captured["chat_template_kwargs"], {"enable_thinking": False})

    def test_chat_uses_single_long_attempt_not_retry_ladder(self):
        captured = {}

        def fake_do(req, retries=3, timeout=None):
            captured["retries"] = retries
            captured["timeout"] = timeout
            return {"choices": [{"message": {"content": "ok"}}]}

        rag._do = fake_do
        rag.chat("system", "question", 80)

        # A serverless chat request is held in the proxy cold-start queue;
        # client-side retries abandon the queue slot and re-queue from the
        # back, so chat must ride ONE attempt with the long chat deadline.
        self.assertEqual(captured["retries"], rag.CHAT_RETRIES)
        self.assertEqual(captured["timeout"], rag.CHAT_TIMEOUT)

    def test_chat_falls_back_to_secondary_model_when_primary_fails(self):
        rag.CHAT_FALLBACK_MODEL = "small-fallback"
        calls = []

        def fake_do(req, retries=3, timeout=None):
            body = json.loads(req.data)
            calls.append(body["model"])
            if body["model"] == rag.CHAT_MODEL:
                raise RuntimeError("primary lane preempted")
            return {"choices": [{"message": {"content": "degraded answer"}}]}

        rag._do = fake_do
        answer, model_used = rag.chat("system", "question", 80)

        self.assertEqual(calls, [rag.CHAT_MODEL, "small-fallback"])
        self.assertEqual(answer, "degraded answer")
        self.assertEqual(model_used, "small-fallback")

    def test_chat_raises_when_primary_fails_and_no_fallback_configured(self):
        rag.CHAT_FALLBACK_MODEL = ""

        def fake_do(req, retries=3, timeout=None):
            raise RuntimeError("primary lane preempted")

        rag._do = fake_do
        with self.assertRaises(RuntimeError):
            rag.chat("system", "question", 80)


class RetrieveOnly(unittest.TestCase):
    def setUp(self):
        self._orig = {
            name: getattr(rag, name)
            for name in ("embed", "qdrant_search", "rerank", "chat")
        }

    def tearDown(self):
        for name, fn in self._orig.items():
            setattr(rag, name, fn)

    def _install_retrieval(self):
        rag.embed = lambda text: [0.0] * 4
        rag.qdrant_search = lambda vector, collection, limit: [
            {"payload": {"text": "chunk a", "path": "pkg/a.go"}, "score": 0.9},
            {"payload": {"text": "chunk b", "path": "pkg/b.go"}, "score": 0.8},
        ]
        rag.rerank = lambda query, docs, top_n: [1, 0]

    def test_retrieve_only_returns_chunk_text_and_skips_chat(self):
        self._install_retrieval()

        def exploding_chat(system, user, max_tokens):
            raise AssertionError("retrieve_only must never reach the chat lane")

        rag.chat = exploding_chat
        out = rag.answer(
            "q", "col", top_n=2, top_k=2, max_per_path=0, retrieve_only=True
        )

        self.assertIsNone(out["answer"])
        self.assertIsNone(out["model"])
        self.assertEqual(
            [c["path"] for c in out["citations"]], ["pkg/b.go", "pkg/a.go"]
        )
        self.assertEqual([c["text"] for c in out["citations"]], ["chunk b", "chunk a"])

    def test_full_answer_reports_model_actually_used(self):
        self._install_retrieval()
        rag.chat = lambda system, user, max_tokens: ("done", "fallback-model")
        out = rag.answer("q", "col", top_n=2, top_k=2, max_per_path=0)

        self.assertEqual(out["answer"], "done")
        self.assertEqual(out["model"], "fallback-model")
        # Non-retrieve_only responses keep the lean citation shape.
        self.assertNotIn("text", out["citations"][0])


if __name__ == "__main__":
    unittest.main(verbosity=2)

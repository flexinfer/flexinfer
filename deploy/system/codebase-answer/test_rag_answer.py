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
        (i + 1 for i, line in enumerate(lines) if line.rstrip().endswith("rag_answer.py: |")),
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


def _catalog(workhorse_ready=True):
    return {
        "data": [
            {
                "id": "bge-large-radeonvii",
                "metadata": {"ready": True, "service_labels": ["embeddings"]},
            },
            {
                "id": "bge-reranker-radeonvii",
                "metadata": {"ready": True, "service_labels": ["rerank"]},
            },
            {
                "id": "qwen35-workhorse-a",
                "metadata": {
                    "ready": workhorse_ready,
                    "service_labels": ["loom-workhorse", "workhorse-128k"],
                },
            },
        ]
    }


class Readiness(unittest.TestCase):
    def setUp(self):
        self._orig_fetch = getattr(rag, "_fetch_json", None)

    def tearDown(self):
        if self._orig_fetch is None:
            rag.__dict__.pop("_fetch_json", None)
        else:
            rag._fetch_json = self._orig_fetch

    def _install_fetch(self, *, workhorse_ready=True, qdrant_status="green"):
        def fake_fetch(url, headers=None, timeout=None):
            if url.endswith("/v1/models"):
                return _catalog(workhorse_ready)
            if "/collections/" in url:
                return {"result": {"status": qdrant_status}}
            raise AssertionError(f"unexpected readiness URL: {url}")

        rag._fetch_json = fake_fetch

    def test_ready_requires_all_three_model_routes_and_green_qdrant(self):
        self._install_fetch()
        ready, checks = rag.readiness_status()
        self.assertTrue(ready)
        self.assertEqual(checks["models"], "ok")
        self.assertEqual(checks["qdrant"], "green")

    def test_preempted_workhorse_makes_readiness_fail(self):
        self._install_fetch(workhorse_ready=False)
        ready, checks = rag.readiness_status()
        self.assertFalse(ready)
        self.assertIn("loom-workhorse", checks["models"])

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
        self.assertIn(
            'flexinfer.ai/config-rev: "2026-07-15-rag-final-answer-v2"',
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

    def tearDown(self):
        rag._do = self._orig_do

    def test_chat_disables_reasoning_trace_in_answer_content(self):
        captured = {}

        def fake_do(req, retries=3):
            captured.update(json.loads(req.data))
            return {"choices": [{"message": {"content": "final answer"}}]}

        rag._do = fake_do
        answer = rag.chat("system", "question", 80)

        self.assertEqual(answer, "final answer")
        self.assertEqual(
            captured["chat_template_kwargs"], {"enable_thinking": False}
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)

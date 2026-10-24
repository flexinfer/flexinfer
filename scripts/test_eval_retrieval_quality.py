"""Retrieval-quality harness: corpus extraction, metrics and request splitting."""

import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest import mock
import urllib.error

spec = importlib.util.spec_from_file_location(
    "eval_rq", Path(__file__).with_name("eval-retrieval-quality.py")
)
evalrq = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evalrq)

GO_SOURCE = """package demo

// retryCall retries call with exponential backoff. It gives up after five attempts.
// Callers own the context.
func retryCall(ctx context.Context, call func() error) error {
\tfor attempt := 0; attempt < 5; attempt++ {
\t\tif err := call(); err == nil {
\t\t\treturn nil
\t\t}
\t}
\treturn errors.New("exhausted")
}

func undocumented() {
}

//go:generate stringer
func generated() {
}
"""


class CorpusTests(unittest.TestCase):
    def test_extracts_documented_funcs_only(self):
        funcs = list(evalrq.go_functions(GO_SOURCE))
        self.assertEqual([name for name, _, _ in funcs], ["retryCall"])
        name, doc, code = funcs[0]
        self.assertTrue(code.startswith("func retryCall(") and code.endswith("}"))
        self.assertNotIn("//", code)
        self.assertIn("Callers own the context.", doc)

    def test_query_drops_the_identifier_and_keeps_two_sentences(self):
        name, doc, _ = next(evalrq.go_functions(GO_SOURCE))
        query = evalrq.doc_to_query(name, doc)
        self.assertEqual(
            query,
            "retries call with exponential backoff. It gives up after five attempts.",
        )

    def test_repo_set_pairs_each_query_with_its_function(self):
        with mock.patch.object(evalrq, "GO_SOURCE_DIRS", ("pkg",)):
            root = Path(self._tmp()) / "repo"
            (root / "pkg").mkdir(parents=True)
            (root / "pkg" / "demo.go").write_text(GO_SOURCE)
            (root / "pkg" / "demo_test.go").write_text(GO_SOURCE)
            s = evalrq.repo_go_set(root, limit=10)
        self.assertEqual(len(s["queries"]), 1)
        key = next(iter(s["queries"]))
        self.assertEqual(s["qrels"][key], [key])
        self.assertEqual(s["origin"][key], "pkg/demo.go:retryCall")

    def _tmp(self):
        import tempfile

        d = tempfile.mkdtemp()
        self.addCleanup(lambda: __import__("shutil").rmtree(d, ignore_errors=True))
        return d


class MetricTests(unittest.TestCase):
    def test_single_relevant_metrics(self):
        import numpy as np

        m = evalrq.metrics(np.array([1, 2, 11]))
        self.assertEqual(m["queries"], 3)
        self.assertAlmostEqual(m["mrr@10"], round((1 + 0.5 + 0) / 3, 4))
        self.assertAlmostEqual(m["recall@1"], round(1 / 3, 4))
        self.assertAlmostEqual(m["recall@10"], round(2 / 3, 4))

    def test_gold_rank_and_truncation_renormalize(self):
        import numpy as np

        docs = np.array([[1.0, 0.0, 0.0], [0.0, 1.0, 0.0], [0.6, 0.0, 0.8]])
        queries = np.array([[0.0, 1.0, 0.0], [0.6, 0.0, 0.8]])
        ranks = evalrq.gold_ranks(
            ["q1", "q2"], queries, ["a", "b", "c"], docs, {"q1": ["b"], "q2": ["a"]}
        )
        self.assertEqual(ranks.tolist(), [1, 2])
        cut = evalrq.truncate(docs, 2)
        self.assertTrue(np.allclose(np.linalg.norm(cut, axis=1), 1.0))

    def test_spearman(self):
        self.assertAlmostEqual(evalrq.spearman([1, 2, 3], [10, 20, 30]), 1.0)
        self.assertAlmostEqual(evalrq.spearman([1, 2, 3], [3, 2, 1]), -1.0)


class EmbedderTests(unittest.TestCase):
    def test_splits_batches_and_shortens_single_over_long_inputs(self):
        calls = []

        def fake_call(self, texts):
            calls.append(list(texts))
            if any(len(t) > 100 for t in texts):
                raise urllib.error.HTTPError(
                    "u", 400, "too long", {}, io.BytesIO(b"{}")
                )
            return [[1.0, 0.0] for _ in texts]

        emb = evalrq.Embedder("http://x", "m")
        with mock.patch.object(evalrq.Embedder, "_call", fake_call):
            out = emb.embed(["short", "x" * 150])
        self.assertEqual(len(out), 2)
        self.assertEqual(emb.shortened, 1)
        self.assertEqual(calls[0], ["short", "x" * 150])
        self.assertEqual(calls[-1], ["x" * 75])

    def test_server_errors_on_single_inputs_are_not_masked(self):
        def fake_call(self, texts):
            raise urllib.error.HTTPError("u", 503, "busy", {}, io.BytesIO(b"{}"))

        with mock.patch.object(evalrq.Embedder, "_call", fake_call):
            with self.assertRaises(urllib.error.HTTPError):
                evalrq.Embedder("http://x", "m").embed(["only"])


class PrefixTests(unittest.TestCase):
    def test_query_formats_follow_each_model_card(self):
        self.assertTrue(evalrq.QUERY_PREFIXES["qwen3"].endswith("\nQuery:"))
        self.assertTrue(evalrq.QUERY_PREFIXES["gte-qwen2"].endswith("\nQuery: "))
        self.assertEqual(json.loads(json.dumps(evalrq.QUERY_PREFIXES))["none"], "")


if __name__ == "__main__":
    unittest.main()

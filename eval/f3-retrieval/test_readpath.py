#!/usr/bin/env python3
"""Unit tests for the read-path context-selection logic (F3 plan Slice 3.1).

Pins ``diversify_selection`` — the pure function that caps how many reranked
chunks one file may contribute to the top-K context, so multi-file questions get
secondary files into the window instead of one file dominating. This is the
Slice 3 "answer synthesis on multi-file questions" lever, made opt-in and
reversible (``max_per_path <= 0`` restores the original top-K slice).

Run standalone like the other repo unittest scripts (no pip deps)::

    python3 eval/f3-retrieval/test_readpath.py

``f3eval`` reads ``PROXY_URL`` at import; set a placeholder so the import is
side-effect-free in a test context (the function under test does no I/O).
"""
from __future__ import annotations

import os
import unittest

os.environ.setdefault("PROXY_URL", "http://localhost:0")

import f3eval  # noqa: E402  (must follow the env placeholder above)

diversify = f3eval.diversify_selection


class DisabledIsOriginalBehaviour(unittest.TestCase):
    """max_per_path <= 0 must return order[:top_k] byte-for-byte."""

    def test_zero_disables(self):
        order = [0, 1, 2, 3, 4, 5, 6, 7]
        paths = ["a"] * 8
        self.assertEqual(diversify(order, paths, 6, 0), [0, 1, 2, 3, 4, 5])

    def test_negative_disables(self):
        order = [3, 1, 2, 0]
        paths = ["a", "b", "c", "d"]
        self.assertEqual(diversify(order, paths, 2, -1), [3, 1])

    def test_disabled_preserves_rerank_order(self):
        order = [5, 4, 3, 2, 1, 0]
        paths = ["x"] * 6
        self.assertEqual(diversify(order, paths, 3, 0), [5, 4, 3])


class CapPullsSecondaryFiles(unittest.TestCase):
    def test_multi_file_coverage_improves(self):
        # candidate index -> path
        paths = ["a", "a", "a", "b", "a", "c", "b", "d"]
        order = [0, 1, 2, 3, 4, 5, 6, 7]
        # Baseline (disabled) top-4 = a,a,a,b -> only 2 distinct files.
        self.assertEqual(diversify(order, paths, 4, 0), [0, 1, 2, 3])
        # Capped at 2/path -> a,a,b,c -> 3 distinct files, secondary 'c' pulled in.
        chosen = diversify(order, paths, 4, 2)
        self.assertEqual(chosen, [0, 1, 3, 5])
        self.assertEqual({paths[i] for i in chosen}, {"a", "b", "c"})

    def test_rerank_order_preserved_within_selection(self):
        paths = ["a", "b", "a", "b", "a"]
        order = [0, 1, 2, 3, 4]
        chosen = diversify(order, paths, 4, 1)
        # cap 1/path: take 0(a),1(b); 2,3,4 capped -> pass2 backfills 2,3
        self.assertEqual(chosen, [0, 1, 2, 3])
        # selection is monotonic in the original rerank order
        self.assertEqual(chosen, sorted(chosen, key=order.index))


class BackfillNeverStarvesContext(unittest.TestCase):
    def test_single_file_domination_backfills_to_top_k(self):
        # Every candidate is the same file: cap can't help, but we must NOT
        # return fewer chunks than the plain top-K slice would.
        order = [0, 1, 2, 3, 4, 5]
        paths = ["only"] * 6
        self.assertEqual(diversify(order, paths, 4, 2), [0, 1, 2, 3])

    def test_two_files_backfill_order(self):
        paths = ["a", "a", "a", "b"]
        order = [0, 1, 2, 3]
        # cap 1: pass1 -> 0(a),3(b); pass2 backfills 1,2 in rerank order
        self.assertEqual(diversify(order, paths, 4, 1), [0, 3, 1, 2])


class EdgeCases(unittest.TestCase):
    def test_fewer_candidates_than_top_k(self):
        self.assertEqual(diversify([0, 1], ["a", "b"], 6, 2), [0, 1])

    def test_empty_order(self):
        self.assertEqual(diversify([], [], 6, 2), [])

    def test_paths_shorter_than_index_is_defensive(self):
        # A malformed/short paths list must not crash; missing path -> "".
        order = [0, 1, 2]
        paths = ["a"]  # indices 1,2 have no path
        chosen = diversify(order, paths, 3, 2)
        self.assertEqual(sorted(chosen), [0, 1, 2])

    def test_top_k_zero(self):
        self.assertEqual(diversify([0, 1, 2], ["a", "b", "c"], 0, 2), [])


if __name__ == "__main__":
    unittest.main(verbosity=2)

#!/usr/bin/env python3
"""Unit tests for the retrieval-quality gate kernel (F3 plan Slice 5).

Pins ``rqgate`` — the pure aggregate→gate→result_row logic that turns the F3
kill-test's per-question rows into a reusable promotion gate (absolute thresholds
+ structured score row). The kernel does no I/O, so these tests are pure and run
standalone with no pip deps (mirrors ``test_readpath.py``)::

    python3 eval/f3-retrieval/test_rqgate.py
"""
from __future__ import annotations

import json
import unittest

import rqgate


def _row(kw, judge, ev):
    return {"retrieval": {"kw": kw, "judge": judge}, "ev_retrieved": ev}


class Aggregate(unittest.TestCase):
    def test_counts_and_ratios(self):
        rows = [
            _row(True, "CORRECT", True),
            _row(False, "PARTIAL", True),
            _row(False, "INCORRECT", False),
            _row(True, "CORRECT", True),
        ]
        s = rqgate.aggregate(rows)  # default partial_weight = 0.5
        self.assertEqual(s["n"], 4)
        self.assertEqual(s["judge_correct"], 2)
        self.assertEqual(s["judge_partial"], 1)
        self.assertEqual(s["kw_correct"], 2)
        self.assertEqual(s["ev_retrieved"], 3)
        # judge_score = 2 + 0.5*1 = 2.5 ; /4 = 0.625
        self.assertAlmostEqual(s["judge_score"], 2.5)
        self.assertAlmostEqual(s["judge_ratio"], 0.625)
        self.assertAlmostEqual(s["ev_ratio"], 0.75)
        self.assertAlmostEqual(s["kw_ratio"], 0.5)

    def test_partial_weight_configurable(self):
        rows = [_row(False, "PARTIAL", True), _row(False, "PARTIAL", True)]
        self.assertAlmostEqual(
            rqgate.aggregate(rows, partial_weight=0.0)["judge_ratio"], 0.0
        )
        self.assertAlmostEqual(
            rqgate.aggregate(rows, partial_weight=1.0)["judge_ratio"], 1.0
        )

    def test_empty_is_zero_not_crash(self):
        s = rqgate.aggregate([])
        self.assertEqual(s["n"], 0)
        self.assertEqual(s["judge_ratio"], 0.0)
        self.assertEqual(s["ev_ratio"], 0.0)

    def test_unknown_judge_counts_incorrect(self):
        # garbage / error markers must not earn credit
        rows = [_row(False, "wat", True), _row(False, "JUDGE_ERR(X)", True)]
        s = rqgate.aggregate(rows)
        self.assertEqual(s["judge_correct"], 0)
        self.assertEqual(s["judge_partial"], 0)

    def test_missing_keys_are_defensive(self):
        # malformed rows (missing retrieval / ev_retrieved) must not raise
        s = rqgate.aggregate([{}, {"retrieval": {}}, None])
        self.assertEqual(s["n"], 3)
        self.assertEqual(s["judge_correct"], 0)
        self.assertEqual(s["ev_retrieved"], 0)


class Gate(unittest.TestCase):
    def test_pass_when_both_axes_meet(self):
        s = rqgate.aggregate([_row(True, "CORRECT", True)] * 10)
        g = rqgate.gate(s, min_judge_ratio=0.6, min_ev_ratio=0.8)
        self.assertEqual(g["verdict"], "PASS")
        self.assertEqual(g["reasons"], [])

    def test_fail_on_judge_axis_only(self):
        # recall high (ev all retrieved) but synthesis low (all INCORRECT)
        s = rqgate.aggregate([_row(False, "INCORRECT", True)] * 10)
        g = rqgate.gate(s, min_judge_ratio=0.6, min_ev_ratio=0.8)
        self.assertEqual(g["verdict"], "FAIL")
        self.assertEqual(len(g["reasons"]), 1)
        self.assertIn("judge_ratio", g["reasons"][0])

    def test_fail_on_ev_axis_only(self):
        # synthesis high but recall low (nothing retrieved)
        s = rqgate.aggregate([_row(True, "CORRECT", False)] * 10)
        g = rqgate.gate(s, min_judge_ratio=0.6, min_ev_ratio=0.8)
        self.assertEqual(g["verdict"], "FAIL")
        self.assertEqual(len(g["reasons"]), 1)
        self.assertIn("ev_ratio", g["reasons"][0])

    def test_fail_on_both_axes(self):
        s = rqgate.aggregate([_row(False, "INCORRECT", False)] * 10)
        g = rqgate.gate(s)
        self.assertEqual(g["verdict"], "FAIL")
        self.assertEqual(len(g["reasons"]), 2)

    def test_empty_run_is_fail_not_vacuous_pass(self):
        g = rqgate.gate(rqgate.aggregate([]))
        self.assertEqual(g["verdict"], "FAIL")
        self.assertIn("no questions graded", g["reasons"])

    def test_boundary_is_inclusive(self):
        # exactly at threshold passes (>=, not >)
        # 6/10 CORRECT -> judge_ratio 0.6 ; 8/10 ev -> ev_ratio 0.8
        rows = (
            [_row(False, "CORRECT", True)] * 6
            + [_row(False, "INCORRECT", True)] * 2
            + [_row(False, "INCORRECT", False)] * 2
        )
        s = rqgate.aggregate(rows)
        self.assertAlmostEqual(s["judge_ratio"], 0.6)
        self.assertAlmostEqual(s["ev_ratio"], 0.8)
        self.assertEqual(
            rqgate.gate(s, min_judge_ratio=0.6, min_ev_ratio=0.8)["verdict"], "PASS"
        )

    def test_thresholds_reported(self):
        g = rqgate.gate(
            rqgate.aggregate([_row(True, "CORRECT", True)]),
            min_judge_ratio=0.5,
            min_ev_ratio=0.7,
        )
        self.assertEqual(g["thresholds"], {"min_judge_ratio": 0.5, "min_ev_ratio": 0.7})


class ResultRow(unittest.TestCase):
    def test_row_shape_and_serialisable(self):
        s = rqgate.aggregate(
            [_row(True, "CORRECT", True), _row(False, "PARTIAL", True)]
        )
        g = rqgate.gate(s)
        row = rqgate.result_row("qwen36-35b", "codebase_memory_bge_v1", s, g)
        self.assertEqual(row["kind"], "retrieval_quality")
        self.assertEqual(row["model"], "qwen36-35b")
        self.assertEqual(row["collection"], "codebase_memory_bge_v1")
        self.assertEqual(row["n"], 2)
        self.assertIn(row["verdict"], ("PASS", "FAIL"))
        # ratios rounded to 3 dp, JSON round-trips
        self.assertEqual(json.loads(json.dumps(row)), row)

    def test_extra_merges(self):
        s = rqgate.aggregate([_row(True, "CORRECT", True)])
        row = rqgate.result_row("m", "c", s, rqgate.gate(s), extra={"elapsed_s": 12})
        self.assertEqual(row["elapsed_s"], 12)

    def test_evaluate_one_shot(self):
        rows = [_row(True, "CORRECT", True)] * 10
        row = rqgate.evaluate(rows, "m", "c", min_judge_ratio=0.6, min_ev_ratio=0.8)
        self.assertEqual(row["verdict"], "PASS")
        self.assertEqual(row["n"], 10)


class SelfCheck(unittest.TestCase):
    def test_self_check_passes(self):
        self.assertTrue(rqgate._self_check())


if __name__ == "__main__":
    unittest.main(verbosity=2)

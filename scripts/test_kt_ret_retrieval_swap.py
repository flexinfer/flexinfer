"""KT-RET must count only gte failures that coincide with a swap as kills."""

import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    "ktret", Path(__file__).with_name("kt-ret-retrieval-swap.py")
)
ktret = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ktret)


def row(t, stream, phase, outcome, latency_ms=20.0, status=200, err=None, headers=None):
    return {
        "t": t,
        "stream": stream,
        "phase": phase,
        "outcome": outcome,
        "latency_ms": latency_ms,
        "status": status,
        "err": err,
        "headers": headers or {},
    }


class Classify(unittest.TestCase):
    def test_outcomes(self):
        self.assertEqual(ktret.classify(200, None), "ok")
        self.assertEqual(ktret.classify(503, None), "5xx")
        self.assertEqual(ktret.classify(400, None), "4xx")
        self.assertEqual(ktret.classify(None, "ConnectionResetError"), "conn_error")
        self.assertEqual(ktret.classify(None, "timeout"), "timeout")

    def test_percentile_nearest_rank(self):
        self.assertIsNone(ktret.percentile([], 99))
        self.assertEqual(ktret.percentile([5.0], 99), 5.0)
        values = [float(v) for v in range(1, 101)]
        self.assertEqual(ktret.percentile(values, 50), 50.0)
        self.assertEqual(ktret.percentile(values, 99), 99.0)


class Summarize(unittest.TestCase):
    def test_failure_near_swap_is_a_kill_and_far_failure_is_not(self):
        events = [{"t": 1000.0, "kind": "model_added", "model": "humanizer"}]
        rows = [
            row(990.0, "interactive", "swap", "5xx", status=503),  # within 15 s
            row(1300.0, "interactive", "swap", "5xx", status=503),  # far from any swap
            row(
                1001.0,
                "interactive",
                "swap",
                "ok",
                latency_ms=25.0,
                headers={"x-flexinfer-replayed": "1"},
            ),
            row(500.0, "interactive", "baseline", "ok", latency_ms=10.0),
        ]
        summary = ktret.summarize(rows, events, window_s=15.0)
        swap = summary["swap"]["interactive"]
        self.assertEqual(swap["5xx"], 2)
        self.assertEqual(swap["failures_near_swap"], 1)
        self.assertEqual(swap["replayed"], 1)
        self.assertEqual(swap["p99_ms"], 25.0)
        result = ktret.verdict(summary, 2.0)
        self.assertTrue(result["kills_reproduced"])
        self.assertEqual(result["proxy_5xx_or_conn_errors_in_swap"], 2)
        self.assertFalse(result["p99"]["interactive"]["ok"])  # 25 > 2 x 10

    def test_long_request_spanning_a_swap_counts(self):
        events = [
            {"t": 1000.0, "kind": "model_changed", "model": "gte-qwen2-1p5b-radeonvii"}
        ]
        rows = [
            row(
                950.0,
                "batch",
                "swap",
                "conn_error",
                latency_ms=60_000.0,
                status=None,
                err="ConnectionResetError",
            )
        ]
        summary = ktret.summarize(rows, events, window_s=5.0)
        self.assertEqual(summary["swap"]["batch"]["failures_near_swap"], 1)

    def test_no_failures_means_no_kill(self):
        events = [{"t": 1000.0, "kind": "swap_forced", "model": "humanizer"}]
        rows = [row(1000.0 + i, "interactive", "swap", "ok") for i in range(10)]
        rows += [row(100.0 + i, "interactive", "baseline", "ok") for i in range(10)]
        result = ktret.verdict(ktret.summarize(rows, events, 15.0), 2.0)
        self.assertFalse(result["kills_reproduced"])
        self.assertTrue(result["p99"]["interactive"]["ok"])

    def test_runtime_snapshot_reads_the_models_envelope(self):
        snap = ktret.runtime_snapshot(
            {
                "models": [
                    {"name": "a", "state": "Ready"},
                    {"name": "b", "state": "Stopping", "draining": True},
                ]
            }
        )
        self.assertEqual(snap, {"a": ("Ready", False), "b": ("Stopping", True)})


if __name__ == "__main__":
    unittest.main()

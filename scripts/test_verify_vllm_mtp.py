"""The MTP probe must compute acceptance from counter deltas and fail closed."""

import argparse
import contextlib
import io
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "mtp", Path(__file__).with_name("verify-vllm-mtp.py")
)
mtp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mtp)

METRICS = """\
# HELP vllm:spec_decode_num_drafts_total Number of spec decoding drafts.
# TYPE vllm:spec_decode_num_drafts_total counter
vllm:spec_decode_num_drafts_total{engine="0",model_name="m"} 100.0
vllm:spec_decode_num_draft_tokens_total{engine="0",model_name="m"} 200.0
vllm:spec_decode_num_accepted_tokens_total{engine="0",model_name="m"} 130.0
vllm:spec_decode_num_accepted_tokens_per_pos_total{engine="0",model_name="m",position="0"} 80.0
vllm:spec_decode_num_accepted_tokens_per_pos_total{engine="0",model_name="m",position="1"} 50.0
vllm:num_requests_running{engine="0",model_name="m"} 0.0
"""


def scaled(factor):
    out = []
    for line in METRICS.splitlines():
        if line.startswith("vllm:spec_decode"):
            name, value = line.rsplit(" ", 1)
            line = f"{name} {float(value) * factor}"
        out.append(line)
    return "\n".join(out) + "\n"


class MetricTests(unittest.TestCase):
    def test_counters_are_summed_and_keyed_by_position(self):
        parsed = mtp.parse_spec_metrics(METRICS)
        self.assertEqual(parsed["drafts"], 100.0)
        self.assertEqual(parsed["draft_tokens"], 200.0)
        self.assertEqual(parsed["accepted"], 130.0)
        self.assertEqual(parsed["per_pos"], {0: 80.0, 1: 50.0})

    def test_missing_counters_mean_no_speculation(self):
        self.assertIsNone(mtp.parse_spec_metrics("vllm:num_requests_running 0.0\n"))

    def test_delta_gives_pos0_rate_and_acceptance_length(self):
        before = mtp.parse_spec_metrics(METRICS)
        after = mtp.parse_spec_metrics(scaled(3))
        delta = mtp.spec_delta(before, after)
        self.assertEqual(delta["drafts"], 200.0)
        self.assertAlmostEqual(delta["pos0_acceptance"], 0.8)
        self.assertAlmostEqual(delta["per_pos_acceptance"]["1"], 0.5)
        self.assertAlmostEqual(delta["acceptance_rate"], 0.65)
        self.assertAlmostEqual(delta["mean_acceptance_length"], 2.3)

    def test_no_drafts_during_workload_is_an_error(self):
        parsed = mtp.parse_spec_metrics(METRICS)
        with self.assertRaises(ValueError):
            mtp.spec_delta(parsed, parsed)

    def test_counter_reset_is_an_error(self):
        with self.assertRaises(ValueError):
            mtp.spec_delta(
                mtp.parse_spec_metrics(scaled(3)), mtp.parse_spec_metrics(METRICS)
            )


def results(coherent=True, ttft=1.0, short=65.0, two_k=62.0):
    probe = {"coherent": coherent, "ttft_s": ttft, "decode_tps": short}
    return {
        "arithmetic": dict(probe),
        "extraction": dict(probe),
        "recall_2k": dict(probe),
        "decode_short_median_tps": short,
        "decode_2k": {"decode_tps": two_k},
    }


def args(phase="mtp"):
    return argparse.Namespace(
        phase=phase, min_pos0=0.70, min_acceptance_length=1.6, min_decode_tps=60.0
    )


class VerdictTests(unittest.TestCase):
    spec = {"pos0_acceptance": 0.75, "mean_acceptance_length": 2.2}

    def test_passes_at_the_s2_bar(self):
        self.assertEqual(mtp.verdict(args(), results(), self.spec), [])

    def test_low_pos0_acceptance_fails(self):
        failures = mtp.verdict(
            args(), results(), {**self.spec, "pos0_acceptance": 0.33}
        )
        self.assertTrue(any("pos-0" in f for f in failures))

    def test_missing_spec_metrics_fail_the_mtp_phase_only(self):
        self.assertTrue(mtp.verdict(args(), results(), None))
        self.assertEqual(mtp.verdict(args("baseline"), results(), None), [])

    def test_slow_decode_or_incoherence_fails(self):
        self.assertTrue(mtp.verdict(args(), results(short=41.5), self.spec))
        self.assertTrue(mtp.verdict(args(), results(coherent=False), self.spec))


class ContextSweepTests(unittest.TestCase):
    def test_parse_contexts_sorts_and_dedupes(self):
        self.assertEqual(mtp.parse_contexts("8192, 2048,8192"), [2048, 8192])
        self.assertEqual(mtp.parse_contexts(""), [])

    def test_parse_contexts_rejects_bad_entries(self):
        for raw in ("2k", "100", "300000"):
            with self.assertRaises(ValueError):
                mtp.parse_contexts(raw)

    def _sweep(self, metrics_texts):
        calls = []
        texts = iter(metrics_texts)
        original_chat, original_prompt = mtp.chat, mtp.sized_prompt
        mtp.sized_prompt = lambda base, model, target, nonce=None: f"p{target}"
        mtp.chat = lambda base, model, prompt, max_tokens: calls.append(prompt) or {
            "answer": "Rain forms when...", "prompt_tokens": 2048, "completion_tokens": 256,
            "ttft_s": 2.0, "decode_tps": 50.0}
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                rows = mtp.context_sweep(
                    "http://x", "m", [2048],
                    metrics=lambda base: mtp.parse_spec_metrics(next(texts)))
        finally:
            mtp.chat, mtp.sized_prompt = original_chat, original_prompt
        self.assertEqual(calls, ["p2048"])
        return rows

    def test_sweep_reports_per_point_acceptance_and_prefill(self):
        (row,) = self._sweep([METRICS, scaled(3)])
        self.assertEqual(row["prefill_tps"], 1024.0)
        self.assertAlmostEqual(row["spec"]["pos0_acceptance"], 0.8)
        self.assertNotIn("spec_error", row)

    def test_sweep_records_a_point_without_drafts(self):
        (row,) = self._sweep([METRICS, METRICS])
        self.assertNotIn("spec", row)
        self.assertIn("no drafts", row["spec_error"])


class MeasurePhaseTests(unittest.TestCase):
    def test_measure_gates_coherence_only(self):
        slow = results(short=41.0, two_k=40.0, ttft=5.0)
        self.assertEqual(mtp.verdict(args("measure"), slow, None), [])
        self.assertTrue(mtp.verdict(args("measure"), results(coherent=False), None))


class CompareTests(unittest.TestCase):
    def summary(self, phase, code="aaaa", answer="def f(): pass"):
        return {
            "phase": phase,
            "parity": {"parity_code": code},
            "parity_text": {"parity_code": answer},
            "probes": {
                "arithmetic": "42",
                "extraction": "{}",
                "recall_2k": "MAPLE-4827",
            },
        }

    def write(self, directory, name, payload):
        path = os.path.join(directory, name)
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(payload, handle)
        return path

    def test_identical_parity_passes_and_divergence_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            a = self.write(directory, "a.json", self.summary("mtp"))
            b = self.write(directory, "b.json", self.summary("baseline"))
            self.assertEqual(mtp.main(["--compare", a, b]), 0)
            c = self.write(
                directory,
                "c.json",
                self.summary("baseline", "bbbb", "def f(): return 1"),
            )
            self.assertEqual(mtp.main(["--compare", a, c]), 1)


if __name__ == "__main__":
    unittest.main()

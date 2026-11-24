"""The seat-mix probe must read the real queue gauge and split out the prefill window."""

import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    "seatmix", Path(__file__).with_name("probe-lane-seat-mix.py")
)
seatmix = importlib.util.module_from_spec(spec)
spec.loader.exec_module(seatmix)


def short(start, ttft, total, out=64, chunks=None):
    decode = (out - 1) / (total - ttft) if ttft is not None and total > ttft else None
    return {
        "start": start,
        "ttft": ttft,
        "total": total,
        "completion_tokens": out,
        "decode_tps": decode,
        "chunks": chunks or [],
    }


class Gauges(unittest.TestCase):
    def test_by_reason_series_do_not_overwrite_the_queue_depth(self):
        # vLLM 0.33 prints the by_reason series after the plain gauge; a prefix
        # match took the last one (deferred = 0) as the queue depth.
        text = """\
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{engine="0",model_name="m"} 2.0
vllm:num_requests_waiting{engine="0",model_name="m"} 3.0
vllm:num_requests_waiting_by_reason{engine="0",model_name="m",reason="capacity"} 3.0
vllm:num_requests_waiting_by_reason{engine="0",model_name="m",reason="deferred"} 0.0
"""
        self.assertEqual(seatmix.parse_gauges(text), (2.0, 3.0))

    def test_missing_gauges_read_as_none(self):
        self.assertEqual(
            seatmix.parse_gauges("vllm:prompt_tokens_total 5.0\n"), (None, None)
        )


class DuringPrefill(unittest.TestCase):
    # The long request starts at t=100 and gets its first token at t=160.
    LONG = {"start": 100.0, "ttft": 60.0, "total": 63.0, "chunks": [160.0, 161.0]}

    def test_only_the_window_before_the_long_first_token_counts(self):
        shorts = [
            # Finished inside the window: 63 tokens over 21 s, one chunk every 3 s.
            short(101.0, 4.0, 25.0, chunks=[105.0, 108.0, 111.0, 114.0]),
            # First token inside, finished after: its decode rate is excluded,
            # and so is the chunk gap that straddles the edge.
            short(130.0, 20.0, 40.0, chunks=[150.0, 156.0, 165.0]),
            # Started inside, first token after the long prompt finished.
            short(150.0, 15.0, 17.0, chunks=[165.0, 165.5]),
            # Started after the long request's first token.
            short(161.0, 1.0, 2.0, chunks=[162.0, 162.5]),
        ]
        got = seatmix.during_prefill(self.LONG, shorts)
        self.assertEqual(
            (got["started"], got["first_token"], got["finished"]), (3, 2, 1)
        )
        self.assertEqual(got["decode_median"], 3.0)
        self.assertEqual(got["total_p50"], 25.0)
        # Gaps inside the window: 3, 3, 3 from the first short and 6 from the second.
        self.assertEqual(got["step_gaps"], 4)
        self.assertEqual(got["step_gap_p50"], 3.0)
        self.assertEqual(got["step_gap_p95"], 6.0)

    def test_a_failed_long_request_has_no_window(self):
        self.assertIsNone(
            seatmix.during_prefill({"error": "boom"}, [short(1.0, 1.0, 2.0)])
        )


class Summary(unittest.TestCase):
    def test_summary_keeps_the_old_keys_and_hides_raw_timestamps(self):
        shorts = [short(101.0, 4.0, 25.0), short(130.0, 20.0, 40.0)]
        got = seatmix.summarize(
            "run1",
            "m",
            dict(DuringPrefill.LONG),
            shorts,
            ["err"],
            [(2.0, 1.0), (2.0, 0.0)],
        )
        self.assertNotIn("start", got["long"])
        self.assertNotIn("chunks", got["long"])
        self.assertEqual(got["long"]["ttft"], 60.0)
        self.assertEqual(got["short"]["n"], 2)
        self.assertEqual(got["short"]["errors"], 1)
        self.assertEqual(got["short"]["ttft_p50"], 4.0)
        self.assertEqual(got["short"]["ttft_p95"], 20.0)
        self.assertEqual(got["short"]["total_p95"], 40.0)
        self.assertEqual(got["engine"]["running_max"], 2.0)
        self.assertEqual(got["engine"]["waiting_max"], 1.0)
        self.assertEqual(got["engine"]["waiting_nonzero_frac"], 0.5)
        self.assertEqual(got["during_prefill"]["started"], 2)


if __name__ == "__main__":
    unittest.main()

"""The step-trace probe must split a short request's wait from its own prefill."""

import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    "steptrace", Path(__file__).with_name("probe-prefill-step-trace.py")
)
steptrace = importlib.util.module_from_spec(spec)
spec.loader.exec_module(steptrace)

BLOCK = 1 / 244


def samples_from(events, start, end, period=0.05, done=((-9.0, 10.0),)):
    """Gauge samples every ``period`` s; ``events`` are (time, blocks, running)."""
    out = []
    t = start
    while t <= end + 1e-9:
        blocks, running = 0, 0
        for when, total, run in events:
            if when <= t + 1e-9:
                blocks, running = total, run
        finished = [count for when, count in done if when <= t + 1e-9]
        out.append(
            {
                "t": round(t, 4),
                "kv": blocks * BLOCK,
                "run": float(running),
                "wait": 0.0,
                "done": finished[-1] if finished else 0.0,
            }
        )
        t += period
    return out


# The shape measured on 2026-11-25: the long prompt alone takes a 9-block step
# of 3.6 s; a short sent at 19.2 is first carried by the step that starts when
# that one ends (23.3), then prefills over five steps 0.85 s apart.
EVENTS = [
    (3.3, 31, 1),
    (19.2, 60, 1),
    (19.7, 69, 1),
    (23.3, 81, 2),
    (24.15, 86, 2),
    (25.0, 91, 2),
    (25.85, 93, 2),
    (26.7, 95, 2),
    (27.55, 96, 2),
    (28.1, 94, 2),
]


def result(shorts, events=EVENTS, done=((-9.0, 10.0),), long_first=40.0):
    return {
        "t_long_sent": 0.0,
        "long": {"start": 0.0, "chunks": [long_first], "prompt_tokens": 56220},
        "shorts": shorts,
        "samples": samples_from(events, -2.0, 42.0, done=done),
    }


class Metrics(unittest.TestCase):
    def test_gauges_are_read_by_exact_name(self):
        text = """\
vllm:num_requests_running{engine="0",model_name="m"} 2.0
vllm:num_requests_waiting{engine="0",model_name="m"} 1.0
vllm:num_requests_waiting_by_reason{engine="0",model_name="m",reason="capacity"} 1.0
vllm:num_requests_waiting_by_reason{engine="0",model_name="m",reason="deferred"} 0.0
vllm:kv_cache_usage_perc{engine="0",model_name="m"} 0.25
vllm:request_success_total{engine="0",finished_reason="stop",model_name="m"} 7.0
vllm:request_success_total{engine="0",finished_reason="length",model_name="m"} 3.0
"""
        self.assertEqual(
            steptrace.parse_metrics(text),
            {"run": 2.0, "wait": 1.0, "kv": 0.25, "done": 10.0},
        )


class Trace(unittest.TestCase):
    def test_block_quantum_is_the_smallest_gauge_step(self):
        samples = samples_from(EVENTS, -2.0, 30.0)
        self.assertAlmostEqual(steptrace.block_quantum(samples), BLOCK, places=6)
        self.assertIsNone(steptrace.block_quantum(samples[:5]))

    def test_events_carry_block_deltas_and_running(self):
        samples = samples_from(EVENTS, -2.0, 30.0)
        events = steptrace.step_events(samples, BLOCK)
        self.assertEqual(
            [(delta, run) for _, delta, run in events],
            [
                (31, 1),
                (29, 1),
                (9, 1),
                (12, 2),
                (5, 2),
                (5, 2),
                (2, 2),
                (2, 2),
                (1, 2),
                (-2, 2),
            ],
        )

    def test_wait_and_own_steps_are_split(self):
        short = {
            "start": 19.21,
            "chunks": [27.6, 28.1],
            "end": 30.0,
            "prompt_tokens": 2080,
        }
        summary = steptrace.summarize(result([short]))
        self.assertEqual(summary["shorts_in_prefill"], 1)
        row = summary["shorts"][0]
        self.assertAlmostEqual(row["wait"], 4.09, places=1)
        self.assertAlmostEqual(row["own"], 4.3, places=1)
        self.assertEqual(row["own_steps"], 5)
        self.assertAlmostEqual(row["ttft"], 8.39, places=2)
        self.assertAlmostEqual(summary["alone_step_max"], 15.9, places=1)
        self.assertEqual(summary["kv_blocks_total"], 244)
        self.assertEqual(summary["long_ttft"], 40.0)

    def test_shorts_outside_the_prefill_are_left_out(self):
        late = {"start": 41.0, "chunks": [41.5], "end": 42.0, "prompt_tokens": 2060}
        silent = {"start": 19.21, "chunks": [], "end": 30.0}
        summary = steptrace.summarize(result([late, silent]))
        self.assertEqual(summary["shorts_in_prefill"], 0)
        self.assertIsNone(summary["short_ttft_p50"])

    def test_another_callers_requests_are_counted_as_outside(self):
        short = {"start": 19.21, "chunks": [27.6], "end": 30.0, "prompt_tokens": 2080}
        # Three requests finished during the run; the probe made two.
        done = ((-9.0, 10.0), (30.0, 11.0), (35.0, 12.0), (41.0, 13.0))
        summary = steptrace.summarize(result([short], done=done))
        self.assertEqual(summary["outside"]["finished_requests"], 1)
        quiet = steptrace.summarize(
            result([short], done=((-9.0, 10.0), (30.0, 11.0), (41.0, 12.0)))
        )
        self.assertEqual(quiet["outside"]["finished_requests"], 0)

    def test_an_empty_trace_summarizes_without_raising(self):
        summary = steptrace.summarize({"long": {}, "shorts": [], "samples": []})
        self.assertIsNone(summary["long_ttft"])
        self.assertEqual(summary["shorts_in_prefill"], 0)


if __name__ == "__main__":
    unittest.main()

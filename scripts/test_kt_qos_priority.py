"""KT-QOS harness: percentile, gap, counter and verdict arithmetic."""

import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest

spec = importlib.util.spec_from_file_location(
    "ktqos", Path(__file__).with_name("kt-qos-priority.py")
)
ktqos = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ktqos)


def scrape(drafts, accepted_pos0, gen, preempt):
    return ktqos.parse_metrics(
        f"""\
# TYPE vllm:spec_decode_num_drafts counter
vllm:spec_decode_num_drafts_total{{engine="0",model_name="m"}} {drafts}
vllm:spec_decode_num_draft_tokens_total{{engine="0",model_name="m"}} {drafts * 2}
vllm:spec_decode_num_accepted_tokens_total{{engine="0",model_name="m"}} {accepted_pos0 * 1.5}
vllm:spec_decode_num_accepted_tokens_per_pos_total{{engine="0",model_name="m",position="0"}} {accepted_pos0}
vllm:spec_decode_num_accepted_tokens_per_pos_total{{engine="0",model_name="m",position="1"}} {accepted_pos0 / 2}
vllm:generation_tokens_total{{engine="0",model_name="m"}} {gen}
vllm:request_generation_tokens_sum{{engine="0",model_name="m"}} 99999
vllm:num_preemptions_total{{engine="0",model_name="m"}} {preempt}
"""
    )


def arm(name, ttft_p95, idle_p95, gap, decode, parity="abc", pos0=0.8):
    return {
        "arm": name,
        "interactive_ttft_p50_s": ttft_p95 / 2,
        "interactive_ttft_p95_s": ttft_p95,
        "idle_ttft_p95_s": idle_p95,
        "background_max_gap_s": [gap, gap / 2],
        "aggregate_decode_tps": decode,
        "parity": {"parity_code": parity},
        "engine": {"pos0_acceptance": pos0, "preemptions": 0},
    }


class KTQoSTest(unittest.TestCase):
    def test_percentile_nearest_rank(self):
        values = [float(v) for v in range(1, 21)]
        self.assertEqual(ktqos.percentile(values, 95), 19.0)
        self.assertEqual(ktqos.percentile(values, 50), 10.0)
        self.assertEqual(ktqos.percentile([3.0], 95), 3.0)
        self.assertIsNone(ktqos.percentile([], 95))

    def test_max_gap_counts_window_edges(self):
        self.assertEqual(ktqos.max_gap([12.0, 13.0, 50.0], 10.0, 60.0), 37.0)
        self.assertEqual(
            ktqos.max_gap([], 10.0, 70.0), 60.0, "a worker that never streamed"
        )
        self.assertEqual(
            ktqos.max_gap([5.0, 11.0, 65.0], 10.0, 60.0),
            49.0,
            "events outside the window are ignored",
        )

    def test_metrics_delta(self):
        before = scrape(drafts=100, accepted_pos0=80, gen=1000, preempt=1)
        after = scrape(drafts=1100, accepted_pos0=880, gen=61000, preempt=4)
        delta = ktqos.metrics_delta(before, after)
        self.assertEqual(delta["generation_tokens"], 60000)
        self.assertEqual(delta["preemptions"], 3)
        self.assertEqual(delta["pos0_acceptance"], 0.8)
        self.assertIsNone(ktqos.metrics_delta(None, after))

    def test_payload_carries_class_per_arm(self):
        def client(arm_name):
            return ktqos.Client(
                types.SimpleNamespace(endpoint="http://x/", model="m", arm=arm_name)
            )

        self.assertNotIn("priority", client("fcfs").payload("hi", 8, "background"))
        self.assertEqual(
            client("priority").payload("hi", 8, "background")["priority"], 10
        )
        self.assertEqual(
            client("priority").payload("hi", 8, "interactive")["priority"], 0
        )
        self.assertNotIn(
            "priority",
            client("proxy").payload("hi", 8, "background"),
            "through the proxy the class travels as a header",
        )
        self.assertTrue(
            client("fcfs").payload("hi", 8, "background", ignore_eos=True)["ignore_eos"]
        )

    def test_verdict(self):
        arms = [
            arm("fcfs", ttft_p95=30.0, idle_p95=0.5, gap=2.0, decode=100.0),
            arm("priority", ttft_p95=29.0, idle_p95=0.5, gap=2.0, decode=99.0),
            arm("proxy", ttft_p95=0.6, idle_p95=0.5, gap=3.0, decode=70.0),
        ]
        with tempfile.TemporaryDirectory() as tmp:
            paths = []
            for a in arms:
                path = Path(tmp) / f"{a['arm']}.json"
                path.write_text(json.dumps(a))
                paths.append(str(path))
            rows = {r["arm"]: r for r in ktqos.verdict(paths)}
            with self.assertRaises(SystemExit):
                ktqos.verdict(paths[1:])

        prio = rows["priority"]["checks"]
        self.assertFalse(
            prio["ttft_p95_vs_idle"]["pass"], "seat-bound TTFT fails the 1.3x bar"
        )
        self.assertTrue(prio["decode_vs_fcfs"]["pass"])
        self.assertFalse(rows["priority"]["pass"])

        proxy = rows["proxy"]["checks"]
        self.assertTrue(proxy["ttft_p95_vs_idle"]["pass"])
        self.assertFalse(
            proxy["decode_vs_fcfs"]["pass"], "the reserve costs aggregate decode"
        )
        self.assertTrue(proxy["greedy_parity"]["pass"])
        self.assertTrue(proxy["mtp_pos0"]["pass"])


if __name__ == "__main__":
    unittest.main()

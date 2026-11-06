"""KT-QOS harness: percentile, gap, counter and verdict arithmetic."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import time
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


def qos2_arm(
    label,
    ttft_p95,
    idle_p95,
    decode,
    probes,
    gap=20.0,
    pos0=0.786,
    parity="abc",
    overlapped=True,
):
    """A KT-QOS2 arm JSON; probes maps filler -> (idle median, loaded median)."""
    return {
        "arm": "proxy",
        "label": label,
        "interactive_ttft_s": [ttft_p95 / 2, ttft_p95],
        "interactive_ttft_p50_s": ttft_p95 / 2,
        "interactive_ttft_p95_s": ttft_p95,
        "idle_ttft_p95_s": idle_p95,
        "background_max_gap_s": [gap, gap / 2],
        "aggregate_decode_tps": decode,
        "parity": {"parity_code": parity},
        "engine": {"pos0_acceptance": pos0, "preemptions": 0},
        "long_probes": {
            "idle": [{"filler": f, "ttft_median_s": i} for f, (i, _) in probes.items()],
            "loaded": [
                {"filler": f, "ttft_median_s": l, "overlapped": [overlapped] * 3}
                for f, (_, l) in probes.items()
            ],
        },
    }


def write_arms(tmp, arms):
    paths = []
    for a in arms:
        path = Path(tmp) / f"{a['label']}.json"
        path.write_text(json.dumps(a))
        paths.append(str(path))
    return paths


class KTQoS2VerdictTest(unittest.TestCase):
    CONTROL_PROBES = {110: (2.0, 2.1), 1540: (25.0, 27.0)}

    def verdict(self, *arms):
        with tempfile.TemporaryDirectory() as tmp:
            return {r["arm"]: r for r in ktqos.verdict_qos2(write_arms(tmp, arms))}

    def control(self):
        return qos2_arm("control", 1.67, 0.575, 61.2, self.CONTROL_PROBES)

    def test_budget_arm_passes_every_bar(self):
        rows = self.verdict(
            self.control(),
            qos2_arm("budget", 0.72, 0.57, 60.0, {110: (2.1, 2.3), 1540: (28.0, 32.0)}),
        )
        self.assertFalse(
            rows["control"]["checks"]["ttft_p95_vs_idle"]["pass"],
            "the reserve-only control reproduces arm C's bar-1 miss",
        )
        budget = rows["budget"]
        self.assertTrue(budget["pass"], budget["checks"])
        self.assertEqual(
            budget["checks"]["long_prompt_ttft_vs_control"]["value"]["worst"], 1.185
        )

    def test_cost_cap_fails_a_tail_win(self):
        rows = self.verdict(
            self.control(),
            qos2_arm("budget", 0.70, 0.57, 60.0, {110: (2.1, 2.2), 1540: (27.0, 34.0)}),
        )
        checks = rows["budget"]["checks"]
        self.assertTrue(checks["ttft_p95_vs_idle"]["pass"])
        self.assertFalse(
            checks["long_prompt_ttft_vs_control"]["pass"],
            "a loaded 28K prompt at 1.26x the control fails the cost cap",
        )
        self.assertFalse(rows["budget"]["pass"])

    def test_slower_idle_cannot_loosen_bar_one(self):
        rows = self.verdict(
            self.control(),
            qos2_arm("budget", 0.85, 0.66, 60.0, self.CONTROL_PROBES),
        )
        checks = rows["budget"]["checks"]
        self.assertTrue(checks["ttft_p95_vs_idle"]["pass"], "0.85 / 0.66 = 1.29x")
        self.assertFalse(checks["idle_ttft_vs_control"]["pass"], "0.66 / 0.575 = 1.15x")

    def test_throughput_pos0_and_parity(self):
        rows = self.verdict(
            self.control(),
            qos2_arm("slow", 0.7, 0.57, 57.0, self.CONTROL_PROBES),
            qos2_arm("drift", 0.7, 0.57, 61.0, self.CONTROL_PROBES, pos0=0.76),
            qos2_arm("diverged", 0.7, 0.57, 61.0, self.CONTROL_PROBES, parity="xyz"),
            qos2_arm("stalled", 0.7, 0.57, 61.0, self.CONTROL_PROBES, gap=61.0),
        )
        self.assertFalse(rows["slow"]["checks"]["decode_vs_control"]["pass"], "0.93x")
        self.assertFalse(rows["drift"]["checks"]["mtp_pos0"]["pass"], "-0.026")
        self.assertFalse(rows["diverged"]["checks"]["greedy_parity"]["pass"])
        self.assertFalse(rows["stalled"]["checks"]["background_max_gap_s"]["pass"])

    def test_missing_or_unloaded_probe_fails_the_cap(self):
        missing = qos2_arm("missing", 0.7, 0.57, 61.0, {110: (2.0, 2.1)})
        unloaded = qos2_arm(
            "unloaded", 0.7, 0.57, 61.0, self.CONTROL_PROBES, overlapped=False
        )
        rows = self.verdict(self.control(), missing, unloaded)
        probes = rows["missing"]["checks"]["long_prompt_ttft_vs_control"]["value"][
            "probes"
        ]
        self.assertIsNone(probes["idle/1540"], "the control's 28K probe has no match")
        self.assertFalse(
            rows["missing"]["checks"]["long_prompt_ttft_vs_control"]["pass"]
        )
        probes = rows["unloaded"]["checks"]["long_prompt_ttft_vs_control"]["value"][
            "probes"
        ]
        self.assertIsNone(probes["loaded/110"], "no background decoded beside it")
        self.assertEqual(probes["idle/110"], 1.0)


def usage(ts_s, cls, ttft_s, arrival_s=None):
    """A LogQL line_format request_usage value: (ts ns, "class ms id")."""
    arrival = ts_s - ttft_s if arrival_s is None else arrival_s
    return [
        str(int(ts_s * 1e9)),
        f"{cls} {int(round(ttft_s * 1000))} {int(arrival * 1e9)}-ab12",
    ]


class CollisionsTest(unittest.TestCase):
    def test_parse_usage_line_formats(self):
        ts = str(int(100.0 * 1e9))
        row = ktqos.parse_usage_line(ts, "background 1750 90000000000-3f35")
        self.assertEqual(row[0], "background")
        self.assertAlmostEqual(row[1], 90.0)
        self.assertAlmostEqual(row[2], 98.25, "forward = first byte - duration")
        self.assertAlmostEqual(row[3], 100.0)
        raw = json.dumps(
            {
                "qos_class": "interactive",
                "duration_ms": 618,
                "request_id": "99000000000-c021",
                "event": "request_usage",
            }
        )
        self.assertEqual(ktqos.parse_usage_line(ts, raw)[0], "interactive")
        self.assertIsNone(ktqos.parse_usage_line(ts, "no class here"))
        self.assertIsNone(ktqos.parse_usage_line(ts, '{"event": "request_start"}'))

    def test_collisions_classifies_both_directions(self):
        values = [
            # background forwarded at 10.0, first byte 11.75
            usage(11.75, "background", 1.75, arrival_s=0.0),
            # interactive forwarded 0.3 s into it: slow
            usage(10.3 + 1.95, "interactive", 1.95),
            # interactive alone at 20.0: fast
            usage(20.55, "interactive", 0.55),
            # background forwarded at 20.2, inside that interactive's prefill
            usage(21.95, "background", 1.75, arrival_s=5.0),
        ]
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "loki.json"
            path.write_text(
                json.dumps({"ok": True, "result": {"result": [{"values": values}]}})
            )
            report = ktqos.collisions(ktqos.load_usage_rows([str(path)]))
        self.assertEqual(report["background_n"], 2)
        self.assertEqual(report["interactive_n"], 2)
        self.assertEqual(report["background_forwarded_during_interactive_prefill"], 1)
        self.assertEqual(report["held_examples"][0]["lag_s"], 0.2)
        self.assertEqual(report["interactive_forwarded_during_background_prefill"], 1)
        self.assertEqual(report["overlaps"][0]["lag_s"], 0.3)
        self.assertEqual(report["slow_interactive_n"], 1)
        self.assertEqual(report["slow_interactive_overlapping_background_prefill"], 1)

    def test_kt_qos_arm_d_evidence(self):
        """The saved arm D export: the reserve already forbids what a gate would hold."""
        path = (
            Path(__file__).parent.parent
            / ".loom/kt-qos-v033-2026-11-05/proxy-usage-D.json"
        )
        if not path.exists():
            self.skipTest("evidence export not present")
        report = ktqos.collisions(ktqos.load_usage_rows([str(path)]))
        self.assertEqual(report["background_forwarded_during_interactive_prefill"], 0)
        self.assertEqual(
            report["slow_interactive_n"],
            report["slow_interactive_overlapping_background_prefill"],
        )


class FakeClient:
    """Stands in for Client: interactive returns at once, background streams
    tokens until on_token raises StreamAborted."""

    def __init__(self):
        self.calls = []
        self.aborted = 0
        self.ttft = iter([9.0, 1.0, 2.0, 3.0, 9.0, 4.0, 5.0, 6.0])

    def stream(self, prompt, max_tokens, qos, on_token=None, **kw):
        self.calls.append(qos)
        if qos == "background":
            for _ in range(10000):
                try:
                    on_token(time.monotonic())
                except ktqos.StreamAborted:
                    self.aborted += 1
                    raise
                time.sleep(0.001)
            return {"ttft_s": 0.1, "prompt_tokens": 1900}
        return {"ttft_s": next(self.ttft), "prompt_tokens": 28000}


class LongProbeTest(unittest.TestCase):
    def test_parse_fillers(self):
        self.assertEqual(ktqos.parse_fillers("110, 440,1540"), [110, 440, 1540])
        self.assertEqual(ktqos.parse_fillers(""), [])

    def test_probe_size_discards_warm_rep_and_aborts_background(self):
        args = types.SimpleNamespace(
            long_probe_reps=3,
            long_probe_tokens=16,
            background_filler=100,
            long_probe_background_tokens=4096,
        )
        client = FakeClient()
        idle = ktqos.probe_size(client, args, 1540, loaded=False)
        self.assertEqual(
            idle["ttft_s"], [1.0, 2.0, 3.0], "rep 0 (9.0 s) is the warm rep"
        )
        self.assertEqual(idle["ttft_median_s"], 2.0)
        self.assertEqual(idle["prefill_tps"], 14000.0)
        self.assertNotIn("overlapped", idle)

        loaded = ktqos.probe_size(client, args, 1540, loaded=True)
        self.assertEqual(loaded["ttft_s"], [4.0, 5.0, 6.0])
        self.assertEqual(loaded["overlapped"], [True, True, True])
        self.assertEqual(
            client.aborted, 4, "every loaded rep closes its background request"
        )
        self.assertEqual(client.calls[4:], ["background", "interactive"] * 4)


class FakeLane(BaseHTTPRequestHandler):
    """An OpenAI-style SSE chat endpoint plus vLLM counters."""

    tokens_served = 0
    lock = threading.Lock()

    def log_message(self, *a):
        pass

    def do_GET(self):
        body = (
            f'vllm:generation_tokens_total{{engine="0"}} {FakeLane.tokens_served}\n'
            'vllm:spec_decode_num_drafts_total{engine="0"} 0\n'
        ).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        prompt = req["messages"][0]["content"]
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        try:
            for _ in range(req["max_tokens"]):
                chunk = {"choices": [{"delta": {"content": "x"}}]}
                self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode())
                self.wfile.flush()
                with FakeLane.lock:
                    FakeLane.tokens_served += 1
                time.sleep(0.0005)
            final = {
                "choices": [{"delta": {}, "finish_reason": "length"}],
                "usage": {
                    "prompt_tokens": len(prompt.split()),
                    "completion_tokens": req["max_tokens"],
                },
            }
            self.wfile.write(f"data: {json.dumps(final)}\n\ndata: [DONE]\n\n".encode())
        except (BrokenPipeError, ConnectionResetError):
            pass


class RunTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeLane)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.endpoint = f"http://127.0.0.1:{cls.server.server_address[1]}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def args(self, **kw):
        base = dict(
            endpoint=self.endpoint,
            model="m",
            arm="proxy",
            metrics=self.endpoint + "/metrics",
            duration=0.3,
            interval=0.1,
            warmup=0.05,
            baseline_reps=2,
            interactive_tokens=4,
            interactive_filler=2,
            background_tokens=20,
            background_filler=1,
            idle_warmup=0,
            warm_sweep=0,
            long_probe_fillers="",
            long_probe_reps=1,
            long_probe_tokens=2,
            long_probe_background_tokens=5000,
        )
        base.update(kw)
        return types.SimpleNamespace(**base)

    def test_defaults_keep_the_kt_qos_shape(self):
        result = ktqos.run(self.args())
        self.assertEqual(len(result["idle_ttft_s"]), 2)
        self.assertEqual(result["warm_turns_n"], 0)
        self.assertNotIn("long_probes", result)
        self.assertGreater(result["interactive_n"], 0)
        self.assertEqual(len(result["interactive_turns"]), result["interactive_n"])
        self.assertGreater(result["aggregate_decode_tps"], 0)
        self.assertIsNotNone(result["interactive_prompt_tokens"])

    def test_qos2_options(self):
        result = ktqos.run(
            self.args(idle_warmup=1, warm_sweep=0.2, long_probe_fillers="3,30")
        )
        self.assertEqual(
            len(result["idle_ttft_s"]), 2, "warm reps are not in the baseline"
        )
        self.assertGreater(result["warm_turns_n"], 0)
        self.assertGreaterEqual(result["window_s"], 0.3)
        self.assertLess(result["window_s"], 2.0, "the warm sweep is outside the window")
        probes = result["long_probes"]
        self.assertEqual([p["filler"] for p in probes["idle"]], [3, 30])
        self.assertEqual([p["filler"] for p in probes["loaded"]], [3, 30])
        self.assertEqual(probes["loaded"][0]["overlapped"], [True])
        self.assertGreater(
            probes["idle"][1]["prompt_tokens"], probes["idle"][0]["prompt_tokens"]
        )


if __name__ == "__main__":
    unittest.main()

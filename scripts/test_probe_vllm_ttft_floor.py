"""The TTFT-floor probe must split one request's time and refuse shared ones."""

import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "ttft", Path(__file__).with_name("probe-vllm-ttft-floor.py")
)
ttft = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ttft)


def scrape(finished, prompt, ttft_sum, ttft_count, running=0.0, hits=0.0):
    return ttft.parse_metrics(
        f"""\
# TYPE vllm:request_success_total counter
vllm:request_success_total{{finished_reason="length",model_name="m"}} {finished}
vllm:request_success_total{{finished_reason="stop",model_name="m"}} 0.0
vllm:prompt_tokens_total{{model_name="m"}} {prompt}
vllm:time_to_first_token_seconds_bucket{{le="0.5",model_name="m"}} 99.0
vllm:time_to_first_token_seconds_sum{{model_name="m"}} {ttft_sum}
vllm:time_to_first_token_seconds_count{{model_name="m"}} {ttft_count}
vllm:time_to_first_token_seconds_created{{model_name="m"}} 1.7e9
vllm:request_queue_time_seconds_sum{{model_name="m"}} {ttft_sum / 10}
vllm:request_prefill_time_seconds_sum{{model_name="m"}} {ttft_sum / 2}
vllm:iteration_tokens_total_sum{{model_name="m"}} {prompt + finished}
vllm:iteration_tokens_total_count{{model_name="m"}} {finished * 2}
vllm:prefix_cache_hits_total{{model_name="m"}} {hits}
vllm:num_requests_running{{model_name="m"}} {running}
vllm:num_requests_waiting{{model_name="m"}} 0.0
"""
    )


class Metrics(unittest.TestCase):
    def test_sums_label_sets_and_skips_buckets(self):
        metrics = scrape(5, 1000, 2.0, 5)
        self.assertEqual(metrics["vllm:request_success_total"], 5.0)
        self.assertEqual(metrics["vllm:time_to_first_token_seconds_count"], 5.0)
        self.assertNotIn("vllm:time_to_first_token_seconds_bucket", metrics)
        self.assertNotIn("vllm:time_to_first_token_seconds_created", metrics)

    def test_info_labels(self):
        text = 'vllm:cache_config_info{block_size="400",cache_dtype="auto"} 1.0\n'
        labels = ttft.info_labels(text, "vllm:cache_config_info")
        self.assertEqual(labels["block_size"], "400")


class RequestSplit(unittest.TestCase):
    def test_one_clean_request(self):
        split = ttft.request_split(
            scrape(5, 1000, 2.0, 5), scrape(6, 1256, 3.7, 6), 256
        )
        self.assertFalse(split["shared"])
        self.assertAlmostEqual(split["server_ttft_s"], 1.7, places=3)
        self.assertAlmostEqual(split["server_prefill_s"], 0.85, places=3)
        self.assertEqual(split["output_batches"], 2)
        self.assertEqual(split["prefix_hit_tokens"], 0)

    def test_another_request_finished_inside(self):
        split = ttft.request_split(
            scrape(5, 1000, 2.0, 5), scrape(7, 1556, 4.0, 7), 256
        )
        self.assertTrue(split["shared"])

    def test_busy_engine_before_the_request(self):
        split = ttft.request_split(
            scrape(5, 1000, 2.0, 5, running=1), scrape(6, 1256, 3.7, 6), 256
        )
        self.assertTrue(split["shared"])

    def test_medians_drop_shared_rows(self):
        rows = [
            {"shared": False, "client_ttft_s": 1.0, "server_ttft_s": 0.9},
            {"shared": False, "client_ttft_s": 3.0, "server_ttft_s": 2.9},
            {"shared": True, "client_ttft_s": 99.0, "server_ttft_s": 99.0},
        ]
        medians = ttft.medians(rows)
        self.assertEqual(medians["n"], 2)
        self.assertEqual(medians["shared"], 1)
        self.assertEqual(medians["client_ttft_s"], 2.0)


class Stream(unittest.TestCase):
    def test_first_choice_and_usage(self):
        lines = [
            b'data: {"choices":[{"text":"x"}]}\n',
            b'data: {"choices":[],"usage":{"prompt_tokens":256}}\n',
            b"data: [DONE]\n",
        ]
        ticks = iter([1.5, 1.6])
        out = ttft.read_first_chunk(lines, 1.0, clock=lambda: next(ticks))
        self.assertEqual(out["client_ttft_s"], 0.5)
        self.assertEqual(out["prompt_tokens"], 256)

    def test_incomplete_stream_fails(self):
        with self.assertRaises(ValueError):
            ttft.read_first_chunk([b'data: {"choices":[{"text":"x"}]}\n'], 0.0)


class Trace(unittest.TestCase):
    def test_busy_idle_and_gap_attribution(self):
        events = [
            {"ph": "X", "cat": "kernel", "name": "gemm", "ts": 0, "dur": 10_000},
            {"ph": "X", "cat": "kernel", "name": "gemm", "ts": 5_000, "dur": 10_000},
            {"ph": "X", "cat": "kernel", "name": "attn", "ts": 115_000, "dur": 5_000},
            {
                "ph": "X",
                "cat": "cpu_op",
                "name": "aten::item",
                "ts": 20_000,
                "dur": 90_000,
            },
            {
                "ph": "X",
                "cat": "user_annotation",
                "name": "execute_model",
                "ts": 0,
                "dur": 120_000,
            },
            {"ph": "i", "cat": "kernel", "name": "marker", "ts": 1},
        ]
        summary = ttft.summarize_trace(events, gap_ms=20)
        self.assertEqual(summary["kernel_count"], 3)
        self.assertEqual(summary["gpu_span_ms"], 120.0)
        self.assertEqual(summary["gpu_busy_ms"], 20.0)
        self.assertEqual(summary["gpu_idle_ms"], 100.0)
        self.assertEqual(summary["top_kernels"][0][:2], ["gemm", 20.0])
        gap = summary["idle_gaps"][0]
        self.assertEqual(gap["dur_ms"], 100.0)
        self.assertEqual(gap["host"][0], ["execute_model", 100.0])
        self.assertIn(["aten::item", 90.0], gap["host"])
        self.assertEqual(summary["annotations"][0], ["execute_model", 120.0, 1])

    def test_no_kernels(self):
        self.assertEqual(ttft.summarize_trace([])["kernel_count"], 0)


class PySpy(unittest.TestCase):
    def test_leaf_and_inclusive_frames(self):
        text = (
            "main (a.py:1);step (b.py:2);sync (c.py:3) 30\n"
            "main (a.py:1);step (b.py:2) 10\n"
            "garbage line\n"
        )
        summary = ttft.summarize_collapsed(text)
        self.assertEqual(summary["samples"], 40)
        self.assertEqual(summary["leaf"][0], ["sync (c.py:3)", 30, 0.75])
        self.assertEqual(summary["inclusive"][0][1], 40)

    def test_find_pids(self):
        with tempfile.TemporaryDirectory() as proc:
            for pid, cmd in (
                ("1", b"/usr/bin/python\0/usr/local/bin/vllm serve\0--port\0"),
                ("144", b"VLLM::EngineCore\0"),
                ("7", b"bash\0"),
                ("self", b"x\0"),
            ):
                os.mkdir(os.path.join(proc, pid))
                with open(os.path.join(proc, pid, "cmdline"), "wb") as handle:
                    handle.write(cmd)
            self.assertEqual(
                ttft.find_pids(proc), {"api_server": 1, "engine_core": 144}
            )

    def test_sizes_floor(self):
        self.assertEqual(ttft.parse_sizes("16, 256"), [16, 256])
        with self.assertRaises(ValueError):
            ttft.parse_sizes("8")


if __name__ == "__main__":
    unittest.main()

"""The canary must fail closed on truncated streams or missing usage."""

import importlib.util
import contextlib
import io
import json
import sys
from pathlib import Path
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "canary", Path(__file__).with_name("verify-vllm-canary.py"))
canary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(canary)


def event(delta=None, finish=None, usage=None):
    payload = {"choices": [{"delta": delta or {}, "finish_reason": finish}]}
    if usage:
        payload["usage"] = usage
    return ("data: " + json.dumps(payload) + "\n\n").encode()


class StreamTests(unittest.TestCase):
    def stream(self):
        return [
            event({"role": "assistant"}),
            event({"content": "Forty"}),
            event({"content": " two"}, finish="stop"),
            event(usage={"prompt_tokens": 2300, "completion_tokens": 3}),
            b"data: [DONE]\n\n",
        ]

    def parse(self, stream):
        times = iter([11.0, 11.5])
        return canary.read_stream(stream, 10.0, clock=lambda: next(times))

    def test_timing_ignores_role_and_usage_chunks(self):
        result = self.parse(self.stream())
        self.assertEqual(result["answer"], "Forty two")
        self.assertEqual(result["ttft_s"], 1)
        self.assertEqual(result["decode_tps"], 4)

    def test_truncated_stream_is_not_a_pass(self):
        with self.assertRaisesRegex(ValueError, "incomplete"):
            self.parse(self.stream()[:-1])

    def test_missing_usage_is_not_zero_throughput(self):
        stream = self.stream()
        del stream[-2]
        with self.assertRaisesRegex(ValueError, "missing completion-token"):
            self.parse(stream)

    def test_server_error_is_not_ignored(self):
        with self.assertRaisesRegex(ValueError, "stream error"):
            self.parse([b'data: {"error": {"message": "engine dead"}}\n'])

    def test_aborted_generation_cannot_pass(self):
        stream = self.stream()
        stream[2] = event({"content": " two"}, finish="abort")
        with self.assertRaisesRegex(ValueError, "unexpected finish reason"):
            self.parse(stream)

    def test_reasoning_fails_default_contract(self):
        stream = self.stream()
        stream[1] = event({"reasoning_content": "Let me think"})
        with self.assertRaisesRegex(ValueError, "non-thinking default"):
            self.parse(stream)

    def test_single_chunk_has_no_decode_measurement(self):
        stream = self.stream()
        del stream[1]
        self.assertIsNone(self.parse(stream)["decode_tps"])


class ProbeGateTests(unittest.TestCase):
    def run_probes(self, *, recall_ttft=2, decode_tokens=256):
        replies = [
            {"answer": "42", "prompt_tokens": 28, "completion_tokens": 3,
             "ttft_s": 0.2, "decode_tps": 40},
            {"answer": '{"name":"Birch","count":7}', "prompt_tokens": 32,
             "completion_tokens": 14, "ttft_s": 0.2, "decode_tps": 40},
            {"answer": "MAPLE-4827", "prompt_tokens": 2337,
             "completion_tokens": 8, "ttft_s": recall_ttft, "decode_tps": 40},
        ] + [{"answer": "Novel generation", "prompt_tokens": 32,
              "completion_tokens": decode_tokens, "ttft_s": 0.2, "decode_tps": 40}
             for _ in range(3)]
        output = io.StringIO()
        with mock.patch.object(sys, "argv", ["probe", "--model", "canary"]), \
                mock.patch.object(canary, "recall_prompt", return_value="recall"), \
                mock.patch.object(canary, "chat", side_effect=replies), \
                contextlib.redirect_stdout(output):
            result = canary.main()
        return result, json.loads(output.getvalue().splitlines()[-1])

    def test_full_http_gate_pass(self):
        code, report = self.run_probes()
        self.assertEqual(code, 0)
        self.assertEqual(report["http_probe_verdict"], "pass")
        self.assertEqual(report["median_decode_tps"], 40)

    def test_slow_recall_rejects_fast_decode(self):
        code, report = self.run_probes(recall_ttft=3)
        self.assertEqual(code, 1)
        self.assertIn("recall TTFT >= 3 seconds", report["failures"])

    def test_short_output_rejects_inflated_decode(self):
        code, report = self.run_probes(decode_tokens=5)
        self.assertEqual(code, 1)
        self.assertEqual(len(report["failures"]), 3)


if __name__ == "__main__":
    unittest.main()

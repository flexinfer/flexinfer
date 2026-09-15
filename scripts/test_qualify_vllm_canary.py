"""Extended qualification must reject false context, tool, and drain passes."""

import contextlib
import copy
import importlib.util
import io
import json
from pathlib import Path
import sys
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "qualify", Path(__file__).with_name("qualify-vllm-canary.py"))
qualify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qualify)


class ContextTests(unittest.TestCase):
    def setUp(self):
        self.expected = {"ALPHA": "pine", "BRAVO": "birch", "CHARLIE": "oak"}
        self.result = {"answer": json.dumps(self.expected), "prompt_tokens": 65344,
                       "finish_reason": "stop"}

    def test_retrieval_from_all_depths(self):
        prompt = qualify.build_context(20, self.expected)[0]["content"]
        self.assertLess(prompt.index("ALPHA vault"), prompt.index("Record 0:"))
        self.assertLess(prompt.index("Record 9:"), prompt.index("BRAVO vault"))
        self.assertLess(prompt.index("BRAVO vault"), prompt.index("Record 10:"))
        self.assertLess(prompt.index("Record 19:"), prompt.index("CHARLIE vault"))
        qualify.check_context(self.result, self.expected, 65344, 65536)

    def test_small_prompt_cannot_certify_64k(self):
        self.result["prompt_tokens"] = 2300
        with self.assertRaisesRegex(ValueError, "outside context"):
            qualify.check_context(self.result, self.expected, 2300, 65536)

    def test_usage_must_confirm_calibrated_length(self):
        self.result["prompt_tokens"] -= 10
        with self.assertRaisesRegex(ValueError, "usage mismatch"):
            qualify.check_context(self.result, self.expected, 65344, 65536)

    def test_wrong_middle_key_fails(self):
        self.result["answer"] = json.dumps({**self.expected, "BRAVO": "wrong"})
        with self.assertRaisesRegex(ValueError, "key mismatch"):
            qualify.check_context(self.result, self.expected, 65344, 65536)

    def test_truncated_answer_fails_even_if_json_matches(self):
        self.result["finish_reason"] = "length"
        with self.assertRaisesRegex(ValueError, "truncated"):
            qualify.check_context(self.result, self.expected, 65344, 65536)

    def test_calibration_reserves_output(self):
        client = mock.Mock()
        client.tokenize.side_effect = [69000, 65344]
        _, count = qualify.calibrated_context(client, 65536, self.expected)
        self.assertEqual(count, 65344)
        self.assertEqual(client.tokenize.call_count, 2)

    def test_long_context_uses_dense_model_timeout(self):
        client = mock.Mock()
        client.chat.return_value = self.result
        messages = qualify.user("long context")
        with mock.patch.object(qualify, "calibrated_context", return_value=(messages, 65344)), \
                mock.patch.object(qualify, "check_context"), \
                mock.patch.object(qualify, "idle"), contextlib.redirect_stdout(io.StringIO()):
            qualify.context_gate(client, 65536)
        client.chat.assert_called_once_with(
            messages, timeout=900, response_format={"type": "json_object"})

    def test_markdown_fences_do_not_pass_json_contract(self):
        self.result["answer"] = '```json\n' + self.result["answer"] + '\n```'
        with self.assertRaises(ValueError):
            qualify.check_context(self.result, self.expected, 65344, 65536)


class ToolTests(unittest.TestCase):
    def setUp(self):
        self.response = {"choices": [{"finish_reason": "tool_calls", "message": {
            "role": "assistant", "tool_calls": [{"id": "call_1", "type": "function",
                "function": {"name": "lookup_depot", "arguments": '{"depot":"Birch"}'}}]}}]}

    def test_valid_call_returns_id_for_tool_reply(self):
        _, call_id = qualify.check_tool(self.response)
        self.assertEqual(call_id, "call_1")

    def test_tool_shaped_prose_cannot_pass(self):
        self.response["choices"][0]["finish_reason"] = "stop"
        with self.assertRaisesRegex(ValueError, "finish reason"):
            qualify.check_tool(self.response)

    def test_wrong_arguments_fail(self):
        call = self.response["choices"][0]["message"]["tool_calls"][0]
        call["function"]["arguments"] = '{"depot":"Cedar"}'
        with self.assertRaisesRegex(ValueError, "arguments"):
            qualify.check_tool(self.response)

    def test_duplicate_calls_fail(self):
        calls = self.response["choices"][0]["message"]["tool_calls"]
        calls.append(copy.deepcopy(calls[0]))
        with self.assertRaisesRegex(ValueError, "exactly one"):
            qualify.check_tool(self.response)

    def test_reasoning_fails_default_contract(self):
        self.response["choices"][0]["message"]["reasoning_content"] = "Thinking"
        with self.assertRaisesRegex(ValueError, "reasoning"):
            qualify.check_tool(self.response)

    def test_embedded_thinking_fails_default_contract(self):
        self.response["choices"][0]["message"]["content"] = "<think>Let me think</think>"
        with self.assertRaisesRegex(ValueError, "reasoning"):
            qualify.check_tool(self.response)


class MetricsTests(unittest.TestCase):
    METRICS = ('vllm:num_requests_running{engine="0"} 2\n'
               'vllm:num_requests_waiting{engine="0"} 0\n'
               'vllm:kv_cache_usage_perc{engine="0"} 0.25\n')

    def test_ignores_waiting_by_reason_without_double_counting(self):
        result = qualify.parse_metrics(self.METRICS +
            'vllm:num_requests_waiting_by_reason{reason="capacity"} 7\n')
        self.assertEqual(result, {"running": 2, "waiting": 0, "kv": 0.25})

    def test_missing_metrics_cannot_mean_idle(self):
        with self.assertRaisesRegex(ValueError, "missing"):
            qualify.parse_metrics("# empty scrape\n")

    def test_nan_metric_cannot_pass(self):
        with self.assertRaisesRegex(ValueError, "invalid metric"):
            qualify.parse_metrics(self.METRICS.replace("0.25", "NaN"))

    def test_idle_waits_for_kv_and_requests_to_drain(self):
        client = mock.Mock()
        client.metrics.side_effect = [
            {"running": 1, "waiting": 0, "kv": 0.1},
            {"running": 0, "waiting": 0, "kv": 0.1},
            {"running": 0, "waiting": 0, "kv": 0},
        ]
        with mock.patch.object(qualify.time, "sleep"):
            qualify.idle(client)
        self.assertEqual(client.metrics.call_count, 3)


class VerdictTests(unittest.TestCase):
    def test_one_failed_phase_keeps_overall_failure(self):
        output = io.StringIO()
        with mock.patch.object(sys, "argv", ["qualify", "--model", "test"]), \
                mock.patch.object(qualify, "idle"), \
                mock.patch.object(qualify, "context_gate", side_effect=ValueError("recall failed")), \
                mock.patch.object(qualify, "tools_gate"), \
                mock.patch.object(qualify, "load_gate"), contextlib.redirect_stdout(output):
            code = qualify.main()
        result = json.loads(output.getvalue().splitlines()[-1])
        self.assertEqual(code, 1)
        self.assertEqual(result["verdict"], "fail")
        self.assertEqual(result["failures"], [{"phase": "context", "error": "recall failed"}])


if __name__ == "__main__":
    unittest.main()

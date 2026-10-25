"""Embedding qualification must fail closed and retain failed parity evidence."""

import argparse
import contextlib
import copy
import importlib.util
import io
import json
import math
import os
from pathlib import Path
import sys
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "qualify_embeddinggemma2", Path(__file__).with_name("qualify-embeddinggemma2.py")
)
qualify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qualify)


def unit_vector(angle=0.0, dimensions=qualify.DIMENSIONS):
    return [math.cos(angle), math.sin(angle)] + [0.0] * (dimensions - 2)


def response(vectors, tokens=11):
    return {
        "data": [
            {"index": index, "embedding": vector}
            for index, vector in enumerate(vectors)
        ],
        "usage": {"prompt_tokens": tokens},
    }


class VectorTests(unittest.TestCase):
    def test_nearest_rank_p95_keeps_the_longest_batch_in_a_small_sample(self):
        self.assertEqual(qualify.percentile([0.1, 0.2, 0.3, 0.4, 80], 0.95), 80)

    def test_normalized_vector_passes_with_explicit_dimensions(self):
        self.assertAlmostEqual(qualify.validate_vector([0.6, 0.8], 2), 1.0)
        self.assertAlmostEqual(qualify.validate_vector(unit_vector()), 1.0)

    def test_invalid_coordinates_are_rejected(self):
        for value in (float("nan"), float("inf"), -float("inf"), True, None, "1"):
            with self.subTest(value=value):
                vector = unit_vector()
                vector[-1] = value
                with self.assertRaisesRegex(ValueError, "invalid or non-finite"):
                    qualify.validate_vector(vector)

    def test_zero_and_unnormalized_vectors_are_rejected(self):
        for vector in (
            [0.0] * qualify.DIMENSIONS,
            [2.0] + [0.0] * (qualify.DIMENSIONS - 1),
        ):
            with self.subTest(norm=sum(value * value for value in vector)):
                with self.assertRaisesRegex(ValueError, "not normalized"):
                    qualify.validate_vector(vector)

    def test_wrong_shape_is_rejected(self):
        for vector in ([], [1.0], tuple(unit_vector()), "base64"):
            with self.subTest(shape=type(vector).__name__):
                with self.assertRaisesRegex(ValueError, "coordinates"):
                    qualify.validate_vector(vector)


class EmbeddingResponseTests(unittest.TestCase):
    def call_embed(self, value, **kwargs):
        with mock.patch.object(
            qualify, "request_json", return_value=value
        ), mock.patch.object(qualify.time, "monotonic", side_effect=[1.0, 1.25]):
            return qualify.embed(
                "http://fixture/", "gemma", ["query", "document"], **kwargs
            )

    def test_indices_restore_request_order_and_actual_usage(self):
        vectors = [unit_vector(0.1), unit_vector(0.2)]
        value = response(vectors, tokens=137)
        value["data"].reverse()
        actual, measurement = self.call_embed(value)
        self.assertEqual(actual, vectors)
        self.assertEqual(
            measurement, {"seconds": 0.25, "documents": 2, "prompt_tokens": 137}
        )

    def test_count_and_indices_must_match_all_inputs(self):
        valid = response([unit_vector(), unit_vector(0.1)])
        rows = valid["data"]
        cases = {
            "empty": [],
            "missing row": rows[:1],
            "extra row": rows + [{"index": 2, "embedding": unit_vector()}],
            "missing index": [{"embedding": unit_vector()}, rows[1]],
            "duplicate": [rows[0], rows[0]],
            "negative": [{"index": -1, "embedding": unit_vector()}, rows[1]],
            "out of range": [rows[0], {"index": 2, "embedding": unit_vector()}],
            "string index": [
                {"index": "0", "embedding": unit_vector()},
                {"index": "1", "embedding": unit_vector()},
            ],
            "boolean index": [rows[0], {"index": True, "embedding": unit_vector()}],
            "floating index": [
                {"index": 0.0, "embedding": unit_vector()},
                {"index": 1.0, "embedding": unit_vector()},
            ],
        }
        for name, data in cases.items():
            with self.subTest(name=name):
                value = {**valid, "data": data}
                with self.assertRaisesRegex(ValueError, "indices|index"):
                    self.call_embed(value)

    def test_vector_validation_applies_to_every_response_row(self):
        for vector in (
            [],
            [0.0] * qualify.DIMENSIONS,
            [2.0] + [0.0] * (qualify.DIMENSIONS - 1),
            [float("nan")] + [0.0] * (qualify.DIMENSIONS - 1),
        ):
            with self.subTest(first_coordinate=vector[:1]):
                with self.assertRaises(ValueError):
                    self.call_embed(response([unit_vector(), vector]))

    def test_positive_integer_usage_is_required_without_estimates(self):
        for usage in (
            {},
            {"total_tokens": 12},
            {"prompt_tokens": None},
            {"prompt_tokens": 0},
            {"prompt_tokens": -1},
            {"prompt_tokens": "12"},
            {"prompt_tokens": 12.0},
            {"prompt_tokens": True},
        ):
            with self.subTest(usage=usage):
                value = response([unit_vector(), unit_vector()])
                value["usage"] = usage
                with self.assertRaisesRegex(
                    ValueError, "actual positive usage.prompt_tokens"
                ):
                    self.call_embed(value)
        value = response([unit_vector(), unit_vector()])
        del value["usage"]
        with self.assertRaisesRegex(ValueError, "actual positive usage.prompt_tokens"):
            self.call_embed(value)

    def test_custom_dimensions_are_requested_and_checked(self):
        with mock.patch.object(
            qualify, "request_json", return_value=response([[0.6, 0.8]])
        ) as request, mock.patch.object(
            qualify.time, "monotonic", side_effect=[1.0, 1.5]
        ):
            vectors, _ = qualify.embed(
                "http://fixture/",
                "gemma",
                ["text"],
                bearer="fixture-token",
                dimensions=2,
            )
        self.assertEqual(vectors, [[0.6, 0.8]])
        request.assert_called_once_with(
            "http://fixture/v1/embeddings",
            {
                "model": "gemma",
                "input": ["text"],
                "encoding_format": "float",
                "dimensions": 2,
            },
            bearer="fixture-token",
        )

    def test_request_json_uses_post_json_and_bearer_header(self):
        payload = {"model": "gemma", "input": ["query"]}
        with mock.patch.object(
            qualify.urllib.request,
            "urlopen",
            return_value=io.StringIO('{"ready":true}'),
        ) as open_url:
            self.assertEqual(
                qualify.request_json(
                    "http://fixture/v1/embeddings",
                    payload,
                    timeout=7,
                    bearer="fixture-token",
                ),
                {"ready": True},
            )
        request = open_url.call_args.args[0]
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(json.loads(request.data), payload)
        self.assertEqual(request.get_header("Authorization"), "Bearer fixture-token")
        self.assertEqual(request.get_header("Content-type"), "application/json")
        self.assertEqual(open_url.call_args.kwargs["timeout"], 7)


class ParityGateTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.samples = [
            {
                "name": "search-query",
                "text": "task: search result | query: GPU",
                "vector": unit_vector(),
            },
            {
                "name": "near-document",
                "text": "title: GPU | text: local inference",
                "vector": unit_vector(0.1),
            },
            {
                "name": "other-document",
                "text": "title: CPUs | text: CPU inference",
                "vector": unit_vector(0.11),
            },
            {
                "name": "short",
                "text": "title: none | text: hello",
                "vector": unit_vector(0.7),
            },
        ]
        self.reference = {
            "model_id": qualify.MODEL_ID,
            "revision": qualify.MODEL_REVISION,
            "dtype": "float32",
            "dimensions": qualify.DIMENSIONS,
            "samples": self.samples,
        }
        self.args = argparse.Namespace(
            reference=str(self.root / "reference.json"),
            base_url="http://fixture",
            model="gemma",
            batch_size=2,
            bearer_env="EMBEDDING_TEST_TOKEN",
            min_cosine=0.99,
            output=str(self.root / "results" / "report.json"),
        )
        self.save_reference()

    def save_reference(self):
        Path(self.args.reference).write_text(json.dumps(self.reference))

    @contextlib.contextmanager
    def candidate(self, overrides=None, single_override=None, resident_responses=None):
        vectors = {sample["text"]: sample["vector"] for sample in self.samples}
        vectors.update(overrides or {})
        calls = []
        candidate_calls = 0

        def server(url, payload, bearer=None):
            nonlocal candidate_calls
            calls.append((url, payload, bearer))
            if resident_responses and payload["model"] in resident_responses:
                return copy.deepcopy(resident_responses[payload["model"]])
            candidate_calls += 1
            result = [vectors[text] for text in payload["input"]]
            if candidate_calls == 3 and single_override is not None:
                result = [single_override]
            value = response(result, tokens=[31, 7, 5][candidate_calls - 1])
            value["data"].reverse()
            return value

        durations = (
            [0.25, 0.1, 0.2, 0.5, 0.1, 0.2, 0.2]
            if resident_responses
            else [0.25, 0.5, 0.2]
        )
        times = [
            value
            for index, duration in enumerate(durations)
            for value in (float(index), index + duration)
        ]
        with mock.patch.object(
            qualify, "request_json", side_effect=server
        ), mock.patch.object(
            qualify.time, "monotonic", side_effect=times
        ), mock.patch.dict(
            os.environ, {"EMBEDDING_TEST_TOKEN": "fixture-token"}
        ), contextlib.redirect_stdout(
            io.StringIO()
        ):
            yield calls

    def report(self):
        output = Path(self.args.output)
        self.assertTrue(
            output.is_file(), "qualification evidence must survive a parity failure"
        )
        self.assertFalse(output.with_suffix(".json.tmp").exists())
        return json.loads(output.read_text())

    def test_pinned_fp32_reference_and_equal_vectors_pass(self):
        with self.candidate() as calls:
            result = qualify.compare(self.args)
        self.assertTrue(result["passed"])
        self.assertEqual(self.report(), result)
        self.assertEqual(result["reference_revision"], qualify.MODEL_REVISION)
        self.assertAlmostEqual(result["minimum_cosine"], 1.0)
        self.assertAlmostEqual(result["single_batch_cosine"], 1.0)
        self.assertTrue(all(row["top1_matches"] for row in result["retrieval_parity"]))
        self.assertEqual(
            result["prompt_tokens"],
            38,
            "single probe usage is excluded from throughput",
        )
        self.assertEqual(result["samples"], 4)
        self.assertAlmostEqual(result["seconds"], 0.75)
        self.assertAlmostEqual(result["input_tokens_per_second"], 38 / 0.75)
        self.assertAlmostEqual(result["documents_per_second"], 4 / 0.75)
        self.assertEqual([len(call[1]["input"]) for call in calls], [2, 2, 1])
        self.assertTrue(all(call[2] == "fixture-token" for call in calls))

    def test_reference_provenance_rejected_before_http(self):
        for field, value in (
            ("model_id", "different-model"),
            ("revision", "unpinned"),
            ("dtype", "bfloat16"),
        ):
            with self.subTest(field=field):
                reference = copy.deepcopy(self.reference)
                reference[field] = value
                Path(self.args.reference).write_text(json.dumps(reference))
                with mock.patch.object(qualify, "request_json") as request:
                    with self.assertRaisesRegex(ValueError, "reference provenance"):
                        qualify.compare(self.args)
                request.assert_not_called()

    def test_vector_parity_failure_persists_report(self):
        changed = {self.samples[3]["text"]: unit_vector(1.2)}
        with self.candidate(changed):
            with self.assertRaisesRegex(ValueError, "parity gate"):
                qualify.compare(self.args)
        report = self.report()
        self.assertFalse(report["passed"])
        self.assertLess(report["minimum_cosine"], self.args.min_cosine)
        self.assertTrue(all(row["top1_matches"] for row in report["retrieval_parity"]))

    def test_ranking_gate_can_fail_despite_passing_vector_cosines(self):
        changed = {self.samples[0]["text"]: unit_vector(0.13)}
        with self.candidate(changed):
            with self.assertRaisesRegex(ValueError, "parity gate"):
                qualify.compare(self.args)
        report = self.report()
        self.assertFalse(report["passed"])
        self.assertGreaterEqual(report["minimum_cosine"], self.args.min_cosine)
        self.assertFalse(report["retrieval_parity"][0]["top1_matches"])
        self.assertEqual(
            report["retrieval_parity"][0]["reference_top"], "near-document"
        )
        self.assertEqual(
            report["retrieval_parity"][0]["candidate_top"], "other-document"
        )

    def test_single_batch_disagreement_fails_with_report(self):
        with self.candidate(single_override=unit_vector(0.2)):
            with self.assertRaisesRegex(ValueError, "parity gate"):
                qualify.compare(self.args)
        report = self.report()
        self.assertFalse(report["passed"])
        self.assertGreaterEqual(report["minimum_cosine"], self.args.min_cosine)
        self.assertTrue(all(row["top1_matches"] for row in report["retrieval_parity"]))
        self.assertLess(report["single_batch_cosine"], 0.999)

    def test_co_resident_probes_interleave_without_inflating_candidate_throughput(self):
        self.args.co_resident = ["bge-m3=http://bge,1024", "gte-qwen2=http://gte,1536"]
        responses = {
            "bge-m3": response([unit_vector(0.2, 1024)], tokens=11),
            "gte-qwen2": response([unit_vector(0.3, 1536)], tokens=13),
        }
        with self.candidate(resident_responses=responses) as calls:
            result = qualify.compare(self.args)
        self.assertTrue(result["passed"])
        self.assertEqual(self.report(), result)
        self.assertEqual(
            [call[1]["model"] for call in calls],
            ["gemma", "bge-m3", "gte-qwen2", "gemma", "bge-m3", "gte-qwen2", "gemma"],
        )
        probes = result["co_resident_probes"]
        self.assertEqual([row["batch"] for row in probes], [0, 0, 2, 2])
        self.assertEqual([row["prompt_tokens"] for row in probes], [11, 13, 11, 13])
        self.assertTrue(all(row["documents"] == 1 for row in probes))
        self.assertAlmostEqual(sum(row["seconds"] for row in probes), 0.6)
        self.assertEqual(result["prompt_tokens"], 38)
        self.assertAlmostEqual(result["seconds"], 0.75)
        self.assertAlmostEqual(result["input_tokens_per_second"], 38 / 0.75)
        self.assertAlmostEqual(result["documents_per_second"], 4 / 0.75)
        for url, payload, bearer in calls:
            if payload["model"] in responses:
                expected = 1024 if payload["model"] == "bge-m3" else 1536
                self.assertEqual(payload["dimensions"], expected)
                self.assertEqual(len(payload["input"]), 1)
                self.assertEqual(
                    url,
                    (
                        "http://bge/v1/embeddings"
                        if expected == 1024
                        else "http://gte/v1/embeddings"
                    ),
                )
                self.assertIsNone(bearer)

    def test_invalid_co_resident_vectors_prevent_a_pass(self):
        self.args.co_resident = ["bge-m3=http://bge,1024"]
        bad_vectors = [unit_vector(), [0.0] * 1024, [float("nan")] + [0.0] * 1023]
        for vector in bad_vectors:
            with self.subTest(coordinate=vector[:1]):
                with self.candidate(resident_responses={"bge-m3": response([vector])}):
                    with self.assertRaises(ValueError):
                        qualify.compare(self.args)
        self.assertFalse(Path(self.args.output).exists())

    def test_co_resident_usage_is_also_required(self):
        self.args.co_resident = ["bge-m3=http://bge,1024"]
        value = response([unit_vector(0.2, 1024)])
        del value["usage"]
        with self.candidate(resident_responses={"bge-m3": value}):
            with self.assertRaisesRegex(
                ValueError, "actual positive usage.prompt_tokens"
            ):
                qualify.compare(self.args)
        self.assertFalse(Path(self.args.output).exists())

    def test_compare_cli_preserves_repeated_co_residents(self):
        residents = ["bge-m3=http://bge,1024", "gte-qwen2=http://gte,1536"]
        arguments = [
            "qualify",
            "compare",
            "--reference",
            self.args.reference,
            "--base-url",
            self.args.base_url,
            "--output",
            self.args.output,
            "--co-resident",
            residents[0],
            "--co-resident",
            residents[1],
        ]
        with mock.patch.object(sys, "argv", arguments), mock.patch.object(
            qualify, "compare"
        ) as compare:
            self.assertEqual(qualify.main(), 0)
        compare.assert_called_once()
        self.assertEqual(compare.call_args.args[0].co_resident, residents)


class GPUOffloadTests(unittest.TestCase):
    def test_complete_nonzero_offload_is_required(self):
        log = "ggml_cuda_init: Found AMD Radeon VII\nload_tensors: offloaded 24/24 layers to GPU\n"
        self.assertEqual(
            qualify.require_gpu_offload(log), "offloaded 24/24 layers to GPU"
        )

    def test_missing_partial_and_zero_offload_are_rejected(self):
        for log in (
            "",
            "load_tensors: CPU model buffer size = 500 MiB",
            "load_tensors: offloaded 0/24 layers to GPU",
            "load_tensors: offloaded 0/0 layers to GPU",
            "load_tensors: offloaded 23/24 layers to GPU",
        ):
            with self.subTest(log=log):
                with self.assertRaisesRegex(
                    ValueError, "did not offload every model layer"
                ):
                    qualify.require_gpu_offload(log)


class RuntimeTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.process = mock.Mock(pid=1234, returncode=2)
        self.process.poll.return_value = None
        self.args = argparse.Namespace(
            reference=str(self.root / "reference.json"),
            artifacts=str(self.root),
            model_directory=None,
            output_dir=str(self.root / "reports"),
            runtime_binary="/fixture/runtime",
            bge_model=str(self.root / "bge.gguf"),
            gte_model=str(self.root / "gte.gguf"),
            batch_size=4,
        )
        for path in [self.args.reference] + [
            payload["modelPath"] for _, payload in qualify.runtime_load_specs(self.args)
        ]:
            file = Path(path)
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text("fixture")

    def test_load_payloads_use_gpu_ports_pooling_context_and_logging(self):
        specs = qualify.runtime_load_specs(self.args)
        self.assertEqual(
            [payload["config"]["port"] for _, payload in specs],
            [18082, 18083, 18081, 18081],
        )
        self.assertNotIn("pooling", specs[0][1]["config"])
        self.assertEqual(specs[1][1]["config"]["pooling"], "last")
        self.assertEqual(
            [payload["config"]["contextSize"] for _, payload in specs],
            [512, 1024, 8192, 8192],
        )
        self.assertEqual(
            [payload["config"]["ubatchSize"] for _, payload in specs],
            [512, 512, 8192, 8192],
        )
        self.assertEqual(
            [payload["config"]["vramEstimateMB"] for _, payload in specs],
            [1500, 2600, 3000, 3000],
        )
        for _, payload in specs:
            self.assertEqual(payload["backend"], "llamacpp")
            self.assertEqual(payload["config"]["nGPULayers"], 999)
            self.assertNotIn("flashAttention", payload["config"])
            self.assertTrue(payload["config"]["fitOff"])
            self.assertTrue(payload["config"]["embedding"])
            self.assertEqual(payload["config"]["threads"], 4)
            self.assertEqual(payload["config"]["parallel"], 1)
            self.assertEqual(
                payload["env"], [{"name": "LLAMA_ARG_LOG_VERBOSITY", "value": "5"}]
            )
        self.assertTrue(
            specs[2][1]["modelPath"].endswith("gguf/embeddinggemma-2-BF16.gguf")
        )
        self.args.model_directory = "/alternative"
        self.assertEqual(
            qualify.runtime_load_specs(self.args)[3][1]["modelPath"],
            "/alternative/embeddinggemma-2-Q8_0.gguf",
        )

    def test_readiness_polls_loading_and_retries_connection_failure(self):
        ready = {"models": [{"name": "gemma", "state": "Ready"}]}
        with mock.patch.object(
            qualify,
            "request_json",
            side_effect=[
                OSError("starting"),
                {"models": [{"name": "gemma", "state": "Loading"}]},
                ready,
            ],
        ) as request, mock.patch.object(qualify.time, "sleep") as sleep:
            self.assertEqual(
                qualify.wait_runtime_model(self.process, "http://runtime", "gemma"),
                ready,
            )
        self.assertEqual(request.call_count, 3)
        self.assertEqual(sleep.call_count, 2)

    def test_failed_resident_crashed_manager_and_deadline_fail_closed(self):
        value = {
            "models": [
                {"name": "bge", "state": "Failed", "error": "HIP failure"},
                {"name": "gemma", "state": "Ready"},
            ]
        }
        with mock.patch.object(qualify, "request_json", return_value=value):
            with self.assertRaisesRegex(RuntimeError, "bge failed: HIP failure"):
                qualify.wait_runtime_model(self.process, "http://runtime", "gemma")
        self.process.poll.return_value = 2
        with self.assertRaisesRegex(RuntimeError, "runtime exited 2"):
            qualify.wait_runtime_model(self.process, "http://runtime")
        self.process.poll.return_value = None
        with mock.patch.object(
            qualify.time, "monotonic", side_effect=[0, 181]
        ), mock.patch.object(qualify, "request_json") as request:
            with self.assertRaisesRegex(TimeoutError, "readiness exceeded 180"):
                qualify.wait_runtime_model(self.process, "http://runtime", "gemma")
            request.assert_not_called()

    def test_load_requires_new_model_attributed_offload_proof(self):
        name, payload = qualify.runtime_load_specs(self.args)[2]
        log = self.root / "runtime.log"
        proof = self.root / "proof.log"
        snapshot = {
            "models": [{"name": name, "state": "Ready", "pid": 55, "port": 18081}]
        }
        for own_offload in ("25/25", "24/25"):
            with self.subTest(offload=own_offload):
                log.write_text(
                    'offloaded 25/25 layers to GPU {"model":"embeddinggemma-2"}\n'
                )

                def request(url, value=None, **kwargs):
                    if url.endswith("/load"):
                        self.assertEqual(value, payload)
                        with log.open("a") as output:
                            output.write(
                                'offloaded 25/25 layers to GPU {"model":"bge"}\n'
                            )
                            output.write(
                                f'offloaded {own_offload} layers to GPU {{"model":"{name}"}}\n'
                            )
                        return {"status": "loading"}
                    return snapshot

                with mock.patch.object(qualify, "request_json", side_effect=request):
                    if own_offload == "25/25":
                        result = qualify.load_runtime_model(
                            self.process, "http://runtime", name, payload, log, proof
                        )
                        self.assertEqual(
                            result["gpu_offload"], "offloaded 25/25 layers to GPU"
                        )
                        self.assertEqual(result["pid"], 55)
                        self.assertEqual(result["snapshot"], snapshot)
                    else:
                        with self.assertRaisesRegex(ValueError, "did not offload"):
                            qualify.load_runtime_model(
                                self.process,
                                "http://runtime",
                                name,
                                payload,
                                log,
                                proof,
                            )
                self.assertNotIn('"model":"bge"', proof.read_text())
                self.assertEqual(len(proof.read_text().splitlines()), 1)

    def test_cleanup_signals_the_group_and_waits_for_a_graceful_exit(self):
        with mock.patch.object(
            qualify.os, "killpg", side_effect=[None, ProcessLookupError()]
        ) as kill, mock.patch.object(qualify.time, "monotonic", side_effect=[0, 1]):
            qualify.stop_process_group(self.process)
        self.assertEqual(
            kill.call_args_list,
            [mock.call(1234, qualify.signal.SIGTERM), mock.call(1234, 0)],
        )
        self.process.wait.assert_called_once_with(timeout=20)

    def test_cleanup_kills_the_group_after_the_shared_grace_deadline(self):
        self.process.wait.side_effect = [subprocess.TimeoutExpired("runtime", 20), 0]
        with mock.patch.object(qualify.os, "killpg") as kill, mock.patch.object(
            qualify.time, "monotonic", side_effect=[0, 21]
        ):
            qualify.stop_process_group(self.process)
        self.assertEqual(
            kill.call_args_list,
            [
                mock.call(1234, qualify.signal.SIGTERM),
                mock.call(1234, qualify.signal.SIGKILL),
            ],
        )
        self.assertEqual(
            self.process.wait.call_args_list,
            [mock.call(timeout=20), mock.call(timeout=5)],
        )

    def test_runtime_uses_a_private_session_and_cleans_up_on_failure(self):
        with mock.patch.object(
            qualify.subprocess, "Popen", return_value=self.process
        ) as start, mock.patch.object(qualify, "stop_process_group") as stop:
            with self.assertRaisesRegex(ValueError, "qualification failed"):
                with qualify.runtime_manager(
                    "/fixture/runtime", self.root / "runtime.log"
                ):
                    raise ValueError("qualification failed")
        command = start.call_args.args[0]
        self.assertEqual(
            command,
            [
                "/fixture/runtime",
                "--listen",
                "127.0.0.1:18088",
                "--multi-model",
                "--gpu-vendor",
                "amd",
                "--gpu-arch",
                "gfx906",
                "--shutdown-timeout",
                "20s",
            ],
        )
        self.assertTrue(start.call_args.kwargs["start_new_session"])
        stop.assert_called_once_with(self.process)

    @contextlib.contextmanager
    def fake_runtime(self, fail_compare=False, restart_resident=False):
        events, models = [], {}

        @contextlib.contextmanager
        def manager(*args):
            try:
                yield self.process, ["runtime"]
            finally:
                events.append("stopped")

        def load(process, base_url, name, payload, runtime_log, proof_path):
            quant = "BF16" if "BF16" in payload["modelPath"] else "Q8_0"
            events.append(("load", name, quant))
            models[name] = {
                "name": name,
                "state": "Ready",
                "pid": 100 + len(events),
                "port": payload["config"]["port"],
            }
            return {
                "name": name,
                "pid": models[name]["pid"],
                "port": models[name]["port"],
                "gpu_offload": "offloaded 25/25 layers to GPU",
            }

        def request(url, **kwargs):
            self.assertEqual(kwargs["method"], "DELETE")
            events.append("unloaded BF16")
            models.pop("embeddinggemma-2")
            return {"status": "unloaded"}

        def compare(args):
            quant = "BF16" if "BF16" in args.output else "Q8_0"
            events.append(("compare", quant))
            self.assertEqual(args.base_url, "http://127.0.0.1:18081")
            self.assertEqual(args.min_cosine, 0.99 if quant == "BF16" else 0.98)
            self.assertEqual(
                args.co_resident,
                [
                    "bge-large-radeonvii=http://127.0.0.1:18082,1024",
                    "gte-qwen2-1p5b-radeonvii=http://127.0.0.1:18083,1536",
                ],
            )
            report = {"passed": not fail_compare}
            if restart_resident:
                models["bge-large-radeonvii"]["pid"] += 100
            if fail_compare:
                qualify.write_json(args.output, report)
                raise ValueError("parity failed")
            return report

        with mock.patch.object(
            qualify, "runtime_manager", side_effect=manager
        ), mock.patch.object(
            qualify, "load_runtime_model", side_effect=load
        ), mock.patch.object(
            qualify,
            "wait_runtime_model",
            side_effect=lambda *args: {"models": copy.deepcopy(list(models.values()))},
        ), mock.patch.object(
            qualify, "request_json", side_effect=request
        ), mock.patch.object(
            qualify, "compare", side_effect=compare
        ):
            yield events

    def test_bf16_unloads_before_q8_and_residents_keep_their_processes(self):
        with self.fake_runtime() as events:
            result = qualify.isolated_runtime(self.args)
        self.assertTrue(result["passed"])
        self.assertEqual(
            [row["quantization"] for row in result["results"]], ["BF16", "Q8_0"]
        )
        self.assertEqual(len(result["model_loads"]), 4)
        self.assertEqual(result["runtime_pid"], 1234)
        self.assertLess(
            events.index("unloaded BF16"),
            events.index(("load", "embeddinggemma-2", "Q8_0")),
        )
        self.assertEqual(events[-1], "stopped")
        first, second = result["runtime_snapshots"]
        for resident in ("bge-large-radeonvii", "gte-qwen2-1p5b-radeonvii"):
            self.assertEqual(
                next(row["pid"] for row in first["models"] if row["name"] == resident),
                next(row["pid"] for row in second["models"] if row["name"] == resident),
            )
        self.assertEqual(
            json.loads(
                (Path(self.args.output_dir) / "runtime-summary.json").read_text()
            ),
            result,
        )

    def test_failure_preserves_evidence_and_stops_the_runtime(self):
        with self.fake_runtime(fail_compare=True) as events:
            with self.assertRaisesRegex(ValueError, "parity failed"):
                qualify.isolated_runtime(self.args)
        summary = json.loads(
            (Path(self.args.output_dir) / "runtime-summary.json").read_text()
        )
        self.assertFalse(summary["passed"])
        self.assertEqual(summary["error"], "parity failed")
        self.assertEqual(summary["runtime_pid"], 1234)
        self.assertEqual(summary["results"], [{"passed": False}])
        self.assertEqual(events[-1], "stopped")

    def test_ready_resident_with_a_changed_pid_cannot_pass(self):
        with self.fake_runtime(restart_resident=True) as events:
            with self.assertRaisesRegex(
                ValueError, "bge-large-radeonvii process PID changed"
            ):
                qualify.isolated_runtime(self.args)
        summary = json.loads(
            (Path(self.args.output_dir) / "runtime-summary.json").read_text()
        )
        self.assertFalse(summary["passed"])
        self.assertIn("process PID changed", summary["error"])
        self.assertEqual(summary["runtime_snapshots"][0]["models"][0]["state"], "Ready")
        self.assertNotEqual(
            summary["runtime_snapshots"][0]["models"][0]["pid"],
            summary["model_loads"][0]["pid"],
        )
        self.assertEqual(events[-1], "stopped")

    def test_isolated_runtime_cli_routes_required_artifacts(self):
        arguments = [
            "qualify",
            "isolated-runtime",
            "--reference",
            self.args.reference,
            "--artifacts",
            self.args.artifacts,
            "--output-dir",
            self.args.output_dir,
            "--runtime-binary",
            "/fixture/runtime",
        ]
        with mock.patch.object(sys, "argv", arguments), mock.patch.object(
            qualify, "isolated_runtime"
        ) as run:
            self.assertEqual(qualify.main(), 0)
        parsed = run.call_args.args[0]
        self.assertEqual(parsed.runtime_binary, "/fixture/runtime")
        self.assertEqual(parsed.batch_size, 4)
        self.assertEqual(
            parsed.bge_model,
            "/models/flexinfer-system/bge-large-radeonvii/bge-large-en-v1.5-q8_0.gguf",
        )

    def test_rocm_errors_reject_even_an_apparent_complete_offload(self):
        for error in ("no usable GPU found", "failed to initialize ROCm"):
            with self.subTest(error=error):
                log = "load_tensors: offloaded 24/24 layers to GPU\n" + error
                with self.assertRaisesRegex(ValueError, "unusable ROCm device"):
                    qualify.require_gpu_offload(log)


if __name__ == "__main__":
    unittest.main()

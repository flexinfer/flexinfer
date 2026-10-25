#!/usr/bin/env python3
"""Qualify text-only llama.cpp embeddings against a pinned FP32 reference.

The reference subcommand downloads the pinned artifacts, writes reusable
vectors, and tests the CPU server. The compare subcommand needs only the
standard library and can run inside the gfx906 image or against the proxy.
"""

import argparse
import contextlib
import json
import math
import os
from pathlib import Path
import re
import signal
import statistics
import subprocess
import sys
import time
import traceback
import urllib.error
import urllib.request
import urllib.parse

MODEL_ID = "google/embeddinggemma-2"
MODEL_REVISION = "914f7f89142e33e77833254d9c9b90c3cef7303b"
GGUF_ID = "ggml-org/embeddinggemma-2-GGUF"
GGUF_REVISION = "bfcd298762cc34d0357ece5ebdd31791a3a374d8"
DIMENSIONS = 768


def corpus():
    """Explicit task prefixes keep query/document semantics identical."""
    examples = [
        (
            "code-query",
            "task: code retrieval | query: Find a Go function that retries HTTP requests with exponential backoff.",
        ),
        (
            "code-document",
            'title: retry.go | text: func retry(ctx context.Context, call func() error) error { for attempt := 0; attempt < 3; attempt++ { if err := call(); err == nil { return nil }; time.Sleep(time.Second << attempt) }; return errors.New("retry exhausted") }',
        ),
        (
            "search-query",
            "task: search result | query: How do I safely update a Kubernetes custom resource definition?",
        ),
        (
            "search-document",
            "title: Kubernetes CRD upgrades | text: Helm does not automatically upgrade CRDs. Review the schema, apply the CRD explicitly, and verify existing custom resources before upgrading the controller.",
        ),
        (
            "unrelated-document",
            "title: Gardening | text: Tomato plants need warm soil, direct sunlight, regular watering, and a support stake.",
        ),
        (
            "python-query",
            "task: code retrieval | query: Sort records by a date and preserve stable order for ties.",
        ),
        (
            "python-document",
            "title: dates.py | text: def ordered(records): return sorted(records, key=lambda record: record['date'])",
        ),
        (
            "chinese-query",
            "task: search result | query: 如何在本地服务器上运行文本向量模型？",
        ),
        (
            "chinese-document",
            "title: 本地向量检索 | text: 使用本地推理服务生成文档和查询的向量，再通过余弦相似度检索相关内容。",
        ),
        (
            "spanish-query",
            "task: search result | query: ¿Cómo buscar documentos por significado en lugar de palabras exactas?",
        ),
        (
            "spanish-document",
            "title: Búsqueda semántica | text: Los vectores representan el significado de consultas y documentos. La similitud permite encontrar contenido relacionado aunque use otras palabras.",
        ),
        ("short", "title: none | text: hello"),
        (
            "unicode",
            "title: Unicode | text: Café, naïve, résumé, 日本語, العربية, emoji 🚀, and source_code identifiers.",
        ),
        (
            "numbers",
            "title: Release notes | text: Version 2.14.0 fixes issue #128, reduces latency from 120 ms to 45 ms, and uses 768-dimensional vectors.",
        ),
        (
            "whitespace",
            "title: whitespace.py | text: def identity(value):\n    return value\n\n# End of file\n",
        ),
        (
            "medium",
            "title: Operations | text: "
            + "Stage model files, check readiness, compare embeddings, and preserve retrieval availability. "
            * 24,
        ),
        (
            "long",
            "title: Long document | text: "
            + "Local embedding inference processes documents using bidirectional attention and mean pooling. "
            * 70,
        ),
    ]
    return [{"name": name, "text": text} for name, text in examples]


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
    temporary.replace(path)


def request_json(url, payload=None, timeout=180, bearer=None, method=None):
    headers = {"Content-Type": "application/json"}
    if bearer:
        headers["Authorization"] = "Bearer " + bearer
    request = urllib.request.Request(
        url,
        data=None if payload is None else json.dumps(payload).encode(),
        headers=headers,
        method=method,
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.load(response)


def validate_vector(vector, dimensions=DIMENSIONS):
    if not isinstance(vector, list) or len(vector) != dimensions:
        raise ValueError(
            f"expected {dimensions} coordinates, got {len(vector) if isinstance(vector, list) else type(vector).__name__}"
        )
    if any(
        isinstance(value, bool)
        or not isinstance(value, (float, int))
        or not math.isfinite(value)
        for value in vector
    ):
        raise ValueError("embedding contains invalid or non-finite coordinates")
    norm = math.sqrt(sum(value * value for value in vector))
    if norm <= 0 or abs(norm - 1) > 0.001:
        raise ValueError(f"embedding is not normalized: norm={norm}")
    return norm


def cosine(left, right):
    return sum(a * b for a, b in zip(left, right)) / math.sqrt(
        sum(a * a for a in left) * sum(b * b for b in right)
    )


def percentile(values, fraction):
    values = sorted(values)
    return values[max(0, math.ceil(len(values) * fraction) - 1)]


def embed(base_url, model, texts, bearer=None, dimensions=None):
    payload = {"model": model, "input": texts, "encoding_format": "float"}
    if dimensions is not None:
        payload["dimensions"] = dimensions
    start = time.monotonic()
    response = request_json(
        base_url.rstrip("/") + "/v1/embeddings", payload, bearer=bearer
    )
    elapsed = time.monotonic() - start
    rows = response.get("data", [])
    if not isinstance(rows, list) or any(
        not isinstance(row, dict) or type(row.get("index")) is not int for row in rows
    ):
        raise ValueError("response indices must be integers")
    if len(rows) != len(texts) or sorted(row.get("index", -1) for row in rows) != list(
        range(len(texts))
    ):
        raise ValueError("response count/indices do not match request order")
    vectors = [row["embedding"] for row in sorted(rows, key=lambda row: row["index"])]
    for vector in vectors:
        validate_vector(vector, dimensions or DIMENSIONS)
    tokens = response.get("usage", {}).get("prompt_tokens")
    if type(tokens) is not int or tokens <= 0:
        raise ValueError("response lacks actual positive usage.prompt_tokens")
    return vectors, {
        "seconds": elapsed,
        "documents": len(texts),
        "prompt_tokens": tokens,
    }


def compare(args):
    reference = json.loads(Path(args.reference).read_text())
    if (
        reference.get("model_id") != MODEL_ID
        or reference.get("revision") != MODEL_REVISION
        or reference.get("dtype") != "float32"
    ):
        raise ValueError(
            "reference provenance must be the pinned EmbeddingGemma 2 FP32 model"
        )
    samples = reference["samples"]
    vectors, measurements, resident_probes = [], [], []
    bearer = os.environ.get(args.bearer_env) if args.bearer_env else None
    for start in range(0, len(samples), args.batch_size):
        batch = samples[start : start + args.batch_size]
        result, measurement = embed(
            args.base_url, args.model, [sample["text"] for sample in batch], bearer
        )
        vectors.extend(result)
        measurements.append(measurement)
        for resident in getattr(args, "co_resident", []):
            name, endpoint_and_dimensions = resident.split("=", 1)
            endpoint, dimensions = endpoint_and_dimensions.rsplit(",", 1)
            _, probe = embed(
                endpoint,
                name,
                ["The local GPU serves document embeddings."],
                dimensions=int(dimensions),
            )
            resident_probes.append({"model": name, "batch": start, **probe})
    similarities = []
    for sample, vector in zip(samples, vectors):
        validate_vector(sample["vector"])
        similarities.append(
            {"name": sample["name"], "cosine": cosine(sample["vector"], vector)}
        )
    # Compare query/document rankings within this model's vector space. This
    # checks parity with the reference, not superiority over BGE or GTE.
    docs = [i for i, sample in enumerate(samples) if "document" in sample["name"]]
    queries = [i for i, sample in enumerate(samples) if "query" in sample["name"]]
    rankings = []
    for query in queries:
        expected = sorted(
            docs,
            key=lambda i: cosine(samples[query]["vector"], samples[i]["vector"]),
            reverse=True,
        )
        actual = sorted(
            docs, key=lambda i: cosine(vectors[query], vectors[i]), reverse=True
        )
        rankings.append(
            {
                "query": samples[query]["name"],
                "reference_top": samples[expected[0]]["name"],
                "candidate_top": samples[actual[0]]["name"],
                "top1_matches": actual[0] == expected[0],
            }
        )
    # Single versus batch agreement catches pooling/sequence boundary errors.
    single, single_measurement = embed(
        args.base_url, args.model, [samples[0]["text"]], bearer
    )
    batch_agreement = cosine(single[0], vectors[0])
    all_cosines = [row["cosine"] for row in similarities]
    seconds = sum(row["seconds"] for row in measurements)
    tokens = sum(row["prompt_tokens"] for row in measurements)
    passed = (
        min(all_cosines) >= args.min_cosine
        and batch_agreement >= 0.999
        and all(row["top1_matches"] for row in rankings)
    )
    report = {
        "passed": passed,
        "model": args.model,
        "base_url": args.base_url,
        "reference_revision": MODEL_REVISION,
        "samples": len(samples),
        "minimum_cosine": min(all_cosines),
        "median_cosine": statistics.median(all_cosines),
        "required_minimum_cosine": args.min_cosine,
        "single_batch_cosine": batch_agreement,
        "dimensions": DIMENSIONS,
        "prompt_tokens": tokens,
        "seconds": seconds,
        "input_tokens_per_second": tokens / seconds,
        "documents_per_second": len(samples) / seconds,
        "request_latency_p50_seconds": statistics.median(
            row["seconds"] for row in measurements
        ),
        "request_latency_p95_seconds": percentile(
            [row["seconds"] for row in measurements], 0.95
        ),
        "measurements": measurements,
        "single_measurement": single_measurement,
        "vector_parity": similarities,
        "retrieval_parity": rankings,
        "co_resident_probes": resident_probes,
    }
    write_json(args.output, report)
    print(
        json.dumps(
            {
                key: value
                for key, value in report.items()
                if key not in {"vector_parity", "measurements"}
            },
            ensure_ascii=False,
        ),
        flush=True,
    )
    if not passed:
        raise ValueError("candidate failed vector or ranking parity gate")
    return report


@contextlib.contextmanager
def llama_server(binary, model_file, port, gpu_layers, threads, log_path):
    log_path = Path(log_path)
    log_path.parent.mkdir(parents=True, exist_ok=True)
    command = [
        binary,
        "--model",
        str(model_file),
        "--host",
        "127.0.0.1",
        "--port",
        str(port),
        "--alias",
        "embeddinggemma-2",
        "-lv",
        "5",
        "--embeddings",
        "--pooling",
        "mean",
        "--ctx-size",
        "8192",
        "--batch-size",
        "8192",
        "--ubatch-size",
        "8192",
        "--parallel",
        "1",
        "--threads",
        str(threads),
        "--n-gpu-layers",
        str(gpu_layers),
        "-fit",
        "off",
    ]
    with log_path.open("w") as log:
        process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 180
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError(
                        f"llama-server exited {process.returncode}; see {log_path}"
                    )
                try:
                    request_json(f"http://127.0.0.1:{port}/health", timeout=2)
                    break
                except (OSError, urllib.error.HTTPError):
                    time.sleep(0.5)
            else:
                raise TimeoutError("llama-server startup exceeded 180 seconds")
            offload = None
            if gpu_layers > 0:
                offload = require_gpu_offload(log_path.read_text())
            yield offload
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


def require_gpu_offload(log_text):
    match = re.search(r"offloaded (\d+)/(\d+) layers to GPU", log_text)
    if not match or int(match[1]) == 0 or match[1] != match[2]:
        raise ValueError(
            "GPU canary did not offload every model layer; CPU fallback is not qualification"
        )
    if "no usable GPU found" in log_text or "failed to initialize ROCm" in log_text:
        raise ValueError("GPU canary reported an unusable ROCm device")
    return match.group(0)


def canary(args):
    start = time.monotonic()
    log_path = Path(args.output).with_suffix(".server.log")
    with llama_server(
        args.llama_binary,
        args.model_file,
        args.port,
        args.gpu_layers,
        args.threads,
        log_path,
    ) as offload:
        startup = time.monotonic() - start
        args.base_url = f"http://127.0.0.1:{args.port}"
        report = compare(args)
        report.update(
            {
                "startup_seconds": startup,
                "gpu_offload": offload,
                "model_file": args.model_file,
                "server_log": str(log_path),
            }
        )
        write_json(args.output, report)


def stop_process_group(process):
    """Give the isolated manager and every descendant a shared 20-second grace."""
    deadline = time.monotonic() + 20
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        process.wait(timeout=5)
        return
    try:
        process.wait(timeout=20)
    except subprocess.TimeoutExpired:
        pass
    while time.monotonic() < deadline:
        try:
            os.killpg(process.pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.1)
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait(timeout=5)


@contextlib.contextmanager
def runtime_manager(binary, log_path):
    command = [
        binary,
        "--listen",
        "127.0.0.1:18088",
        "--multi-model",
        "--gpu-vendor",
        "amd",
        "--gpu-arch",
        "gfx906",
        "--shutdown-timeout",
        "20s",
    ]
    with Path(log_path).open("w") as log:
        process = subprocess.Popen(
            command, stdout=log, stderr=subprocess.STDOUT, start_new_session=True
        )
        try:
            yield process, command
        finally:
            stop_process_group(process)


def wait_runtime_model(process, base_url, name=None, timeout=180):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"isolated runtime exited {process.returncode}")
        try:
            snapshot = request_json(base_url + "/api/v1/models", timeout=2)
        except (OSError, urllib.error.HTTPError):
            time.sleep(0.5)
            continue
        models = snapshot.get("models")
        if not isinstance(models, list) or any(
            not isinstance(row, dict) for row in models
        ):
            raise ValueError("runtime models response must contain a list of models")
        for row in models:
            if row.get("state") == "Failed":
                raise RuntimeError(
                    f"runtime model {row.get('name')} failed: {row.get('error')}"
                )
        if name is None or any(
            row.get("name") == name and row.get("state") == "Ready" for row in models
        ):
            return snapshot
        time.sleep(0.5)
    raise TimeoutError(
        f"runtime model {name or 'API'} readiness exceeded {timeout} seconds"
    )


def runtime_load_specs(args):
    common = {
        "nGPULayers": 999,
        # flashAttention stays unset (llama.cpp auto), matching the deployed lanes.
        "fitOff": True,
        "threads": 4,
        "embedding": True,
        "parallel": 1,
        "startupTimeoutSeconds": 180,
        "vramEstimateMB": 3000,
    }
    model_directory = (
        Path(args.model_directory)
        if args.model_directory
        else Path(args.artifacts) / "gguf"
    )
    definitions = [
        (
            "bge-large-radeonvii",
            "BAAI/bge-large-en-v1.5",
            args.bge_model,
            {
                "port": 18082,
                "contextSize": 512,
                "batchSize": 2048,
                "ubatchSize": 512,
                "vramEstimateMB": 1500,
            },
        ),
        (
            "gte-qwen2-1p5b-radeonvii",
            "Alibaba-NLP/gte-Qwen2-1.5B-instruct",
            args.gte_model,
            {
                "port": 18083,
                "contextSize": 1024,
                "batchSize": 2048,
                "ubatchSize": 512,
                "pooling": "last",
                "vramEstimateMB": 2600,
            },
        ),
    ]
    for quant in ("BF16", "Q8_0"):
        definitions.append(
            (
                "embeddinggemma-2",
                MODEL_ID,
                str(model_directory / f"embeddinggemma-2-{quant}.gguf"),
                {
                    "port": 18081,
                    "contextSize": 8192,
                    "batchSize": 8192,
                    "ubatchSize": 8192,
                    "pooling": "mean",
                },
            )
        )
    return [
        (
            name,
            {
                "backend": "llamacpp",
                "model": model,
                "modelPath": path,
                "config": {**common, **config},
                "env": [{"name": "LLAMA_ARG_LOG_VERBOSITY", "value": "5"}],
            },
        )
        for name, model, path, config in definitions
    ]


def load_runtime_model(process, base_url, name, payload, runtime_log, proof_path):
    offset = Path(runtime_log).stat().st_size
    start = time.monotonic()
    model_url = base_url + "/api/v1/models/" + urllib.parse.quote(name, safe="")
    request_json(model_url + "/load", payload, timeout=10)
    snapshot = wait_runtime_model(process, base_url, name)
    model = next(row for row in snapshot["models"] if row.get("name") == name)
    if (
        model.get("port") != payload["config"]["port"]
        or type(model.get("pid")) is not int
        or model["pid"] <= 0
    ):
        raise ValueError(
            f"runtime model {name} has unexpected port or missing process PID"
        )
    with Path(runtime_log).open("rb") as log:
        log.seek(offset)
        segment = log.read().decode(errors="replace")
    # Concurrent residents also emit logs: only this new model's lines count.
    attributed = "\n".join(
        line
        for line in segment.splitlines()
        if re.search(r'"model"\s*:\s*"' + re.escape(name) + '"', line)
    )
    Path(proof_path).write_text(attributed + "\n")
    offload = require_gpu_offload(attributed)
    return {
        "name": name,
        "pid": model["pid"],
        "port": model["port"],
        "model_file": payload["modelPath"],
        "gpu_offload": offload,
        "startup_seconds": time.monotonic() - start,
        "log_file": str(proof_path),
        "snapshot": snapshot,
    }


def isolated_runtime(args):
    """Qualify a fresh runtime without modifying the fleet's manager or models."""
    root = Path(args.output_dir)
    root.mkdir(parents=True, exist_ok=True)
    specs = runtime_load_specs(args)
    for path in [args.reference] + [payload["modelPath"] for _, payload in specs]:
        if not Path(path).is_file():
            raise FileNotFoundError(f"qualification artifact is missing: {path}")
    base_url = "http://127.0.0.1:18088"
    runtime_log = root / "runtime.log"
    summary = {
        "passed": False,
        "reference": args.reference,
        "runtime_log": str(runtime_log),
        "model_loads": [],
        "results": [],
        "runtime_snapshots": [],
    }
    try:
        with runtime_manager(args.runtime_binary, runtime_log) as (process, command):
            summary.update({"runtime_pid": process.pid, "runtime_command": command})
            write_json(root / "runtime-summary.json", summary)
            wait_runtime_model(process, base_url)
            for name, payload in specs[:2]:
                proof = load_runtime_model(
                    process,
                    base_url,
                    name,
                    payload,
                    runtime_log,
                    root / f"{name}.load.log",
                )
                summary["model_loads"].append(proof)
                write_json(root / "runtime-summary.json", summary)
            for index, quant in enumerate(("BF16", "Q8_0")):
                name, payload = specs[index + 2]
                proof = load_runtime_model(
                    process,
                    base_url,
                    name,
                    payload,
                    runtime_log,
                    root / f"embeddinggemma-2-{quant}.load.log",
                )
                summary["model_loads"].append(proof)
                report_path = root / f"runtime-{quant}.json"
                candidate = argparse.Namespace(
                    reference=args.reference,
                    base_url="http://127.0.0.1:18081",
                    model="embeddinggemma-2",
                    batch_size=args.batch_size,
                    bearer_env=None,
                    min_cosine=0.99 if quant == "BF16" else 0.98,
                    output=str(report_path),
                    co_resident=[
                        "bge-large-radeonvii=http://127.0.0.1:18082,1024",
                        "gte-qwen2-1p5b-radeonvii=http://127.0.0.1:18083,1536",
                    ],
                )
                try:
                    report = compare(candidate)
                except Exception:
                    if report_path.is_file():
                        summary["results"].append(json.loads(report_path.read_text()))
                    raise
                snapshot = wait_runtime_model(process, base_url)
                summary["runtime_snapshots"].append({"quantization": quant, **snapshot})
                if any(
                    not any(
                        row.get("name") == required and row.get("state") == "Ready"
                        for row in snapshot["models"]
                    )
                    for required in (specs[0][0], specs[1][0], name)
                ):
                    raise ValueError(
                        "all three runtime models must remain Ready after qualification"
                    )
                for initial in summary["model_loads"][:2]:
                    resident = next(
                        row
                        for row in snapshot["models"]
                        if row.get("name") == initial["name"]
                    )
                    if resident.get("pid") != initial["pid"]:
                        raise ValueError(
                            f"co-resident {initial['name']} process PID changed during qualification: {initial['pid']} -> {resident.get('pid')}"
                        )
                report.update(
                    {
                        "runtime_pid": process.pid,
                        "gpu_offload": proof["gpu_offload"],
                        "runtime_snapshot": snapshot,
                        "quantization": quant,
                    }
                )
                write_json(report_path, report)
                summary["results"].append(report)
                write_json(root / "runtime-summary.json", summary)
                if quant == "BF16":
                    request_json(
                        base_url + "/api/v1/models/embeddinggemma-2",
                        timeout=25,
                        method="DELETE",
                    )
                    snapshot = wait_runtime_model(process, base_url)
                    if any(row.get("name") == name for row in snapshot["models"]):
                        raise ValueError(
                            "BF16 model remains loaded before Q8 qualification"
                        )
            summary["passed"] = all(report["passed"] for report in summary["results"])
    except Exception as error:
        summary["error"] = str(error)
        summary["passed"] = False
        raise
    finally:
        write_json(root / "runtime-summary.json", summary)
    return summary


def reference(args):
    from huggingface_hub import hf_hub_download, snapshot_download
    import torch
    from sentence_transformers import SentenceTransformer

    torch.set_num_threads(args.threads)
    torch.manual_seed(0)
    root = Path(args.artifacts)
    root.mkdir(parents=True, exist_ok=True)
    source = snapshot_download(
        MODEL_ID, revision=MODEL_REVISION, local_dir=root / "reference-model"
    )
    model = SentenceTransformer(
        source,
        device="cpu",
        model_kwargs={"torch_dtype": torch.float32},
        config_kwargs={"vision_config": None, "audio_config": None},
    )
    if any(parameter.dtype != torch.float32 for parameter in model.parameters()):
        raise ValueError("reference contains non-FP32 parameters")
    samples = corpus()
    model.max_seq_length = 8192
    long_text = (
        "title: Context qualification | text: "
        + "Model deployment uses local caches, safe precision, bidirectional attention, and normalized document embeddings. "
        * 1200
    )
    long_ids = model.tokenizer(long_text)["input_ids"]
    for budget in (4096, 8192):
        text = model.tokenizer.decode(
            long_ids[1 : budget - 8], skip_special_tokens=True
        )
        while len(model.tokenizer(text)["input_ids"]) > budget:
            text = text.rsplit(" ", 1)[0]
        samples.append({"name": f"context-{budget}", "text": text})
    vectors = model.encode(
        [sample["text"] for sample in samples],
        batch_size=1,
        normalize_embeddings=True,
        show_progress_bar=False,
    )
    for sample, vector in zip(samples, vectors):
        sample["vector"] = vector.tolist()
        validate_vector(sample["vector"])
        sample["tokens"] = len(model.tokenize([sample["text"]])["input_ids"][0])
    reference_path = root / "reference.json"
    write_json(
        reference_path,
        {
            "model_id": MODEL_ID,
            "revision": MODEL_REVISION,
            "dtype": "float32",
            "torch": torch.__version__,
            "parameters": sum(p.numel() for p in model.parameters()),
            "dimensions": DIMENSIONS,
            "max_seq_length": model.max_seq_length,
            "samples": samples,
        },
    )
    del model, vectors
    reports = []
    for quant, threshold in [("BF16", 0.99), ("Q8_0", 0.98)]:
        gguf = hf_hub_download(
            GGUF_ID,
            f"embeddinggemma-2-{quant}.gguf",
            revision=GGUF_REVISION,
            local_dir=root / "gguf",
        )
        with llama_server(
            args.llama_binary,
            gguf,
            args.port,
            0,
            args.threads,
            root / f"cpu-{quant}.log",
        ):
            candidate = argparse.Namespace(
                reference=str(reference_path),
                base_url=f"http://127.0.0.1:{args.port}",
                model="embeddinggemma-2",
                batch_size=4,
                bearer_env=None,
                min_cosine=threshold,
                output=str(root / f"cpu-{quant}.json"),
            )
            reports.append(compare(candidate))
    write_json(
        root / "cpu-summary.json",
        {"passed": all(report["passed"] for report in reports), "results": reports},
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    ref = commands.add_parser("reference")
    ref.add_argument("--artifacts", required=True)
    ref.add_argument("--llama-binary", default="/app/llama-server")
    ref.add_argument("--port", type=int, default=18080)
    ref.add_argument("--threads", type=int, default=4)
    check = commands.add_parser("compare")
    check.add_argument("--reference", required=True)
    check.add_argument("--base-url", required=True)
    check.add_argument("--model", default="embeddinggemma-2")
    check.add_argument("--batch-size", type=int, default=4)
    check.add_argument("--min-cosine", type=float, default=0.99)
    check.add_argument("--bearer-env")
    check.add_argument("--output", required=True)
    check.add_argument(
        "--co-resident", action="append", default=[], metavar="MODEL=URL,DIMENSIONS"
    )
    gpu = commands.add_parser(
        "canary", help="Launch and qualify a CPU/GPU llama.cpp process"
    )
    gpu.add_argument("--reference", required=True)
    gpu.add_argument("--model-file", required=True)
    gpu.add_argument("--llama-binary", default="/opt/llamacpp/bin/llama-server")
    gpu.add_argument("--gpu-layers", type=int, default=999)
    gpu.add_argument("--threads", type=int, default=4)
    gpu.add_argument("--port", type=int, default=18081)
    gpu.add_argument("--model", default="embeddinggemma-2")
    gpu.add_argument("--batch-size", type=int, default=4)
    gpu.add_argument("--min-cosine", type=float, default=0.99)
    gpu.add_argument("--bearer-env")
    gpu.add_argument(
        "--co-resident", action="append", default=[], metavar="MODEL=URL,DIMENSIONS"
    )
    gpu.add_argument("--output", required=True)
    runtime = commands.add_parser(
        "isolated-runtime",
        help="Qualify an isolated Radeon VII runtime with BGE/GTE residents",
    )
    runtime.add_argument("--reference", required=True)
    runtime.add_argument("--artifacts", required=True)
    runtime.add_argument(
        "--model-directory", help="GGUF directory (default: ARTIFACTS/gguf)"
    )
    runtime.add_argument("--output-dir", required=True)
    runtime.add_argument("--runtime-binary", default="/usr/local/bin/flexinfer-runtime")
    runtime.add_argument("--batch-size", type=int, default=4)
    runtime.add_argument(
        "--bge-model",
        default="/models/flexinfer-system/bge-large-radeonvii/bge-large-en-v1.5-q8_0.gguf",
    )
    runtime.add_argument(
        "--gte-model",
        default="/models/flexinfer-system/gte-qwen2-1p5b-radeonvii/gte-Qwen2-1.5B-instruct-Q8_0.gguf",
    )
    args = parser.parse_args()
    if getattr(args, "batch_size", 1) < 1:
        parser.error("batch size must be positive")
    try:
        {
            "reference": reference,
            "compare": compare,
            "canary": canary,
            "isolated-runtime": isolated_runtime,
        }[args.command](args)
    except Exception as error:
        print(f"FAIL: {error}", file=sys.stderr, flush=True)
        traceback.print_exc()
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

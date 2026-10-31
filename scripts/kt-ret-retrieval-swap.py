#!/usr/bin/env python3
"""kt-ret-retrieval-swap.py — KT-RET, the D6 retrieval-plane kill-test.

Spec: `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md`
§6.6 and brief D6; matrix row `gfx906-retrieval-swap-safety-<date>` in
`.loom/60-validation-matrix.md`, whose pass rule is written before any run.

`run` drives embedding traffic at the Radeon VII gte lane while it forces
Humanizer swaps on the same multi-model runtime, then optionally holds the
stream while the operator parks the lane. Three request streams run at once:

  interactive  open-loop Poisson singles through flexinfer-proxy (the
               production shape: ~1 input of a few tokens, ~1.3 req/s);
  batch        open-loop 64-input batches through flexinfer-proxy (the
               jobsearch backfill shape);
  service      open-loop singles straight at the lane Service, which times
               the engine without the proxy.

A poller reads the runtime's `/api/v1/models` every second and records each
change of model set, state or drain flag, so client errors can be matched to
swap events. Phases: `baseline` (no swaps), `swap` (N Humanizer bursts spaced
evenly; each waits for the previous Humanizer to unload), `park` (the
operator parks the gte lane at the printed mark; the proxy should reroute to
the CPU twin). Every request is one JSON line in `requests.jsonl`, every
runtime change one line in `runtime.jsonl`, and `summary.json` holds the
per-phase, per-stream verdict inputs.

`capacity` sweeps one lane at fixed concurrency levels with closed-loop
clients and reports throughput and latency per level (the CPU-twin capacity
curve).

Run it on a cluster node (Service and pod IPs are routable there, cluster
DNS is not): `python3 -I kt-ret-retrieval-swap.py run --proxy
http://10.43.49.107 --lane http://10.43.153.107:8000 --runtime
http://10.42.8.233:8080 --out kt-ret-r`.
"""

from __future__ import annotations

import argparse
import json
import os
import random
import sys
import threading
import time
import urllib.error
import urllib.request

GTE = "gte-qwen2-1p5b-radeonvii"
TWIN = "gte-qwen2-1p5b-cpu-5930k"
HUMANIZER = "humanizer"

WORDS = (
    "senior platform engineer kubernetes golang python data pipeline remote "
    "hybrid healthcare integration fhir hl7 startup series compensation equity "
    "benefits team lead architecture observability reliability incident "
    "response cloud terraform postgres kafka streaming inference gpu agent "
    "context decision finding memory session retrieval index"
).split()

HUMANIZER_PROMPT = (
    "Rewrite this so it reads naturally: The quarterly platform review found "
    "that the retrieval plane served every request, that latency stayed flat, "
    "and that the team should keep the current layout for another quarter."
)


def text(words: int, rng: random.Random) -> str:
    # The trailing nonce keeps every input distinct; gte has no prefix cache
    # on embeddings, but identical inputs would still flatter the batch.
    return " ".join(rng.choice(WORDS) for _ in range(words)) + f" {rng.random():.6f}"


def percentile(values: list[float], q: float) -> float | None:
    """Nearest-rank percentile, q in [0, 100]; None for no values."""
    if not values:
        return None
    ordered = sorted(values)
    rank = max(1, min(len(ordered), int(-(-q * len(ordered) // 100))))
    return ordered[rank - 1]


def classify(status: int | None, err: str | None) -> str:
    """Client-visible outcome of one request."""
    if err == "timeout":
        return "timeout"
    if err is not None:
        return "conn_error"
    if status is None:
        return "conn_error"
    if 200 <= status < 300:
        return "ok"
    if status >= 500:
        return "5xx"
    return "4xx"


def post(url: str, body: dict, timeout: float) -> dict:
    """POST JSON; never raises. Returns status, error class, latency, headers."""
    data = json.dumps(body).encode()
    req = urllib.request.Request(
        url, data=data, headers={"Content-Type": "application/json"}
    )
    start = time.time()
    t0 = time.perf_counter()
    out = {"t": start, "status": None, "err": None, "headers": {}, "n": 0}
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            payload = resp.read()
            out["status"] = resp.status
            out["headers"] = {
                k.lower(): v
                for k, v in resp.headers.items()
                if k.lower().startswith("x-flexinfer")
            }
            try:
                parsed = json.loads(payload)
                out["n"] = len(parsed.get("data") or parsed.get("choices") or [])
            except ValueError:
                out["err"] = "bad_json"
    except urllib.error.HTTPError as e:
        out["status"] = e.code
        out["headers"] = {
            k.lower(): v
            for k, v in (e.headers or {}).items()
            if k.lower().startswith("x-flexinfer")
        }
        try:
            out["body"] = e.read()[:300].decode("utf-8", "replace")
        except Exception:  # noqa: BLE001 - best-effort error body
            pass
    except TimeoutError:
        out["err"] = "timeout"
    except urllib.error.URLError as e:
        reason = getattr(e, "reason", e)
        out["err"] = (
            "timeout" if isinstance(reason, TimeoutError) else type(reason).__name__
        )
    except (ConnectionError, OSError) as e:
        out["err"] = type(e).__name__
    out["latency_ms"] = round((time.perf_counter() - t0) * 1000, 1)
    out["outcome"] = classify(out["status"], out["err"])
    return out


def get_json(url: str, timeout: float = 3.0):
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            return json.loads(resp.read())
    except Exception:  # noqa: BLE001 - the poller records gaps, never dies
        return None


class Recorder:
    def __init__(self, out_dir: str):
        os.makedirs(out_dir, exist_ok=True)
        self.lock = threading.Lock()
        self.requests = open(os.path.join(out_dir, "requests.jsonl"), "a", buffering=1)
        self.runtime = open(os.path.join(out_dir, "runtime.jsonl"), "a", buffering=1)
        self.rows: list[dict] = []
        self.events: list[dict] = []
        self.phase = "setup"

    def request(self, row: dict) -> None:
        row["phase"] = self.phase
        with self.lock:
            self.rows.append(row)
            self.requests.write(json.dumps(row) + "\n")

    def event(self, row: dict) -> None:
        row.setdefault("phase", self.phase)
        with self.lock:
            self.events.append(row)
            self.runtime.write(json.dumps(row) + "\n")
        print(
            f"[{time.strftime('%H:%M:%S')}] {row['kind']} {row.get('model', '')} {row.get('detail', '')}",
            flush=True,
        )


def runtime_snapshot(models) -> dict[str, tuple[str, bool]]:
    if not isinstance(models, list):
        models = (models or {}).get("models") or []
    snap = {}
    for m in models:
        snap[m.get("name", "?")] = (m.get("state", "?"), bool(m.get("draining")))
    return snap


def poll_runtime(base: str, rec: Recorder, stop: threading.Event) -> None:
    prev: dict[str, tuple[str, bool]] | None = None
    while not stop.is_set():
        models = get_json(base.rstrip("/") + "/api/v1/models")
        now = time.time()
        if models is None:
            rec.event({"t": now, "kind": "runtime_unreachable"})
        else:
            snap = runtime_snapshot(models)
            if prev is not None:
                for name in sorted(set(prev) | set(snap)):
                    before, after = prev.get(name), snap.get(name)
                    if before == after:
                        continue
                    if before is None:
                        kind = "model_added"
                    elif after is None:
                        kind = "model_removed"
                    else:
                        kind = "model_changed"
                    rec.event(
                        {
                            "t": now,
                            "kind": kind,
                            "model": name,
                            "detail": f"{before} -> {after}",
                        }
                    )
            prev = snap
        stop.wait(1.0)


def open_loop(
    name: str,
    url: str,
    make_body,
    rps: float,
    deadline: float,
    rec: Recorder,
    stop: threading.Event,
    seed: int,
) -> None:
    """Poisson arrivals at rps; each request on its own thread (open loop)."""
    rng = random.Random(seed)
    while not stop.is_set():
        stop.wait(rng.expovariate(rps))
        if stop.is_set():
            break
        body = make_body(rng)

        def fire(body=body):
            row = post(url, body, deadline)
            row["stream"] = name
            row["inputs"] = (
                len(body.get("input", [])) if isinstance(body.get("input"), list) else 1
            )
            rec.request(row)

        threading.Thread(target=fire, daemon=True).start()


def humanizer_present(base: str) -> bool:
    snap = runtime_snapshot(get_json(base.rstrip("/") + "/api/v1/models"))
    return HUMANIZER in snap


def humanizer_burst(args, rec: Recorder, index: int) -> None:
    """Fire the burst that makes the election admit Humanizer (a swap)."""
    rec.event(
        {
            "t": time.time(),
            "kind": "swap_forced",
            "model": HUMANIZER,
            "detail": f"burst {index}",
        }
    )
    threads = []
    for k in range(args.humanizer_burst):

        def fire(k=k):
            body = {
                "model": HUMANIZER,
                "max_tokens": args.humanizer_max_tokens,
                "messages": [
                    {"role": "user", "content": f"{HUMANIZER_PROMPT} ({index}.{k})"}
                ],
            }
            row = post(
                args.proxy.rstrip("/") + "/v1/chat/completions",
                body,
                args.humanizer_deadline,
            )
            row["stream"] = "humanizer"
            row["swap"] = index
            rec.request(row)

        t = threading.Thread(target=fire, daemon=True)
        t.start()
        threads.append(t)
    for t in threads:
        t.join()


def wait_humanizer_gone(args, rec: Recorder, limit: float) -> bool:
    end = time.time() + limit
    while time.time() < end:
        if not humanizer_present(args.runtime):
            return True
        time.sleep(2)
    rec.event(
        {
            "t": time.time(),
            "kind": "humanizer_still_loaded",
            "detail": f"after {limit:.0f}s",
        }
    )
    return False


def summarize(rows: list[dict], events: list[dict], window_s: float) -> dict:
    """Per phase and stream: counts by outcome, latency percentiles of
    successful requests, replay and reroute counts, and the failures that
    fall within window_s of a runtime change of the gte lane or Humanizer
    (a swap event)."""
    swap_times = [
        e["t"]
        for e in events
        if e.get("kind")
        in ("model_added", "model_removed", "model_changed", "swap_forced")
    ]
    out: dict = {}
    for row in rows:
        phase = out.setdefault(row.get("phase", "?"), {})
        s = phase.setdefault(
            row.get("stream", "?"),
            {
                "requests": 0,
                "ok": 0,
                "5xx": 0,
                "4xx": 0,
                "conn_error": 0,
                "timeout": 0,
                "replayed": 0,
                "rerouted": 0,
                "failures_near_swap": 0,
                "_lat": [],
                "_fail": [],
            },
        )
        s["requests"] += 1
        s[row["outcome"]] += 1
        headers = row.get("headers") or {}
        if "x-flexinfer-replayed" in headers:
            s["replayed"] += 1
        if "x-flexinfer-unavailable-reroute" in headers:
            s["rerouted"] += 1
        if row["outcome"] == "ok":
            s["_lat"].append(row["latency_ms"])
        elif row["outcome"] in ("5xx", "conn_error", "timeout"):
            end = row["t"] + row["latency_ms"] / 1000
            near = any(row["t"] - window_s <= t <= end + window_s for t in swap_times)
            s["failures_near_swap"] += int(near)
            s["_fail"].append(
                {
                    "t": row["t"],
                    "status": row.get("status"),
                    "err": row.get("err"),
                    "latency_ms": row["latency_ms"],
                    "near_swap": near,
                    "body": (row.get("body") or "")[:120],
                }
            )
    for phase in out.values():
        for s in phase.values():
            lat = s.pop("_lat")
            s["p50_ms"] = percentile(lat, 50)
            s["p95_ms"] = percentile(lat, 95)
            s["p99_ms"] = percentile(lat, 99)
            s["max_ms"] = max(lat) if lat else None
            s["failures"] = s.pop("_fail")[:50]
    return out


def verdict(summary: dict, p99_factor: float) -> dict:
    """The reproduction outcome and the KT-RET swap-phase checks."""
    swap = summary.get("swap", {})
    base = summary.get("baseline", {})
    gte_streams = ("interactive", "batch", "service")
    kills = sum(swap.get(s, {}).get("failures_near_swap", 0) for s in gte_streams)
    client_5xx = sum(
        swap.get(s, {}).get("5xx", 0) + swap.get(s, {}).get("conn_error", 0)
        for s in ("interactive", "batch")
    )
    p99 = {}
    for s in gte_streams:
        b, w = base.get(s, {}).get("p99_ms"), swap.get(s, {}).get("p99_ms")
        p99[s] = {
            "baseline": b,
            "swap": w,
            "ok": None if b is None or w is None else w <= p99_factor * b,
        }
    return {
        "kills_reproduced": kills > 0,
        "gte_failures_near_swap": kills,
        "proxy_5xx_or_conn_errors_in_swap": client_5xx,
        "p99": p99,
    }


def cmd_run(args) -> int:
    rec = Recorder(args.out)
    stop_streams = threading.Event()
    stop_poll = threading.Event()
    proxy = args.proxy.rstrip("/") + "/v1/embeddings"
    lane = args.lane.rstrip("/") + "/v1/embeddings"

    def single(rng):
        return {"model": args.model, "input": [text(args.interactive_words, rng)]}

    def batch(rng):
        return {
            "model": args.model,
            "input": [text(args.batch_words, rng) for _ in range(args.batch_inputs)],
        }

    meta = {k: v for k, v in vars(args).items() if k != "func"}
    meta["started"] = time.time()
    with open(os.path.join(args.out, "meta.json"), "w") as fh:
        json.dump(meta, fh, indent=2)

    poller = threading.Thread(
        target=poll_runtime, args=(args.runtime, rec, stop_poll), daemon=True
    )
    poller.start()
    streams = [
        ("interactive", proxy, single, args.interactive_rps, 11),
        (
            "batch",
            proxy,
            batch,
            1.0 / args.batch_period if args.batch_period > 0 else 0,
            12,
        ),
        ("service", lane, single, args.service_rps, 13),
    ]
    for name, url, make, rps, seed in streams:
        if rps > 0:
            threading.Thread(
                target=open_loop,
                args=(name, url, make, rps, args.deadline, rec, stop_streams, seed),
                daemon=True,
            ).start()

    try:
        rec.phase = "baseline"
        rec.event(
            {
                "t": time.time(),
                "kind": "phase",
                "detail": f"baseline {args.baseline_min} min",
            }
        )
        time.sleep(args.baseline_min * 60)

        rec.phase = "swap"
        rec.event(
            {
                "t": time.time(),
                "kind": "phase",
                "detail": f"swap {args.swap_min} min, {args.swaps} swaps",
            }
        )
        swap_end = time.time() + args.swap_min * 60
        period = args.swap_min * 60 / max(args.swaps, 1)
        for i in range(args.swaps):
            due = time.time() + period
            if humanizer_present(args.runtime):
                wait_humanizer_gone(args, rec, args.humanizer_unload_wait)
            humanizer_burst(args, rec, i + 1)
            time.sleep(max(0, min(due, swap_end) - time.time()))
        time.sleep(max(0, swap_end - time.time()))

        if args.park_minutes > 0:
            rec.phase = "park"
            rec.event(
                {
                    "t": time.time(),
                    "kind": "phase",
                    "detail": f"park {args.park_minutes} min: PARK {args.model} NOW",
                }
            )
            time.sleep(args.park_minutes * 60)
            rec.event(
                {
                    "t": time.time(),
                    "kind": "phase",
                    "detail": f"park over: UNPARK {args.model} NOW",
                }
            )
    except KeyboardInterrupt:
        rec.event({"t": time.time(), "kind": "interrupted"})
    finally:
        stop_streams.set()
        rec.phase = "drain"
        time.sleep(min(args.deadline, 60) + 2)
        stop_poll.set()

    summary = summarize(rec.rows, rec.events, args.window)
    result = {
        "summary": summary,
        "verdict": verdict(summary, args.p99_factor),
        "swap_events": [e for e in rec.events if e["kind"] != "runtime_unreachable"],
    }
    with open(os.path.join(args.out, "summary.json"), "w") as fh:
        json.dump(result, fh, indent=2)
    print(json.dumps(result["verdict"], indent=2))
    for phase, streams_ in summary.items():
        for name, s in streams_.items():
            print(
                f"{phase:9s} {name:12s} n={s['requests']:5d} ok={s['ok']:5d} 5xx={s['5xx']} conn={s['conn_error']} "
                f"timeout={s['timeout']} 4xx={s['4xx']} replayed={s['replayed']} rerouted={s['rerouted']} "
                f"p50={s['p50_ms']} p95={s['p95_ms']} p99={s['p99_ms']} max={s['max_ms']} near_swap={s['failures_near_swap']}"
            )
    return 0


def cmd_capacity(args) -> int:
    url = args.url.rstrip("/") + "/v1/embeddings"
    levels = []
    for conc in args.concurrency:
        lat: list[float] = []
        errors = 0
        lock = threading.Lock()
        end = time.time() + args.seconds

        def client(seed: int) -> None:
            nonlocal errors
            rng = random.Random(seed)
            while time.time() < end:
                body = {
                    "model": args.model,
                    "input": [text(args.words, rng) for _ in range(args.inputs)],
                }
                row = post(url, body, args.deadline)
                with lock:
                    if row["outcome"] == "ok":
                        lat.append(row["latency_ms"])
                    else:
                        errors += 1

        threads = [
            threading.Thread(target=client, args=(1000 + conc * 10 + k,))
            for k in range(conc)
        ]
        t0 = time.time()
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        wall = time.time() - t0
        level = {
            "concurrency": conc,
            "requests": len(lat),
            "errors": errors,
            "req_per_s": round(len(lat) / wall, 3),
            "inputs_per_s": round(len(lat) * args.inputs / wall, 3),
            "p50_ms": percentile(lat, 50),
            "p95_ms": percentile(lat, 95),
            "max_ms": max(lat) if lat else None,
        }
        levels.append(level)
        print(json.dumps(level), flush=True)
        time.sleep(args.rest)
    if args.out:
        os.makedirs(args.out, exist_ok=True)
        with open(os.path.join(args.out, "capacity.json"), "w") as fh:
            json.dump(
                {
                    "url": args.url,
                    "model": args.model,
                    "inputs": args.inputs,
                    "words": args.words,
                    "levels": levels,
                },
                fh,
                indent=2,
            )
    return 0


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = ap.add_subparsers(dest="cmd", required=True)

    run = sub.add_parser(
        "run", help="swap replay (and optional park) against the gte lane"
    )
    run.add_argument("--proxy", required=True, help="flexinfer-proxy base URL")
    run.add_argument("--lane", required=True, help="gte lane Service base URL")
    run.add_argument(
        "--runtime", required=True, help="gfx906 runtime API base URL (pod IP :8080)"
    )
    run.add_argument("--model", default=GTE)
    run.add_argument("--out", required=True)
    run.add_argument("--baseline-min", type=float, default=3)
    run.add_argument("--swap-min", type=float, default=10)
    run.add_argument("--swaps", type=int, default=6)
    run.add_argument("--park-minutes", type=float, default=0)
    run.add_argument("--interactive-rps", type=float, default=2.0)
    run.add_argument("--interactive-words", type=int, default=6)
    run.add_argument(
        "--batch-period",
        type=float,
        default=10.0,
        help="seconds between 64-input batches (0 = off)",
    )
    run.add_argument("--batch-inputs", type=int, default=64)
    run.add_argument("--batch-words", type=int, default=12)
    run.add_argument("--service-rps", type=float, default=0.5)
    run.add_argument(
        "--deadline",
        type=float,
        default=30.0,
        help="client timeout per embedding request",
    )
    run.add_argument("--humanizer-burst", type=int, default=2)
    run.add_argument("--humanizer-max-tokens", type=int, default=128)
    run.add_argument("--humanizer-deadline", type=float, default=600.0)
    run.add_argument("--humanizer-unload-wait", type=float, default=150.0)
    run.add_argument(
        "--window",
        type=float,
        default=15.0,
        help="seconds around a swap event that count as coinciding",
    )
    run.add_argument("--p99-factor", type=float, default=2.0)
    run.set_defaults(func=cmd_run)

    cap = sub.add_parser("capacity", help="closed-loop capacity curve of one lane")
    cap.add_argument("--url", required=True)
    cap.add_argument("--model", default=TWIN)
    cap.add_argument("--concurrency", type=int, nargs="+", default=[1, 2, 4, 6, 8])
    cap.add_argument("--seconds", type=float, default=45)
    cap.add_argument("--inputs", type=int, default=1)
    cap.add_argument("--words", type=int, default=6)
    cap.add_argument("--deadline", type=float, default=60)
    cap.add_argument("--rest", type=float, default=5)
    cap.add_argument("--out")
    cap.set_defaults(func=cmd_capacity)

    args = ap.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

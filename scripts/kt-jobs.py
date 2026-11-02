#!/usr/bin/env python3
"""KT-JOBS: can async jobs take the flex card from the warm lane only while they run?

Kill-test for D2 (.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md
§6.2, matrix row proxy-async-jobs-<date>). One run, through flexinfer-proxy:

  1. baseline: --baseline-min minutes of the chat replay alone on the warm lane
     (--chat-rpm streaming requests a minute, nonce prompts, short answers);
  2. jobs: while the replay continues, submit --image-jobs background image
     jobs and --video-jobs background video jobs with a webhook to this
     script, poll them every 5 s, and poll the warm lane's descriptor
     (GET /v1/models/{name}) every 5 s to see the election flip and return;
  3. cancel: once those jobs are terminal, submit one more image job, cancel
     it as soon as it is in_progress, and time how long the warm lane takes
     to be Ready again;
  4. tail: keep the replay up --tail-min minutes after the last job.

Every HTTP call is timed (no client connection may be held > 30 s), every
status is counted (504/524 must be 0) and every 503 body is checked for the
typed envelope. A Prefer: wait=0 probe against the video lane while it is cold
shows the code Mills will read instead of the body string.

    python3 scripts/kt-jobs.py --proxy http://flexinfer-proxy.flexinfer-system.svc \
        --warm-model qwen38-27b-xtx-warm --image-model qwen-image-21-gfx1100 \
        --video-model wan21-t2v-1p3b-gfx1100 \
        --webhook-url http://<this-host>:8765/hook --webhook-port 8765 \
        --cooldown 300 --out kt-jobs.json
    python3 scripts/kt-jobs.py --verdict kt-jobs.json

Stdlib only.
"""

import argparse
import base64
import hashlib
import hmac
import json
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PASS_MAX_CONN_S = 30.0  # no client connection held longer than 30 s
PASS_TTFT_DELTA = 0.20  # chat p95 TTFT within ±20 % of baseline until the flip
PASS_WARM_BACK_S = 300.0  # warm lane Ready <= 5 min after the last job
PASS_WEBHOOK_S = 10.0  # >= 3 of 4 webhooks within 10 s of completion
PASS_WEBHOOKS_MIN = 3

IMAGE_PROMPTS = [
    "a lighthouse on a basalt cliff at dusk, long exposure, crisp detail",
    "a red fox curled in fresh snow under birch trees, soft morning light",
    "an isometric cutaway of a tiny bakery, warm colors, clean linework",
    "a koi pond seen from above with lily pads and falling maple leaves",
]
VIDEO_PROMPT = "a paper boat drifting down a rain gutter, shallow depth of field"

lock = threading.Lock()
calls = []  # every HTTP call: {t, kind, status, seconds, code}
envelope_misses = []  # 503s without error.flexinfer


def now():
    return time.time()


def record(kind, status, seconds, code=None):
    with lock:
        calls.append(
            {
                "t": now(),
                "kind": kind,
                "status": status,
                "seconds": round(seconds, 3),
                "code": code,
            }
        )


def http(method, url, body=None, headers=None, timeout=60, kind="api"):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    t0 = now()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            record(kind, resp.status, now() - t0)
            return resp.status, dict(resp.headers), raw
    except urllib.error.HTTPError as e:
        raw = e.read()
        code = None
        try:
            err = json.loads(raw).get("error", {})
            code = err.get("code")
            if e.code == 503 and "flexinfer" not in err:
                envelope_misses.append(
                    {"kind": kind, "body": raw[:300].decode(errors="replace")}
                )
        except (ValueError, AttributeError):
            if e.code == 503:
                envelope_misses.append(
                    {"kind": kind, "body": raw[:300].decode(errors="replace")}
                )
        record(kind, e.code, now() - t0, code)
        return e.code, dict(e.headers), raw
    except Exception as e:  # noqa: BLE001 - every transport failure is evidence
        record(kind, 0, now() - t0, type(e).__name__)
        return 0, {}, b""


# ── chat replay ───────────────────────────────────────────────────


class Replay(threading.Thread):
    """Streams one chat request every 60/rpm seconds; records TTFT."""

    def __init__(self, proxy, model, rpm, auth):
        super().__init__(daemon=True)
        self.proxy, self.model, self.interval, self.auth = (
            proxy,
            model,
            60.0 / rpm,
            auth,
        )
        self.samples = []  # (start, ttft or None, status)
        self.stop = threading.Event()

    def one(self):
        body = {
            "model": self.model,
            "stream": True,
            "max_tokens": 48,
            "messages": [
                {
                    "role": "user",
                    "content": f"[{uuid.uuid4()}] In two sentences, why does the sea look blue?",
                }
            ],
        }
        req = urllib.request.Request(
            self.proxy + "/v1/chat/completions",
            data=json.dumps(body).encode(),
            method="POST",
        )
        req.add_header("Content-Type", "application/json")
        if self.auth:
            req.add_header("Authorization", "Bearer " + self.auth)
        t0 = now()
        ttft = None
        try:
            with urllib.request.urlopen(req, timeout=PASS_MAX_CONN_S * 4) as resp:
                for line in resp:
                    if (
                        ttft is None
                        and line.startswith(b"data:")
                        and b"[DONE]" not in line
                    ):
                        ttft = now() - t0
                status = resp.status
        except urllib.error.HTTPError as e:
            status = e.code
            raw = e.read()
            if e.code == 503:
                try:
                    if "flexinfer" not in json.loads(raw).get("error", {}):
                        envelope_misses.append(
                            {"kind": "chat", "body": raw[:300].decode(errors="replace")}
                        )
                except ValueError:
                    envelope_misses.append(
                        {"kind": "chat", "body": raw[:300].decode(errors="replace")}
                    )
        except Exception as e:  # noqa: BLE001
            status = 0
            print("chat error:", e, file=sys.stderr)
        record("chat", status, now() - t0)
        with lock:
            self.samples.append((t0, ttft, status))

    def run(self):
        while not self.stop.is_set():
            threading.Thread(target=self.one, daemon=True).start()
            self.stop.wait(self.interval)


# ── webhook receiver ──────────────────────────────────────────────

hooks = []  # {t, id, event, job, signature_ok}
WEBHOOK_SECRET = (
    "whsec_" + base64.b64encode(b"kt-jobs-" + uuid.uuid4().hex.encode()).decode()
)


class Hook(BaseHTTPRequestHandler):
    def do_POST(self):  # noqa: N802
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        msg_id, ts = self.headers.get("webhook-id", ""), self.headers.get(
            "webhook-timestamp", ""
        )
        key = base64.b64decode(WEBHOOK_SECRET[len("whsec_") :])
        want = (
            "v1,"
            + base64.b64encode(
                hmac.new(
                    key, f"{msg_id}.{ts}.".encode() + body, hashlib.sha256
                ).digest()
            ).decode()
        )
        try:
            event = json.loads(body)
        except ValueError:
            event = {}
        with lock:
            hooks.append(
                {
                    "t": now(),
                    "id": msg_id,
                    "event": event.get("type"),
                    "job": event.get("data", {}).get("id"),
                    "signature_ok": hmac.compare_digest(
                        want, self.headers.get("webhook-signature", "")
                    ),
                }
            )
        self.send_response(204)
        self.end_headers()

    def log_message(self, *_):
        pass


# ── jobs and lanes ────────────────────────────────────────────────


def submit(args, model, path, request):
    headers = {
        "X-FlexInfer-Webhook-Url": args.webhook_url,
        "X-FlexInfer-Webhook-Secret": WEBHOOK_SECRET,
    }
    if args.auth:
        headers["Authorization"] = "Bearer " + args.auth
    body = dict(request, model=model, background=True)
    status, _, raw = http("POST", args.proxy + path, body, headers, kind="submit")
    if status != 200:
        raise SystemExit(f"submit {model} {path}: {status} {raw[:300]!r}")
    return json.loads(raw)["id"]


def get_job(args, job_id):
    headers = {"Authorization": "Bearer " + args.auth} if args.auth else {}
    status, _, raw = http(
        "GET", f"{args.proxy}/v1/jobs/{job_id}", headers=headers, kind="poll"
    )
    return json.loads(raw) if status == 200 else None


def lane(args, model):
    status, _, raw = http("GET", f"{args.proxy}/v1/models/{model}", kind="descriptor")
    if status != 200:
        return None
    return json.loads(raw).get("flexinfer") or {}


class LaneWatch(threading.Thread):
    """Records the warm lane's phase every 5 s."""

    def __init__(self, args):
        super().__init__(daemon=True)
        self.args, self.timeline, self.stop = args, [], threading.Event()

    def run(self):
        while not self.stop.is_set():
            d = lane(self.args, self.args.warm_model) or {}
            with lock:
                self.timeline.append((now(), d.get("phase"), d.get("loading_substage")))
            self.stop.wait(5)


def wait_terminal(args, ids, deadline):
    done = {}
    while len(done) < len(ids) and now() < deadline:
        for job_id in ids:
            if job_id in done:
                continue
            j = get_job(args, job_id)
            if j and j["status"] in ("completed", "failed", "cancelled", "incomplete"):
                done[job_id] = dict(j, observed_at=now())
                print(
                    f"{job_id} {j['status']} lane={j['flexinfer'].get('lane')}",
                    file=sys.stderr,
                )
        time.sleep(5)
    return done


def fetch_content(args, job_id):
    headers = {"Authorization": "Bearer " + args.auth} if args.auth else {}
    status, hdrs, raw = http(
        "GET",
        f"{args.proxy}/v1/jobs/{job_id}/content",
        headers=headers,
        timeout=300,
        kind="content",
    )
    return {
        "status": status,
        "bytes": len(raw),
        "content_type": hdrs.get("Content-Type"),
        "sha256": hashlib.sha256(raw).hexdigest(),
    }


def warm_ready_after(timeline, t0):
    for t, phase, _ in timeline:
        if t >= t0 and phase == "Ready":
            return t - t0
    return None


def first_flip(timeline, t0):
    for t, phase, _ in timeline:
        if t >= t0 and phase != "Ready":
            return t
    return None


def run(args):
    srv = ThreadingHTTPServer(("0.0.0.0", args.webhook_port), Hook)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    watch = LaneWatch(args)
    watch.start()
    replay = Replay(args.proxy, args.warm_model, args.chat_rpm, args.auth)
    replay.start()
    out = {
        "args": {k: v for k, v in vars(args).items() if k != "auth"},
        "started_at": now(),
    }

    print(f"baseline: {args.baseline_min} min of chat replay", file=sys.stderr)
    time.sleep(args.baseline_min * 60)
    jobs_t0 = now()
    out["jobs_submitted_at"] = jobs_t0

    probe = {}
    d = lane(args, args.video_model) or {}
    if d.get("phase") != "Ready":
        status, hdrs, raw = http(
            "POST",
            args.proxy + "/v1/videos/generations",
            {"model": args.video_model, "prompt": VIDEO_PROMPT},
            {"Prefer": "wait=0"},
            kind="probe",
        )
        try:
            probe = {
                "status": status,
                "retry_after": hdrs.get("Retry-After"),
                "error": json.loads(raw).get("error"),
            }
        except ValueError:
            probe = {"status": status, "body": raw[:300].decode(errors="replace")}
    out["cold_probe"] = probe

    ids = []
    for i in range(args.image_jobs):
        ids.append(
            submit(
                args,
                args.image_model,
                "/v1/images/generations",
                {
                    "prompt": IMAGE_PROMPTS[i % len(IMAGE_PROMPTS)],
                    "size": args.image_size,
                    "n": 1,
                },
            )
        )
    for _ in range(args.video_jobs):
        ids.append(
            submit(
                args,
                args.video_model,
                "/v1/videos/generations",
                {"prompt": VIDEO_PROMPT, "num_frames": args.video_frames},
            )
        )
    out["job_ids"] = ids
    done = wait_terminal(args, ids, now() + args.jobs_timeout_min * 60)
    out["jobs"] = done
    out["contents"] = {
        i: fetch_content(args, i) for i, j in done.items() if j["status"] == "completed"
    }
    last_done = max((j["observed_at"] for j in done.values()), default=now())
    out["last_job_done_at"] = last_done

    print("cancel: one more image job", file=sys.stderr)
    cancel_id = submit(
        args,
        args.image_model,
        "/v1/images/generations",
        {"prompt": IMAGE_PROMPTS[0], "size": args.image_size},
    )
    deadline = now() + args.jobs_timeout_min * 60
    while now() < deadline:
        j = get_job(args, cancel_id)
        if j and j["status"] != "queued":
            break
        time.sleep(2)
    headers = {"Authorization": "Bearer " + args.auth} if args.auth else {}
    cancelled_at = now()
    http("POST", f"{args.proxy}/v1/jobs/{cancel_id}/cancel", {}, headers, kind="cancel")
    out["cancel"] = {
        "id": cancel_id,
        "cancelled_at": cancelled_at,
        "final": wait_terminal(args, [cancel_id], now() + 600).get(cancel_id),
    }

    print(f"tail: {args.tail_min} min", file=sys.stderr)
    time.sleep(args.tail_min * 60)
    replay.stop.set()
    watch.stop.set()
    time.sleep(5)
    srv.shutdown()
    with lock:
        out.update(
            calls=calls,
            envelope_misses=envelope_misses,
            hooks=hooks,
            chat=[{"t": t, "ttft": f, "status": s} for t, f, s in replay.samples],
            warm_timeline=watch.timeline,
        )
    out["finished_at"] = now()
    return out


# ── verdict ───────────────────────────────────────────────────────


def p95(values):
    values = sorted(v for v in values if v is not None)
    if not values:
        return None
    return values[min(len(values) - 1, int(round(0.95 * (len(values) - 1))))]


def verdict(r, cooldown):
    checks = {}
    jobs = r.get("jobs", {})
    completed = [i for i, j in jobs.items() if j["status"] == "completed"]
    contents_ok = all(
        r["contents"].get(i, {}).get("status") == 200 and r["contents"][i]["bytes"] > 0
        for i in completed
    )
    checks["all_jobs_complete_with_output"] = (
        len(completed) == len(r["job_ids"]) and contents_ok
    )
    longest = max(
        (c["seconds"] for c in r["calls"] if c["kind"] != "content"), default=0
    )
    checks["no_connection_over_30s"] = longest <= PASS_MAX_CONN_S
    gateway = [c for c in r["calls"] if c["status"] in (504, 524)]
    checks["zero_504_524"] = not gateway
    base = [s["ttft"] for s in r["chat"] if s["t"] < r["jobs_submitted_at"]]
    flip = first_flip(r["warm_timeline"], r["jobs_submitted_at"])
    window_end = flip if flip else r["last_job_done_at"]
    during = [
        s["ttft"] for s in r["chat"] if r["jobs_submitted_at"] <= s["t"] < window_end
    ]
    b95, d95 = p95(base), p95(during)
    checks["chat_ttft_p95_within_20pct_until_flip"] = (
        b95 is not None and d95 is not None and abs(d95 - b95) <= PASS_TTFT_DELTA * b95
    )
    back = warm_ready_after(r["warm_timeline"], r["last_job_done_at"])
    checks["warm_back_within_5min"] = back is not None and back <= PASS_WARM_BACK_S
    on_time = 0
    for job_id, j in jobs.items():
        arrivals = [
            h
            for h in r["hooks"]
            if h["job"] == job_id and h["event"] == "job." + j["status"]
        ]
        if (
            arrivals
            and j.get("completed_at")
            and arrivals[0]["t"] - j["completed_at"] <= PASS_WEBHOOK_S
            and arrivals[0]["signature_ok"]
        ):
            on_time += 1
    checks["webhooks_3_of_4_within_10s"] = on_time >= min(PASS_WEBHOOKS_MIN, len(jobs))
    c = r.get("cancel", {})
    freed = warm_ready_after(r["warm_timeline"], c.get("cancelled_at", 0))
    checks["cancel_frees_card_within_cooldown"] = (
        (c.get("final") or {}).get("status") == "cancelled"
        and freed is not None
        and freed <= cooldown
    )
    checks["typed_envelope_on_every_503"] = not r["envelope_misses"]
    probe = r.get("cold_probe") or {}
    checks["cold_probe_typed"] = (not probe) or (
        (probe.get("error") or {}).get("code")
        in ("model_cold_starting", "model_preempted", "model_parked", "queue_full")
    )
    detail = {
        "longest_connection_s": longest,
        "gateway_timeouts": len(gateway),
        "chat_ttft_p95_baseline_s": b95,
        "chat_ttft_p95_until_flip_s": d95,
        "flip_after_submit_s": (flip - r["jobs_submitted_at"]) if flip else None,
        "warm_back_after_last_job_s": back,
        "webhooks_on_time": on_time,
        "cancel_to_warm_ready_s": freed,
        "envelope_misses": len(r["envelope_misses"]),
        "jobs": {
            i: {
                "status": j["status"],
                "lane": j["flexinfer"].get("lane"),
                "run_s": (
                    (j["completed_at"] - j["started_at"])
                    if j.get("completed_at") and j.get("started_at")
                    else None
                ),
            }
            for i, j in jobs.items()
        },
    }
    return all(checks.values()), checks, detail


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("--proxy")
    ap.add_argument("--auth", default="", help="proxy bearer token")
    ap.add_argument("--warm-model")
    ap.add_argument("--image-model")
    ap.add_argument("--video-model")
    ap.add_argument(
        "--webhook-url", help="URL the proxy can reach this script's receiver at"
    )
    ap.add_argument("--webhook-port", type=int, default=8765)
    ap.add_argument("--chat-rpm", type=float, default=20)
    ap.add_argument("--baseline-min", type=float, default=5)
    ap.add_argument("--tail-min", type=float, default=7)
    ap.add_argument("--image-jobs", type=int, default=3)
    ap.add_argument("--video-jobs", type=int, default=1)
    ap.add_argument("--image-size", default="1024x1024")
    ap.add_argument("--video-frames", type=int, default=33)
    ap.add_argument("--jobs-timeout-min", type=float, default=60)
    ap.add_argument(
        "--cooldown", type=float, default=300, help="shared-GPU swap cooldown, seconds"
    )
    ap.add_argument("--out", default="kt-jobs.json")
    ap.add_argument("--verdict", nargs="?", const=True, metavar="RESULT_JSON")
    args = ap.parse_args()
    if isinstance(args.verdict, str):
        with open(args.verdict) as f:
            r = json.load(f)
        ok, checks, detail = verdict(r, r["args"].get("cooldown", args.cooldown))
        print(json.dumps({"pass": ok, "checks": checks, "detail": detail}, indent=2))
        sys.exit(0 if ok else 1)
    for need in ("proxy", "warm_model", "image_model", "video_model", "webhook_url"):
        if not getattr(args, need):
            ap.error(f"--{need.replace('_', '-')} is required")
    args.proxy = args.proxy.rstrip("/")
    r = run(args)
    with open(args.out, "w") as f:
        json.dump(r, f, indent=1, default=str)
    ok, checks, detail = verdict(r, args.cooldown)
    print(json.dumps({"pass": ok, "checks": checks, "detail": detail}, indent=2))
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()

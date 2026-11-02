#!/usr/bin/env python3
"""KT-DIAR harness: a meeting through the diarize job path, with the gte probe.

Kill-test KT-DIAR of D5-a (.loom/10-research-gfx1100-gfx906-next-differentiators-
2026-10-31.md §6.5, plan .loom/32-iteration-plan-d5a-diarize-job-2026-11-01.md).
Stdlib only; runs anywhere with cluster DNS (a node shell, or a one-shot pod).
It never applies anything.

Phases:
  baseline  embedding latency probe at the gte (and bge) Services, nothing else
  job       POST /v1/audio/transcriptions diarize=true through the proxy, poll the
            job to a terminal status with the probe still running, fetch content
  standalone the same audio through the hand-written pyannote Deployment
            (/diarize), after the probe stopped, as the DER baseline
  queues    with --kfd (on the node, or a pod that sees the host sysfs), the
            card's compute queues sampled every 2 s through all phases

Writes OUT/job.json, OUT/baseline.json, OUT/probe.json, OUT/summary.json and
prints the flexinfer-bench diarize_conformance command with the measured gte
p95s filled in.
"""

import argparse
import json
import os
import threading
import time
import urllib.request
import uuid

PROBE_TEXT = (
    "The retrieval plane embeds meeting notes, design documents and runbooks so the "
    "assistant can cite the paragraph that answers a question about the cluster."
)


def log(msg):
    print(time.strftime("%H:%M:%S"), msg, flush=True)


def pctl(vals, q):
    if not vals:
        return None
    s = sorted(vals)
    k = max(0, min(len(s) - 1, int(round(q * (len(s) - 1)))))
    return s[k]


def post_json(url, body, timeout=30):
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def get_json(url, timeout=30):
    with urllib.request.urlopen(url, timeout=timeout) as resp:
        return json.loads(resp.read())


def multipart(fields, filename, data):
    boundary = uuid.uuid4().hex
    out = []
    for k, v in fields.items():
        out.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode()
        )
    out.append(
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
        "Content-Type: audio/wav\r\n\r\n".encode()
    )
    out.append(data)
    out.append(f"\r\n--{boundary}--\r\n".encode())
    return b"".join(out), f"multipart/form-data; boundary={boundary}"


def post_multipart(url, fields, path, timeout):
    with open(path, "rb") as f:
        body, ct = multipart(fields, os.path.basename(path), f.read())
    req = urllib.request.Request(url, data=body, headers={"Content-Type": ct})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return resp.read()


class Probe(threading.Thread):
    """One embedding request per target every period seconds, tagged by phase."""

    def __init__(self, targets, period):
        super().__init__(daemon=True)
        self.targets, self.period = targets, period
        self.phase = "baseline"
        self.samples = []  # (phase, target, ms or None)
        self.stop = threading.Event()

    def run(self):
        while not self.stop.is_set():
            for name, url, model in self.targets:
                t0 = time.monotonic()
                try:
                    post_json(url, {"model": model, "input": PROBE_TEXT}, timeout=30)
                    self.samples.append(
                        (self.phase, name, 1000 * (time.monotonic() - t0))
                    )
                except Exception:  # an error is a sample too; the summary counts them
                    self.samples.append((self.phase, name, None))
            self.stop.wait(self.period)

    def summary(self):
        out = {}
        for phase in ("baseline", "job"):
            for name, _, _ in self.targets:
                vals = [
                    ms
                    for p, n, ms in self.samples
                    if p == phase and n == name and ms is not None
                ]
                errors = sum(
                    1
                    for p, n, ms in self.samples
                    if p == phase and n == name and ms is None
                )
                out[f"{name}.{phase}"] = {
                    "n": len(vals),
                    "errors": errors,
                    "p50_ms": pctl(vals, 0.5),
                    "p95_ms": pctl(vals, 0.95),
                }
        return out


class Queues(threading.Thread):
    """Total and per-process compute queues from KFD, every 2 s."""

    def __init__(self, kfd):
        super().__init__(daemon=True)
        self.kfd = kfd
        self.samples = []
        self.stop = threading.Event()

    def snapshot(self):
        total = 0
        for pid in os.listdir(self.kfd):
            qdir = os.path.join(self.kfd, pid, "queues")
            try:
                for q in os.listdir(qdir):
                    with open(os.path.join(qdir, q, "type")) as f:
                        # KFD_QUEUE_TYPE_COMPUTE=0; SDMA queues do not count
                        if f.read().strip() == "0":
                            total += 1
            except OSError:
                continue
        return total

    def run(self):
        while not self.stop.is_set():
            self.samples.append((time.time(), self.snapshot()))
            self.stop.wait(2)


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument(
        "--audio",
        required=True,
        help="the 30-minute clip (WAV), e.g. EN2002a Mix-Headset 0:00-30:00",
    )
    ap.add_argument(
        "--proxy", default="http://flexinfer-proxy.flexinfer-system.svc.cluster.local"
    )
    ap.add_argument("--asr-model", default="whisper-large-v3-turbo-radeonvii")
    ap.add_argument(
        "--standalone",
        default="http://pyannote-diarization.flexinfer-system.svc:8000/diarize",
    )
    ap.add_argument(
        "--num-speakers",
        type=int,
        default=0,
        help="pass to both pyannote runs (AMI meetings have 4)",
    )
    ap.add_argument("--baseline-s", type=int, default=240)
    ap.add_argument("--probe-period-s", type=float, default=1.0)
    ap.add_argument("--job-timeout-s", type=int, default=3600)
    ap.add_argument(
        "--kfd",
        default="",
        help="/sys/class/kfd/kfd/proc when the host sysfs is visible",
    )
    ap.add_argument("--out", default="kt-diar-out")
    ap.add_argument(
        "--reference", default="EN2002a.rttm", help="for the printed gate command"
    )
    ap.add_argument("--uri", default="EN2002a")
    ap.add_argument("--ref-offset", default="0")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    targets = [
        (
            "gte",
            "http://gte-qwen2-1p5b-radeonvii.flexinfer-system.svc:8000/v1/embeddings",
            "gte-qwen2-1p5b-radeonvii",
        ),
        (
            "bge",
            "http://bge-large-radeonvii.flexinfer-system.svc:8000/v1/embeddings",
            "bge-large-radeonvii",
        ),
    ]
    probe = Probe(targets, args.probe_period_s)
    queues = Queues(args.kfd) if args.kfd else None
    probe.start()
    if queues:
        queues.start()
    summary = {"audio": args.audio, "asr_model": args.asr_model, "started": time.time()}
    log(f"baseline probe {args.baseline_s}s")
    time.sleep(args.baseline_s)

    probe.phase = "job"
    fields = {"model": args.asr_model, "diarize": "true"}
    if args.num_speakers:
        fields["num_speakers"] = str(args.num_speakers)
    t0 = time.monotonic()
    job = json.loads(
        post_multipart(
            f"{args.proxy}/v1/audio/transcriptions", fields, args.audio, timeout=300
        )
    )
    summary["job_id"] = job.get("id")
    log(f"submitted {job.get('id')} status={job.get('status')}")
    if job.get("object") != "job":
        summary["error"] = f"submission did not return a job: {job}"
        probe.stop.set()
        json.dump(summary, open(os.path.join(args.out, "summary.json"), "w"), indent=2)
        raise SystemExit(summary["error"])
    status = job
    while time.monotonic() - t0 < args.job_timeout_s:
        time.sleep(5)
        status = get_json(f"{args.proxy}/v1/jobs/{job['id']}")
        if status.get("status") in ("completed", "failed", "cancelled", "incomplete"):
            break
    summary["job_seconds"] = round(time.monotonic() - t0, 1)
    summary["job_status"] = status
    log(f"job {status.get('status')} after {summary['job_seconds']}s")
    probe.phase = "after"
    probe.stop.set()
    if status.get("status") == "completed":
        with urllib.request.urlopen(
            f"{args.proxy}/v1/jobs/{job['id']}/content", timeout=300
        ) as resp:
            open(os.path.join(args.out, "job.json"), "wb").write(resp.read())

    log("standalone pyannote baseline")
    fields = {"num_speakers": str(args.num_speakers)} if args.num_speakers else {}
    t1 = time.monotonic()
    open(os.path.join(args.out, "baseline.json"), "wb").write(
        post_multipart(args.standalone, fields, args.audio, timeout=3600)
    )
    summary["standalone_seconds"] = round(time.monotonic() - t1, 1)
    if queues:
        queues.stop.set()
        summary["queues"] = {
            "max": max((n for _, n in queues.samples), default=None),
            "samples": len(queues.samples),
        }
    summary["probe"] = probe.summary()
    json.dump(
        summary["probe"], open(os.path.join(args.out, "probe.json"), "w"), indent=2
    )
    json.dump(summary, open(os.path.join(args.out, "summary.json"), "w"), indent=2)

    gte_base, gte_job = (
        summary["probe"]["gte.baseline"]["p95_ms"],
        summary["probe"]["gte.job"]["p95_ms"],
    )
    print(json.dumps({k: summary[k] for k in summary if k != "job_status"}, indent=2))
    print(
        "\ngo run ./cmd/flexinfer-bench diarize_conformance "
        f"--reference {args.reference} --uri {args.uri} --ref-offset {args.ref_offset} "
        f"--hypothesis {args.out}/job.json --baseline {args.out}/baseline.json "
        f"--gte-baseline-p95-ms {gte_base} --gte-run-p95-ms {gte_job} --job {summary.get('job_id')} "
        "--image <the certified runtime digest> --out kt-diar-report.json"
    )


if __name__ == "__main__":
    main()

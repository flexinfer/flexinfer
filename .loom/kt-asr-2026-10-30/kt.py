"""KT-ASR harness: whisper.cpp ggml-hip on the Radeon VII (gfx906).

Runs inside a one-shot privileged pod on cblevins-radeonvii, beside the live
runtime pod. Stdlib only. Writes everything under OUT and prints a JSON
summary line prefixed with KT_SUMMARY.

Phases:
  baseline  - embedding latency probe at the gte/bge Services, no whisper
  load      - whisper-server start, KFD queue/VRAM attribution
  clips     - warmup (twospk), 10-min excerpt, 60-min recording (verbose_json)
  during    - the same probe runs through the clip phase
"""

import json
import os
import subprocess
import sys
import threading
import time
import urllib.request
import uuid

OUT = os.environ.get("KT_OUT", "/out")
MODEL = os.environ.get("KT_MODEL", "/models/whisper/ggml-large-v3-turbo-q8_0.bin")
SERVER = os.environ.get("KT_SERVER", "/opt/whispercpp/bin/whisper-server")
PORT = int(os.environ.get("KT_PORT", "8090"))
THREADS = os.environ.get("KT_THREADS", "4")
EXTRA_ARGS = os.environ.get("KT_EXTRA_ARGS", "").split()
BASELINE_S = int(os.environ.get("KT_BASELINE_S", "240"))
PROBE_PERIOD_S = float(os.environ.get("KT_PROBE_PERIOD_S", "1.0"))
FIXTURES = [
    ("twospk", "/fixtures/twospk.wav"),
    ("meeting10", "/fixtures/meeting10.wav"),
    ("meeting60", "/fixtures/meeting60.wav"),
]
PROBE_TARGETS = [
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
PROBE_TEXT = (
    "The retrieval plane embeds meeting notes, design documents and runbooks so the "
    "assistant can cite the paragraph that answers a question about the cluster."
)
KFD = "/sys/class/kfd/kfd/proc"


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def read(path, default=""):
    try:
        with open(path) as f:
            return f.read().strip()
    except OSError:
        return default


def kfd_snapshot():
    """Per host-PID: compute queues, SDMA queues and VRAM bytes."""
    procs = {}
    for pid in os.listdir(KFD):
        qdir = os.path.join(KFD, pid, "queues")
        compute = sdma = other = 0
        if os.path.isdir(qdir):
            for q in os.listdir(qdir):
                t = read(os.path.join(qdir, q, "type"), "?")
                # KFD_QUEUE_TYPE_COMPUTE=0, SDMA=1, SDMA_XGMI=4
                if t == "0":
                    compute += 1
                elif t in ("1", "4"):
                    sdma += 1
                else:
                    other += 1
        vram = 0
        for name in os.listdir(os.path.join(KFD, pid)):
            if name.startswith("vram_"):
                try:
                    vram += int(read(os.path.join(KFD, pid, name), "0"))
                except ValueError:
                    pass
        procs[pid] = {"compute": compute, "sdma": sdma, "other": other, "vram": vram}
    return procs


def card_vram_used():
    for card in sorted(os.listdir("/sys/class/drm")):
        p = f"/sys/class/drm/{card}/device/mem_info_vram_used"
        if card.startswith("card") and "-" not in card and os.path.exists(p):
            return int(read(p, "0"))
    return -1


def dmesg_lines():
    try:
        out = subprocess.run(
            ["dmesg", "-T"], capture_output=True, text=True, timeout=30
        ).stdout
    except Exception as exc:  # noqa: BLE001
        return [f"dmesg failed: {exc}"]
    keys = ("oversubscri", "amdgpu", "kfd", "gpu fault", "hws", "mes")
    return [ln for ln in out.splitlines() if any(k in ln.lower() for k in keys)]


class Probe(threading.Thread):
    """Embedding latency probe at the lanes' own Services."""

    def __init__(self):
        super().__init__(daemon=True)
        self.phase = "baseline"
        self.samples = []  # (phase, lane, t, latency_s or None, err)
        self.stop = threading.Event()

    def run(self):
        while not self.stop.is_set():
            for lane, url, model in PROBE_TARGETS:
                body = json.dumps({"model": model, "input": PROBE_TEXT}).encode()
                req = urllib.request.Request(
                    url, data=body, headers={"Content-Type": "application/json"}
                )
                t0 = time.monotonic()
                err = ""
                try:
                    with urllib.request.urlopen(req, timeout=60) as resp:
                        resp.read()
                    lat = time.monotonic() - t0
                except Exception as exc:  # noqa: BLE001
                    lat, err = None, str(exc)[:200]
                self.samples.append((self.phase, lane, time.time(), lat, err))
            self.stop.wait(PROBE_PERIOD_S)


class Sampler(threading.Thread):
    """VRAM and queue sampler, 0.5 s period."""

    def __init__(self):
        super().__init__(daemon=True)
        self.whisper_pids = set()
        self.rows = []
        self.stop = threading.Event()

    def run(self):
        while not self.stop.is_set():
            snap = kfd_snapshot()
            w = [snap[p] for p in self.whisper_pids if p in snap]
            self.rows.append(
                {
                    "t": time.time(),
                    "card_vram_used": card_vram_used(),
                    "plane_compute_queues": sum(v["compute"] for v in snap.values()),
                    "plane_sdma_queues": sum(v["sdma"] for v in snap.values()),
                    "whisper_vram": sum(v["vram"] for v in w),
                    "whisper_compute_queues": sum(v["compute"] for v in w),
                    "whisper_sdma_queues": sum(v["sdma"] for v in w),
                }
            )
            self.stop.wait(0.5)


def multipart(fields, file_field, filename, data):
    boundary = uuid.uuid4().hex
    parts = []
    for k, v in fields:
        parts.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode()
        )
    parts.append(
        f'--{boundary}\r\nContent-Disposition: form-data; name="{file_field}"; filename="{filename}"\r\n'
        f"Content-Type: audio/wav\r\n\r\n".encode() + data + b"\r\n"
    )
    parts.append(f"--{boundary}--\r\n".encode())
    return b"".join(parts), f"multipart/form-data; boundary={boundary}"


def transcribe(path):
    with open(path, "rb") as f:
        data = f.read()
    body, ctype = multipart(
        [
            ("model", "whisper-large-v3-turbo"),
            ("response_format", "verbose_json"),
            ("timestamp_granularities[]", "word"),
        ],
        "file",
        os.path.basename(path),
        data,
    )
    req = urllib.request.Request(
        f"http://127.0.0.1:{PORT}/v1/audio/transcriptions",
        data=body,
        headers={"Content-Type": ctype},
    )
    t0 = time.monotonic()
    with urllib.request.urlopen(req, timeout=7200) as resp:
        payload = resp.read()
    return time.monotonic() - t0, payload


def wav_seconds(path):
    # 16 kHz mono s16le with a 44-byte header (ffmpeg pcm_s16le output)
    return (os.path.getsize(path) - 44) / 32000.0


def pctl(vals, q):
    vals = sorted(vals)
    if not vals:
        return None
    k = max(0, min(len(vals) - 1, int(round(q * (len(vals) - 1)))))
    return vals[k]


def main():
    os.makedirs(OUT, exist_ok=True)
    summary = {
        "model": MODEL,
        "threads": THREADS,
        "extra_args": EXTRA_ARGS,
        "env": {
            k: os.environ.get(k)
            for k in (
                "GPU_MAX_HW_QUEUES",
                "HSA_ENABLE_SDMA",
                "HSA_USE_SVM",
                "ROCR_VISIBLE_DEVICES",
                "HSA_OVERRIDE_GFX_VERSION",
                "LD_PRELOAD",
            )
        },
    }
    dmesg_before = dmesg_lines()
    pre = kfd_snapshot()
    summary["pre"] = {
        "card_vram_used": card_vram_used(),
        "kfd": pre,
        "plane_compute_queues": sum(v["compute"] for v in pre.values()),
    }
    log(
        f"pre: plane compute queues={summary['pre']['plane_compute_queues']} vram={summary['pre']['card_vram_used']}"
    )

    probe = Probe()
    probe.start()
    sampler = Sampler()
    sampler.start()
    log(f"baseline probe for {BASELINE_S}s")
    time.sleep(BASELINE_S)

    probe.phase = "load"
    env = dict(os.environ)
    cmd = [
        SERVER,
        "-m",
        MODEL,
        "--host",
        "127.0.0.1",
        "--port",
        str(PORT),
        "--inference-path",
        "/v1/audio/transcriptions",
        "-t",
        THREADS,
        "-l",
        "auto",
        "--tmp-dir",
        "/tmp",
    ] + EXTRA_ARGS
    log("start: " + " ".join(cmd))
    t_start = time.monotonic()
    srv = subprocess.Popen(
        cmd, stdout=open(f"{OUT}/server.log", "w"), stderr=subprocess.STDOUT, env=env
    )
    ready = False
    while time.monotonic() - t_start < 600:
        if srv.poll() is not None:
            break
        try:
            with urllib.request.urlopen(
                f"http://127.0.0.1:{PORT}/health", timeout=2
            ) as r:
                if b"ok" in r.read():
                    ready = True
                    break
        except Exception:  # noqa: BLE001
            pass
        time.sleep(0.5)
    summary["server_ready_s"] = round(time.monotonic() - t_start, 2)
    if not ready:
        summary["error"] = f"server not ready rc={srv.poll()}"
        log(summary["error"])
        print("KT_SUMMARY " + json.dumps(summary), flush=True)
        return
    post_load = kfd_snapshot()
    new = sorted(set(post_load) - set(pre))
    sampler.whisper_pids = set(new)
    summary["whisper_kfd_pids"] = {p: post_load[p] for p in new}
    log(
        f"ready in {summary['server_ready_s']}s, new KFD pids={summary['whisper_kfd_pids']}"
    )

    probe.phase = "during"
    runs = {}
    for name, path in FIXTURES:
        audio_s = wav_seconds(path)
        log(f"transcribe {name} ({audio_s:.1f}s)")
        t_wall0 = time.time()
        try:
            wall, payload = transcribe(path)
        except Exception as exc:  # noqa: BLE001
            runs[name] = {"error": str(exc)[:500]}
            log(f"{name} failed: {exc}")
            continue
        with open(f"{OUT}/{name}.whispercpp.json", "wb") as f:
            f.write(payload)
        doc = json.loads(payload)
        runs[name] = {
            "audio_s": round(audio_s, 2),
            "wall_s": round(wall, 2),
            "x_realtime": round(audio_s / wall, 2),
            "t_start": t_wall0,
            "t_end": time.time(),
            "language": doc.get("language"),
            "segments": len(doc.get("segments", [])),
            "chars": len(doc.get("text", "")),
        }
        log(f"{name}: wall={wall:.1f}s x_rt={audio_s / wall:.2f}")
    summary["runs"] = runs

    probe.phase = "after"
    time.sleep(5)
    probe.stop.set()
    sampler.stop.set()
    time.sleep(1)
    final = kfd_snapshot()
    summary["post"] = {
        "card_vram_used": card_vram_used(),
        "plane_compute_queues": sum(v["compute"] for v in final.values()),
        "whisper": {p: final.get(p) for p in new},
    }
    rows = sampler.rows
    summary["peak"] = {
        "whisper_vram_mb": round(
            max((r["whisper_vram"] for r in rows), default=0) / 2**20, 1
        ),
        "whisper_compute_queues": max(
            (r["whisper_compute_queues"] for r in rows), default=0
        ),
        "whisper_sdma_queues": max((r["whisper_sdma_queues"] for r in rows), default=0),
        "plane_compute_queues": max(
            (r["plane_compute_queues"] for r in rows), default=0
        ),
        "card_vram_used_mb": round(
            max((r["card_vram_used"] for r in rows), default=0) / 2**20, 1
        ),
    }
    lat = {}
    for phase in ("baseline", "during"):
        for lane, _, _ in PROBE_TARGETS:
            xs = [
                s[3]
                for s in probe.samples
                if s[0] == phase and s[1] == lane and s[3] is not None
            ]
            errs = sum(
                1
                for s in probe.samples
                if s[0] == phase and s[1] == lane and s[3] is None
            )
            lat[f"{lane}_{phase}"] = {
                "n": len(xs),
                "errors": errs,
                "p50_ms": round(1000 * (pctl(xs, 0.5) or 0), 1),
                "p95_ms": round(1000 * (pctl(xs, 0.95) or 0), 1),
                "max_ms": round(1000 * max(xs, default=0), 1),
            }
    summary["probe"] = lat
    dmesg_after = dmesg_lines()
    summary["dmesg_new"] = [ln for ln in dmesg_after if ln not in set(dmesg_before)]
    summary["dmesg_oversubscribed"] = [
        ln for ln in dmesg_after if "oversubscri" in ln.lower()
    ]
    with open(f"{OUT}/probe.json", "w") as f:
        json.dump(probe.samples, f)
    with open(f"{OUT}/sampler.json", "w") as f:
        json.dump(rows, f)
    with open(f"{OUT}/summary.json", "w") as f:
        json.dump(summary, f, indent=1)
    srv.terminate()
    try:
        srv.wait(timeout=30)
    except subprocess.TimeoutExpired:
        srv.kill()
    print("KT_SUMMARY " + json.dumps(summary), flush=True)


if __name__ == "__main__":
    main()
    sys.stdout.flush()

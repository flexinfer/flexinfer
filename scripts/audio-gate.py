#!/usr/bin/env python3
"""Audio gate: is a Stable Audio lane sound after a load, and as fast?

The in-pod twin of scripts/video-gate.py for Stable Audio Open (Draft !1239:
the lane in-pod on the XTX runtime). The dedicated lane renders three clips
as the control (arm A, before the merge); the in-pod lane renders four in one
process (arm B, after the merge), so a corruption that appears only after the
first render in a process shows up.

Three steps, on different hosts:

  run     (stdlib; any host that reaches the proxy, e.g. cblevins-5930k)
          POST /v1/audio/generations through the proxy, back to back:
            arm A (dedicated, before the merge)  a1, a2, a1
            arm B (in-pod, after the merge)      a1, a2, a1, a1
          a1 is the rain prompt at seed 42, a2 the guitar prompt at seed 7;
          10 s at 100 steps, the lane's negative prompt. Saves each WAV and
          audio-gate-<arm>.json.

  sample-vram  scripts/video-gate.py sample-vram, unchanged.

  score   (stdlib; numpy adds the spectral comparison)
          Reads every WAV, checks it is sound (format, length, level,
          clipping, silence, the -32768 runs a NaN cast leaves), compares
          clips (bytes, then waveform SNR, then log-spectrum cosine) and
          judges the row's bars, its validity checks and its decision rule.

    python3 scripts/audio-gate.py run --url http://10.43.49.107 \\
        --model <lane> --arm A --out-dir gate-A
    python3 scripts/audio-gate.py score gate-A gate-B \\
        --vram-a vram-A.jsonl --vram-b vram-B.jsonl --runtime-peak-gib 21.3 \\
        --runtime-log runtime.log --process-env env.txt \\
        --kernel-log kernel.txt --root-mode rw --owner-listen pass \\
        --json verdict.json

Two stacks do not render the same waveform for a seed (diffusion amplifies
float differences), so bar (3) is the owner's A/B listen of a1 and a2; the
SNR and spectral cosine are reported beside it; a rejected listen fails the
row. A bar without its evidence is reported as not measured and the decision
is "incomplete", never "pass". Row gfx1100-xtx-inpod-stable-audio-gate-
2026-11-22 holds these bars.

Exit status: run and score return 0 on success (score: decision "pass"), 1
otherwise; 2 on an error (bad arguments, unreadable input, missing tool).
"""

from __future__ import annotations

import argparse
import array
import base64
import hashlib
import importlib.util
import io
import json
import math
import re
import sys
import time
import urllib.error
import urllib.request
import wave
from pathlib import Path


def _video_gate():
    path = Path(__file__).with_name("video-gate.py")
    spec = importlib.util.spec_from_file_location("video_gate", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


vg = _video_gate()

PROMPTS = {
    "rain": "gentle rain on a tin roof with distant rolling thunder",
    "guitar": "a solo acoustic guitar playing a slow folk melody, warm and close",
}
CLIPS = {"a1": ("rain", 42), "a2": ("guitar", 7)}
SEQUENCES = {"A": ["a1", "a2", "a1"], "B": ["a1", "a2", "a1", "a1"]}
SECONDS = 10.0
STEPS = 100

# Soundness of one clip.
SAMPLE_RATE = 44100
CHANNELS = 2
DURATION_TOL_S = 0.05
RMS_DBFS_RANGE = (-40.0, -3.0)
CLIP_FRAC_MAX = 0.01
MIN_VALUE_FRAC_MAX = 0.001  # int16 -32768: what a NaN sample casts to
SILENT_WINDOW_S = 0.05
SILENT_DBFS = -60.0
SILENT_FRAC_MAX = 0.5

# Bars.
SAME_SEED_SNR_DB = 50.0
TOTAL_RATIO = 1.25
# The lane sets no flexinfer.ai/lane-first-byte-budget, so the proxy's
# default 120 s bounds the first request, load included.
FIRST_CLIP_MAX_S = 120.0
VRAM_MARGIN_MIB = 1024.0
RUNTIME_PEAK_MAX_GIB = 44.0
# S9 arm sa-A (the dedicated lane on d87f75f8, 2026-11-05) denoised at 7.4 it/s
# (its 3.24 s clip ran 20 steps, not 100), so 100 steps take about 13.5 s; the
# A arm of 2026-11-22 measured 14.07 s. The guard is 0.7x to 1.3x of 13.5 s,
# the proportions of video-gate's VACE range.
A_WARM_TOTAL_RANGE = (9.5, 17.5)

# The torchsde RecursionError (!1389) and tracebacks count as warning lines.
WARNING_RE = re.compile(
    vg.WARNING_RE.pattern + r"|RecursionError|Traceback", re.IGNORECASE
)


# --------------------------------------------------------------------------
# run (stdlib)
# --------------------------------------------------------------------------


def request_body(model: str, clip: str) -> dict:
    key, seed = CLIPS[clip]
    return {
        "model": model,
        "prompt": PROMPTS[key],
        "seconds": SECONDS,
        "num_inference_steps": STEPS,
        "seed": seed,
        "response_format": "b64_json",
    }


def render_one(url, model, clip, auth, timeout):
    body = json.dumps(request_body(model, clip)).encode()
    req = urllib.request.Request(
        url.rstrip("/") + "/v1/audio/generations", data=body, method="POST"
    )
    req.add_header("Content-Type", "application/json")
    if auth:
        req.add_header("Authorization", "Bearer " + auth)
    t0 = time.time()
    rec = {"clip": clip, "seed": CLIPS[clip][1], "start": vg.iso(t0), "t0": t0}
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            rec["status"] = resp.status
    except urllib.error.HTTPError as e:
        rec["status"] = e.code
        rec["body"] = e.read()[:600].decode(errors="replace")
    except Exception as e:  # noqa: BLE001 - recorded, the run stops
        rec["status"] = 0
        rec["error"] = repr(e)[:300]
    rec["t1"] = time.time()
    rec["wall_s"] = round(rec["t1"] - t0, 2)
    if rec.get("status") != 200:
        return rec, None
    try:
        data = json.loads(raw)["data"][0]
        audio = base64.b64decode(data["b64_json"])
    except (ValueError, KeyError, IndexError, TypeError) as e:
        rec["status"] = 0
        rec["error"] = "malformed response: " + repr(e)[:200]
        return rec, None
    for k in ("sample_rate", "duration", "seed", "mime_type"):
        if k in data:
            rec["resp_" + k] = data[k]
    rec["timings"] = data.get("timings") or {}
    return rec, audio


def cmd_run(args) -> int:
    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)
    seq = SEQUENCES[args.arm]
    report = {
        "arm": args.arm,
        "model": args.model,
        "url": args.url,
        "sequence": seq,
        "request": {"seconds": SECONDS, "num_inference_steps": STEPS},
        "t0": time.time(),
        "renders": [],
    }
    report["started_at"] = vg.iso(report["t0"])
    ok = True
    for i, clip in enumerate(seq, 1):
        rec, audio = render_one(args.url, args.model, clip, args.auth, args.timeout)
        rec["i"] = i
        if audio is not None:
            name = f"{args.arm}-r{i}-{clip}.wav"
            (out / name).write_bytes(audio)
            rec.update(
                file=name, bytes=len(audio), sha256=hashlib.sha256(audio).hexdigest()
            )
        print(
            json.dumps({k: v for k, v in rec.items() if k not in ("t0", "t1")}),
            flush=True,
        )
        report["renders"].append(rec)
        if audio is None:
            ok = False
            break
    report["t1"] = time.time()
    report["finished_at"] = vg.iso(report["t1"])
    (out / f"audio-gate-{args.arm}.json").write_text(json.dumps(report, indent=1))
    return 0 if ok else 1


# --------------------------------------------------------------------------
# metrics (stdlib; numpy for the spectrum)
# --------------------------------------------------------------------------


def read_wav(data: bytes):
    """WAV bytes -> (info, interleaved int16 samples)."""
    with wave.open(io.BytesIO(data), "rb") as w:
        info = {
            "sample_rate": w.getframerate(),
            "channels": w.getnchannels(),
            "sample_width": w.getsampwidth(),
            "frames": w.getnframes(),
        }
        raw = w.readframes(info["frames"])
    if info["sample_width"] != 2:
        raise ValueError(f"expected 16-bit PCM, got {info['sample_width'] * 8}-bit")
    pcm = array.array("h")
    pcm.frombytes(raw)
    if sys.byteorder == "big":  # pragma: no cover - WAV is little-endian
        pcm.byteswap()
    return info, pcm


def _dbfs(rms: float) -> float:
    return -math.inf if rms <= 0 else 20.0 * math.log10(rms / 32768.0)


def audio_stats(info: dict, pcm) -> dict:
    n = len(pcm)
    rate, ch = info["sample_rate"], max(1, info["channels"])
    sq = sum(x * x for x in pcm)
    rms = math.sqrt(sq / n) if n else 0.0
    peak = max((abs(x) for x in pcm), default=0)
    clipped = sum(1 for x in pcm if x >= 32767 or x <= -32767)
    min_value = sum(1 for x in pcm if x == -32768)
    win = max(1, int(SILENT_WINDOW_S * rate)) * ch
    windows = silent = 0
    for s in range(0, n - win + 1, win):
        seg = pcm[s : s + win]
        windows += 1
        if _dbfs(math.sqrt(sum(x * x for x in seg) / win)) < SILENT_DBFS:
            silent += 1
    return {
        "sample_rate": rate,
        "channels": info["channels"],
        "duration_s": round(info["frames"] / rate, 4) if rate else 0.0,
        "rms_dbfs": round(_dbfs(rms), 2),
        "peak": peak,
        "clip_frac": round(clipped / n, 6) if n else 1.0,
        "min_value_frac": round(min_value / n, 6) if n else 1.0,
        "silent_frac": round(silent / windows, 4) if windows else 1.0,
    }


def snr_db(a, b):
    """Waveform SNR of b against a (equal length), inf when identical."""
    if len(a) != len(b) or not len(a):
        return None
    sig = sum(x * x for x in a)
    noise = sum((x - y) * (x - y) for x, y in zip(a, b))
    if noise == 0:
        return math.inf
    if sig == 0:
        return -math.inf
    return round(10.0 * math.log10(sig / noise), 2)


def spectral_cosine(a, b, rate: int, channels: int):
    """Cosine of the mean log-magnitude spectra (mono, 2048-point frames).

    1.0 is the same spectral envelope; two renders of one prompt on two
    stacks are expected high, two different prompts lower. None without numpy.
    """
    try:
        import numpy as np
    except ImportError:
        return None

    def spectrum(x):
        mono = np.asarray(x, dtype=np.float64).reshape(-1, channels).mean(axis=1)
        n = 2048
        frames = len(mono) // n
        if frames == 0:
            return None
        win = np.hanning(n)
        mag = np.abs(np.fft.rfft(mono[: frames * n].reshape(frames, n) * win, axis=1))
        return np.log1p(mag).mean(axis=0)

    sa, sb = spectrum(a), spectrum(b)
    if sa is None or sb is None:
        return None
    den = float(np.linalg.norm(sa) * np.linalg.norm(sb))
    return round(float(np.dot(sa, sb)) / den, 5) if den else None


# --------------------------------------------------------------------------
# verdict (pure Python)
# --------------------------------------------------------------------------


def sound(r: dict) -> bool:
    m = r.get("media") or {}
    return (
        r.get("status") == 200
        and m.get("sample_rate") == SAMPLE_RATE
        and m.get("channels") == CHANNELS
        and abs(m.get("duration_s", 0) - SECONDS) <= DURATION_TOL_S
        and RMS_DBFS_RANGE[0] <= m.get("rms_dbfs", -math.inf) <= RMS_DBFS_RANGE[1]
        and m.get("clip_frac", 1.0) < CLIP_FRAC_MAX
        and m.get("min_value_frac", 1.0) < MIN_VALUE_FRAC_MAX
        and m.get("silent_frac", 1.0) < SILENT_FRAC_MAX
    )


def matches(cmp) -> bool:
    return bool(cmp) and (
        cmp.get("identical") is True
        or (cmp.get("snr_db") or -math.inf) >= SAME_SEED_SNR_DB
    )


def _mean(values):
    values = [v for v in values if isinstance(v, (int, float))]
    return sum(values) / len(values) if values else None


def _timing(renders, idx, key):
    return [renders[i].get("timings", {}).get(key) for i in idx if i < len(renders)]


def _bar(n, name, ok, value, limit):
    return {"bar": n, "name": name, "pass": ok, "value": value, "limit": limit}


def verdict(m: dict) -> dict:
    """Judge the row's bars from the collected metrics.

    m: {"A": {"renders": [...]}, "B": {"renders": [...]},
        "cmp": {"A_a1_r3", "B_a1_r3", "B_a1_r4", "e2e_a1", "e2e_a2"},
        "vram": {"A_peak_mib", "B_peak_mib"} or None,
        "runtime_peak_gib": float or None,
        "a_warm_total_range": [lo, hi] (default A_WARM_TOTAL_RANGE),
        "evidence": {"pins", "warning_lines", "kernel_lines", "root_rw",
                     "owner_listen"} (each may be None)}
    Each render: {"i", "clip", "status", "wall_s", "timings", "media"}.
    """
    A = m["A"]["renders"]
    B = m["B"]["renders"]
    cmp = m.get("cmp", {})
    ev = m.get("evidence") or {}
    bars = []

    b_complete = len(B) == len(SEQUENCES["B"])
    bars.append(
        _bar(
            1,
            "every B clip sound",
            b_complete and all(sound(r) for r in B),
            [sound(r) for r in B],
            f"200, {SAMPLE_RATE} Hz x{CHANNELS}, {SECONDS} s, level/clip/silence",
        )
    )
    bars.append(
        _bar(
            2,
            "B same-seed a1 clips match",
            matches(cmp.get("B_a1_r3")) and matches(cmp.get("B_a1_r4")),
            [(cmp.get(k) or {}).get("snr_db") for k in ("B_a1_r3", "B_a1_r4")],
            f"identical or >= {SAME_SEED_SNR_DB} dB SNR",
        )
    )
    e2e = [cmp.get("e2e_a1") or {}, cmp.get("e2e_a2") or {}]
    listen = ev.get("owner_listen")
    bars.append(
        _bar(
            3,
            "end to end against A (owner listen)",
            None if listen is None else bool(listen),
            {
                "snr_db": [c.get("snr_db") for c in e2e],
                "spectral_cosine": [c.get("spectral_cosine") for c in e2e],
                "owner_listen": listen,
            },
            "owner A/B listen of a1 and a2; SNR and spectrum reported",
        )
    )
    tot_a = _mean(_timing(A, [1, 2], "total_seconds"))
    tot_b = _mean(_timing(B, [1, 2, 3], "total_seconds"))
    bars.append(
        _bar(
            4,
            "warm total",
            None if tot_a is None or tot_b is None else tot_b <= TOTAL_RATIO * tot_a,
            [tot_b, tot_a],
            f"B <= {TOTAL_RATIO} x A",
        )
    )
    first = B[0].get("wall_s") if B else None
    bars.append(
        _bar(
            5,
            "first B clip",
            (
                None
                if first is None
                else B[0].get("status") == 200 and first <= FIRST_CLIP_MAX_S
            ),
            first,
            f"<= {FIRST_CLIP_MAX_S} s",
        )
    )
    v = m.get("vram") or {}
    va, vb = v.get("A_peak_mib"), v.get("B_peak_mib")
    bars.append(
        _bar(
            6,
            "card VRAM peak",
            None if va is None or vb is None else vb <= va + VRAM_MARGIN_MIB,
            [vb, va],
            f"B <= A + {VRAM_MARGIN_MIB} MiB",
        )
    )
    rp = m.get("runtime_peak_gib")
    bars.append(
        _bar(
            7,
            "runtime pod working set",
            None if rp is None else rp < RUNTIME_PEAK_MAX_GIB,
            rp,
            f"< {RUNTIME_PEAK_MAX_GIB} GiB",
        )
    )
    pins, warns = ev.get("pins"), ev.get("warning_lines")
    bars.append(
        _bar(
            8,
            "pins, no warnings",
            None if None in (pins, warns) else bool(pins) and warns == 0,
            {"pins": pins, "warning_lines": warns},
            "both pins, 0 warning lines (incl. RecursionError, Traceback)",
        )
    )
    kl, rw = ev.get("kernel_lines"), ev.get("root_rw")
    bars.append(
        _bar(
            9,
            "kernel log and root",
            None if kl is None or rw is None else kl == 0 and bool(rw),
            {"kernel_lines": kl, "root_rw": rw},
            "0 lines, root rw",
        )
    )

    lo, hi = m.get("a_warm_total_range") or A_WARM_TOTAL_RANGE
    validity = {
        "A complete and sound": len(A) == len(SEQUENCES["A"])
        and all(sound(r) for r in A),
        "A same-seed a1 match": matches(cmp.get("A_a1_r3")),
        "A warm total in range": tot_a is not None and lo <= tot_a <= hi,
    }

    failed = [b["bar"] for b in bars if b["pass"] is False]
    missing = [b["bar"] for b in bars if b["pass"] is None]
    # Bar (3) is already the owner's judgment, so unlike the video gate a
    # rejected listen is a fail, not a conditional.
    if not all(validity.values()):
        decision = "invalid"
    elif failed:
        decision = "fail"
    elif missing:
        decision = "incomplete"
    else:
        decision = "pass"
    return {
        "decision": decision,
        "failed_bars": failed,
        "not_measured": missing,
        "validity": validity,
        "a_warm_total_s": tot_a,
        "bars": bars,
    }


# --------------------------------------------------------------------------
# score
# --------------------------------------------------------------------------


def load_arm(d: Path, arm: str):
    report = json.loads((d / f"audio-gate-{arm}.json").read_text())
    pcm = {}
    for r in report["renders"]:
        if r.get("status") != 200 or not r.get("file"):
            continue
        info, samples = read_wav((d / r["file"]).read_bytes())
        r["media"] = audio_stats(info, samples)
        pcm[r["i"]] = samples
    return report, pcm


def _compare(ra, pa, i, rb, pb, j):
    if i not in pa or j not in pb:
        return None
    sa = ra["renders"][i - 1].get("sha256")
    sb = rb["renders"][j - 1].get("sha256")
    if sa and sa == sb:
        return {"identical": True, "snr_db": math.inf, "spectral_cosine": 1.0}
    media = ra["renders"][i - 1].get("media") or {}
    return {
        "identical": False,
        "snr_db": snr_db(pa[i], pb[j]),
        "spectral_cosine": spectral_cosine(
            pa[i],
            pb[j],
            media.get("sample_rate", SAMPLE_RATE),
            media.get("channels", CHANNELS),
        ),
    }


def runtime_log_evidence(text: str, model: str) -> dict:
    lines = [line for line in text.splitlines() if model in line]
    return {
        "warning_lines": sum(1 for line in lines if WARNING_RE.search(line)),
        "lane_lines": len(lines),
    }


def cmd_score(args) -> int:
    ra, pa = load_arm(Path(args.a_dir), "A")
    rb, pb = load_arm(Path(args.b_dir), "B")
    cmp = {
        "A_a1_r3": _compare(ra, pa, 1, ra, pa, 3),
        "B_a1_r3": _compare(rb, pb, 1, rb, pb, 3),
        "B_a1_r4": _compare(rb, pb, 1, rb, pb, 4),
        "e2e_a1": _compare(ra, pa, 1, rb, pb, 1),
        "e2e_a2": _compare(ra, pa, 2, rb, pb, 2),
    }
    vram = None
    if args.vram_a and args.vram_b:
        vram = {
            "A_peak_mib": vg.vram_peak(args.vram_a, ra["t0"], ra["t1"]),
            "B_peak_mib": vg.vram_peak(args.vram_b, rb["t0"], rb["t1"]),
        }
    ev = {
        "pins": None,
        "warning_lines": None,
        "kernel_lines": None,
        "root_rw": None,
        "owner_listen": None,
    }
    if args.process_env:
        ev["pins"] = vg.pins_present(Path(args.process_env).read_text(errors="replace"))
    if args.runtime_log:
        ev["warning_lines"] = runtime_log_evidence(
            Path(args.runtime_log).read_text(errors="replace"), rb["model"]
        )["warning_lines"]
    if args.kernel_log:
        ev["kernel_lines"] = vg.kernel_line_count(
            Path(args.kernel_log).read_text(errors="replace")
        )
    if args.root_mode:
        ev["root_rw"] = args.root_mode == "rw"
    if args.owner_listen:
        ev["owner_listen"] = args.owner_listen == "pass"
    metrics = {
        "A": ra,
        "B": rb,
        "cmp": cmp,
        "vram": vram,
        "runtime_peak_gib": args.runtime_peak_gib,
        "a_warm_total_range": args.a_warm_range,
        "evidence": ev,
    }
    result = verdict(metrics)
    keep = ("i", "clip", "status", "wall_s", "timings", "media", "sha256")
    result["metrics"] = {
        "cmp": cmp,
        "vram": vram,
        "evidence": ev,
        "A": [{k: r.get(k) for k in keep} for r in ra["renders"]],
        "B": [{k: r.get(k) for k in keep} for r in rb["renders"]],
    }
    text = json.dumps(result, indent=1, default=str)
    if args.json:
        Path(args.json).write_text(text)
    for b in result["bars"]:
        mark = {True: "pass", False: "FAIL", None: "not measured"}[b["pass"]]
        print(
            f"({b['bar']}) {b['name']}: {mark}  value={b['value']}  limit={b['limit']}"
        )
    print("validity:", result["validity"])
    print("decision:", result["decision"])
    return 0 if result["decision"] == "pass" else 1


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run", help="render an arm's clips through the proxy")
    r.add_argument("--url", required=True, help="proxy base URL (no /v1 suffix)")
    r.add_argument("--model", required=True)
    r.add_argument("--arm", required=True, choices=sorted(SEQUENCES))
    r.add_argument("--out-dir", required=True)
    r.add_argument("--auth", default="")
    r.add_argument(
        "--timeout",
        type=float,
        default=1500.0,
        help="seconds per request (default covers a 900 s cold start)",
    )
    s = sub.add_parser("score", help="judge arms A and B (stdlib; numpy optional)")
    s.add_argument("a_dir")
    s.add_argument("b_dir")
    s.add_argument("--vram-a")
    s.add_argument("--vram-b")
    s.add_argument("--runtime-peak-gib", type=float)
    s.add_argument("--runtime-log", help="runtime pod log covering arm B")
    s.add_argument("--process-env", help="/proc/<pid>/environ of the in-pod server")
    s.add_argument("--kernel-log", help="GPU host kernel log over both arms")
    s.add_argument("--root-mode", choices=["rw", "ro"])
    s.add_argument(
        "--owner-listen",
        choices=["pass", "fail"],
        help="bar (3): the owner's A/B listen of a1 and a2",
    )
    s.add_argument(
        "--a-warm-range",
        type=float,
        nargs=2,
        metavar=("LO", "HI"),
        help=f"validity range of A's warm total in seconds (default {A_WARM_TOTAL_RANGE})",
    )
    s.add_argument("--json", help="write the verdict JSON here")
    args = ap.parse_args(argv)
    if args.cmd == "score" and args.a_warm_range:
        args.a_warm_range = list(args.a_warm_range)
    try:
        return {"run": cmd_run, "score": cmd_score}[args.cmd](args)
    except (OSError, ValueError, KeyError, EOFError, wave.Error) as e:
        print(f"error: {e!r}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Video gate: is a Wan text-to-video lane sound after a load, and as fast?

The live gate of row gfx1100-xtx-inpod-wan-t2v-gate-2026-11-21 in
.loom/60-validation-matrix.md (Draft !1237: Wan 2.1 T2V in-pod on the XTX
runtime with the allocator pinned). S9 found that expandable_segments with
model CPU offload corrupts every Wan VAE use after the first in a process, so
the in-pod arm renders four clips in one process; the dedicated lane renders
three as the control.

Three steps, on different hosts:

  run     (stdlib; any host that reaches the proxy, e.g. cblevins-5930k)
          POST /v1/videos/generations through the proxy, back to back:
            arm A (dedicated, before the merge)  c1, c2, c1
            arm B (in-pod, after the merge)      c1, c2, c1, c1
          c1 is S9's boat prompt at seed 42, c2 its fox prompt at seed 7;
          33 frames at 832x480, 20 steps, the lane's default guidance and
          negative prompt. Saves each MP4 and video-gate-<arm>.json.

  sample-vram  (stdlib + ssh; run beside each arm, stop it with Ctrl-C)
          Samples the discrete card's mem_info_vram_used on the GPU host
          every --interval seconds into a JSONL file.

  score   (numpy + ffmpeg/ffprobe; e.g. a workstation)
          Decodes every MP4, compares clips with scripts/wan-decode-ab.py's
          compare_frames (PSNR, SSIM on BT.601 luma) and judges the row's
          bars (1)-(10), its validity checks and its decision rule.

    python3 scripts/video-gate.py run --url http://10.43.49.107 \\
        --model <lane> --arm A --out-dir gate-A
    python3 scripts/video-gate.py sample-vram --host cblevins-7900xtx --out vram-A.jsonl
    python3 scripts/video-gate.py score gate-A gate-B \\
        --vram-a vram-A.jsonl --vram-b vram-B.jsonl --runtime-peak-gib 21.3 \\
        --runtime-log runtime.log --process-env env.txt \\
        --kernel-log kernel.txt --root-mode rw --json verdict.json

Bars a step cannot see are passed in as evidence (runtime pod peak, process
environment, runtime and kernel logs). A bar without its evidence is reported
as not measured and the decision is "incomplete", never "pass".

Exit status: run and score return 0 on success (score: decision "pass"), 1
otherwise; 2 on an error (bad arguments, unreadable input, missing tool).
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import importlib.util
import json
import math
import re
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

# S9's prompts (scripts/wan-decode-ab.py PROMPTS); a test keeps them equal.
PROMPTS = {
    "boat": "a paper boat drifting down a rain gutter, shallow depth of field",
    "fox": "a red fox trotting through fresh snow at dusk, cinematic lighting",
}
CLIPS = {"c1": ("boat", 42), "c2": ("fox", 7)}
SEQUENCES = {"A": ["c1", "c2", "c1"], "B": ["c1", "c2", "c1", "c1"]}
SIZE = "832x480"
WIDTH, HEIGHT = 832, 480
FRAMES = 33
STEPS = 20

# Row gfx1100-xtx-inpod-wan-t2v-gate-2026-11-21, pass rule.
LUMA_STD_MIN = 5.0
LUMA_MEAN_RANGE = (8.0, 247.0)
SAME_SEED_PSNR = 50.0
E2E_PSNR = 22.0
E2E_SSIM = 0.80
DECODE_RATIO = 1.5
DECODE_MAX_S = 15.0
TOTAL_RATIO = 1.25
FIRST_CLIP_MAX_S = 900.0
VRAM_MARGIN_MIB = 1024.0
RUNTIME_PEAK_MAX_GIB = 44.0
A_WARM_TOTAL_RANGE = (105.0, 143.0)

PIN_VARS = ("PYTORCH_ALLOC_CONF", "PYTORCH_HIP_ALLOC_CONF")
BYPASS_LINE = "Wan VAE encode/decode run with MIOpen off"
WARNING_RE = re.compile(r"RuntimeWarning|\bnan\b|\binf\b", re.IGNORECASE)
KERNEL_RE = re.compile(r"amdgpu|\bsmu\b|pcie|\bata1\b|I/O error", re.IGNORECASE)


def iso(t: float) -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(t))


# --------------------------------------------------------------------------
# run (stdlib)
# --------------------------------------------------------------------------


def request_body(model: str, clip: str) -> dict:
    key, seed = CLIPS[clip]
    return {
        "model": model,
        "prompt": PROMPTS[key],
        "size": SIZE,
        "num_frames": FRAMES,
        "num_inference_steps": STEPS,
        "seed": seed,
        "response_format": "b64_json",
    }


def render_one(url, model, clip, auth, timeout):
    body = json.dumps(request_body(model, clip)).encode()
    req = urllib.request.Request(
        url.rstrip("/") + "/v1/videos/generations", data=body, method="POST"
    )
    req.add_header("Content-Type", "application/json")
    if auth:
        req.add_header("Authorization", "Bearer " + auth)
    t0 = time.time()
    rec = {"clip": clip, "seed": CLIPS[clip][1], "start": iso(t0), "t0": t0}
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
        video = base64.b64decode(data["b64_json"])
    except (ValueError, KeyError, IndexError, TypeError) as e:
        rec["status"] = 0
        rec["error"] = "malformed response: " + repr(e)[:200]
        return rec, None
    for k in ("width", "height", "num_frames", "fps", "seed", "mime_type"):
        if k in data:
            rec["resp_" + k] = data[k]
    rec["timings"] = data.get("timings") or {}
    return rec, video


def cmd_run(args) -> int:
    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)
    seq = SEQUENCES[args.arm]
    report = {
        "arm": args.arm,
        "model": args.model,
        "url": args.url,
        "sequence": seq,
        "request": {"size": SIZE, "num_frames": FRAMES, "num_inference_steps": STEPS},
        "t0": time.time(),
        "renders": [],
    }
    report["started_at"] = iso(report["t0"])
    ok = True
    for i, clip in enumerate(seq, 1):
        rec, video = render_one(args.url, args.model, clip, args.auth, args.timeout)
        rec["i"] = i
        if video is not None:
            name = f"{args.arm}-r{i}-{clip}.mp4"
            (out / name).write_bytes(video)
            rec.update(
                file=name, bytes=len(video), sha256=hashlib.sha256(video).hexdigest()
            )
        print(
            json.dumps({k: v for k, v in rec.items() if k not in ("t0", "t1")}),
            flush=True,
        )
        report["renders"].append(rec)
        if video is None:
            ok = False
            break
    report["t1"] = time.time()
    report["finished_at"] = iso(report["t1"])
    (out / f"video-gate-{args.arm}.json").write_text(json.dumps(report, indent=1))
    return 0 if ok else 1


# --------------------------------------------------------------------------
# sample-vram (stdlib + ssh)
# --------------------------------------------------------------------------

REMOTE_SAMPLER = r"""
best=; bt=0
for d in /sys/class/drm/card*/device; do
  t=$(cat "$d/mem_info_vram_total" 2>/dev/null || echo 0)
  if [ "$t" -gt "$bt" ]; then bt=$t; best=$d; fi
done
[ -n "$best" ] || { echo "no card with mem_info_vram_total" >&2; exit 3; }
echo "card $best $bt"
while :; do echo "$(date +%s.%N) $(cat "$best/mem_info_vram_used")"; sleep INTERVAL; done
"""


def parse_sample_line(line: str):
    """'<epoch> <bytes>' -> {"t", "used_mib"}; anything else -> None."""
    parts = line.split()
    if len(parts) != 2:
        return None
    try:
        return {"t": float(parts[0]), "used_mib": round(int(parts[1]) / 1048576, 1)}
    except ValueError:
        return None


def cmd_sample_vram(args) -> int:
    script = REMOTE_SAMPLER.replace("INTERVAL", str(args.interval))
    proc = subprocess.Popen(
        ["ssh", "-o", "ConnectTimeout=10", args.host, script],
        stdout=subprocess.PIPE,
        text=True,
    )
    stop = {"now": False}

    def _stop(*_):
        stop["now"] = True
        proc.terminate()

    signal.signal(signal.SIGTERM, _stop)
    deadline = time.time() + args.duration if args.duration else None
    n = 0
    with open(args.out, "a") as fh:
        try:
            for line in proc.stdout:
                if line.startswith("card "):
                    fh.write(json.dumps({"card": line.split()[1:]}) + "\n")
                    continue
                s = parse_sample_line(line)
                if s:
                    fh.write(json.dumps(s) + "\n")
                    fh.flush()
                    n += 1
                if stop["now"] or (deadline and time.time() > deadline):
                    break
        except KeyboardInterrupt:
            pass
        finally:
            proc.terminate()
    print(f"{n} samples -> {args.out}", file=sys.stderr)
    return 0 if n else 1


def vram_peak(path, t0: float, t1: float):
    """Largest sample in [t0, t1] of a sample-vram JSONL file, or None."""
    peak = None
    for line in Path(path).read_text().splitlines():
        try:
            s = json.loads(line)
        except ValueError:
            continue
        if "t" in s and t0 <= s["t"] <= t1:
            peak = s["used_mib"] if peak is None else max(peak, s["used_mib"])
    return peak


# --------------------------------------------------------------------------
# evidence parsers (stdlib)
# --------------------------------------------------------------------------


def pins_present(env_text: str) -> bool:
    """Both allocator variables set, each without expandable_segments:True."""
    env = {}
    for line in env_text.replace("\0", "\n").splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip()
    return all(
        v in env
        and "expandable_segments:False" in env[v]
        and "expandable_segments:True" not in env[v]
        for v in PIN_VARS
    )


def runtime_log_evidence(text: str, model: str) -> dict:
    """Bypass line present and warning lines counted, on the lane's lines only.

    The runtime pod logs every in-pod model; each line names its model, so a
    line counts only when it contains the lane name.
    """
    lines = [l for l in text.splitlines() if model in l]
    return {
        "bypass_line": any(BYPASS_LINE in l for l in lines),
        "warning_lines": sum(1 for l in lines if WARNING_RE.search(l)),
        "lane_lines": len(lines),
    }


def kernel_line_count(text: str) -> int:
    return sum(1 for l in text.splitlines() if KERNEL_RE.search(l))


# --------------------------------------------------------------------------
# verdict (pure Python)
# --------------------------------------------------------------------------


def sound(r: dict) -> bool:
    m = r.get("media") or {}
    return (
        r.get("status") == 200
        and m.get("codec") == "h264"
        and m.get("frames") == FRAMES
        and m.get("width") == WIDTH
        and m.get("height") == HEIGHT
        and m.get("luma_std_min", 0) >= LUMA_STD_MIN
        and m.get("luma_mean_min", -1) >= LUMA_MEAN_RANGE[0]
        and m.get("luma_mean_max", 999) <= LUMA_MEAN_RANGE[1]
    )


def matches(cmp) -> bool:
    return bool(cmp) and (
        cmp.get("identical") is True or cmp.get("psnr_mean", 0) >= SAME_SEED_PSNR
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
        "cmp": {"A_c1_r3", "B_c1_r3", "B_c1_r4", "e2e_c1", "e2e_c2"},
        "vram": {"A_peak_mib", "B_peak_mib"} or None,
        "runtime_peak_gib": float or None,
        "evidence": {"pins", "bypass_line", "warning_lines",
                     "kernel_lines", "root_rw"} (each may be None)}
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
            "200, h264, 33x480x832, luma std/mean",
        )
    )
    bars.append(
        _bar(
            2,
            "B same-seed c1 clips match",
            matches(cmp.get("B_c1_r3")) and matches(cmp.get("B_c1_r4")),
            [(cmp.get(k) or {}).get("psnr_mean") for k in ("B_c1_r3", "B_c1_r4")],
            f"identical or >= {SAME_SEED_PSNR} dB",
        )
    )
    e2e = [cmp.get("e2e_c1") or {}, cmp.get("e2e_c2") or {}]
    bars.append(
        _bar(
            3,
            "end to end against A",
            all(
                c.get("psnr_mean", 0) >= E2E_PSNR and c.get("ssim_mean", 0) >= E2E_SSIM
                for c in e2e
            ),
            [[c.get("psnr_mean"), c.get("ssim_mean")] for c in e2e],
            f">= {E2E_PSNR} dB and SSIM >= {E2E_SSIM}",
        )
    )
    dec_a = _mean(_timing(A, [1, 2], "decode_postprocess_seconds"))
    dec_b = _mean(_timing(B, [1, 2, 3], "decode_postprocess_seconds"))
    bars.append(
        _bar(
            4,
            "warm decode",
            (
                None
                if dec_a is None or dec_b is None
                else dec_b <= DECODE_RATIO * dec_a and dec_b <= DECODE_MAX_S
            ),
            [dec_b, dec_a],
            f"B <= {DECODE_RATIO} x A and <= {DECODE_MAX_S} s",
        )
    )
    tot_a = _mean(_timing(A, [1, 2], "total_seconds"))
    tot_b = _mean(_timing(B, [1, 2, 3], "total_seconds"))
    bars.append(
        _bar(
            5,
            "warm total",
            None if tot_a is None or tot_b is None else tot_b <= TOTAL_RATIO * tot_a,
            [tot_b, tot_a],
            f"B <= {TOTAL_RATIO} x A",
        )
    )
    first = B[0].get("wall_s") if B else None
    bars.append(
        _bar(
            6,
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
            7,
            "card VRAM peak",
            None if va is None or vb is None else vb <= va + VRAM_MARGIN_MIB,
            [vb, va],
            f"B <= A + {VRAM_MARGIN_MIB} MiB",
        )
    )
    rp = m.get("runtime_peak_gib")
    bars.append(
        _bar(
            8,
            "runtime pod working set",
            None if rp is None else rp < RUNTIME_PEAK_MAX_GIB,
            rp,
            f"< {RUNTIME_PEAK_MAX_GIB} GiB",
        )
    )
    pins, bypass, warns = ev.get("pins"), ev.get("bypass_line"), ev.get("warning_lines")
    bars.append(
        _bar(
            9,
            "pins, bypass line, no warnings",
            (
                None
                if None in (pins, bypass, warns)
                else bool(pins) and bool(bypass) and warns == 0
            ),
            {"pins": pins, "bypass_line": bypass, "warning_lines": warns},
            "both pins, bypass line, 0 warning lines",
        )
    )
    kl, rw = ev.get("kernel_lines"), ev.get("root_rw")
    bars.append(
        _bar(
            10,
            "kernel log and root",
            None if kl is None or rw is None else kl == 0 and bool(rw),
            {"kernel_lines": kl, "root_rw": rw},
            "0 lines, root rw",
        )
    )

    a_warm = _mean(_timing(A, [1, 2], "total_seconds"))
    validity = {
        "A complete and sound": len(A) == len(SEQUENCES["A"])
        and all(sound(r) for r in A),
        "A same-seed c1 match": matches(cmp.get("A_c1_r3")),
        "A warm total in range": a_warm is not None
        and A_WARM_TOTAL_RANGE[0] <= a_warm <= A_WARM_TOTAL_RANGE[1],
    }

    failed = [b["bar"] for b in bars if b["pass"] is False]
    missing = [b["bar"] for b in bars if b["pass"] is None]
    if not all(validity.values()):
        decision = "invalid"
    elif failed == [3] and not missing:
        decision = "conditional"
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
        "a_warm_total_s": a_warm,
        "bars": bars,
    }


# --------------------------------------------------------------------------
# score (numpy + ffmpeg)
# --------------------------------------------------------------------------


def _s9():
    path = Path(__file__).with_name("wan-decode-ab.py")
    spec = importlib.util.spec_from_file_location("wan_decode_ab", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def probe(path: Path) -> dict:
    out = subprocess.run(
        [
            "ffprobe",
            "-v",
            "error",
            "-select_streams",
            "v:0",
            "-count_frames",
            "-show_entries",
            "stream=codec_name,width,height,nb_read_frames",
            "-of",
            "json",
            str(path),
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    s = json.loads(out.stdout)["streams"][0]
    return {
        "codec": s.get("codec_name"),
        "width": int(s["width"]),
        "height": int(s["height"]),
        "frames": int(s.get("nb_read_frames", 0)),
    }


def decode_rgb(path: Path, width: int, height: int):
    import numpy as np

    raw = subprocess.run(
        [
            "ffmpeg",
            "-v",
            "error",
            "-i",
            str(path),
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgb24",
            "-",
        ],
        capture_output=True,
        check=True,
    ).stdout
    return np.frombuffer(raw, dtype=np.uint8).reshape(-1, height, width, 3)


def load_arm(d: Path, arm: str, s9) -> tuple[dict, dict]:
    report = json.loads((d / f"video-gate-{arm}.json").read_text())
    frames = {}
    for r in report["renders"]:
        if r.get("status") != 200 or not r.get("file"):
            continue
        p = d / r["file"]
        info = probe(p)
        f = decode_rgb(p, info["width"], info["height"])
        stats = s9.frame_stats(f)
        info.update(
            {k: stats[k] for k in ("luma_mean_min", "luma_mean_max", "luma_std_min")}
        )
        r["media"] = info
        frames[r["i"]] = f
    return report, frames


def _compare(s9, ra, fa, i, rb, fb, j):
    if i not in fa or j not in fb:
        return None
    sa = ra["renders"][i - 1].get("sha256")
    sb = rb["renders"][j - 1].get("sha256")
    if sa and sa == sb:
        return {
            "identical": True,
            "psnr_mean": math.inf,
            "ssim_mean": 1.0,
            "same_bytes": True,
        }
    c = s9.compare_frames(fa[i], fb[j])
    return {
        k: c.get(k)
        for k in (
            "identical",
            "psnr_mean",
            "psnr_min",
            "ssim_mean",
            "ssim_min",
            "error",
        )
    }


def cmd_score(args) -> int:
    for tool in ("ffmpeg", "ffprobe"):
        if not shutil.which(tool):
            print(f"{tool} not found", file=sys.stderr)
            return 2
    s9 = _s9()
    ra, fa = load_arm(Path(args.a_dir), "A", s9)
    rb, fb = load_arm(Path(args.b_dir), "B", s9)
    cmp = {
        "A_c1_r3": _compare(s9, ra, fa, 1, ra, fa, 3),
        "B_c1_r3": _compare(s9, rb, fb, 1, rb, fb, 3),
        "B_c1_r4": _compare(s9, rb, fb, 1, rb, fb, 4),
        "e2e_c1": _compare(s9, ra, fa, 1, rb, fb, 1),
        "e2e_c2": _compare(s9, ra, fa, 2, rb, fb, 2),
    }
    vram = None
    if args.vram_a and args.vram_b:
        vram = {
            "A_peak_mib": vram_peak(args.vram_a, ra["t0"], ra["t1"]),
            "B_peak_mib": vram_peak(args.vram_b, rb["t0"], rb["t1"]),
        }
    ev = {
        "pins": None,
        "bypass_line": None,
        "warning_lines": None,
        "kernel_lines": None,
        "root_rw": None,
    }
    if args.process_env:
        ev["pins"] = pins_present(Path(args.process_env).read_text(errors="replace"))
    if args.runtime_log:
        rl = runtime_log_evidence(
            Path(args.runtime_log).read_text(errors="replace"), rb["model"]
        )
        ev.update(bypass_line=rl["bypass_line"], warning_lines=rl["warning_lines"])
    if args.kernel_log:
        ev["kernel_lines"] = kernel_line_count(
            Path(args.kernel_log).read_text(errors="replace")
        )
    if args.root_mode:
        ev["root_rw"] = args.root_mode == "rw"
    metrics = {
        "A": ra,
        "B": rb,
        "cmp": cmp,
        "vram": vram,
        "runtime_peak_gib": args.runtime_peak_gib,
        "evidence": ev,
    }
    result = verdict(metrics)
    result["metrics"] = {
        "cmp": cmp,
        "vram": vram,
        "evidence": ev,
        "A": [
            {
                k: r.get(k)
                for k in ("i", "clip", "status", "wall_s", "timings", "media", "sha256")
            }
            for r in ra["renders"]
        ],
        "B": [
            {
                k: r.get(k)
                for k in ("i", "clip", "status", "wall_s", "timings", "media", "sha256")
            }
            for r in rb["renders"]
        ],
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
    v = sub.add_parser("sample-vram", help="sample card VRAM over ssh into JSONL")
    v.add_argument("--host", required=True)
    v.add_argument("--out", required=True)
    v.add_argument("--interval", type=float, default=2.0)
    v.add_argument(
        "--duration", type=float, default=0.0, help="seconds; 0 = until stopped"
    )
    s = sub.add_parser("score", help="judge arms A and B (numpy + ffmpeg)")
    s.add_argument("a_dir")
    s.add_argument("b_dir")
    s.add_argument("--vram-a")
    s.add_argument("--vram-b")
    s.add_argument("--runtime-peak-gib", type=float)
    s.add_argument("--runtime-log", help="runtime pod log covering arm B")
    s.add_argument("--process-env", help="/proc/<pid>/environ of the in-pod server")
    s.add_argument("--kernel-log", help="GPU host kernel log over both arms")
    s.add_argument("--root-mode", choices=["rw", "ro"])
    s.add_argument("--json", help="write the verdict JSON here")
    args = ap.parse_args(argv)
    try:
        return {"run": cmd_run, "sample-vram": cmd_sample_vram, "score": cmd_score}[
            args.cmd
        ](args)
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as e:
        print(f"error: {e!r}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())

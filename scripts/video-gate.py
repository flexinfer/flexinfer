#!/usr/bin/env python3
"""Video gate: is a Wan video lane sound after a load, and as fast?

The live gate of row gfx1100-xtx-inpod-wan-t2v-gate-2026-11-21 in
.loom/60-validation-matrix.md (Draft !1237: Wan 2.1 T2V in-pod on the XTX
runtime with the allocator pinned). S9 found that expandable_segments with
model CPU offload corrupts every Wan VAE use after the first in a process, so
the in-pod arm renders four clips in one process; the dedicated lane renders
three as the control.

Two profiles (--profile on run; score reads it from the arm reports):

  wan-t2v   (default) Wan 2.1 T2V, the prompts and bars above.
  vace-i2v  Wan 2.1 VACE image-to-video (Draft !1238). Every request also
            sends one conditioning image: a deterministic 832x480 PNG the
            harness draws (or --image), saved as condition.png beside the
            clips with its sha256 in the report. The fp32 VACE VAE is not
            covered by the MIOpen bypass, so bar (9) does not need the bypass
            line and bar (4) has no absolute decode cap. The dedicated VACE
            lane is not byte-repeatable within a load (S9: 37.3 dB between
            same-seed clips), so A's same-seed check needs 35 dB and bar (2)
            needs B to repeat no worse than A. Row
            gfx1100-xtx-inpod-wan-vace-gate-2026-11-22 holds these bars.
            Score also reports how close each clip's first frame stays to the
            conditioning image.

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
# VACE image-to-video: both clips start from the one conditioning image.
VACE_PROMPTS = {
    "dusk": "the sun sinks behind the hills as the sky slowly turns orange",
    "breeze": "a light breeze ripples the lake while the camera pans slowly right",
}
VACE_CLIPS = {"c1": ("dusk", 42), "c2": ("breeze", 7)}
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

# VACE control figures from S9 (row gfx1100-wan-decode-ab-v032-2026-11-05,
# arm vace-A, dedicated diffusers-video on the XTX): warm total 264.8 s, and
# the two same-seed clips of one load matched at only 37.3 dB (SSIM 0.993),
# so the dedicated VACE lane is not byte-repeatable within a load.
VACE_A_WARM_TOTAL_RANGE = (185.0, 345.0)  # 0.7x to 1.3x of 264.8 s
VACE_A_SAME_SEED_MIN_DB = 35.0

# Per-profile prompts and the bars that differ. a_warm_total_range None skips
# that validity check (score --a-warm-range overrides it). b_same_seed_vs_a:
# bar (2) passes at 50 dB, or at the control's own same-seed PSNR when that is
# lower, so B must repeat no worse than the dedicated lane does.
PROFILES = {
    "wan-t2v": {
        "prompts": PROMPTS,
        "clips": CLIPS,
        "image": False,
        "bypass_required": True,
        "decode_max_s": DECODE_MAX_S,
        "a_warm_total_range": A_WARM_TOTAL_RANGE,
        "a_same_seed_min_db": SAME_SEED_PSNR,
        "b_same_seed_vs_a": False,
    },
    "vace-i2v": {
        "prompts": VACE_PROMPTS,
        "clips": VACE_CLIPS,
        "image": True,
        "bypass_required": False,
        "decode_max_s": None,
        "a_warm_total_range": VACE_A_WARM_TOTAL_RANGE,
        "a_same_seed_min_db": VACE_A_SAME_SEED_MIN_DB,
        "b_same_seed_vs_a": True,
    },
}
CONDITION_FILE = "condition.png"

PIN_VARS = ("PYTORCH_ALLOC_CONF", "PYTORCH_HIP_ALLOC_CONF")
BYPASS_LINE = "Wan VAE encode/decode run with MIOpen off"
WARNING_RE = re.compile(r"RuntimeWarning|\bnan\b|\binf\b", re.IGNORECASE)
KERNEL_RE = re.compile(r"amdgpu|\bsmu\b|pcie|\bata1\b|I/O error", re.IGNORECASE)


def iso(t: float) -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(t))


# --------------------------------------------------------------------------
# run (stdlib)
# --------------------------------------------------------------------------


def condition_png(width: int = WIDTH, height: int = HEIGHT) -> bytes:
    """The VACE conditioning image: a fixed landscape drawn in stdlib.

    Sky gradient, a sun disc, two hill bands and a lake, so the clip has
    edges and flat areas to keep. The pixels are the same on every host (a
    test pins their hash); the compressed bytes can differ between zlib
    builds, so run both arms from one host and score checks the two arms'
    PNG sha256 match.
    """
    import struct
    import zlib

    far = [
        height * 0.55 - 40 * math.sin(x / width * math.pi * 2.0) for x in range(width)
    ]
    near = [
        height * 0.68 - 25 * math.sin(x / width * math.pi * 3.0 + 1.0)
        for x in range(width)
    ]
    sun_x, sun_y, sun_r2 = int(width * 0.7), int(height * 0.3), (height // 10) ** 2
    lake_y = height * 0.8
    rows = []
    for y in range(height):
        row = bytearray(b"\x00")  # PNG filter type 0
        t = y / (height - 1)
        sky = bytes((int(90 + 120 * t), int(140 + 80 * t), int(220 - 40 * t)))
        if y >= lake_y:
            d = (y - lake_y) / (height - lake_y)
            row += bytes((30, int(70 + 40 * d), int(120 + 60 * d))) * width
            rows.append(bytes(row))
            continue
        for x in range(width):
            if y >= near[x]:
                row += b"\x28\x5f\x23"  # near hill (40, 95, 35)
            elif y >= far[x]:
                row += b"\x46\x78\x46"  # far hill (70, 120, 70)
            elif (x - sun_x) ** 2 + (y - sun_y) ** 2 <= sun_r2:
                row += b"\xfa\xd7\x5a"  # sun (250, 215, 90)
            else:
                row += sky
        rows.append(bytes(row))
    raw = zlib.compress(b"".join(rows), 9)

    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body))

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", raw)
        + chunk(b"IEND", b"")
    )


def request_body(
    model: str, clip: str, profile: str = "wan-t2v", image_b64: str | None = None
) -> dict:
    prof = PROFILES[profile]
    key, seed = prof["clips"][clip]
    body = {
        "model": model,
        "prompt": prof["prompts"][key],
        "size": SIZE,
        "num_frames": FRAMES,
        "num_inference_steps": STEPS,
        "seed": seed,
        "response_format": "b64_json",
    }
    if prof["image"]:
        if not image_b64:
            raise ValueError(f"profile {profile} needs a conditioning image")
        body["image"] = image_b64
    return body


def render_one(url, model, clip, auth, timeout, profile="wan-t2v", image_b64=None):
    body = json.dumps(request_body(model, clip, profile, image_b64)).encode()
    req = urllib.request.Request(
        url.rstrip("/") + "/v1/videos/generations", data=body, method="POST"
    )
    req.add_header("Content-Type", "application/json")
    if auth:
        req.add_header("Authorization", "Bearer " + auth)
    t0 = time.time()
    seed = PROFILES[profile]["clips"][clip][1]
    rec = {"clip": clip, "seed": seed, "start": iso(t0), "t0": t0}
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
    profile = getattr(args, "profile", "wan-t2v")
    report = {
        "arm": args.arm,
        "profile": profile,
        "model": args.model,
        "url": args.url,
        "sequence": seq,
        "request": {"size": SIZE, "num_frames": FRAMES, "num_inference_steps": STEPS},
        "t0": time.time(),
        "renders": [],
    }
    image_b64 = None
    if PROFILES[profile]["image"]:
        image = (
            Path(args.image).read_bytes()
            if getattr(args, "image", None)
            else condition_png()
        )
        (out / CONDITION_FILE).write_bytes(image)
        report["image_file"] = CONDITION_FILE
        report["image_sha256"] = hashlib.sha256(image).hexdigest()
        image_b64 = base64.b64encode(image).decode()
    report["started_at"] = iso(report["t0"])
    ok = True
    for i, clip in enumerate(seq, 1):
        rec, video = render_one(
            args.url, args.model, clip, args.auth, args.timeout, profile, image_b64
        )
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


def matches(cmp, floor: float = SAME_SEED_PSNR, ssim_floor: float = 0.0) -> bool:
    return bool(cmp) and (
        cmp.get("identical") is True
        or (
            (cmp.get("psnr_mean") or 0) >= floor
            and (cmp.get("ssim_mean") or 0) >= ssim_floor
        )
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
    prof = PROFILES[m.get("profile", "wan-t2v")]
    decode_max = prof["decode_max_s"]
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
    b_floor = SAME_SEED_PSNR
    a_same = cmp.get("A_c1_r3") or {}
    if (
        prof["b_same_seed_vs_a"]
        and a_same.get("identical") is not True
        and isinstance(a_same.get("psnr_mean"), (int, float))
    ):
        b_floor = min(SAME_SEED_PSNR, a_same["psnr_mean"])
    b_ssim_floor = 0.0
    limit = f"identical or >= {b_floor} dB" + (
        " (A's own same-seed PSNR)" if b_floor < SAME_SEED_PSNR else ""
    )
    # A row may set its own repeat floors (score --b-repeat-psnr-min and
    # --b-repeat-ssim-min), replacing the rule above: each same-seed pair is
    # byte-identical or reaches both floors.
    repeat = m.get("b_repeat") or {}
    if repeat.get("psnr_min") is not None or repeat.get("ssim_min") is not None:
        b_floor = repeat.get("psnr_min") or 0.0
        b_ssim_floor = repeat.get("ssim_min") or 0.0
        limit = (
            f"identical or (PSNR >= {b_floor} dB and SSIM >= {b_ssim_floor}) "
            "(row-set repeat floors)"
        )
    pairs = [cmp.get(k) or {} for k in ("B_c1_r3", "B_c1_r4")]
    bars.append(
        _bar(
            2,
            "B same-seed c1 clips match",
            all(matches(c, b_floor, b_ssim_floor) for c in pairs),
            [[c.get("psnr_mean"), c.get("ssim_mean")] for c in pairs],
            limit,
        )
    )
    e2e = [cmp.get("e2e_c1") or {}, cmp.get("e2e_c2") or {}]
    # A row may drop the PSNR floor (e2e_psnr_min 0) and judge bar (3) on SSIM
    # plus the owner's side-by-side look: two different stacks land at
    # 19-24 dB whatever is fixed (row gfx1100-xtx-inpod-wan-t2v-warmup-gate).
    psnr_min = m.get("e2e_psnr_min", E2E_PSNR)
    bars.append(
        _bar(
            3,
            "end to end against A",
            all(
                (c.get("psnr_mean") or 0) >= psnr_min
                and (c.get("ssim_mean") or 0) >= E2E_SSIM
                and c.get("psnr_mean") is not None
                for c in e2e
            ),
            [[c.get("psnr_mean"), c.get("ssim_mean")] for c in e2e],
            (
                f">= {psnr_min} dB and SSIM >= {E2E_SSIM}"
                if psnr_min > 0
                else f"SSIM >= {E2E_SSIM} (PSNR reported, not judged)"
            ),
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
                else dec_b <= DECODE_RATIO * dec_a
                and (decode_max is None or dec_b <= decode_max)
            ),
            [dec_b, dec_a],
            f"B <= {DECODE_RATIO} x A"
            + (f" and <= {decode_max} s" if decode_max is not None else ""),
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
    need_bypass = prof["bypass_required"]
    needed = (pins, bypass, warns) if need_bypass else (pins, warns)
    bars.append(
        _bar(
            9,
            "pins, bypass line, no warnings" if need_bypass else "pins, no warnings",
            (
                None
                if None in needed
                else bool(pins) and (bool(bypass) or not need_bypass) and warns == 0
            ),
            {"pins": pins, "bypass_line": bypass, "warning_lines": warns},
            (
                "both pins, bypass line, 0 warning lines"
                if need_bypass
                else "both pins, 0 warning lines (bypass line reported, not needed)"
            ),
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
        "A same-seed c1 match": matches(cmp.get("A_c1_r3"), prof["a_same_seed_min_db"]),
    }
    warm_range = m.get("a_warm_total_range") or prof["a_warm_total_range"]
    if warm_range:
        validity["A warm total in range"] = (
            a_warm is not None and warm_range[0] <= a_warm <= warm_range[1]
        )
    if prof["image"]:
        validity["A and B conditioned on one image"] = bool(
            m.get("image_sha256_a")
            and m.get("image_sha256_a") == m.get("image_sha256_b")
        )

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


def decode_image(path: Path, width: int, height: int):
    """The conditioning image as one RGB frame at the clip size (ffmpeg lanczos,
    as the server resizes with PIL LANCZOS)."""
    import numpy as np

    raw = subprocess.run(
        [
            "ffmpeg",
            "-v",
            "error",
            "-i",
            str(path),
            "-vf",
            f"scale={width}:{height}:flags=lanczos",
            "-frames:v",
            "1",
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgb24",
            "-",
        ],
        capture_output=True,
        check=True,
    ).stdout
    return np.frombuffer(raw, dtype=np.uint8).reshape(1, height, width, 3)


def first_frame_fidelity(s9, report, frames, d: Path):
    """Per render: PSNR/SSIM of the clip's first frame against the image."""
    name = report.get("image_file")
    if not name or not (d / name).exists():
        return None
    cond = decode_image(d / name, WIDTH, HEIGHT)
    out = {}
    for i, f in sorted(frames.items()):
        if f.shape[1:3] != cond.shape[1:3]:
            continue
        c = s9.compare_frames(cond, f[:1])
        out[i] = {k: c.get(k) for k in ("psnr_mean", "ssim_mean")}
    return out


def cmd_score(args) -> int:
    for tool in ("ffmpeg", "ffprobe"):
        if not shutil.which(tool):
            print(f"{tool} not found", file=sys.stderr)
            return 2
    s9 = _s9()
    ra, fa = load_arm(Path(args.a_dir), "A", s9)
    rb, fb = load_arm(Path(args.b_dir), "B", s9)
    profile = ra.get("profile", "wan-t2v")
    if rb.get("profile", "wan-t2v") != profile:
        print(
            f"arm profiles differ: A {profile}, B {rb.get('profile', 'wan-t2v')}",
            file=sys.stderr,
        )
        return 2
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
    fidelity = None
    if PROFILES[profile]["image"]:
        fidelity = {
            "A": first_frame_fidelity(s9, ra, fa, Path(args.a_dir)),
            "B": first_frame_fidelity(s9, rb, fb, Path(args.b_dir)),
        }
    metrics = {
        "A": ra,
        "B": rb,
        "profile": profile,
        "image_sha256_a": ra.get("image_sha256"),
        "image_sha256_b": rb.get("image_sha256"),
        "a_warm_total_range": args.a_warm_range,
        "e2e_psnr_min": args.e2e_psnr_min,
        "b_repeat": {
            "psnr_min": args.b_repeat_psnr_min,
            "ssim_min": args.b_repeat_ssim_min,
        },
        "cmp": cmp,
        "vram": vram,
        "runtime_peak_gib": args.runtime_peak_gib,
        "evidence": ev,
    }
    result = verdict(metrics)
    result["profile"] = profile
    result["metrics"] = {
        "cmp": cmp,
        "vram": vram,
        "evidence": ev,
        "first_frame_vs_image": fidelity,
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
    r.add_argument("--profile", choices=sorted(PROFILES), default="wan-t2v")
    r.add_argument(
        "--image",
        help="vace-i2v: conditioning image file (default: the harness's own PNG)",
    )
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
    s.add_argument(
        "--e2e-psnr-min",
        type=float,
        default=E2E_PSNR,
        help="bar (3) PSNR floor in dB; 0 judges bar (3) on SSIM alone, as "
        "row gfx1100-xtx-inpod-wan-t2v-warmup-gate-2026-11-21 does",
    )
    s.add_argument(
        "--b-repeat-psnr-min",
        type=float,
        help="bar (2): a row-set PSNR floor (dB) for B's same-seed pairs, "
        "replacing the profile rule; used with --b-repeat-ssim-min",
    )
    s.add_argument(
        "--b-repeat-ssim-min",
        type=float,
        help="bar (2): a row-set SSIM floor for B's same-seed pairs",
    )
    s.add_argument(
        "--a-warm-range",
        type=float,
        nargs=2,
        metavar=("LO", "HI"),
        help="validity range of A's warm total in seconds (default: the " "profile's)",
    )
    args = ap.parse_args(argv)
    if args.cmd == "score" and args.a_warm_range:
        args.a_warm_range = list(args.a_warm_range)
    try:
        return {"run": cmd_run, "sample-vram": cmd_sample_vram, "score": cmd_score}[
            args.cmd
        ](args)
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as e:
        print(f"error: {e!r}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())

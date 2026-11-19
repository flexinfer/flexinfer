#!/usr/bin/env python3
"""Render gate: are an image lane's first renders after a load sound?

The multi-render gate from the 2026-11-19 D3 run (row
gfx1100-xtx-inpod-qwen-image-gate-2026-11-19 in .loom/60-validation-matrix.md),
made repeatable. Through any OpenAI-compatible /v1/images/generations endpoint
(flexinfer-proxy or a lane's own Service), back to back in one process:

  render 1..N-1  seeds S, S+1, ..., S+N-2
  render N       seed S again

then score every PNG and compare the two seed-S renders. Run it right after a
load: on 2026-11-19 only the first render after the load was garbage.

Checks per image:
  http_200            the request returned 200 with an image
  decodes             Pillow opens the PNG
  size                the image is --size
  mode                RGB, or RGBA (Qwen-Image 2.1 returns RGBA even when sound;
                      --require-rgb rejects any alpha channel)
  opaque              at most MAX_TRANSPARENT_SHARE of the pixels have alpha < 128
                      (--allow-transparent skips it)
  channel_std         every RGB channel's standard deviation >= MIN_CHANNEL_STD
  channel_mean        every RGB channel's mean within MEAN_RANGE
  flat_tiles          at most MAX_FLAT_TILE_SHARE of the 64x64 blocks are flat
                      (largest per-channel standard deviation in the block below
                      FLAT_TILE_STD); --max-flat-tile-share changes the share
Check across images:
  same_seed_match     the seed-S renders are identical bytes or differ by at most
                      MAX_SAME_SEED_DIFF on every pixel and channel

The same-seed check assumes both seed-S renders ran on the same lane and GPU.
Through the proxy, name a model that only one lane serves; two lanes or two
cards can differ legitimately and fail it.

opaque and flat_tiles were calibrated on the default prompt, a busy photographic
scene. A custom --prompt that asks for a plain or transparent image (a logo on a
flat background, a sticker with an alpha cutout) can fail them while sound; use
--max-flat-tile-share and --allow-transparent for such prompts, and expect them
to catch less.

The 2026-11-19 garbage passed channel_std and channel_mean (that is why this
gate exists). It fails opaque and flat_tiles by a wide margin: 91-95 % of its
pixels were transparent and 93-96 % of its blocks flat, against 0 % and at most
0.4 % for the sound renders of that day.

    python3 scripts/render-gate.py --url http://flexinfer-proxy.flexinfer-system.svc \\
        --model qwen-image-21-gfx1100 --seed 20261119 --out-dir gate-B
    python3 scripts/render-gate.py --verdict gate-B/render-gate.json
    python3 scripts/render-gate.py --score some.png other.png

Stdlib plus Pillow. Exit status 0 when the gate passes, 1 when it fails, 2 on
an error (bad arguments, an unwritable --out-dir, an unreadable or malformed
report, a missing file to score).
"""

import argparse
import base64
import hashlib
import json
import os
import sys
import time
import urllib.error
import urllib.request

from PIL import Image, ImageChops, ImageStat

MIN_CHANNEL_STD = 10.0  # the 2026-11-19 rule, kept
MEAN_RANGE = (5.0, 250.0)  # the 2026-11-19 rule, kept
TILE = 64
# A block is flat when even its busiest RGB channel varies by less than one
# level. A VAE decode leaves sub-level texture almost everywhere, so a sound
# render has very few such blocks (0 to 0.4 % on 2026-11-19 at 1024x1024),
# while the tiled garbage had 93 to 96 %.
FLAT_TILE_STD = 1.0
# 25 %: far below the garbage, and enough room for a sound image with a plain
# backdrop or a flat graphic area covering a quarter of the frame.
MAX_FLAT_TILE_SHARE = 0.25
# A text-to-image render has no business being transparent. The sound RGBA
# renders of 2026-11-19 had no pixel below alpha 128; the garbage had 91-95 %.
MAX_TRANSPARENT_SHARE = 0.01
MAX_SAME_SEED_DIFF = 2

DEFAULT_PROMPT = (
    "a lighthouse on a basalt cliff at dusk, long exposure, crisp detail, "
    "warm window light, waves breaking on dark rocks"
)


def now_utc():
    return time.strftime("%H:%M:%SZ", time.gmtime())


def seeds_for(seed, renders):
    """S, S+1, ..., S+N-2, then S again."""
    if renders < 2:
        raise ValueError("--renders must be at least 2 (the last one repeats seed S)")
    return [seed + i for i in range(renders - 1)] + [seed]


# ── rendering ─────────────────────────────────────────────────────


def render_one(url, model, prompt, size, seed, auth, timeout):
    """POST one generation. Returns (status, png bytes or None, error text)."""
    body = {
        "model": model,
        "prompt": prompt,
        "size": size,
        "seed": seed,
        "n": 1,
        "response_format": "b64_json",
    }
    req = urllib.request.Request(
        url + "/v1/images/generations", data=json.dumps(body).encode(), method="POST"
    )
    req.add_header("Content-Type", "application/json")
    if auth:
        req.add_header("Authorization", "Bearer " + auth)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            status = resp.status
    except urllib.error.HTTPError as e:
        return e.code, None, e.read()[:500].decode(errors="replace")
    except Exception as e:  # noqa: BLE001 - every transport failure is evidence
        return 0, None, f"{type(e).__name__}: {e}"
    try:
        png = base64.b64decode(json.loads(raw)["data"][0]["b64_json"])
    except (ValueError, KeyError, IndexError, TypeError) as e:
        return status, None, f"no image in the response: {type(e).__name__}: {e}"
    return status, png, None


def run(args):
    os.makedirs(args.out_dir, exist_ok=True)
    report = {
        "args": {
            "url": args.url,
            "model": args.model,
            "prompt": args.prompt,
            "size": args.size,
            "seed": args.seed,
            "renders": args.renders,
            "require_rgb": args.require_rgb,
            "max_flat_tile_share": args.max_flat_tile_share,
            "allow_transparent": args.allow_transparent,
        },
        "renders": [],
    }
    for i, seed in enumerate(seeds_for(args.seed, args.renders), start=1):
        start = now_utc()
        t0 = time.time()
        status, png, error = render_one(
            args.url, args.model, args.prompt, args.size, seed, args.auth, args.timeout
        )
        entry = {
            "i": i,
            "seed": seed,
            "start": start,
            "status": status,
            "wall_s": round(time.time() - t0, 1),
        }
        if png is not None:
            name = f"r{i}-seed{seed}.png"
            with open(os.path.join(args.out_dir, name), "wb") as f:
                f.write(png)
            entry.update(
                file=name, bytes=len(png), sha256=hashlib.sha256(png).hexdigest()
            )
        if error:
            entry["error"] = error
        report["renders"].append(entry)
        print(json.dumps(entry), flush=True)
    return report


# ── scoring ───────────────────────────────────────────────────────


class GateError(Exception):
    """An error that is not a gate failure; main() exits 2 with its message."""


def flat_tile_share(rgb):
    """Share of TILE x TILE blocks whose busiest channel has std < FLAT_TILE_STD.
    Only whole blocks count; an image smaller than one block is one block."""
    w, h = rgb.size
    boxes = [
        (x, y, x + TILE, y + TILE)
        for y in range(0, h - TILE + 1, TILE)
        for x in range(0, w - TILE + 1, TILE)
    ] or [(0, 0, w, h)]
    flat = sum(
        1 for box in boxes if max(ImageStat.Stat(rgb.crop(box)).stddev) < FLAT_TILE_STD
    )
    return flat / len(boxes)


def score_image(
    path,
    expected_size=None,
    require_rgb=False,
    status=None,
    max_flat_share=MAX_FLAT_TILE_SHARE,
    allow_transparent=False,
):
    """Return (checks, metrics) for one PNG. A check is True, False, or None
    (not applicable, e.g. no HTTP status when scoring a file by hand)."""
    checks = {"http_200": None if status is None else status == 200}
    metrics = {}
    try:
        with Image.open(path) as im:
            im.load()
            image = im.copy()
    except Exception as e:  # noqa: BLE001 - any decode failure fails the image
        checks["decodes"] = False
        metrics["decode_error"] = f"{type(e).__name__}: {e}"
        return checks, metrics
    checks["decodes"] = True
    metrics["mode"] = image.mode
    metrics["size"] = f"{image.width}x{image.height}"
    checks["size"] = None if not expected_size else metrics["size"] == expected_size
    allowed = ("RGB",) if require_rgb else ("RGB", "RGBA")
    checks["mode"] = image.mode in allowed
    pixels = image.width * image.height
    if "A" in image.getbands():
        histogram = image.getchannel("A").histogram()
        transparent = sum(histogram[:128]) / pixels
    else:
        transparent = 0.0
    metrics["transparent_share"] = round(transparent, 4)
    checks["opaque"] = (
        None if allow_transparent else transparent <= MAX_TRANSPARENT_SHARE
    )
    rgb = image.convert("RGB")
    stat = ImageStat.Stat(rgb)
    metrics["channel_std"] = [round(v, 2) for v in stat.stddev]
    metrics["channel_mean"] = [round(v, 2) for v in stat.mean]
    checks["channel_std"] = all(v >= MIN_CHANNEL_STD for v in stat.stddev)
    lo, hi = MEAN_RANGE
    checks["channel_mean"] = all(lo <= v <= hi for v in stat.mean)
    share = flat_tile_share(rgb)
    metrics["flat_tile_share"] = round(share, 4)
    checks["flat_tiles"] = share <= max_flat_share
    return checks, metrics


def max_pixel_diff(path_a, path_b):
    """Largest absolute per-channel difference, or None if the images cannot be
    compared (missing, undecodable, or different size or mode)."""
    try:
        with Image.open(path_a) as a, Image.open(path_b) as b:
            a.load()
            b.load()
            if a.size != b.size or a.mode != b.mode:
                return None
            extrema = ImageChops.difference(a, b).getextrema()
    except Exception:  # noqa: BLE001
        return None
    if isinstance(extrema[0], tuple):
        return max(hi for _, hi in extrema)
    return extrema[1]


def file_sha256(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def verdict(report, base_dir):
    """Score the files a report names. Returns (pass, result dict)."""
    args = report.get("args", {})
    expected = args.get("size")
    options = dict(
        require_rgb=bool(args.get("require_rgb")),
        max_flat_share=args.get("max_flat_tile_share", MAX_FLAT_TILE_SHARE),
        allow_transparent=bool(args.get("allow_transparent")),
    )
    images = []
    for r in report["renders"]:
        path = os.path.join(base_dir, r["file"]) if r.get("file") else None
        if path and os.path.exists(path):
            checks, metrics = score_image(
                path, expected, status=r.get("status"), **options
            )
        else:
            checks = {"http_200": r.get("status") == 200, "decodes": False}
            metrics = {"error": r.get("error")}
        failed = [k for k, v in checks.items() if v is False]
        images.append(
            {
                "i": r["i"],
                "seed": r["seed"],
                "file": r.get("file"),
                "pass": not failed,
                "failed": failed,
                "checks": checks,
                "metrics": metrics,
            }
        )
    same_seed = []
    by_seed = {}
    for r in report["renders"]:
        by_seed.setdefault(r["seed"], []).append(r)
    for seed, group in sorted(by_seed.items()):
        for other in group[1:]:
            first = group[0]
            pair = {"seed": seed, "renders": [first["i"], other["i"]]}
            paths = [
                os.path.join(base_dir, x["file"]) if x.get("file") else None
                for x in (first, other)
            ]
            if not all(p and os.path.exists(p) for p in paths):
                pair.update(identical=False, max_diff=None, pass_=False)
            else:
                identical = file_sha256(paths[0]) == file_sha256(paths[1])
                diff = 0 if identical else max_pixel_diff(*paths)
                pair.update(
                    identical=identical,
                    max_diff=diff,
                    pass_=diff is not None and diff <= MAX_SAME_SEED_DIFF,
                )
            pair["pass"] = pair.pop("pass_")
            same_seed.append(pair)
    ok = (
        bool(images)
        and all(i["pass"] for i in images)
        and bool(same_seed)
        and all(p["pass"] for p in same_seed)
    )
    return ok, {"pass": ok, "images": images, "same_seed": same_seed}


def score_files(paths, expected_size, **options):
    results = []
    for path in paths:
        if not os.path.isfile(path):
            raise GateError(f"no such file to score: {path}")
        checks, metrics = score_image(path, expected_size, **options)
        failed = [k for k, v in checks.items() if v is False]
        results.append(
            {
                "file": path,
                "pass": not failed,
                "failed": failed,
                "checks": checks,
                "metrics": metrics,
            }
        )
    ok = all(r["pass"] for r in results)
    return ok, {"pass": ok, "images": results}


def load_report(path):
    try:
        with open(path) as f:
            report = json.load(f)
    except OSError as e:
        raise GateError(f"cannot read {path}: {e.strerror or e}")
    except ValueError as e:
        raise GateError(f"{path} is not valid JSON: {e}")
    renders = report.get("renders") if isinstance(report, dict) else None
    if not isinstance(renders, list) or not all(
        isinstance(r, dict) and "i" in r and "seed" in r for r in renders
    ):
        raise GateError(f"{path} is not a render-gate report (no renders list)")
    return report


def main(argv=None):
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("--url", help="base URL of the proxy or lane (no /v1 suffix)")
    ap.add_argument("--auth", default="", help="bearer token")
    ap.add_argument(
        "--model",
        help="model name; for the same-seed check it must be served by one lane",
    )
    ap.add_argument("--prompt", default=DEFAULT_PROMPT)
    ap.add_argument(
        "--size", help="WIDTHxHEIGHT; 1024x1024 when rendering, unchecked with --score"
    )
    ap.add_argument("--seed", type=int, default=20261119)
    ap.add_argument("--renders", type=int, default=4)
    ap.add_argument(
        "--timeout", type=float, default=1800, help="per-request timeout, seconds"
    )
    ap.add_argument("--require-rgb", action="store_true", help="reject RGBA output")
    ap.add_argument(
        "--max-flat-tile-share",
        type=float,
        default=MAX_FLAT_TILE_SHARE,
        help="largest share of flat 64x64 blocks allowed (default %(default)s); "
        "raise it for prompts that ask for plain backgrounds",
    )
    ap.add_argument(
        "--allow-transparent",
        action="store_true",
        help="skip the opaque check, for prompts that ask for transparency",
    )
    ap.add_argument("--out-dir", default="render-gate")
    ap.add_argument("--verdict", metavar="REPORT_JSON", help="re-score a saved run")
    ap.add_argument("--score", nargs="+", metavar="PNG", help="score files by hand")
    args = ap.parse_args(argv)
    if not 0 <= args.max_flat_tile_share <= 1:
        ap.error("--max-flat-tile-share must be between 0 and 1")
    try:
        if args.verdict:
            report = load_report(args.verdict)
            ok, result = verdict(report, os.path.dirname(os.path.abspath(args.verdict)))
        elif args.score:
            ok, result = score_files(
                args.score,
                args.size,
                require_rgb=args.require_rgb,
                max_flat_share=args.max_flat_tile_share,
                allow_transparent=args.allow_transparent,
            )
        else:
            for need in ("url", "model"):
                if not getattr(args, need):
                    ap.error(f"--{need} is required")
            try:
                seeds_for(args.seed, args.renders)
            except ValueError as e:
                ap.error(str(e))
            args.size = args.size or "1024x1024"
            args.url = args.url.rstrip("/")
            path = os.path.join(args.out_dir, "render-gate.json")
            try:
                os.makedirs(args.out_dir, exist_ok=True)
                with open(path, "w") as f:  # fail before the first render
                    f.write("{}\n")
                report = run(args)
                with open(path, "w") as f:
                    json.dump(report, f, indent=1)
            except OSError as e:
                raise GateError(f"cannot write to {args.out_dir}: {e.strerror or e}")
            ok, result = verdict(report, args.out_dir)
            result["report"] = path
    except GateError as e:
        print(f"render-gate: error: {e}", file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

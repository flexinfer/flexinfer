"""KT-ASR retest: compare passes (CU masks vs control) from one pod's /out.

usage: python3 -I retest_summary.py <out_dir> <ami_words_dir> <score_py_dir>
"""

import json
import os
import sys

out, ami, score_dir = sys.argv[1], sys.argv[2], sys.argv[3]
sys.path.insert(0, score_dir)
import score  # noqa: E402


def pct(xs, q):
    xs = sorted(xs)
    return xs[max(0, min(len(xs) - 1, int(round(q * (len(xs) - 1)))))] if xs else None


def lat(probe, lane, pred):
    return [
        l for ph, ln, t, l, e in probe if ln == lane and l is not None and pred(ph, t)
    ]


rows = {}
for name in ("cu40", "nomask", "cu30"):
    d = os.path.join(out, name)
    if not os.path.exists(os.path.join(d, "summary.json")):
        print(name, "missing")
        continue
    s = json.load(open(os.path.join(d, "summary.json")))
    probe = json.load(open(os.path.join(d, "probe.json")))
    runs = s.get("runs", {})
    r = {
        "ready_s": s.get("server_ready_s"),
        "peak": s.get("peak"),
        "x_rt": {k: v.get("x_realtime") for k, v in runs.items()},
        "wall": {k: v.get("wall_s") for k, v in runs.items()},
        "pre_queues": s["pre"]["plane_compute_queues"],
        "oversubscribed": s.get("dmesg_oversubscribed"),
        "amdgpu_new": [
            x
            for x in s.get("dmesg_new", [])
            if "amdgpu" in x.lower() or "kfd" in x.lower()
        ],
    }
    m60 = runs.get("meeting60", {})
    for lane in ("gte", "bge"):
        base = lat(probe, lane, lambda ph, t: ph == "baseline")
        dur = lat(probe, lane, lambda ph, t: ph == "during")
        d60 = lat(
            probe, lane, lambda ph, t: m60 and m60["t_start"] <= t <= m60["t_end"]
        )
        b95, d95, m95 = pct(base, 0.95), pct(dur, 0.95), pct(d60, 0.95)
        r[lane] = {
            "base_p50": round(1000 * pct(base, 0.5), 1),
            "base_p95": round(1000 * b95, 1),
            "during_p50": round(1000 * pct(dur, 0.5), 1),
            "during_p95": round(1000 * d95, 1),
            "during_p95_pct": round(100 * (d95 / b95 - 1), 1),
            "m60_p95": round(1000 * m95, 1) if m95 else None,
            "m60_p95_pct": round(100 * (m95 / b95 - 1), 1) if m95 else None,
            "n_during": len(dur),
            "errors": sum(1 for ph, ln, t, l, e in probe if ln == lane and l is None),
        }
    # transcript stability: WER of this pass vs AMI truth on meeting60
    p60 = os.path.join(d, "meeting60.whispercpp.json")
    if os.path.exists(p60):
        gt = score.normalize(score.ami_reference(ami, "EN2001a", 0.0, 3600.0))
        r["wer_m60_vs_truth"] = round(
            100 * score.wer(gt, score.normalize(score.text_of(p60))), 2
        )
    rows[name] = r
    print(name, json.dumps(r), flush=True)
json.dump(rows, open(os.path.join(out, "retest.json"), "w"), indent=1)

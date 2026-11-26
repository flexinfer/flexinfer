#!/usr/bin/env python3
"""KT-RERANK: what bounds the Radeon VII reranker, batch width or weight format?

Matrix row `gfx906-rerank-batch-width-format-2026-11-26` in
`.loom/60-validation-matrix.md`; its bars and kill rules were written before
any run. The harness runs in a one-shot pod next to the live gfx906 runtime
(`deploy/debug/kt-rerank-batch-gfx906-canary.yaml`). It starts llama-server
from the live runtime image, one server at a time, in these arms:

  q8-live     the live lane's file and flags (Q8_0, 512-token batch, 4 slots)
  q8-w1024    8 slots, 1024-token batch
  q8-w2048    16 slots, 2048-token batch
  f16-live    the FP16 file with the live flags
  q8-repeat   q8-live again, last: the run-to-run noise floor

Why these arms. llama-server clamps n_batch to n_ubatch on an embedding-type
server, and with no --parallel it runs 4 slots on a shared context, so the
live lane scores at most 512 tokens (2 to 4 documents) per pass. A wider arm
therefore raises --parallel, --ubatch-size and --ctx-size together: with an
explicit --parallel the context is split per slot, so --ctx-size is 512 times
the slot count to keep 512 tokens per document.

Per arm: /props (slots, context per slot), KFD VRAM and compute queues of the
server process, then closed-loop /v1/rerank requests of 4, 24, 80 and 160
documents (one at a time), two concurrent 24-document streams, and three fixed
parity requests whose scores are compared with q8-live and with the live lane.
A watcher probes the live gte lane every 2 s and aborts the run on three slow
or failed probes in a row.

Standard library only. `python3 -I kt-rerank-batch.py`; settings come from
KT_* environment variables (see the manifest). Results: one KT_ROW line per
arm on stdout and /out/results.json.
"""

import hashlib
import json
import os
import random
import signal
import statistics
import subprocess
import sys
import threading
import time
import urllib.request

OUT = os.environ.get("KT_OUT", "/out")
KFD = os.environ.get("KT_KFD", "/sys/class/kfd/kfd/proc")
BIN = os.environ.get("KT_BIN", "/opt/llamacpp/bin")
Q8_FILE = os.environ.get(
    "KT_Q8_FILE",
    "/models/flexinfer-system/bge-reranker-radeonvii/bge-reranker-v2-m3-Q8_0.gguf",
)
Q8_SHA256 = "a43c7c9b11a4c1517e5bf95151960e1621d1b72f7a493364b01e386cf1aaa1d3"
F16_URL = os.environ.get(
    "KT_F16_URL",
    "https://huggingface.co/gpustack/bge-reranker-v2-m3-GGUF/resolve/"
    "3093af03b1a635e67b084b1d8c03c5f5e020fd05/bge-reranker-v2-m3-FP16.gguf",
)
F16_SHA256 = "5df93be121c09c43432102ad2b9569d369ccb85c209ca7583e8ccd28f0e41b88"
F16_FILE = os.path.join(OUT, "bge-reranker-v2-m3-FP16.gguf")
LIVE_GTE = os.environ.get("KT_LIVE_GTE", "http://10.43.153.107:8000")
LIVE_RERANK = os.environ.get("KT_LIVE_RERANK", "http://10.43.52.80:8000")
ABORT_S = float(os.environ.get("KT_ABORT_S", "1.0"))
BASELINE_S = int(os.environ.get("KT_BASELINE_S", "60"))
MAX_CARD_QUEUES = int(os.environ.get("KT_MAX_CARD_QUEUES", "24"))
PORT = int(os.environ.get("KT_PORT", "18090"))
SEED = 20261126

# (documents per request, scored repetitions). Live traffic on 2026-11-26:
# about 22 documents at the median request and about 80 at p95.
SHAPES = ((4, 30), (24, 20), (80, 10), (160, 5))
WARMUPS = 3

ARMS = (
    # name, file key, --parallel (0 = unset), --ctx-size, --batch-size, --ubatch-size
    ("q8-live", "q8", 0, 512, 2048, 512),
    ("q8-w1024", "q8", 8, 4096, 1024, 1024),
    ("q8-w2048", "q8", 16, 8192, 2048, 2048),
    ("f16-live", "f16", 0, 512, 2048, 512),
    ("q8-repeat", "q8", 0, 512, 2048, 512),
)

TOPICS = {
    "storage": "volume replica snapshot rebuild disk longhorn backup restore engine degraded".split(),
    "gpu": "kernel driver vram queue shader compute firmware thermal fan power".split(),
    "network": "ingress route packet latency socket proxy tunnel dns firewall gateway".split(),
    "billing": "invoice claim remittance payer adjustment ledger refund balance charge audit".split(),
    "garden": "soil compost seedling mulch prune harvest trellis irrigation frost bloom".split(),
}
FILLER = (
    "the a of and to in for with on by from that this was is are be it as at".split()
)


def now():
    return time.strftime("%H:%M:%SZ", time.gmtime())


def log(*a):
    print(now(), *a, flush=True)


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(p * len(xs)))] if xs else None


def make_doc(rnd, topic, words):
    """One document of about `words` words, mostly on `topic`."""
    vocab = TOPICS[topic]
    return " ".join(
        rnd.choice(vocab) if rnd.random() < 0.45 else rnd.choice(FILLER)
        for _ in range(words)
    )


def make_request(seed, n_docs):
    """A query and n_docs documents. A fifth of the documents are on the
    query's topic, and a fifth are about twice as long (live p95 is 250 tokens
    against a median of 117)."""
    rnd = random.Random(seed)
    topic = rnd.choice(sorted(TOPICS))
    others = [t for t in sorted(TOPICS) if t != topic]
    docs = []
    for i in range(n_docs):
        t = topic if i % 5 == 0 else rnd.choice(others)
        words = 150 if i % 5 == 3 else 75
        docs.append(make_doc(rnd, t, words))
    rnd.shuffle(docs)
    query = "what went wrong with the " + " ".join(rnd.sample(TOPICS[topic], 3))
    return query, docs


def spearman(a, b):
    def ranks(v):
        order = sorted(range(len(v)), key=lambda i: v[i])
        r = [0.0] * len(v)
        for pos, i in enumerate(order):
            r[i] = float(pos)
        return r

    ra, rb = ranks(a), ranks(b)
    n = len(a)
    if n < 2:
        return 1.0
    ma, mb = sum(ra) / n, sum(rb) / n
    cov = sum((x - ma) * (y - mb) for x, y in zip(ra, rb))
    va = sum((x - ma) ** 2 for x in ra) ** 0.5
    vb = sum((y - mb) ** 2 for y in rb) ** 0.5
    return cov / (va * vb) if va and vb else 1.0


def parity(ref, got):
    """Compare two parity sets (lists of score lists, same requests)."""
    if not ref or not got or len(ref) != len(got):
        return None
    worst, rho, top5 = 0.0, 1.0, True
    for a, b in zip(ref, got):
        if len(a) != len(b):
            return None
        worst = max(worst, max(abs(x - y) for x, y in zip(a, b)))
        rho = min(rho, spearman(a, b))
        ta = sorted(range(len(a)), key=lambda i: -a[i])[:5]
        tb = sorted(range(len(b)), key=lambda i: -b[i])[:5]
        top5 = top5 and ta == tb
    return {
        "max_abs_diff": round(worst, 5),
        "min_spearman": round(rho, 5),
        "top5_order_equal": top5,
    }


def post(url, body, timeout=120):
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read())


def rerank(base, query, docs, model="kt"):
    t0 = time.time()
    res = post(base + "/v1/rerank", {"model": model, "query": query, "documents": docs})
    dt = time.time() - t0
    scores = [0.0] * len(docs)
    for item in res.get("results", []):
        scores[item["index"]] = float(item["relevance_score"])
    return dt, scores


def read_int(path):
    try:
        with open(path) as f:
            return int(f.read().strip())
    except (OSError, ValueError):
        return None


def kfd_vram(pid):
    total = 0
    base = os.path.join(KFD, str(pid))
    try:
        for name in os.listdir(base):
            if name.startswith("vram_"):
                total += read_int(os.path.join(base, name)) or 0
    except OSError:
        return None
    return total


def kfd_compute_queues(pid):
    """Compute queues of one KFD process: a queue's `type` file reads 0."""
    base = os.path.join(KFD, str(pid), "queues")
    try:
        return sum(
            1 for q in os.listdir(base) if read_int(os.path.join(base, q, "type")) == 0
        )
    except OSError:
        return None


def card_compute_queues():
    try:
        return sum(kfd_compute_queues(p) or 0 for p in os.listdir(KFD) if p.isdigit())
    except OSError:
        return None


def metrics(base):
    out = {}
    try:
        with urllib.request.urlopen(base + "/metrics", timeout=5) as r:
            for line in r.read().decode().splitlines():
                if line.startswith("llamacpp:"):
                    k, _, v = line.partition(" ")
                    out[k[len("llamacpp:") :]] = float(v)
    except Exception:
        pass
    return out


class Watcher(threading.Thread):
    """Probes the live gte lane at its Service; aborts on a sustained stall."""

    def __init__(self):
        super().__init__(daemon=True)
        self.abort = threading.Event()
        self.stop = threading.Event()
        self.reason = ""
        self.samples = []  # (phase, seconds or None)
        self.phase = "baseline"

    def run(self):
        bad = 0
        while not self.stop.is_set():
            t0 = time.time()
            try:
                post(
                    LIVE_GTE + "/v1/embeddings",
                    {
                        "input": "live gte probe from KT-RERANK",
                        "model": "gte-qwen2-1p5b-radeonvii",
                    },
                    timeout=10,
                )
                dt = time.time() - t0
            except Exception:
                dt = None
            self.samples.append((self.phase, dt))
            bad = bad + 1 if (dt is None or dt > ABORT_S) else 0
            if bad >= 3 and not self.abort.is_set():
                self.reason = (
                    f"live gte: 3 probes in a row slow or failed during {self.phase}"
                )
                self.abort.set()
            self.stop.wait(2.0)

    def summary(self, phase):
        xs = [d for p, d in self.samples if p == phase]
        ok = [d for d in xs if d is not None]
        return {
            "probes": len(xs),
            "failed": len(xs) - len(ok),
            "p50_ms": round(1000 * statistics.median(ok), 1) if ok else None,
            "p95_ms": round(1000 * pct(ok, 0.95), 1) if ok else None,
            "max_ms": round(1000 * max(ok), 1) if ok else None,
        }


def sha256_of(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 22), b""):
            h.update(chunk)
    return h.hexdigest()


def fetch_f16():
    """Download the FP16 file; returns an error string or None."""
    try:
        if not os.path.exists(F16_FILE):
            log("downloading FP16 weights")
            with urllib.request.urlopen(F16_URL, timeout=60) as r, open(
                F16_FILE + ".part", "wb"
            ) as f:
                while True:
                    chunk = r.read(1 << 22)
                    if not chunk:
                        break
                    f.write(chunk)
            os.replace(F16_FILE + ".part", F16_FILE)
        got = sha256_of(F16_FILE)
        return (
            None
            if got == F16_SHA256
            else f"FP16 sha256 {got[:12]} is not the pinned {F16_SHA256[:12]}"
        )
    except Exception as e:
        return f"FP16 download failed: {e!r}"[:200]


def server_cmd(file, parallel, ctx, batch, ubatch):
    cmd = [
        os.path.join(BIN, "llama-server"),
        "--host",
        "127.0.0.1",
        "--port",
        str(PORT),
        "--model",
        file,
        "--ctx-size",
        str(ctx),
        "--n-gpu-layers",
        "999",
        "--batch-size",
        str(batch),
        "--ubatch-size",
        str(ubatch),
        "--reranking",
        "--metrics",
        "-fit",
        "off",
    ]
    if parallel:
        cmd += ["--parallel", str(parallel)]
    return cmd


def measure(base, watcher):
    """The request shapes of one arm. Returns per-shape stats and parity scores."""
    shapes = {}
    for n_docs, reps in SHAPES:
        times = []
        for i in range(WARMUPS + reps):
            if watcher.abort.is_set():
                return shapes, None
            q, docs = make_request(SEED + 1000 * n_docs + i, n_docs)
            dt, _ = rerank(base, q, docs)
            if i >= WARMUPS:
                times.append(dt)
        shapes[str(n_docs)] = {
            "reps": len(times),
            "p50_ms": round(1000 * statistics.median(times), 1),
            "p95_ms": round(1000 * pct(times, 0.95), 1),
            "docs_per_s": round(n_docs / statistics.median(times), 1),
        }
    # Two closed-loop streams of 24-document requests.
    conc = []

    def stream(k):
        for i in range(10):
            if watcher.abort.is_set():
                return
            q, docs = make_request(SEED + 500000 + 100 * k + i, 24)
            conc.append(rerank(base, q, docs)[0])

    threads = [threading.Thread(target=stream, args=(k,)) for k in range(2)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    if conc:
        shapes["24x2"] = {
            "reps": len(conc),
            "p50_ms": round(1000 * statistics.median(conc), 1),
            "p95_ms": round(1000 * pct(conc, 0.95), 1),
        }
    scores = [rerank(base, *make_request(SEED + 900000 + i, 24))[1] for i in range(3)]
    return shapes, scores


def run_arm(arm, files, watcher):
    name, key, parallel, ctx, batch, ubatch = arm
    res = {
        "arm": name,
        "parallel": parallel or "unset",
        "ctx": ctx,
        "batch": batch,
        "ubatch": ubatch,
    }
    if files.get(key + "_error"):
        res["skipped"] = files[key + "_error"]
        return res
    before = card_compute_queues()
    res["card_queues_before"] = before
    if before is not None and before + 3 > MAX_CARD_QUEUES:
        res["skipped"] = (
            f"card holds {before} compute queues; 3 more would pass {MAX_CARD_QUEUES}"
        )
        return res
    watcher.phase = name
    env = dict(os.environ)
    env.pop("HSA_OVERRIDE_GFX_VERSION", None)
    env["LD_LIBRARY_PATH"] = BIN + ":/opt/rocm/lib:/usr/local/lib"
    base = f"http://127.0.0.1:{PORT}"
    logf = open(os.path.join(OUT, f"{name}.server.log"), "wb")
    t0 = time.time()
    proc = subprocess.Popen(
        server_cmd(files[key], parallel, ctx, batch, ubatch),
        env=env,
        stdout=logf,
        stderr=subprocess.STDOUT,
    )
    try:
        while True:
            if proc.poll() is not None:
                res["error"] = f"llama-server exited {proc.returncode} during load"
                return res
            if time.time() - t0 > 180 or watcher.abort.is_set():
                res["error"] = (
                    "load timeout" if not watcher.abort.is_set() else watcher.reason
                )
                return res
            try:
                with urllib.request.urlopen(base + "/health", timeout=2) as r:
                    if r.status == 200:
                        break
            except Exception:
                time.sleep(0.5)
        res["load_s"] = round(time.time() - t0, 1)
        with urllib.request.urlopen(base + "/props", timeout=5) as r:
            props = json.loads(r.read())
        res["total_slots"] = props.get("total_slots")
        res["n_ctx_slot"] = (props.get("default_generation_settings") or {}).get(
            "n_ctx"
        )
        res["build"] = props.get("build_info")
        res["vram_idle_mib"] = round((kfd_vram(proc.pid) or 0) / 2**20, 1)
        res["queues"] = kfd_compute_queues(proc.pid)
        peak = {"vram": 0, "card": 0}
        done = threading.Event()

        def sampler():
            while not done.is_set():
                peak["vram"] = max(peak["vram"], kfd_vram(proc.pid) or 0)
                peak["card"] = max(peak["card"], card_compute_queues() or 0)
                done.wait(0.5)

        st = threading.Thread(target=sampler, daemon=True)
        st.start()
        m0 = metrics(base)
        shapes, scores = measure(base, watcher)
        m1 = metrics(base)
        done.set()
        st.join(timeout=2)
        res["shapes"] = shapes
        res["scores"] = scores
        res["vram_peak_mib"] = round(peak["vram"] / 2**20, 1)
        res["card_queues_peak"] = peak["card"]
        decodes = m1.get("n_decode_total", 0) - m0.get("n_decode_total", 0)
        tokens = m1.get("prompt_tokens_total", 0) - m0.get("prompt_tokens_total", 0)
        if decodes:
            res["tokens_per_pass"] = round(tokens / decodes, 1)
            res["passes"] = int(decodes)
        if watcher.abort.is_set():
            res["error"] = watcher.reason
        return res
    finally:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            proc.kill()
        logf.close()
        res["live_gte"] = watcher.summary(name)
        time.sleep(3)


def row(a, ref):
    if a.get("skipped") or a.get("error"):
        return f"KT_ROW {a['arm']} | {a.get('skipped') or a.get('error')}"
    s = a["shapes"]
    cells = []
    for k in ("4", "24", "80", "160", "24x2"):
        if k in s:
            r = (
                f" ({s[k]['p50_ms'] / ref['shapes'][k]['p50_ms']:.2f}x)"
                if ref and k in ref.get("shapes", {})
                else ""
            )
            cells.append(f"{k}: {s[k]['p50_ms']} ms{r}")
    return (
        f"KT_ROW {a['arm']} | slots {a['total_slots']} ctx/slot {a['n_ctx_slot']} | "
        + " | ".join(cells)
        + f" | tok/pass {a.get('tokens_per_pass')} | vram peak {a['vram_peak_mib']} MiB | queues {a['queues']} "
        f"(card peak {a['card_queues_peak']}) | live gte p95 {a['live_gte']['p95_ms']} ms | "
        f"vs q8-live {json.dumps(a.get('parity_vs_q8_live'))} | vs live lane {json.dumps(a.get('parity_vs_live_lane'))}"
    )


def main():
    os.makedirs(OUT, exist_ok=True)
    meta = {"started": time.strftime("%FT%TZ", time.gmtime()), "seed": SEED, "bin": BIN}
    files = {"q8": Q8_FILE, "f16": F16_FILE}
    got = sha256_of(Q8_FILE)
    meta["q8_sha256"] = got
    if got != Q8_SHA256:
        files["q8_error"] = f"Q8 sha256 {got[:12]} is not the pinned {Q8_SHA256[:12]}"
    files["f16_error"] = fetch_f16()
    meta["f16_error"] = files["f16_error"]
    watcher = Watcher()
    watcher.start()
    log(f"baseline {BASELINE_S}s on live gte {LIVE_GTE}")
    time.sleep(BASELINE_S)
    meta["live_gte_baseline"] = watcher.summary("baseline")
    log("BASELINE", json.dumps(meta["live_gte_baseline"]))
    # The live lane's scores for the parity requests: three 24-document calls.
    try:
        live = [
            rerank(
                LIVE_RERANK,
                *make_request(SEED + 900000 + i, 24),
                model="bge-reranker-radeonvii",
            )[1]
            for i in range(3)
        ]
    except Exception as e:
        live = None
        meta["live_lane_error"] = repr(e)[:200]
    arms = []
    ref = None
    for arm in ARMS:
        if watcher.abort.is_set():
            arms.append({"arm": arm[0], "skipped": "aborted: " + watcher.reason})
            continue
        log("ARM", arm[0])
        a = run_arm(arm, files, watcher)
        if a.get("scores"):
            if ref is None and a["arm"] == "q8-live":
                ref = a
            a["parity_vs_q8_live"] = parity(ref["scores"], a["scores"]) if ref else None
            a["parity_vs_live_lane"] = parity(live, a["scores"]) if live else None
        arms.append(a)
        print(row(a, ref), flush=True)
    watcher.stop.set()
    meta["aborted"] = watcher.reason or None
    meta["finished"] = time.strftime("%FT%TZ", time.gmtime())
    with open(os.path.join(OUT, "results.json"), "w") as f:
        json.dump({"meta": meta, "live_lane_scores": live, "arms": arms}, f, indent=1)
    try:
        os.remove(F16_FILE)  # keep the archive small
    except OSError:
        pass
    log(
        "KT_VERDICT_INPUTS",
        json.dumps(
            {
                "aborted": meta["aborted"],
                "arms": [a["arm"] for a in arms if a.get("shapes")],
            }
        ),
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())

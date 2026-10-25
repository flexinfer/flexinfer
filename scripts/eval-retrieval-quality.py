#!/usr/bin/env python3
"""Compare embedding and rerank lanes on code-retrieval samples.

Subcommands:
  corpus  build the evaluation sets: MTEB CodeSearchNet go/python (docstring ->
          function) plus this repository's Go doc comments -> function bodies.
  embed   embed documents and queries (raw and instructed) through an
          OpenAI-compatible /v1/embeddings endpoint; writes float32 matrices.
  score   retrieval metrics per set, query style and MRL output dimension.
  rerank  rerank a first-stage top-k with /v1/rerank lanes; quality + agreement.

corpus/embed/rerank requests use only the standard library; score and the
rerank candidate selection need numpy.
"""

import argparse
import hashlib
import json
import math
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.request

# One task description for every instruction-aware model, in each model's own
# documented query format. Documents are always embedded without a prefix.
CODE_TASK = (
    "Given a code search query, retrieve relevant code snippets that answer the query"
)
QUERY_PREFIXES = {
    "none": "",
    "bge": "Represent this sentence for searching relevant passages: ",
    "gte-qwen2": f"Instruct: {CODE_TASK}\nQuery: ",
    "qwen3": f"Instruct: {CODE_TASK}\nQuery:",
}
GO_SOURCE_DIRS = ("api", "backend", "cmd", "controllers", "internal", "pkg")
FUNC_RE = re.compile(r"^func (?:\([^)]*\) )?(?P<name>[A-Za-z_][A-Za-z0-9_]*)")
KINDS = ("docs", "q-raw", "q-inst")


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(value, ensure_ascii=False, indent=1) + "\n")
    tmp.replace(path)


# --- corpus -----------------------------------------------------------------


def go_functions(text):
    """Yield (name, doc, code) for gofmt'd top-level funcs with a doc comment."""
    lines = text.splitlines()
    for i, line in enumerate(lines):
        match = FUNC_RE.match(line)
        if not match or not line.rstrip().endswith("{"):
            continue
        end = next((j for j in range(i + 1, len(lines)) if lines[j] == "}"), None)
        if end is None:
            continue
        k = i - 1
        while k >= 0 and lines[k].startswith("//"):
            k -= 1
        doc_lines = [l[2:].strip() for l in lines[k + 1 : i]]
        if not doc_lines or any(
            l.startswith(("go:", "+kubebuilder", "nolint")) for l in doc_lines[:1]
        ):
            continue
        yield match.group("name"), " ".join(l for l in doc_lines if l), "\n".join(
            lines[i : end + 1]
        )


def doc_to_query(name, doc, max_chars=300):
    """Turn a Go doc comment into a natural-language query without the identifier.

    Go doc comments start with the function name, which would make the task a
    lexical lookup; drop it and keep the first two sentences.
    """
    words = doc.split()
    if words and words[0] == name:
        words = words[1:]
    text = " ".join(words)
    sentences = re.split(r"(?<=[.!?])\s+", text)
    query = " ".join(sentences[:2])[:max_chars].strip()
    return query


def repo_go_set(repo, limit):
    rows = []
    for top in GO_SOURCE_DIRS:
        for path in sorted((repo / top).rglob("*.go")):
            rel = path.relative_to(repo).as_posix()
            if (
                rel.endswith("_test.go")
                or "zz_generated" in rel
                or "/testdata/" in rel
                or "/vendor/" in rel
            ):
                continue
            for name, doc, code in go_functions(path.read_text(errors="replace")):
                query = doc_to_query(name, doc)
                if (
                    len(query.split()) < 6
                    or code.count("\n") < 4
                    or doc.startswith("Deprecated")
                ):
                    continue
                key = hashlib.sha1(f"{rel}:{name}".encode()).hexdigest()
                rows.append((key, rel, name, query, code))
    rows.sort()
    seen, picked = set(), []
    for key, rel, name, query, code in rows:
        if code in seen or query in seen:
            continue
        seen.update((code, query))
        picked.append((key, rel, name, query, code))
        if len(picked) >= limit:
            break
    return {
        "source": "flexinfer Go doc comments (identifier stripped) -> function source",
        "queries": {key: query for key, _, _, query, _ in picked},
        "docs": {key: code for key, _, _, _, code in picked},
        "qrels": {key: [key] for key, *_ in picked},
        "origin": {key: f"{rel}:{name}" for key, rel, name, _, _ in picked},
    }


def csn_set(path):
    data = json.loads(Path(path).read_text())
    qrels = {}
    for row in data["qrels"]:
        if int(row.get("score", 1)) > 0:
            qrels.setdefault(str(row["query-id"]), []).append(str(row["corpus-id"]))
    return {
        "source": f"mteb/CodeSearchNetRetrieval@{data.get('revision', '?')} {Path(path).stem}",
        "queries": {
            str(r["id"]): r["text"] for r in data["queries"] if str(r["id"]) in qrels
        },
        "docs": {str(r["id"]): r["text"] for r in data["corpus"]},
        "qrels": qrels,
    }


def cmd_corpus(args):
    sets = {}
    for path in args.csn:
        name = Path(path).stem
        sets[name] = csn_set(path)
    sets["flexinfer-go"] = repo_go_set(Path(args.repo), args.repo_limit)
    for name, s in sets.items():
        print(name, "queries", len(s["queries"]), "docs", len(s["docs"]))
    write_json(
        args.out,
        {"doc_chars": args.doc_chars, "query_chars": args.query_chars, "sets": sets},
    )


# --- embed ------------------------------------------------------------------


def post_json(url, payload, timeout):
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.load(resp)


class Embedder:
    def __init__(self, base_url, model, timeout=300):
        self.url = base_url.rstrip("/") + "/v1/embeddings"
        self.model = model
        self.timeout = timeout
        self.prompt_tokens = 0
        self.seconds = 0.0
        self.requests = 0
        self.shortened = 0

    def _call(self, texts):
        start = time.monotonic()
        body = post_json(
            self.url,
            {"model": self.model, "input": texts, "encoding_format": "float"},
            self.timeout,
        )
        self.seconds += time.monotonic() - start
        self.requests += 1
        self.prompt_tokens += int(body.get("usage", {}).get("prompt_tokens") or 0)
        rows = sorted(body["data"], key=lambda r: r["index"])
        if [r["index"] for r in rows] != list(range(len(texts))):
            raise ValueError("response indices do not match the request")
        return [r["embedding"] for r in rows]

    def embed(self, texts):
        """Embed texts; split failing batches and shorten single over-long items."""
        try:
            return self._call(texts)
        except urllib.error.HTTPError as err:
            if err.code < 400 or err.code >= 500 and len(texts) == 1:
                raise
            if len(texts) > 1:
                mid = len(texts) // 2
                return self.embed(texts[:mid]) + self.embed(texts[mid:])
            if len(texts[0]) < 64:
                raise
            self.shortened += 1
            return self.embed([texts[0][: len(texts[0]) // 2]])


def write_matrix(path, vectors):
    import array

    flat = array.array("f")
    for v in vectors:
        flat.extend(v)
    Path(path).write_bytes(flat.tobytes())


def cmd_embed(args):
    corpus = json.loads(Path(args.corpus).read_text())
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    emb = Embedder(args.base_url, args.model)
    prefix = QUERY_PREFIXES[args.query_style]
    meta = {
        "model": args.model,
        "base_url": args.base_url,
        "query_style": args.query_style,
        "query_prefix": prefix,
        "doc_chars": corpus["doc_chars"],
        "query_chars": corpus["query_chars"],
        "sets": {},
    }
    previous = {}
    if args.resume and (out / "meta.json").exists():
        previous = json.loads((out / "meta.json").read_text()).get("sets", {})
    sets = args.sets or list(corpus["sets"])
    for name in sets:
        s = corpus["sets"][name]
        doc_ids = sorted(s["docs"])
        q_ids = sorted(s["queries"])
        inputs = {
            "docs": [s["docs"][i][: corpus["doc_chars"]] for i in doc_ids],
            "q-raw": [s["queries"][i][: corpus["query_chars"]] for i in q_ids],
            "q-inst": [
                prefix + s["queries"][i][: corpus["query_chars"]] for i in q_ids
            ],
        }
        info = {"doc_ids": doc_ids, "query_ids": q_ids}
        meta["sets"][name] = info
        for kind in KINDS:
            if kind == "q-inst" and not prefix:
                continue
            texts = inputs[kind]
            done = previous.get(name, {}).get(kind)
            path = out / f"{name}.{kind}.f32"
            if (
                done
                and done["count"] == len(texts)
                and path.exists()
                and path.stat().st_size == 4 * done["count"] * done["dims"]
            ):
                info[kind] = done
                print(name, kind, "resumed", flush=True)
                continue
            tokens0, seconds0 = emb.prompt_tokens, emb.seconds
            vectors = []
            for start in range(0, len(texts), args.batch):
                begun = time.monotonic()
                vectors += emb.embed(texts[start : start + args.batch])
                if args.duty_cycle < 1:
                    # Pace bulk work so live lanes on a shared GPU keep their latency.
                    time.sleep((time.monotonic() - begun) * (1 / args.duty_cycle - 1))
            dims = {len(v) for v in vectors}
            norms = [math.sqrt(sum(x * x for x in v)) for v in vectors[:50]]
            if len(dims) != 1:
                raise ValueError(f"inconsistent dimensions {dims}")
            write_matrix(out / f"{name}.{kind}.f32", vectors)
            info[kind] = {
                "count": len(vectors),
                "dims": dims.pop(),
                "norm_min": min(norms),
                "norm_max": max(norms),
                "prompt_tokens": emb.prompt_tokens - tokens0,
                "seconds": round(emb.seconds - seconds0, 3),
            }
            print(name, kind, info[kind], flush=True)
            # Persist after every kind so an interrupted run can --resume.
            write_json(out / "meta.json", meta)
    meta.update(
        requests=emb.requests,
        shortened_inputs=emb.shortened,
        prompt_tokens=emb.prompt_tokens,
        seconds=round(emb.seconds, 3),
    )
    meta["input_tokens_per_second"] = (
        round(emb.prompt_tokens / emb.seconds, 1) if emb.seconds else None
    )
    write_json(out / "meta.json", meta)


# --- score ------------------------------------------------------------------


def load_run(run_dir, name, kind):
    import numpy as np

    meta = json.loads((Path(run_dir) / "meta.json").read_text())
    info = meta["sets"][name]
    if kind not in info:
        return None, None
    mat = np.fromfile(Path(run_dir) / f"{name}.{kind}.f32", dtype=np.float32).reshape(
        info[kind]["count"], info[kind]["dims"]
    )
    ids = info["doc_ids"] if kind == "docs" else info["query_ids"]
    return ids, mat


def truncate(mat, dims):
    import numpy as np

    cut = mat[:, :dims].astype(np.float64)
    return cut / np.linalg.norm(cut, axis=1, keepdims=True)


def gold_ranks(q_ids, q_mat, d_ids, d_mat, qrels):
    """1-based rank of the best-ranked relevant document for each query."""
    import numpy as np

    sims = q_mat @ d_mat.T
    index = {d: i for i, d in enumerate(d_ids)}
    ranks = []
    for row, qid in enumerate(q_ids):
        gold = [index[d] for d in qrels.get(qid, []) if d in index]
        if not gold:
            continue
        best = sims[row, gold].max()
        ranks.append(int((sims[row] > best).sum()) + 1)
    return np.array(ranks)


def metrics(ranks):
    import numpy as np

    within = ranks <= 10
    return {
        "queries": int(len(ranks)),
        "mrr@10": round(float(np.where(within, 1.0 / ranks, 0).mean()), 4),
        "ndcg@10": round(
            float(np.where(within, 1.0 / np.log2(ranks + 1), 0).mean()), 4
        ),
        "recall@1": round(float((ranks <= 1).mean()), 4),
        "recall@5": round(float((ranks <= 5).mean()), 4),
        "recall@10": round(float(within.mean()), 4),
    }


def cmd_score(args):
    corpus = json.loads(Path(args.corpus).read_text())
    report = {}
    for run in args.runs:
        label, run_dir = run.split("=", 1)
        meta = json.loads((Path(run_dir) / "meta.json").read_text())
        for name in meta["sets"]:
            qrels = corpus["sets"][name]["qrels"]
            d_ids, d_mat = load_run(run_dir, name, "docs")
            native = d_mat.shape[1]
            dims_list = (
                [native] + [d for d in args.mrl_dims if d < native]
                if args.mrl
                else [native]
            )
            for kind in ("q-raw", "q-inst"):
                q_ids, q_mat = load_run(run_dir, name, kind)
                if q_mat is None:
                    continue
                for dims in dims_list:
                    key = f"{label}|{kind}|{dims}"
                    m = metrics(
                        gold_ranks(
                            q_ids,
                            truncate(q_mat, dims),
                            d_ids,
                            truncate(d_mat, dims),
                            qrels,
                        )
                    )
                    report.setdefault(key, {})[name] = m
    for key, per_set in report.items():
        per_set["macro"] = {
            k: round(sum(m[k] for m in per_set.values()) / len(per_set), 4)
            for k in ("mrr@10", "ndcg@10", "recall@1", "recall@5", "recall@10")
        }
    write_json(args.out, report)
    sets = sorted({s for v in report.values() for s in v if s != "macro"})
    print(
        "| run | query | dims | "
        + " | ".join(f"{s} nDCG@10" for s in sets)
        + " | macro nDCG@10 | macro MRR@10 | macro R@1 |"
    )
    print("|---" * (6 + len(sets)) + "|")
    for key, per_set in report.items():
        label, kind, dims = key.split("|")
        cells = [f"{per_set[s]['ndcg@10']:.4f}" if s in per_set else "" for s in sets]
        print(
            f"| {label} | {kind} | {dims} | "
            + " | ".join(cells)
            + f" | {per_set['macro']['ndcg@10']:.4f} | {per_set['macro']['mrr@10']:.4f} | {per_set['macro']['recall@1']:.4f} |"
        )


# --- rerank -----------------------------------------------------------------


def rerank_scores(base_url, model, query, docs, timeout=120):
    """Score docs; on an HTTP error retry once with docs cut to half.

    llama-server answers an over-long rerank pair with 500 ("input is too large
    to process"), not 400, so every status is treated as possibly length-related.
    """
    try:
        return _rerank_scores(base_url, model, query, docs, timeout)
    except urllib.error.HTTPError:
        return _rerank_scores(
            base_url, model, query, [d[: len(d) // 2] for d in docs], timeout
        )


def _rerank_scores(base_url, model, query, docs, timeout):
    body = post_json(
        base_url.rstrip("/") + "/v1/rerank",
        {"model": model, "query": query, "documents": docs},
        timeout,
    )
    scores = [None] * len(docs)
    for r in body["results"]:
        scores[r["index"]] = r["relevance_score"]
    if any(s is None for s in scores):
        raise ValueError("rerank response is missing indices")
    return scores


def spearman(a, b):
    def ranks(x):
        order = sorted(range(len(x)), key=lambda i: x[i])
        r = [0.0] * len(x)
        for pos, i in enumerate(order):
            r[i] = float(pos)
        return r

    ra, rb = ranks(a), ranks(b)
    n = len(a)
    mean = (n - 1) / 2
    num = sum((x - mean) * (y - mean) for x, y in zip(ra, rb))
    den = math.sqrt(sum((x - mean) ** 2 for x in ra) * sum((y - mean) ** 2 for y in rb))
    return num / den if den else 0.0


def cmd_rerank(args):
    import numpy as np

    corpus = json.loads(Path(args.corpus).read_text())
    lanes = [lane.split("=", 2) for lane in args.lane]  # label=base_url=model
    report = {
        "first_stage": args.first_stage,
        "top_k": args.top_k,
        "doc_chars": args.doc_chars,
        "sets": {},
    }
    for name in args.sets or list(corpus["sets"]):
        s = corpus["sets"][name]
        d_ids, d_mat = load_run(args.first_stage, name, "docs")
        q_ids, q_mat = load_run(args.first_stage, name, args.first_stage_kind)
        d_mat, q_mat = truncate(d_mat, d_mat.shape[1]), truncate(q_mat, q_mat.shape[1])
        picked = sorted(
            q_ids, key=lambda q: hashlib.sha1(f"{name}:{q}".encode()).hexdigest()
        )[: args.queries]
        q_index = {q: i for i, q in enumerate(q_ids)}
        per_lane = {
            label: {"ranks": [], "seconds": 0.0, "top1": []} for label, _, _ in lanes
        }
        first_ranks, gold_in, rhos = [], 0, []
        for qid in picked:
            sims = d_mat @ q_mat[q_index[qid]]
            cand = list(np.argsort(-sims)[: args.top_k])
            gold = {d_ids.index(d) for d in s["qrels"][qid] if d in d_ids}
            hit = [i for i, c in enumerate(cand) if c in gold]
            first_ranks.append(hit[0] + 1 if hit else args.top_k + 1)
            gold_in += bool(hit)
            docs = [s["docs"][d_ids[c]][: args.doc_chars] for c in cand]
            query = s["queries"][qid][: corpus["query_chars"]]
            lane_scores = {}
            for label, base_url, model in lanes:
                start = time.monotonic()
                scores = rerank_scores(base_url, model, query, docs)
                elapsed = time.monotonic() - start
                per_lane[label]["seconds"] += elapsed
                if args.duty_cycle < 1:
                    time.sleep(elapsed * (1 / args.duty_cycle - 1))
                order = sorted(range(len(cand)), key=lambda i: -scores[i])
                reranked = [i for i in order if i in hit]
                per_lane[label]["ranks"].append(
                    order.index(reranked[0]) + 1 if reranked else args.top_k + 1
                )
                per_lane[label]["top1"].append(order[0])
                lane_scores[label] = scores
            if len(lanes) == 2:
                a, b = (lane_scores[label] for label, _, _ in lanes)
                rhos.append(spearman(a, b))
        result = {
            "queries": len(picked),
            "gold_in_top_k": round(gold_in / len(picked), 4),
            "first_stage": metrics(np.array(first_ranks)),
        }
        for label, data in per_lane.items():
            result[label] = metrics(np.array(data["ranks"]))
            result[label]["seconds_per_query"] = round(data["seconds"] / len(picked), 3)
        if len(lanes) == 2:
            la, lb = (per_lane[label]["top1"] for label, _, _ in lanes)
            result["agreement"] = {
                "top1_same": round(sum(x == y for x, y in zip(la, lb)) / len(la), 4),
                "spearman_mean": round(sum(rhos) / len(rhos), 4),
            }
        report["sets"][name] = result
        print(name, json.dumps(result), flush=True)
    write_json(args.out, report)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("corpus")
    p.add_argument(
        "--csn",
        action="append",
        default=[],
        help="MTEB CodeSearchNet JSON {queries,corpus,qrels}",
    )
    p.add_argument("--repo", default=str(Path(__file__).resolve().parent.parent))
    p.add_argument("--repo-limit", type=int, default=500)
    p.add_argument(
        "--doc-chars", type=int, default=1000, help="fits bge-large's 512-token lane"
    )
    p.add_argument("--query-chars", type=int, default=400)
    p.add_argument("--out", required=True)
    p.set_defaults(func=cmd_corpus)

    p = sub.add_parser("embed")
    p.add_argument("--corpus", required=True)
    p.add_argument("--base-url", required=True)
    p.add_argument("--model", required=True)
    p.add_argument("--query-style", choices=sorted(QUERY_PREFIXES), required=True)
    p.add_argument("--sets", nargs="*")
    p.add_argument("--batch", type=int, default=16)
    p.add_argument(
        "--duty-cycle",
        type=float,
        default=1.0,
        help="fraction of wall time spent in requests; <1 sleeps between batches",
    )
    p.add_argument(
        "--resume", action="store_true", help="skip kinds already complete in --out"
    )
    p.add_argument("--out", required=True)
    p.set_defaults(func=cmd_embed)

    p = sub.add_parser("score")
    p.add_argument("--corpus", required=True)
    p.add_argument("--runs", nargs="+", required=True, help="label=run_dir")
    p.add_argument("--mrl", action="store_true")
    p.add_argument(
        "--mrl-dims", type=int, nargs="*", default=[2048, 1536, 1024, 768, 512]
    )
    p.add_argument("--out", required=True)
    p.set_defaults(func=cmd_score)

    p = sub.add_parser("rerank")
    p.add_argument("--corpus", required=True)
    p.add_argument(
        "--first-stage", required=True, help="embed run dir used to pick candidates"
    )
    p.add_argument("--first-stage-kind", default="q-inst", choices=("q-raw", "q-inst"))
    p.add_argument(
        "--lane", action="append", required=True, help="label=base_url=model"
    )
    p.add_argument("--sets", nargs="*")
    p.add_argument("--queries", type=int, default=100)
    p.add_argument("--top-k", type=int, default=20)
    p.add_argument(
        "--doc-chars", type=int, default=800, help="fits bge-reranker's 512-token lane"
    )
    p.add_argument(
        "--duty-cycle",
        type=float,
        default=1.0,
        help="fraction of wall time spent in requests",
    )
    p.add_argument("--out", required=True)
    p.set_defaults(func=cmd_rerank)

    args = parser.parse_args(argv)
    args.func(args)


if __name__ == "__main__":
    sys.exit(main())

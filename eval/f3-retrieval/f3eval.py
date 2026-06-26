#!/usr/bin/env python3
"""F3 kill-test: retrieval (production qdrant index) vs. naive context-stuffing
vs. no-context, on the loom-core codebase.

Pure stdlib (urllib/json) so it runs on a bare python:3-slim pod, mirroring
deploy/tasks/codebase-reembed/reembed.py.

For each grounded loom-core Q&A (QUESTIONS_FILE) it runs three conditions
through the live flexinfer-proxy and scores each:

  retrieval : embed query (bge-large) -> search the REAL production index
              (codebase_memory_bge_v1 in qdrant) top-N -> rerank (/v1/rerank)
              -> top-K small context -> generate
  naive     : stuff loom-core in deterministic walk order up to a char budget
              (~the model's window) -> generate
  baseline  : no context -> generate

Scoring: keyword substring match (deterministic) + an INDEPENDENT LLM judge
(a different model than the answerer). Verdict (the kill-test): PASS if
retrieval keyword-correct >= naive AND retrieval uses >= 3x fewer context
tokens. Also reports the discriminating subset (evidence OUTSIDE the naive
window) where retrieval should win and naive structurally cannot.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

# ---- config (env) ----------------------------------------------------------
PROXY = os.environ["PROXY_URL"].rstrip("/")
EMBED_MODEL = os.environ.get("EMBED_MODEL", "bge-large-radeonvii")
RERANK_MODEL = os.environ.get("RERANK_MODEL", "bge-reranker-radeonvii")
CHAT_MODEL = os.environ.get("CHAT_MODEL", "qwen36-35b-mtp-uncensored-5930k")
JUDGE_MODEL = os.environ.get("JUDGE_MODEL", "gemma4-26b-a4b-gptq")

QDRANT_URL = os.environ.get("QDRANT_URL", "http://192.168.50.176:6333").rstrip("/")
QDRANT_API_KEY = os.environ.get("QDRANT_API_KEY", "")
COLLECTION = os.environ.get("COLLECTION", "codebase_memory_bge_v1")

REPO_PATH = os.environ.get("REPO_PATH", "/workspace/services/loom-core").rstrip("/")
REPO_NAME = os.environ.get("REPO_NAME") or os.path.basename(REPO_PATH)
QUESTIONS_FILE = os.environ.get("QUESTIONS_FILE", "/questions/questions.json")

NAIVE_CHAR_BUDGET = int(os.environ.get("NAIVE_CHAR_BUDGET", "120000"))
# Optional: restrict naive stuffing to specific subdirs (comma-separated, in
# order). Used by the confirmatory run to give naive its STRONGEST shot: stuff
# only the answer-bearing source dirs near the model's context ceiling.
NAIVE_ROOTS = [
    r.strip() for r in os.environ.get("NAIVE_ROOTS", "").split(",") if r.strip()
]
RETR_N = int(os.environ.get("RETR_N", "24"))
RETR_K = int(os.environ.get("RETR_K", "6"))
GEN_MAX_TOKENS = int(os.environ.get("GEN_MAX_TOKENS", "300"))
MAX_FILE_BYTES = int(os.environ.get("MAX_FILE_BYTES", str(512 * 1024)))
HTTP_TIMEOUT = int(os.environ.get("HTTP_TIMEOUT", "240"))

EXTS = tuple(
    e.strip()
    for e in os.environ.get(
        "EXTENSIONS",
        ".go,.py,.ts,.tsx,.js,.rs,.md,.sh,.yaml,.yml,.toml",
    ).split(",")
    if e.strip()
)
DENY_DIRS = set(
    d.strip()
    for d in os.environ.get(
        "DENY_DIRS",
        ".git,node_modules,vendor,dist,build,.cache,.venv,venv,__pycache__,testdata,.worktrees,bin,.loom,generated,web",
    ).split(",")
    if d.strip()
)


def log(msg):
    print(msg, flush=True)


# ---- http -----------------------------------------------------------------
def _req(url, data=None, method=None, headers=None):
    h = {"Content-Type": "application/json"}
    if headers:
        h.update(headers)
    body = json.dumps(data).encode() if data is not None else None
    return urllib.request.Request(url, data=body, method=method, headers=h)


def _do(req, retries=3):
    last = None
    for attempt in range(retries):
        try:
            with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as r:
                raw = r.read()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as exc:
            last = RuntimeError(f"HTTP {exc.code} {req.full_url}: {exc.read()[:300]!r}")
        except Exception as exc:  # noqa: BLE001
            last = RuntimeError(f"{type(exc).__name__} {req.full_url}: {exc}")
        time.sleep(2 * (attempt + 1))
    raise last


def embed(texts):
    resp = _do(
        _req(f"{PROXY}/v1/embeddings", data={"model": EMBED_MODEL, "input": texts})
    )
    rows = sorted(resp["data"], key=lambda d: d["index"])
    return [row["embedding"] for row in rows]


def qdrant_search(vector, limit):
    headers = {"api-key": QDRANT_API_KEY} if QDRANT_API_KEY else {}
    resp = _do(
        _req(
            f"{QDRANT_URL}/collections/{COLLECTION}/points/search",
            data={"vector": vector, "limit": limit, "with_payload": True},
            method="POST",
            headers=headers,
        )
    )
    return resp.get("result", []) if resp else []


def rerank(query, docs, top_n):
    try:
        resp = _do(
            _req(
                f"{PROXY}/v1/rerank",
                data={
                    "model": RERANK_MODEL,
                    "query": query,
                    "documents": docs,
                    "top_n": top_n,
                },
            )
        )
        results = resp.get("results") or resp.get("data") or []
        ordered = sorted(
            results, key=lambda r: -r.get("relevance_score", r.get("score", 0))
        )
        idxs = [r["index"] for r in ordered if "index" in r]
        if idxs:
            return idxs[:top_n]
    except Exception as exc:  # noqa: BLE001
        log(f"  WARN rerank failed ({exc}); using cosine order")
    return list(range(min(top_n, len(docs))))


def chat(model, system, user, max_tokens=GEN_MAX_TOKENS):
    msgs = ([{"role": "system", "content": system}] if system else []) + [
        {"role": "user", "content": user}
    ]
    resp = _do(
        _req(
            f"{PROXY}/v1/chat/completions",
            data={
                "model": model,
                "messages": msgs,
                "temperature": 0,
                "max_tokens": max_tokens,
            },
        )
    )
    return (resp["choices"][0]["message"]["content"] or "").strip()


# ---- naive corpus (NFS walk) ----------------------------------------------
def iter_files(root):
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(
            d for d in dirnames if d not in DENY_DIRS and not d.startswith(".")
        )
        for fn in sorted(filenames):
            if fn.endswith(EXTS) and not fn.startswith("."):
                yield os.path.join(dirpath, fn)


def naive_file_iter():
    if NAIVE_ROOTS:
        for sub in NAIVE_ROOTS:
            yield from iter_files(os.path.join(REPO_PATH, sub))
    else:
        yield from iter_files(REPO_PATH)


def build_naive_context():
    parts, total, files = [], 0, set()
    for path in naive_file_iter():
        try:
            if os.path.getsize(path) > MAX_FILE_BYTES:
                continue
            with open(path, "r", encoding="utf-8", errors="replace") as fh:
                content = fh.read()
        except OSError:
            continue
        rel = os.path.relpath(path, REPO_PATH)
        block = f"# {REPO_NAME}/{rel}\n{content}\n\n"
        if total + len(block) > NAIVE_CHAR_BUDGET:
            block = block[: max(0, NAIVE_CHAR_BUDGET - total)]
            if block:
                parts.append(block)
                files.add(rel)
            break
        parts.append(block)
        files.add(rel)
        total += len(block)
    return "".join(parts), files


# ---- scoring --------------------------------------------------------------
def keyword_hit(answer, keywords):
    a = answer.lower()
    return all(k.lower() in a for k in keywords) if keywords else False


def judge(question, reference, candidate):
    if not candidate.strip() or candidate.startswith("[ERR"):
        return "INCORRECT"
    prompt = (
        "You are grading a free-text answer against a reference answer.\n"
        f"QUESTION: {question}\nREFERENCE ANSWER: {reference}\nCANDIDATE ANSWER: {candidate}\n\n"
        "Does the candidate contain and agree with the reference answer? Ignore extra detail. "
        "Reply with EXACTLY one word: CORRECT, PARTIAL, or INCORRECT."
    )
    try:
        out = chat(JUDGE_MODEL, None, prompt, max_tokens=8).upper()
    except Exception as exc:  # noqa: BLE001
        return f"JUDGE_ERR({type(exc).__name__})"
    for v in ("INCORRECT", "PARTIAL", "CORRECT"):
        if v in out:
            return v
    return "INCORRECT"


def est_tokens(s):
    return len(s) // 4


def ev_match(ev, path):
    if not ev or not path:
        return False
    return ev == path or path.endswith(ev) or ev.endswith(path)


# ---- main -----------------------------------------------------------------
def main():
    t0 = time.time()
    log(
        f"F3 kill-test start repo={REPO_PATH} chat={CHAT_MODEL} judge={JUDGE_MODEL} "
        f"collection={COLLECTION}"
    )
    if not os.path.isdir(REPO_PATH):
        log(f"FATAL REPO_PATH not found: {REPO_PATH}")
        sys.exit(1)
    with open(QUESTIONS_FILE) as fh:
        questions = json.load(fh)
    if isinstance(questions, dict):
        questions = questions.get("questions", [])
    log(f"loaded {len(questions)} questions")

    skip_naive = os.environ.get("SKIP_NAIVE", "").lower() in ("1", "true", "yes")
    if skip_naive:
        # naive/baseline are index-independent; skip them for chunking-comparison
        # runs (which only vary the retrieval COLLECTION) to save the big prefills.
        naive_ctx, naive_files = "", set()
        log("SKIP_NAIVE set: retrieval-only run (naive + baseline skipped)")
    else:
        naive_ctx, naive_files = build_naive_context()
        log(
            f"naive window: {len(naive_files)} files, ~{est_tokens(naive_ctx)} tok "
            f"({len(naive_ctx)} chars)"
        )

    SYS = (
        "Answer the question using ONLY the provided codebase context. Be concise. "
        "If the answer is not in the context, say 'NOT FOUND'."
    )
    SYS_BASE = "Answer the question concisely. If you do not know, say 'NOT FOUND'."

    rows = []
    for qi, q in enumerate(questions):
        ev = q.get("evidence_path", "")
        kws = q.get("keywords", [])
        ref = q.get("answer", "")
        question = q["question"]
        in_naive = any(ev_match(ev, f) for f in naive_files)
        log(f"\n[Q{qi+1}/{len(questions)}] {question[:90]}")
        log(f"  evidence={ev} in_naive_window={in_naive}")

        # --- retrieval (production qdrant index) ---
        try:
            qv = embed([f"# query\n{question}"])[0]
            hits = qdrant_search(qv, RETR_N)
            cand_docs = [(h.get("payload") or {}).get("text", "") for h in hits]
            cand_paths = [(h.get("payload") or {}).get("path", "") for h in hits]
            order = rerank(question, cand_docs, RETR_K)
            chosen = order[:RETR_K]
            retr_files = [cand_paths[i] for i in chosen]
            ev_retrieved = any(ev_match(ev, f) for f in retr_files)
            retr_ctx = "\n\n".join(cand_docs[i] for i in chosen)
            a_retr = chat(
                CHAT_MODEL, SYS, f"CONTEXT:\n{retr_ctx}\n\nQUESTION: {question}"
            )
        except Exception as exc:  # noqa: BLE001
            retr_files, ev_retrieved, retr_ctx, a_retr = [], False, "", f"[ERR {exc}]"

        # --- naive stuff ---
        if skip_naive:
            a_naive = a_base = "[skipped]"
        else:
            try:
                a_naive = chat(
                    CHAT_MODEL, SYS, f"CONTEXT:\n{naive_ctx}\n\nQUESTION: {question}"
                )
            except Exception as exc:  # noqa: BLE001
                a_naive = f"[ERR {exc}]"
            # --- baseline ---
            try:
                a_base = chat(CHAT_MODEL, SYS_BASE, f"QUESTION: {question}")
            except Exception as exc:  # noqa: BLE001
                a_base = f"[ERR {exc}]"

        row = {
            "q": question,
            "evidence": ev,
            "in_naive_window": in_naive,
            "ev_retrieved": ev_retrieved,
            "retrieved_files": retr_files,
            "retrieval": {
                "kw": keyword_hit(a_retr, kws),
                "judge": judge(question, ref, a_retr),
                "tok": est_tokens(retr_ctx),
                "ans": a_retr[:220],
            },
            "naive": {
                "kw": keyword_hit(a_naive, kws),
                "judge": judge(question, ref, a_naive),
                "tok": est_tokens(naive_ctx),
                "ans": a_naive[:220],
            },
            "baseline": {
                "kw": keyword_hit(a_base, kws),
                "judge": judge(question, ref, a_base),
                "tok": 0,
                "ans": a_base[:220],
            },
        }
        rows.append(row)
        log(
            f"  retrieval kw={row['retrieval']['kw']} judge={row['retrieval']['judge']} "
            f"ev_retrieved={ev_retrieved} | naive kw={row['naive']['kw']} judge={row['naive']['judge']} "
            f"| base kw={row['baseline']['kw']} judge={row['baseline']['judge']}"
        )
        log(f"    R: {row['retrieval']['ans'][:150]!r}")
        log(f"    N: {row['naive']['ans'][:150]!r}")

    # ---- aggregate ----
    n = len(rows)
    kw = lambda c: sum(1 for r in rows if r[c]["kw"] is True)
    jg = lambda c: sum(1 for r in rows if r[c]["judge"] == "CORRECT")
    outside = [r for r in rows if not r["in_naive_window"]]

    log("\n" + "=" * 72)
    log(f"F3 KILL-TEST RESULTS  (n={n} questions, index={COLLECTION})")
    log("-" * 72)
    log(f"{'condition':<12}{'kw_correct':>12}{'judge_correct':>15}{'avg_ctx_tok':>13}")
    for c in ("retrieval", "naive", "baseline"):
        avg = sum(r[c]["tok"] for r in rows) / n if n else 0
        log(f"{c:<12}{kw(c):>12}{jg(c):>15}{avg:>13.0f}")
    log("-" * 72)
    log(
        f"evidence retrieved into top-{RETR_K}: {sum(1 for r in rows if r['ev_retrieved'])}/{n}"
    )
    log(f"questions with evidence OUTSIDE naive window: {len(outside)}/{n}")
    if outside:
        log(
            f"  on those: retrieval kw={sum(1 for r in outside if r['retrieval']['kw'])}/{len(outside)}"
            f"  naive kw={sum(1 for r in outside if r['naive']['kw'])}/{len(outside)}"
        )

    avg_r = sum(r["retrieval"]["tok"] for r in rows) / n if n else 0
    avg_nv = sum(r["naive"]["tok"] for r in rows) / n if n else 1
    ratio = (avg_nv / avg_r) if avg_r else 0
    passed = (
        (kw("retrieval") >= kw("naive"))
        and (jg("retrieval") >= jg("naive"))
        and (ratio >= 3)
    )
    log("-" * 72)
    log(
        f"retrieval vs naive: kw {kw('retrieval')} vs {kw('naive')} | "
        f"judge {jg('retrieval')} vs {jg('naive')} | token savings {ratio:.1f}x"
    )
    log(
        f"VERDICT: {'PASS' if passed else 'FAIL'} "
        f"(retrieval >= naive on correctness AND >= 3x fewer tokens)"
    )
    log(f"elapsed {time.time()-t0:.0f}s")
    log("=" * 72)
    print(
        "F3_RESULT_JSON "
        + json.dumps(
            {
                "n": n,
                "retrieval": {
                    "kw": kw("retrieval"),
                    "judge": jg("retrieval"),
                    "avg_tok": round(avg_r),
                },
                "naive": {
                    "kw": kw("naive"),
                    "judge": jg("naive"),
                    "avg_tok": round(avg_nv),
                },
                "baseline": {"kw": kw("baseline"), "judge": jg("baseline")},
                "ev_retrieved": sum(1 for r in rows if r["ev_retrieved"]),
                "outside_naive": len(outside),
                "token_ratio": round(ratio, 1),
                "verdict": "PASS" if passed else "FAIL",
            }
        ),
        flush=True,
    )


if __name__ == "__main__":
    main()

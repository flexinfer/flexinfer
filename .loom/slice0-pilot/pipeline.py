#!/usr/bin/env python3
"""
Slice 0 kill-test pipeline (hands-off up to the send line) -- v2.

Improvements over v1:
- SQLite backend (store.py): dedup by domain, status lifecycle, resumable.
- Lead identification: detect + skip listicle/aggregator pages, but MINE them for
  individual company names to chase (better yield). Dedup by domain.
- Background research: deeper multi-query enrichment -> structured signals
  (what they sell, stage, funding, headcount, trigger) + the DECISION-MAKER's name.
- Email discovery (verified-only): prefer the named person's on-domain address;
  fall back to other human on-domain addresses; generic role mailbox last.
- Drafting: uses the person's name + concrete signals + rotating angle => variety.

NEVER sends. Review + send happens in server.py.
stdlib-only. Config via env (see README): TAVILY_API_KEY, LLM_BASE_URL, LLM_MODEL.
"""
import argparse
import csv
import json
import os
import re
import sys
import time
import urllib.request
from pathlib import Path

import store

HERE = Path(__file__).resolve().parent
SKIPLOG = HERE / "pipeline-skips.csv"

DEFAULT_SEEDS = [
    'Series A AI startup hiring "founding account executive" 2026',
    '"we just raised" seed B2B SaaS founder 2026',
    "dev tools startup seed round 2026 product launch",
    "vertical AI SaaS startup seed funded 2025 2026",
]

EMAIL_RE = re.compile(r"[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}")
NEVER_LOCAL = {
    "noreply",
    "no-reply",
    "donotreply",
    "do-not-reply",
    "mailer-daemon",
    "postmaster",
    "abuse",
    "webmaster",
    "bounce",
    "notifications",
    "support",
    # system / product / compliance mailboxes -- never a sales contact
    "console",
    "api",
    "dev",
    "devops",
    "app",
    "apps",
    "dashboard",
    "status",
    "dataprotection",
    "dpo",
    "gdpr",
    "security",
    "root",
    "sysadmin",
    "dns",
    "smtp",
    "mx",
    "ftp",
    "test",
    "demo",
    "docs",
    "engineering",
    "eng",
}
FOUNDER_LOCAL = {"founders", "founder", "cofounder", "co-founder", "ceo", "hey", "hi"}
ROLE_LOCAL = {
    "info",
    "hello",
    "contact",
    "team",
    "sales",
    "legal",
    "privacy",
    "press",
    "careers",
    "jobs",
    "admin",
    "hr",
    "billing",
    "help",
    "marketing",
}
# Doc/example placeholder local-parts that the email regex falsely picks up from API
# docs ("e.g. jane@yourcompany.com"). Never a real prospect.
PLACEHOLDER_LOCAL = {
    "jane",
    "john",
    "janedoe",
    "johndoe",
    "jane.doe",
    "john.doe",
    "firstname",
    "lastname",
    "name",
    "yourname",
    "username",
    "user",
    "email",
    "you",
    "someone",
    "first",
    "last",
}
JUNK_DOMAINS = {
    "example.com",
    "sentry.io",
    "wixpress.com",
    "domain.com",
    "email.com",
    "yourdomain.com",
    "schema.org",
    "w3.org",
    "googleapis.com",
    "cloudflare.com",
    "godaddy.com",
    "sentry-next.wixpress.com",
}
# aggregator / listicle / directory sources: mine for names, never treat as a single
# company AND never accept as a lead's own domain (would yield e.g. hn@ycombinator.com).
AGG_DOMAINS = {
    "crunchbase.com",
    "growjo.com",
    "f6s.com",
    "tracxn.com",
    "cbinsights.com",
    "failory.com",
    "medium.com",
    "substack.com",
    "reddit.com",
    "youtube.com",
    "exploding-topics.com",
    "getlatka.com",
    "news.ycombinator.com",
    "ycombinator.com",  # YC company directory: mine for names, not a single company
    "seedtable.com",  # startup newsletter/directory
    "builtin.com",
    "bebee.com",
    "g2.com",
    "capterra.com",
    "glassdoor.com",
    "indeed.com",
    "ziprecruiter.com",
    "linkedin.com",
    "wellfound.com",
    "angel.co",
    "producthunt.com",
    "forbes.com",
    "techcrunch.com",
    "businessinsider.com",
    "eu-startups.com",
    "sifted.eu",
    "owler.com",
    "pitchbook.com",
}


def is_agg_domain(domain):
    d = strip_www(domain)
    return bool(d) and any(d == a or d.endswith("." + a) for a in AGG_DOMAINS)


AGG_TITLE_RE = re.compile(
    r"\b(top|best)\s+\d+|\d{2,}\+?\s+(funded|startups|companies)|list of|"
    r"startups to watch|funded .*startups|directory|roundup",
    re.I,
)

ANGLES = [
    "Lead with the trigger signal and the time it would save the founder.",
    "Lead with a sharp question about their current top-of-funnel, then the offer.",
    "Lead with the dogfood proof (this email was sourced by the agent) as the hook.",
]

ENRICH_PROMPT = """You are a B2B sales researcher. From the research text about ONE company,
return STRICT JSON (no prose):
- company (string)
- domain (string; the company's OWN primary web domain like "acme.com", else "")
- what_they_sell (<=140 chars)
- stage (one of: pre-seed, seed, series-a, later, unknown)
- funding (short, e.g. "$4M seed 2026" or "")
- headcount_est (string, e.g. "10-40" or "")
- trigger_signal (the most outreach-relevant recent event, <=140 chars)
- decision_maker_name (full name of founder/CEO/head of growth if present, else "")
- decision_maker_title (e.g. "Founder/CEO", else "")
- is_single_company (true if this text is about ONE real company, false if a list/article)

Research text:
---
{research}
---
Return only the JSON object."""

NAMES_PROMPT = """From this listicle/aggregator text, extract up to 8 distinct, real B2B
software STARTUP company names (not investors, not the article author, not big public
companies). Return STRICT JSON: {"companies": ["Name1", "Name2", ...]}. Text:
---
{text}
---
Return only the JSON object."""

SCORE_PROMPT = """Score this prospect against our ICP. STRICT JSON only.

ICP: seed-Series-A B2B software startups, ~10-40 staff, founder-led / thin sales motion,
no dedicated RevOps/SDR, already using AI. We sell a 3-week sprint that stands up an
automated, self-hosted outbound lead-gen agent on their stack.
Disqualify: pure B2C/PLG-only, >75 staff with existing RevOps, agencies selling the
same thing, pre-product, not B2B.

Prospect signals:
{signals}

Return: {{"icp_score": int 0-100, "disqualified": bool, "fit_rationale": "<=180 chars"}}
Return only the JSON object."""

DRAFT_PROMPT = """Write a cold outreach email. <=110 words, plain text, no markdown.
Voice: clever but not too clever; specific, warm, direct. No "I hope this finds you well",
no buzzwords, no PS.

Angle for THIS email: {angle}

Rules:
- Address the person by first name if provided.
- Open with the specific trigger signal (must be true to the research).
- One sentence offer: a 3-week sprint that stands up an automated, self-hosted outbound
  lead-gen agent on their stack -- "the same system that sourced this email".
- One clear CTA: a 20-minute call; offer to find a time.

Prospect:
company: {company}
person: {person} ({title})
what_they_sell: {sells}
trigger_signal: {trigger}
Return STRICT JSON: {{"subject": "...", "body": "..."}}. Only the JSON object."""


def http_json(url, payload=None, headers=None, timeout=60):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method="POST" if data else "GET")
    req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode())


# Tavily credit cost: "basic"=1 credit/search, "advanced"=2. Default basic to conserve
# the free tier; override with TAVILY_SEARCH_DEPTH=advanced for higher-recall runs.
SEARCH_DEPTH = os.environ.get("TAVILY_SEARCH_DEPTH", "basic")
_TAV = {"searches": 0, "extracts": 0}  # credit accounting for the run summary


def tav_search(query, key, n=8):
    _TAV["searches"] += 1
    out = http_json(
        "https://api.tavily.com/search",
        {
            "api_key": key,
            "query": query,
            "max_results": n,
            "search_depth": SEARCH_DEPTH,
            "include_raw_content": True,
        },
    )
    return out.get("results", [])


def tav_extract(url, key):
    _TAV["extracts"] += 1
    try:
        out = http_json(
            "https://api.tavily.com/extract", {"api_key": key, "urls": [url]}
        )
        res = out.get("results", [])
        return res[0].get("raw_content", "")[:8000] if res else ""
    except Exception:
        return ""


def llm(base, model, prompt, timeout=180):
    out = http_json(
        base.rstrip("/") + "/chat/completions",
        {
            "model": model,
            "temperature": 0.5,
            "messages": [{"role": "user", "content": prompt}],
        },
        headers={"Authorization": "Bearer none"},
        timeout=timeout,
    )
    txt = out["choices"][0]["message"]["content"]
    m = re.search(r"\{.*\}", txt, re.DOTALL)
    return json.loads(m.group(0)) if m else {}


def strip_www(host):
    """Strip a leading 'www.' PREFIX (str.lstrip strips a char-set, which corrupts
    domains like 'wix.com' -> 'ix.com'). Lowercases."""
    h = (host or "").strip().lower()
    return h[4:] if h.startswith("www.") else h


def host_of(url):
    m = re.match(r"https?://([^/]+)", url or "")
    return strip_www(m.group(1)) if m else ""


def is_aggregator(url, title):
    h = host_of(url)
    if any(h == d or h.endswith("." + d) for d in AGG_DOMAINS):
        return True
    return bool(AGG_TITLE_RE.search(title or ""))


# How many enrichment searches to spend per surviving candidate. Default 1 (was 3) to
# conserve Tavily credits; one combined query carries enough signal for the ICP scorer.
GATHER_QUERIES = int(os.environ.get("GATHER_QUERIES", 1))
# How many email-discovery searches per surviving candidate (default 1). The guessed
# fallback covers the rest for tiny startups, so paying for 3 searches rarely helps.
EMAIL_QUERIES = int(os.environ.get("EMAIL_QUERIES", 1))


def gather_research(company, key, seed_text=""):
    """Enrichment for one company name. Credit-bounded by GATHER_QUERIES."""
    texts = [seed_text] if seed_text else []
    queries = [
        f"{company} startup product funding founder CEO seed series A",  # combined: 1 search
        f"{company} funding seed series A round",
        f"{company} founder CEO team",
    ][: max(1, GATHER_QUERIES)]
    for q in queries:
        try:
            for r in tav_search(q, key, n=4):
                if is_aggregator(r.get("url", ""), r.get("title", "")):
                    continue
                texts.append(
                    f"{r.get('title','')} {r.get('content','')} {(r.get('raw_content') or '')[:1500]}"
                )
        except Exception:
            pass
    return "\n".join(texts)[:9000]


def find_email(company, domain, person_name, source_url, key):
    """Verified-only on-domain email. Prefer the named person; role mailbox last."""
    domain = strip_www(domain or host_of(source_url))
    # Prefer the single highest-signal query; spend more only if EMAIL_QUERIES allows.
    queries = []
    if person_name:
        queries.append(f'"{person_name}" {company} email')
    queries.append(f'"{company}" email contact')
    if domain:
        queries.append(f"email @{domain}")
    queries = queries[: max(1, EMAIL_QUERIES)]
    texts = []
    for q in queries:
        try:
            for r in tav_search(q, key, n=5):
                texts.append(r.get("content", "") + " " + (r.get("raw_content") or ""))
        except Exception:
            pass

    def harvest(texts):
        f = {}
        for t in texts:
            for e in EMAIL_RE.findall(t or ""):
                e = e.lower()
                local, edom = e.split("@", 1)
                if edom in JUNK_DOMAINS or edom.endswith((".png", ".jpg", ".svg")):
                    continue
                if (
                    local in NEVER_LOCAL
                    or local in PLACEHOLDER_LOCAL
                    or len(local) > 30
                ):
                    continue
                f[e] = f.get(e, 0) + 1
        return f

    found = harvest(texts)
    # Only pay for an extract (1 credit) if cheap search turned up nothing on-domain.
    if source_url and not any(
        e.split("@", 1)[1] == domain or e.split("@", 1)[1].endswith("." + domain)
        for e in found
        if domain
    ):
        found = harvest(texts + [tav_extract(source_url, key)])
    name_tokens = [t.lower() for t in re.findall(r"[A-Za-z]+", person_name or "")]
    best = ""
    if found:

        def rank(item):
            e, n = item
            local, edom = e.split("@", 1)
            same = domain and (edom == domain or edom.endswith("." + domain))
            name_hit = any(tok in local for tok in name_tokens if len(tok) > 2)
            founder = local in FOUNDER_LOCAL
            role = local in ROLE_LOCAL
            # same-domain > named person > founder mailbox > non-role > frequency
            return (
                1 if same else 0,
                1 if name_hit else 0,
                1 if founder else 0,
                0 if role else 1,
                n,
            )

        cand = sorted(found.items(), key=rank, reverse=True)[0][0]
        cdom = cand.split("@", 1)[1]
        if not domain or cdom == domain or cdom.endswith("." + domain):
            best = cand
    if best:
        blocal = best.split("@", 1)[0]
        if any(t in blocal for t in name_tokens if len(t) > 2):
            quality = "personal"
        elif blocal in FOUNDER_LOCAL:
            quality = "founder"
        elif blocal in ROLE_LOCAL:
            quality = "role"
        else:
            quality = "generic"
        return best, quality, []
    # HYBRID fallback: no verified email -> construct the named person's likely address
    primary, alts = guess_email(name_tokens, domain)
    if primary:
        return primary, "guessed", alts
    return "", "", []


def guess_email(name_tokens, domain):
    """Build likely addresses for a NAMED person at a known domain (ICP = tiny startups,
    so first@domain is the best single guess). Returns (primary, [alternates])."""
    toks = [t for t in name_tokens if len(t) > 1]
    if not domain or not toks:
        return "", []
    first = toks[0]
    last = toks[-1] if len(toks) > 1 else ""
    pats = [first]
    if last:
        pats += [
            f"{first}.{last}",
            f"{first}{last}",
            f"{first[0]}{last}",
            f"{first[0]}.{last}",
            f"{first}_{last}",
        ]
    cands = [f"{p}@{domain}" for p in pats]
    return cands[0], cands[1:]


def log_skip(company, reason, detail=""):
    new = not SKIPLOG.exists()
    with open(SKIPLOG, "a", newline="") as f:
        w = csv.writer(f)
        if new:
            w.writerow(["company", "reason", "detail"])
        w.writerow([company, reason, detail])


def candidate_names(hits, base, model, key):
    """Yield (name, domain, source_url, seed_text). Mines aggregator pages for names."""
    for h in hits:
        url, title = h.get("url", ""), h.get("title", "")
        seed_text = (
            f"{title} {h.get('content','')} {(h.get('raw_content') or '')[:3000]}"
        )
        if is_aggregator(url, title):
            try:
                got = llm(base, model, NAMES_PROMPT.format(text=seed_text[:6000]))
                for nm in got.get("companies", [])[:8]:
                    if nm and len(nm) > 1:
                        yield nm.strip(), "", "", ""
            except Exception:
                pass
        else:
            yield title, host_of(url), url, seed_text


def process(name, domain, source_url, seed_text, c, base, model, key, min_score):
    SIG_KEYS = ("what_they_sell", "stage", "funding", "headcount_est", "trigger_signal")

    # Phase 1 -- TRIAGE on text we ALREADY have (seed_text): 0 Tavily when present, so we
    # never pay to research a company we'd disqualify. Only thin/mined names spend a search.
    base_text = (seed_text or "").strip()
    gathered = False
    if len(base_text) < 200:
        base_text = (base_text + "\n" + gather_research(name, key, seed_text)).strip()
        gathered = True
    if len(base_text) < 80:
        log_skip(name, "no_research")
        return None
    try:
        s = llm(base, model, ENRICH_PROMPT.format(research=base_text[:9000]))
    except Exception as e:
        log_skip(name, "enrich_error", str(e)[:80])
        return None
    if not s.get("is_single_company", True):
        log_skip(name, "not_single_company")
        return None
    company = (s.get("company") or name).strip()
    domain = strip_www(s.get("domain") or domain or host_of(source_url))
    if not domain or store.has_domain(c, domain):
        log_skip(company, "dup_or_no_domain", domain)
        return None
    if is_agg_domain(domain):
        # resolved to a directory/aggregator host (e.g. ycombinator.com) -> the email
        # would belong to the directory, not the prospect. Drop it.
        log_skip(company, "aggregator_domain", domain)
        return None
    signals = {k: s.get(k, "") for k in SIG_KEYS}
    try:
        sc = llm(
            base, model, SCORE_PROMPT.format(signals=json.dumps(signals, indent=1))
        )
    except Exception as e:
        log_skip(company, "score_error", str(e)[:80])
        return None
    if sc.get("disqualified"):
        log_skip(company, "disqualified", sc.get("fit_rationale", ""))
        return None
    score = int(sc.get("icp_score", 0) or 0)
    if score < min_score:
        log_skip(company, "low_score", str(score))
        return None
    # Phase 2 -- SURVIVORS ONLY: deepen signals if the cheap pass left key fields empty.
    if not gathered and (
        not signals.get("trigger_signal") or not s.get("decision_maker_name")
    ):
        deeper = gather_research(company, key, base_text)
        if len(deeper) > len(base_text) + 50:
            try:
                s2 = llm(base, model, ENRICH_PROMPT.format(research=deeper[:9000]))
                for k, v in s2.items():
                    if v and not s.get(k):
                        s[k] = v
                signals = {k: s.get(k, "") for k in SIG_KEYS}
            except Exception:
                pass
    person = s.get("decision_maker_name", "")
    email, quality, alts = find_email(company, domain, person, source_url, key)
    if not email or store.has_email(c, email):
        log_skip(company, "no_email", domain)
        return None
    if quality == "guessed":
        signals["email_guess"] = "unverified pattern — confirm before sending"
        if alts:
            signals["email_alternates"] = ", ".join(alts[:4])
    angle = ANGLES[sum(ord(ch) for ch in domain) % len(ANGLES)]
    first = person.split()[0] if person else ""
    try:
        d = llm(
            base,
            model,
            DRAFT_PROMPT.format(
                angle=angle,
                company=company,
                person=person or "there",
                title=s.get("decision_maker_title", ""),
                sells=signals["what_they_sell"],
                trigger=signals["trigger_signal"],
            ),
        )
    except Exception as e:
        log_skip(company, "draft_error", str(e)[:80])
        return None
    lid = store.insert(
        c,
        {
            "domain": domain,
            "company": company,
            "person_name": person,
            "person_title": s.get("decision_maker_title", ""),
            "email": email,
            "email_quality": quality,
            "score": score,
            "rationale": sc.get("fit_rationale", ""),
            "signals_json": signals,
            "source_url": source_url,
            "subject": d.get("subject", ""),
            "body": d.get("body", ""),
            "status": "staged",
        },
    )
    print(f"  + {company} [{score}] {email} ({quality}) <- {first or 'no-name'}")
    return lid


def migrate_queue_json(c):
    qj = HERE / "queue.json"
    if qj.exists() and not store.listall(c):
        for r in json.loads(qj.read_text()):
            dom = strip_www(r.get("domain") or host_of(r.get("source_url", "")))
            if not dom or store.has_domain(c, dom):
                continue
            store.insert(
                c,
                {
                    "domain": dom,
                    "company": r.get("company", ""),
                    "email": r.get("email", ""),
                    "email_quality": "legacy",
                    "score": r.get("score", 0),
                    "rationale": r.get("rationale", ""),
                    "person_title": r.get("contact_title", ""),
                    "source_url": r.get("source_url", ""),
                    "subject": r.get("subject", ""),
                    "body": r.get("body", ""),
                    "status": r.get("status", "staged"),
                },
            )
        print(f"(migrated {len(store.listall(c))} rows from queue.json)")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--seeds")
    ap.add_argument("--max", type=int, default=int(os.environ.get("MAX_PROSPECTS", 50)))
    ap.add_argument(
        "--min-score", type=int, default=int(os.environ.get("MIN_SCORE", 60))
    )
    args = ap.parse_args()

    key = os.environ.get("TAVILY_API_KEY")
    if not key:
        sys.exit("TAVILY_API_KEY required.")
    base = os.environ.get("LLM_BASE_URL", "http://127.0.0.1:8088/v1")
    model = os.environ.get("LLM_MODEL", "gemma4-26b-a4b-gptq")
    seeds = (
        [
            s.strip()
            for s in Path(args.seeds).read_text().splitlines()
            if s.strip() and not s.strip().startswith("#")
        ]
        if args.seeds
        else DEFAULT_SEEDS
    )

    c = store.connect()
    migrate_queue_json(c)
    added = 0
    print(
        f"LLM={model} @ {base} | verified-emails-only | SQLite leads.db | target {args.max}"
    )
    print(
        f"Tavily: depth={SEARCH_DEPTH} (1cr basic/2cr advanced) | "
        f"gather={GATHER_QUERIES} email={EMAIL_QUERIES} searches/lead"
    )
    for seed in seeds:
        if added >= args.max:
            break
        print(f"\n# seed: {seed}")
        try:
            hits = tav_search(seed, key)
        except Exception as e:
            print(f"  ! search failed: {e}")
            continue
        for name, domain, url, seed_text in candidate_names(hits, base, model, key):
            if added >= args.max:
                break
            try:
                if process(
                    name, domain, url, seed_text, c, base, model, key, args.min_score
                ):
                    added += 1
                    time.sleep(0.3)
            except Exception as e:
                log_skip(name, "process_error", str(e)[:80])
    credits = (
        _TAV["searches"] * (2 if SEARCH_DEPTH == "advanced" else 1) + _TAV["extracts"]
    )
    print(f"\nDONE. staged {added} new. totals={store.counts(c)} -> leads.db")
    print(
        f"Tavily spend this run: {_TAV['searches']} searches + {_TAV['extracts']} extracts "
        f"= ~{credits} credits (depth={SEARCH_DEPTH})"
    )
    print("Review + send: python3 server.py  (http://127.0.0.1:8765)")


if __name__ == "__main__":
    main()

# Slice 0 — Kill-Test Pilot (automated up to the send line)

**Goal:** prove automated sourcing + personalized outreach books **≥2 qualified discovery calls** in 14 days. This gate must pass before any tooling (Slice 1+) is built. See [../20-product-spec-gtm-engine.md](../20-product-spec-gtm-engine.md) §2 and [../gtm-icp-v1.md](../gtm-icp-v1.md).

## How it works
The pipeline runs **hands-off up to the send line**. You only review and click Send.

```
store.py    : SQLite backend (leads.db). Dedup by domain, status lifecycle, resumable.
pipeline.py : Tavily search -> mine listicles for company names -> deep enrich
              (decision-maker name + signals) -> score/disqualify vs ICP
              -> HYBRID email (verified, else name-pattern guess) -> draft (gemma4)
              -> stage into leads.db   [NEVER sends]
server.py   : local web page; review/edit each lead; per-draft Send button
              -> sends via SMTP only on click (the human gate) -> marks sent
```
- **Lead ID:** listicle/aggregator pages are detected and mined for individual company names; agencies/VCs/competitors/too-big/pre-product are disqualified by the scorer. All skips logged with reasons in `pipeline-skips.csv`.
- **Research:** each lead carries structured signals (decision-maker name + title, what they sell, stage, funding, trigger) — shown on the card.
- **Hybrid email:** prefer a real published on-domain address (system/compliance mailboxes like `console@`/`dataprotection@` are rejected); if none, construct the *named* person's likely address (`first@domain`, …) and flag it **⚠ guessed** with alternates to try. You vet guessed addresses before sending.
- **Nothing auto-sends.** Send is one click per lead, after you read it.

## One-command run
```bash
cd .loom/slice0-pilot
cp .env.example .env       # then edit .env: paste your TAVILY_API_KEY (+ SMTP_* to send)
./run.sh --max 50          # auto-loads .env, port-forwards proxy, runs pipeline, opens UI
# then open http://127.0.0.1:8765
```
`run.sh` loads `./.env` (gitignored), handles the FlexInfer proxy port-forward
(`flexinfer-proxy` is in-cluster, ingress is behind Cloudflare Access) and model warm-up
(cold-start ~1-2 min). Env vars set in the shell still work if you prefer not to use `.env`.

## Tavily cost control (free tier ~1,000 credits/mo)
Sourcing is the only paid step. The pipeline is tuned to be cheap by default and prints
its credit spend at the end of every run. Knobs (env or `.env`):

| Var | Default | Effect |
|---|---|---|
| `TAVILY_SEARCH_DEPTH` | `basic` | `basic`=1 credit/search, `advanced`=2 (higher recall) |
| `GATHER_QUERIES` | `1` | research searches per *surviving* lead |
| `EMAIL_QUERIES` | `1` | email-discovery searches per *surviving* lead |

Key efficiency: candidates are **scored/disqualified on already-fetched text before any
paid search** — off-ICP companies cost 0 credits. A ~50-lead run is ~150–200 credits
(was ~600 with the old advanced-depth, research-everything flow). Watch the
`Tavily spend this run: N searches ... = ~C credits` line. To use a **fresh/dedicated
Tavily account** for this pipeline, just put that account's key in `.env` — runs draw on
its quota, isolated from your main key.

## Run the pieces separately (optional)
```bash
# 1) port-forward the LLM proxy
KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml \
  kubectl port-forward -n flexinfer-system svc/flexinfer-proxy 8088:80 &
# 2) stage drafts (re-runnable; idempotent on company+email)
TAVILY_API_KEY=... python3 pipeline.py --max 50
# 3) review + send
SMTP_USER=... SMTP_PASS=... python3 server.py   # http://127.0.0.1:8765
```

## Files
- `store.py` — SQLite backend (`leads.db`): dedup by domain, status lifecycle, resumable.
- `pipeline.py` — source→mine→enrich→score→hybrid-email→draft→`leads.db`. Skips logged to `pipeline-skips.csv`.
- `server.py` — local review/send web app (edit + per-lead Send).
- `run.sh` — port-forward + pipeline + UI in one command.
- `leads.db` — staged leads (generated, SQLite). `outreach-tracker.csv` — record replies/calls (the verdict source).
- `../gtm-icp-v1.md` — the ICP + sourcing seeds + the gate metric.

When a lead is sent its status flips to `sent` in `leads.db`. Record outcomes (replied/booked) in `outreach-tracker.csv` for the verdict.

## Verdict → spec status
After 14 days, count `status=booked` rows in `outreach-tracker.csv` (you set these as calls get booked):
- **≥2 booked qualified calls** → set spec §2 Status = `passed YYYY-MM-DD`, proceed to Slice 1.
- **<2** → set spec §2 Status = `FAILED YYYY-MM-DD`, STOP, pivot to content-inbound (brainstorm Combination B).

## Guardrails
- Send stays human-gated (per-draft click) on purpose — deliverability + you vouch for every email.
- Verified-emails-only, low volume, personalized. Quality over blast.
- Throwaway pilot tooling; the real engine is Slices 1–5 (mcp-gtm + Corteza + managed runtime).

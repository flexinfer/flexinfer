# Slice 0 Kill-Test — Operator Handoff

**Date:** 2026-06-24
**Status of tooling:** complete, tested (6/6 regression tests pass), hardened.
**Status of gate:** `not run` — execution is operator-gated by design (human send + booked calls).

This is the run sheet for executing the GTM kill-test. The build is done; only a human
can run the send-and-book loop. See [../20-product-spec-gtm-engine.md](../20-product-spec-gtm-engine.md) §2
for the gate definition and [../gtm-icp-v1.md](../gtm-icp-v1.md) for the ICP.

## The gate (unambiguous)
Book **≥2 qualified discovery calls within 14 days** of first send.
- ≥2 booked → spec §2 Status = `passed YYYY-MM-DD`, proceed to Slice 1.
- <2 → spec §2 Status = `FAILED YYYY-MM-DD`, STOP, pivot to content-inbound (brainstorm Combination B).

> **2026-06-24 operator decision:** Slice 1 (Corteza enablement) was authorized to start
> in parallel, *overriding* the riskiest-assumption gate. The kill-test still owns the
> go/no-go on Slices 2–5; Slice 1 is the cheap, reversible CRM-config prerequisite.

## Run sheet
```bash
cd .loom/slice0-pilot
cp .env.example .env          # paste TAVILY_API_KEY (+ SMTP_* to actually send)
./run.sh --max 50             # port-forwards proxy, warms model, sources+drafts, opens UI
# open http://127.0.0.1:8765  # review each lead, edit, click Send per lead
```
Nothing auto-sends. `run.sh` handles the `flexinfer-proxy` port-forward (LLM is in-cluster,
behind Cloudflare Access) and cold-start (~1–2 min).

## Current state (2026-06-24)
- **3 clean staged leads** ready to review/send: Lightly (score 92), Crosby (95), Corvera (95).
- 4 `skipped` rows in `leads.db` are **stale pre-fix artifacts** (aggregator domains /
  `jane@` placeholders from an earlier pipeline version) — they can never be sent; the
  current code drops these at [pipeline.py:517](pipeline.py) and in `find_email()` before any insert.
- No real batch has been sourced yet — the staged set is from a tiny debug run. A real
  `--max 50` run fills the review queue (~150–200 Tavily credits, basic depth).

## Recording the verdict
1. Send reviewed drafts (per-lead button) → status flips to `sent` in `leads.db`.
2. As replies/calls land, add rows to `outreach-tracker.csv` (status: `sent|replied|booked`).
3. After 14 days, count `status=booked` rows. That count IS the verdict.

## Tooling map
| File | Role |
|---|---|
| `pipeline.py` | source→mine→enrich→score→hybrid-email→draft→`leads.db` (never sends) |
| `server.py` | local review/send web app (edit + per-lead Send via SMTP) |
| `store.py` | SQLite backend (`leads.db`): dedup by domain, status lifecycle, resumable |
| `run.sh` | one-command: port-forward + pipeline + UI |
| `test_pipeline.py` | 6 stdlib regression tests (`python3 test_pipeline.py`) |

## Guardrails (do not relax)
- Send stays human-gated (per-lead click). You vouch for every email.
- Verified-emails-only preferred; guessed addresses flagged ⚠ — vet before sending.
- Low volume, personalized. Quality over blast (deliverability + spam risk).
- PII protection: `leads.db`, `.env`, skip CSVs are gitignored — never commit them.

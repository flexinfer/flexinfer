# Result: S5 stage A, token coverage for a pruned MTP draft vocabulary (2026-11-06)

This is slice S5 of `10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` §5.3, candidate 1 (gap G1). The idea is
to restrict the MTP draft's `lm_head` to a fixed, frequency-ranked token subset.

**Why it looked worth doing.** S4 measured the two full-vocabulary draft heads at 17.0 % of a decode step's GPU time:
5.42 of 31.85 ms at 2K, on the XTX (row `gfx1100-s4-step0-decode-anatomy-2026-11-05`).

**What stage A asks, before any canary.** Does a subset of V′ = 32,768 rows, or failing that 65,536, cover the tokens
the workhorse actually writes on our mix?

**Pre-registration.** The rules are in `60-validation-matrix.md`, row `gfx1100-s5-draft-vocab-coverage-2026-11-06`.
They were written before any answer was generated, and pushed at 17:49Z in `79ce229bd`.

**Raw evidence.** Local only, under `/Users/cblevins/workspace/.loom/local/flexinfer/s5-draft-vocab-2026-11-06/`: prompts,
answers with every token id, the table, `coverage.json`, logs, and the table-freeze record `00-table-frozen.md`.

## Verdict

**Fail: stop.** Pooled held-out coverage misses the 98.5 % bar at both sizes, so the S5 canary does not run. Row
`xtx-s5-draft-vocab-canary-2026-11-06` is `skip` for its S5 arms.

| V′ (rows) | Pooled coverage | Bar | Without non-English answers |
|---|---|---|---|
| 16,384 (reported) | 88.96 % | — | — |
| **32,768** | **93.37 %** | ≥ 98.5 % | 98.00 % |
| **65,536** | **95.69 %** | ≥ 98.5 % | 99.58 % |

The Q2 fix in the same brief does not depend on S5:
- It merges on its own (plugin-common 0.6.0).
- It gets its re-test on a short canary of its own: row `xtx-q2-segmented-live-kv-2026-11-06`,
  `deploy/debug/qwen38-27b-q2-live-kv-canary.yaml`.

## 1. What ran

| Item | Value |
|---|---|
| Prompts | `scripts/s5-draft-vocab.py prompts`: 540 unique prompts (sha256 `d0ccee1f…`), split by prompt into 270 ranking and 270 held-out. Per half: code 80, tool calls and JSON 68, prose 80, non-English 42 (es, fr, de, pt, it, zh, ja, ko, ru); 35 held-out prompts with thinking on |
| Answers | The live workhorse, `runtime@b06b06fe`, vLLM 0.32, on cblevins-5930k, reached through flexinfer-proxy (`http://flexinfer.lan`). Concurrency 1, temperature 0, `max_tokens` 512 (1,024 with thinking), 17:51:00Z–18:46:31Z. 540/540 answered, 0 retries, 0 empty answers. The token id count matched `usage.completion_tokens` for every answer. The runbook session cleared the load: it touched no gte lane. |
| Tokens | Ranking half 100,574. Held-out 99,786: code 41,918, prose 28,359, non-English 18,521, tools 10,988. Finish reasons for the held-out half: 129 stop, 99 length, 42 `tool_calls` |
| Table | Built by `rank` from the ranking half (100,574 tokens, 15,044 distinct ids) plus this repo's tracked text (1,339 files, 13.1 MB). Corpus weight 0.05, picked by 2-fold CV on the ranking half: 0.92096 at w = 0, 0.92134 at every w > 0. Every one of the 33 added ids (248044–248076) is included. Unseen ids follow in id order. sha256 `77a38385…`, recorded at 18:47:03Z before `coverage` read any held-out answer |
| Script | `scripts/s5-draft-vocab.py` on branch `feat/s5-mtp-draft-vocab` at `79ce229bd`, unchanged through the run. Coverage goes through the plugin's own `select_ids` |

## 2. Coverage by slice (held-out)

| Slice | Tokens | 16,384 | 32,768 | 65,536 |
|---|---|---|---|---|
| code | 41,918 | 92.77 % | 97.85 % | 99.58 % |
| prose | 28,359 | 92.11 % | 97.64 % | 99.49 % |
| tool calls and JSON | 10,988 | 96.82 % | 99.51 % | 99.83 % |
| thinking on (across categories) | 23,141 | 92.58 % | 97.06 % | 98.50 % |
| **non-English** | 18,521 | 70.87 % | **73.03 %** | **78.62 %** |
| — zh | 3,567 | 63.64 % | 64.76 % | 66.61 % |
| — ru | 1,602 | 62.92 % | 62.98 % | 68.17 % |
| — de | 2,763 | 64.35 % | 69.35 % | 82.66 % |
| — ko / ja | 2,048 / 1,577 | 70.17 / 71.40 % | 70.80 / 71.59 % | 74.71 / 73.24 % |
| — es / fr / it / pt | 3,098 / 1,330 / 2,048 / 488 | 79–81 % | 80–84 % | 86–91 % |
| **pooled** | 99,786 | 88.96 % | **93.37 %** | **95.69 %** |

**The answer-level picture is the same.** The 5th-percentile answer covers 71.1 % at 32,768 and 75.6 % at 65,536; the
worst covers 53.7 % and 56.7 %. All 14 answers in the bottom 5 % are non-English at both sizes; the lowest English
answer (prose) covers 80.3 % at 32,768 and 92.6 % at 65,536.

## 3. Why it misses

The shortfall is the ranking source, not the idea.

- **Most misses come from non-English answers.** At 32,768 rows, 6,621 held-out tokens miss (4,673 distinct ids), and
  75.5 % of them come from the 18.6 % of tokens in non-English answers. Code, prose and tool calls alone reach 98.00 % at
  32,768 and 99.58 % at 65,536.
- **The ranking saw too little non-English text.** The ranking half held 42 non-English answers (about 18K tokens), and
  the repo corpus is English and code. So 12.2 % of held-out tokens never appeared in any ranking answer. The CV score
  of 92.1 % on the ranking half itself said the same, before the held-out half was read.
- **The fallback cannot rescue them.** Unseen ids fall back to id order, which roughly follows merge order for the
  original Qwen vocabulary. But Qwen's CJK, Hangul and kana tokens sit at ids ~95K–155K, above even 65,536.
  - The most-missed ids at 65,536 are everyday Chinese and Korean tokens: `高` 95883, `训练` 99476, `加速` 103806,
    `表` 96038, `你` 95933, `기를` 153275, plus kana `ソ` 149261.
  - A ranking that had seen a few MB of Chinese would include them at any V′.
- **At 32,768 even English and code miss 2 %.** The misses are ordinary words (` java`, ` cargo`, ` println`, ` rust`,
  ` das`). The ranking half's 100K tokens are too few to rank the tail; that is what 65,536 fixes.

## 4. What it would take to try again

Each of these needs a new row, written before data, and a **fresh held-out prompt set**. The held-out answers here have
now been read, so a ranking tuned against them would be scored on its own test data.

1. **Settle what "our mix" is, from traffic.**
   - The pre-registered mix puts 15 % of prompts (18.6 % of tokens) in non-English. The workhorse's real consumers are
     Mills, psyche-simulation, news-analyzer and the jobsearch daemon, mostly English.
   - Neither the proxy nor vLLM logs prompt text, so the language share needs a short sampled measurement (for example,
     a proxy-side counter of non-ASCII share per request, without content).
   - At a 2–5 % non-English token share, this table's 65,536 rows would read about 98.5–99.2 %, and its 32,768 rows
     96.8–97.5 % (a mix-weighted estimate, not a result).
2. **Rank from a corpus that covers the languages.**
   - For example, a few MB per language of public text, or a much larger set of the workhorse's own multilingual
     answers, tokenized with this `tokenizer.json`.
   - Expected effect (estimate): common CJK and Hangul ids move into the first 32K, and non-English coverage at 65,536
     rises well above 79 %.
3. **Drop the subset idea and shrink the bytes instead.**
   - vLLM 0.33 already has `speculative_config.draft_lm_head_quantization` (`fp8` / `nvfp4`). It gives the drafter a
     quantized copy of the shared head, keeps the full vocabulary, and has no coverage risk.
   - On gfx11 there is no hardware fp8, and nvfp4 goes through the platform W4A16 path. Neither is tested here; it first
     needs a microbench against `wvSplitK` on [n, 5120] × [248320, 5120].

**Rejected along the way.** Before the run, the orchestrating session chose the gather over the zero-copy alternative
for stage B's pool bar. The alternative was to permute the shared head's rows in place so the subset is a contiguous
view. It keeps the pool, but adds a per-step gather over [n, 248,320] on the target's `compute_logits`: a cost, and a
parity risk on the path S5 promised not to touch. It stays the fallback if the pool cost ever matters more than the
target path.

## 5. What exists, and where

- **The S5 code is ready but unmerged**, on Draft MR branch `feat/s5-mtp-draft-vocab`.
  - Text plugin 0.13.0, `draft_vocab.py`, behind `FLEXINFER_MTP_DRAFT_VOCAB`. It wraps MRV2
    `MTPSpeculator.load_draft_model` inside the memory-profiling window and overrides `Qwen3_5MTP.get_top_tokens`.
  - Plus its tests, the build-verifier check and the stage A script.
- **It was checked against real images, CPU only**, in the cached serving images `29378fe0` (vLLM 0.33.0+rocm72) and
  `527e2714` (0.32.0+rocm723) on cblevins-7900xtx:
  - the build verifier passes with the patch installing;
  - the pruned argmax equals the full head's argmax masked to the subset.
- **The hook points are byte-identical in vLLM v0.32.0 and v0.33.0** (`qwen3_5_mtp.py`, `mtp/speculator.py`).
- **It stays unmerged until a new stage A passes**, because shipping it would put a known-insufficient table into the
  workhorse image.
- **The stage B rules stay valid for a rerun.** They are in row `xtx-s5-draft-vocab-canary-2026-11-06`, including the
  pool bar fixed per V′: D = 4,133 tokens at 32,768 rows, 8,265 at 65,536, plus the 32K × 2 `KVPoolShort` check.

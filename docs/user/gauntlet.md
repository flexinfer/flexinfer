# Gauntlet: serving verdict primitive

The gauntlet turns raw inference measurements into a structured **PASS/FAIL
verdict** against operator-supplied thresholds. It is the shared verdict
mechanism for the experiment platform: the vLLM currency-canary kill-test, the
weekly eval gauntlet, and the `ModelExperiment` controller all decide
whether a serving canary is healthy by feeding a measured `Sample` through
`gauntlet.Evaluate`.

- Package: `pkg/gauntlet` (`Evaluate` is pure and unit-tested; `Probe` does the HTTP measurement)
- CLI: `flexinfer-bench --gauntlet ...`
- Plan: [.loom/30-implementation-plan-experiment-platform-2026-06-15.md](../../.loom/30-implementation-plan-experiment-platform-2026-06-15.md) (Slice 2)

## Verdict contract

A run produces a `Verdict { pass, checks[], summary }`. `pass` is true only if
every **included** check passed. Each gate is opt-in — a zero-valued threshold
(or empty expect list) skips that check, so you assert exactly what you care
about. The `served` check always runs; if the model does not serve a successful,
non-empty response, the numeric/coherence checks are skipped and the verdict
fails.

| Check | Enabled by | Passes when |
|-------|-----------|-------------|
| `served` | always | model returns a successful, non-empty completion |
| `min_tokens_per_second` | `MinTokensPerSecond > 0` | decode throughput ≥ floor |
| `max_ttft` | `MaxTTFT > 0` | time-to-first-token ≤ max (an unmeasured TTFT fails) |
| `min_completion_tokens` | `MinCompletionTokens > 0` | tokens generated ≥ min |
| `coherence` | `CoherenceExpect` non-empty | completion contains the expected substrings (case-insensitive) under mode `all` (default) or `any` |

## CLI usage

```bash
flexinfer-bench \
  --model google/gemma-4-26B-A4B-it \
  --model-name gemma4-26b-a4b-gptq \
  --backend vllm \
  --configmap unused-for-gauntlet \
  --gauntlet \
  --gauntlet-api chat \
  --gauntlet-min-tps 20 \
  --gauntlet-max-ttft 2s \
  --gauntlet-min-tokens 8 \
  --gauntlet-prompt "What is 2 + 2? Answer with just the number." \
  --gauntlet-expect "4" \
  --gauntlet-expect-mode all
```

Throughput comes from the existing benchmarker (robust, multi-iteration);
coherence, TTFT, and the generated text come from a single streaming probe. The
command prints the verdict as JSON and **exits non-zero on FAIL**, so it gates CI
and controller steps directly. Example output:

`chat` is the CLI default and sends an OpenAI `messages` payload to
`/v1/chat/completions`. Use `--gauntlet-api completions` only for base models
that expect a raw `prompt` at `/v1/completions`.

```json
{
  "pass": true,
  "checks": [
    {"name": "served", "pass": true, "want": "model serves a successful response", "got": "served"},
    {"name": "min_tokens_per_second", "pass": true, "want": ">= 20.00 tok/s", "got": "58.90 tok/s"},
    {"name": "coherence", "pass": true, "want": "all of [4]", "got": "all expected substrings present"}
  ],
  "summary": "PASS (3/3 checks)"
}
```

## Programmatic use

```go
sample, _ := gauntlet.Probe(ctx, http.DefaultClient, chatCompletionsURL,
    gauntlet.ProbeRequest{
        API: gauntlet.ProbeAPIChat, Model: model,
        Prompt: "What is 2 + 2?", MaxTokens: 16,
    }, nil)
verdict := gauntlet.Evaluate(sample, gauntlet.Thresholds{
    MinTokensPerSecond: 20,
    CoherenceExpect:    []string{"4"},
})
if !verdict.Pass {
    // canary rejected
}
```

The `ModelExperiment` controller (Slice 4) will call `Evaluate` against a
canary's measured `Sample` and write the `Verdict` into the experiment's status.

## Tool-call conformance gate (`tool_call_conformance`)

`flexinfer-bench tool_call_conformance` is the D4 gate and the harness of kill-test KT-SO
(`.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.4). It drives
schema-constrained tool-call turns through `internal/agentloop` against one lane and answers
whether that lane's structured outputs can be certified (`vllm/structured-outputs`,
`docs/agents/gpu-compatibility.md`).

```bash
flexinfer-bench tool_call_conformance --url http://<lane-service>:8000 --model <served-name> \
  --parity-baseline parity_code=8174a49e --image <image@sha256> --context-length 32768 \
  --out /tmp/kt-so.json
```

`--url` may be the Model's Service or pod, or the proxy (`http://<proxy>/model/<name>`); through
the proxy pass `--metrics-url` to the lane's own `/metrics`.

What it runs, in order:

1. **Greedy parity.** The three parity prompts of `scripts/verify-vllm-mtp.py` at temperature 0,
   256 tokens; the hash is the first 16 hex characters of the answer's SHA-256, so
   `parity_code` compares with recorded hashes (8174a49e on the workhorse image `b06b06fe`).
2. **Constrained arm.** `--turns` (200) tool-call turns in append-only agent-loop sessions of
   `--session-turns` (5). Sessions cycle `--modes`: `required` (tools + `tool_choice:
   "required"`), `named` (`tool_choice` naming the expected tool), `json_schema`
   (`response_format` json_schema call envelope, `strict: true`) and `structured_outputs` (vLLM
   `structured_outputs.json`, same envelope). Six tools (enums, integer and number bounds, regex
   patterns, arrays, `additionalProperties: false`), deterministic prompts, temperature 0. A good
   turn appends the call and a canned tool result; a failed turn restarts its conversation.
   `--warmup-turns` (4) unmeasured turns run first, outside the metrics window.
3. **Unguided arm.** The same turns with the constraint removed (`tool_choice: "auto"`, no
   `response_format` / `structured_outputs`): the decode and acceptance baseline, and the parse
   rate the lane manages without constraints.
4. **Parity again**, which must match step 1.

Every reply lands in one class: `ok`, `invalid_json` (arguments or content not one JSON value,
prose instead of a call), `schema_violation` (valid JSON breaking the tool schema or the envelope,
or a call returned as message content instead of `tool_calls`), `wrong_tool_name` (a tool not
offered, or not the one `named` forced), `truncated` (`finish_reason: length`) and `request_error`
(transport error or non-2xx). Decode tokens/s is `(Δvllm:generation_tokens − Δvllm:request_success)
/ Δvllm:request_decode_time_seconds_sum` over each arm's window; position-0 acceptance is
`Δspec_decode_num_accepted_tokens_per_pos{position="0"} / Δspec_decode_num_drafts`.

Pass rule (§6.4, all four): 100 % of constrained turns `ok`; constrained decode ≥ 0.90 × unguided;
constrained pos-0 acceptance ≥ 0.75 (`--expect-spec-decode=false` makes it n/a on a lane without
speculative decoding); parity hashes before = after (= `--parity-baseline` when given). Missing
metrics fail the decode and pos-0 checks as unmeasured. Output: a JSON report on stdout (and
`--out`), the 13-column `.loom/60-validation-matrix.md` row on stderr and in `matrix_row`; exit 0
on PASS, 1 on FAIL, 2 on a flag error.

## Diarization conformance gate (`diarize_conformance`)

`flexinfer-bench diarize_conformance` is the D5-a gate and the scorer of kill-test KT-DIAR
(`.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.5, window plan
`.loom/32-iteration-plan-d5a-diarize-job-2026-11-01.md`). It runs no inference: it scores a
`diarize=true` job's content and the standalone pyannote Deployment's answer for the same clip
against a reference RTTM, and answers whether the gfx906 profile may carry the
`pyannote/speaker-diarization` certificate (`docs/agents/gpu-compatibility.md`).
`scripts/kt-diar.py` produces its inputs in the window and prints the command.

```bash
flexinfer-bench diarize_conformance --reference EN2002a.rttm --uri EN2002a --ref-offset 0 \
  --hypothesis kt-diar/job.json --baseline kt-diar/baseline.json \
  --gte-baseline-p95-ms 64.1 --gte-run-p95-ms 66.0 --image <runtime image@sha256> --out kt-diar.json
```

- `--hypothesis`: `GET /v1/jobs/{id}/content` of the diarize job (words with `speaker`,
  `utterances`, `diarization.segments`).
- `--baseline`: the `/diarize` JSON, or any RTTM.
- `--reference`, `--uri`: the AMI `only_words` RTTM and its file id; `--ref-offset` moves
  reference times onto the clip (`-300` for a clip cut from 5:00) and `--duration` (default: the
  transcript's) bounds the scored region.

DER is md-eval's: missed + false alarm + confusion over reference speech, overlapped speech
counted per speaker, a `--collar` (0.25 s) excluded each side of every reference boundary, and
each hypothesis speaker mapped to at most one reference speaker by the assignment with the most
overlap (Hungarian, `pkg/transcript`). Checks, all required: `meeting_length` ≥ `--min-minutes`
(29.5); `speaker_attributed_words` ≥ `--min-word-coverage` (95 %) of the job's words carry a
speaker; `der_vs_standalone`: the job's DER (its `diarization.segments`) at most `--max-der-delta`
(5) points above the standalone's; `gte_p95` ≤ (1 + `--gte-tolerance`, 0.20) × baseline. Without
the gte flags the verdict is `INCOMPLETE`. The DER of the turns the labelled words imply is
reported as `words_der`, not gated. Output as `tool_call_conformance`: JSON report, the 13-column
matrix row on stderr and in `matrix_row`; exit 0 on PASS, 1 on FAIL or INCOMPLETE, 2 on a flag
or input error.

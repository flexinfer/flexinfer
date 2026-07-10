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

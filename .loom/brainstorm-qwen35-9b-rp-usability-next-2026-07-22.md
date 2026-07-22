# Brainstorm: Qwen3.5-9B RP usability and next optimization

**Date**: 2026-07-22
**Triggered by**: Continue improving the now-fast Qwen3.5-9B GPTQ + rank-64
RP lane as the usable replacement for the former 9B RP service.
**Constraints noted**: one 24 GiB gfx1100; preserve the verified 131K window,
native W4A16 path, and adapter contract; do not edit the production Model until
an owned canary passes.

## Baseline

The vLLM 0.23 promotion removed the main kernel bottleneck: the base reaches
about 102 output tok/s, the short LoRA path about 60 tok/s, and two LoRA
streams sustain 55.68 aggregate tok/s while retaining exact 5/5 recall in a
127,969-token prompt. Live metrics since that rollout show 42 requests, a
roughly 733-token average prompt, 38 prompts at or below 1,000 tokens, roughly
529 output tokens per request, about 0.25-second average TTFT, and no failures.
Prefix reuse and maximum context therefore are not the current ordinary-traffic
bottlenecks.

A direct smoke without request-level chat-template kwargs exposed the larger
usability problem: Qwen3.5's default thinking mode emitted `Thinking Process:`
into normal base and LoRA content and exhausted the short output limit. Adding
`chat_template_kwargs.enable_thinking=false` returned the requested literals
immediately. vLLM 0.23 provides a server default for these kwargs and documents
that a request value takes precedence.

## Phase 1 — Framings

### F1 — Make non-thinking the RP server default

Pass `{"enable_thinking":false}` through vLLM's
`--default-chat-template-kwargs`, while preserving explicit request overrides.

- **Bet**: removing hidden chain-of-thought from ordinary RP requests cuts
  wasted tokens and makes existing clients usable without bespoke payloads.
- **Risk**: the plugin, streaming path, or dynamically loaded LoRA may not honor
  the merge semantics exactly as the upstream CLI documents.

### F2 — Install RP-specific generation defaults

Set a conservative temperature/top-p/repetition profile server-side, still
allowing requests to override it.

- **Bet**: coherent RP defaults reduce repetitive or sterile output for clients
  that send only messages and a token limit.
- **Risk**: generation taste is workload-dependent and could silently override
  deliberate client behavior.

### F3 — Bisect the recall ceiling

Probe five-depth recall at 160K, 192K, and 224K between the 131K pass and 245K
failure.

- **Bet**: the lane can expose more verified context without changing weights.
- **Risk**: almost all observed traffic is below 5K, so this spends GPU time on
  capacity that does not improve current usability.

### F4 — Test hybrid prefix caching

Enable the Mamba align-mode cache in a disposable arm and measure cache hits,
TTFT, and correctness on append-only multi-turn transcripts.

- **Bet**: long recurring conversations avoid repeated prefill.
- **Risk**: current traffic has short prompts and no demonstrated prefix reuse;
  upstream labels the hybrid path experimental.

### F5 — Restore native MTP-1

Qualify a model-matched native draft head under the abliterated base and
rank-64 adapter, measuring acceptance and text throughput at concurrency.

- **Bet**: accepted speculative tokens reduce light-load inter-token latency.
- **Risk**: the available draft tensors came from a different base posture,
  while MTP can reduce aggregate throughput by consuming KV capacity.

### F6 — Requantize W4/G32 for quality

Blind-compare a smaller-group GPTQ artifact against W4/G128 while keeping the
same gfx1100 W4A16 kernel and 131K envelope.

- **Bet**: finer quantization groups recover character voice and instruction
  adherence without a large memory increase.
- **Risk**: conversion metadata or kernel behavior may cost speed and VRAM for
  no perceptible quality gain.

### F7 — Split interactive and maximum-context profiles

Keep a warm 64K-96K interactive lane and retain 131K as a demand-start route
only if a later optimization cannot coexist with maximum context.

- **Bet**: smaller reservations permit more aggressive batching or features.
- **Risk**: shared-group swaps and route ambiguity are worse than the capacity
  saved, especially now that 131K already fits with graphs.

## Phase 2 — Cross-pollinations and tensions

- **F1 + F2** could produce a true zero-configuration RP endpoint, but only F1
  fixes an observed failure and has model-defined semantics. Generation taste
  should remain client-owned until separately evaluated.
- **F1 + F4** would improve both wasted decode and repeated-prefill cost, but
  the live workload supports F1 and currently disconfirms F4 as the first move.
- **F3 + F7** makes sense only if users actually need more than 131K or if a
  faster feature forces a context trade. Neither condition is present today.
- **F5 + F6** compounds model-artifact risk and prevents clean attribution;
  each needs its own quality and throughput arm.
- **Default simplicity vs. expert control** is the key tension. A server default
  must improve old clients without blocking explicit thinking requests.

## Phase 3 — Convergence

### Recommended: F1

Add one backend config key, `defaultChatTemplateKwargs`, serialize native maps
to vLLM's CLI JSON, and run an owned 9B candidate with only
`enable_thinking=false` changed. The gauntlet must omit request kwargs for its
normal base, LoRA, throughput, concurrency, and 128K recall requests. Separate
literal probes must prove the default, and explicit `enable_thinking=true`
probes must prove request precedence.

### Runner-up: F3

Once the default behavior is shipped, bisect the recall ceiling because it is
a clean model-quality experiment that does not perturb the already-fast
execution path. It is lower priority because the current prompt histogram is
overwhelmingly short.

### Open question

Should public clients expose the explicit thinking override in UI, or keep it
as an advanced API-only control? The server mechanism supports either and does
not block this slice.

## Riskiest assumption + kill-test

**Load-bearing assumption**: the exact pinned vLLM 0.23 Qwen3.5 plugin honors
`--default-chat-template-kwargs '{"enable_thinking":false}'` for both the base
model and a dynamically loaded rank-64 LoRA, while request-level
`chat_template_kwargs.enable_thinking=true` still takes precedence.

**Kill test**: boot a disposable candidate with the certified model, runtime,
graphs, FP16 KV, LoRA, and 131K profile, changing only the server default. Send
base and adapter literal requests with no chat-template kwargs and require the
exact literal, no reasoning field, and no thinking marker. Repeat with an
explicit true override and require non-literal thinking behavior. Run the
existing short, multi-turn, two-stream, and five-depth 128K gauntlet with the
request kwargs omitted; require no more than 5% throughput regression, exact
recall, the immutable image digest, zero restarts/faults, and parent restoration.

**Disconfirming evidence searched**: upstream vLLM issues do contain adjacent
failures—Qwen3.6 streaming with disabled thinking and Gemma tool-call leakage—
but the search found no reported Qwen3.5 v0.23 failure of request precedence.
That absence is not proof; the live base-plus-LoRA canary is the deciding test.

**Failure mode if wrong**: old RP clients still leak thinking, an explicit
thinking client loses control, or the adapter differs from the base. Keep the
production profile unchanged and require clients to send the known-good false
kwarg while isolating the plugin behavior.

**Status**: validated. Run 1 proved every behavior, quality, context, and
single-stream gate but narrowly missed the strict concurrent floor. The
same-threshold warm-cache rerun passed at 56.0145 median aggregate tok/s,
preserved 102.6556 base / 60.113 short-LoRA / 40.6942 multi-turn tok/s, and
recalled all five needles from 127,969 prompt tokens. Default non-thinking and
explicit thinking opt-in both passed for the base and dynamically loaded LoRA.
MR !930 promoted the single tested behavior key to production; direct base and
LoRA production probes passed with the Model Ready and adapter Loaded 1/1.

## Handoff

- If chosen → `rapid-dev-iteration-loop` with
  `deploy/experiments/qwen35-9b-default-nonthinking-canary.yaml`.
- Evidence log →
  `.loom/iteration-qwen35-9b-default-nonthinking-2026-07-22.md`.

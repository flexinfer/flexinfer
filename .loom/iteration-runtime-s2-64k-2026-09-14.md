# Runtime S2: extended 64K qualification

## Scope and hypothesis

Continue merged !1058 for issue #74. The first smoke used one request slot;
the production XTX 64K lane uses two. Test that envelope on the isolated
5930k canary while the unchanged XTX lane provides the Mills fallback.
This is synthetic qualification on the same GPU architecture, not a
certificate for the production XTX node, 128K workhorse, or LoRA lanes.

Riskiest assumption: the small fresh-compile KV margin still accommodates
a nearly full context and releases capacity correctly after cancellation.
Test long recall before the five-minute concurrent workload.

## Artifact and configuration

- Runtime: `registry.harbor.lan/flexinfer/runtime@sha256:ad93ae726bbeb27b2731e3d9ca1ba8f0a9d4234ad7c6d58ef6423e4dc40beeb5`.
- HF source revision: `2d7425f78ee0abd518aceabf385997fbb54c9342`.
- Model: `qwen38-27b-v026-canary`; node: `cblevins-5930k`.
- Retain 64K, 0.96 GPU utilization, native W4A16, TRITON_ATTN, FP8 E4M3,
  chunked prefill, disabled prefix caching, and the non-thinking default.
- Set `maxNumSeqs: 2` and `dedicatedDeployment: true` to match the intended
  64K lane. Production model names, aliases, and pins remain unchanged.
- Fresh compile path: `/var/lib/flexinfer/compile-cache-qwen38-27b-v026-seqs2-5930k`.
- Pod: `qwen38-27b-v026-canary-59f6d84775-jt4wc`.

## Preflight and startup

Both nodes Ready without DiskPressure or MemoryPressure. Workhorse metrics
showed zero running and waiting requests and zero KV usage. The 5930k root
filesystem had 222 GiB free. The live Mills deployment's
`FLEXINFER_WEAVER_MODEL_FALLBACKS` points to the Ready XTX lane.
Production config hashes and mtimes were captured before the canary start.

The exact imageID matches the pinned digest. Plugin registration reported
11 FLA autotuners and the engine selected `RDNA3W4A16LinearKernel`.
Weights loaded in 26.42 s. Fresh `torch.compile` took **87.49 s**;
at 00:23:42Z on September 15 the engine reported **2.36 GiB KV / 71,361 tokens**.
The API started at 00:23:54Z with zero restarts.

## Reproducible probes

Copy `scripts/qualify-vllm-canary.py` and `scripts/verify-vllm-canary.py`
into the same directory inside the isolated pod, then run:

```sh
python3 qualify-vllm-canary.py --model qwen38-27b-v026-canary
```

The script has a 30-minute overall bound, 90-second ordinary HTTP timeouts,
a 900-second long-context timeout, and JSONL results. `--phase context|tools|load`
reruns one gate. The default
soak is 300 seconds with two simultaneous requests.

- Context: random independent keys at 0%, 50%, and 100% depth; `/tokenize`
  calibration reserves output capacity; chat usage must confirm the count;
  all three key-value pairs must match exactly without truncation. The final
  probe requests `response_format: {"type":"json_object"}` explicitly.
- Tools: automatic and required structured calls with exact arguments,
  valid call IDs, then a tool-result round trip using a fresh random count.
- Cancellation: observe an active request and nonzero KV, disconnect while
  generating, require running/waiting/KV to drain, then verify recovery.
- Load: two 256-token novel generations per pair for five minutes; require
  the server to report two active requests, complete every stream, and
  drain all request/KV state afterward.

The original ten smoke regression tests and nineteen extended-gate tests pass.
Server-side dry-run of the two-sequence manifest and `git diff --check` pass.

## Live results and cleanup

The first context attempt used 65,364 prompt tokens but hit the probe's
240-second client deadline. This is a harness failure, not a recall result.
The existing dense-27B notes (`10-research-gfx1100-model-quality-2026-08-15.md`,
sections 8c and 8h) document roughly 300 s prefill at 60K and a 900-second
needle-test timeout. The probe now uses that budget for context only and has
a regression test for it. The corrected run is recorded separately; it reuses
kernel shapes warmed by the incomplete attempt.

Both automatic and required tool calls returned exactly `lookup_depot` with
`{"depot":"Birch"}`, valid IDs, and `finish_reason: tool_calls`. Round trips
returned the independently supplied counts `9535` and `6089`; neither emitted
reasoning. Follow-up TTFT was 0.3647 / 0.3614 s.

All three cancellation probes observed one running request and 8.33% KV
usage, then zero running/waiting requests and zero KV usage. Recovery answered
`42`; disconnect-to-drain-plus-recovery took 0.775 / 0.793 / 0.768 s.
The final script reports drain time separately from the recovery request.

The concurrent soak passed **82 requests / 41 pairs in 303.81 s**. Both request
slots were observed active together. Median per-request decode was **39.35 tok/s**;
TTFT median **0.366 s**, p95 **0.4261 s**, with a first-pair warmup maximum of
**7.0386 s**. Every generation completed 256 tokens. Final running/waiting/KV
metrics were zero, and the same pod remained Ready with zero restarts.

With the corrected timeout, the 65,359-token prompt returned all three exact
keys from the beginning, middle, and end. TTFT was **353.8337 s** and decode
**25.4465 tok/s** (63 output tokens). However, the unconstrained response was
wrapped in a Markdown JSON fence, so the strict JSON gate failed. This is a
format-contract failure despite correct recall; the raw failure is retained.
The final probe explicitly requests the API's JSON mode and has a regression
test rejecting fenced output. Prompt-only callers must still handle this
observed formatting behavior. The Mills client currently does not request
`response_format`, but its `extractJSONCandidates` path and fenced-JSON
regression test already handle this form (`loom-core/pkg/mills/clients/flexinfer.go`
and `flexinfer_test.go`). This observation alone is not a Mills regression.

The explicit-JSON repeat passed: **65,365 prompt tokens**, all three random
keys correct, plain valid JSON, and `finish_reason: stop`. TTFT was
**353.7214 s** and decode **25.3849 tok/s** (62 output tokens). Prefix caching
remained disabled, so both complete recall runs processed their full inputs.

The measured 64K/two-request configuration passes the synthetic recall,
structured-output, tool-round-trip, cancellation, and five-minute load gates.
This does not qualify two simultaneous near-64K prompts, an extended
production-duration soak, the XTX node's own memory/device envelope, 128K,
or LoRA. Production promotion remains gated on the intended lane's rollout.

The final short smoke passed 3/3 coherent non-thinking probes, with
**2.3575 s** TTFT at 2,337 tokens and **41.4356 tok/s** median novel decode.
The canary remained Ready with zero restarts throughout the qualification.
Its parent Model was deleted after all probes. The restored workhorse pod
`qwen38-27b-autoround-workhorse-7c464c5cdb-gr5vt` is Ready with zero restarts,
using its original image:
`registry.harbor.lan/flexinfer/runtime@sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`.
It returned `4` for `2+2` through the proxy, with fingerprint
`vllm-0.23.0-203f4341`. Both production Models are Ready; their config hashes
and mtimes are unchanged. The XTX fallback stayed Ready throughout, and no
temporary serving Model remains. Final canary logs and the node kernel
journal contain no GPU fault/reset or out-of-memory markers.

The existing model cache and the isolated compile cache remain for reuse.
All probe processes ended; the process audit found no stale benchmark process
from this run. Raw evidence:
`.loom/local/runtime-s2-64k-qualification/` in the reused worktree.

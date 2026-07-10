# Chat-aware gauntlet follow-on

**Date:** 2026-07-10
**Roadmap milestone:** observe and promote the first deployed `ModelBackfill`
based on useful artifacts and foreground safety.

## Riskiest assumption + kill-test

**Load-bearing assumption:** the warm Gemma deployment supports OpenAI chat
completions with streaming content, while retaining raw completions as an
explicit compatibility path for base models.

**Kill test:** send the same background-classified prompt to both APIs on the
live `gemma4-26b-a4b-gptq-5930k` deployment. Pass only when
`/v1/chat/completions` returns the expected answer, `/v1/completions`
reproduces the failed gauntlet behavior, and the serving pod UID/restart count
remain unchanged.

**Failure mode if the assumption is wrong:** changing the gauntlet default
would replace one false failure with transport failures or would create model
demand that violates the backfill safety contract.

**Status:** passed 2026-07-10. Positive evidence: the background chat request
returned content `4`. Disconfirming evidence: the same prompt through raw
completions returned only a newline, reproducing the coherence failure. The
warm pod UID remained `4fb89ff2-f40c-4bbf-86c4-977ae6af4023` with zero
restarts.

## Review

- The first production backfill ran both configured benchmarks and persisted
  approximately 145-147 tokens/s results.
- Its terminal failure came only from the coherence probe using the legacy
  `/v1/completions` shape for a chat-tuned model.
- Background request classification and foreground-idle accounting worked as
  designed.

## Align

### Scope in

- Add chat and raw-completions probe modes.
- Make chat completions the gauntlet CLI default.
- Keep raw completions available through an explicit CLI/template setting.
- Parse streamed `delta.content` without regressing streamed `text` support.
- Update the deployed CronJob contract and operator documentation.

### Scope out

- Changing `ModelBackfill` admission, retry, or cancellation semantics.
- Weakening coherence thresholds.
- Adding multimodal/tool-call evaluation.
- Acquiring a GPU lease or changing model placement.

### Acceptance criteria

- The default gauntlet calls `/v1/chat/completions` with an OpenAI `messages`
  payload and captures streamed `delta.content`.
- `--gauntlet-api=completions` preserves the existing prompt/text path.
- Invalid modes fail before an HTTP request is sent.
- Both modes apply the internal background workload class.
- The deployed template states its mode explicitly and the rerun reaches a
  truthful terminal verdict without restarting the warm model.

### Dependencies and blockers

- Depends on the already-deployed background request class and warm 5930k
  model.
- No blocker identified; the live kill-test passed.

## Land

Planned areas:

1. `pkg/gauntlet`: request modes, payloads, SSE parsing, regression tests.
2. `cmd/flexinfer-bench`: CLI selection and endpoint routing.
3. `deploy/tasks/model-eval-gauntlet` and docs/roadmap: explicit operational
   contract and completed observation.

## Prove

- `go test ./pkg/gauntlet ./cmd/flexinfer-bench`
- `go test -race ./pkg/gauntlet ./cmd/flexinfer-bench`
- `make test`
- `go build -o /dev/null ./cmd/flexinfer-bench` (the documented per-component
  Make target is not present in the current Makefile)
- `golangci-lint run ./pkg/gauntlet/... ./cmd/flexinfer-bench/...`
- `kubectl kustomize deploy`
- Merge-request and master pipelines to terminal green.
- Flux reconcile and a live backfill rerun with pod UID/restart verification.

## Handoff/harvest

- Update `ROADMAP.md`, gauntlet README, and ModelBackfill troubleshooting.
- Preserve raw completions as the next diagnostic path for base-model lanes.
- Consider per-model evaluation profiles only after this single-mode selector
  proves sufficient in production.

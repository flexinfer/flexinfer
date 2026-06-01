# RALPH iteration plan: F4 agent-loop ReAct client — slice 1 (CLI) (2026-06-01)

Tracking:
- Brainstorm: `.loom/brainstorm-f4-long-context-agent-2026-05-25.md`
  (`F4-tool-loop-as-prefix` lines 140-146; **Open question** lines 320-329 —
  "pick the client form before writing the loop")
- Gating kill-test: `.loom/ralph-f4-tool-loop-as-prefix-2026-06-01.md`
  (**PASSED 2026-06-01** — append-only prefix reuse confirmed; matrix row 194)
- Roadmap: ROADMAP.md "ROCm gfx1100 Performance Tuning" + Innovation Roadmap
- Owner: Cody (claude-code agent)
- Status: In Progress (slice 1 of 2)

## Goal

Build the F4 ReAct client — the slice the kill-test gated. The riskiest
assumption ("vLLM re-renders each append-only turn as a block-aligned prefix
extension, so tool history is a near-free sunk cost") is **PASSED**, so the
spec-riskiest-assumption rule no longer blocks the client.

The brainstorm flagged the client *form* as a pick-first decision. Operator
chose **(a) CLI `cmd/agent-loop/` + (b) loom-core MCP tool**, scope **loop +
real tool execution**. Because (a) and (b) live in different modules/repos,
RALPH ships them as two vertical slices:

- **Slice 1 (this doc)**: CLI `cmd/agent-loop/` on a reusable
  `internal/agentloop/` engine, with real read-only tool execution. Establishes
  the canonical append-only prefix layout + tool protocol.
- **Slice 2 (queued)**: expose the same engine shape as an MCP tool loom-core
  hosts, mirroring this layout against the agent-context surface.

## Non-Goals (this slice)

- **No write/exec tools.** Slice 1 ships only read-only, path-jailed tools
  (`read_file`, `list_dir`). A misbehaving model cannot mutate the host.
- **No loom-core MCP tool yet** — that is slice 2, and it is a separate repo
  with its own build/registry/sync. Keeping the engine free of CLI/process
  globals so slice 2 can mirror the shape.
- **No APC flip on the production primary.** The client targets the
  APC-enabled canary (or any APC model); production posture is unchanged.
- **No streaming.** TTFT instrumentation uses the proxy's per-turn
  `X-Flexinfer-Upstream-Ms` header, not SSE first-byte (a later refinement,
  `F4-streaming-floor`).

## Design

`internal/agentloop/` — the reusable engine (no CLI deps):

- `Conversation` (conversation.go): the **append-only, mutability-ordered**
  context. System prompt immutable; history grows by `Append` only — there is
  deliberately no `Insert`/`Replace`/`Reorder`, so the prefix-cache invariant
  cannot be violated by accident. `Messages()` returns a fresh copy
  (`[system] + history`).
- `ChatClient` (client.go): OpenAI `/v1/chat/completions`, sets
  `X-Flexinfer-Cache-Key=session_id` to pin prefix-consistent routing. A
  context-overflow 400/413 is surfaced as a typed `*BudgetError`.
- `TurnMetrics` (metrics.go): parses `X-Flexinfer-Upstream-Ms`,
  `-Cached-Tokens`, `-Prompt-Tokens`, `-Finish-Reason`. `CachedTokens` stays
  nil when the engine omits it (the gemma4 fallback the kill-test took), so a
  missing header is distinct from a reported zero.
- `Budget` (budget.go): encodes the usable-context bound row 194 surfaced —
  `Usable() = maxModelLen − system − output`. `Check(promptTokens)` returns a
  `*BudgetError` (the **F4-413-as-feature** affordance, in-process) so the loop
  stops cleanly before overflowing.
- `Registry` (registry.go): fixed, ordered tool set (order changes bust the
  prefix). Duplicate names error.
- `Engine.Run` (loop.go): appends the user turn, then per round sends the full
  append-only context + fixed tools; executes every requested tool call,
  appends each result as a tool message, continues; the first tool-call-free
  reply is the final answer. Stops on final / `MaxRounds` / budget. A
  `*BudgetError` is a clean `StopBudget`, not a failure.

`cmd/agent-loop/` — thin CLI:

- Flags: `--endpoint --model --session --system[-file] --prompt --workdir
  --max-model-len --system-tokens --max-tokens --max-rounds --temperature
  --report --self-check`.
- Real tools (`tools.go`): `read_file` (≤64KB), `list_dir`, both jailed to
  `--workdir` (absolute paths and `..` escapes rejected outright).
- `--self-check` (selfcheck.go): hermetic offline gate — a canned chat server
  (flexinfer headers + scripted tool-call→final dialogue), a real temp file
  the `read_file` tool actually reads, and assertions on: append-only
  prefix-extension (verified from the wire side), real tool execution, the
  final answer, header/metric parse, and the path-jail. Mirrors the
  `--self-check` mode of the F4 kill-test scripts.

## Riskiest assumption

The load-bearing external assumption — "vLLM keeps each append-only turn a
block-aligned prefix extension; prefix-hit ratio stays >90%" — was the
**previous slice's** kill-test and is **PASSED 2026-06-01** (matrix row 194:
TTFT-flatness ratio 1.42 ≤ 1.5 at 2.94× prompt growth; engine `/metrics`
prefix-cache hit rate 93.0%). This slice is the now-unblocked client; it adds
no new *external* assumption — its correctness is internal (append-only layout,
header parse, tool dispatch, budget math) and covered by unit tests + the
offline self-check. The live multi-round session against the canary is the
post-merge follow-up that confirms the real prefix-hit curve end-to-end.

## Acceptance criteria

- [x] `internal/agentloop/` engine: `Conversation` (append-only), `ChatClient`
  (`X-Flexinfer-Cache-Key`), `TurnMetrics` (header parse), `Budget`
  (413-as-feature), `Registry`, `Engine.Run` loop. Unit tests incl. the
  append-only prefix-extension property and budget stop.
- [x] `cmd/agent-loop/` CLI with real path-jailed `read_file`/`list_dir` tools.
- [x] `agent-loop --self-check` passes offline (canned server, real tool exec,
  append-only assertion, header parse, path-jail). Also a `go test` target.
- [x] `go build ./...`, `go vet`, `go test` (+`-race`) green locally; gofmt clean.
- [x] `make build-agent-loop` target.
- [x] Validation matrix row (pending — live session) in `60-validation-matrix.md`.
- [ ] CI green; MR merged.
- [ ] **Post-merge follow-up**: live multi-round session against the canary,
  asserting flat `upstream_ms` across tool rounds; capture report JSON.
- [ ] **Slice 2 queued**: loom-core MCP tool mirroring this engine shape.

## Operator runbook — live session (post-merge)

Pre-conditions: APC canary Ready at `maxModelLen 20480` (per row 194; same
`flux suspend` + `forcePromotion` activation — `maxModelLen` is at
`.spec.config`, NOT `.spec.runtime`). Proxy port-forward in another shell.

```bash
make build-agent-loop
bin/agent-loop \
  --endpoint http://localhost:18080 \
  --model gemma4-26b-a4b-gptq-apc-canary \
  --workdir ./internal/agentloop \
  --max-model-len 20480 --max-tokens 256 --max-rounds 12 \
  --prompt "List the files here, read loop.go, and summarise how the ReAct loop stops." \
  --report .loom/local/validation/agent-loop/$(date -u +%F)/run.json
```

Pass signal: per-round `upstream_ms` stays roughly flat while `prompt_tokens`
grows across tool rounds (same signal row 194 proved with the kill-test);
the loop reaches a final answer or a clean `StopBudget` before HTTP 400.

## Live verdict (2026-06-01) — PASS

Operator authorized a **full canary preemption** to close the live signal
(the only APC-enabled model is the preempted `gemma4-26b-a4b-gptq-apc-canary`;
both primaries run `enablePrefixCaching: false`). Procedure:

1. Suspended the `flexinfer-models` Flux kustomization (so transient patches
   wouldn't be reverted mid-test).
2. Patched the canary `spec.config.maxModelLen` 32768→20480 (row 193: 32k FP8
   KV is structurally infeasible on the 22GB cap) and `spec.gpu.forcePromotion:
   true` to win the `7900xtx-textgen` group over the priority-350 primary.
3. Canary cold-started cleanly at 20480 (0 restarts, weights + KV-cache profile
   + graph capture, ~11 min to Ready). The 5930k primary replica covered the
   shared aliases (copilot/gpt-4/project-mgmt) during the 7900xtx preemption.
4. Ran two `bin/agent-loop` sessions through `flexinfer-proxy` (port-forward
   18080→80), then reverted both patches, resumed Flux, and re-activated the
   primary — restored to **Ready + Active**, canary back to **Idle/Queued**,
   spec git-consistent (`maxModelLen=32768`, no `forcePromotion`).

**Run 1** (`run.json`, mixed outputs): a real 5-round ReAct loop — `list_dir`
then `read_file × 3` over the real package, ending in a coherent summary that
accurately described the append-only design. Proves the client end-to-end:
real tool execution, header/metric parse, budget (`usable=20206`), append-only
growing `prompt_tokens` on the wire, cache-key pin. But `upstream_ms` was
confounded — the final round hit `finish=length` (256-token essay ≈ 10.6s of
decode), which masks prefill since `upstream_ms` = prefill + decode.

**Run 2** (`prefill-probe.json`, prefill-isolation): terse system prompt +
`--max-tokens 24` so every round emits a tiny output (`upstream_ms` ≈ prefill).
Clean signal:

| round | prompt_tokens | Δ new | upstream_ms |
|------:|--------------:|------:|------------:|
| 0 | 247 | (247) | 526 |
| 1 | 946 | +699 | 649 |
| 2 | 1433 | +487 | 649 |
| 3 | 3370 | +1937 | **1571** |
| 4 | 5030 | +1660 | **1732** |
| 5 | 5565 | +535 | 701 |

`prompt_tokens` grew **22.5×** (247→5565) while `upstream_ms` tracked the
per-round token **delta**, not the cumulative prompt: small-delta rounds
(Δ≤700: 0,1,2,5) all sit in a 526–701ms band; only the two large-file rounds
(Δ≈1937/1660: 3,4) rise. With no prefix cache, prefill of the 5565-token prompt
would be ~11s (22× round 0); measured **701ms**. That decoupling of latency
from total prompt size — latency tracking the appended tail instead — is the
prefix cache absorbing the immutable prefix, observed **client-side**. It
matches row 194's engine-side TTFT-flat finding and extends it to 22.5× growth.

**Honest caveat**: gemma4 omits `cached_tokens` (`prefix_hit=n/a` every round),
so the cache hit is *inferred* from the delta-bound latency, not read off the
engine. That's a documented client limitation, not a failure. Follow-up: have
the proxy emit a prefill-only / TTFT header, or scrape the vLLM prefix-hit
gauge, so the client can report the hit ratio directly on engines that don't
surface `prompt_tokens_details`.

Verdict: **PASS** — client proven live end-to-end AND the prefix-cache benefit
observed client-side. Matrix row 195 `pending`→`pass`.

## Rollback / backout

- Pre-merge: drop the commits.
- Post-merge: `git revert` the MR — removes `internal/agentloop`,
  `cmd/agent-loop`, and the Makefile target. No runtime, controller, or proxy
  code is touched, so nothing in-cluster changes.

## Sources

- `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` — F4-tool-loop-as-prefix
  framing + the client-form open question this slice resolves
- `.loom/ralph-f4-tool-loop-as-prefix-2026-06-01.md` — the kill-test PASS that
  unblocked this slice
- `internal/proxy/usage_log.go` — response headers the client consumes
- `internal/routing/router.go` — `X-Flexinfer-Cache-Key` prefix-routing the
  client pins
- `cmd/spec-decode-bench/http_backend.go` — the OpenAI-compatible HTTP plumbing
  pattern this client mirrors
- `~/.claude/rules/spec-riskiest-assumption.md` — the rule whose gate (row 194)
  is now cleared

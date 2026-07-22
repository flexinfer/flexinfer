# Rapid Dev Iteration Log: Qwen3.5-9B default non-thinking RP profile

## Scope

- Iteration goal: make the existing Qwen3.5-9B base and `nsfw-rp` adapter
  return ordinary RP content without every client adding a Qwen-specific
  request kwarg, while retaining explicit thinking opt-in.
- Current blocker: Qwen3.5 defaults to thinking; requests that omit the kwarg
  emit `Thinking Process:` in content and consume their response budget.
- Hypothesis: vLLM 0.23 server defaults work identically for the base and a
  runtime-loaded LoRA, and request kwargs retain precedence.

## Artifact Pinning

- Branch: `codex/qwen35-9b-next-optimization`
- Runtime digest:
  `sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`.
- Model: repaired abliterated Qwen3.5-9B GPTQ W4/G128.
- Adapter: `mirazrafi/NSFW-RP-RolePlay-LoRA-Qwen-3.5-9B`, rank 64.
- Target: `cblevins-7900xtx`, gfx1100 ordinal zero.
- Unchanged certificate: graphs 1/2/4, FP16 KV, 131,072 context, native W4A16.

## Change

- Narrow patch point: add `defaultChatTemplateKwargs` to the vLLM backend,
  test map/string serialization and omission, then set only
  `enable_thinking: false` in a disposable candidate.
- Gauntlet delta: normal requests omit `chat_template_kwargs`; exact base and
  LoRA literal probes assert the server default; explicit true probes assert
  request precedence.
- Production remains unchanged until the experiment records a typed PASS.

## Probe

- Local: focused backend tests, embedded Python compilation, kustomize render,
  schema validation, unit suite, vet, and runtime patch-contract checks.
- Live: exact candidate image ID; default and override behavioral probes for
  base plus LoRA; existing RP quality/throughput, two-stream, and 128K recall
  gates; restart/fault scan; automatic parent restoration.
- Regression budget: at most 5% below the certified short-LoRA and concurrent
  throughput baselines, accounting for normal run variance.

## Result

### Controller support

- Backend passthrough merged as MR !927, merge commit `74fcca3b8`, after the
  full merge-request pipeline passed.
- The canary was intentionally withheld from that MR so Flux could not start
  it under the previous controller binary.

### Local canary proof

- Embedded gauntlet Python compiled successfully.
- Task and experiment kustomizations rendered successfully.
- Kubeconform validated all four retained CronJobs: four valid, zero invalid.
- `make test-unit`, `go vet ./...`, `make check-runtime-patch-contracts`,
  focused backend tests, and `git diff --check` passed.
- Status: waiting for the master controller image from merge `74fcca3b8`
  before the executable canary MR is allowed to merge.

## Next

1. Implement and validate the backend passthrough and canary gates.
2. Run the disposable candidate.
3. Promote the one config key only after PASS.

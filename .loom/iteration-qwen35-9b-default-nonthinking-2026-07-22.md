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
- The executable canary remained withheld until the master controller image
  from merge `74fcca3b8` was deployed Ready.

### Live run 1

- Controller `20260722-151147` deployed Ready at digest
  `sha256:af7c17d6afe1444f66f366b3aacdfafd2dea795c7c07194b929871f9655019bf`
  with one ready replica and zero restarts before the canary was released.
- Candidate startup passed on the exact runtime digest with vLLM 0.23,
  `RDNA3W4A16LinearKernel`, Triton/FLA GDN prefill, graphs 1/2/4, 131K,
  rank-64 LoRA support, and zero restarts. vLLM parsed
  `default_chat_template_kwargs={'enable_thinking': False}`.
- Base default probe returned exact `ROCM_9B_BASE_OK` with no reasoning;
  explicit true produced `Thinking Process:`. The dynamically loaded
  `nsfw-rp` adapter behaved identically for its default and override probes.
- Base RP decoded at 101.612 tok/s. LoRA dialogue median was 59.1161 tok/s;
  2,268-token multi-turn median was 40.5334 tok/s. All exceeded the 5%
  regression floors and emitted no thinking markers.
- Long context passed exact 5/5 recall from 127,969 prompt tokens with
  118.6716-second TTFT and 152.002-second elapsed time.
- Typed outcome: FAIL only because two-stream median aggregate was 52.7527
  tok/s against the strict 52.9000 floor, a 0.1473 tok/s miss. Preserve the
  threshold and bump only the experiment revision for a warm-cache rerun.

### Live run 2

- The warm-cache rerun changed only the experiment revision; all performance
  gates and the candidate profile remained unchanged.
- Base and LoRA requests with no request-level chat-template kwargs returned
  exact `ROCM_9B_BASE_OK` and `ROCM_9B_LORA_OK`, with zero reasoning content or
  thinking markers. Explicit `enable_thinking=true` produced
  `Thinking Process:` for both served models, proving request precedence.
- Base RP decoded at 102.6556 tok/s. LoRA dialogue median was 60.113 tok/s and
  the 2,268-token multi-turn median was 40.6942 tok/s. LoRA/base was 0.5856.
- Two-stream aggregate output was 56.7472, 56.0145, and 55.8076 tok/s; the
  56.0145 median passed the unchanged 52.9000 floor.
- Long context passed exact 5/5 recall from 127,969 prompt tokens with
  120.1712-second TTFT and 153.5372-second elapsed time.
- Typed outcome: `Succeeded/GauntletPassed`, generation 4. The candidate had
  zero restarts and the experiment restored the production parent.

### Promotion

- MR !930 promoted only `defaultChatTemplateKwargs.enable_thinking=false` and
  the generation-4 certificate annotation. Every execution, memory, context,
  LoRA, and performance parameter remained unchanged.
- Flux applied merge commit `1549c8af6`; the production Deployment reached
  one ready replica with zero restarts on the pinned runtime digest.
- The rendered container args contain
  `--default-chat-template-kwargs {"enable_thinking":false}`. Startup confirmed
  vLLM 0.23, native `RDNA3W4A16LinearKernel`, Triton/FLA GDN, graphs 1/2/4,
  and the 131,072-token window.
- Direct production requests without request kwargs returned exact
  `PROD_9B_BASE_OK` and `PROD_9B_LORA_OK`, both with null reasoning. Explicit
  true requests restored thinking behavior; the LoRA emitted the expected
  `Thinking Process:` marker and the base emitted internal-analysis prose.
- Final state: Model `Ready`, LoRA `Loaded` on 1/1 replica, and no startup or
  runtime faults in the promoted pod.

## Next

1. Observe normal RP traffic for any client-specific generation regressions.
2. Treat recall-ceiling bisection as the next isolated experiment; do not
   couple it to this now-certified interaction default.

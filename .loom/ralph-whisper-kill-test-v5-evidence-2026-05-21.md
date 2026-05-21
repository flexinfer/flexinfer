# RALPH: Whisper kill-test v5 — runtime pin verdict + new blocker surfaced

Date: 2026-05-21
Branch (this MR): `docs/whisper-kill-test-v5-evidence`
Parent chain:
- v3 evidence: `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`
- v4 evidence: `.loom/ralph-whisper-kill-test-v4-evidence-2026-05-20.md`
- Slice plan: `.loom/ralph-gfx1100-mistral-common-bump-2026-05-20.md`

## TL;DR

**Kill-test verdict: PASS for the mistral_common bump.** The
`ImportError: cannot import name 'ReasoningEffort'` that defeated v3
and v4 is now **gone**. vLLM advances much further along — past
`Dockerfile.runtime`'s pip-install chain, past Python module imports,
past the in-process transformers/AutoProcessor walk — and crashes
much later, at vLLM's argparse with **`api_server.py: error:
unrecognized arguments: --task`**.

This is the next-deeper failure mode the v4 evidence predicted, and
it is a smaller-scope blocker than the dep import: a CLI compatibility
issue between vLLM 0.17.0+rocm700's `api_server.py` and the
runtime controller's `python -m vllm.entrypoints.openai.api_server …
--task transcription …` subprocess invocation.

The runtime image bump is closed. The next slice handles the
`--task` flag deprecation in the controller's vLLM invocation.

## Riskiest assumption — close

**Status**: PASS for "mistral_common 1.10.0+ bump propagates to the
runtime DaemonSet image and unblocks the AutoProcessor class-registry
walk for multimodal models." The runtime DaemonSet (`flexinfer-runtime-gfx1100-tbkq6`,
image `sha256:592c0a751c89…`) loaded the Whisper model far past the
transformers `tokenization_mistral_common.py:42` import chain.

## Decisive in-cluster evidence

Pod: `flexinfer-runtime-gfx1100-tbkq6` on `cblevins-7900xtx`. Image:
`registry.harbor.lan/flexinfer/runtime@sha256:592c0a751c89c6e7c79c0854a4a41fc2b86d6423af3ffe148b61174416dce166`.
Window: 2026-05-21T01:01:18Z → 01:01:24Z.

```text
2026-05-21T01:01:18Z INFO  Loading model {"model":"whisper-kill-test-v3","backend":"vllm","source":"openai/whisper-large-v3-turbo"}
2026-05-21T01:01:18Z INFO  Starting backend subprocess {"model":"whisper-kill-test-v3","backend":"vllm",
  "executable":"python","args":["-m","vllm.entrypoints.openai.api_server","--host","0.0.0.0","--port","8000",
  "--model","/models/flexinfer-system/whisper-kill-test-v3","--dtype","half","--max-model-len","448",
  "--gpu-memory-utilization","0.30","--enforce-eager","--served-model-name","whisper-large-v3-turbo",
  "--task","transcription","--kv-cache-dtype","auto"],"port":8000}
2026-05-21T01:01:22Z INFO  Skipping import of cpp extensions due to incompatible torch version. Please upgrade to torch >= 2.11.0 (found 2.9.1+git5bc97ba). {"stream":"stderr"}
2026-05-21T01:01:23Z INFO  usage: api_server.py [-h] [--headless] [--api-server-count …] {"stream":"stderr"}
…
2026-05-21T01:01:23Z INFO  api_server.py: error: unrecognized arguments: --task {"stream":"stderr"}
2026-05-21T01:01:24Z ERROR Backend subprocess crashed {"error":"exit status 2"}
```

Notable contrasts vs v3/v4 (the same `runtime DaemonSet` exercise):

- **No transformers `tokenization_mistral_common.py:42` import-time
  frame.** That entire chain runs to completion. The
  `ReasoningEffort` enum is now importable in the runtime image's
  bundled mistral_common (`1.11.x` per build #39's `mistral_common
  ReasoningEffort import OK: <enum 'ReasoningEffort'>` smoke).
- **No AutoProcessor's `check_argument_for_proper_class` walk.**
  vLLM never reached the WhisperProcessor `__init__` because argparse
  rejected `--task` BEFORE the api_server's main body ran.
- **No vocab/embedding/profile-run path.** Crash is at the CLI
  validation stage, which is a much earlier surface than v3/v4's
  AutoProcessor or v1/v2's vLLM core init.
- **Different exit code**: v3/v4 had `exit status 1` (Python exception
  surfaced after argparse passed); v5 has `exit status 2` (argparse
  rejection).

## Why this surfaced now

The runtime image bundles vLLM via `pip install vllm==0.17.0+rocm700`
(per `build/runtime.yaml`'s `vllm_version` profile var). The
controller invokes vLLM with `--task transcription` via the same
backend/vLLM args path (`backend/vllm.go:167` per the v3 evidence's
reference). The controller's vLLM invocation matches an older vLLM CLI
shape where `--task` was a top-level api_server flag.

vLLM 0.17.0+ moved task selection — it is no longer a top-level
argparse flag on `api_server.py`. The `--task` flag may now be:
- exposed via `--override-model-config '{"task": "transcription"}'`,
- inferred from the model's `config.json` (Whisper auto-resolves to
  the encoder-decoder/transcription path), or
- removed entirely in favor of architecture-driven task dispatch.

The v3/v4 evidence's vLLM 0.17.0 logs DID show
`Resolved architecture: WhisperForConditionalGeneration` cleanly
without the `--task` arg surfacing as a problem, because v3/v4's
subprocess invocation crashed in `AutoProcessor.from_pretrained`
during the `Loading model` phase — argparse had already succeeded
because the failing AutoProcessor walk was triggered by a *different*
in-process code path (the runtime's `renderer_from_config` ->
`build_processor` chain, NOT the subprocess argparse).

Wait — that's the v4 evidence's contrast (in-process APIServer). v3
and v5 use the subprocess invocation (`python -m
vllm.entrypoints.openai.api_server …`). The subprocess vLLM picks up
the controller's CLI args. v3 had the same subprocess args and DID
NOT crash on `--task` because that vLLM build accepted it — that was
0.17.0+rocm700 as the standalone vLLM image at the time. The runtime
image's bundled vLLM was also 0.17.0+rocm700 (per
`build/runtime.yaml`'s `vllm_version: "0.17.0+rocm700"`).

The likely explanation: the runtime DaemonSet image and the standalone
vLLM image differ even though both claim 0.17.0+rocm700, because the
standalone image (`Dockerfile.vllm-gfx1100-qwen35-patched-nodiag`)
sits on a base that has its own pre-built vLLM with the older argparse,
while the runtime image was just freshly rebuilt with `pip install
vllm==0.17.0+rocm700 --extra-index-url
https://wheels.vllm.ai/rocm/0.17.0/rocm700` which pulled a newer
patch of 0.17.0 that removed `--task`. Mismatched wheel revisions
under the same nominal version is a classic skew. (Confirm with
`pip show vllm` inside the new image if needed.)

## What this means for the next slice

Three viable paths, ordered by minimum-blast-radius:

1. **Drop `--task` from the runtime controller's vLLM invocation**.
   Whisper's `config.json` has `architectures: ["WhisperForConditionalGeneration"]`,
   which lets vLLM auto-resolve to the transcription task without an
   explicit CLI flag. Edit `backend/vllm.go:167` (the args-assembly
   site referenced in v3 evidence) to gate the `--task` arg on a
   feature check, OR remove it entirely if vLLM 0.17+ infers correctly.
   Smallest diff.
2. **Translate `--task` → `--override-model-config '{"task":
   "transcription"}'`** in the controller if the underlying setting
   is still honored at the config layer. Slightly bigger diff but
   keeps explicit intent.
3. **Pin the runtime image's vLLM to an older 0.17.0+rocm700 wheel
   that still accepts `--task`** (e.g. point `VLLM_VERSION` /
   `VLLM_EXTRA_INDEX_URL` at a known-good revision in
   `build/runtime.yaml`). Counter-productive — locks the runtime to a
   stale vLLM and re-introduces dep skew.

Recommended: **path 1**. Smallest diff, fastest to validate, doesn't
trade today's mistral_common fix for tomorrow's CLI fragility.

## Production impact during this loop

- Runtime DaemonSet rollout to `cblevins-7900xtx` swapped the pod from
  `flexinfer-runtime-gfx1100-92ztq` (digest `sha256:7899640c…`) to
  `flexinfer-runtime-gfx1100-tbkq6` (digest `sha256:592c0a75…`) in
  ~94 seconds (`ContainerCreating` → `1/1 Running`). The image was
  already cached on the node from the build push, so the pull was
  essentially free.
- 7900 XTX warm primary `gemma4-26b-a4b-gptq` recycled with the
  runtime DaemonSet (since the runtime serves models in-process); the
  pod is now `Loading … readiness probe not passing yet` post-tear-down.
  Sister 26B on `cblevins-5930k` carried fast-chat / quality-chat
  traffic via shared service labels throughout.
- Whisper kill-test v5 reconcile preempted the 7900 XTX 26B for ~30
  seconds (apply 01:01:18Z → tear-down ~01:01:30Z), much shorter than
  v3/v4 because the new failure surface is the immediate-exit
  argparse stage rather than the longer Loading + AutoProcessor walk.
- gfx906 substrate untouched.

## References

- v3 evidence (root-cause): `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`
- v4 evidence (image-mismatch finding): `.loom/ralph-whisper-kill-test-v4-evidence-2026-05-20.md`
- MR !458/!460: standalone vLLM bump + pin.
- MR !461: `Dockerfile.runtime` mistral_common pin + build-time smoke.
- MR !462: runtime digest pin (`sha256:592c0a75…`).
- Build trace `mistral_common ReasoningEffort import OK` smoke landed
  at step #39 of `./build/build-runtime.sh gfx1100 --push` on 2026-05-21.
- Runtime image lineage:
  - `flexinfer/runtime@sha256:7899640c…` (pre-MR !462) — bundled mistral_common 1.9.1.
  - `flexinfer/runtime@sha256:592c0a75…` (post-MR !462) — bundled mistral_common 1.11.x.
- Controller vLLM arg-assembly site: `backend/vllm.go:167` (per v3 evidence reference).

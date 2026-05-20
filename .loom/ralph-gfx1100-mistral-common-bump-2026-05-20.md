# RALPH: gfx1100 vLLM image — bump mistral_common to expose ReasoningEffort

Date: 2026-05-20
Branch: `fix/gfx1100-mistral-common-reasoning-effort`
Parent evidence: `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`

## Goal

Unblock multimodal model loading (Whisper, future Qwen2.5-Omni etc.) on
the gfx1100 vLLM image by bumping `mistral_common` to a version that
exposes the `ReasoningEffort` enum that `transformers` 5.8.1's
`tokenization_mistral_common.py` imports.

## Scope

In:
- `build/Dockerfile.vllm-gfx1100-qwen35-patched-nodiag` — add a single
  `pip install --no-cache-dir --no-deps "mistral_common>=1.10.0,<2"`
  step plus an import smoke (`python3 -c "from
  mistral_common.protocol.instruct.request import ReasoningEffort"`)
  that fails the build if the bump did not land.

Out:
- The `Dockerfile.vllm-rocm-gfx1100` / `Dockerfile.vllm-rocm-gfx1100-fa`
  / `Dockerfile.vllm-nightly-rocm-gfx1100` siblings. None of them is
  the source of the currently-deployed gfx1100 image (`sha256:cb6d92c9…`,
  produced by `publish_vllm_rocm_gfx1100_qwen35_patched`). Bumping
  mistral_common in those would be a no-op for production today.
- A `transformers` downgrade. The base image carries 5.8.1 and the goal
  is to support newer multimodal call paths, not to revert.
- The cached-model layout fix described in the kill-test v3 evidence
  (path `/models/<namespace>/<model>/` vs `/models/<model>/`). That is a
  separate spec-doc concern, not a runtime dependency fix.

## Riskiest assumption + kill-test

**Load-bearing assumption**: `mistral_common 1.10.0+` exposes
`ReasoningEffort` from `mistral_common.protocol.instruct.request`, AND
no other `transformers` / vLLM import on the gfx1100 image regresses
on the 1.8.8 → 1.10.0+ bump (i.e. the other Mistral request types
`ChatCompletionRequest`, `InstructRequest`, etc. still resolve).

**Kill test**:
1. Dockerfile build runs `python3 -c "from
   mistral_common.protocol.instruct.request import ReasoningEffort"`
   as the last step in the layer. If the bump didn't land or the
   symbol moved, the build fails before publish.
2. Post-publish: trigger the kill-test v3 reconcile (re-annotate the
   Model CR with a new `flexinfer.ai/reconcile-at`), watch for the
   vLLM subprocess to either:
   - reach `Resolved architecture: WhisperForConditionalGeneration`
     AND advance past `WhisperProcessor.__init__` without the
     `ImportError: cannot import name 'ReasoningEffort'` traceback →
     **PASS**;
   - fail at a DIFFERENT import (any other broken `mistral_common` /
     `transformers` symbol) → **PASS for the bump, NEW blocker
     surfaced** — the next slice handles the new symbol;
   - fail at the same `ReasoningEffort` import → **FAIL**, the bump
     didn't propagate (cache poisoning, wrong base image, etc.).

**Failure mode if the assumption is wrong**:
- If `--no-deps` skips required transitive dependencies that the new
  1.10.0+ release expects, runtime errors might surface deeper in
  the loading chain (e.g. `ImportError: tiktoken` or similar). The
  build smoke imports just `ReasoningEffort`, which is a pure-Python
  enum and has no extra deps. Risk is low.
- If 1.10.0+ changes a `ChatCompletionRequest` or tokenizer v15
  signature in a way that breaks vLLM's own use of mistral_common,
  text-only models could regress. The Whisper kill-test will catch
  this if it goes far enough; if it shows up only on a different
  model path, a separate slice will revert/repin.

**Status**: not run.

## Acceptance criteria

1. New image build smoke passes (import line in Dockerfile produces
   `mistral_common ReasoningEffort import OK: …` instead of a
   traceback).
2. Publish job `publish_vllm_rocm_gfx1100_qwen35_patched` succeeds
   post-merge.
3. New image digest captured for the follow-up pin slice.
4. Production gfx1100 lane (`gemma4-26b-a4b-gptq`) stays Ready — the
   bump is layered on top of the existing patched base and must not
   regress text-only paths.

## Out-of-scope (will not happen in this slice)

- Pinning the new digest in `deploy/gpuprofiles/gfx1100.yaml` +
  `deploy/system/values-k3s.yaml`. That's the next slice
  (`pin/gfx1100-vllm-mistral-common-bump`).
- Re-running the Whisper kill-test v3 reconcile. That happens after
  the pin lands and Flux reconciles.
- The gfx906 strategic pivot to llama.cpp (separate slice).

## References

- Parent evidence: `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`
- Upstream mistral-common 1.10.0 release notes:
  https://github.com/mistralai/mistral-common/releases/tag/v1.10.0
  (2026-03-13, "Tokenizer v15, Reasoning Effort and Python 3.14").
- Publish job target: `publish_vllm_rocm_gfx1100_qwen35_patched`
  emits `${REGISTRY}/vllm:rocm-gfx1100-qwen35-patched-nodiag-textcfg`
  (== `registry.harbor.lan/flexinfer/vllm:rocm-gfx1100-qwen35-patched-nodiag-textcfg`).
- Production pin sites:
  - `deploy/gpuprofiles/gfx1100.yaml` (`image:` line)
  - `deploy/system/values-k3s.yaml` (`gfx1100Image`,
    `prewarm` blocks at lines ~223/229)

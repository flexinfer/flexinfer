# RALPH: Whisper kill-test v4 — re-run against the pinned vLLM image

Date: 2026-05-20
Branch: `fix/runtime-mistral-common-reasoning-effort`
Parent slice plan: `.loom/ralph-gfx1100-mistral-common-bump-2026-05-20.md`
Parent evidence (v3): `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`

## TL;DR

**Kill-test verdict: FAIL — same `ReasoningEffort` ImportError, but for an
architectural reason MRs !458/!460 did not address.**

The bump landed in `Dockerfile.vllm-gfx1100-qwen35-patched-nodiag` (which
produces the **standalone** `flexinfer/vllm` image used when a model with
`backend: vllm` is launched in its own pod, e.g. the gfx906
`qwen3-1p7b-vllm-radeonvii` canary). But the Whisper kill-test on
gfx1100 runs **in-process** in the runtime DaemonSet
(`flexinfer-runtime-gfx1100-tnntb`), which uses a **separate** image
(`registry.harbor.lan/flexinfer/runtime@sha256:310988969f3448…`) that
bundles its own vLLM + transformers + mistral_common via
`Dockerfile.runtime` lines 294/301 — with no version constraint on
`mistral_common`. The runtime image was last built when pip resolved
mistral_common to 1.9.1, which still pre-dates `ReasoningEffort`
(introduced in 1.10.0).

**Fix landed in this MR**: explicit `>=1.10.0,<2` constraint on both
mistral_common pip-install lines in `Dockerfile.runtime`, plus a
build-time `python3 -c "from mistral_common.protocol.instruct.request
import ReasoningEffort"` smoke that fails the build if the pin didn't
propagate.

**Out-of-scope for this MR**: the actual runtime image rebuild. The
runtime image is built manually via `./build/build-runtime.sh gfx1100
--push` (no CI publish job exists for it). That's the next operator-
triggered slice.

## Riskiest assumption (re-evaluated)

The original slice's load-bearing assumption was: "`mistral_common
1.10.0+` exposes `ReasoningEffort` AND the bump landed in the image
the Whisper kill-test exercises."

- **First half: CONFIRMED at build time** (MR !458 publish trace
  showed `mistral_common ReasoningEffort import OK: <enum
  'ReasoningEffort'>` in the new digest `sha256:a9b306af9122…`).
- **Second half: FAILED**. The Whisper kill-test exercises the
  *runtime DaemonSet image*, not the standalone vLLM image. Two
  distinct images, two distinct mistral_common installations.

## Decisive live evidence (in-cluster, 2026-05-20T23:47:05Z)

Pod: `flexinfer-runtime-gfx1100-tnntb` on `cblevins-7900xtx`.
Image: `registry.harbor.lan/flexinfer/runtime@sha256:310988969f3448ccb7b6001d36df0610c40a0354cacbd7e3410cf9d9592dd187`.

```text
File ".../vllm/multimodal/processing/processor.py", line 997 in __init__
    self.data_parser = self.info.get_data_parser()
File ".../vllm/model_executor/models/whisper.py", line 659 in get_data_parser
    feature_extractor = self.get_feature_extractor()
File ".../vllm/model_executor/models/whisper.py", line 675 in get_feature_extractor
    hf_processor = self.get_hf_processor(**kwargs)
…
File ".../transformers/models/whisper/processing_whisper.py", line 25 in __init__
    super().__init__(feature_extractor, tokenizer)
File ".../vllm/transformers_utils/processor.py", line 70 in __init__
    original_init(self, *args, **kwargs)
File ".../transformers/processing_utils.py", line 628 in __init__
    self.check_argument_for_proper_class(attribute_name, arg)
File ".../transformers/processing_utils.py", line 706 in check_argument_for_proper_class
    proper_class = tuple(self.get_possibly_dynamic_module(n) for n in class_name if n is not None)
File ".../transformers/processing_utils.py", line 1594 in get_possibly_dynamic_module
    if hasattr(transformers_module, module_name):
File ".../transformers/utils/import_utils.py", line 2226 in __getattr__
    module = self._get_module(self._class_to_module[name])
File ".../transformers/utils/import_utils.py", line 2458 in _get_module
    return importlib.import_module("." + module_name, self.__name__)
…
File "/opt/venv/lib/python3.12/site-packages/transformers/tokenization_mistral_common.py", line 42 in <module>
    from mistral_common.protocol.instruct.request import ChatCompletionRequest, ReasoningEffort
ImportError: cannot import name 'ReasoningEffort' from 'mistral_common.protocol.instruct.request' (/opt/venv/lib/python3.12/site-packages/mistral_common/protocol/instruct/request.py)
```

Notable differences from the v3 traceback:

- **Same crash signature** (`ImportError: cannot import name
  'ReasoningEffort'`). Confirms the bump did not propagate to this
  image.
- **Different process** — v3's stack was a subprocess vLLM
  AutoProcessor call; this v4 trace is inside the runtime DaemonSet's
  in-process vLLM `APIServer` (`(APIServer pid=933)`). The DaemonSet
  binds the vLLM API server directly when serving `backend: vllm`
  models that match its profile, bypassing the standalone-image path.
- **Image used**: `flexinfer/runtime@sha256:310988969f3448…`, NOT
  `flexinfer/vllm@sha256:a9b306af9122…`. Validates that the runtime
  image and the standalone vLLM image are two separate artifacts.

## Architectural distinction this exposed

| Surface | Image | Source Dockerfile | Used by |
|---------|-------|-------------------|---------|
| **Standalone vLLM backend** (separate pod per Model) | `flexinfer/vllm:rocm-gfx1100-qwen35-patched-nodiag-textcfg` | `Dockerfile.vllm-gfx1100-qwen35-patched-nodiag` | Models with `backend: vllm` that get their own Deployment (e.g. the gfx906 `qwen3-1p7b-vllm-radeonvii` canary). Bumped by MR !458/!460. |
| **Runtime DaemonSet in-process vLLM** | `flexinfer/runtime` | `Dockerfile.runtime` | The `flexinfer-runtime-gfx1100` DaemonSet on every gfx1100 node. Serves models via in-process vLLM `APIServer`. **NOT** bumped by MR !458/!460. |

The v3 evidence doc's recommendation said "bump
`build/Dockerfile.vllm-rocm-gfx1100*`" — that recommendation was
incomplete. The right surface for Whisper-via-runtime-DaemonSet is
`Dockerfile.runtime`. The right surface for any standalone vLLM model
is `Dockerfile.vllm-gfx1100-qwen35-patched-nodiag`. Both need the
bump; only the latter has it as of master `cd5cd042`.

## Fix landed in this MR

`build/Dockerfile.runtime` lines 291-315 now:

1. Pin `mistral_common>=1.10.0,<2` in both branches of the
   `VLLM_EXTRA_DEPS_PROFILE=minimal|else` switch (lines 294/301 in
   pre-MR layout).
2. Add a comment block explaining the constraint and pointing at this
   evidence doc.
3. Add a build-time `python3 -c "from mistral_common.protocol.instruct.request
   import ReasoningEffort"` smoke after both branches converge, so any
   regression on the pin fails the build before the runtime image is
   shipped.

## Next slice (operator action)

The runtime image is built manually, NOT in CI. The next slice is a
single operator command:

```bash
make push-runtime-gfx1100
# expands to ./build/build-runtime.sh gfx1100 --push
```

Expected outcome:
- Build smoke `python3 -c "from mistral_common.protocol.instruct.request
  import ReasoningEffort"` prints `mistral_common ReasoningEffort
  import OK: <enum 'ReasoningEffort'>` before any cached steps run.
- New digest `sha256:<new>` published to
  `registry.harbor.lan/flexinfer/runtime`.
- Follow-up pin slice updates `deploy/system/values-k3s.yaml:234`
  (`runtimeImage`) + `deploy/system/values-k3s.yaml:353/386/415` (the
  task-runtime image entries that all currently point at the runtime
  image's previous digest).
- Re-run the Whisper kill-test v4 reconcile against the new digest.

## Production impact during this loop

- 7900 XTX warm primary `gemma4-26b-a4b-gptq` was preempted for ~3
  minutes (kill-test apply at 23:47:05Z → tear-down at ~23:50Z). Sister
  26B on `cblevins-5930k` carried fast-chat / quality-chat traffic
  during preemption (shared service labels). After tear-down, the
  7900 XTX primary scheduler re-elected the 26B as Ready leader
  immediately.
- Radeon VII (gfx906) and gfx906 substrate untouched.

## References

- Parent slice plan: `.loom/ralph-gfx1100-mistral-common-bump-2026-05-20.md`
- v3 evidence (root-cause): `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`
- MR !458 (standalone vLLM Dockerfile bump): merged at `947fd7ee`.
- MR !460 (standalone vLLM digest pin): merged at `cd5cd042`.
- Image lineage:
  - `flexinfer/vllm@sha256:a9b306af9122…` — standalone vLLM, **has the
    bump** (verified by publish job 111789 trace).
  - `flexinfer/runtime@sha256:310988969f3448…` — runtime DaemonSet,
    **does NOT have the bump** (still mistral_common 1.9.1 inside).
- Build path:
  - Standalone vLLM: `publish_vllm_rocm_gfx1100_qwen35_patched` in
    `.gitlab-ci.yml` (line 1419).
  - Runtime: `./build/build-runtime.sh gfx1100 --push` (no CI publish
    job; manual operator action).
- Whisper crash file:line (vLLM in-process under runtime DaemonSet):
  - `vllm/model_executor/models/whisper.py:659` →
    `vllm/model_executor/models/whisper.py:675` →
    `vllm/transformers_utils/processor.py:70` →
    `transformers/processing_utils.py:628` (the
    `check_argument_for_proper_class` walk) →
    `transformers/processing_utils.py:1594`
    (`get_possibly_dynamic_module`) →
    `transformers/tokenization_mistral_common.py:42` (the broken
    import line).

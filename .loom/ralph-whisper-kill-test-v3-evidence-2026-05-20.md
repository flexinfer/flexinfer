# RALPH iteration — Whisper kill-test v3 live evidence

Date: 2026-05-20
Parent slice: `.loom/ralph-whisper-kill-test-v3-2026-05-19.md` (MR !443, commits `3af07488` / `dbd3cc5a`, merged into master via `bb09dcae`)
Parent plan: `.loom/asr-diarization-7900xtx-plan-2026-05-18.md`

## TL;DR

- **Kill-test load-bearing assumption: CONFIRMED.** `cache.strategy: Local` + `cache.storageClass: nvme-1r-gpu` correctly stages files at `/models/flexinfer-system/whisper-kill-test-v3/` on `cblevins-7900xtx`, and the runtime DaemonSet sees them. All 1.6 GB of `model.safetensors` + tokenizer/preprocessor configs are present.
- **vLLM crash root cause: NOT Whisper-on-ROCm-gfx1100.** It is a Python dependency mismatch in the gfx1100 vLLM image: `transformers/tokenization_mistral_common.py` imports `ReasoningEffort` from `mistral_common.protocol.instruct.request`, but the installed `mistral_common 1.8.8` does not export it.
- **Next slice (queued):** bump `mistral_common` (or pin `transformers`) in `build/Dockerfile.vllm-rocm-gfx1100*` so AutoProcessor's class registry walk does not blow up. Re-run the kill-test after the new gfx1100 vLLM image is pinned.

## Cache layout evidence (PASS)

`status.cache.ready: true`, `status.cache.message: "local cache previously staged"`.

Listing on the runtime pod's `/models` mount (hostPath `/var/lib/flexinfer/models`):

```text
$ kubectl exec -n flexinfer-system flexinfer-runtime-gfx1100-btwbw -- \
    ls -la /models/flexinfer-system/whisper-kill-test-v3/
total 1584484
drwxr-xr-x 3 root root       4096 May 20 03:00 .
drwxr-xr-x 4 root root       4096 May 20 03:00 ..
drwxr-xr-x 3 root root       4096 May 20 03:00 .cache
-rw-r--r-- 1 root root          0 May 20 03:00 .flexinfer_cached_390ea170...
-rw-r--r-- 1 root root       1519 May 20 03:00 .gitattributes
-rw-r--r-- 1 root root      21196 May 20 03:00 README.md
-rw-r--r-- 1 root root      34648 May 20 03:00 added_tokens.json
-rw-r--r-- 1 root root       1256 May 20 03:00 config.json
-rw-r--r-- 1 root root       3772 May 20 03:00 generation_config.json
-rw-r--r-- 1 root root     493869 May 20 03:00 merges.txt
-rw-r--r-- 1 root root 1617824864 May 20 03:00 model.safetensors
-rw-r--r-- 1 root root      52666 May 20 03:00 normalizer.json
-rw-r--r-- 1 root root        340 May 20 03:00 preprocessor_config.json
-rw-r--r-- 1 root root       2186 May 20 03:00 special_tokens_map.json
-rw-r--r-- 1 root root    2710337 May 20 03:00 tokenizer.json
-rw-r--r-- 1 root root     282843 May 20 03:00 tokenizer_config.json
-rw-r--r-- 1 root root    1036558 May 20 03:00 vocab.json
```

Path nuance vs the slice doc: files land at `/models/<namespace>/<model>/`, not `/models/<model>/`. The slice doc described the expected path as `/models/whisper-kill-test-v3/`; the actual layout is namespace-prefixed. This still satisfies the assumption (runtime DaemonSet sees the files), but should be folded back into the slice doc's expectation for future kill-tests.

## Crash trace (gfx1100 vLLM image)

Image: `flexinfer-runtime-gfx1100` DaemonSet, vLLM 0.17.0+rocm700 inside `/opt/venv/lib/python3.12/site-packages/vllm/`. Triggered by reconcile after annotating `flexinfer.ai/reconcile-at=20260520T053015Z` on the Model CR (controller hot-loops the reconcile so the same trace repeats every ~3 s).

Architecture resolution and arg parsing succeed:

```text
INFO 05-20 05:30:19 [model.py:531] Resolved architecture: WhisperForConditionalGeneration
INFO 05-20 05:30:19 [model.py:1554] Using max model len 448
INFO 05-20 05:30:19 [model.py:566] Encoder-decoder model detected, disabling mm processor cache.
INFO 05-20 05:30:19 [scheduler.py:222] Encoder-decoder models do not support chunked prefill nor prefix caching; disabling both.
INFO 05-20 05:30:19 [vllm.py:747] Asynchronous scheduling is enabled.
WARNING 05-20 05:30:19 [vllm.py:781] Enforce eager set, disabling torch.compile and CUDAGraphs.
ERROR 05-20 05:30:20 [config.py:29] Failed to import Triton kernels. ... Error: No module named 'triton.language.target_info'
ERROR 05-20 05:30:20 [gpt_oss_triton_kernels_moe.py:61] Failed to import Triton kernels. ... Error: No module named 'triton.language.target_info'
```

Then it fails at AutoProcessor instantiation:

```text
File "/opt/venv/lib/python3.12/site-packages/vllm/model_executor/models/whisper.py", line 659, in get_data_parser
  feature_extractor = self.get_feature_extractor()
File "/opt/venv/lib/python3.12/site-packages/vllm/model_executor/models/whisper.py", line 675, in get_feature_extractor
  hf_processor = self.get_hf_processor(**kwargs)
File "/opt/venv/lib/python3.12/site-packages/vllm/transformers_utils/processor.py", line 156, in get_processor
  processor = AutoProcessor.from_pretrained(...)
File "/opt/venv/lib/python3.12/site-packages/transformers/models/whisper/processing_whisper.py", line 25, in __init__
  super().__init__(feature_extractor, tokenizer)
File "/opt/venv/lib/python3.12/site-packages/transformers/processing_utils.py", line 628, in __init__
  self.check_argument_for_proper_class(attribute_name, arg)
File "/opt/venv/lib/python3.12/site-packages/transformers/processing_utils.py", line 706, in check_argument_for_proper_class
  proper_class = tuple(self.get_possibly_dynamic_module(n) for n in class_name if n is not None)
File "/opt/venv/lib/python3.12/site-packages/transformers/processing_utils.py", line 1594, in get_possibly_dynamic_module
  if hasattr(transformers_module, module_name):
File "/opt/venv/lib/python3.12/site-packages/transformers/utils/import_utils.py", line 2226, in __getattr__
  module = self._get_module(self._class_to_module[name])
File "/opt/venv/lib/python3.12/site-packages/transformers/utils/import_utils.py", line 2460, in _get_module
  raise e
File "/opt/venv/lib/python3.12/site-packages/transformers/utils/import_utils.py", line 2458, in _get_module
  return importlib.import_module("." + module_name, self.__name__)
File "/opt/venv/lib/python3.12/site-packages/transformers/tokenization_mistral_common.py", line 42, in <module>
  from mistral_common.protocol.instruct.request import ChatCompletionRequest, ReasoningEffort
ImportError: cannot import name 'ReasoningEffort' from 'mistral_common.protocol.instruct.request'
  (/opt/venv/lib/python3.12/site-packages/mistral_common/protocol/instruct/request.py)
```

The subprocess exits 1; the controller raises `RuntimeFailed { exit status 1 }` and re-enters the reconcile every ~3 s.

## Installed versions on the failing image

```text
transformers 5.8.1
mistral_common 1.8.8   # does not export ReasoningEffort
```

`mistral_common.protocol.instruct.request` in 1.8.8 has no `ReasoningEffort` symbol (`dir()` filter returned `[]`).

## Why this surfaces on Whisper but not on OPT / Qwen / gemma4

`transformers.check_argument_for_proper_class` walks the dynamic module registry to verify the `feature_extractor` and `tokenizer` arguments are instances of the expected classes. The walk eagerly imports modules whose names match potential class targets, including `tokenization_mistral_common`. The import happens inside `WhisperProcessor.__init__`'s call to `super().__init__(feature_extractor, tokenizer)`.

Text-only models loaded with `AutoTokenizer.from_pretrained` (e.g. gemma4-26b, qwen3, opt) do not hit `AutoProcessor.from_pretrained` and therefore do not exercise the multimodal processor's class-registry walk. Whisper is the first model on this image to traverse that path, which is why the latent dependency mismatch only surfaces here.

## Recommendation

Treat this as a `transformers` ↔ `mistral_common` upstream version skew in `build/Dockerfile.vllm-rocm-gfx1100*` and bump `mistral_common` to a release that exposes `ReasoningEffort` (introduced when the `reasoning_effort` request field was added to Mistral chat completions). A single-line `pip install --no-deps "mistral_common>=<NEW>"` in the runtime image's Dockerfile is the minimum-blast-radius fix; verify by re-pulling vLLM module imports and re-running the kill-test reconcile.

After the bump, re-trigger this kill-test by removing it from the tear-down (if torn down) and re-applying via Flux, OR by re-annotating the existing Model CR with a new `flexinfer.ai/reconcile-at` value if not torn down.

## Riskiest assumption + kill-test (this slice's slot)

**Load-bearing assumption (from parent slice doc)**: With `cache.strategy: Local` + `cache.storageClass: nvme-1r-gpu`, the runtime DaemonSet sees the staged files at the expected path.

**Kill-test result**: PASS.
- `status.cache.ready: true` with `Cached: True` / `reason: CacheStage`.
- `ls /models/flexinfer-system/whisper-kill-test-v3/` returns `config.json` + `model.safetensors` + tokenizer/preprocessor configs.
- vLLM's resolved architecture line reads `Resolved architecture: WhisperForConditionalGeneration` — proving the model files are on-disk and discoverable from vLLM's POV.

**Failure mode if the assumption had been wrong**: vLLM would have crashed at file resolution (e.g. `HFValidationError` from v2) BEFORE arch resolution. It did not; it crashed strictly inside the AutoProcessor's transitive import chain.

**New blocker surfaced**: dependency skew in the gfx1100 vLLM image. Scoped at `build/Dockerfile.vllm-rocm-gfx1100*` (Dockerfile pip pins for `transformers` + `mistral_common`), not at the ASR/diarization plan. Will be queued as a separate slice.

**Status**: PASS for assumption, FAIL for end-to-end vLLM Whisper readiness.

## Production impact

While the kill-test is force-promoted, `gemma4-26b-a4b-gptq` on `cblevins-7900xtx` sits at `0/0` (Preempted), and the sister `gemma4-26b-a4b-gptq-5930k` on `cblevins-5930k` carries ICC's quality-chat / mid-chat / project-mgmt traffic via shared serviceLabels. The controller hot-loops the reconcile (no backoff) while the kill-test remains in the manifest, so removing the kill-test promptly returns the 7900 XTX warm primary to Ready.

## Handoff

Next slices (queued):

1. **Tear down `whisper-kill-test-v3`** — remove the CR from `deploy/models/kustomization.yaml` so Flux drops it. Restore the 7900 XTX warm primary. Mirrors the precedent in `chore/tear-down-whisper-kill-test-v2` (worktree already exists).
2. **Bump `mistral_common`** in `build/Dockerfile.vllm-rocm-gfx1100*` — release the gfx1100 vLLM image with a `mistral_common` version that exposes `ReasoningEffort`. Verify with `python3 -c "from mistral_common.protocol.instruct.request import ReasoningEffort; print('ok')"` inside the new image.
3. **Re-run the kill-test** — re-apply `deploy/models/whisper-kill-test-v3.yaml` (or write a v4) against the bumped image, expect Phase=Ready and a successful `kubectl port-forward` + `curl /v1/audio/transcriptions` smoke.

Slot order: 1 before 2 (returns 7900 XTX warm primary to Ready and stops the controller hot-loop while the fix is built). 2 before 3 (need the new image pinned). 3 closes the parent ASR/diarization plan's Slice 1.

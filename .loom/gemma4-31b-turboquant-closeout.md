# Gemma4 31B TurboQuant Closeout

Date: 2026-04-25

## Outcome

MR !192 is merged. The 31B TurboQuant long-context lane is closed for a
single 24 GiB gfx1100 card, and the production 31B profile remains capped at
`maxModelLen: 2048`.

## Facts

- MR !192 merged commit `b64f0502`, title
  `docs(31b): close out TurboQuant canary, record 24 GiB OOM analysis`.
- The OOM happened during model weight construction before KV cache allocation:
  the failing pod had 23.59 GiB allocated on a 23.98 GiB card, with only
  26 MiB free.
- Memory accounting in the merged analysis records 20.02 GiB for 31B INT4
  weights plus about 3.57 GiB of TurboQuant plugin rotation matrices and tensor
  state, leaving roughly 0.4 GiB before activations, graph payload, or KV.
- `gpuMemoryUtilization` is not a fix because the plugin allocates through raw
  `torch.empty()` during `Gemma4DecoderLayer.__init__`, bypassing vLLM's memory
  manager.
- CPU offload is not a current lever: vLLM V1 removed CPU offload, and tests
  assert `--cpu-offload-gb` is absent on V1 paths.
- `deploy/models/kustomization.yaml` now leaves
  `gemma4-31b-gptq-long.yaml` commented out; the file stays on disk for
  historical reference.
- The primary CR is stamped
  `flexinfer.ai/promotion-state: validated-2048-gfx1100-ceiling` and
  `flexinfer.ai/promotion-requires:
  docs/dev/gemma4-31b-turboquant-24gb-oom.md`.
- The superseded canary CR is stamped
  `flexinfer.ai/promotion-state:
  turboquant-canary-superseded-gfx1100-24gb-insufficient-vram` and
  `flexinfer.ai/promotion-evidence:
  docs/dev/gemma4-31b-turboquant-24gb-oom.md`.

## Decision

Do not re-enable `gemma4-31b-gptq-long` or push the 31B primary past 2048
tokens on 24 GiB gfx1100 until one of these levers exists:

1. 31B weights below about 15 GiB with acceptable quality.
2. A TurboQuant plugin change that defers rotation-matrix allocation until
   after weight load or otherwise avoids the 3.5 GiB resident overhead.
3. A different RDNA3-compatible KV compression path that does not carry this
   plugin overhead.
4. Different GPU hardware with materially more VRAM.

## Next Lane

For long-context Gemma4 work, pivot to `gemma4-26b-a4b-gptq-long`. It has
smaller weights, does not use TurboQuant, and is not blocked by the same plugin
allocation failure. Keep that as a separate MR and validation thread.

## Sources

- `glab mr view 192` -> `state: merged`, URL
  `https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/192`.
- `git show --stat --oneline --decorate gitlab/claude/31b-turboquant-close-out -n 1`
  -> `b64f0502 docs(31b): close out TurboQuant canary, record 24 GiB OOM analysis`.
- `git show b64f0502:docs/dev/gemma4-31b-turboquant-24gb-oom.md | nl -ba`:
  lines 13-34 (failure signature), lines 40-55 (memory accounting), lines
  57-76 (`gpuMemoryUtilization` cannot fix it), lines 78-90 (CPU offload),
  lines 120-137 (lane-close decision).
- `git show b64f0502:deploy/models/gemma4-31b-gptq.yaml | nl -ba`:
  lines 45-53 (primary promotion-state and requires doc), lines 125-141
  (`maxModelLen: 2048` ceiling rationale).
- `git show b64f0502:deploy/models/gemma4-31b-gptq-long.yaml | nl -ba`:
  lines 65-74 (canary superseded annotations), lines 115-124 (failed
  TurboQuant 16K configuration).
- `git show b64f0502:deploy/models/kustomization.yaml | nl -ba`:
  lines 14-21 (removed from Flux reconciliation with doc pointer).

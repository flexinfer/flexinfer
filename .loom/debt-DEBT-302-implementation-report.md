# Tech Debt Implementation Report

## Item

- Debt ID: DEBT-302
- Branch/PR: `debt/DEBT-302`
- Owner: Codex

## Problem

- Original pain point: Qwen3.6/Qwen3.5 GDN GPTQ policy lived in one-off manifest comments and incident notes instead of a reusable validation gate.
- Affected components: artifact validator, ModelCache publish validation, quantization CRD schema, qwen36 ModelCache manifest, validation matrix.

## Changes

- Summary of refactor/remediation: added a `qwen36-27b` validator family profile and warning-first GDN GPTQ policy that records any `linear_attn.*` qweight tensors under `checks.gdn_gptq_policy`.
- Notable design choices: kept the first pass warning-only through existing `failOnWarnings=false`, so the qwen36 recovery cycle can publish while still surfacing policy evidence. `failOnWarnings=true` can promote the same check to a blocking gate after the clean dynamic-exclusion artifact is proven. The v1alpha2 quantization CRD now also admits `dynamicExclusion: gdn`, matching the ModelCache API and controller conversion path.

## Verification

- Local checks:
  - `python3 -m unittest test_validate_quantized_artifact.py`
  - `go test ./pkg/quantization -run 'TestBuildValidateArtifactJob|TestValidatorWrapperScript' -count=1`
  - `go test ./pkg/quantization/... ./api/v1alpha2/...`
  - `bash /Users/cblevins/.codex/skills/tech-debt-backlog-dev-loop/scripts/verify_local_loop.sh`
- CI pipeline/run: pending branch push.
- Extra validation: `.loom/60-validation-matrix.md` qwen36 row now names the reusable GDN policy gate.

## Outcome

- Risk reduced: future qwen36/qwen3.5 GPTQ artifacts no longer rely on humans grepping shard indexes for `linear_attn.*.qweight`.
- Delivery drag reduced: the ModelCache publish gate captures policy evidence in status/termination JSON before OCI publish.
- Residual debt / follow-ups: flip qwen36 `failOnWarnings` to `true` once the first `dynamicExclusion=gdn` artifact passes and live smoke is coherent.

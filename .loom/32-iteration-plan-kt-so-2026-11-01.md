# KT-SO — structured outputs with MTP on the workhorse posture (canary plan)

Feature D4, `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.4 and brief
D4. Matrix row `gfx1100-structured-outputs-2026-11-01` in `60-validation-matrix.md` (pass rule
written before the run). Canary `deploy/debug/qwen38-27b-so-canary.yaml`; gate
`flexinfer-bench tool_call_conformance` (`docs/user/gauntlet.md`). Code: !1226 (capability and
certificate gate), !1229 (proxy pass-through and tag), D4 MR 3 (gate). **Not yet run.**

## What is known before the run

- vLLM 0.32 serves `response_format` and `structured_outputs` on every lane with backend `auto`
  by default (docs, `features/structured_outputs.md`). The controller's
  `--structured-outputs-config {"backend":"auto"}` therefore pins today's behaviour rather than
  enabling it; the certificate is the evidence that this image, card and speculative config keep
  constrained turns correct and fast. The canary can be measured before any certificate exists.
- Riskiest assumption: the bitmask is filled on the serial CPU path whenever speculative tokens
  > 0 (vLLM PR #14702 composes structured outputs with spec decode; the parallel fill runs only at
  0), so MTP K=2 may lose its 1.6–1.7x gain on constrained turns.
- Secondary risk: the lane runs the `qwen3` reasoning parser with thinking off. vLLM applies the
  grammar after reasoning ends; if the empty think block is not recognised the constraint is not
  applied and the gate shows parse failures. The fix (`enable_in_reasoning`) is another envelope.

## Canary choice

A demand-only twin of the workhorse on the 5930k, at priority 510, as S3/S3b and KT-QOS ran. It
cannot run beside the workhorse: `vramEstimateMB: 22000` is the whole 23 GiB card, so the twin
takes the card for the window and the workhorse reclaims it on demand afterwards. The XTX is out
until the soak verdict (2026-11-06 00:15Z) and that window belongs to !1203 and KT-FLEX; the XTX
warm lane runs the same image, so a 5930k certificate covers it if it opts in later (the `gfx1100`
GPUProfile spans both nodes). The canary reuses the S3 Local cache (`hfRevision 2d7425f7`) and the
S3 compile cache (warm segmented + MTP K=2 at 32K), so nothing is pulled or downloaded and the
first boot is warm (~3 min). A grammar-mask Triton kernel compiles on first use; the gate's
warm-up turns keep it out of the measured windows.

## Preconditions (agent checks, no apply)

1. Controller image contains !1226 (master `87ec8205` or later) for the `StructuredOutput`
   condition, and the Model CRD has the field (`kubectl explain model.spec.capabilities.structuredOutput`;
   the HelmRelease applies chart CRDs with `crds: CreateReplace`). Without them, delete the
   `capabilities` block from the canary: the measurement does not depend on it.
2. Owner present, quiet hour; the workhorse can be preempted for ~30 min.
3. A workstation with this repo at master (for `go run`) and `kubectl` on the k3s context.

## Owner paste sequence

```bash
kubectl apply -f deploy/debug/qwen38-27b-so-canary.yaml
kubectl -n flexinfer-system wait model/qwen38-27b-so-canary --for=jsonpath='{.status.phase}'=Ready --timeout=25m
kubectl -n flexinfer-system get model qwen38-27b-so-canary -o jsonpath='{.status.conditions[?(@.type=="StructuredOutput")]}'
```

Expect `StructuredOutput=False StructuredOutputNotCertified` (no certificate yet). Then the gate,
from the workstation (agent or owner):

```bash
kubectl -n flexinfer-system port-forward svc/qwen38-27b-so-canary 18000:8000
go run ./cmd/flexinfer-bench tool_call_conformance --url http://localhost:18000 \
  --model qwen38-27b-so-canary --parity-baseline parity_code=8174a49e \
  --row gfx1100-structured-outputs-2026-11-01 --context-length 32768 \
  --image registry.harbor.lan/flexinfer/runtime@sha256:b06b06fe1c0265ce207f3fc719c181e8edd18eae50bdb8490c122800b17abfe2 \
  --source HF://biMEMO/Qwen3.8-27B-int4-AutoRound@2d7425f7 --out kt-so-A.json
```

~15 min: 2 × 200 turns of ~50 output tokens, parity twice. Operator gates during the run: no
HIP/HSA/OOM markers in the canary log, no restarts, boot line `GPU KV cache size` ≈ 52.9K tokens.

## Reading the result

- **PASS** (exit 0): paste the printed row over the pending one and run `make certify-matrix`
  (commit `docs/certification-matrix.json`; CI's `certify_matrix` job checks it), then open the
  certificate MR, which needs the same regeneration:
  `flexinfer.ai/backend-certificate-vllm-structured-outputs` (the image above), `-since` (the run's
  finish time) and `-evidence` (`.loom/60-validation-matrix.md#gfx1100-structured-outputs-2026-11-01`)
  in `deploy/gpuprofiles/gfx1100.yaml`. Optional, same window: once Flux applies it the canary
  re-renders with the flag (condition `Certified`, ~3 min relaunch); rerun the gate with
  `--turns 40` and confirm parse and parity are unchanged with the flag on the pod.
- Any verdict: the row edit needs `make certify-matrix` too.
- **FAIL on parse**: read `constrained.failures` and the taxonomy. Failures in one mode only point
  at that request shape; `invalid_json` with prose on the JSON modes points at the reasoning
  parser (secondary risk).
- **FAIL on decode or pos-0 only**: the serial bitmask path costs the MTP gain. Record the measured
  ratio; no certificate. The owner decides between an uncertified lane (callers keep their own
  retries) and waiting for upstream to fill the bitmask in parallel with spec tokens.

## Rollback (close the window)

```bash
kubectl -n flexinfer-system delete model qwen38-27b-so-canary
```

Then activate the workhorse (`flexinfer_activate_model qwen38-27b-autoround-workhorse`). Nothing in
production changed: no certificate, no workhorse edit.

## After certification

- A lane opts in with `spec.capabilities.structuredOutput: true` plus `dedicatedDeployment: true`
  (the workhorse already has the latter); that relaunch is window-gated.
- Mills (`loom-core pkg/mills/clients/flexinfer.go`) sends `response_format: json_object` today and
  retries reasoning-budget failures with 4x tokens. On a lane whose descriptor says
  `structured_output: true` it can send `json_schema` for schema turns and drop that retry for them
  (loom-core change, separate MR).

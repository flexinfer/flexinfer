# RALPH Iteration Plan: Label-Group Least-Loaded Routing

## Review

- Roadmap milestone: dual-node 128K Qwen3.5 workhorse serving efficiency
- Spec section(s): `docs/user/routing.md`; F4 label-group routing
- Prior decisions to preserve: select the Model before rewriting the served
  model name; never aggregate endpoints across distinct Model Services; only
  Ready Models may receive warm traffic.

## Align

- Slice name: shared-label least-loaded routing
- Scope in: a validated `least-loaded` mode, Ready-only selection, round-robin
  tie-breaking, chart wiring, K3s enablement, debug visibility, route-decision
  and target-hit metrics, unit/Helm/live verification.
- Scope out: token-level scheduler telemetry, cross-proxy distributed load,
  prefix-cache affinity combined with load, more than one proxy replica.
- Acceptance criteria: a busy member is avoided; equal-load members remain
  fair; non-Ready members are excluded; the live `workhorse-128k` label uses
  the mode and exposes its selected targets.
- Dependencies/blockers: proxy image pipeline and Flux reconciliation.
- Riskiest assumption: per-Model proxy active-connection counts cover the full
  upstream request lifetime and become visible soon enough to steer a later
  shared-label request away from a maxNumSeqs=1 replica.
- Kill-test: hold a long direct request against one 128K Model, wait until
  `flexinfer_proxy_active_connections{model}` is 1, then send at least four
  short requests through `workhorse-128k`; every request must select the idle
  peer, and the least-loaded decision/target counters must increase.

## Land

- Planned file areas: proxy resolver/config/metrics/tests, Helm values/template,
  K3s values, routing/metrics docs.
- Implementation steps:
  1. Add the mode and fair minimum-load selection.
  2. Expose the mode and selected targets operationally.
  3. Enable it on K3s and ship through CI/Flux.

## Prove

- Tests to run: targeted proxy tests, `go test ./internal/proxy/...`, Helm lint
  and rendered-environment assertion, full repository test suite.
- Lint/static checks: gofmt, go vet/build through repository quality gates.
- CI checks: GitLab pipeline green before merge; Flux Ready after merge.

### Live verdict: PASS

- Shipped through FlexInfer MRs !830 (implementation), !831 (safe config
  rollback while the image baked), and !832 (image-gated enablement).
- Final runtime: proxy image `20260715-001254`,
  `labelGroupRouting=least-loaded`, Ready with zero restarts; both Qwen3.5 128K
  Models remained Ready.
- Stable-window kill-test: held a direct request on
  `qwen35-35b-clean-gptq-workhorse` until its active-connection gauge was 1.
  All 4/4 `workhorse-128k` probes selected
  `qwen35-35b-clean-gptq-workhorse-128k` in 0.25–0.46 seconds. The held request
  completed successfully in 25.7 seconds.
- Metrics at the decision point: primary active=1, sister active=0,
  `least_loaded` decisions=4, sister target hits=4.
- Full repository `make test`, targeted proxy tests, Helm lint, and rendered
  environment assertion passed before merge.

### Harvested rollout defect

An earlier, longer hold was interrupted when image automation rolled the
single proxy replica. The Model stayed Ready and did not restart; the new proxy
pod was healthy, proving this is missing in-flight HTTP drain behavior rather
than a model or least-loaded failure. Tracked as P1
[#65](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/65).

The same rollout exposed a revoked shared GitLab credential. Platform GitOps
MRs !399 and !400 rotated the SOPS-managed source credential, removed an
accidental trailing newline, restored `GitRepository/gitops-gitlab`, and proved
`ImageUpdateAutomation/flexinfer` could push the final immutable image tag.

## Handoff/Harvest

- Docs to update: this plan with the live verdict; `ROADMAP.md` current truth.
- Agent-context entries to add: unavailable while the context transport is
  closed; preserve decisions and evidence in this tracked plan instead.
- Next-slice candidates: graceful proxy drain during rollouts ([#65](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/65));
  connection-aware admission/reservations for bursts; load-plus-prefix hybrid
  routing once APC is enabled and measured.

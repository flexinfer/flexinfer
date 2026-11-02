# Multi-Tenancy Design (M1 Foundation)

**Status:** Draft (M1)
**Owner:** FlexInfer Team
**Last updated:** 2026-10-31
**Roadmap issue:** [#2](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/2)

## Overview

This document defines the first implementation slice for multi-tenancy in FlexInfer:

- namespace-level isolation primitives
- quota and default limit guardrails
- least-privilege tenant RBAC
- optional default-deny network policy pattern
- repeatable tenant onboarding workflow

This slice is intentionally conservative. It provides a practical baseline for shared clusters without introducing new CRDs or scheduler behavior changes.

## Tenancy Model

The selected model for M1 is **namespace-per-tenant** on a shared FlexInfer control plane.

- Control-plane components remain in `flexinfer-system`.
- Tenant workloads (`ModelDeployment`, `ModelCache`, `Model`, `LoRAAdapter`) run in tenant namespaces.
- Each tenant namespace receives a baseline policy bundle from Helm.

## Guarantees (M1)

When the tenant baseline bundle is enabled and applied:

1. **Quota boundaries:** tenant namespaces get a `ResourceQuota` to cap CPU, memory, storage, object counts, and optional extended resources (for example `nvidia.com/gpu`, `amd.com/gpu`).
2. **Default sizing guardrails:** tenant namespaces get a `LimitRange` so pods without explicit resources still receive bounded defaults.
3. **Tenant RBAC baseline:** tenant operators can manage FlexInfer namespaced CRDs and read related workload resources in their own namespace only.
4. **Pod Security defaults:** created tenant namespaces receive Pod Security Admission labels (`baseline` enforce, `restricted` warn/audit by default; configurable).
5. **Network isolation pattern:** optional default-deny + same-namespace-allow + DNS-egress pattern can be enabled per tenant.

## Non-Guarantees (M1)

M1 does **not** provide:

1. Hard GPU partitioning/fair-share scheduling across tenants (step 1 above gives request classes
   on a shared lane, not tenant shares).
2. Cost accounting/chargeback.
3. Cross-tenant runtime isolation stronger than Kubernetes namespace boundaries.
4. Tenant-specific control planes.
5. Admission webhooks for tenancy policy enforcement.

## Step 1: caller QoS classes on a shared lane (D3, 2026-10-31)

Namespace isolation does nothing for the case the fleet actually has: several callers sharing one
model lane with a handful of seats. The production text lane `qwen38-27b-autoround-workhorse` runs
`maxNumSeqs: 2` and serves interactive callers (jobsearch, psyche, the HUD, fi-fhir) next to Mills'
background research and shadow judging. D3 is the first fairness slice, at the request level rather
than the namespace level. State on 2026-10-31: class resolution and labels (!1209), the seat gate
(!1212) and vLLM priority (!1214) are in code; no lane opts in until KT-QOS passes, and enforcement
needs a proxy image built after them.

- **Classes.** The proxy resolves every request to `interactive`, `background` or `batch` from the
  `X-FlexInfer-QoS` header, the caller map in ConfigMap `flexinfer-proxy-qos` (hashed bearer key or
  caller name), a LiteLLM alias suffix (`workhorse-background`), or the `interactive` default.
- **Enforcement per lane, opt-in.** A lane with `spec.config.schedulingPolicy: priority` (or the
  annotation `flexinfer.ai/qos: proxy`) holds `flexinfer.ai/qos-interactive-reserve` seats (default
  1) for interactive requests; background and batch work waits in a class-ordered queue and is
  refused with a typed 503 `queue_full`, `Retry-After` and its queue position when the wait bound
  runs out. On `priority` lanes the proxy also sets vLLM's per-request `priority` (0 / 10 / 20).
- **Why the proxy and not only vLLM.** vLLM 0.32 never preempts a running sequence to free a seat;
  priority scheduling orders its waiting queue and picks the KV-pressure victim. The seat reserve is
  what keeps an interactive turn from waiting behind two background generations.
- **Evidence.** Kill-test KT-QOS (`.loom/32-iteration-plan-kt-qos-2026-10-31.md`, matrix row
  `workhorse-qos-priority-2026-10-31`) decides whether the workhorse opts in with the reserve,
  with vLLM priority, or both.

Contract: `docs/specs/proxy-api.md` ("Caller QoS classes"), `docs/specs/labels-and-annotations.md`
(`flexinfer.ai/qos`), metrics `flexinfer_proxy_qos_*` in `docs/specs/metrics.md`. Classes are not
tenants: there are no quotas, billing or per-tenant lanes, and a caller that sends
`X-FlexInfer-QoS: interactive` is believed. Later steps can bind classes to namespaces or keys.

## Helm Implementation

M1 introduces `tenancy.*` values in `charts/flexinfer/values.yaml` and a new template:

- `charts/flexinfer/templates/tenancy-baseline.yaml`

The template renders per-tenant resources:

- `Namespace` (optional creation)
- `ResourceQuota`
- `LimitRange`
- `NetworkPolicy` resources when `networkPolicyMode: default-deny`
- tenant `Role`
- optional tenant `RoleBinding` if subjects are configured

### Value Structure

`tenancy.tenants[]` supports:

- `namespace` (required)
- `createNamespace` (optional override)
- `labels` (optional namespace labels)
- `podSecurity` (optional enforce/warn/audit overrides)
- `quotaHard` (optional full override for `ResourceQuota.spec.hard`)
- `limitRangeLimits` (optional full override for `LimitRange.spec.limits`)
- `networkPolicyMode` (`disabled` or `default-deny`)
- `subjects[]` (optional rolebinding subjects)

## Example Configuration

```yaml
tenancy:
  enabled: true
  createNamespaces: true
  podSecurity:
    enforce: baseline
    warn: restricted
    audit: restricted
  quotaHard:
    requests.cpu: "8"
    requests.memory: 32Gi
    limits.cpu: "16"
    limits.memory: 64Gi
    pods: "40"
    services: "20"
  networkPolicy:
    mode: default-deny
    allowDNS: true
  tenants:
    - namespace: team-inference
      quotaHard:
        requests.cpu: "12"
        requests.memory: 48Gi
        limits.cpu: "24"
        limits.memory: 96Gi
        nvidia.com/gpu: "2"
      subjects:
        - kind: ServiceAccount
          name: team-inference-operator
          namespace: team-inference
```

## Operational Workflow

1. Add/update tenant entries in Helm values.
2. Run `helm template` and validate rendered namespace policies.
3. Apply via GitOps/Helm release.
4. Verify namespace-level controls:
   - `kubectl get resourcequota -n <tenant>`
   - `kubectl get limitrange -n <tenant>`
   - `kubectl get networkpolicy -n <tenant>` (if enabled)
   - `kubectl auth can-i --as=system:serviceaccount:<tenant>:<sa> create modeldeployments.ai.flexinfer -n <tenant>`

Detailed onboarding steps are in `docs/DEPLOYMENT_RUNBOOK.md`.

## Security Notes

1. ResourceQuota is only effective when workloads declare resources correctly; this should be enforced in admission policy in later phases.
2. Default-deny network policies can break model pulls/egress unless explicit egress rules are added.
3. RBAC subjects should be reviewed by platform owners before rollout.

## Future Enhancements (Post-M1)

1. Tenant policy CRD (declarative API instead of values-driven templates).
2. Admission policy guardrails for mandatory resource requests/limits (see `docs/planning/multi-tenancy-followups.md`, MT-2).
3. Tenant-aware scheduling/fair-share quotas (see `docs/planning/multi-tenancy-followups.md`, MT-3),
   building on the step 1 caller classes.
4. Per-tenant metrics, SLOs, and chargeback.

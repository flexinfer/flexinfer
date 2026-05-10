---
title: Workflow Profiles
description: Stable serving-intent names over existing v1alpha2 Models.
---

# Workflow Profiles

`WorkflowProfile` is a v1alpha2 serving-intent resource. It exposes a stable
client-facing name such as `fast-chat`, `long-context-review`, `code-agent`, or
`image-edit`, then selects existing `Model` resources that can satisfy that
intent.

The first implementation wave is deliberately lightweight:

- Profiles select and explain existing `Model` resources.
- Profiles do not create or mutate underlying models.
- Direct concrete model names continue to work.
- Proxy routing and CLI explain commands are separate follow-up slices.

## Minimal shape

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: WorkflowProfile
metadata:
  name: fast-chat
spec:
  intent: Low-latency chat with tool-calling support
  servedName: fast-chat
  aliases:
    - chat-fast
  selector:
    matchServiceLabels:
      - textgen
      - fast
    capabilities:
      toolCalling: true
  routes:
    - name: ready-fast
      objective: PreferReady
      maxPromptTokens: 12000
      requiredCapabilities:
        toolCalling: true
    - name: fallback-long
      objective: LongestContext
      maxPromptTokens: 32768
  gates:
    requireReadyOrColdStart: true
    requirePromotionEvidence: true
    allowedPhases:
      - Idle
      - Loading
    minSupportLevel: Validated
```

## Selector

`spec.selector` narrows candidate models. At least one selector field must be
set:

- `modelRefs`: same-namespace `Model` names.
- `matchLabels`: Kubernetes labels on the `Model`.
- `matchServiceLabels`: semantic labels from `Model.spec.serviceLabels`.
- `backends`: allowed backend names, such as `vllm`, `ollama`, or `diffusers`.
- `capabilities`: explicit `Model.spec.capabilities` requirements.

The selector is intentionally about existing resources. To add a new model to a
profile, create or update the `Model` separately and make it match the selector.

## Routes

Routes are ordered. A request matches the first route whose constraints fit.
Supported objectives are:

| Objective | Intent |
|-----------|--------|
| `PreferReady` | Prefer warm/ready models before cold starts |
| `LowestLatency` | Prefer the lowest observed latency candidate |
| `LongestContext` | Prefer the largest sufficient context window |
| `CheapestSufficient` | Prefer the smallest validated lane that satisfies the request |
| `Pinned` | Route to a configured model until policy changes |

## Gates

Gates fail closed. A candidate blocked by gates should appear in status as a
rejected candidate rather than receiving traffic.

MVP gate fields:

- `requireReadyOrColdStart`: permit `Ready` plus explicitly allowed cold-start phases.
- `allowedPhases`: extra phases that may be routed, commonly `Idle` for scale-to-zero.
- `requirePromotionEvidence`: require validation or promotion evidence.
- `minSupportLevel`: require `Experimental`, `Canary`, `Validated`, or `Production`.
- `backendAllowlist`: narrow routable backends after broad selector discovery.

Promotion evidence initially maps to existing model promotion conditions and
annotations. See `.loom/60-validation-matrix.md` for the operator evidence
matrix used by current runtime-promotion decisions.

## Status

The controller status slice records:

- resolved served name and aliases
- active route and concrete model
- aggregate gate state
- eligible candidates
- rejected candidates with reasons and evidence references
- conditions: `Resolved`, `CandidatesAvailable`, `GatesSatisfied`, `Routable`

## Examples

- `examples/v1alpha2/workflow-profiles/fast-chat.yaml`
- `examples/v1alpha2/workflow-profiles/long-context-review.yaml`
- `examples/v1alpha2/workflow-profiles/code-agent.yaml`
- `examples/v1alpha2/workflow-profiles/image-edit.yaml`

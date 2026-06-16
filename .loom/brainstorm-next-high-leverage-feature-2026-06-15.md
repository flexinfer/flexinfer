# Brainstorm: Next high-leverage feature for flexinfer

- **Date**: 2026-06-15
- **Author**: claude-code
- **Decision under consideration**: What should the next high-leverage flexinfer feature be?
- **Context**: Tech-debt Wave 2 just shipped (abliteration VRAM caps !620, Renovate Go bumps !621, roadmap-doc cleanup !622). The [DiffusionGemma research](research-diffusiongemma-2026-06-15.md) just exposed a recurring **architecture-servability ceiling**: flexinfer's ROCm vLLM is 0.6.3 standalone; new diffusion-MoE archs need vLLM 0.24.0+ and the dLLM path is NVIDIA-only. Completed milestones: F5 3-way 72B window served; hardware-utilization arc (fleet-util instrumentation, bge embeddings+reranker retrieval plane, SD/batching per-lane verdicts); voice stack live; F4 prefix-cache client forms delivered. Backlog directive: productize the F5 window pattern into a ModelExperiment CRD+controller+MCP ("experiment platform"), gated on "70B daily driver settles" ([[project_experiment_platform]]).

## Riskiest assumption + kill-test

**Load-bearing assumption** (inherited from the winning bet): the operator's "70B daily driver settled" gate is satisfied (F5 3-way 72B is served), so the experiment-platform directive is unblocked and A+B can start now.

**Kill test** (≤30 min): Ask the operator directly whether the gate is cleared; cross-check fleet state — is a 70B/72B lane actually load-bearing/served today, or paused? ([[project_f5_heterogeneous_70b]] notes active 70B work *paused* at a capability ceiling, while [[project_f5_3way_window]] reports 72B *served*.) Resolve the contradiction before committing controller effort.

**Failure mode if wrong**: building the ModelExperiment controller before the gate clears burns effort on an abstraction the operator considers blocked — the premature-abstraction risk A already carries.

**Status**: not run (operator decision pending — see Open Question).

## Phase 1 — Diverge

### A. Experiment platform (ModelExperiment CRD + controller + MCP)
Productize the F5 window / Track-B self-quant pattern: declare an experiment (model, arch, partition, quant, gauntlet); controller orchestrates build→deploy→bench→verdict; MCP surfaces it.
- **Bet**: Repeatable experiments are the bottleneck — every trial today is multi-day hand-work that doesn't compound.
- **Risk**: Building the abstraction before enough experiment variety locks in the wrong shape; directive is gated on "70B settled."

### B. vLLM-currency lane (ROCm runtime modernization pipeline)
Automated track-upstream-vLLM-on-ROCm: bump → build multiarch image off-CI → smoke gauntlet across gfx1100/gfx906 → gate promotion.
- **Bet**: The true ceiling is being stuck on 0.6.3; new archs need current vLLM, and currency should be a cheap recurring process, not a heroic one-off.
- **Risk**: ROCm upstream lag means even current vLLM has NVIDIA-only paths — modernize and still can't serve the shiny model.

### C. Capability pre-flight tool ("can the fleet serve model X?")
Ingest a HF model id → fleet verdict: arch supported by which engine/version, quant path, VRAM fit per GPU, blockers. Turns the manual DiffusionGemma research into a repeatable MCP tool.
- **Bet**: The recurring cost is answering servability + path; a tool collapses hours→seconds and catches gaps before any lane is built.
- **Risk**: Meta-tool — informs, doesn't expand capability; could become a report nobody acts on.

### D. Self-quant as a first-class lane (DEBT-QNT-02 productized)
Decompose `gptq.go`, validated resume default-on, `gptq-reset` helper, observable quant jobs.
- **Bet**: Every model that fits the 24GB fleet needs quant; reliable self-quant is the broadest unblock.
- **Risk**: Tech-debt-adjacent, high effort, regresses a live lane, no user-facing capability.

### E. Daily-driver reliability / SLO + preemption (#29)
Health SLOs, auto-recovery, shared-GPU leader hardening, spot/preemption handling for a now-load-bearing fleet (primary chat, agent-context recall, voice).
- **Bet**: Leverage is hardening what's load-bearing, not adding features.
- **Risk**: Invisible until something breaks; possible over-engineering for a homelab.

### F. Multimodal (vision/image-input) serving
gemma4-26b and DiffusionGemma are multimodal but served text-only. Light up image input (screenshot/doc understanding).
- **Bet**: A new use-class on models already deployed.
- **Risk**: ROCm vision-encoder support + throughput unknown; demand unproven here.

### G. RAG/agent serving platform
Compose bge embeddings+reranker plane + F4 agent-loop forms into a managed agent/RAG product loom-core consumes.
- **Bet**: The retrieval plane + agent loops are seeds of the actual product; composing them is the payoff.
- **Risk**: Scope sprawl, overlaps loom-core's domain (cross-repo), fuzzy boundary.

## Phase 2 — Cross-Pollinate

- **B+C → capability radar that drives modernization**: the pre-flight tool's verdicts auto-feed the currency queue — a model unservable *only* due to vLLM version files a tracked bump + gauntlet run. C becomes the trigger for B, closing the loop *detect gap → expand capability*.
- **A+B → modernization as the experiment platform's first workload**: the ModelExperiment CRD's killer first use case is "canary vLLM 0.x + arch Y against the gauntlet, promote if green." Currency bumps *become* experiments — handing A a concrete recurring driver and defusing its premature-abstraction risk.
- **Tension A ↔ E — research rig vs production daily driver**: A says move fast and trial; E says the fleet is load-bearing, don't destabilize. The real axis: *what is flexinfer primarily now?*
- **Tension B ↔ DiffusionGemma reality**: modernizing vLLM still won't serve NVIDIA-only ROCm-absent paths. Currency's ceiling is upstream ROCm kernel coverage — don't expect B to unlock everything.

## Phase 3 — Converge

### ✅ Recommended: A+B — experiment platform seeded with vLLM-currency as its first workload
The operator's directed direction, and it attacks the live recurring pain the DiffusionGemma research exposed (architecture-servability ceiling). Anchoring the CRD to a concrete repeating job — "canary a new vLLM/arch against the gauntlet, auto-verdict, promote if green" — dissolves A's biggest risk (abstracting too early): the abstraction is *derived from* a real workload, not guessed. The "after 70B settles" gate reads as satisfied (F5 3-way 72B served). Highest compounding leverage: every future model trial and every currency bump rides the same rails.

### 🥈 Runner-up: C alone (capability pre-flight tool)
Tips the choice if the operator wants a fast, low-risk win first or isn't ready to commit to the controller build. Ships in days, immediate self-serve value, and de-risks A+B by quantifying the gap first. Natural Slice 1 even if A+B is the destination.

### ❓ Open question
Is the "70B daily driver settled" gate satisfied (72B window served) so the experiment-platform directive is unblocked — or are Tracks A+B still blocking? The answer picks starting the CRD now (A+B) vs. shipping the pre-flight tool first (C) while the gate clears.

## Handoff
- Direction chosen → `plan-loom-core` for a slice plan (A+B: start with the gauntlet + currency build lane, then the CRD; or C as Slice 1).
- Lineage: link this doc from the resulting `.loom/NNN-product-spec-*.md`.

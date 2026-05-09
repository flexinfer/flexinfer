# Brainstorm Phases — Detailed Guide

The skill enforces a three-phase loop: **Diverge → Cross-Pollinate → Converge**. Each phase has a job and an anti-pattern. Don't blur them.

## Phase 1 — Diverge

**Job**: Generate distinct framings of the problem or solution space. Each framing is one paragraph max. Each gets a one-line "what's the bet" and "what's the risk."

**Target count**: 5–8. Below 5, you didn't actually diverge. Above 8, returns diminish and the user can't hold it in their head.

**Distinctness test**: Two framings are distinct only if their bet or their risk differs *materially*. "Use Postgres" vs. "Use Postgres with pgvector" is one framing, not two. "Use Postgres" vs. "Use a graph DB" is two.

**Anti-patterns**:
- Ranking or hedging in phase 1. No "this is probably the best" yet.
- Generating obvious rephrasings to hit a count. Better to produce 5 strong framings than 8 mushy ones.
- Anchoring on the first framing. Treat it as a draft and force at least 3 unrelated angles before returning.
- Skipping the risk line. Every framing has a downside; if you can't name it, you don't understand the framing.

**Useful prompts to self**:
- "What's the framing the user would never propose themselves?"
- "What's the lazy default that everyone reaches for first? Name it, then move past it."
- "What if the constraint we accepted is wrong?"

## Phase 2 — Cross-Pollinate

**Job**: Find combinations and tensions between framings.

**Combinations**: Can framing A's mechanism + framing B's framing produce something neither has alone? Often yes, often the best output of the whole skill.

**Tensions**: Which framings are in direct opposition? Naming the opposition usually clarifies which axis the real decision lives on.

**Output**: 1–3 combinations or tensions. Not exhaustive — just the load-bearing ones.

**Anti-patterns**:
- Combining for the sake of combining. If A+B is just "do both," skip it.
- Treating tensions as obstacles to dissolve. Often the tension *is* the insight.

## Phase 3 — Converge

**Job**: Rank by leverage × feasibility, name the top 1–2, hand back to the user with the decision tradeoff explicit.

**Format**:
- **Recommended**: one framing (or one combination), one paragraph on why it wins.
- **Runner-up**: one framing, one paragraph on what would tip the choice the other way.
- **Open question**: one question the user has to answer before the recommended path is actually chosen.

**Anti-patterns**:
- Recommending more than 2. That's not converging.
- Hiding the tradeoff. The runner-up exists *because* there's a real tradeoff; if you can't articulate it, the recommendation isn't grounded.
- Picking the "safe" framing because it's safe. Safe is fine when the situation calls for it, but say that explicitly.

## Output Document

Write to `.loom/brainstorm-<slug>-<YYYY-MM-DD>.md` using the template in `assets/templates/brainstorm-doc.md`. The doc is shareable across agents (per workspace policy: root `.loom/` is git-tracked for multi-agent visibility).

If the brainstorm leads to a real plan or spec, link the brainstorm doc from the resulting `.loom/NNN-product-spec-*.md` so the lineage is preserved.

## When to Use This Skill vs. Others

| Situation | Use |
|-----------|-----|
| "Critique this existing thing" | `multi-lens-product-review` |
| "Find facts to ground a decision" | `research` |
| "Turn an aligned direction into a structured plan" | `plan-loom-core` |
| **"I'm stuck / what are my options / give me framings"** | **`brainstorm`** |
| "Score and prioritize a known list of items" | `tech-debt-planning` |

`brainstorm` is the *generative* phase. Once a direction is chosen, hand off.

# Research: Agentic Engineering Patterns (Willison 2026)

**Source:** [simonwillison.net/guides/agentic-engineering-patterns](https://simonwillison.net/guides/agentic-engineering-patterns)
**Date:** 2026-04-05
**Purpose:** Map external patterns against loom's current capabilities, identify gaps and encoding opportunities.

---

## Pattern Inventory

### 1. Principles

#### 1.1 Agent Definition & Core Loop
**Pattern:** An agent is "software that runs tools in a loop to achieve a goal." LLM + system prompt + tools in iterative loop. Code execution capability distinguishes agentic engineering from AI-assisted coding.

**Loom status:** Fully implemented. The daemon proxy (`callPipeline`) orchestrates exactly this loop — routing tool calls through connection pools to MCP servers. The `loom proxy` command exposes this as a transparent bridge.

#### 1.2 Code Is Cheap, Good Code Is Expensive
**Pattern:** Writing code is now near-free; the cost shifted to verification, correctness, testing, documentation, and maintainability. Challenge intuitions about effort — "fire off a prompt anyway, in an asynchronous agent session."

**Loom status:** Partially encoded. Loom supports async agent sessions, but the "fire off a prompt" low-friction pattern isn't surfaced as a first-class workflow. The `parallel-slice-ship` skill approaches this but requires upfront planning.

**Opportunity:** A "speculative spike" skill — fire-and-forget async agent session that explores a question, produces a mini-report, and archives it. No commit, no PR. Just knowledge generation.

#### 1.3 Knowledge Hoarding & Recombination
**Pattern:** Systematically collect working code examples. "We only ever need to figure out a useful trick once." Agents can fetch from public repos, search local collections, and recombine proven implementations.

**Loom status:** The agent-context memory system (`agent_memory_add`, `agent_memory_recall`, `agent_memory_promote`) captures decisions and findings. The `.loom/` planning artifacts persist research across sessions. Auto-recall on session-start pulls relevant context.

**Gap:** No structured "trick library" or "recipe index." Memories are free-text blobs — they don't have typed categories like "recipe," "pattern," or "gotcha." Recall is keyword-based, not structured.

**Opportunity:** A `recipes` memory tier — structured entries with `problem`, `solution`, `proof` (file reference or test), and `tags`. Agents could `agent_recipe_add` when they solve something novel and `agent_recipe_recall` when facing similar problems. The key insight: each recipe needs a *working code reference*, not just prose.

#### 1.4 Compound Engineering Loop
**Pattern:** Complete projects → retrospective → document what worked → apply lessons to future agent instructions. Systematic refinement of agent behavior over time.

**Loom status:** The session summarization pipeline (`agent_session_end --summarize`) produces summaries. The `decision-journal` skill records decisions. CLAUDE.md/AGENTS.md instructions accumulate.

**Gap:** No automated feedback loop. Session summaries exist but aren't automatically distilled into instruction updates. The loop is manual — someone reads summaries and manually edits AGENTS.md.

**Opportunity:** A `retrospective` post-session hook that:
1. Reads the session summary
2. Identifies patterns (repeated failures, novel solutions, workflow friction)
3. Proposes AGENTS.md amendments or new skill definitions
4. Queues them for human review (not auto-apply)

#### 1.5 Technical Debt as Agent Fodder
**Pattern:** Agents excel at conceptually-simple-but-time-consuming refactoring: renaming, consolidation, module splitting, API cleanup. Launch asynchronously, review via PR.

**Loom status:** The `refactor` and `tech-debt-backlog-dev-loop` skills exist. The `parallel-slice-ship` workflow can decompose large refactors.

**Status:** Well-covered.

#### 1.6 Anti-Pattern: Unreviewed Code in PRs
**Pattern:** Never file PRs with code you haven't reviewed. Don't dump agent output on collaborators. Small PRs, provide context, show evidence of testing.

**Loom status:** The `code-review` skill exists. The `merge-readiness` skill validates before merge. But there's no *gate* preventing unreviewed agent PRs.

**Opportunity:** A `pr-self-review` step in shipping workflows that forces the agent to diff-review its own changes before creating the MR. Already partially done in `feature-dev` workflow but could be more rigorous.

---

### 2. Working with Coding Agents

#### 2.1 LLM Mechanics: Statelessness & Token Caching
**Pattern:** LLMs are stateless — agents replay full conversation history each call. Providers cache repeated prefixes for reduced cost. Agents should avoid modifying early conversation content to maximize cache hits.

**Loom status:** The proxy daemon handles connection pooling and transport, but token caching is a provider-side concern. The `auto-recall` strategy ("fast" vs "full") controls how much context is injected at session start.

**Insight for loom:** The auto-recall payload should be *prepended* as a system-level context block rather than injected mid-conversation, to maximize prefix cache hits across turns.

#### 2.2 Git Integration Strategies
**Pattern:** Agents excel at git operations: bisect, merge conflict resolution, history rewriting, repo extraction. Lean into these capabilities.

**Loom status:** Worktree-first development is the primary pattern. `agent_worktree_allocate` creates isolated branches. Auto-ship policy handles commit/push/MR/merge/cleanup automatically.

**Gap:** No `git bisect` integration. No "extract to new repo" workflow. No merge conflict resolution skill.

**Opportunity:** A `git-bisect-debug` skill that takes a test command and a symptom description, runs automated bisect, and reports the introducing commit with context.

#### 2.3 Subagent Architecture
**Pattern:** Three types: sequential/blocking (explore, return findings), parallel (concurrent independent edits), specialist (custom system prompts per role). Primary value: preserving root context window.

**Loom status:** Fully implemented across all three patterns:
- Sequential: `Explore` subagent type, `Plan` subagent type
- Parallel: `parallel-slice-ship` skill, `slice-implementer` agent type
- Specialist: `code-reviewer`, `statusline-setup`, etc.

**Insight:** Willison warns against over-engineering subagent specialization. Loom's current set is reasonable. The `slice-implementer` isolated-worktree pattern is a strong implementation of parallel subagents.

---

### 3. Testing & QA

#### 3.1 Red/Green TDD
**Pattern:** "Use red/green TDD" — write tests first, confirm they fail (red), implement until they pass (green). Prevents both non-functional code and unnecessary code.

**Loom status:** Testing guidelines exist in rules, but TDD isn't encoded as a *workflow step*. The `feature-dev` workflow has "test" as step 5 of 10 — after implementation, not before.

**Gap:** No TDD-first workflow variant. Tests are a validation step, not a specification step.

**Opportunity:** A `tdd-feature-dev` workflow variant where the agent:
1. Writes failing tests from the spec
2. Confirms red (tests fail)
3. Implements until green
4. Refactors
This is a significant pattern shift worth encoding.

#### 3.2 "First Run the Tests"
**Pattern:** Four-word prompt that transforms agent behavior. Forces discovery of test infrastructure, provides project complexity estimate, activates testing mindset.

**Loom status:** The SessionStart hook runs `auto-recall` which injects context, but doesn't run tests. The `quality-gate-loop` skill runs tests as a gate.

**Opportunity:** Add a `first-run-tests` option to session-start or recall that executes the project's test suite and includes a summary in the session context. This gives the agent immediate awareness of project health.

#### 3.3 Agentic Manual Testing
**Pattern:** Never assume LLM-generated code works until executed. Use `python -c`, temp files in `/tmp`, `curl` for APIs, Playwright for browser UIs. Showboat for documenting test sessions.

**Loom status:** The `devbox_exec` sandbox enables safe execution. The `browserkit-screenshots` skill provides Playwright-based browser testing. The `quality-gate-loop` skill validates builds.

**Status:** Well-covered. The devbox sandbox is a stronger version of the `/tmp` file pattern — it provides full isolation.

---

### 4. Understanding Code

#### 4.1 Linear Walkthroughs
**Pattern:** Agents produce structured explanations of codebases by extracting actual code (via sed/grep/cat, preventing hallucination) interleaved with commentary.

**Loom status:** The `repo-intake` skill does codebase exploration. The `codebase-exploration-memory` skill preserves findings. But neither produces a structured *walkthrough document*.

**Opportunity:** A `codebase-walkthrough` skill that produces a structured Markdown document explaining a subsystem, with actual code excerpts extracted by the agent (not hallucinated). Stored in `.loom/` for future agent sessions.

#### 4.2 Interactive Explanations
**Pattern:** When text descriptions fail, agents build animated/interactive visualizations to explain algorithms. Reduces "cognitive debt" — the burden of not understanding agent-generated code.

**Loom status:** Not implemented. This is a UI/visualization concern outside loom's current scope. The HUD could potentially host algorithm visualizations, but this is a stretch.

**Status:** Out of scope for now.

---

### 5. Annotated Prompts (GIF Optimization Case Study)

Key patterns from the case study:

#### 5.1 Assumption Leveraging
**Pattern:** Trust the agent's knowledge of widely-used tools. Don't over-specify what the agent already knows.

**Loom status:** AGENTS.md instructions tend to be comprehensive. Could benefit from pruning redundant instructions that agents already know (e.g., standard Go testing patterns).

#### 5.2 Reference-by-Example
**Pattern:** Point agents at existing implementations in the codebase. "Add tests inspired by how ~/dev/ecosystem/llm-mistral is doing it."

**Loom status:** Auto-recall can surface relevant prior context. The `agent_context_recall_enhanced` with `file_context` parameter enables this.

**Status:** Well-covered via the recall system.

#### 5.3 Browser Automation for Verification
**Pattern:** Use dedicated browser CLIs (Playwright, Rodney, agent-browser) for continuous visual verification during development.

**Loom status:** `browserkit-screenshots` skill provides this.

---

## Priority Encoding Opportunities

Ranked by impact × feasibility:

### Tier 1: High Impact, Low Effort

| # | Pattern | Encoding | Effort |
|---|---------|----------|--------|
| 1 | **TDD-first workflow** | New `tdd-feature-dev` workflow in skills-registry | 1-2h |
| 2 | **"First run the tests" on session start** | Add test-suite discovery + execution to SessionStart hook or auto-recall | 2-3h |
| 3 | **Compound engineering loop** | Post-session retrospective hook proposing AGENTS.md updates | 3-4h |

### Tier 2: Medium Impact, Medium Effort

| # | Pattern | Encoding | Effort |
|---|---------|----------|--------|
| 4 | **Structured recipe library** | New `recipes` memory tier with problem/solution/proof schema | 4-6h |
| 5 | **PR self-review gate** | Pre-MR step in shipping workflows: agent reviews own diff | 2-3h |
| 6 | **Git bisect skill** | New `git-bisect-debug` skill | 3-4h |
| 7 | **Codebase walkthrough skill** | New `codebase-walkthrough` producing structured docs | 3-4h |

### Tier 3: Nice to Have

| # | Pattern | Encoding | Effort |
|---|---------|----------|--------|
| 8 | **Speculative spike skill** | Fire-and-forget async exploration sessions | 2-3h |
| 9 | **Auto-recall prefix optimization** | Ensure recall context maximizes token cache hits | 1-2h |

---

## Key Takeaways

1. **Loom is ahead on infrastructure** — connection pooling, subagent architecture, worktree isolation, sandbox execution, and lifecycle hooks are all more sophisticated than what Willison describes. The daemon proxy pattern is a significant differentiator.

2. **Loom is behind on feedback loops** — the compound engineering loop (session → retrospective → instruction update) is manual. Automating the insight-to-instruction pipeline would be high leverage.

3. **TDD-first is conspicuously absent** — tests are a validation step, not a specification step. Encoding TDD as a workflow variant would align with Willison's strongest recommendation.

4. **Knowledge management needs structure** — agent-context memories are unstructured text. A typed recipe/pattern library with working code references would make the hoarding-and-recombination pattern practical.

5. **The "fire off a prompt" low-friction pattern** needs a simpler entry point than the current workflow system. A speculative spike that requires zero upfront planning would lower the bar for exploratory work.

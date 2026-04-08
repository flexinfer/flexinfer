# Implementation Plan: Dark Factory Patterns

**Date:** 2026-04-05
**Source research:** `.loom/77-research-agentic-engineering-patterns-2026-04-05.md`
**Concept:** Encode professional-grade engineering patterns into loom's skill/workflow/hook scaffold so agents produce deterministic, high-quality output without human handholding.

---

## Dark Factory Thesis

A "dark factory" runs lights-off — no humans on the floor. The quality comes from the *tooling and process*, not from supervision. Applied to agentic engineering:

1. **Deterministic quality** comes from automated gates, not human review at each step
2. **Self-correcting loops** replace human "try again" prompts
3. **Structured knowledge** eliminates interpretation ambiguity — agents consume typed schemas, not prose hints
4. **Feedback flows back** — every session's learnings strengthen the next session's scaffold
5. **Verification precedes shipping** — nothing leaves the factory without automated proof of correctness

The gap in loom today: workflows have approval gates (`step_type: approval`) where a human says "ready?" These are handholding points. The dark factory replaces them with automated verification gates.

---

## Implementation Slices

### Slice 1: TDD-First Workflow (`tdd-dev`)

**Problem:** Current `feature-dev` workflow puts tests at step 5/10 — after implementation. This means agents write code first, then retrofit tests. The result is tests that verify what was written, not tests that specify what should exist.

**Dark factory pattern:** Tests are the *specification*. Agent writes failing tests from requirements, confirms red, implements until green. No ambiguity about "done" — green means done.

**Deliverables:**
- New skill `tdd-dev` in `skills-registry.yaml` (type: `command` on claude, `skill` on codex)
- New workflow `tdd-dev.yaml` in `.agents/workflows/`
- Workflow steps: `init → worktree → recall → write-tests → verify-red → implement → verify-green → refactor → precommit → commit → push → end → cleanup`
- Key difference: `verify-red` and `verify-green` are `step_type: tool` (automated), not `approval` (human)
- Instructions encode: "Write the minimum tests that specify the feature. Run them. They MUST fail. Only then implement. Run again. They MUST pass."

**Files to touch:**
- `mcp/context/skills-registry.yaml` — new skill entry
- `.agents/workflows/tdd-dev.yaml` — new workflow file
- `mcp/skills/tdd-dev/` — skill directory (references, templates)

**Independence:** Fully independent. New skill, new workflow file. No existing file conflicts.

---

### Slice 2: Automated Retrospective Hook (`session-retro`)

**Problem:** The compound engineering loop is manual. Session summaries exist but nobody reads them to improve future sessions. Institutional learning doesn't compound.

**Dark factory pattern:** Every session end triggers an automated retrospective that proposes concrete instruction amendments. The factory gets smarter with every run.

**Deliverables:**
- New `PostToolUse` hook extra: `postSessionEnd_retrospective` in `configs_hooks.go`
- Shell script `scripts/session-retro.sh` that:
  1. Reads the session summary from `loom agent session --agent-id "$AGENT_ID"`
  2. Extracts: failures encountered, novel solutions found, workflow friction points
  3. Writes structured findings to `.loom/local/retro-<session-id>.md`
  4. Appends to a rolling `.loom/local/retro-queue.md` for human batch review
- New skill `session-retro` for on-demand retrospective with pattern extraction
- Integration: hook fires on `Stop`/`SessionEnd` event, after the existing session-end hook

**Files to touch:**
- `pkg/generator/configs_hooks.go` — new hook extra case + builder function
- `pkg/generator/platform_profiles.yaml` — add `postSessionEnd_retrospective` to claude/gemini extras
- `mcp/context/skills-registry.yaml` — new skill entry
- `mcp/skills/session-retro/scripts/session-retro.sh` — new script

**Independence:** Touches `configs_hooks.go` (shared file) but adds a new case in `appendHookExtras` switch — minimal conflict risk. The hook extra name is unique.

---

### Slice 3: Structured Recipe Library (`agent-recipes`)

**Problem:** Agent memories are unstructured text blobs. When an agent solves a novel problem, the solution is recorded as prose. Future agents searching for it get fuzzy matches, not deterministic answers.

**Dark factory pattern:** Recipes are typed entries with `problem`, `solution`, `proof` (file ref or test command), and `tags`. An agent that solved "how to handle stale pool connections" records a recipe. A future agent facing the same class of problem gets a deterministic, proven answer — not a fuzzy recall.

**Deliverables:**
- New MCP tools: `agent_recipe_add`, `agent_recipe_recall`, `agent_recipe_list`
- Recipe schema: `{title, problem, solution, proof, tags, language, scope}`
  - `proof` is required — either a file path + line range, a test command, or a URL
  - `scope` is "project" | "workspace" | "universal"
- Storage: recipes stored in agent-context Qdrant collection with `entry_type: "recipe"`
- New skill `agent-recipes` documenting when to add/recall recipes
- Integration with auto-recall: session-start pulls relevant recipes for the current project

**Files to touch:**
- `cmd/mcp-agent-context/tools_recipes.go` — new tool file
- `pkg/agentcontext/svc_recipes.go` — service implementation
- `mcp/context/skills-registry.yaml` — new skill entry
- `mcp/skills/agent-recipes/` — skill directory

**Independence:** New files only. The MCP tool registration is additive (new tool file in `cmd/mcp-agent-context/`).

---

### Slice 4: Automated Quality Gate (`auto-quality-gate`)

**Problem:** The `feature-dev` workflow has three human approval gates: `implement`, `test`, `precommit`. Each is a handholding point where a human says "yes, proceed." In the dark factory, these should be automated verification.

**Dark factory pattern:** Replace approval gates with automated tool gates. The agent runs the verification itself and only stops if verification *fails*. Success = proceed automatically.

**Deliverables:**
- New skill `auto-quality-gate` with a verification script
- Script `scripts/auto-quality-gate.sh` that runs in sequence:
  1. `make fmt` or language-appropriate formatter
  2. `make lint` or language-appropriate linter
  3. `make test` or language-appropriate test suite
  4. `git diff --check` (whitespace errors)
  5. Exit 0 if all pass, exit 1 with structured failure report if any fail
- Updated `feature-dev` workflow variant (`feature-dev-auto.yaml`) that replaces `step_type: approval` with `step_type: tool` calling the quality gate
- Integration with devbox: gate runs inside sandbox when available
- Failure mode: on gate failure, agent gets structured error report and self-corrects (retry loop, max 3 attempts)

**Files to touch:**
- `mcp/context/skills-registry.yaml` — new skill entry
- `mcp/skills/auto-quality-gate/scripts/auto-quality-gate.sh` — new script
- `.agents/workflows/feature-dev-auto.yaml` — new workflow variant
- `mcp/skills/auto-quality-gate/` — skill directory

**Independence:** Fully independent. New files only. The existing `feature-dev.yaml` is untouched — this creates a parallel variant.

---

### Slice 5: Session Health Injection (`test-health-inject`)

**Problem:** Agents start sessions blind to project health. They don't know if tests are passing, what coverage looks like, or whether the build is green. This leads to wasted cycles discovering broken state mid-task.

**Dark factory pattern:** "First run the tests." Inject test suite health into the session context at startup. The agent starts every session knowing: build status, test pass/fail count, recent failures, coverage delta.

**Deliverables:**
- New SessionStart hook addition: run test suite discovery + health check
- Shell script `scripts/test-health-snapshot.sh` that:
  1. Detects project language (Go/Python/TS/Rust) from file patterns
  2. Runs the test suite with timeout (30s max — fast feedback, not full suite)
  3. Captures: total tests, passed, failed, skipped, runtime
  4. Emits structured JSON as `{"systemMessage": "Project health: 142 tests, 140 passed, 2 failed (TestFoo, TestBar). Build: OK. Last commit: abc123."}`
- Integration: appended to SessionStart hook array in `buildPlatformHooks`
- Opt-in via registry setting `test_health_on_session_start: true` (default false — don't slow down all sessions)

**Files to touch:**
- `pkg/generator/configs_hooks.go` — add test-health hook to SessionStart array (conditional on registry setting)
- `pkg/generator/configs_hooks_test.go` — test for conditional inclusion
- `mcp/context/skills-registry.yaml` — new skill entry for documentation
- `mcp/skills/test-health-inject/scripts/test-health-snapshot.sh` — new script

**Independence:** Touches `configs_hooks.go` but adds to `sessionStartHooks` array — different section than Slice 2 (which adds to session-end). Low conflict risk.

---

### Slice 6: PR Self-Review Gate (`pr-self-review`)

**Problem:** Agents create PRs/MRs without reviewing their own diffs. The anti-pattern from Willison: "Don't file PRs with code you haven't reviewed." Current shipping workflows go straight from commit to MR creation.

**Dark factory pattern:** Before creating an MR, the agent reviews its own diff using a structured checklist. If the review finds issues, it self-corrects before shipping. The MR arrives pre-reviewed.

**Deliverables:**
- New skill `pr-self-review` with structured review checklist
- Review checklist (encoded in skill instructions):
  1. Diff size check (>500 lines = split warning)
  2. No debug statements (console.log, fmt.Println, print())
  3. No commented-out code
  4. No hardcoded secrets/credentials
  5. All new functions have tests
  6. No unrelated changes (scope creep)
  7. Commit message follows conventional format
- Integration point: called in `feature-dev` / `parallel-slice-ship` instructions between "commit" and "push+MR" steps
- Self-correction: if issues found, agent fixes them and re-runs review before proceeding

**Files to touch:**
- `mcp/context/skills-registry.yaml` — new skill entry
- `mcp/skills/pr-self-review/` — skill directory with checklist reference
- Updated instructions in `feature-dev`, `bugfix`, `parallel-slice-ship` skills to reference `pr-self-review` before MR creation

**Independence:** Mostly new files. The skill registry additions for existing skills are instruction text changes (low conflict — different line ranges from other slices).

---

## Slice Dependency Map

```
Slice 1 (tdd-dev)          — independent
Slice 2 (session-retro)    — independent (configs_hooks.go: session-end section)
Slice 3 (agent-recipes)    — independent (new Go files + skill)
Slice 4 (auto-quality-gate)— independent (new workflow + skill)
Slice 5 (test-health)      — independent (configs_hooks.go: session-start section)
Slice 6 (pr-self-review)   — weak dep on Slice 4 (both touch shipping workflow instructions)
```

Slices 1-5 are fully independent. Slice 6 has a weak dependency on Slice 4 (both reference shipping workflows) but touches different instruction sections.

**Recommended parallel groups:**
- Group A: Slices 1, 2, 3 (no shared files)
- Group B: Slices 4, 5, 6 (share configs_hooks.go but different sections)

Or all 6 in parallel if each slice uses worktree isolation.

---

## Integration Order

After all slices land on their branches:

1. Merge Slice 3 first (new Go files — clean merge)
2. Merge Slice 1 (new skill + workflow — clean merge)
3. Merge Slice 4 (new skill + workflow variant — clean merge)
4. Merge Slice 2 + 5 together (both touch `configs_hooks.go` — resolve in one pass)
5. Merge Slice 6 last (references other slices' skills in instruction text)
6. Run `loom sync --all-projects --regen` to propagate all changes
7. Full test suite: `go test ./...`
8. Build + restart loomd

---

## Success Criteria

The dark factory is working when:

1. An agent can complete a `tdd-dev` workflow end-to-end without a single approval gate
2. Session retrospectives automatically queue instruction improvement proposals
3. Agents facing a previously-solved problem class get a deterministic recipe with proof
4. Quality gates are automated — fmt, lint, test pass without human "yes, proceed"
5. Agents start sessions knowing project health state
6. PRs arrive pre-reviewed with structured evidence of self-review

**Metric:** Ratio of `step_type: approval` to `step_type: tool` gates in active workflows should decrease. The dark factory target is zero approval gates for routine feature work.

# Backlog Delivery Loop Reference

This reference supports the `backlog-delivery-loop` skill with concrete decision points and fallback behavior.

## Backlog Source Priority

Use the first available source:

1. Jira backlog (priority + status fields)
2. GitLab issues (labels/milestones/weight)
3. GitHub issues (labels/milestones/projects)
4. Local roadmap/spec docs

## Selection Heuristic

Choose one item using:

1. Highest declared priority
2. Not blocked
3. Oldest updated timestamp
4. Smallest likely implementation slice

## Local Verification Contract

Before commit/push, run local verification gates:

1. `pre-commit run -a` (if configured)
2. repository-native verification (`make verify` / `make ci` / `make check`, then test/lint fallback)
3. language fallback checks when no repo target exists

You can run the bundled helper:

```bash
bash $CODEX_HOME/skills/backlog-delivery-loop/scripts/verify_local_loop.sh
```

If any check fails, fix and rerun before pushing.

## Dirty Worktree Baseline

If there are pre-existing modified/untracked files before the loop starts:

1. Treat them as baseline context, not an automatic blocker.
2. Continue on the current branch/worktree by default.
3. Stage/commit only files for the selected backlog item.
4. Escalate only when new unexpected changes appear in files you are editing, or when branch/worktree switching is needed.

## CI Verification Matrix

- GitLab:
  - `list_pipelines`
  - `pipeline_summary`
  - `poll_pipeline`
  - `get_job_trace` for failures
- GitHub:
  - verify through available repo tooling (PR checks or equivalent)
  - if no direct check API available in session, report limitation explicitly

## CI Retry Loop

1. Poll latest pipeline for pushed branch.
2. If failed, fetch failing job logs/tests.
3. Implement minimal fix.
4. Rerun local verification contract.
5. Push and repoll.
6. Repeat until green or blocked.

Bundled helper:

```bash
bash $CODEX_HOME/skills/backlog-delivery-loop/scripts/verify_ci_loop.sh --provider auto --ref <branch>
```

Explicit provider overrides:

```bash
bash $CODEX_HOME/skills/backlog-delivery-loop/scripts/verify_ci_loop.sh --provider gitlab --project <group/project> --ref <branch>
bash $CODEX_HOME/skills/backlog-delivery-loop/scripts/verify_ci_loop.sh --provider github --repo <owner/repo> --ref <branch>
```

You can pin a known pipeline/run:

```bash
bash $CODEX_HOME/skills/backlog-delivery-loop/scripts/verify_ci_loop.sh --project <group/project> --pipeline-id <id>
bash $CODEX_HOME/skills/backlog-delivery-loop/scripts/verify_ci_loop.sh --provider github --repo <owner/repo> --run-id <id>
```

## Blocked Handoff Contract

If blocked by external dependency:

1. Set task status to `blocked` with exact blocker.
2. Record what has been validated already.
3. Provide the next concrete command/tool call for the next agent.

## Completion Definition

The loop is complete when:

1. Implementation merged/pushed for selected item
2. Local checks are clean (hooks + tests + lint where configured)
3. CI is passing or a blocker is clearly documented
4. Agent-context task is closed with resolution
5. Session is summarized for the next run

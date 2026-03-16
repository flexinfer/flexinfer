# Agent Handoff Brief

**From**: {source_agent}
**To**: {target_agent}
**Created**: {timestamp}
**Handoff ID**: {handoff_id}

## Context Summary

{context_summary}

## Key Decisions

| Decision | Rationale | Impact |
|----------|-----------|--------|
| {decision_1} | {rationale_1} | {impact_1} |

## Critical Findings

1. {finding_1}
2. {finding_2}
3. {finding_3}

## Files of Interest

| File | Purpose | Lines |
|------|---------|-------|
| `{file_path_1}` | {purpose_1} | {lines_1} |

## Pending Tasks

- [ ] {task_1} (priority: {priority_1})
- [ ] {task_2} (priority: {priority_2})

## Open Questions

1. {question_1}
2. {question_2}

## Instructions for Target Agent

{instructions}

---

## Acceptance

To accept this handoff:

```
agent_handoff_accept(
    handoff_id="{handoff_id}",
    session_id="new-session-id",
    import_entries=true
)
```

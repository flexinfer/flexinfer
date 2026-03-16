# Agent Handoff: {title}

**From**: {source_agent} | **To**: {target_agent}
**Created**: {timestamp}
**Priority**: {priority}
**Namespace**: {namespace}

## Summary

{summary}

## Context

### Key Decisions Made

{decisions}

### Key Findings

{findings}

### Files to Focus On

{key_files}

## Pending Tasks

{pending_tasks}

## Open Questions

{open_questions}

## Handoff Acceptance

To accept this handoff:
```
agent_handoff_accept(handoff_id="{handoff_id}")
```

This will:
1. Create a new session linked to the original
2. Import all context from the source session
3. Mark all pending tasks as yours

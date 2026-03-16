# Quick Decision: {title}

**Date**: {timestamp}
**Category**: {category}
**Tags**: {tags}

## Decision

{decision}

## Why

{rationale}

## Trade-offs

- Chose: {choice}
- Over: {alternative}
- Because: {reason}

## Impact

{impact}

---

Record this decision:

```
agent_context_add(
    session_id="...",
    entries=[{
        entry_type: "decision",
        title: "{title}",
        content: "{decision}\n\nRationale: {rationale}",
        tags: [{tags}]
    }]
)
```

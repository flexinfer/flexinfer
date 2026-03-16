# Approval Request: {workflow_name}

**Workflow ID**: {workflow_id}
**Step**: {step_name}
**Requested**: {timestamp}
**Expires**: {expiry}

## Summary

{summary}

## Changes

{changes}

## Testing Results

{test_results}

## Risk Assessment

- **Risk Level**: {risk_level}
- **Rollback Plan**: {rollback_plan}

## Approvers

| Name | Status | Timestamp |
|------|--------|-----------|
| {approver_1} | {status_1} | {timestamp_1} |

## Actions

### Approve

```
agent_workflow_approve(
    workflow_id="{workflow_id}",
    step_name="{step_name}",
    comment="Approved"
)
```

### Reject

```
agent_workflow_reject(
    workflow_id="{workflow_id}",
    step_name="{step_name}",
    reason="{rejection_reason}"
)
```

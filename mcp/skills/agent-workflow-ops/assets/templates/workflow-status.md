# Workflow Status: {workflow_name}

**ID**: {workflow_id}
**Definition**: {definition_name}
**Started**: {start_time}
**Status**: {status}

## Parameters

| Parameter | Value |
|-----------|-------|
| version | {version} |
| environment | {environment} |

## Step Progress

| Step | Status | Duration | Notes |
|------|--------|----------|-------|
| prepare | {step_1_status} | {step_1_duration} | |
| test | {step_2_status} | {step_2_duration} | |
| build | {step_3_status} | {step_3_duration} | |
| deploy-staging | {step_4_status} | {step_4_duration} | |
| staging-approval | {step_5_status} | {step_5_duration} | |
| deploy-production | {step_6_status} | {step_6_duration} | |
| verify | {step_7_status} | {step_7_duration} | |

## Current Step

**Name**: {current_step}
**Status**: {current_status}

## Recent Events

| Time | Event | Details |
|------|-------|---------|
| {event_1_time} | {event_1_type} | {event_1_details} |

## Actions

### Cancel Workflow

```
agent_workflow_cancel(
    workflow_id="{workflow_id}",
    reason="{cancel_reason}"
)
```

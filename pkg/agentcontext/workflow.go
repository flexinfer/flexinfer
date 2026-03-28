// workflow.go — workflow subsystem entry point.
//
// The workflow implementation is split across several files:
//   - workflow_engine.go  — public workflow API, engine lifecycle, and CRUD operations
//   - workflow_executor.go — DAG execution, step dispatch, rollback, and event emission
//   - workflow_expr.go    — condition evaluation, variable resolution, DAG validation, and utility helpers
//   - workflow_persist.go — Qdrant persistence layer for workflows and definitions
//
// Shared types (WorkflowDefinition, Workflow, WorkflowStep, etc.) live in schema.go.
package agentcontext

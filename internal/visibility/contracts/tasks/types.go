// Package tasks holds the agent-task DTOs surfaced through the
// agent_task_* tools and the HUD/CLI task surfaces.
//
// # Scaffold-only in S1
//
// The canonical TaskInfo type currently lives in internal/hud/bridge
// because its UnmarshalJSON method depends on the bridge package's
// CanonicalProject helper and the bridge-local *PipelineRef type. Lifting
// the type without those would break method identity (Go requires methods
// to live in the same package as their receiver type). A later EPIC 2 (#66)
// slice will move CanonicalProject and PipelineRef alongside TaskInfo and
// complete the lift.
//
// For now this package is reserved for that future placement and exists so
// downstream readers can import internal/visibility/contracts/tasks without
// churn when the lift completes.
package tasks

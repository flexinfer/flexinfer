import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("Ops Models")
struct OpsModelsTests {
    @Test("Tasks decode when context/tags/blocked_by are omitted")
    func tasksDecodeWithOmittedOptionalFields() throws {
        let json = """
        {
          "ok": true,
          "data": {
            "tasks": [
              {
                "id": "task-1",
                "session_id": "session-1",
                "agent_id": "codex-1",
                "namespace": "loom-core/main",
                "title": "Ship mobile parity",
                "priority": "high",
                "status": "in_progress",
                "created_at": "2026-02-25T10:00:00Z",
                "updated_at": "2026-02-25T10:05:00Z"
              }
            ],
            "counts": {
              "pending": 1,
              "in_progress": 2,
              "blocked": 0,
              "completed": 3
            }
          },
          "meta": {
            "request_id": "req_ops_1",
            "timestamp": "2026-02-25T10:05:00Z"
          }
        }
        """

        let envelope = try JSONDecoder().decode(APIEnvelope<MobileTasksResponse>.self, from: Data(json.utf8))
        #expect(envelope.ok == true)

        guard let payload = envelope.data else {
            Issue.record("Expected data payload")
            return
        }
        #expect(payload.tasks.count == 1)
        #expect(payload.counts.inProgress == 2)

        let task = payload.tasks[0]
        #expect(task.context == "")
        #expect(task.tags.isEmpty)
        #expect(task.blockedBy.isEmpty)
        #expect(task.status == .inProgress)
    }

    @Test("Tasks decode richer source and linkage metadata when present")
    func tasksDecodeWithSourceMetadata() throws {
        let json = """
        {
          "ok": true,
          "data": {
            "tasks": [
              {
                "id": "task-2",
                "session_id": "session-2",
                "agent_id": "claude-1",
                "namespace": "loom-core/main",
                "title": "Normalize work items",
                "context": "Projected from heartbeat",
                "priority": "medium",
                "status": "pending",
                "tags": ["projected"],
                "blocked_by": ["ci"],
                "source_platform": "claude",
                "source_kind": "projected",
                "native_key": "current_task:normalize",
                "workflow_id": "wf-2",
                "pipeline_ref": {
                  "id": 42,
                  "project": "services/loom-core",
                  "ref": "main",
                  "web_url": "https://gitlab.example.com/services/loom-core/-/pipelines/42"
                },
                "is_projected": true,
                "created_at": "2026-02-25T10:00:00Z",
                "updated_at": "2026-02-25T10:05:00Z"
              }
            ],
            "counts": {
              "pending": 1,
              "in_progress": 0,
              "blocked": 0,
              "completed": 0
            }
          },
          "meta": {
            "request_id": "req_ops_2",
            "timestamp": "2026-02-25T10:05:00Z"
          }
        }
        """

        let envelope = try JSONDecoder().decode(APIEnvelope<MobileTasksResponse>.self, from: Data(json.utf8))
        guard let task = envelope.data?.tasks.first else {
            Issue.record("Expected a task payload")
            return
        }

        #expect(task.sourcePlatform == "claude")
        #expect(task.sourceKind == "projected")
        #expect(task.nativeKey == "current_task:normalize")
        #expect(task.workflowId == "wf-2")
        #expect(task.isProjected == true)
        #expect(task.sourceLabel == "Projected")
        #expect(task.linkageSummary?.contains("Workflow wf-2") == true)
        #expect(task.pipelineRef?.id == 42)
        #expect(task.pipelineRef?.project == "services/loom-core")
    }

    @Test("Task counts default missing keys to zero")
    func taskCountsMissingFieldsDefaultToZero() throws {
        let json = """
        {
          "pending": 4
        }
        """

        let counts = try JSONDecoder().decode(MobileTaskCounts.self, from: Data(json.utf8))
        #expect(counts.pending == 4)
        #expect(counts.inProgress == 0)
        #expect(counts.blocked == 0)
        #expect(counts.completed == 0)
    }

    @Test("Pipelines decode optional stage breakdown and default missing counts")
    func pipelinesDecodeWithStageBreakdown() throws {
        let json = """
        {
          "ok": true,
          "data": {
            "pipelines": [
              {
                "id": 101,
                "project": "services/loom-core",
                "ref": "main",
                "status": "running",
                "source": "push",
                "created_at": "2026-02-25T10:00:00Z",
                "web_url": "https://gitlab.example.com/services/loom-core/-/pipelines/101",
                "current_stage": "build",
                "stages": [
                  {
                    "name": "build",
                    "status": "running",
                    "jobs": [
                      {
                        "id": 1,
                        "name": "build:image",
                        "status": "running",
                        "stage": "build"
                      }
                    ]
                  },
                  {
                    "name": "test",
                    "status": "pending",
                    "jobs": []
                  }
                ],
                "completed_stages": 1,
                "total_stages": 2,
                "failed_job_count": 0,
                "agent_id": "codex-1",
                "agent_type": "codex"
              }
            ],
            "available": true
          },
          "meta": {
            "request_id": "req_ops_3",
            "timestamp": "2026-02-25T10:05:00Z"
          }
        }
        """

        let envelope = try JSONDecoder().decode(APIEnvelope<MobilePipelinesResponse>.self, from: Data(json.utf8))
        guard let pipeline = envelope.data?.pipelines.first else {
            Issue.record("Expected a pipeline payload")
            return
        }

        #expect(pipeline.completedStages == 1)
        #expect(pipeline.totalStages == 2)
        #expect(pipeline.failedJobCount == 0)
        #expect(pipeline.stages?.count == 2)
        #expect(pipeline.stages?.first?.jobs.count == 1)
        #expect(pipeline.agentId == "codex-1")
    }
}

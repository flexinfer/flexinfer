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
}

import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("SessionInfo Decoding")
struct SessionInfoTests {

    @Test("Decodes sessions list response")
    func decodesSessionsList() throws {
        let data = try loadFixture("sessions_response")
        let envelope = try JSONDecoder().decode(APIEnvelope<SessionsResponse>.self, from: data)
        #expect(envelope.ok == true)

        let sessions = try #require(envelope.data?.sessions)
        #expect(sessions.count == 2)

        let active = sessions[0]
        #expect(active.id == "sess_abc123")
        #expect(active.agentId == "claude-code")
        #expect(active.namespace == "loom-core/main")
        #expect(active.status == .active)
        #expect(active.description == "Working on mobile API")
        #expect(active.startedAt == "2026-02-23T10:00:00Z")
        #expect(active.endedAt == nil)
        #expect(active.entryCount == 42)
        #expect(active.totalTokens == 8500)

        let ended = sessions[1]
        #expect(ended.status == .ended)
        #expect(ended.endedAt == "2026-02-23T09:30:00Z")
    }

    @Test("Decodes single session detail response")
    func decodesSessionDetail() throws {
        let data = try loadFixture("session_detail_response")
        let envelope = try JSONDecoder().decode(APIEnvelope<SessionDetailResponse>.self, from: data)
        #expect(envelope.ok == true)

        let session = try #require(envelope.data?.session)
        #expect(session.id == "sess_abc123")
        #expect(session.status == .active)
    }

    @Test("Decodes summarized session status from mobile API")
    func decodesSummarizedSessionStatus() throws {
        let payload = """
        {
          "ok": true,
          "data": {
            "sessions": [
              {
                "id": "sess_summary_1",
                "agent_id": "codex",
                "namespace": "loom-core/main",
                "status": "summarized",
                "description": "Completed run",
                "started_at": "2026-02-25T10:00:00Z",
                "ended_at": "2026-02-25T10:05:00Z",
                "entry_count": 12,
                "total_tokens": 840
              }
            ]
          },
          "meta": {
            "request_id": "req_test",
            "timestamp": "2026-02-25T10:06:00Z"
          }
        }
        """
        let data = Data(payload.utf8)
        let envelope = try JSONDecoder().decode(APIEnvelope<SessionsResponse>.self, from: data)
        let sessions = try #require(envelope.data?.sessions)
        #expect(sessions.count == 1)
        #expect(sessions[0].status == .summarized)
    }

    @Test("Unknown session status decodes as .unknown instead of failing contract")
    func decodesUnknownSessionStatus() throws {
        let payload = """
        {
          "ok": true,
          "data": {
            "session": {
              "id": "sess_weird_1",
              "agent_id": "codex",
              "namespace": "loom-core/main",
              "status": "archived",
              "description": "Unexpected status from upstream",
              "started_at": "2026-02-25T10:00:00Z",
              "ended_at": "2026-02-25T10:05:00Z",
              "entry_count": 12,
              "total_tokens": 840
            }
          },
          "meta": {
            "request_id": "req_test",
            "timestamp": "2026-02-25T10:06:00Z"
          }
        }
        """
        let data = Data(payload.utf8)
        let envelope = try JSONDecoder().decode(APIEnvelope<SessionDetailResponse>.self, from: data)
        let session = try #require(envelope.data?.session)
        #expect(session.status == .unknown)
    }

    @Test("Decodes session tree response")
    func decodesSessionTree() throws {
        let payload = """
        {
          "ok": true,
          "data": {
            "roots": [
              {
                "session": {
                  "id": "sess_root",
                  "agent_id": "codex",
                  "namespace": "loom-core/mobile",
                  "status": "active",
                  "description": "Root",
                  "started_at": "2026-05-11T14:00:00Z",
                  "entry_count": 10,
                  "total_tokens": 1000,
                  "root_session_id": "sess_root"
                },
                "depth": 0,
                "child_count": 1,
                "active_child_count": 1,
                "children": [
                  {
                    "session": {
                      "id": "sess_child",
                      "agent_id": "codex-sub",
                      "namespace": "loom-core/mobile",
                      "status": "active",
                      "description": "Child",
                      "started_at": "2026-05-11T14:01:00Z",
                      "entry_count": 2,
                      "total_tokens": 200,
                      "parent_session_id": "sess_root",
                      "root_session_id": "sess_root"
                    },
                    "depth": 1,
                    "child_count": 0,
                    "active_child_count": 0,
                    "children": []
                  }
                ]
              }
            ],
            "orphans": [],
            "summary": {
              "root_count": 1,
              "active_sessions": 2,
              "orphan_sessions": 0,
              "updated_at": "2026-05-11T14:02:00Z"
            }
          },
          "meta": {
            "request_id": "req_tree",
            "timestamp": "2026-05-11T14:02:00Z"
          }
        }
        """

        let envelope = try JSONDecoder().decode(APIEnvelope<SessionTreeResponse>.self, from: Data(payload.utf8))
        let tree = try #require(envelope.data)
        #expect(tree.roots.count == 1)
        #expect(tree.roots[0].childCount == 1)
        #expect(tree.roots[0].children[0].depth == 1)
        #expect(tree.summary.activeSessions == 2)
    }

    @Test("Decodes session activity response")
    func decodesSessionActivity() throws {
        let payload = """
        {
          "ok": true,
          "data": {
            "session_id": "sess_root",
            "tasks": [
              {
                "id": "task-1",
                "title": "Fix mobile route",
                "status": "blocked",
                "priority": "high",
                "tags": ["mobile"],
                "created_at": "2026-05-11T14:00:00Z",
                "updated_at": "2026-05-11T14:01:00Z"
              }
            ],
            "pipelines": [
              {
                "id": 42,
                "project": "services/loom-core",
                "ref": "codex/mobile",
                "status": "failed",
                "current_stage": "test",
                "failed_job_count": 1,
                "web_url": "https://gitlab.example/pipelines/42"
              }
            ],
            "task_count": 1,
            "pipeline_count": 1
          },
          "meta": {
            "request_id": "req_activity",
            "timestamp": "2026-05-11T14:02:00Z"
          }
        }
        """

        let envelope = try JSONDecoder().decode(APIEnvelope<SessionActivityResponse>.self, from: Data(payload.utf8))
        let activity = try #require(envelope.data)
        #expect(activity.taskCount == 1)
        #expect(activity.pipelineCount == 1)
        #expect(activity.hasFailedPipeline == true)
        #expect(activity.tasks[0].tags == ["mobile"])
    }
}

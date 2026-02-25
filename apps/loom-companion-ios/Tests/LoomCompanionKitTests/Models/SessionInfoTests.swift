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
}

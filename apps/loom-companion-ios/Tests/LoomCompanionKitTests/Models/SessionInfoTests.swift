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
}

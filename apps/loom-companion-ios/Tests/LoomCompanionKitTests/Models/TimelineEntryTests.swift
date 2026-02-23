import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("TimelineEntry Decoding")
struct TimelineEntryTests {

    @Test("Decodes session events response")
    func decodesSessionEvents() throws {
        let data = try loadFixture("session_events_response")
        let envelope = try JSONDecoder().decode(APIEnvelope<SessionEventsResponse>.self, from: data)
        #expect(envelope.ok == true)

        let response = try #require(envelope.data)
        #expect(response.sessionId == "sess_abc123")
        #expect(response.events.count == 2)

        let first = response.events[0]
        #expect(first.eventType == "agent.session.start")
        #expect(first.agentId == "claude-code")
        #expect(first.agentType == "claude-code")
        #expect(first.data?["session_id"]?.stringValue == "sess_abc123")
    }

    @Test("Timeline entry has stable identity")
    func timelineEntryIdentity() {
        let entry = TimelineEntry(
            timestamp: "2026-02-23T10:00:00Z",
            eventType: "agent.session.start",
            agentId: "claude-code",
            agentType: "claude-code",
            data: nil
        )
        #expect(entry.id == "2026-02-23T10:00:00Z-agent.session.start")
    }

    @Test("Decodes entry with optional fields omitted")
    func decodesMinimalEntry() throws {
        let json = """
        {"timestamp":"2026-02-23T10:00:00Z","event_type":"hud.fleet"}
        """
        let entry = try JSONDecoder().decode(TimelineEntry.self, from: Data(json.utf8))
        #expect(entry.eventType == "hud.fleet")
        #expect(entry.agentId == nil)
        #expect(entry.agentType == nil)
        #expect(entry.data == nil)
    }
}

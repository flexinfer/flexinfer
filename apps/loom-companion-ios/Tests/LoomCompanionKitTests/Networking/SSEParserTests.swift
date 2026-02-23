import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("SSE Parser")
struct SSEParserTests {

    @Test("Parses single data event")
    func parsesSingleEvent() {
        let raw = """
        data: {"type":"test","value":1}

        """
        let events = parseSSEBlock(raw)
        #expect(events.count == 1)
        #expect(events[0].type == "message")
        #expect(events[0].data == "{\"type\":\"test\",\"value\":1}")
    }

    @Test("Parses event with type field")
    func parsesEventType() {
        let raw = """
        event: agent.session.start
        data: {"session_id":"sess_001"}

        """
        let events = parseSSEBlock(raw)
        #expect(events.count == 1)
        #expect(events[0].type == "agent.session.start")
    }

    @Test("Parses event with id field")
    func parsesEventId() {
        let raw = """
        id: evt-12345
        event: heartbeat
        data: {"time":"2026-02-23T12:00:00Z"}

        """
        let events = parseSSEBlock(raw)
        #expect(events.count == 1)
        #expect(events[0].id == "evt-12345")
        #expect(events[0].type == "heartbeat")
    }

    @Test("Parses multiple events")
    func parsesMultipleEvents() {
        let raw = """
        event: connected
        data: {"subscriberID":"browser-1"}

        event: heartbeat
        data: {"time":"2026-02-23T12:00:00Z"}

        event: agent.session.start
        data: {"session_id":"sess_001"}

        """
        let events = parseSSEBlock(raw)
        #expect(events.count == 3)
        #expect(events[0].type == "connected")
        #expect(events[1].type == "heartbeat")
        #expect(events[2].type == "agent.session.start")
    }

    @Test("Handles multi-line data")
    func parsesMultiLineData() {
        let raw = """
        data: line1
        data: line2

        """
        let events = parseSSEBlock(raw)
        #expect(events.count == 1)
        #expect(events[0].data == "line1\nline2")
    }

    @Test("Skips comment lines")
    func skipsComments() {
        let raw = """
        : this is a comment
        data: {"value":true}

        """
        let events = parseSSEBlock(raw)
        #expect(events.count == 1)
        #expect(events[0].data == "{\"value\":true}")
    }
}

import Foundation

/// Matches `TimelineEntry` from eventlog.go.
public struct TimelineEntry: Decodable, Identifiable, Sendable {
    public var id: String { "\(timestamp)-\(eventType)" }

    public let timestamp: String
    public let eventType: String
    public let agentId: String?
    public let agentType: String?
    public let data: [String: AnyCodable]?

    enum CodingKeys: String, CodingKey {
        case timestamp
        case eventType = "event_type"
        case agentId = "agent_id"
        case agentType = "agent_type"
        case data
    }

    public init(timestamp: String, eventType: String, agentId: String? = nil, agentType: String? = nil, data: [String: AnyCodable]? = nil) {
        self.timestamp = timestamp
        self.eventType = eventType
        self.agentId = agentId
        self.agentType = agentType
        self.data = data
    }
}

/// Response wrapper for session events endpoint.
public struct SessionEventsResponse: Decodable, Sendable {
    public let sessionId: String
    public let events: [TimelineEntry]

    enum CodingKeys: String, CodingKey {
        case sessionId = "session_id"
        case events
    }
}

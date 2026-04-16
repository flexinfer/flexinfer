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

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.timestamp = try container.decode(String.self, forKey: .timestamp)
        self.eventType = try container.decode(String.self, forKey: .eventType)
        self.agentId = try container.decodeIfPresent(String.self, forKey: .agentId)
        self.agentType = try container.decodeIfPresent(String.self, forKey: .agentType)
        // Go sends json.RawMessage which can be any JSON type (object, array, string, etc.)
        // Gracefully fall back to nil when data is not a dictionary.
        self.data = try? container.decodeIfPresent([String: AnyCodable].self, forKey: .data)
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

    public init(sessionId: String, events: [TimelineEntry]) {
        self.sessionId = sessionId
        self.events = events
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.sessionId = (try? container.decodeIfPresent(String.self, forKey: .sessionId)) ?? ""
        self.events = (try? container.decodeIfPresent([TimelineEntry].self, forKey: .events)) ?? []
    }
}
